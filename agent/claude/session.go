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
	"github.com/zoumo/agent-sdk-go/agent/internal/telemetry"
	"github.com/zoumo/agent-sdk-go/claude"
	claudetypes "github.com/zoumo/agent-sdk-go/claude/types"
	"github.com/zoumo/agent-sdk-go/internal/queue"
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
	completedURLs     map[elicitationKey]struct{}
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
		Release:   func() { provider.sessionClosed(created) },
		Published: provider.telemetry.RecordPublished,
	})
	// Finish conversion before starting a queue the caller must drain.
	var staged []agent.Event
	for _, received := range buffered {
		events, err := created.convertReceived(received)
		if err != nil {
			return nil, err
		}
		staged = append(staged, events...)
	}
	if init != nil && init.System != nil && len(init.System.SlashCommands) != 0 {
		events, err := created.initialCommandsEvent(*init)
		if err != nil {
			return nil, err
		}
		staged = append(staged, events...)
	}
	if init != nil && init.System != nil {
		created.injectSupported = supportsInject(init.System.ClaudeCodeVersion)
	}
	if err := created.pushEvents(staged); err != nil {
		return nil, adapterError(
			agent.ErrorUnavailable,
			agent.ErrorReasonEventDeliveryFailed,
			"deliver staged Claude events",
			err,
		)
	}
	return created, nil
}

func (s *session) start() {
	go s.observe()
}

func (s *session) ID() string { return s.id }

func (s *session) Capabilities() agent.Capabilities {
	return providerCapabilities()
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
	ctx, observation := s.provider.telemetry.BeginSubmission(ctx, s.provider.config.Tracer != nil)
	defer func() { observation.End(string(receipt.Result), err) }()
	content, err := promptContent(input)
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

	var traceCarrier *agent.TraceContext
	if s.provider.config.Tracer != nil {
		submission := submissiontrace.Start(ctx, s.provider.config.Tracer)
		defer func() { submission.End(err) }()
		traceCarrier = submission.Carrier
	} else {
		traceCarrier = observation.SubmissionCarrier()
	}

	submissionID, err := s.newSubmissionID(stateVersion)
	if err != nil {
		return agent.InputReceipt{}, err
	}

	priority := "next"
	shouldQuery := true
	var carrier claude.TraceContext
	if traceCarrier != nil {
		carrier = claude.TraceContext{
			Traceparent: traceCarrier.Traceparent,
			Tracestate:  traceCarrier.Tracestate,
		}
	}
	ctx = claude.WithTraceContext(ctx, carrier)
	nativeSubmission, submitErr := s.native.Submit(ctx, claude.Input{
		User: &claudetypes.UserInput{
			Message: claudetypes.InputMessage{
				Role:    "user",
				Content: content,
			},
			UUID:        &submissionID,
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
	submissionID, err := s.provider.newSubmissionID()
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
) (receipt agent.InterruptReceipt, err error) {
	ctx, observation := s.provider.telemetry.Begin(ctx, telemetry.Interrupt)
	defer func() { observation.End(string(receipt.Result), err) }()
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
	ctx, observation := s.provider.telemetry.Begin(ctx, telemetry.Close)
	reused, err := s.stream.Close(ctx)
	observation.EndLifecycle(reused, err)
	return err
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

func effectiveFeatures(features agent.Features) agent.EffectiveFeatures {
	return agent.EffectiveFeatures{
		AgentQuestions:     features.AgentQuestionsEnabled(),
		RestrictLocalTools: features.RestrictLocalTools,
	}
}
