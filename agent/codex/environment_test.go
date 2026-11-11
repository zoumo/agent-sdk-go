package agentcodex_test

import (
	"context"
	"maps"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentcodex "github.com/zoumo/agent-sdk-go/agent/codex"
	"github.com/zoumo/agent-sdk-go/codex"
)

func TestEnvironmentShellEnvMapsOpenAndResume(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "open", true: "resume"}[resume], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			method, params, result := "thread/start", `{"config":{"shell_environment_policy.set":{"EMPTY":"","TOKEN":"literal=$HOME=附加"}},"cwd":"/synthetic"}`, codexThreadStartResult("root")
			if resume {
				method = "thread/resume"
				params = `{"config":{"shell_environment_policy.set":{"EMPTY":"","TOKEN":"literal=$HOME=附加"}},"cwd":"/synthetic","excludeTurns":true,"threadId":"root"}`
				result = codexThreadResumeResult("root")
			}
			frames := append([]string(nil), mockCodexInit...)
			frames = append(frames, `>{"id":92,"method":"`+method+`","params":`+params+`}`, `<{"id":92,"result":`+result+`}`)
			frames = append(frames, codexCloseFrames(93, "root")...)
			provider := newTestProvider(t, prepareCodexPeer(t, frames...))
			t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
			caps, err := provider.Start(ctx)
			require.NoError(t, err)
			assert.True(t, caps.Supports(agent.CapabilityShellEnv))
			assert.False(t, caps.Supports(agent.CapabilityProcessEnv))
			env := map[string]string{"TOKEN": "literal=$HOME=附加", "EMPTY": ""}
			before := maps.Clone(env)
			options := agent.OpenOptions{Cwd: "/synthetic", ShellEnv: env}
			var session agent.Session
			if resume {
				session, err = provider.Resume(ctx, agent.ResumeOptions{SessionID: "root", OpenOptions: options})
			} else {
				session, err = provider.Open(ctx, options)
			}
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)
			assert.Equal(t, before, env)
			assert.True(t, session.Capabilities().Supports(agent.CapabilityShellEnv))
			assert.False(t, session.Capabilities().Supports(agent.CapabilityProcessEnv))
			require.NoError(t, session.Close(ctx))
		})
	}
}

func TestEnvironmentProcessEnvRejectedBeforeProviderSideEffects(t *testing.T) {
	provider := agentcodex.New(agentcodex.Config{Stdio: codex.StdioOptions{Executable: "/does-not-exist"}})
	options := agent.OpenOptions{Cwd: t.TempDir(), ProcessEnv: map[string]string{"TOKEN": "secret-process-value"}}
	_, err := provider.Open(context.Background(), options)
	assert.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret-process-value")
	_, err = provider.Resume(context.Background(), agent.ResumeOptions{SessionID: "root", OpenOptions: options})
	assert.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
}

func TestEnvironmentEmptyShellEnvOmitsConfig(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames, `>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`, `<{"id":92,"result":`+codexThreadStartResult("root")+`}`)
	frames = append(frames, codexCloseFrames(93, "root")...)
	provider := newTestProvider(t, prepareCodexPeer(t, frames...))
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic", ShellEnv: map[string]string{}, ProcessEnv: map[string]string{}})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	require.NoError(t, session.Close(ctx))
}

func TestEnvironmentInvalidShellEnvRejectedBeforeThreadRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	provider := newTestProvider(t, prepareCodexPeer(t, mockCodexInit...))
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	for _, env := range []map[string]string{{"codex_session_id": "secret-env-value"}, {"Path": "secret-env-value", "PATH": "other-secret"}, {"TOKEN": "secret-env-value\x00"}} {
		options := agent.OpenOptions{Cwd: "/synthetic", ShellEnv: env}
		_, err := provider.Open(ctx, options)
		require.Error(t, err)
		assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
		assert.NotContains(t, err.Error(), "secret-env-value")
		_, err = provider.Resume(ctx, agent.ResumeOptions{SessionID: "root", OpenOptions: options})
		require.Error(t, err)
		assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
		assert.NotContains(t, err.Error(), "secret-env-value")
	}
}
