package agentclaude

import (
	"context"
	"errors"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/claude"
	claudetypes "github.com/zoumo/agent-sdk-go/claude/types"
)

type executionOptionsState struct {
	modelRequested  string
	modelEffective  string
	effortRequested string
}

func initialExecutionOptionsState(
	init *claudetypes.Event,
	requested agent.ExecutionOptions,
) executionOptionsState {
	state := executionOptionsState{
		modelRequested:  requested.Model,
		effortRequested: requested.ReasoningEffort,
	}
	if init != nil && init.System != nil && init.System.Model != "" {
		state.modelEffective = init.System.Model
	}
	return state
}

func (s executionOptionsState) report() agent.ExecutionOptionsReport {
	var effectiveModel *string
	if s.modelEffective != "" {
		observed := s.modelEffective
		effectiveModel = &observed
	}
	return agent.ExecutionOptionsReport{
		Model: agent.ExecutionValue{
			Requested: s.modelRequested,
			Effective: effectiveModel,
		},
		ReasoningEffort: agent.ExecutionValue{
			Requested: s.effortRequested,
		},
	}
}

func (s *session) SetExecutionOption(
	ctx context.Context,
	change agent.ExecutionOptionChange,
) (agent.ExecutionOptionsReceipt, error) {
	return agent.SetExecutionOption(ctx, change, executionOptionsRuntime{session: s})
}

type executionOptionsRuntime struct{ session *session }

func (r executionOptionsRuntime) Lock()   { r.session.executionMu.Lock() }
func (r executionOptionsRuntime) Unlock() { r.session.executionMu.Unlock() }

func (r executionOptionsRuntime) EffectiveExecutionOptions() agent.ExecutionOptionsReport {
	return r.session.EffectiveExecutionOptions()
}

func (r executionOptionsRuntime) RequireOpen() error { return r.session.requireOpen() }

func (r executionOptionsRuntime) Apply(
	ctx context.Context,
	change agent.ExecutionOptionChange,
) error {
	value := change.Value
	switch change.Option {
	case agent.ExecutionOptionModel:
		_, err := r.session.native.SetModel(ctx, claude.ModelChange{Model: &value})
		return err
	case agent.ExecutionOptionReasoningEffort:
		_, err := r.session.native.SetEffort(ctx, claude.EffortChange{Level: &value})
		return err
	default:
		return &claude.DispatchError{
			Op:    "set_execution_option",
			State: claude.NotSent,
			Err:   claude.ErrInvalidInput,
		}
	}
}

func (r executionOptionsRuntime) ConvertError(err error) error {
	return nativeOperationError("set_execution_option", err)
}

func (r executionOptionsRuntime) PrepareAccepted(change agent.ExecutionOptionChange) func() {
	return func() {
		r.session.recordAcceptedExecutionOption(change)
	}
}

func (r executionOptionsRuntime) ClearUncertain(option agent.ExecutionOption) {
	r.session.clearUncertainExecutionOptionEffective(option)
}

func (r executionOptionsRuntime) Acceptance(err error) agent.Acceptance {
	sent := true
	var dispatch *claude.DispatchError
	if errors.As(err, &dispatch) && dispatch.State == claude.NotSent {
		sent = false
		return agent.Acceptance{Sent: &sent}
	}
	var control *claude.ControlError
	if errors.As(err, &control) {
		accepted := false
		return agent.Acceptance{Sent: &sent, NativeAccepted: &accepted}
	}
	return agent.Acceptance{Sent: &sent}
}

func (s *session) recordAcceptedExecutionOption(change agent.ExecutionOptionChange) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch change.Option {
	case agent.ExecutionOptionModel:
		s.executionOptions.modelRequested = change.Value
		s.executionOptions.modelEffective = ""
	case agent.ExecutionOptionReasoningEffort:
		s.executionOptions.effortRequested = change.Value
	}
}

func (s *session) clearUncertainExecutionOptionEffective(
	option agent.ExecutionOption,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch option {
	case agent.ExecutionOptionModel:
		s.executionOptions.modelEffective = ""
	case agent.ExecutionOptionReasoningEffort:
		// Claude has no independent effort observation to retain.
	}
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

func (s *session) SetModel(
	ctx context.Context,
	model string,
) (agent.ConfigurationReceipt, error) {
	return agent.SetExecutionModel(ctx, model, executionOptionsRuntime{session: s})
}
