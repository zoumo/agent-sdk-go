package agentclaude

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/internal/queue"
	"github.com/zoumo/agent-sdk-go/internal/uuidv4"
)

var (
	_ agent.Session     = (*session)(nil)
	_ agent.EventStream = (*eventStream)(nil)
)

type session struct {
	provider *Provider
	native   *claude.Session
	id       string
	features agent.EffectiveFeatures
	events   queue.Queue[agent.Event]

	mu            sync.Mutex
	eventsClaimed bool
	closed        bool
	submitting    bool
	workActive    bool
	state         *agent.SessionState
	stateVersion  uint64
	done          chan struct{}
	doneClosed    bool
	err           error

	observationDone chan struct{}
	closeOnce       sync.Once
	closeErr        error
}

func newSession(
	provider *Provider,
	native *claude.Session,
	sessionID string,
	features agent.Features,
	buffered []claude.Received,
) (*session, error) {
	created := &session{
		provider:        provider,
		native:          native,
		id:              sessionID,
		features:        effectiveFeatures(features),
		done:            make(chan struct{}),
		observationDone: make(chan struct{}),
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
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

func (s *session) Submit(
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
	if s.submitting || s.workActive {
		s.mu.Unlock()
		return agent.InputReceipt{}, adapterError(
			agent.ErrorFailedPrecondition,
			"work_in_progress",
			"the session already has unsettled work",
			nil,
		)
	}
	s.submitting = true
	stateVersion := s.stateVersion
	s.mu.Unlock()

	submissionID, idErr := uuidv4.New()
	if idErr != nil {
		s.finishSubmit(false, stateVersion)
		return agent.InputReceipt{}, adapterError(
			agent.ErrorUnavailable,
			"submission_id_failed",
			"create unified submission identity",
			idErr,
		)
	}

	nativeSubmission, submitErr := s.native.Submit(ctx, claude.Input{Text: text})
	if submitErr != nil {
		mapped := nativeOperationError("submit", submitErr)
		s.finishSubmit(agent.CodeOf(mapped) == agent.ErrorOutcomeUnknown, stateVersion)
		return agent.InputReceipt{}, mapped
	}
	if !nativeSubmission.Written {
		s.finishSubmit(true, stateVersion)
		return agent.InputReceipt{}, adapterError(
			agent.ErrorOutcomeUnknown,
			"submit_acceptance_missing",
			"Claude submit returned without write evidence",
			nil,
		)
	}

	s.mu.Lock()
	s.submitting = false
	if s.stateVersion == stateVersion {
		s.workActive = true
	}
	s.mu.Unlock()

	sent := true
	return agent.InputReceipt{
		Submission: submissionID,
		Acceptance: agent.Acceptance{Sent: &sent},
	}, nil
}

func (s *session) finishSubmit(outcomeUnknown bool, stateVersion uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.submitting = false
	if outcomeUnknown && s.stateVersion == stateVersion {
		s.workActive = true
	}
}

func (s *session) Answer(
	context.Context,
	agent.InteractionRef,
	agent.Answer,
) (agent.InteractionReceipt, error) {
	if err := s.requireOpen(); err != nil {
		return agent.InteractionReceipt{}, err
	}
	return agent.InteractionReceipt{}, adapterError(
		agent.ErrorUnsupported,
		"answer_unsupported",
		"Claude interactions are not supported by this adapter",
		nil,
	)
}

func (s *session) Interrupt(
	context.Context,
	agent.InterruptOptions,
) (agent.InterruptReceipt, error) {
	if err := s.requireOpen(); err != nil {
		return agent.InterruptReceipt{}, err
	}
	return agent.InterruptReceipt{}, adapterError(
		agent.ErrorUnsupported,
		"interrupt_unsupported",
		"Claude interrupts are not supported by this adapter",
		nil,
	)
}

func (s *session) Close(context.Context) error {
	s.closeOnce.Do(func() {
		s.closeForOperations()
		s.closeErr = s.native.Close()
		if s.closeErr != nil {
			s.closeErr = nativeOperationError("close", s.closeErr)
		}
		<-s.observationDone
		s.complete(s.native.Err())
	})
	return s.closeErr
}

func (s *session) Done() <-chan struct{} { return s.done }

func (s *session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *session) requireOpen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return adapterError(
			agent.ErrorClosed,
			"session_closed",
			"session is closed",
			nil,
		)
	}
	return nil
}

func (s *session) closeForOperations() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func (s *session) observe() {
	defer close(s.observationDone)
	cause := s.receiveUntilTerminal()
	if cause != nil {
		_ = s.native.Close()
	}
	s.complete(cause)
}

func (s *session) receiveUntilTerminal() error {
	for {
		received, err := s.native.Receive(context.Background())
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			if nativeErr := s.native.Err(); nativeErr != nil {
				return nativeErr
			}
			return err
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

func (s *session) complete(cause error) {
	cause = sessionTerminationError(cause)
	s.mu.Lock()
	s.closed = true
	if s.err == nil && cause != nil {
		s.err = cause
	}
	if !s.doneClosed {
		s.doneClosed = true
		close(s.done)
	}
	s.mu.Unlock()
	_ = s.events.Close(cause)
	s.provider.sessionClosed(s)
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
	var block agent.TextBlock
	switch typed := input.Content[0].(type) {
	case agent.TextBlock:
		block = typed
	case *agent.TextBlock:
		block = *typed
	default:
		return "", adapterError(
			agent.ErrorUnsupported,
			"content_block_unsupported",
			"Claude input supports text blocks only",
			nil,
		)
	}
	if block.Annotations != nil {
		return "", adapterError(
			agent.ErrorUnsupported,
			"text_annotations_unsupported",
			"Claude input does not support text annotations",
			nil,
		)
	}
	return block.Text, nil
}

func effectiveFeatures(agent.Features) agent.EffectiveFeatures {
	return agent.EffectiveFeatures{AgentQuestions: false}
}
