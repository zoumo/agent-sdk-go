package agentclaude_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	"github.com/zoumo/agent-sdk-go/client/claude"
)

func TestConcreteSessionPermissionsReachClaudeLaunch(t *testing.T) {
	for _, test := range []struct {
		name                    string
		wantIdentityArg         string
		withPermissions         agentclaude.Permissions
		wantPermissionArguments []string
	}{
		{
			name:                    "create",
			wantIdentityArg:         "--session-id=" + claudeResumePeerSessionID,
			withPermissions:         agentclaude.Permissions{PermissionMode: claude.PermissionModeBypassPermissions, Sandbox: &claude.SandboxOptions{Enabled: new(false)}},
			wantPermissionArguments: []string{"--permission-mode=bypassPermissions", `--settings={"sandbox":{"enabled":false}}`},
		},
		{
			name:                    "claim",
			wantIdentityArg:         "--await-claim",
			withPermissions:         agentclaude.Permissions{PermissionMode: claude.PermissionModeDefault, AllowDangerouslySkipPermissions: true},
			wantPermissionArguments: []string{"--permission-mode=default", "--allow-dangerously-skip-permissions"},
		},
		{
			name:            "resume",
			wantIdentityArg: "--resume=" + claudeResumePeerSessionID,
			withPermissions: agentclaude.Permissions{
				PermissionMode: claude.PermissionModeAuto,
				Sandbox: &claude.SandboxOptions{
					Enabled:                  new(true),
					FailIfUnavailable:        new(true),
					AllowUnsandboxedCommands: new(false),
				},
			},
			wantPermissionArguments: []string{"--permission-mode=auto", `--settings={"sandbox":{"enabled":true,"failIfUnavailable":true,"allowUnsandboxedCommands":false}}`},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cwd, config := t.TempDir(), newResumeConfigDir(t)
			argvPath := filepath.Join(t.TempDir(), "argv.json")
			peer := newClaudeResumePeer(t, claudeResumePeerScenario{
				CWD: cwd, ArgvPath: argvPath,
				StatusResponse: resumeStatusBody(claudeResumePeerSessionID, cwd), SkipSandboxProbe: true,
			})
			provider := newResumeProvider(t, peer.CLIPath, peer.Env, config, time.Second)
			if test.name == "create" {
				agentclaude.SetSessionIDGeneratorForTest(provider, func() (string, error) { return claudeResumePeerSessionID, nil })
			}
			open := func(opts agent.OpenOptions, permissions agentclaude.Permissions) (agent.Session, error) {
				return provider.OpenWithPermissions(ctx, opts, permissions)
			}
			if test.name == "resume" {
				writeResumeTranscript(t, config, resumeProjectKey(t, cwd), claudeResumePeerSessionID, cwd)
				open = func(opts agent.OpenOptions, permissions agentclaude.Permissions) (agent.Session, error) {
					return provider.ResumeWithPermissions(ctx, agent.ResumeOptions{SessionID: claudeResumePeerSessionID, OpenOptions: opts}, permissions)
				}
			}

			options := agent.OpenOptions{Cwd: cwd}
			session, err := open(options, test.withPermissions)
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)
			assert.Equal(t, claudeResumePeerSessionID, session.ID())
			argv := readClaudeArgv(t, argvPath)
			assert.Contains(t, argv, test.wantIdentityArg)
			assert.Equal(t, test.wantPermissionArguments, claudePermissionArgv(argv))
			require.NoError(t, session.Close(ctx))

			session, err = open(options, agentclaude.Permissions{})
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)
			assert.Empty(t, claudePermissionArgv(readClaudeArgv(t, argvPath)))
			require.NoError(t, session.Close(ctx))
		})
	}
}

func TestConcreteSessionPermissionsRejectInvalidModeBeforeNativeCalls(t *testing.T) {
	ctx := context.Background()
	cwd, config := t.TempDir(), newResumeConfigDir(t)
	versionPath := filepath.Join(t.TempDir(), "version-called.json")
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{CWD: cwd, VersionEnvPath: versionPath})
	provider := newResumeProvider(t, peer.CLIPath, peer.Env, config, time.Second)
	options := agent.OpenOptions{Cwd: cwd}
	permissions := agentclaude.Permissions{PermissionMode: "futureMode"}

	session, err := provider.OpenWithPermissions(ctx, options, permissions)
	assert.Nil(t, session)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
	session, err = provider.ResumeWithPermissions(ctx, agent.ResumeOptions{
		SessionID: claudeResumePeerSessionID, OpenOptions: options,
	}, permissions)
	assert.Nil(t, session)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))

	_, err = os.Stat(versionPath)
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(peer.SessionMark)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func claudePermissionArgv(args []string) []string {
	var filtered []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "--permission-mode=") ||
			strings.HasPrefix(arg, "--settings=") ||
			arg == "--allow-dangerously-skip-permissions" ||
			arg == "--dangerously-skip-permissions" {
			filtered = append(filtered, arg)
		}
	}
	return filtered
}
