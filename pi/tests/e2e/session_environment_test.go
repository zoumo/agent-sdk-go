//go:build darwin || linux

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentpi "github.com/zoumo/agent-sdk-go/agent/pi"
)

const controlledModel = "auto/deepseek-v4.1-flash-1017"

// TestOfficialPiSessionEnvironment uses the installed Pi against a loopback
// completion server. The latter emits a fixed tool call and never calls a model.
func TestOfficialPiSessionEnvironment(t *testing.T) {
	cli := os.Getenv("AGENT_SDK_TEST_PI_CLI")
	if cli == "" {
		t.Skip("set AGENT_SDK_TEST_PI_CLI to the absolute Pi 1.6.0 executable")
	}
	require.True(t, filepath.IsAbs(cli))
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, cli, "--version").Output()
	require.NoError(t, err)
	require.Equal(t, "1.6.0", strings.TrimSpace(string(version)))

	server := httptest.NewServer(http.HandlerFunc(serveEnvironmentProbe))
	t.Cleanup(server.Close)
	root, cwd, home, agentDir, sessionDir := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("SDK_PI_PROBE_INHERITED", "inherited")
	t.Setenv("SDK_PI_PROBE_PROCESS", "host")
	t.Setenv("SDK_PI_PROBE_SHELL", "outside")
	t.Setenv("TMPDIR", root)
	models := fmt.Sprintf(`{"providers":{"magpie":{"api":"openai-completions","baseUrl":%q,"apiKey":"$SDK_PI_PROBE_PROCESS","models":[{"id":%q}]}}}`, server.URL, controlledModel)
	settings := `{"shellPath":"/bin/bash","shellCommandPrefix":"export SDK_PI_PROBE_PREFIX=configured"}`
	auth := `{}`
	for file, content := range map[string]string{"models.json": models, "settings.json": settings, "auth.json": auth} {
		require.NoError(t, os.WriteFile(filepath.Join(agentDir, file), []byte(content), 0o600))
	}
	launcher := filepath.Join(root, "pi-launch")
	require.NoError(t, os.WriteFile(launcher, []byte(`#!/bin/sh
cli="$1"
shift
printf '%s' "$$" > "$SDK_PI_PROBE_PIDFILE"
exec "$cli" --offline --no-skills --no-context-files --no-prompt-templates "$@"
`), 0o700))
	core := agentpi.New(agentpi.Config{
		CLIPath: launcher, Args: []string{cli}, Provider: "magpie", SessionDir: sessionDir,
		Env: []string{"HOME=" + home, "PI_CODING_AGENT_DIR=" + agentDir, "SDK_PI_PROBE_PROCESS=core"},
	})
	_, err = core.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	opts := func(label string) agent.OpenOptions {
		return agent.OpenOptions{
			Cwd: cwd, Config: agent.SessionConfig{Model: controlledModel},
			ProcessEnv: map[string]string{
				"SDK_PI_PROBE_PROCESS": "token-" + label,
				"SDK_PI_PROBE_PIDFILE": filepath.Join(root, label+".pid"),
			},
			ShellEnv: map[string]string{
				"SDK_PI_PROBE_PROCESS": "shell-" + label,
				"SDK_PI_PROBE_SHELL":   "only-" + label,
			},
		}
	}
	a, err := core.Open(ctx, opts("alpha"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close(context.Background()) })
	b, err := core.Open(ctx, opts("beta"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	aPID, bPID := readPiPID(t, root, "alpha"), readPiPID(t, root, "beta")
	require.NotEqual(t, aPID, bPID)
	require.NotEqual(t, a.ID(), b.ID())
	assertPiExtensionCount(t, root, 2)
	aEvents, err := a.Events()
	require.NoError(t, err)
	bEvents, err := b.Events()
	require.NoError(t, err)
	runEnvironmentProbe(t, ctx, a, aEvents, "alpha")
	runEnvironmentProbe(t, ctx, b, bEvents, "beta")
	require.NoError(t, a.Close(ctx))
	assert.ErrorIs(t, syscall.Kill(aPID, 0), syscall.ESRCH)
	require.NoError(t, syscall.Kill(bPID, 0))
	assertPiExtensionCount(t, root, 1)
	runEnvironmentProbe(t, ctx, b, bEvents, "beta")

	resumed, err := core.Resume(ctx, agent.ResumeOptions{SessionID: a.ID(), OpenOptions: opts("resume")})
	require.NoError(t, err)
	t.Cleanup(func() { _ = resumed.Close(context.Background()) })
	resumedPID := readPiPID(t, root, "resume")
	require.NotEqual(t, aPID, resumedPID)
	require.NotEqual(t, bPID, resumedPID)
	require.Equal(t, a.ID(), resumed.ID())
	assertPiExtensionCount(t, root, 2)
	resumedEvents, err := resumed.Events()
	require.NoError(t, err)
	runEnvironmentProbe(t, ctx, resumed, resumedEvents, "resume")
	require.NoError(t, resumed.Close(ctx))
	require.NoError(t, b.Close(ctx))
	assert.ErrorIs(t, syscall.Kill(resumedPID, 0), syscall.ESRCH)
	assert.ErrorIs(t, syscall.Kill(bPID, 0), syscall.ESRCH)
	assertPiExtensionCount(t, root, 0)
	require.NoError(t, core.Stop(ctx))

	assert.Equal(t, "host", os.Getenv("SDK_PI_PROBE_PROCESS"))
	assert.Equal(t, "outside", os.Getenv("SDK_PI_PROBE_SHELL"))
	for file, content := range map[string]string{"models.json": models, "settings.json": settings, "auth.json": auth} {
		after, err := os.ReadFile(filepath.Join(agentDir, file))
		require.NoError(t, err)
		assert.Equal(t, content, string(after))
	}
	t.Logf("Pi %s local loopback. Distinct Open PIDs %d, %d and Resume PID %d", strings.TrimSpace(string(version)), aPID, bPID, resumedPID)
}

func runEnvironmentProbe(t *testing.T, ctx context.Context, session agent.Session, stream agent.EventStream, label string) {
	t.Helper()
	receipt, err := session.StartOrSteer(ctx, agent.Input{Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "environment probe"}}}})
	require.NoError(t, err)
	require.Equal(t, agent.InputAppliedNewWork, receipt.Result)
	toolOK := false
	for {
		ev, err := stream.Next(ctx)
		require.NoError(t, err)
		switch payload := ev.Payload.(type) {
		case agent.ToolResultPayload:
			if payload.ToolName != "bash" {
				continue
			}
			require.Equal(t, "completed", payload.Status)
			require.NotNil(t, payload.RawOutput)
			raw, err := json.Marshal(*payload.RawOutput)
			require.NoError(t, err)
			require.Contains(t, string(raw), "shell-"+label+"|only-"+label+"|inherited|configured")
			toolOK = true
		case agent.StateUpdatePayload:
			if payload.State != nil && *payload.State == agent.StateIdle {
				require.Nil(t, payload.Error)
				require.True(t, toolOK)
				return
			}
		}
	}
}

func assertPiExtensionCount(t *testing.T, root string, count int) {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(root, "agent-sdk-pi-*"))
	require.NoError(t, err)
	assert.Len(t, dirs, count)
}

func readPiPID(t *testing.T, root, label string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, label+".pid"))
	require.NoError(t, err)
	pid, err := strconv.Atoi(string(data))
	require.NoError(t, err)
	require.Positive(t, pid)
	return pid
}

func serveEnvironmentProbe(w http.ResponseWriter, r *http.Request) {
	type message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	var request struct {
		Model    string    `json:"model"`
		Messages []message `json:"messages"`
	}
	labels := []string{"alpha", "beta", "resume"}
	auth, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer token-")
	if !ok || !slices.Contains(labels, auth) || r.URL.Path != "/chat/completions" {
		http.Error(w, "unexpected route or process environment", http.StatusBadRequest)
		return
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Model != controlledModel || len(request.Messages) == 0 {
		http.Error(w, "unexpected request format or model", http.StatusBadRequest)
		return
	}
	delta := map[string]any{"role": "assistant"}
	finish := "stop"
	if request.Messages[len(request.Messages)-1].Role != "tool" {
		delta["tool_calls"] = []any{map[string]any{
			"index": 0, "id": "call_environment_probe", "type": "function",
			"function": map[string]string{"name": "bash", "arguments": `{"command":"printf '%s|%s|%s|%s' \"$SDK_PI_PROBE_PROCESS\" \"$SDK_PI_PROBE_SHELL\" \"$SDK_PI_PROBE_INHERITED\" \"$SDK_PI_PROBE_PREFIX\""}`},
		}}
		finish = "tool_calls"
	} else {
		want := "shell-" + auth + "|only-" + auth + "|inherited|configured"
		if !strings.Contains(string(request.Messages[len(request.Messages)-1].Content), want) {
			http.Error(w, "tool did not observe the matching shell overlay", http.StatusBadRequest)
			return
		}
		delta["content"] = "Probe completed"
	}
	chunk, err := json.Marshal(map[string]any{
		"id": "controlled-environment-chunk", "object": "chat.completion.chunk", "created": 1,
		"model": controlledModel, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
	})
	if err != nil {
		http.Error(w, "invalid probe response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
}
