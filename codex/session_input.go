package codex

import (
	"encoding/json"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

// Input preserves native turn/start execution options, without a target ThreadID.
type Input struct {
	AdditionalContext          types.Optional[map[string]types.AdditionalContextEntry]
	ApprovalPolicy             types.Optional[types.AskForApproval]
	ApprovalsReviewer          types.Optional[types.ApprovalsReviewer]
	ClientUserMessageID        types.Optional[string]
	CollaborationMode          types.Optional[types.CollaborationMode]
	Cwd                        types.Optional[string]
	CyberAccessProgram         types.Optional[types.CyberAccessProgram]
	DisabledPluginIDs          types.Optional[[]string]
	Effort                     types.Optional[types.ReasoningEffort]
	Environments               types.Optional[[]types.TurnEnvironmentParams]
	Input                      []types.UserInput
	Model                      types.Optional[string]
	MultiAgentMode             types.Optional[types.MultiAgentMode]
	OutputSchema               types.Optional[json.RawMessage]
	ParentTurnID               types.Optional[string]
	Permissions                types.Optional[string]
	Personality                types.Optional[types.Personality]
	ResponsesapiClientMetadata types.Optional[map[string]string]
	RootTurnID                 types.Optional[string]
	RuntimeWorkspaceRoots      types.Optional[[]types.AbsolutePathBuf]
	SandboxPolicy              types.Optional[types.SandboxPolicy]
	ServiceTier                types.Optional[string]
	ServiceTierForTurn         types.Optional[string]
	Summary                    types.Optional[types.ReasoningSummary]
	ToolOutput                 types.Optional[types.TurnToolOutput]
	TurnTrigger                types.Optional[string]
}

func (input Input) native(threadID string) types.TurnStartParams {
	return types.TurnStartParams{ThreadID: threadID,
		AdditionalContext:          input.AdditionalContext,
		ApprovalPolicy:             input.ApprovalPolicy,
		ApprovalsReviewer:          input.ApprovalsReviewer,
		ClientUserMessageID:        input.ClientUserMessageID,
		CollaborationMode:          input.CollaborationMode,
		Cwd:                        input.Cwd,
		CyberAccessProgram:         input.CyberAccessProgram,
		DisabledPluginIDs:          input.DisabledPluginIDs,
		Effort:                     input.Effort,
		Environments:               input.Environments,
		Input:                      input.Input,
		Model:                      input.Model,
		MultiAgentMode:             input.MultiAgentMode,
		OutputSchema:               input.OutputSchema,
		ParentTurnID:               input.ParentTurnID,
		Permissions:                input.Permissions,
		Personality:                input.Personality,
		ResponsesapiClientMetadata: input.ResponsesapiClientMetadata,
		RootTurnID:                 input.RootTurnID,
		RuntimeWorkspaceRoots:      input.RuntimeWorkspaceRoots,
		SandboxPolicy:              input.SandboxPolicy,
		ServiceTier:                input.ServiceTier,
		ServiceTierForTurn:         input.ServiceTierForTurn,
		Summary:                    input.Summary,
		ToolOutput:                 input.ToolOutput,
		TurnTrigger:                input.TurnTrigger,
	}
}
