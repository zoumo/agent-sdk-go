package agentcodex

import (
	"errors"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/client/codex"
	codextypes "github.com/zoumo/agent-sdk-go/client/codex/types"
)

// Permissions carries native permission fields for one Codex thread start
// or resume. Its zero value omits all permission fields from the request.
type Permissions struct {
	ApprovalPolicy    codextypes.Optional[codextypes.AskForApproval]
	ApprovalsReviewer codextypes.Optional[codextypes.ApprovalsReviewer]
	Sandbox           codextypes.Optional[codextypes.SandboxMode]
	// ProfileID selects a named permissions profile and is mutually exclusive
	// with a Sandbox value.
	ProfileID codextypes.Optional[string]
}

func (p Permissions) validate() error {
	_, hasSandbox := p.Sandbox.Get()
	_, hasProfile := p.ProfileID.Get()
	if hasSandbox && hasProfile {
		return adapterError(
			agent.ErrorInvalidArgument,
			ErrorReasonSandboxProfileConflict,
			"Codex sandbox and permissions profile cannot be combined",
			nil,
		)
	}
	return nil
}

func (p Permissions) resolve(preset agent.PermissionPreset) (Permissions, error) {
	if preset == "" {
		return p, p.validate()
	}
	if p.ApprovalPolicy.IsSet() || p.ApprovalsReviewer.IsSet() || p.ProfileID.IsSet() ||
		(p.Sandbox.IsSet() && (preset == agent.PermissionPresetFullAccess || p.Sandbox.IsNull())) {
		return Permissions{}, adapterError(agent.ErrorInvalidArgument, agent.ErrorReasonPermissionPresetConflict,
			"Codex permission preset conflicts with native permission fields", nil)
	}
	if preset == agent.PermissionPresetFullAccess {
		return Permissions{
			ApprovalPolicy: approvalPolicy(codextypes.ApprovalPolicyNever),
			Sandbox:        codextypes.Some(codextypes.SandboxModeDangerFullAccess),
		}, nil
	}
	if !p.Sandbox.IsSet() {
		p.Sandbox = codextypes.Some(codextypes.SandboxModeWorkspaceWrite)
	}
	switch p.Sandbox.ValueOr("") {
	case codextypes.SandboxModeWorkspaceWrite, codextypes.SandboxModeReadOnly, codextypes.SandboxModeDangerFullAccess:
	default:
		return Permissions{}, adapterError(agent.ErrorInvalidArgument, ErrorReasonInvalidSandboxMode,
			"unknown Codex sandbox mode", nil)
	}
	reviewer := codextypes.ApprovalsReviewerUser
	if preset == agent.PermissionPresetAutoReview {
		reviewer = codextypes.ApprovalsReviewerAutoReview
	}
	p.ApprovalPolicy = approvalPolicy(codextypes.ApprovalPolicyOnRequest)
	p.ApprovalsReviewer = codextypes.Some(reviewer)
	return p, nil
}

func approvalPolicy(policy codextypes.ApprovalPolicy) codextypes.Optional[codextypes.AskForApproval] {
	return codextypes.Some(codextypes.AskForApproval{Value: new(policy)})
}

func (p Permissions) matches(
	policy codextypes.AskForApproval,
	reviewer codextypes.ApprovalsReviewer,
	sandbox codextypes.SandboxPolicy,
) bool {
	expected := p.ApprovalPolicy.ValueOr(codextypes.AskForApproval{})
	if expected.Value == nil || policy.Value == nil || *policy.Value != *expected.Value {
		return false
	}
	if value, ok := p.ApprovalsReviewer.Get(); ok && value != reviewer {
		return false
	}
	switch p.Sandbox.ValueOr("") {
	case codextypes.SandboxModeWorkspaceWrite:
		return sandbox.WorkspaceWrite != nil
	case codextypes.SandboxModeReadOnly:
		return sandbox.ReadOnly != nil
	case codextypes.SandboxModeDangerFullAccess:
		return sandbox.DangerFullAccess != nil
	default:
		return false
	}
}

func abortPermissionMismatch(native *codex.Session) error {
	err := adapterError(agent.ErrorNativeRejected, ErrorReasonPermissionPresetMismatch,
		"Codex did not confirm the requested approval and sandbox settings", nil)
	if closeErr := native.Abort(); closeErr != nil {
		return errors.Join(err, nativeOperationError("close", closeErr))
	}
	return err
}
