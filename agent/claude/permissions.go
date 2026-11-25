package agentclaude

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/client/claude"
)

// Permissions carries native Claude permission options for one Session.
// Its zero value leaves permission configuration to Claude.
type Permissions struct {
	PermissionMode                  claude.PermissionMode
	AllowDangerouslySkipPermissions bool
	Sandbox                         *claude.SandboxOptions
}

func (p Permissions) validate(operation string) error {
	if p.PermissionMode != "" && !p.PermissionMode.Valid() {
		return nativeOperationError(operation, fmt.Errorf("%w: unsupported permission mode %q", claude.ErrInvalidInput, p.PermissionMode))
	}
	return nil
}

func (p Permissions) resolve(preset agent.PermissionPreset, operation string) (Permissions, error) {
	if preset == "" {
		return p, p.validate(operation)
	}
	if p.PermissionMode != "" || p.AllowDangerouslySkipPermissions ||
		(preset == agent.PermissionPresetFullAccess && p.Sandbox != nil) {
		return Permissions{}, adapterError(agent.ErrorInvalidArgument, agent.ErrorReasonPermissionPresetConflict,
			"Claude permission preset conflicts with native permission fields", nil)
	}
	if preset == agent.PermissionPresetFullAccess {
		return Permissions{
			PermissionMode: claude.PermissionModeBypassPermissions,
			Sandbox:        &claude.SandboxOptions{Enabled: new(false)},
		}, nil
	}
	var override claude.SandboxOptions
	if p.Sandbox != nil {
		override = *p.Sandbox
	}
	p.Sandbox = &claude.SandboxOptions{
		Enabled:                  new(*cmp.Or(override.Enabled, new(true))),
		FailIfUnavailable:        new(*cmp.Or(override.FailIfUnavailable, new(true))),
		AllowUnsandboxedCommands: new(*cmp.Or(override.AllowUnsandboxedCommands, new(false))),
	}
	p.PermissionMode = claude.PermissionModeDefault
	if preset == agent.PermissionPresetAutoReview {
		p.PermissionMode = claude.PermissionModeAuto
	}
	return p, nil
}

func (p Permissions) confirm(ctx context.Context, native *claude.Session) error {
	if mode := native.InitialPermissionMode(); mode != p.PermissionMode {
		code := agent.ErrorNativeRejected
		if mode == "" || p.PermissionMode == claude.PermissionModeAuto {
			code = agent.ErrorUnsupported
		}
		return adapterError(code, ErrorReasonPermissionModeUnconfirmed,
			"Claude did not confirm the requested startup permission mode", nil)
	}
	sandbox, err := native.GetEffectiveSandbox(ctx)
	if err != nil && !errors.Is(err, claude.ErrProtocol) {
		return nativeOperationError("get_settings", err)
	}
	if err != nil || !matchesSandbox(*p.Sandbox, sandbox) {
		return adapterError(agent.ErrorNativeRejected, ErrorReasonSandboxSettingsUnconfirmed,
			"Claude did not confirm the requested effective sandbox settings", nil)
	}
	return nil
}

func permissionOpenError(err error) error {
	if errors.Is(err, claude.ErrSandboxUnavailable) {
		return adapterError(agent.ErrorUnsupported, ErrorReasonSandboxUnavailable,
			"Claude requires a working Bash sandbox for this session", err)
	}
	return nativeOperationError("open", err)
}

func matchesSandbox(expected, actual claude.SandboxOptions) bool {
	matches := func(expected, actual *bool) bool {
		return expected == nil || actual != nil && *expected == *actual
	}
	return matches(expected.Enabled, actual.Enabled) &&
		matches(expected.FailIfUnavailable, actual.FailIfUnavailable) &&
		matches(expected.AllowUnsandboxedCommands, actual.AllowUnsandboxedCommands)
}
