package agentclaude_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	"github.com/zoumo/agent-sdk-go/client/claude"
)

const (
	claudeApprovalSandbox   = `--settings={"sandbox":{"enabled":true,"failIfUnavailable":true,"allowUnsandboxedCommands":false}}`
	claudeFullAccessSandbox = `--settings={"sandbox":{"enabled":false}}`
)

func TestClaudePermissionPresetOpenChecksStartup(t *testing.T) {
	for _, tc := range []struct {
		name        string
		preset      agent.PermissionPreset
		override    *claude.SandboxOptions
		wantMode    string
		wantSandbox string
	}{
		{name: "omitted", wantMode: "bypassPermissions", wantSandbox: claudeFullAccessSandbox},
		{name: "auto review", preset: agent.PermissionPresetAutoReview, wantMode: "auto", wantSandbox: claudeApprovalSandbox},
		{name: "manual review", preset: agent.PermissionPresetManualReview, wantMode: "default", wantSandbox: claudeApprovalSandbox},
		{name: "full access", preset: agent.PermissionPresetFullAccess, wantMode: "bypassPermissions", wantSandbox: claudeFullAccessSandbox},
		{
			name:        "explicit sandbox override",
			preset:      agent.PermissionPresetAutoReview,
			override:    &claude.SandboxOptions{Enabled: new(false), AllowUnsandboxedCommands: new(true)},
			wantMode:    "auto",
			wantSandbox: `--settings={"sandbox":{"enabled":false,"failIfUnavailable":true,"allowUnsandboxedCommands":true}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cwd, configDir := t.TempDir(), newResumeConfigDir(t)
			argvPath := filepath.Join(t.TempDir(), "argv.json")
			peer := newClaudeResumePeer(t, claudeResumePeerScenario{CWD: cwd, ArgvPath: argvPath})
			core := newResumeProvider(t, peer.CLIPath, peer.Env, configDir, time.Second)
			options := agent.OpenOptions{Cwd: cwd, PermissionPreset: tc.preset}

			var opened agent.Session
			var err error
			if tc.override != nil {
				opened, err = core.OpenWithPermissions(ctx, options, agentclaude.Permissions{Sandbox: tc.override})
			} else {
				opened, err = core.Open(ctx, options)
			}
			require.NoError(t, err)
			closeSessionOnCleanup(t, opened)
			assert.Equal(t, claudeResumePeerSessionID, opened.ID())
			argv := readClaudeArgv(t, argvPath)
			assert.Contains(t, argv, "--await-claim")
			assert.Equal(t, []string{"--permission-mode=" + tc.wantMode, tc.wantSandbox}, claudePermissionArgv(argv))
			require.NoError(t, opened.Close(ctx))
			assert.Empty(t, readOpenPeerObservation(t, peer.Observation).UserFrames)
		})
	}
}

func TestClaudePermissionPresetResumeAppliesRequestedPermissions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		preset  agent.PermissionPreset
		mode    string
		sandbox string
	}{
		{name: "manual", preset: agent.PermissionPresetManualReview, mode: "default", sandbox: claudeApprovalSandbox},
		{name: "auto", preset: agent.PermissionPresetAutoReview, mode: "auto", sandbox: claudeApprovalSandbox},
		{name: "omitted", mode: "bypassPermissions", sandbox: claudeFullAccessSandbox},
		{name: "full access", preset: agent.PermissionPresetFullAccess, mode: "bypassPermissions", sandbox: claudeFullAccessSandbox},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cwd, configDir := t.TempDir(), newResumeConfigDir(t)
			argvPath := filepath.Join(t.TempDir(), "argv.json")
			var transcript map[string]any
			require.NoError(t, json.Unmarshal([]byte(resumeMetadataLine(t, claudeResumePeerSessionID, cwd)), &transcript))
			transcript["permissionMode"] = "bypassPermissions"
			wire, err := json.Marshal(transcript)
			require.NoError(t, err)
			writeResumeSource(t, configDir, resumeProjectKey(t, cwd), claudeResumePeerSessionID, string(wire)+"\n")

			peer := newClaudeResumePeer(t, claudeResumePeerScenario{CWD: cwd, ArgvPath: argvPath})
			core := newResumeProvider(t, peer.CLIPath, peer.Env, configDir, time.Second)
			resumed, err := core.Resume(ctx, agent.ResumeOptions{
				SessionID:   claudeResumePeerSessionID,
				OpenOptions: agent.OpenOptions{Cwd: cwd, PermissionPreset: tc.preset},
			})
			require.NoError(t, err)
			closeSessionOnCleanup(t, resumed)
			assert.Equal(t, claudeResumePeerSessionID, resumed.ID())
			argv := readClaudeArgv(t, argvPath)
			assert.Contains(t, argv, "--resume="+claudeResumePeerSessionID)
			assert.Equal(t, []string{"--permission-mode=" + tc.mode, tc.sandbox}, claudePermissionArgv(argv))
			require.NoError(t, resumed.Close(ctx))
			assert.Empty(t, readOpenPeerObservation(t, peer.Observation).UserFrames)
		})
	}
}

func TestClaudePermissionPresetRejectsUnconfirmedStartup(t *testing.T) {
	for _, tc := range []struct {
		name     string
		preset   agent.PermissionPreset
		resume   bool
		scenario claudeResumePeerScenario
		code     agent.ErrorCode
		reason   string
	}{
		{
			name:     "auto falls back to manual",
			preset:   agent.PermissionPresetAutoReview,
			scenario: claudeResumePeerScenario{InitialMode: new("default")},
			code:     agent.ErrorUnsupported,
			reason:   "permission_mode_unconfirmed",
		},
		{
			name:     "missing startup mode",
			preset:   agent.PermissionPresetManualReview,
			scenario: claudeResumePeerScenario{InitialMode: new("")},
			code:     agent.ErrorUnsupported,
			reason:   "permission_mode_unconfirmed",
		},
		{
			name:     "noncanonical startup mode",
			preset:   agent.PermissionPresetAutoReview,
			scenario: claudeResumePeerScenario{InitializeBody: `{"CURRENT_PERMISSION_MODE":"auto"}`},
			code:     agent.ErrorUnsupported,
			reason:   agentclaude.ErrorReasonPermissionModeUnconfirmed,
		},
		{
			name:     "manual resume still reports bypass",
			preset:   agent.PermissionPresetManualReview,
			resume:   true,
			scenario: claudeResumePeerScenario{InitialMode: new("bypassPermissions")},
			code:     agent.ErrorNativeRejected,
			reason:   "permission_mode_unconfirmed",
		},
		{
			name:     "missing sandbox fields",
			preset:   agent.PermissionPresetAutoReview,
			scenario: claudeResumePeerScenario{SandboxSettings: `{"effective":{"sandbox":{"enabled":true}}}`},
			code:     agent.ErrorNativeRejected,
			reason:   "sandbox_settings_unconfirmed",
		},
		{
			name:     "effective settings disagree",
			preset:   agent.PermissionPresetAutoReview,
			scenario: claudeResumePeerScenario{SandboxSettings: `{"effective":{"sandbox":{"enabled":false,"failIfUnavailable":true,"allowUnsandboxedCommands":false}}}`},
			code:     agent.ErrorNativeRejected,
			reason:   "sandbox_settings_unconfirmed",
		},
		{
			name:     "ambiguous effective sandbox",
			preset:   agent.PermissionPresetAutoReview,
			scenario: claudeResumePeerScenario{SandboxSettings: `{"effective":{"sandbox":{"enabled":false,"enabled":true,"failIfUnavailable":true,"allowUnsandboxedCommands":false}}}`},
			code:     agent.ErrorNativeRejected,
			reason:   agentclaude.ErrorReasonSandboxSettingsUnconfirmed,
		},
		{
			name:     "noncanonical sandbox key cannot override native value",
			preset:   agent.PermissionPresetAutoReview,
			scenario: claudeResumePeerScenario{SandboxSettings: `{"effective":{"sandbox":{"enabled":false,"Enabled":true,"failIfUnavailable":true,"allowUnsandboxedCommands":false}}}`},
			code:     agent.ErrorNativeRejected,
			reason:   agentclaude.ErrorReasonSandboxSettingsUnconfirmed,
		},
		{
			name:     "native sandbox startup fails",
			preset:   agent.PermissionPresetAutoReview,
			scenario: claudeResumePeerScenario{SandboxUnavailable: true},
			code:     agent.ErrorUnsupported,
			reason:   agentclaude.ErrorReasonSandboxUnavailable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			tc.scenario.CWD = t.TempDir()
			tc.scenario.ArgvPath = filepath.Join(t.TempDir(), "argv.json")
			peer := newClaudeResumePeer(t, tc.scenario)
			configDir := newResumeConfigDir(t)
			core := newResumeProvider(t, peer.CLIPath, peer.Env, configDir, time.Second)
			options := agent.OpenOptions{Cwd: tc.scenario.CWD, PermissionPreset: tc.preset}
			var session agent.Session
			var err error
			if tc.resume {
				writeResumeTranscript(t, configDir, resumeProjectKey(t, options.Cwd), claudeResumePeerSessionID, options.Cwd)
				session, err = core.Resume(ctx, agent.ResumeOptions{
					SessionID:   claudeResumePeerSessionID,
					OpenOptions: options,
				})
			} else {
				session, err = core.Open(ctx, options)
			}
			require.Nil(t, session)
			var classified *agent.Error
			require.ErrorAs(t, err, &classified)
			assert.Equal(t, tc.code, classified.Code)
			assert.Equal(t, tc.reason, classified.Reason)
			assert.Contains(t, readClaudeArgv(t, tc.scenario.ArgvPath), claudeApprovalSandbox)
			require.NoError(t, core.Stop(ctx))
		})
	}
}

func TestClaudePermissionPresetConflictStopsBeforeLaunch(t *testing.T) {
	for _, tc := range []struct {
		name        string
		preset      agent.PermissionPreset
		permissions agentclaude.Permissions
	}{
		{
			name:        "explicit permission mode",
			preset:      agent.PermissionPresetManualReview,
			permissions: agentclaude.Permissions{PermissionMode: claude.PermissionModeDefault},
		},
		{
			name:        "permit later bypass",
			preset:      agent.PermissionPresetAutoReview,
			permissions: agentclaude.Permissions{AllowDangerouslySkipPermissions: true},
		},
		{
			name:        "full access sandbox override",
			preset:      agent.PermissionPresetFullAccess,
			permissions: agentclaude.Permissions{Sandbox: &claude.SandboxOptions{Enabled: new(false)}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			cwd := t.TempDir()
			versionPath := filepath.Join(t.TempDir(), "version.json")
			peer := newClaudeResumePeer(t, claudeResumePeerScenario{CWD: cwd, VersionEnvPath: versionPath})
			core := newResumeProvider(t, peer.CLIPath, peer.Env, newResumeConfigDir(t), time.Second)
			options := agent.OpenOptions{Cwd: cwd, PermissionPreset: tc.preset}

			session, err := core.OpenWithPermissions(ctx, options, tc.permissions)
			assert.Nil(t, session)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
			session, err = core.ResumeWithPermissions(ctx, agent.ResumeOptions{
				SessionID:   claudeResumePeerSessionID,
				OpenOptions: options,
			}, tc.permissions)
			assert.Nil(t, session)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
			_, err = os.Stat(versionPath)
			assert.ErrorIs(t, err, os.ErrNotExist)
			_, err = os.Stat(peer.SessionMark)
			assert.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}
