package codex

import (
	"context"
	"encoding/json"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

// ApprovalOptions selects at most one response matching the original method.
// Empty options select accept/decline (approved/denied for legacy requests).
// Permissions approval requires an explicit native grant; denial grants nothing.
// Native response fields, scope and policy amendments are emitted unchanged.
type ApprovalOptions struct {
	CommandExecution *types.CommandExecutionRequestApprovalResponse
	FileChange       *types.FileChangeRequestApprovalResponse
	Permissions      *types.PermissionsRequestApprovalResponse
	ApplyPatch       *types.ApplyPatchApprovalResponse
	ExecCommand      *types.ExecCommandApprovalResponse
}

func (r IncomingRequest) Approve(ctx context.Context, options ApprovalOptions) error {
	return r.approval(ctx, options, true)
}
func (r IncomingRequest) Deny(ctx context.Context, options ApprovalOptions) error {
	return r.approval(ctx, options, false)
}

// Answer responds only to a live native requestUserInput, retaining question IDs,
// answer arrays and native extensions. It never submits an ordinary turn input.
func (r IncomingRequest) Answer(ctx context.Context, answer types.ToolRequestUserInputResponse) error {
	if r.Method() != "item/tool/requestUserInput" {
		return r.Handle().answerError(NotSent, ErrInvalidInput)
	}
	if answer.Answers == nil {
		return r.Handle().answerError(NotSent, ErrInvalidInput)
	}
	return r.Handle().Answer(ctx, answer)
}
func (r IncomingRequest) approval(ctx context.Context, o ApprovalOptions, approve bool) error {
	invalid := func() error { return r.Handle().answerError(NotSent, ErrInvalidInput) }
	count := 0
	for _, present := range []bool{o.CommandExecution != nil, o.FileChange != nil, o.Permissions != nil, o.ApplyPatch != nil, o.ExecCommand != nil} {
		if present {
			count++
		}
	}
	if count > 1 {
		return invalid()
	}
	decision := "decline"
	legacy := `{"denied":{"rejection":"user denied"}}`
	if approve {
		decision = "accept"
		legacy = `"approved"`
	}
	var response any
	switch r.Method() {
	case "item/commandExecution/requestApproval":
		if count != 0 && o.CommandExecution == nil {
			return invalid()
		}
		if o.CommandExecution == nil {
			o.CommandExecution = &types.CommandExecutionRequestApprovalResponse{}
			v := types.CommandExecutionApprovalDecisionValue(decision)
			o.CommandExecution.Decision.Value = &v
		}
		response = o.CommandExecution
	case "item/fileChange/requestApproval":
		if count != 0 && o.FileChange == nil {
			return invalid()
		}
		if o.FileChange == nil {
			o.FileChange = &types.FileChangeRequestApprovalResponse{Decision: types.FileChangeApprovalDecision(decision)}
		}
		response = o.FileChange
	case "item/permissions/requestApproval":
		if count != 0 && o.Permissions == nil {
			return invalid()
		}
		if o.Permissions == nil {
			if approve {
				return invalid()
			}
			o.Permissions = &types.PermissionsRequestApprovalResponse{}
		}
		response = o.Permissions
	case "applyPatchApproval":
		if count != 0 && o.ApplyPatch == nil {
			return invalid()
		}
		if o.ApplyPatch == nil {
			o.ApplyPatch = &types.ApplyPatchApprovalResponse{}
			_ = json.Unmarshal([]byte(legacy), &o.ApplyPatch.Decision)
		}
		response = o.ApplyPatch
	case "execCommandApproval":
		if count != 0 && o.ExecCommand == nil {
			return invalid()
		}
		if o.ExecCommand == nil {
			o.ExecCommand = &types.ExecCommandApprovalResponse{}
			_ = json.Unmarshal([]byte(legacy), &o.ExecCommand.Decision)
		}
		response = o.ExecCommand
	default:
		return invalid()
	}
	// Serialize once before validation so union validity and required fields come
	// from the native codec, and the validated bytes are exactly those dispatched.
	raw, err := json.Marshal(response)
	if err != nil {
		return r.Handle().answerError(NotSent, err)
	}
	if r.Method() == "item/permissions/requestApproval" {
		if scope, ok := o.Permissions.Scope.Get(); ok && scope != types.PermissionGrantScopeTurn && scope != types.PermissionGrantScopeSession {
			return invalid()
		}
		if !approve {
			grant, err := json.Marshal(o.Permissions.Permissions)
			if err != nil || string(grant) != "{}" {
				return invalid()
			}
		}
	} else {
		fields, _ := objectFields(raw)
		allowed, known := approvalDecision(r.Method(), fields["decision"])
		if !known || allowed != approve {
			return invalid()
		}
	}
	return r.Handle().Answer(ctx, json.RawMessage(raw))
}

// approvalDecision classifies only the pinned native decision forms. Unknown
// variants stay available through the explicit raw handle, never guessed here.
func approvalDecision(method string, raw json.RawMessage) (approve, known bool) {
	legacy := method == "applyPatchApproval" || method == "execCommandApproval"
	var value string
	if json.Unmarshal(raw, &value) == nil {
		if legacy {
			switch value {
			case "approved", "approved_for_session", "approved_mcp_policy_amendment":
				return true, true
			case "abort":
				return false, true
			}
		} else {
			switch value {
			case "accept", "acceptForSession":
				return true, true
			case "decline", "cancel":
				return false, true
			}
		}
		return false, false
	}
	if method == "item/fileChange/requestApproval" {
		return false, false
	}
	fields, err := objectFields(raw)
	if err != nil {
		return false, false
	}
	acceptKey, networkKey := "acceptWithExecpolicyAmendment", "applyNetworkPolicyAmendment"
	if legacy {
		acceptKey, networkKey = "approved_execpolicy_amendment", "network_policy_amendment"
		if _, ok := fields["denied"]; ok {
			return false, true
		}
	}
	if _, ok := fields[acceptKey]; ok {
		return true, true
	}
	if nested, ok := fields[networkKey]; ok {
		body, _ := objectFields(nested)
		amendment, _ := objectFields(body["network_policy_amendment"])
		var action string
		_ = json.Unmarshal(amendment["action"], &action)
		return action == "allow", action == "allow" || action == "deny"
	}
	return false, false
}
