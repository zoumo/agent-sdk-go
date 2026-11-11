package agentcodex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/eventstream"
	"github.com/zoumo/agent-sdk-go/agent/internal/interactionledger"
	"github.com/zoumo/agent-sdk-go/agent/internal/submissiontrace"
	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
)

var (
	_ agent.Session          = (*session)(nil)
	_ agent.Archiver         = (*session)(nil)
	_ agent.ConfigController = (*session)(nil)
)

type session struct {
	provider *Core
	native   *codex.Session
	features agent.EffectiveFeatures
	stream   *eventstream.Stream

	mu            sync.Mutex
	closed        bool
	submitting    bool
	submitUnknown bool
	// workEpoch prevents a late submit failure from rearming unknown state after
	// the observer has already accepted native turn evidence.
	workEpoch    uint64
	activeTurn   string
	startedTurn  string
	idleTurn     string
	lastFinished string
	state        *agent.SessionState

	interactions      map[agent.InteractionRef]*pendingInteraction
	requestRefs       map[string]agent.InteractionRef
	interactionLedger *interactionledger.Ledger

	parentMu sync.RWMutex
	parents  map[string]string

	toolIdentities map[toolIdentityKey]toolIdentity
}

func newSession(
	provider *Core,
	native *codex.Session,
	features agent.EffectiveFeatures,
) *session {
	created := &session{
		provider:          provider,
		native:            native,
		features:          features,
		interactions:      make(map[agent.InteractionRef]*pendingInteraction),
		requestRefs:       make(map[string]agent.InteractionRef),
		interactionLedger: interactionledger.New("codex-"),
		parents:           make(map[string]string),
		toolIdentities:    make(map[toolIdentityKey]toolIdentity),
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
		CloseEvents: func(end eventstream.End) []agent.Event {
			created.toolIdentities = nil
			reason := "session_ended"
			if end.Explicit {
				reason = agent.ErrorReasonSessionClosed
			} else if end.Cause != nil {
				reason = "session_failed"
			}
			events := created.closedInteractionEvents(
				invalidated,
				agent.InteractionCancelled,
				reason,
				time.Now().UTC(),
			)
			invalidated = nil
			return events
		},
		Release: func() { provider.sessionClosed(created) },
	})
	return created
}

func (s *session) start() {
	go s.observe()
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
	return s.stream.Claim()
}

func (s *session) StartOrSteer(
	ctx context.Context,
	input agent.Input,
) (receipt agent.InputReceipt, err error) {
	nativeInput, err := textInput(input)
	if err != nil {
		return agent.InputReceipt{}, err
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
	if s.submitting || s.submitUnknown {
		s.mu.Unlock()
		return codexInputNotApplied(false), nil
	}
	activeBefore := s.activeTurn
	s.submitting = true
	submitEpoch := s.workEpoch
	s.mu.Unlock()

	trace := submissiontrace.Start(ctx, s.provider.tracer)
	defer func() { trace.End(err) }()

	if ctx != nil {
		carrier := codex.TraceContext{}
		if trace.Carrier != nil {
			carrier.Traceparent = codextypes.Some(trace.Carrier.Traceparent)
			if trace.Carrier.Tracestate != "" {
				carrier.Tracestate = codextypes.Some(trace.Carrier.Tracestate)
			}
		}
		ctx = codex.WithTraceContext(ctx, carrier)
	}
	submission, submitErr := s.native.Submit(ctx, nativeInput)

	s.mu.Lock()
	s.submitting = false
	if submitErr != nil {
		notApplied := codexTurnInputNotApplied(submitErr)
		s.recordSubmitErrorLocked(submitErr, submitEpoch)
		s.mu.Unlock()
		if notApplied {
			return codexInputNotApplied(true), nil
		}
		return agent.InputReceipt{}, nativeOperationError("start_or_steer", submitErr)
	}
	turnID := submission.Response.Turn.ID
	if turnID == "" {
		if s.workEpoch == submitEpoch {
			s.submitUnknown = true
		}
		s.mu.Unlock()
		return agent.InputReceipt{}, adapterError(
			agent.ErrorOutcomeUnknown,
			ErrorReasonStartOrSteerIdentityMissing,
			"Codex accepted the input without returning a turn identity",
			nil,
		)
	}
	result := agent.InputAppliedNewWork
	if activeBefore != "" && activeBefore == turnID {
		result = agent.InputAppliedCurrentWork
	}
	if s.lastFinished != turnID {
		s.activeTurn = turnID
	}
	s.mu.Unlock()

	sent, accepted := true, true
	return agent.InputReceipt{
		Submission: turnID,
		Result:     result,
		Acceptance: agent.Acceptance{
			Sent:           &sent,
			NativeAccepted: &accepted,
		},
	}, nil
}

func codexInputNotApplied(sent bool) agent.InputReceipt {
	receipt := agent.InputReceipt{
		Result: agent.InputNotApplied,
		Acceptance: agent.Acceptance{
			Sent: &sent,
		},
	}
	if sent {
		accepted := false
		receipt.Acceptance.NativeAccepted = &accepted
	}
	return receipt
}

func codexTurnInputNotApplied(err error) bool {
	var callErr *codex.CallError
	if !errors.As(err, &callErr) || callErr.Dispatch != codex.Responded {
		return false
	}
	var rpcErr *codex.RPCError
	if !errors.As(err, &rpcErr) {
		return false
	}
	if len(rpcErr.Data) != 0 {
		var turnErr codextypes.TurnError
		if json.Unmarshal(rpcErr.Data, &turnErr) == nil {
			if info, ok := turnErr.CodexErrorInfo.Get(); ok &&
				info.ActiveTurnNotSteerable != nil {
				return true
			}
		}
	}
	for _, marker := range []string{
		"no active turn to steer",
		"expected active turn id ",
		"cannot steer a review turn",
		"cannot steer a compact turn",
		"active turn uses a different output schema",
		"failed to submit turn input: NoActiveTurn",
		"failed to submit turn input: NotIdle",
		"failed to submit turn input: ActiveTurnNotSteerable",
		"failed to submit turn input: ExpectedTurnMismatch",
		"failed to submit turn input: ActiveTurnOutputSchemaMismatch",
		"failed to submit turn input: PendingTriggerTurn",
		"failed to submit turn input: PlanMode",
		"failed to submit turn input: Superseded",
	} {
		if strings.Contains(rpcErr.Message, marker) {
			return true
		}
	}
	return false
}

func (s *session) recordSubmitErrorLocked(err error, epoch uint64) {
	if submitOutcomeUnknown(err) && s.workEpoch == epoch {
		s.submitUnknown = true
	}
}

func (s *session) noteWorkEvidenceLocked() {
	s.workEpoch++
	s.submitUnknown = false
}

func submitOutcomeUnknown(err error) bool {
	if err == nil || errors.Is(err, codex.ErrInvalidInput) {
		return false
	}
	var callErr *codex.CallError
	if errors.As(err, &callErr) {
		return callErr.Dispatch == codex.OutcomeUnknown
	}
	return true
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
			agent.ErrorReasonSessionClosed,
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

func (s *session) Close(ctx context.Context) error {
	return s.stream.Close(ctx)
}

func (s *session) Done() <-chan struct{} { return s.stream.Done() }

func (s *session) Err() error {
	return s.stream.Err()
}

func (s *session) requireOpen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return adapterError(
			agent.ErrorClosed,
			agent.ErrorReasonSessionClosed,
			"session is closed",
			nil,
		)
	}
	return nil
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
			return err
		}
		events, convertErr := s.convertReceived(received)
		if convertErr != nil {
			return convertErr
		}
		if err := s.pushEvents(events); err != nil {
			return adapterError(
				agent.ErrorUnavailable,
				agent.ErrorReasonEventDeliveryFailed,
				"deliver unified Codex event",
				err,
			)
		}
	}
}

func (s *session) pushEvents(events []agent.Event) error {
	return s.stream.Publish(events)
}

func (s *session) closeForOperations() []*pendingInteraction {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return s.clearInteractionsLocked()
}

func textInput(input agent.Input) (codex.Input, error) {
	if err := input.Validate(); err != nil {
		return codex.Input{}, err
	}
	parts := make([]codextypes.UserInput, 0, len(input.Content))
	for _, block := range input.Content {
		if block.Text == nil {
			return codex.Input{}, adapterError(
				agent.ErrorUnsupported,
				agent.ErrorReasonContentBlockUnsupported,
				"Codex input supports text blocks only",
				nil,
			)
		}
		if block.Text.Annotations != nil {
			return codex.Input{}, adapterError(
				agent.ErrorUnsupported,
				agent.ErrorReasonTextAnnotationsUnsupported,
				"Codex input does not support text annotations",
				nil,
			)
		}
		parts = append(parts, codextypes.UserInput{
			Text: &codextypes.TextUserInput{Text: block.Text.Text},
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
		agent.ErrorReasonSessionTerminated,
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
				agent.ErrorReasonForCode(operation, agent.ErrorOutcomeUnknown),
				operation+" result is unknown; do not retry",
				err,
			)
		default:
			if code := nativeCauseCode(answerErr.Cause); code != "" {
				return adapterError(
					code,
					agent.ErrorReasonForCode(operation, code),
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
				agent.ErrorReasonForCode(operation, agent.ErrorOutcomeUnknown),
				operation+" result is unknown; do not retry",
				err,
			)
		}
		if code := nativeCauseCode(callErr.Cause); code != "" {
			return adapterError(
				code,
				agent.ErrorReasonForCode(operation, code),
				operation+" failed",
				err,
			)
		}
	}

	var rpcErr *codex.RPCError
	if errors.As(err, &rpcErr) {
		return adapterError(
			agent.ErrorNativeRejected,
			agent.ErrorReasonForCode(operation, agent.ErrorNativeRejected),
			operation+" was rejected by Codex",
			err,
		)
	}
	if code := nativeCauseCode(err); code != "" {
		return adapterError(code, agent.ErrorReasonForCode(operation, code), operation+" failed", err)
	}
	return adapterError(
		agent.ErrorUnavailable,
		agent.ErrorReasonForOperation(operation, agent.ErrorReasonSuffixFailed),
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
