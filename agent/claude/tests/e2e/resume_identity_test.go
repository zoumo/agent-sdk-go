package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	"github.com/zoumo/agent-sdk-go/client/claude"
)

func TestUnifiedResumeIdentityWithoutInput(t *testing.T) {
	if os.Getenv("SDK_REAL_AGENT_E2E") != "1" || os.Getenv("CLAUDE_E2E") != "1" {
		t.Skip("explicit real Claude lifecycle opt-in required")
	}
	require.Empty(t, os.Getenv("CI"))
	cli := os.Getenv("CLAUDE_RESUME_E2E_CLI")
	config := os.Getenv("CLAUDE_RESUME_E2E_CONFIG_DIR")
	cwd := os.Getenv("CLAUDE_RESUME_E2E_CWD")
	id := os.Getenv("CLAUDE_RESUME_E2E_SESSION_ID")
	for _, path := range []string{cli, config, cwd} {
		require.True(t, filepath.IsAbs(path))
	}
	require.NotEmpty(t, id)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	options := claude.SessionInfoOptions{Scope: claude.SessionQueryScope{ConfigDir: config, ProjectDir: cwd}}
	before, err := claude.GetSessionInfo(ctx, id, options)
	require.NoError(t, err)
	require.NotNil(t, before.Info)
	fileBefore, err := os.Stat(before.Info.Path)
	require.NoError(t, err)
	provider := agentclaude.New(agentclaude.Config{CLIPath: cli, ConfigDir: config, Env: append(os.Environ(), "CLAUDE_CONFIG_DIR="+config), InitializeTimeout: 10 * time.Second})
	_, err = provider.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Stop(context.Background())) })
	session, err := provider.Resume(ctx, agent.ResumeOptions{SessionID: id, OpenOptions: agent.OpenOptions{Cwd: cwd, Config: agent.SessionConfig{Model: "auto/deepseek-v4.1-flash-1017"}, Features: agent.Features{RestrictLocalTools: true}}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close(context.Background())) })
	require.Equal(t, id, session.ID())
	require.NoError(t, session.Close(ctx))
	require.NoError(t, session.Err())
	after, err := claude.GetSessionInfo(ctx, id, options)
	require.NoError(t, err)
	require.NotNil(t, after.Info)
	require.Equal(t, before.Info.Path, after.Info.Path)
	fileAfter, err := os.Stat(after.Info.Path)
	require.NoError(t, err)
	require.True(t, os.SameFile(fileBefore, fileAfter))
	t.Logf("resumed same Session %s without Open, user input or model work; same transcript file; Close succeeded", id)
}
