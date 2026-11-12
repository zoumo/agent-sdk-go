package agentclaude_test

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

func TestEnvironmentProcessEnvComposesWithoutMutatingCaller(t *testing.T) {
	t.Setenv("AGENT_ENV_AMBIENT", "ambient")
	for _, tc := range []struct {
		name     string
		base     []string
		overlay  map[string]string
		expected []string
	}{
		{"replacement", []string{"TOKEN=base", "BASE=keep"}, map[string]string{"TOKEN": "literal=$HOME=附加", "EMPTY": "", "A": "first"}, []string{"TOKEN=base", "BASE=keep", "A=first", "EMPTY=", "TOKEN=literal=$HOME=附加", "CLAUDE_CODE_SDK_READS_SESSION_STATE=1"}},
		{"empty replacement", []string{}, map[string]string{"EMPTY": ""}, []string{"EMPTY=", "CLAUDE_CODE_SDK_READS_SESSION_STATE=1"}},
		{"empty overlay", []string{"BASE=keep"}, map[string]string{}, []string{"BASE=keep", "CLAUDE_CODE_SDK_READS_SESSION_STATE=1"}},
		{"nil overlay", []string{"BASE=keep"}, nil, []string{"BASE=keep", "CLAUDE_CODE_SDK_READS_SESSION_STATE=1"}},
		{"empty replacement without overlay", []string{}, nil, []string{"CLAUDE_CODE_SDK_READS_SESSION_STATE=1"}},
		{"inherited", nil, map[string]string{"EMPTY": ""}, nil},
		{"inherited without overlay", nil, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			frames := append([]string(nil), mockClaudeInit...)
			frames = append(frames, `<{"type":"system","subtype":"init","session_id":"root","cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.293"}`)
			beforeBase, beforeOverlay := slices.Clone(tc.base), maps.Clone(tc.overlay)
			var observed []string
			provider := newTestClaudeProviderWithObserver(t, agentclaude.Config{Env: tc.base}, func(options claude.SessionOptions) { observed = options.Env }, prepareClaudePeer(t, "passed", frames...))
			t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
			caps, err := provider.Start(ctx)
			require.NoError(t, err)
			assert.True(t, caps.Supports(agent.CapabilityProcessEnv))
			assert.False(t, caps.Supports(agent.CapabilityShellEnv))
			session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd, ProcessEnv: tc.overlay, ShellEnv: map[string]string{}})
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)
			assert.Equal(t, beforeBase, tc.base)
			assert.Equal(t, beforeOverlay, tc.overlay)
			if tc.base == nil {
				assert.Contains(t, observed, "AGENT_ENV_AMBIENT=ambient")
				if len(tc.overlay) > 0 {
					assert.Contains(t, observed, "EMPTY=")
				}
			} else {
				assert.Equal(t, tc.expected, observed)
			}
			saved := slices.Clone(observed)
			if len(tc.base) > 0 {
				tc.base[0] = "MUTATED=1"
			}
			if tc.overlay != nil {
				tc.overlay["TOKEN"] = "mutated"
			}
			assert.Equal(t, saved, observed)
			assert.True(t, session.Capabilities().Supports(agent.CapabilityProcessEnv))
			assert.False(t, session.Capabilities().Supports(agent.CapabilityShellEnv))
			require.NoError(t, session.Close(ctx))
		})
	}
}

func TestEnvironmentEnablesNativeSessionStateEvents(t *testing.T) {
	const stateEnv = "CLAUDE_CODE_SDK_READS_SESSION_STATE"
	for _, tc := range []struct {
		name    string
		base    []string
		overlay map[string]string
		ambient string
		want    string
	}{
		{name: "replacement default", base: []string{"BASE=keep"}, want: "1"},
		{name: "empty replacement default", base: []string{}, want: "1"},
		{name: "explicit value preserved", base: []string{stateEnv + "=0"}, want: "0"},
		{name: "explicit empty value preserved", base: []string{stateEnv + "="}, want: ""},
		{name: "explicit lowercase name preserved", base: []string{"claude_code_sdk_reads_session_state=0"}, want: "0"},
		{name: "project overlay preserved", base: []string{"BASE=keep"}, overlay: map[string]string{stateEnv: "0"}, want: "0"},
		{name: "lowercase overlay preserved", base: []string{}, overlay: map[string]string{"claude_code_sdk_reads_session_state": ""}, want: ""},
		{name: "inherited value preserved", ambient: "0", want: "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.ambient != "" {
				t.Setenv(stateEnv, tc.ambient)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			frames := append([]string(nil), mockClaudeInit...)
			frames = append(frames, `<{"type":"system","subtype":"init","session_id":"root","cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.293"}`)
			var observed []string
			provider := newTestClaudeProviderWithObserver(t, agentclaude.Config{Env: tc.base}, func(options claude.SessionOptions) { observed = options.Env }, prepareClaudePeer(t, "passed", frames...))
			t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
			_, err := provider.Start(ctx)
			require.NoError(t, err)
			session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd, ProcessEnv: tc.overlay, ShellEnv: map[string]string{}})
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)

			values := make([]string, 0, 1)
			for _, entry := range observed {
				name, value, ok := strings.Cut(entry, "=")
				if ok && strings.EqualFold(name, stateEnv) {
					values = append(values, value)
				}
			}
			require.Len(t, values, 1, "effective environment must select one session state mode")
			assert.Equal(t, tc.want, values[0])
			require.NoError(t, session.Close(ctx))
		})
	}
}

func TestEnvironmentShellEnvRejectedBeforeClaudeLaunch(t *testing.T) {
	provider := agentclaude.New(agentclaude.Config{CLIPath: "/does-not-exist"})
	options := agent.OpenOptions{Cwd: t.TempDir(), ShellEnv: map[string]string{"TOKEN": "secret-shell-value"}}
	_, err := provider.Open(context.Background(), options)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
	assert.NotContains(t, err.Error(), "secret-shell-value")
	_, err = provider.Resume(context.Background(), agent.ResumeOptions{SessionID: claudeResumePeerSessionID, OpenOptions: options})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
}

func TestEnvironmentResumeVersionAndLaunchShareEffectiveEnvironment(t *testing.T) {
	for _, baseKind := range []string{"inherited", "empty", "replacement"} {
		t.Run(baseKind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd, configDir := t.TempDir(), newResumeConfigDir(t)
			transcript := writeResumeTranscript(t, configDir, "project", claudeResumePeerSessionID, cwd)
			versionPath, sessionPath := filepath.Join(t.TempDir(), "version-env.json"), filepath.Join(t.TempDir(), "session-env.json")
			peer := newClaudeResumePeer(t, claudeResumePeerScenario{
				CWD: cwd, TranscriptPath: transcript,
				Frames:          []string{resumeInitFrame(t, claudeResumePeerSessionID, cwd)},
				EnvironmentKeys: []string{"AGENT_ENV_AMBIENT", "AGENT_ENV_TOKEN", "EMPTY", "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_SDK_READS_SESSION_STATE"},
				VersionEnvPath:  versionPath, SessionEnvPath: sessionPath,
			})
			// The fixture selector is copied through the overlay, allowing an otherwise
			// empty replacement environment without relying on installed executables.
			selector := ""
			for _, entry := range peer.Env {
				if value, ok := strings.CutPrefix(entry, claudeResumePeerEnv+"="); ok {
					selector = value
				}
			}
			require.NotEmpty(t, selector)
			t.Setenv("AGENT_ENV_AMBIENT", "ambient")
			overlay := map[string]string{claudeResumePeerEnv: selector, "CLAUDE_CONFIG_DIR": configDir, "AGENT_ENV_TOKEN": "session", "EMPTY": "", "GORACE": "atexit_sleep_ms=0"}
			var base []string
			want := map[string]string{"CLAUDE_CONFIG_DIR": configDir, "AGENT_ENV_TOKEN": "session", "EMPTY": "", "CLAUDE_CODE_SDK_READS_SESSION_STATE": "1"}
			switch baseKind {
			case "inherited":
				want["AGENT_ENV_AMBIENT"] = "ambient"
			case "empty":
				base = []string{}
			case "replacement":
				base = []string{"AGENT_ENV_TOKEN=base", "AGENT_ENV_AMBIENT=replacement"}
				want["AGENT_ENV_AMBIENT"] = "replacement"
			}
			provider := newResumeProvider(t, peer.CLIPath, base, configDir, 3*time.Second)
			session, err := provider.Resume(ctx, agent.ResumeOptions{SessionID: claudeResumePeerSessionID, OpenOptions: agent.OpenOptions{Cwd: cwd, ProcessEnv: overlay}})
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)
			for _, path := range []string{versionPath, sessionPath} {
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				var env map[string]string
				require.NoError(t, json.Unmarshal(data, &env))
				assert.Equal(t, want, env)
			}
			require.NoError(t, session.Close(ctx))
		})
	}
}

func TestEnvironmentDiscoveryUsesEffectiveProcessEnvironment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd, baseDir, effectiveDir := t.TempDir(), t.TempDir(), t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames, `<{"type":"system","subtype":"init","session_id":"`+claudeResumePeerSessionID+`","cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.293"}`)
	peer := prepareClaudePeer(t, "passed", frames...)
	var native *claude.Session
	provider := agentclaude.NewWithStream(agentclaude.Config{Env: []string{"CLAUDE_CONFIG_DIR=" + baseDir}}, func(ctx context.Context, options claude.SessionOptions) (agentclaude.TestStreamOpen, error) {
		args, err := agentclaude.TestifyNativeArgs(options)
		if err != nil {
			return agentclaude.TestStreamOpen{}, err
		}
		run, err := peer.NewRun(ctx, mocktest.RunOptions{NativeArgs: args})
		if err != nil {
			return agentclaude.TestStreamOpen{}, err
		}
		t.Cleanup(func() {
			_ = run.CloseSDK()
			waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = run.Wait(waitCtx)
		})
		// Keep the adapter environment intact: the stream has no process to inject
		// it into, but native discovery must use exactly this effective snapshot.
		return agentclaude.TestStreamOpen{
			Transport: claude.StreamTransport{Reader: run.Reader, Writer: run.Writer, Stderr: run.Stderr, Wait: run.WaitError},
			Env:       options.Env, Started: func(session *claude.Session) {
				native = session
				completion := run.CloseInputWhenComplete(session.CloseInput)
				t.Cleanup(func() {
					_ = session.Close()
					select {
					case err := <-completion:
						if err != nil {
							assert.ErrorIs(t, err, claude.ErrInputClosed)
						}
					case <-time.After(5 * time.Second):
						t.Error("input completion watcher did not finish")
					}
				})
			},
		}, nil
	})
	agentclaude.SetSessionIDGeneratorForTest(provider, func() (string, error) {
		return claudeResumePeerSessionID, nil
	})
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd, ProcessEnv: map[string]string{"CLAUDE_CONFIG_DIR": effectiveDir}})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	require.NotNil(t, native)
	page, err := native.ListSubagents(ctx, claude.SubagentQuery{})
	// Both test roots are intentionally empty; the scope must still identify the
	// overlay directory rather than falling back to the provider directory.
	assert.ErrorIs(t, err, claude.ErrDiscoveryCoverage)
	canonical, err := filepath.EvalSymlinks(effectiveDir)
	require.NoError(t, err)
	assert.Equal(t, canonical, page.Coverage.ConfigDir)
	require.NoError(t, session.Close(ctx))
}

func TestEnvironmentInvalidProcessEnvRejectedBeforeLaunch(t *testing.T) {
	provider := agentclaude.New(agentclaude.Config{CLIPath: "/does-not-exist"})
	for _, env := range []map[string]string{{"Path": "secret-env-value", "PATH": "other-secret"}, {"TOKEN": "secret-env-value\x00"}} {
		options := agent.OpenOptions{Cwd: t.TempDir(), ProcessEnv: env}
		_, err := provider.Open(context.Background(), options)
		require.Error(t, err)
		assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
		assert.NotContains(t, err.Error(), "secret-env-value")
		_, err = provider.Resume(context.Background(), agent.ResumeOptions{SessionID: claudeResumePeerSessionID, OpenOptions: options})
		require.Error(t, err)
		assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
		assert.NotContains(t, err.Error(), "secret-env-value")
	}
}
