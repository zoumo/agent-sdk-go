package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/internal/uuidv4"
)

func TestUnifiedOpenBeforeInput(t *testing.T) {
	cliPath, model, env := assignedSessionLiveEnvironment(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	provider := agentclaude.New(agentclaude.Config{CLIPath: cliPath, Env: env})
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = provider.Stop(context.Background()) })
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: t.TempDir(), Config: agent.SessionConfig{Model: model},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	initialID := session.ID()
	require.NotEmpty(t, initialID)
	t.Logf("Open before user input: session_id=%s", initialID)
	stream, err := session.Events()
	require.NoError(t, err)
	receipt, err := session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "Reply with SDK67_ACP_OK only. Do not use tools."}}},
	})
	require.NoError(t, err)
	require.NotNil(t, receipt.Acceptance.Sent)
	require.True(t, *receipt.Acceptance.Sent)
	require.Nil(t, receipt.Acceptance.NativeAccepted)
	seenAnswer := false
	for !seenAnswer {
		event, err := stream.Next(ctx)
		require.NoError(t, err)
		require.Equal(t, initialID, event.SessionID)
		if event.Type() == agent.EventAgentMessage {
			meta, ok := event.Meta["claude"].(map[string]any)
			require.True(t, ok)
			var native struct {
				SessionID string `json:"session_id"`
			}
			raw, ok := meta["raw"].(json.RawMessage)
			require.True(t, ok)
			require.NoError(t, json.Unmarshal(raw, &native))
			require.Equal(t, initialID, native.SessionID)
			seenAnswer = true
		}
		if payload, ok := event.Payload.(agent.SessionErrorPayload); ok {
			require.Equal(t, "native_event_unsupported", payload.Reason)
			meta, ok := event.Meta["claude"].(map[string]any)
			require.True(t, ok)
			t.Logf("unified mapping gap: type=%v subtype=%v", meta["type"], meta["subtype"])
		}
	}
	require.NoError(t, session.Close(ctx))
	require.NoError(t, session.Err())
	require.Equal(t, initialID, session.ID())
	require.NoError(t, provider.Stop(ctx))
	t.Log("one unified input produced a matching native assistant identity; Close and Stop succeeded")
}

func TestNativeAssignedSessionIdentityAcrossFirstTurn(t *testing.T) {
	cliPath, model, env := assignedSessionLiveEnvironment(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sessionID, err := uuidv4.New()
	require.NoError(t, err)
	session, err := claude.Open(ctx, claude.SessionOptions{
		CLIPath: cliPath, WorkDir: t.TempDir(), Env: env, Model: model, SessionID: sessionID,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	actual, known := session.SessionID()
	require.True(t, known)
	require.Equal(t, sessionID, actual)
	t.Logf("native Open before user input: session_id=%s", sessionID)
	submission, err := session.Submit(ctx, claude.Input{Text: "Reply with SDK67_ACP_OK only. Do not use tools."})
	require.NoError(t, err)
	require.True(t, submission.Written)
	seenInit, seenAssistant, seenResult := false, false, false
	for !seenResult {
		record, err := session.Receive(ctx)
		require.NoError(t, err)
		event := record.Event
		if event == nil {
			continue
		}
		if event.System != nil && event.Subtype == "init" {
			require.Equal(t, sessionID, event.SessionID)
			seenInit = true
		}
		if event.Assistant != nil {
			require.Equal(t, sessionID, event.SessionID)
			seenAssistant = true
		}
		if event.Result != nil {
			require.Equal(t, sessionID, event.SessionID)
			require.False(t, event.Result.IsError)
			seenResult = true
		}
	}
	require.True(t, seenInit)
	require.True(t, seenAssistant)
	require.NoError(t, session.Close())
	require.NoError(t, session.Err())
	t.Log("one native input completed: init, assistant and success result matched the assigned UUID; Close succeeded")
}

func assignedSessionLiveEnvironment(t *testing.T) (string, string, []string) {
	t.Helper()
	if os.Getenv("SDK_REAL_AGENT_E2E") != "1" {
		t.Skip("SDK_REAL_AGENT_E2E=1 is required for real model calls")
	}
	require.Equal(t, "1", os.Getenv("CLAUDE_E2E"))
	require.Empty(t, os.Getenv("CI"), "real Claude model calls are forbidden in CI")
	cliPath, model := os.Getenv("CLAUDE_E2E_CLI"), os.Getenv("CLAUDE_E2E_MODEL")
	require.True(t, filepath.IsAbs(cliPath))
	require.NotEmpty(t, model)
	binary, err := os.ReadFile(cliPath)
	require.NoError(t, err)
	digest := sha256.Sum256(binary)
	versionCtx, cancelVersion := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancelVersion()
	version, err := exec.CommandContext(versionCtx, cliPath, "--version").Output()
	require.NoError(t, err)
	t.Logf("CLI version=%s sha256=%s model=%s", strings.TrimSpace(string(version)), hex.EncodeToString(digest[:]), model)
	var settings struct {
		Env map[string]string `json:"env"`
	}
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	if data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json")); err == nil {
		require.NoError(t, json.Unmarshal(data, &settings))
	}
	isolatedHome := t.TempDir()
	env := []string{
		"HOME=" + isolatedHome, "PATH=" + os.Getenv("PATH"),
		"CLAUDE_CONFIG_DIR=" + filepath.Join(isolatedHome, ".claude"),
		"CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1",
	}
	for _, key := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"} {
		value := os.Getenv(key)
		if value == "" {
			value = settings.Env[key]
		}
		if value != "" {
			env = append(env, key+"="+value)
		}
	}
	for _, key := range []string{"ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
		env = append(env, key+"="+model)
	}
	return cliPath, model, env
}
