package agentcodex

import (
	"context"
	"errors"
	"unicode/utf8"

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
	if err := validateExecutionOptionChange(change); err != nil {
		return s.executionOptionsReceipt(change, executionOptionsNotSent()), err
	}
	s.executionMu.Lock()
	defer s.executionMu.Unlock()
	return s.setExecutionOptionLocked(ctx, change)
}

func (s *session) setExecutionOptionLocked(
	ctx context.Context,
	change agent.ExecutionOptionChange,
) (agent.ExecutionOptionsReceipt, error) {
	if err := s.requireOpen(); err != nil {
		return s.executionOptionsReceipt(change, executionOptionsNotSent()), err
	}
	observedAt := s.executionObservationSequence()
	err := s.applyNativeExecutionOption(ctx, change)
	if err == nil {
		s.recordAcceptedExecutionOption(change, observedAt)
		return s.executionOptionsReceipt(change, executionOptionsAccepted()), nil
	}

	convertedErr := nativeOperationError("set_execution_option", err)
	if agent.CodeOf(convertedErr) == agent.ErrorOutcomeUnknown {
		s.clearUncertainExecutionOptionEffective(change.Option)
	}
	return s.executionOptionsReceipt(
		change,
		executionOptionsAcceptance(err),
	), convertedErr
}

func (s *session) applyNativeExecutionOption(
	ctx context.Context,
	change agent.ExecutionOptionChange,
) error {
	switch change.Option {
	case agent.ExecutionOptionModel:
		_, err := s.native.SetModel(ctx, change.Value)
		return err
	case agent.ExecutionOptionReasoningEffort:
		_, err := s.native.SetThinking(ctx, codex.ThinkingChange{
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

func validateExecutionOptionChange(change agent.ExecutionOptionChange) error {
	switch change.Option {
	case agent.ExecutionOptionModel, agent.ExecutionOptionReasoningEffort:
	default:
		return adapterError(
			agent.ErrorInvalidArgument,
			"execution_option_unknown",
			"execution option is not supported",
			nil,
		)
	}
	if change.Value == "" {
		return adapterError(
			agent.ErrorInvalidArgument,
			"execution_option_empty_value",
			"execution option value must be nonempty",
			nil,
		)
	}
	if !utf8.ValidString(change.Value) {
		return adapterError(
			agent.ErrorInvalidArgument,
			"execution_option_invalid_utf8",
			"execution option value must be valid UTF-8",
			nil,
		)
	}
	return nil
}

func executionOptionsAccepted() agent.Acceptance {
	sent, accepted := true, true
	return agent.Acceptance{
		Sent:           &sent,
		NativeAccepted: &accepted,
	}
}

func executionOptionsNotSent() agent.Acceptance {
	sent := false
	return agent.Acceptance{Sent: &sent}
}

func executionOptionsAcceptance(err error) agent.Acceptance {
	sent := true
	var callErr *codex.CallError
	if errors.As(err, &callErr) && callErr.Dispatch == codex.NotSent {
		sent = false
		return agent.Acceptance{Sent: &sent}
	}
	acceptance := agent.Acceptance{Sent: &sent}
	if callErr != nil && callErr.Dispatch == codex.Responded {
		accepted := false
		acceptance.NativeAccepted = &accepted
	}
	return acceptance
}

func (s *session) executionOptionsReceipt(
	change agent.ExecutionOptionChange,
	acceptance agent.Acceptance,
) agent.ExecutionOptionsReceipt {
	return agent.ExecutionOptionsReceipt{
		Change:     change,
		Acceptance: acceptance,
		Report:     s.EffectiveExecutionOptions(),
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
