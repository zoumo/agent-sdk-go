package agentcodex

import (
	"context"
	"errors"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
)

func optionalString(value string) codextypes.Optional[string] {
	if value == "" {
		return codextypes.Optional[string]{}
	}
	return codextypes.Some(value)
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
	switch change.Option {
	case agent.ExecutionOptionModel:
		_, err := r.session.native.SetModel(ctx, change.Value)
		return err
	case agent.ExecutionOptionReasoningEffort:
		_, err := r.session.native.SetThinking(ctx, codex.ThinkingChange{
			Effort: codextypes.Some[codextypes.ReasoningEffort](change.Value),
		})
		return err
	default:
		return &codex.CallError{
			Method:   "thread/settings/update",
			Dispatch: codex.NotSent,
			Cause:    codex.ErrInvalidInput,
		}
	}
}

func (r executionOptionsRuntime) ConvertError(err error) error {
	return nativeOperationError("set_execution_option", err)
}

func (r executionOptionsRuntime) PrepareAccepted(change agent.ExecutionOptionChange) func() {
	observedAt := r.session.executionObservationSequence()
	return func() {
		r.session.recordAcceptedExecutionOption(change, observedAt)
	}
}

func (r executionOptionsRuntime) ClearUncertain(option agent.ExecutionOption) {
	r.session.clearUncertainExecutionOptionEffective(option)
}

func (r executionOptionsRuntime) Acceptance(err error) agent.Acceptance {
	sent := true
	var callErr *codex.CallError
	if !errors.As(err, &callErr) {
		return agent.Acceptance{Sent: &sent}
	}
	switch callErr.Dispatch {
	case codex.NotSent:
		sent = false
		return agent.Acceptance{Sent: &sent}
	case codex.Responded:
		accepted := false
		return agent.Acceptance{Sent: &sent, NativeAccepted: &accepted}
	default:
		return agent.Acceptance{Sent: &sent}
	}
}

func (s *session) recordAcceptedExecutionOption(
	change agent.ExecutionOptionChange,
	observedAt uint64,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	observed := s.executionObservationSeq != observedAt
	switch change.Option {
	case agent.ExecutionOptionModel:
		s.execution.Model.Requested = change.Value
		if !observed {
			s.execution.Model.Effective = nil
			s.execution.ReasoningEffort.Effective = nil
		}
	case agent.ExecutionOptionReasoningEffort:
		s.execution.ReasoningEffort.Requested = change.Value
		if !observed {
			s.execution.ReasoningEffort.Effective = nil
		}
	}
}

func (s *session) executionObservationSequence() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.executionObservationSeq
}

func (s *session) clearUncertainExecutionOptionEffective(option agent.ExecutionOption) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch option {
	case agent.ExecutionOptionModel:
		s.execution.Model.Effective = nil
		s.execution.ReasoningEffort.Effective = nil
	case agent.ExecutionOptionReasoningEffort:
		s.execution.ReasoningEffort.Effective = nil
	}
}

func (s *session) recordObservedExecutionSettings(
	notification *codextypes.ThreadSettingsUpdatedNotification,
) {
	if notification == nil || notification.ThreadID != s.ID() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executionObservationSeq++
	if notification.ThreadSettings.Model == "" {
		s.execution.Model.Effective = nil
	} else {
		model := notification.ThreadSettings.Model
		s.execution.Model.Effective = &model
	}
	if effort, ok := notification.ThreadSettings.Effort.Get(); ok {
		observed := effort
		s.execution.ReasoningEffort.Effective = &observed
	} else {
		s.execution.ReasoningEffort.Effective = nil
	}
}

func initialExecutionOptionsReport(
	native *codex.Session,
	requested agent.ExecutionOptions,
	mode openingMode,
) agent.ExecutionOptionsReport {
	var model string
	var effort codextypes.Optional[codextypes.ReasoningEffort]
	if mode == openingResume {
		if response, ok := native.ResumeResponse(); ok {
			model = response.Model
			effort = response.ReasoningEffort
		}
	} else if response, ok := native.StartResponse(); ok {
		model = response.Model
		effort = response.ReasoningEffort
	}

	var effectiveEffort *string
	if value, ok := effort.Get(); ok {
		observed := value
		effectiveEffort = &observed
	}
	var effectiveModel *string
	if model != "" {
		effectiveModel = &model
	}
	return agent.ExecutionOptionsReport{
		Model: agent.ExecutionValue{
			Requested: requested.Model,
			Effective: effectiveModel,
		},
		ReasoningEffort: agent.ExecutionValue{
			Requested: requested.ReasoningEffort,
			Effective: effectiveEffort,
		},
	}
}

func cloneExecutionOptionsReport(
	report agent.ExecutionOptionsReport,
) agent.ExecutionOptionsReport {
	cloned := report
	cloned.Model.Effective = cloneStringPointer(report.Model.Effective)
	cloned.ReasoningEffort.Effective = cloneStringPointer(report.ReasoningEffort.Effective)
	return cloned
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
