package agentcodex

import (
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
