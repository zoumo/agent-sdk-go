package agentcodex_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentcodex "github.com/zoumo/agent-sdk-go/agent/codex"
	codextypes "github.com/zoumo/agent-sdk-go/client/codex/types"
)

func TestNativePermissionsAreScopedToEachOpenAndResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"approvalPolicy":"on-request","approvalsReviewer":"auto_review","cwd":"/synthetic","sandbox":"read-only"}}`,
		`<{"id":92,"result":`+codexPermissionResult("root-start", "on-request", "auto_review", "readOnly")+`}`,
		`>{"id":93,"method":"thread/resume","params":{"approvalPolicy":"never","approvalsReviewer":"user","cwd":"/synthetic","excludeTurns":true,"permissions":"named-profile","threadId":"root-resume"}}`,
		`<{"id":93,"result":`+codexThreadResumeResult("root-resume")+`}`,
		`>{"id":94,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":94,"result":`+codexPermissionResult("root-default", "never", "user", "readOnly")+`}`,
		`>{"id":95,"method":"thread/unsubscribe","params":{"threadId":"root-start"}}`,
		`<{"id":95,"result":{"status":"unsubscribed"}}`,
		`>{"id":96,"method":"thread/unsubscribe","params":{"threadId":"root-resume"}}`,
		`<{"id":96,"result":{"status":"unsubscribed"}}`,
		`>{"id":97,"method":"thread/unsubscribe","params":{"threadId":"root-default"}}`,
		`<{"id":97,"result":{"status":"unsubscribed"}}`,
	)
	core := newTestProvider(t, prepareCodexPeerWithExactFrames(t, "passed", nil, frames...))
	t.Cleanup(func() { assert.NoError(t, core.Stop(context.Background())) })
	_, err := core.Start(ctx)
	require.NoError(t, err)

	options := agent.OpenOptions{Cwd: "/synthetic"}
	started, err := core.OpenWithPermissions(ctx, options, agentcodex.Permissions{
		ApprovalPolicy: codextypes.Some(codextypes.AskForApproval{
			Value: new(codextypes.ApprovalPolicyOnRequest),
		}),
		ApprovalsReviewer: codextypes.Some(codextypes.ApprovalsReviewerAutoReview),
		Sandbox:           codextypes.Some(codextypes.SandboxModeReadOnly),
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, started)
	assert.Equal(t, "root-start", started.ID())

	resumed, err := core.ResumeWithPermissions(ctx, agent.ResumeOptions{
		SessionID:   "root-resume",
		OpenOptions: options,
	}, agentcodex.Permissions{
		ApprovalPolicy: codextypes.Some(codextypes.AskForApproval{
			Value: new(codextypes.ApprovalPolicyNever),
		}),
		ApprovalsReviewer: codextypes.Some(codextypes.ApprovalsReviewerUser),
		ProfileID:         codextypes.Some("named-profile"),
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, resumed)
	assert.Equal(t, "root-resume", resumed.ID())

	nativeDefaults, err := core.OpenWithPermissions(ctx, options, agentcodex.Permissions{})
	require.NoError(t, err)
	closeSessionOnCleanup(t, nativeDefaults)
	assert.Equal(t, "root-default", nativeDefaults.ID())

	require.NoError(t, started.Close(ctx))
	require.NoError(t, resumed.Close(ctx))
	require.NoError(t, nativeDefaults.Close(ctx))
}

func TestNativePermissionsRejectSandboxWithNamedProfileBeforeOpenOrResume(t *testing.T) {
	core := agentcodex.New(agentcodex.Config{})
	options := agent.OpenOptions{Cwd: t.TempDir()}
	permissions := agentcodex.Permissions{
		Sandbox:   codextypes.Some(codextypes.SandboxModeReadOnly),
		ProfileID: codextypes.Some("named-profile"),
	}

	opened, err := core.OpenWithPermissions(t.Context(), options, permissions)
	assert.Nil(t, opened)
	var openError *agent.Error
	require.ErrorAs(t, err, &openError)
	assert.Equal(t, agent.ErrorInvalidArgument, openError.Code)
	assert.Equal(t, agentcodex.ErrorReasonSandboxProfileConflict, openError.Reason)

	resumed, err := core.ResumeWithPermissions(t.Context(), agent.ResumeOptions{
		SessionID:   "root-resume",
		OpenOptions: options,
	}, permissions)
	assert.Nil(t, resumed)
	var resumeError *agent.Error
	require.ErrorAs(t, err, &resumeError)
	assert.Equal(t, agent.ErrorInvalidArgument, resumeError.Code)
	assert.Equal(t, agentcodex.ErrorReasonSandboxProfileConflict, resumeError.Reason)
}

func TestUnifiedPermissionPresetsMapOnOpenAndResume(t *testing.T) {
	tests := []struct {
		name       string
		preset     agent.PermissionPreset
		override   codextypes.SandboxMode
		policy     string
		reviewer   string
		wireFields string
		sandbox    string
	}{
		{
			"omitted", "", "", "never", "user",
			`"approvalPolicy":"never","sandbox":"danger-full-access"`, "dangerFullAccess",
		},
		{
			"autoReview", agent.PermissionPresetAutoReview, "", "on-request", "auto_review",
			`"approvalPolicy":"on-request","approvalsReviewer":"auto_review","sandbox":"workspace-write"`, "workspaceWrite",
		},
		{
			"manualReview", agent.PermissionPresetManualReview, "", "on-request", "user",
			`"approvalPolicy":"on-request","approvalsReviewer":"user","sandbox":"workspace-write"`, "workspaceWrite",
		},
		{
			"fullAccess", agent.PermissionPresetFullAccess, "", "never", "user",
			`"approvalPolicy":"never","sandbox":"danger-full-access"`, "dangerFullAccess",
		},
		{
			"manualReview read-only", agent.PermissionPresetManualReview, codextypes.SandboxModeReadOnly, "on-request", "user",
			`"approvalPolicy":"on-request","approvalsReviewer":"user","sandbox":"read-only"`, "readOnly",
		},
		{
			"autoReview sandbox disabled", agent.PermissionPresetAutoReview, codextypes.SandboxModeDangerFullAccess, "on-request", "auto_review",
			`"approvalPolicy":"on-request","approvalsReviewer":"auto_review","sandbox":"danger-full-access"`, "dangerFullAccess",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			frames := append([]string(nil), mockCodexInit...)
			frames = append(frames,
				fmt.Sprintf(`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic",%s}}`, test.wireFields),
				`<{"id":92,"result":`+codexPermissionResult("opened", test.policy, test.reviewer, test.sandbox)+`}`,
				fmt.Sprintf(`>{"id":93,"method":"thread/resume","params":{"cwd":"/synthetic","excludeTurns":true,"threadId":"resumed",%s}}`, test.wireFields),
				`<{"id":93,"result":`+codexPermissionResult("resumed", test.policy, test.reviewer, test.sandbox)+`}`,
			)
			frames = append(frames, codexCloseFrames(94, "opened")...)
			frames = append(frames, codexCloseFrames(95, "resumed")...)
			core := newTestProvider(t, prepareCodexPeerWithExactFrames(t, "passed", nil, frames...))
			_, err := core.Start(ctx)
			require.NoError(t, err)

			openOptions := agent.OpenOptions{Cwd: "/synthetic", PermissionPreset: test.preset}
			resumeOptions := agent.ResumeOptions{SessionID: "resumed", OpenOptions: openOptions}
			open, resume := core.Open, core.Resume
			if test.override != "" {
				permissions := agentcodex.Permissions{Sandbox: codextypes.Some(test.override)}
				open = func(ctx context.Context, options agent.OpenOptions) (agent.Session, error) {
					return core.OpenWithPermissions(ctx, options, permissions)
				}
				resume = func(ctx context.Context, options agent.ResumeOptions) (agent.Session, error) {
					return core.ResumeWithPermissions(ctx, options, permissions)
				}
			}
			opened, err := open(ctx, openOptions)
			require.NoError(t, err)
			closeSessionOnCleanup(t, opened)
			assert.Equal(t, "opened", opened.ID())

			resumed, err := resume(ctx, resumeOptions)
			require.NoError(t, err)
			closeSessionOnCleanup(t, resumed)
			assert.Equal(t, "resumed", resumed.ID())

			require.NoError(t, opened.Close(ctx))
			require.NoError(t, resumed.Close(ctx))
		})
	}
}

func TestResumeRejectsStaleFullAccessSandboxAndCanRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"approvalPolicy":"never","cwd":"/synthetic","sandbox":"danger-full-access"}}`,
		`<{"id":92,"result":`+codexPermissionResult("thread-a", "never", "user", "dangerFullAccess")+`}`,
	)
	frames = append(frames, codexCloseFrames(93, "thread-a")...)
	frames = append(frames,
		`>{"id":94,"method":"thread/resume","params":{"approvalPolicy":"on-request","approvalsReviewer":"user","cwd":"/synthetic","excludeTurns":true,"sandbox":"workspace-write","threadId":"thread-a"}}`,
		`<{"id":94,"result":`+codexPermissionResult("thread-a", "on-request", "user", "dangerFullAccess")+`}`,
	)
	frames = append(frames, codexCloseFrames(95, "thread-a")...)
	frames = append(frames,
		`>{"id":96,"method":"thread/resume","params":{"approvalPolicy":"on-request","approvalsReviewer":"user","cwd":"/synthetic","excludeTurns":true,"sandbox":"workspace-write","threadId":"thread-a"}}`,
		`<{"id":96,"result":`+codexPermissionResult("thread-a", "on-request", "user", "workspaceWrite")+`}`,
	)
	frames = append(frames, codexCloseFrames(97, "thread-a")...)
	core := newTestProvider(t, prepareCodexPeerWithExactFrames(t, "passed", nil, frames...))
	_, err := core.Start(ctx)
	require.NoError(t, err)

	fullAccess, err := core.Open(ctx, agent.OpenOptions{
		Cwd: "/synthetic", PermissionPreset: agent.PermissionPresetFullAccess,
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, fullAccess)
	require.NoError(t, fullAccess.Close(ctx))

	options := agent.ResumeOptions{
		SessionID: "thread-a",
		OpenOptions: agent.OpenOptions{
			Cwd: "/synthetic", PermissionPreset: agent.PermissionPresetManualReview,
		},
	}
	rejected, err := core.Resume(ctx, options)
	assert.Nil(t, rejected)
	var rejection *agent.Error
	require.ErrorAs(t, err, &rejection)
	assert.Equal(t, agent.ErrorNativeRejected, rejection.Code)
	assert.Equal(t, "permission_preset_mismatch", rejection.Reason)

	resumed, err := core.Resume(ctx, options)
	require.NoError(t, err)
	closeSessionOnCleanup(t, resumed)
	assert.Equal(t, "thread-a", resumed.ID())
	require.NoError(t, resumed.Close(ctx))
	require.NoError(t, core.Stop(ctx))
}

func TestUnifiedPermissionPresetsRejectConflictingNativeFieldsBeforeOpenOrResume(t *testing.T) {
	tests := []struct {
		name        string
		preset      agent.PermissionPreset
		permissions agentcodex.Permissions
	}{
		{"approval policy", agent.PermissionPresetAutoReview, agentcodex.Permissions{
			ApprovalPolicy: codextypes.Some(codextypes.AskForApproval{Value: new(codextypes.ApprovalPolicyOnRequest)}),
		}},
		{"reviewer", agent.PermissionPresetManualReview, agentcodex.Permissions{
			ApprovalsReviewer: codextypes.Some(codextypes.ApprovalsReviewerUser),
		}},
		{"profile", agent.PermissionPresetAutoReview, agentcodex.Permissions{
			ProfileID: codextypes.Some("native-profile"),
		}},
		{"fullAccess sandbox", agent.PermissionPresetFullAccess, agentcodex.Permissions{
			Sandbox: codextypes.Some(codextypes.SandboxModeDangerFullAccess),
		}},
		{"null sandbox", agent.PermissionPresetAutoReview, agentcodex.Permissions{
			Sandbox: codextypes.Null[codextypes.SandboxMode](),
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			core := agentcodex.New(agentcodex.Config{})
			options := agent.OpenOptions{Cwd: t.TempDir(), PermissionPreset: test.preset}

			opened, err := core.OpenWithPermissions(t.Context(), options, test.permissions)
			assert.Nil(t, opened)
			var openError *agent.Error
			require.ErrorAs(t, err, &openError)
			assert.Equal(t, agent.ErrorInvalidArgument, openError.Code)
			assert.Equal(t, "permission_preset_conflict", openError.Reason)

			resumed, err := core.ResumeWithPermissions(t.Context(), agent.ResumeOptions{
				SessionID: "resumed", OpenOptions: options,
			}, test.permissions)
			assert.Nil(t, resumed)
			var resumeError *agent.Error
			require.ErrorAs(t, err, &resumeError)
			assert.Equal(t, agent.ErrorInvalidArgument, resumeError.Code)
			assert.Equal(t, "permission_preset_conflict", resumeError.Reason)
		})
	}
}
