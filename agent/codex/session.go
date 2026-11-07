package agentcodex

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/queue"
)

var (
	_ agent.Session         = (*session)(nil)
	_ agent.Archiver        = (*session)(nil)
	_ agent.ModelController = (*session)(nil)
	_ agent.EventStream     = (*eventStream)(nil)
)

type session struct {
	provider *Provider
	native   *codex.Session
	features agent.EffectiveFeatures
	events   queue.Queue[agent.Event]

	mu            sync.Mutex
	eventsClaimed bool
	closed        bool
	explicitClose bool
	submitting    bool
	activeTurn    string
	startedTurn   string
	idleTurn      string
	lastFinished  string
	state         *agent.SessionState
	interactions  map[agent.InteractionRef]*pendingInteraction
	requestRefs   map[string]agent.InteractionRef
	done          chan struct{}
	err           error

	parentMu sync.RWMutex
	parents  map[string]string

	observationDone chan struct{}
	closeOnce       sync.Once
	closeErr        error
}

func newSession(
	provider *Provider,
	native *codex.Session,
	features agent.EffectiveFeatures,
) *session {
	created := &session{
		provider:        provider,
		native:          native,
		features:        features,
		interactions:    make(map[agent.InteractionRef]*pendingInteraction),
		requestRefs:     make(map[string]agent.InteractionRef),
		parents:         make(map[string]string),
		done:            make(chan struct{}),
		observationDone: make(chan struct{}),
	}
	go created.observe()
	return created
}

func (s *session) ID() string { return s.native.ThreadID() }

func (s *session) Capabilities() agent.Capabilities {
	return sessionCapabilities(
		providerCapabilities(s.provider.formsSupported),
		s.features,
	)
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
	if turnID == "" {
		s.mu.Unlock()
		return agent.InputReceipt{}, adapterError(
			agent.ErrorOutcomeUnknown,
			"submit_identity_missing",
			"Codex accepted the input without returning a turn identity",
			nil,
		)
	}
	if s.activeTurn == "" && s.lastFinished != turnID {
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
	return s.answerInteraction(ctx, interaction, answer)
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
	target := s.activeTurn
	s.mu.Unlock()
	if target == "" {
		return agent.InterruptReceipt{Result: agent.InterruptNoActiveWork}, nil
	}
	if err := s.native.Interrupt(ctx, codex.InterruptOptions{TurnID: target}); err != nil {
		return agent.InterruptReceipt{}, nativeOperationError("interrupt", err)
	}
	return agent.InterruptReceipt{
		Target: target,
		Result: agent.InterruptAcknowledged,
	}, nil
}

func (s *session) Archive(ctx context.Context) (agent.ActionReceipt, error) {
	if err := s.requireOpen(); err != nil {
		return agent.ActionReceipt{}, err
	}
	if _, err := s.native.Archive(ctx); err != nil {
		return agent.ActionReceipt{}, nativeOperationError("archive", err)
	}
	sent, accepted := true, true
	return agent.ActionReceipt{Acceptance: agent.Acceptance{
		Sent:           &sent,
		NativeAccepted: &accepted,
	}}, nil
}

func (s *session) Unarchive(ctx context.Context) (agent.ActionReceipt, error) {
	if err := s.requireOpen(); err != nil {
		return agent.ActionReceipt{}, err
	}
	if _, err := s.native.Unarchive(ctx); err != nil {
		return agent.ActionReceipt{}, nativeOperationError("unarchive", err)
	}
	sent, accepted := true, true
	return agent.ActionReceipt{Acceptance: agent.Acceptance{
		Sent:           &sent,
		NativeAccepted: &accepted,
	}}, nil
}

func (s *session) SetModel(
	ctx context.Context,
	model string,
) (agent.ConfigurationReceipt, error) {
	if model == "" {
		return agent.ConfigurationReceipt{}, adapterError(
			agent.ErrorInvalidArgument,
			"missing_model",
			"model must be nonempty",
			nil,
		)
	}
	if err := s.requireOpen(); err != nil {
		return agent.ConfigurationReceipt{}, err
	}
	result, err := s.native.SetModel(ctx, model)
	if err != nil {
		return agent.ConfigurationReceipt{}, nativeOperationError("set_model", err)
	}
	sent := true
	accepted := result.NativeAccepted
	return agent.ConfigurationReceipt{
		Model: model,
		Acceptance: agent.Acceptance{
			Sent:           &sent,
			NativeAccepted: &accepted,
		},
	}, nil
}

func (s *session) Close(context.Context) error {
	s.closeOnce.Do(func() {
		invalidated := s.closeForOperations(true)
		s.closeErr = s.native.Close()
		if s.closeErr != nil {
			s.closeErr = nativeOperationError("close", s.closeErr)
		}
		<-s.observationDone
		events := s.closedInteractionEvents(
			invalidated,
			agent.InteractionCancelled,
			"session_closed",
			time.Now().UTC(),
		)
		_ = s.pushEvents(events)
		s.terminate(s.native.Err())
		_ = s.events.Close(sessionTerminationError(s.native.Err()))
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

func (s *session) observe() {
	defer close(s.observationDone)
	for {
		received, err := s.native.Receive(context.Background())
		if err != nil {
			if errors.Is(err, io.EOF) {
				s.finishObservation(nil)
				return
			}
			s.failObservation(sessionTerminationError(err))
			return
		}
		events, convertErr := s.convertReceived(received)
		if convertErr != nil {
			s.failObservation(convertErr)
			return
		}
		if err := s.pushEvents(events); err != nil {
			s.failObservation(adapterError(
				agent.ErrorUnavailable,
				"event_delivery_failed",
				"deliver unified Codex event",
				err,
			))
			return
		}
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

func (s *session) closeForOperations(explicit bool) []*pendingInteraction {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if explicit {
		s.explicitClose = true
	}
	return s.clearInteractionsLocked()
}

func (s *session) finishObservation(cause error) {
	reason := "session_ended"
	if cause != nil {
		cause = sessionTerminationError(cause)
		reason = "session_failed"
	}
	s.endObservation(cause, reason)
}

func (s *session) failObservation(cause error) {
	s.endObservation(sessionTerminationError(cause), "session_failed")
}

func (s *session) endObservation(cause error, reason string) {
	invalidated := s.closeForOperations(false)
	events := s.closedInteractionEvents(
		invalidated,
		agent.InteractionCancelled,
		reason,
		time.Now().UTC(),
	)
	_ = s.pushEvents(events)
	s.terminate(cause)
	s.closeObservedQueue(cause)
}

func (s *session) closeObservedQueue(cause error) {
	s.mu.Lock()
	explicit := s.explicitClose
	s.mu.Unlock()
	if !explicit {
		_ = s.events.Close(cause)
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
	return s.session.events.Pop(ctx)
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
	if err == nil {
		return nil
	}
	if agent.CodeOf(err) != "" {
		return err
	}

	var answerErr *codex.AnswerError
	if errors.As(err, &answerErr) {
		switch answerErr.Dispatch {
		case codex.OutcomeUnknown:
			return adapterError(
				agent.ErrorOutcomeUnknown,
				operation+"_outcome_unknown",
				operation+" result is unknown; do not retry",
				err,
			)
		default:
			if code := nativeCauseCode(answerErr.Cause); code != "" {
				return adapterError(
					code,
					operation+"_"+string(code),
					operation+" failed",
					err,
				)
			}
		}
	}

	var callErr *codex.CallError
	if errors.As(err, &callErr) {
		if callErr.Dispatch == codex.OutcomeUnknown {
			return adapterError(
				agent.ErrorOutcomeUnknown,
				operation+"_outcome_unknown",
				operation+" result is unknown; do not retry",
				err,
			)
		}
		if code := nativeCauseCode(callErr.Cause); code != "" {
			return adapterError(
				code,
				operation+"_"+string(code),
				operation+" failed",
				err,
			)
		}
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
	if code := nativeCauseCode(err); code != "" {
		return adapterError(code, operation+"_"+string(code), operation+" failed", err)
	}
	return adapterError(
		agent.ErrorUnavailable,
		operation+"_failed",
		operation+" failed",
		err,
	)
}

func nativeCauseCode(err error) agent.ErrorCode {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, codex.ErrInvalidInput):
		return agent.ErrorInvalidArgument
	case errors.Is(err, codex.ErrUnsupportedConfiguration):
		return agent.ErrorUnsupported
	case errors.Is(err, codex.ErrRequestAnswered),
		errors.Is(err, codex.ErrRequestResolved),
		errors.Is(err, codex.ErrRequestCanceled),
		errors.Is(err, codex.ErrRequestExpired),
		errors.Is(err, codex.ErrInvalidAnswerHandle):
		return agent.ErrorFailedPrecondition
	case errors.Is(err, codex.ErrClosed):
		return agent.ErrorClosed
	default:
		return ""
	}
}
