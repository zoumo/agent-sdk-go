package agentclaude

import (
	"context"
	"errors"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/eventstream"
	"github.com/zoumo/agent-sdk-go/agent/internal/interactionledger"
	"github.com/zoumo/agent-sdk-go/agent/internal/submissiontrace"
	"github.com/zoumo/agent-sdk-go/claude"
	claudetypes "github.com/zoumo/agent-sdk-go/claude/types"
	"github.com/zoumo/agent-sdk-go/internal/queue"
	"github.com/zoumo/agent-sdk-go/internal/uuidv4"
)

var (
	_ agent.Session          = (*session)(nil)
	_ agent.Injector         = (*session)(nil)
	_ agent.ConfigController = (*session)(nil)
)

type session struct {
	provider *Core
	native   *claude.Session
	id       string
	features agent.EffectiveFeatures
	stream   *eventstream.Stream

	mu                sync.Mutex
	closed            bool
	submitting        bool
	injecting         bool
	submitUnknown     bool
	workActive        bool
	activeSubmit      string
	state             *agent.SessionState
	stateVersion      uint64
	interactions      map[agent.InteractionRef]*pendingInteraction
	interactionRefs   map[string]agent.InteractionRef
	interactionLedger *interactionledger.Ledger
	resume            *resumeIdentityGuard
	injectSupported   bool

	lanes      map[string]*claudeLane
	blocks     map[claudeBlockKey]*claudeBlock
	calls      map[string]claudeCall
	usageModel string
}

func newSession(
	provider *Core,
	native *claude.Session,
	init *claudetypes.Event,
	sessionID string,
	features agent.Features,
	buffered []claude.Received,
	resume *resumeIdentityGuard,
) (*session, error) {
	if sessionID == "" {
		return nil, adapterError(
			agent.ErrorUnavailable,
			ErrorReasonInitializeIdentityMissing,
			"Claude session has no native identity",
			nil,
		)
	}
	created := &session{
		provider:          provider,
		native:            native,
		id:                sessionID,
		features:          effectiveFeatures(features),
		interactions:      make(map[agent.InteractionRef]*pendingInteraction),
		interactionRefs:   make(map[string]agent.InteractionRef),
		interactionLedger: interactionledger.New("claude-"),
		resume:            resume,
	}
	var invalidated []*pendingInteraction
	created.stream = eventstream.New(eventstream.Hooks{
		BeginClose: func(eventstream.End) {
			invalidated = created.closeForOperations()
		},
		CloseNative: func() error {
			return nativeOperationError("close", native.Close())
		},
		FinalCause: func() error { return sessionTerminationError(native.Err()) },
		CloseEvents: func(eventstream.End) []agent.Event {
			events, _ := created.closeInteractionEvents(invalidated, time.Now().UTC())
			invalidated = nil
			return events
		},
		Release: func() { provider.sessionClosed(created) },
	})
	for _, received := range buffered {
		events, err := created.convertReceived(received)
		if err != nil {
			return nil, err
		}
		if err := created.pushEvents(events); err != nil {
			return nil, adapterError(
				agent.ErrorUnavailable,
				agent.ErrorReasonEventDeliveryFailed,
				"deliver buffered Claude event",
				err,
			)
		}
	}
	if init != nil && init.System != nil && len(init.System.SlashCommands) != 0 {
		events, err := created.initialCommandsEvent(*init)
		if err != nil {
			return nil, err
		}
		if err := created.pushEvents(events); err != nil {
			return nil, adapterError(
				agent.ErrorUnavailable,
				agent.ErrorReasonEventDeliveryFailed,
				"deliver initial Claude commands",
				err,
			)
		}
	}
	if init != nil && init.System != nil {
		created.injectSupported = supportsInject(init.System.ClaudeCodeVersion)
	}
	return created, nil
}

func (s *session) start() {
	go s.observe()
}

func (s *session) ID() string { return s.id }

func (s *session) Capabilities() agent.Capabilities {
	return sessionCapabilities(providerCapabilities(), s.features)
}

func (s *session) EffectiveFeatures() agent.EffectiveFeatures {
	return s.features
}

func (s *session) Events() (agent.EventStream, error) {
	return s.stream.Claim()
}

func (s *session) StartOrSteer(
	ctx context.Context,
	input agent.Input,
) (receipt agent.InputReceipt, err error) {
	text, err := textInput(input)
	if err != nil {
		return agent.InputReceipt{}, err
	}
	if ctx == nil {
		return agent.InputReceipt{}, nativeOperationError("start_or_steer", queue.ErrNilContext)
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return agent.InputReceipt{}, adapterError(
			agent.ErrorClosed,
			agent.ErrorReasonSessionClosed,
			"session is closed",
			nil,
		)
	}
	if s.submitting || s.injecting || s.submitUnknown {
		s.mu.Unlock()
		return claudeInputNotApplied(), nil
	}
	stateVersion := s.stateVersion
	s.submitting = true
	s.mu.Unlock()

	trace := submissiontrace.Start(ctx, s.provider.config.Tracer)
	defer func() { trace.End(err) }()

	submissionID, err := s.newSubmissionID(stateVersion)
	if err != nil {
		return agent.InputReceipt{}, err
	}

	priority := "next"
	shouldQuery := true
	var carrier claude.TraceContext
	if trace.Carrier != nil {
		carrier = claude.TraceContext{
			Traceparent: trace.Carrier.Traceparent,
			Tracestate:  trace.Carrier.Tracestate,
		}
	}
	ctx = claude.WithTraceContext(ctx, carrier)
	nativeSubmission, submitErr := s.native.Submit(ctx, claude.Input{
		User: &claudetypes.UserInput{
			Message: claudetypes.InputMessage{
				Role:    "user",
				Content: claudetypes.Content{Text: &text},
			},
			Priority:    &priority,
			ShouldQuery: &shouldQuery,
		},
	})
	if submitErr != nil {
		mapped := nativeOperationError("start_or_steer", submitErr)
		s.finishInputWrite(agent.CodeOf(mapped) == agent.ErrorOutcomeUnknown, stateVersion)
		return agent.InputReceipt{}, mapped
	}
	if !nativeSubmission.Written {
		s.finishInputWrite(true, stateVersion)
		return agent.InputReceipt{}, adapterError(
			agent.ErrorOutcomeUnknown,
			ErrorReasonStartOrSteerAcceptanceMissing,
			"Claude start-or-steer returned without write evidence",
			nil,
		)
	}

	s.mu.Lock()
	s.submitting = false
	if s.inputWriteMayBeActiveLocked(stateVersion) {
		if s.activeSubmit == "" {
			s.activeSubmit = submissionID
		}
		s.workActive = true
	}
	s.mu.Unlock()

	sent := true
	return agent.InputReceipt{
		Submission: submissionID,
		Result:     agent.InputAppliedBestEffort,
		Acceptance: agent.Acceptance{Sent: &sent},
	}, nil
}

func claudeInputNotApplied() agent.InputReceipt {
	sent := false
	return agent.InputReceipt{
		Result:     agent.InputNotApplied,
		Acceptance: agent.Acceptance{Sent: &sent},
	}
}

func (s *session) newSubmissionID(stateVersion uint64) (string, error) {
	submissionID, err := uuidv4.New()
	if err != nil {
		s.finishInputWrite(false, stateVersion)
		return "", adapterError(
			agent.ErrorUnavailable,
			ErrorReasonSubmissionIDFailed,
			"create unified submission identity",
			err,
		)
	}
	return submissionID, nil
}

func (s *session) finishInputWrite(outcomeUnknown bool, stateVersion uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.submitting = false
	if outcomeUnknown && s.inputWriteMayBeActiveLocked(stateVersion) {
		s.submitUnknown = true
		s.workActive = true
	}
}

func (s *session) inputWriteMayBeActiveLocked(stateVersion uint64) bool {
	return s.stateVersion == stateVersion || s.state == nil || *s.state != agent.StateIdle
}

func (s *session) Interrupt(
	ctx context.Context,
	_ agent.InterruptOptions,
) (agent.InterruptReceipt, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return agent.InterruptReceipt{}, adapterError(
			agent.ErrorClosed,
			agent.ErrorReasonSessionClosed,
			"session is closed",
			nil,
		)
	}
	target := s.activeSubmit
	s.mu.Unlock()
	if target == "" {
		return agent.InterruptReceipt{Result: agent.InterruptNoActiveWork}, nil
	}
	if err := s.native.Interrupt(ctx, claude.InterruptOptions{}); err != nil {
		return agent.InterruptReceipt{}, nativeOperationError("interrupt", err)
	}
	return agent.InterruptReceipt{
		Target: target,
		Result: agent.InterruptAcknowledged,
	}, nil
}

func (s *session) Close(ctx context.Context) error {
	return s.stream.Close(ctx)
}

func (s *session) Done() <-chan struct{} { return s.stream.Done() }

func (s *session) Err() error {
	return s.stream.Err()
}

func (s *session) closeForOperations() []*pendingInteraction {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	interactions := make([]*pendingInteraction, 0, len(s.interactions))
	for _, interaction := range s.interactions {
		interactions = append(interactions, interaction)
	}
	s.interactions = make(map[agent.InteractionRef]*pendingInteraction)
	s.interactionRefs = make(map[string]agent.InteractionRef)
	sort.Slice(interactions, func(left, right int) bool {
		return interactions[left].sequence < interactions[right].sequence
	})
	return interactions
}

func (s *session) observe() {
	s.stream.ObservationEnded(sessionTerminationError(s.receiveUntilTerminal()))
}

func (s *session) receiveUntilTerminal() error {
	for {
		received, err := s.native.Receive(context.Background())
		if err != nil {
			if errors.Is(err, io.EOF) {
				return s.native.Err()
			}
			if nativeErr := s.native.Err(); nativeErr != nil {
				if decodeErr := claudeDecodeError(nativeErr); decodeErr != nil {
					events, gapErr := s.assistantDecodeGap(decodeErr)
					if gapErr != nil {
						return gapErr
					}
					if pushErr := s.pushEvents(events); pushErr != nil {
						return adapterError(
							agent.ErrorUnavailable,
							agent.ErrorReasonEventDeliveryFailed,
							"deliver Claude decode diagnostic",
							pushErr,
						)
					}
				}
				return nativeErr
			}
			return err
		}
		if received.Event != nil {
			handled, resumeErr := s.resume.inspect(received.Event)
			if resumeErr != nil {
				return resumeErr
			}
			if handled {
				continue
			}
		}
		events, convertErr := s.convertReceived(received)
		if convertErr != nil {
			return convertErr
		}
		if pushErr := s.pushEvents(events); pushErr != nil {
			return adapterError(
				agent.ErrorUnavailable,
				agent.ErrorReasonEventDeliveryFailed,
				"deliver unified Claude event",
				pushErr,
			)
		}
	}
}

func (s *session) pushEvents(events []agent.Event) error {
	return s.stream.Publish(events)
}

func (s *session) updateState(state agent.SessionState) *agent.SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.state
	next := state
	s.state = &next
	s.workActive = next != agent.StateIdle
	if next == agent.StateIdle {
		s.submitUnknown = false
		s.activeSubmit = ""
	}
	s.stateVersion++
	return previous
}

func (s *session) markUnknownState() *agent.SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.state
	s.state = nil
	s.workActive = true
	s.stateVersion++
	return previous
}

func textInput(input agent.Input) (string, error) {
	if err := input.Validate(); err != nil {
		return "", err
	}
	if len(input.Content) != 1 {
		return "", adapterError(
			agent.ErrorUnsupported,
			ErrorReasonMultipleContentBlocksUnsupported,
			"Claude text input supports exactly one content block",
			nil,
		)
	}
	block := input.Content[0]
	if block.Text == nil {
		return "", adapterError(
			agent.ErrorUnsupported,
			agent.ErrorReasonContentBlockUnsupported,
			"Claude input supports text blocks only",
			nil,
		)
	}
	if block.Text.Annotations != nil {
		return "", adapterError(
			agent.ErrorUnsupported,
			agent.ErrorReasonTextAnnotationsUnsupported,
			"Claude input does not support text annotations",
			nil,
		)
	}
	return block.Text.Text, nil
}

func effectiveFeatures(features agent.Features) agent.EffectiveFeatures {
	return agent.EffectiveFeatures{
		AgentQuestions: features.AgentQuestionsEnabled(),
	}
}
