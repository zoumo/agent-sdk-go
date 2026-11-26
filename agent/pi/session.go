package agentpi

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/eventstream"
	"github.com/zoumo/agent-sdk-go/agent/internal/interactionledger"
	"github.com/zoumo/agent-sdk-go/agent/internal/telemetry"
	"github.com/zoumo/agent-sdk-go/client/pi"
	"github.com/zoumo/agent-sdk-go/internal/uuidv4"
)

var (
	_ agent.Session          = (*session)(nil)
	_ agent.ConfigController = (*session)(nil)
)

type session struct {
	core           *Core
	native         *pi.Session
	stream         *eventstream.Stream
	features       agent.EffectiveFeatures
	shellEnv       bool
	toolApproval   bool
	interactionIDs *interactionledger.Ledger
	interactions   map[agent.InteractionRef]*piInteraction
	mu             sync.Mutex
	closed         bool
	submission     submissionState
	state          agent.SessionState
	// Only the single native event reader accesses messageID and messageCount.
	messageID    string
	messageCount uint64
	stopReason   string
	runError     *agent.ObservedError
}

func newSession(core *Core, native *pi.Session, restricted bool, extension *sessionExtension) *session {
	s := &session{
		core: core, native: native, features: agent.EffectiveFeatures{RestrictLocalTools: restricted},
		shellEnv:     extension != nil && extension.shellEnv,
		toolApproval: extension != nil && extension.toolApproval,
	}
	s.features.AgentQuestions = extension != nil && extension.agentQuestions
	if s.toolApproval || s.features.AgentQuestions {
		s.interactionIDs = interactionledger.New("pi-")
		s.interactions = make(map[agent.InteractionRef]*piInteraction)
	}
	s.stream = eventstream.New(eventstream.Hooks{
		BeginClose:         func(eventstream.End) { s.mu.Lock(); s.closed = true; s.mu.Unlock() },
		CloseEvents:        s.closeInteractionEvents,
		CloseNative:        func() error { return operationError("close", errors.Join(native.Close(), extension.remove())) },
		Release:            func() { core.release(s.ID()); core.leases.SessionClosed(s) },
		PreparePublication: core.telemetry.PreparePublication,
	})
	return s
}

func (s *session) ID() string { return s.native.ID() }
func (s *session) Capabilities() agent.Capabilities {
	return capabilities(s.shellEnv, s.toolApproval, s.features.AgentQuestions)
}
func (s *session) EffectiveFeatures() agent.EffectiveFeatures { return s.features }
func (s *session) Events() (agent.EventStream, error)         { return s.stream.Claim() }
func (s *session) Done() <-chan struct{}                      { return s.stream.Done() }
func (s *session) Err() error                                 { return s.stream.Err() }

func (s *session) Close(ctx context.Context) error {
	ctx, observation := s.core.telemetry.Begin(ctx, telemetry.Close)
	reused, err := s.stream.Close(ctx)
	observation.EndLifecycle(reused, err)
	return err
}

func (s *session) observe() {
	for {
		event, err := s.native.Receive(context.Background())
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
			}
			s.stream.ObservationEnded(operationError("receive", err))
			return
		}
		events, err := s.project(event)
		if err == nil {
			err = s.stream.Publish(events)
		}
		if err != nil {
			s.stream.ObservationEnded(operationError("event_delivery", err))
			return
		}
	}
}

func closedError() error {
	return &agent.Error{Code: agent.ErrorClosed, Reason: agent.ErrorReasonSessionClosed, Message: "Pi session is closed"}
}

func (s *session) StartOrSteer(ctx context.Context, input agent.Input) (receipt agent.InputReceipt, err error) {
	ctx, observation := s.core.telemetry.BeginSubmission(ctx)
	defer func() { observation.End(receipt, err) }()
	message, images, err := promptContent(input)
	if err != nil {
		return receipt, err
	}
	id, err := uuidv4.New()
	if err != nil {
		return receipt, operationError("submission_id", err)
	}
	admitted, err := s.admitPrompt(id)
	if err != nil {
		return receipt, err
	}
	if !admitted {
		return agent.InputReceipt{Result: agent.InputNotApplied, Acceptance: agent.Acceptance{Sent: new(false)}}, nil
	}

	observation.AfterAdmission(ctx)
	// Pi decides atomically whether to start or steer during prompt admission.
	// A separate get_state probe here could race Pi's own scheduler.
	result, sendErr := s.native.Prompt(ctx, message, images, "steer")
	return s.finishPrompt(result.Disposition, sendErr)
}

func (s *session) Interrupt(ctx context.Context, _ agent.InterruptOptions) (receipt agent.InterruptReceipt, err error) {
	ctx, observation := s.core.telemetry.Begin(ctx, telemetry.Interrupt)
	defer func() { observation.End(string(receipt.Result), err) }()
	target, err := s.admitInterrupt()
	if err != nil {
		return receipt, err
	}
	if target == "" {
		return agent.InterruptReceipt{Result: agent.InterruptNoActiveWork}, nil
	}
	defer s.finishInterrupt()
	if err := s.native.Abort(ctx); err != nil {
		return receipt, operationError("interrupt", err)
	}
	return agent.InterruptReceipt{Result: agent.InterruptAcknowledged, Target: target}, nil
}

func (s *session) SetConfig(ctx context.Context, change agent.ConfigChange) (agent.ConfigReceipt, error) {
	receipt := agent.ConfigReceipt{Change: change, Acceptance: agent.Acceptance{Sent: new(false)}}
	if err := change.Validate(); err != nil {
		return receipt, err
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return receipt, closedError()
	}
	var err error
	switch change.ID {
	case agent.ConfigEffort:
		if err := validateThinkingLevel(change.Value); err != nil {
			return receipt, err
		}
		err = s.native.SetThinkingLevel(ctx, change.Value)
	case agent.ConfigModel:
		if s.core.config.Provider == "" {
			return receipt, unsupported("model_provider_required", "Pi model changes require a configured provider")
		}
		err = s.native.SetModel(ctx, s.core.config.Provider, change.Value)
	}
	if err != nil {
		receipt.Acceptance = commandAcceptance(err)
		return receipt, operationError("set_config", err)
	}
	receipt.Acceptance = agent.Acceptance{Sent: new(true), NativeAccepted: new(true)}
	if change.ID == agent.ConfigEffort {
		err = checkThinkingLevel(ctx, s.native, change.Value)
		return receipt, operationError("set_config", err)
	}
	return receipt, nil
}

func checkThinkingLevel(ctx context.Context, native *pi.Session, requested string) error {
	state, err := native.State(ctx)
	if err != nil {
		return err
	}
	if state.ThinkingLevel != requested {
		return unsupported("thinking_level_unsupported", "Pi selected thinking level "+state.ThinkingLevel+" instead of "+requested)
	}
	return nil
}

func validateThinkingLevel(level string) error {
	switch level {
	case "", "off", "minimal", "low", "medium", "high", "xhigh", "max":
		return nil
	default:
		return &agent.Error{Code: agent.ErrorInvalidArgument, Reason: "thinking_level_invalid", Message: "Unknown Pi thinking level"}
	}
}

func commandAcceptance(err error) agent.Acceptance {
	var rejected *pi.CommandError
	var dispatched *pi.DispatchError
	switch {
	case errors.As(err, &rejected):
		return agent.Acceptance{Sent: new(true), NativeAccepted: new(false)}
	case errors.As(err, &dispatched):
		if dispatched.Sent {
			return agent.Acceptance{Sent: new(true)}
		}
		return agent.Acceptance{}
	default:
		return agent.Acceptance{Sent: new(false)}
	}
}

func promptContent(input agent.Input) (string, []pi.Image, error) {
	if err := input.Validate(); err != nil {
		return "", nil, err
	}
	var text string
	var seenText bool
	var images []pi.Image
	for _, b := range input.Content {
		switch {
		case b.Text != nil:
			if seenText || b.Text.Annotations != nil || len(images) != 0 {
				return "", nil, unsupported(agent.ErrorReasonContentBlockUnsupported, "Pi supports one unannotated text block before images")
			}
			text = b.Text.Text
			seenText = true
		case b.Image != nil && b.Image.Annotations == nil && b.Image.URI == nil:
			images = append(images, pi.Image{Type: "image", Data: b.Image.Data, MIMEType: b.Image.MIMEType})
		default:
			return "", nil, unsupported(agent.ErrorReasonContentBlockUnsupported, "Pi accepts text followed by image blocks")
		}
	}
	return text, images, nil
}

func operationError(op string, err error) error {
	if err == nil || agent.CodeOf(err) != "" {
		return err
	}
	code := agent.ErrorUnavailable
	var rejection *pi.CommandError
	var dispatched *pi.DispatchError
	switch {
	case errors.As(err, &rejection):
		code = agent.ErrorNativeRejected
	case errors.As(err, &dispatched):
		code = agent.ErrorOutcomeUnknown
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	}
	return &agent.Error{Code: code, Reason: agent.ErrorReasonForCode(op, code), Message: "Pi " + op + " failed", Cause: err}
}
