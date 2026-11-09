package agentclaude

import (
	"github.com/zoumo/agent-sdk-go/agent"
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
