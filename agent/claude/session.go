package agentclaude

import (
	"context"
	"errors"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/claude"
	claudetypes "github.com/zoumo/agent-sdk-go/claude/types"
	"github.com/zoumo/agent-sdk-go/internal/queue"
	"github.com/zoumo/agent-sdk-go/internal/uuidv4"
)

var (
	_ agent.Session                    = (*session)(nil)
	_ agent.ModelController            = (*session)(nil)
	_ agent.EventStream                = (*eventStream)(nil)
	_ agent.ExecutionOptionsReporter   = (*session)(nil)
	_ agent.ExecutionOptionsController = (*session)(nil)
)

type session struct {
	provider *Provider
	native   *claude.Session
	id       string
	features agent.EffectiveFeatures
	events   queue.Queue[agent.Event]

	executionMu         sync.Mutex
	mu                  sync.Mutex
	eventsClaimed       bool
	closed              bool
	explicitClose       bool
	providerReleased    bool
	submitting          bool
	submitUnknown       bool
	workActive          bool
	activeSubmit        string
	state               *agent.SessionState
	stateVersion        uint64
	executionOptions    executionOptionsState
	interactions        map[agent.InteractionRef]*pendingInteraction
	interactionRefs     map[string]agent.InteractionRef
	interactionSequence uint64
	done                chan struct{}
	doneClosed          bool
	err                 error
	resume              *resumeIdentityGuard

	lanes  map[string]*claudeLane
	blocks map[claudeBlockKey]*claudeBlock
	calls  map[string]claudeCall
	usage  claudeUsageState

	observationDone chan struct{}
	closeOnce       sync.Once
	closeErr        error

	nativeCleanupOnce sync.Once
	nativeCleanupErr  error
}

func newSession(
	provider *Provider,
	native *claude.Session,
	init *claudetypes.Event,
	sessionID string,
	features agent.Features,
	executionOptions executionOptionsState,
	buffered []claude.Received,
	resume *resumeIdentityGuard,
) (*session, error) {
	created := &session{
		provider:         provider,
		native:           native,
		id:               sessionID,
		features:         effectiveFeatures(features),
		executionOptions: executionOptions,
		interactions:     make(map[agent.InteractionRef]*pendingInteraction),
		interactionRefs:  make(map[string]agent.InteractionRef),
		done:             make(chan struct{}),
		observationDone:  make(chan struct{}),
		resume:           resume,
	}
	for _, received := range buffered {
		events, err := created.convertReceived(received)
		if err != nil {
			return nil, err
		}
		if err := created.pushEvents(events); err != nil {
			return nil, adapterError(
				agent.ErrorUnavailable,
				"event_delivery_failed",
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
				"event_delivery_failed",
				"deliver initial Claude commands",
				err,
			)
		}
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

func (s *session) EffectiveExecutionOptions() agent.ExecutionOptionsReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.executionOptions.report()
}

func (s *session) Events() (agent.EventStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Observation may end before the consumer claims the stream. Keep the
	// accepted events and terminal cause claimable unless Close was explicit.
	if s.closed && s.explicitClose {
		return nil, adapterError(
			agent.ErrorClosed,
			"session_closed",
			"session is closed",
			nil,
		)
	}
	if s.eventsClaimed {
		return nil, adapterError(
			agent.ErrorFailedPrecondition,
			"events_already_claimed",
			"the session event stream has already been claimed",
			nil,
		)
	}
	s.eventsClaimed = true
	return &eventStream{session: s}, nil
}

func (s *session) StartOrSteer(
	ctx context.Context,
	input agent.Input,
) (agent.InputReceipt, error) {
	text, err := textInput(input)
	if err != nil {
		return agent.InputReceipt{}, err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return agent.InputReceipt{}, adapterError(
			agent.ErrorClosed,
			"session_closed",
			"session is closed",
			nil,
		)
	}
	if s.submitting || s.submitUnknown {
		s.mu.Unlock()
		return claudeInputNotApplied(), nil
	}
	stateVersion := s.stateVersion
	s.submitting = true
	s.mu.Unlock()

	submissionID, err := s.newSubmissionID(stateVersion)
	if err != nil {
		return agent.InputReceipt{}, err
	}

	priority := "next"
	shouldQuery := true
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
			"start_or_steer_acceptance_missing",
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
			"submission_id_failed",
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
			"session_closed",
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

func (s *session) Close(context.Context) error {
	s.closeOnce.Do(func() {
		invalidated := s.closeForOperations(true)
		s.closeErr = s.cleanupNative()
		<-s.observationDone
		events, _ := s.closeInteractionEvents(invalidated, time.Now().UTC())
		_ = s.pushEvents(events)
		cause := sessionTerminationError(s.native.Err())
		s.terminate(cause)
		_ = s.events.Close(cause)
		s.releaseProviderSession(false)
	})
	return s.closeErr
}

func (s *session) Done() <-chan struct{} { return s.done }

func (s *session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *session) closeForOperations(explicit bool) []*pendingInteraction {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if explicit {
		s.explicitClose = true
	}
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
	defer close(s.observationDone)
	cause := s.receiveUntilTerminal()
	invalidated := s.closeForOperations(false)
	events, _ := s.closeInteractionEvents(invalidated, time.Now().UTC())
	_ = s.pushEvents(events)
	_ = s.cleanupNative()
	s.mu.Lock()
	explicit := s.explicitClose
	s.mu.Unlock()
	if !explicit {
		_ = s.events.Close(sessionTerminationError(cause))
	}
	s.releaseProviderSession(true)
	s.terminate(cause)
}

func (s *session) cleanupNative() error {
	s.nativeCleanupOnce.Do(func() {
		if err := s.native.Close(); err != nil {
			s.nativeCleanupErr = nativeOperationError("close", err)
		}
	})
	return s.nativeCleanupErr
}

func (s *session) releaseProviderSession(requireImplicit bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.providerReleased || (requireImplicit && s.explicitClose) {
		return
	}
	s.providerReleased = true
	s.provider.sessionClosed(s)
}

func (s *session) receiveUntilTerminal() error {
	for {
		received, err := s.native.Receive(context.Background())
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
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
							"event_delivery_failed",
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
				"event_delivery_failed",
				"deliver unified Claude event",
				pushErr,
			)
		}
	}
}

func (s *session) terminate(cause error) {
	cause = sessionTerminationError(cause)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.err == nil && cause != nil {
		s.err = cause
	}
	if !s.doneClosed {
		s.doneClosed = true
		close(s.done)
	}
}

func (s *session) pushEvents(events []agent.Event) error {
	if len(events) == 0 {
		return nil
	}
	items := make([]queue.Item[agent.Event], 0, len(events))
	for _, event := range events {
		items = append(items, queue.Item[agent.Event]{Value: event, Bytes: 1})
	}
	return s.events.PushBatch(items)
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

type eventStream struct {
	session *session
}

func (s *eventStream) Next(ctx context.Context) (agent.Event, error) {
	return s.session.events.Pop(ctx)
}

func textInput(input agent.Input) (string, error) {
	if err := input.Validate(); err != nil {
		return "", err
	}
	if len(input.Content) != 1 {
		return "", adapterError(
			agent.ErrorUnsupported,
			"multiple_content_blocks_unsupported",
			"Claude text input supports exactly one content block",
			nil,
		)
	}
	block := input.Content[0]
	if block.Text == nil {
		return "", adapterError(
			agent.ErrorUnsupported,
			"content_block_unsupported",
			"Claude input supports text blocks only",
			nil,
		)
	}
	if block.Text.Annotations != nil {
		return "", adapterError(
			agent.ErrorUnsupported,
			"text_annotations_unsupported",
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
