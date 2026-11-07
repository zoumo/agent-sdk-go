package agentcodex

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
)

var (
	_ agent.Session     = (*session)(nil)
	_ agent.EventStream = (*eventStream)(nil)
)

type session struct {
	provider *Provider
	native   *codex.Session

	mu            sync.Mutex
	eventsClaimed bool
	closed        bool
	submitting    bool
	activeTurn    string
	lastFinished  string
	state         *agent.SessionState
	eventFailure  error
	done          chan struct{}
	err           error

	closeOnce sync.Once
	closeErr  error
}

func newSession(provider *Provider, native *codex.Session) *session {
	created := &session{
		provider: provider,
		native:   native,
		done:     make(chan struct{}),
	}
	go func() {
		<-native.Done()
		created.terminate(native.Err())
	}()
	return created
}

func (s *session) ID() string { return s.native.ThreadID() }

func (s *session) Capabilities() agent.Capabilities {
	return agent.Capabilities{agent.CapabilityTextInput: true}
}

func (s *session) EffectiveFeatures() agent.EffectiveFeatures {
	return agent.EffectiveFeatures{AgentQuestions: false}
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
	nativeInput, err := textInput(input)
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
	if s.submitting || s.activeTurn != "" {
		s.mu.Unlock()
		return agent.InputReceipt{}, adapterError(
			agent.ErrorFailedPrecondition,
			"work_in_progress",
			"the session already has unsettled work",
			nil,
		)
	}
	s.submitting = true
	s.mu.Unlock()

	submission, submitErr := s.native.Submit(ctx, nativeInput)

	s.mu.Lock()
	s.submitting = false
	if submitErr != nil {
		s.mu.Unlock()
		return agent.InputReceipt{}, nativeOperationError("submit", submitErr)
	}
	turnID := submission.Response.Turn.ID
	if s.lastFinished != turnID {
		s.activeTurn = turnID
	}
	s.mu.Unlock()

	sent, accepted := true, true
	return agent.InputReceipt{
		Submission: turnID,
		Acceptance: agent.Acceptance{
			Sent:           &sent,
			NativeAccepted: &accepted,
		},
	}, nil
}

func (s *session) Answer(
	ctx context.Context,
	interaction agent.InteractionRef,
	answer agent.Answer,
) (agent.InteractionReceipt, error) {
	return agent.InteractionReceipt{}, adapterError(
		agent.ErrorUnsupported,
		"answer_unsupported",
		"answering interactions is not available",
		nil,
	)
}

func (s *session) Interrupt(
	ctx context.Context,
	options agent.InterruptOptions,
) (agent.InterruptReceipt, error) {
	return agent.InterruptReceipt{}, adapterError(
		agent.ErrorUnsupported,
		"interrupt_unsupported",
		"interrupting work is not available",
		nil,
	)
}

func (s *session) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.closeErr = s.native.Close()
		if s.closeErr != nil {
			s.closeErr = nativeOperationError("close", s.closeErr)
		}
		s.terminate(s.native.Err())
		s.provider.sessionClosed(s)
	})
	return s.closeErr
}

func (s *session) Done() <-chan struct{} { return s.done }

func (s *session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *session) terminate(cause error) {
	cause = sessionTerminationError(cause)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil && cause != nil {
		s.err = cause
	}
	select {
	case <-s.done:
		return
	default:
		close(s.done)
	}
}

type eventStream struct {
	session *session
}

func (s *eventStream) Next(ctx context.Context) (agent.Event, error) {
	for {
		if err := s.session.eventStreamError(); err != nil {
			return agent.Event{}, err
		}
		received, err := s.session.native.Receive(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return agent.Event{}, io.EOF
			}
			if errors.Is(err, context.Canceled) ||
				errors.Is(err, context.DeadlineExceeded) {
				return agent.Event{}, err
			}
			return agent.Event{}, s.session.failEventStream(err)
		}
		switch received.Kind {
		case codex.ReceivedEvent:
			event, supported, convertErr := s.session.convertEvent(received)
			if convertErr != nil {
				return agent.Event{}, s.session.failEventStream(convertErr)
			}
			if supported {
				return event, nil
			}
		case codex.ReceivedRequest:
			return agent.Event{}, s.session.failEventStream(adapterError(
				agent.ErrorUnsupported,
				"native_request_unsupported",
				"Codex native requests are not supported by this adapter version",
				nil,
			))
		}
	}
}

func (s *session) eventStreamError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.eventFailure
}

func (s *session) failEventStream(cause error) error {
	cause = sessionTerminationError(cause)
	s.mu.Lock()
	if s.err != nil {
		cause = s.err
	}
	if s.eventFailure == nil {
		s.eventFailure = cause
	}
	cause = s.eventFailure
	s.mu.Unlock()
	s.terminate(cause)
	return cause
}

func textInput(input agent.Input) (codex.Input, error) {
	if err := input.Validate(); err != nil {
		return codex.Input{}, err
	}
	parts := make([]codextypes.UserInput, 0, len(input.Content))
	for _, raw := range input.Content {
		var block agent.TextBlock
		switch typed := raw.(type) {
		case agent.TextBlock:
			block = typed
		case *agent.TextBlock:
			// Input validation above rejects nil text pointers.
			block = *typed
		default:
			return codex.Input{}, adapterError(
				agent.ErrorUnsupported,
				"content_block_unsupported",
				"Codex input supports text blocks only",
				nil,
			)
		}
		if block.Annotations != nil {
			return codex.Input{}, adapterError(
				agent.ErrorUnsupported,
				"text_annotations_unsupported",
				"Codex input does not support text annotations",
				nil,
			)
		}
		parts = append(parts, codextypes.UserInput{
			Text: &codextypes.TextUserInput{Text: block.Text},
		})
	}
	return codex.Input{Input: parts}, nil
}

func sessionTerminationError(cause error) error {
	if cause == nil {
		return nil
	}
	if agent.CodeOf(cause) != "" {
		return cause
	}
	return adapterError(
		agent.ErrorUnavailable,
		"session_terminated",
		"Codex session terminated",
		cause,
	)
}

func nativeOperationError(operation string, err error) error {
	var callErr *codex.CallError
	if errors.As(err, &callErr) && callErr.Dispatch == codex.OutcomeUnknown {
		return adapterError(
			agent.ErrorOutcomeUnknown,
			operation+"_outcome_unknown",
			operation+" result is unknown; do not retry",
			err,
		)
	}
	var rpcErr *codex.RPCError
	if errors.As(err, &rpcErr) {
		return adapterError(
			agent.ErrorNativeRejected,
			operation+"_native_rejected",
			operation+" was rejected by Codex",
			err,
		)
	}
	if errors.Is(err, codex.ErrClosed) {
		return adapterError(agent.ErrorClosed, operation+"_closed", operation+" on closed session", err)
	}
	return adapterError(
		agent.ErrorUnavailable,
		operation+"_failed",
		operation+" failed",
		err,
	)
}
