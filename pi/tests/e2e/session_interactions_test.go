//go:build darwin || linux

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentpi "github.com/zoumo/agent-sdk-go/agent/pi"
)

func TestOfficialPiInteractions(t *testing.T) {
	cli := os.Getenv("AGENT_SDK_TEST_PI_CLI")
	if cli == "" {
		t.Skip("set AGENT_SDK_TEST_PI_CLI to the absolute Pi 1.6.0 executable")
	}
	require.True(t, filepath.IsAbs(cli))
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, cli, "--version").Output()
	require.NoError(t, err)
	require.Equal(t, "1.6.0", strings.TrimSpace(string(version)))
	server := httptest.NewServer(http.HandlerFunc(serveInteractionProbe))
	t.Cleanup(server.Close)
	agentDir, cwd := t.TempDir(), t.TempDir()
	models := fmt.Sprintf(`{"providers":{"probe":{"api":"openai-completions","baseUrl":%q,"apiKey":"test-only","models":[{"id":"probe","reasoning":true},{"id":"alternate","reasoning":true}]}}}`, server.URL)
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600))
	launcher := filepath.Join(t.TempDir(), "pi-launch")
	require.NoError(t, os.WriteFile(launcher, []byte(`#!/bin/sh
cli="$1"
shift
exec "$cli" --offline --no-skills --no-context-files --no-prompt-templates "$@"
`), 0o700))
	core := agentpi.New(agentpi.Config{
		CLIPath: launcher, Args: []string{cli}, Provider: "probe", SessionDir: t.TempDir(),
		Env: []string{"HOME=" + t.TempDir(), "PI_CODING_AGENT_DIR=" + agentDir},
	})
	_, err = core.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	options := agent.OpenOptions{
		Cwd: cwd, Features: agent.Features{AgentQuestions: new(true)}, Config: agent.SessionConfig{Model: "probe", Effort: "off"},
	}

	rawOptions := agentpi.SessionOptions{ToolApproval: true, InteractionTimeout: 15 * time.Second}
	session, err := core.OpenWithOptions(ctx, options, rawOptions)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	require.True(t, session.Capabilities().Supports(agent.CapabilityPermission))
	require.True(t, session.Capabilities().Supports(agent.CapabilityForm))
	require.True(t, session.EffectiveFeatures().AgentQuestions)
	events, err := session.Events()
	require.NoError(t, err)
	for _, tc := range []struct {
		operation, decision, file, value string
	}{
		{"write-allow", "allow", "write-allow.txt", ""},
		{"write-deny", "deny", "write-deny.txt", ""},
		{"bash-allow", "allow", "bash-allow.txt", ""},
		{"bash-deny", "deny", "bash-deny.txt", ""},
		{"select", "", "", "Green"},
		{"input", "", "", "blue sky"},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			request := beginInteraction(t, ctx, session, events, tc.operation)
			var answer agent.Answer
			if tc.decision != "" {
				require.Equal(t, agent.InteractionPermission, request.Request.Kind)
				require.NotEmpty(t, request.Request.ToolCallID)
				assert.Equal(t, []string{"allow", "deny"}, []string{request.Request.Options[0].OptionID, request.Request.Options[1].OptionID})
				if tc.operation == "write-allow" {
					checkControlsWhilePending(t, ctx, session, events)
				}
				_, err := session.Answer(ctx, request.Interaction, agent.FormAnswer{Outcome: agent.FormAccept, Content: map[string]any{"value": "wrong kind"}})
				require.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
				_, err = session.Answer(ctx, request.Interaction, agent.PermissionAnswer{Outcome: agent.PermissionSelected, OptionID: "not-offered"})
				require.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
				answer = agent.PermissionAnswer{Outcome: agent.PermissionSelected, OptionID: tc.decision}
			} else {
				require.Equal(t, agent.InteractionForm, request.Request.Kind)
				assert.Equal(t, "Choose an answer", request.Request.Message)
				properties, ok := request.Request.RequestedSchema["properties"].(map[string]any)
				require.True(t, ok)
				value, ok := properties["value"].(map[string]any)
				require.True(t, ok)
				if tc.operation == "select" {
					require.Equal(t, []string{"Red", "Green"}, value["enum"])
					_, err := session.Answer(ctx, request.Interaction, agent.FormAnswer{Outcome: agent.FormAccept, Content: map[string]any{"value": "not-offered"}})
					require.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
				} else {
					require.NotContains(t, value, "enum")
				}
				answer = agent.FormAnswer{Outcome: agent.FormAccept, Content: map[string]any{"value": tc.value}}
			}
			_, err := session.Answer(ctx, "other-session", answer)
			require.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
			if tc.operation == "select" {
				checkConcurrentAnswers(t, ctx, session, request.Interaction, answer)
			} else {
				receipt, err := session.Answer(ctx, request.Interaction, answer)
				require.NoError(t, err)
				require.Equal(t, request.Interaction, receipt.Interaction)
				require.Equal(t, new(true), receipt.Acceptance.Sent)
				require.Nil(t, receipt.Acceptance.NativeAccepted)
			}
			result := finishInteraction(t, ctx, events, request.Interaction, agent.InteractionAnswered)
			if tc.decision == "deny" {
				require.Equal(t, "failed", result.Status)
				require.Contains(t, toolOutput(t, result), "Agent SDK tool approval denied")
				require.NoFileExists(t, filepath.Join(cwd, tc.file))
			} else {
				require.Equal(t, "completed", result.Status)
				if tc.file != "" {
					data, err := os.ReadFile(filepath.Join(cwd, tc.file))
					require.NoError(t, err)
					assert.Equal(t, "approved", string(data))
				} else {
					require.Contains(t, toolOutput(t, result), tc.value)
				}
			}
			_, err = session.Answer(ctx, request.Interaction, answer)
			require.Error(t, err)
		})
	}
	require.NoError(t, session.Close(ctx))

	// A separate native timer sends no UI answer. The tool result observes the
	// expiry and confirms that the guarded command did not run.
	timeoutSession, err := core.OpenWithOptions(ctx, options, agentpi.SessionOptions{ToolApproval: true, InteractionTimeout: 700 * time.Millisecond})
	require.NoError(t, err)
	t.Cleanup(func() { _ = timeoutSession.Close(context.Background()) })
	timeoutEvents, err := timeoutSession.Events()
	require.NoError(t, err)
	timedOut := beginInteraction(t, ctx, timeoutSession, timeoutEvents, "bash-timeout")
	result := finishInteraction(t, ctx, timeoutEvents, timedOut.Interaction, agent.InteractionExpired)
	assert.Contains(t, toolOutput(t, result), "Agent SDK tool approval expired")
	assert.NoFileExists(t, filepath.Join(cwd, "bash-timeout.txt"))
	_, err = timeoutSession.Answer(ctx, timedOut.Interaction, agent.PermissionAnswer{Outcome: agent.PermissionSelected, OptionID: "allow"})
	require.Error(t, err)
	require.NoError(t, timeoutSession.Close(ctx))

	closing, err := core.OpenWithOptions(ctx, options, rawOptions)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closing.Close(context.Background()) })
	closeEvents, err := closing.Events()
	require.NoError(t, err)
	unanswered := beginInteraction(t, ctx, closing, closeEvents, "bash-close")
	require.NoError(t, closing.Close(ctx))
	closed := false
	for {
		event, err := closeEvents.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if payload, ok := event.Payload.(agent.InteractionClosedPayload); ok {
			require.Equal(t, unanswered.Interaction, payload.Interaction)
			require.Equal(t, agent.InteractionCancelled, payload.Outcome)
			require.False(t, closed)
			closed = true
		}
	}
	require.True(t, closed)
	require.NoFileExists(t, filepath.Join(cwd, "bash-close.txt"))
	_, err = closing.Answer(ctx, unanswered.Interaction, agent.PermissionAnswer{Outcome: agent.PermissionSelected, OptionID: "allow"})
	require.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
	require.NoError(t, core.Stop(ctx))
}

func checkControlsWhilePending(t *testing.T, ctx context.Context, session agent.Session, events agent.EventStream) {
	t.Helper()
	controller, ok := session.(agent.ConfigController)
	require.True(t, ok)
	receipt, err := controller.SetConfig(ctx, agent.ConfigChange{ID: agent.ConfigEffort, Value: "low"})
	require.NoError(t, err)
	require.Equal(t, new(true), receipt.Acceptance.NativeAccepted)
	for {
		event, err := events.Next(ctx)
		require.NoError(t, err)
		require.NotEqual(t, agent.EventInteractionClosed, event.Type())
		if payload, ok := event.Payload.(agent.UnsupportedNativeEventPayload); ok && strings.Contains(string(payload.Raw), `"type":"thinking_level_changed"`) {
			require.JSONEq(t, `{"type":"thinking_level_changed","level":"low"}`, string(payload.Raw))
			break
		}
	}
	receipt, err = controller.SetConfig(ctx, agent.ConfigChange{ID: agent.ConfigModel, Value: "alternate"})
	require.NoError(t, err)
	require.Equal(t, new(true), receipt.Acceptance.NativeAccepted)
	t.Log("Observed a native thinking_level_changed frame and a separate set_model acknowledgement while the approval was pending")
}

func checkConcurrentAnswers(t *testing.T, ctx context.Context, session agent.Session, ref agent.InteractionRef, answer agent.Answer) {
	t.Helper()
	replies := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := session.Answer(ctx, ref, answer)
			replies <- err
		}()
	}
	first, second := <-replies, <-replies
	if first != nil {
		first, second = second, first
	}
	require.NoError(t, first)
	require.Error(t, second)
}

func beginInteraction(t *testing.T, ctx context.Context, session agent.Session, stream agent.EventStream, operation string) agent.InteractionRequestedPayload {
	t.Helper()
	receipt, err := session.StartOrSteer(ctx, agent.Input{Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: operation}}}})
	require.NoError(t, err)
	require.Equal(t, agent.InputAppliedNewWork, receipt.Result)
	requiresAction := false
	for {
		event, err := stream.Next(ctx)
		require.NoError(t, err)
		switch payload := event.Payload.(type) {
		case agent.StateUpdatePayload:
			require.Nil(t, payload.Error)
			if payload.State != nil {
				require.NotEqual(t, agent.StateIdle, *payload.State, "session settled before interaction request")
				requiresAction = requiresAction || *payload.State == agent.StateRequiresAction
			}
		case agent.InteractionRequestedPayload:
			require.True(t, requiresAction)
			require.True(t, payload.Blocking)
			meta, ok := event.Meta["pi"].(map[string]any)
			require.True(t, ok)
			id, ok := meta["nativeRequestId"].(string)
			require.True(t, ok)
			require.NotEmpty(t, id)
			require.NotEqual(t, id, string(payload.Interaction))
			return payload
		}
	}
}

func finishInteraction(t *testing.T, ctx context.Context, stream agent.EventStream, ref agent.InteractionRef, outcome agent.InteractionOutcome) agent.ToolResultPayload {
	t.Helper()
	var result agent.ToolResultPayload
	closed := false
	for {
		event, err := stream.Next(ctx)
		require.NoError(t, err)
		switch payload := event.Payload.(type) {
		case agent.InteractionClosedPayload:
			require.Equal(t, ref, payload.Interaction)
			require.Equal(t, outcome, payload.Outcome)
			require.False(t, closed)
			closed = true
		case agent.ToolResultPayload:
			result = payload
		case agent.StateUpdatePayload:
			require.Nil(t, payload.Error)
			if payload.State != nil && *payload.State == agent.StateIdle {
				require.True(t, closed)
				require.NotEmpty(t, result.ID)
				return result
			}
		}
	}
}

func toolOutput(t *testing.T, result agent.ToolResultPayload) string {
	t.Helper()
	require.NotNil(t, result.RawOutput)
	raw, err := json.Marshal(*result.RawOutput)
	require.NoError(t, err)
	return string(raw)
}

func serveInteractionProbe(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil ||
		(request.Model != "probe" && request.Model != "alternate") || len(request.Messages) == 0 ||
		r.URL.Path != "/chat/completions" {
		http.Error(w, "invalid local probe request", http.StatusBadRequest)
		return
	}
	deliver := map[string]any{"role": "assistant", "content": "Probe complete"}
	finish := "stop"
	last := request.Messages[len(request.Messages)-1]
	if last.Role == "user" {
		operation := probeText(last.Content)
		name, args := probeTool(operation)
		if name == "" {
			http.Error(w, "invalid probe tool", http.StatusBadRequest)
			return
		}
		deliver = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
			"index": 0, "id": "call_" + strings.ReplaceAll(operation, "-", "_"), "type": "function",
			"function": map[string]string{"name": name, "arguments": args},
		}}}
		finish = "tool_calls"
	}
	chunk, err := json.Marshal(map[string]any{
		"id": "controlled-interactions-chunk", "object": "chat.completion.chunk", "created": 1, "model": request.Model,
		"choices": []any{map[string]any{"index": 0, "delta": deliver, "finish_reason": finish}},
	})
	if err != nil {
		http.Error(w, "invalid probe response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
}

func probeText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var parts []struct{ Text string }
	if json.Unmarshal(raw, &parts) == nil && len(parts) == 1 {
		return parts[0].Text
	}
	return ""
}

func probeTool(operation string) (name, args string) {
	switch operation {
	case "write-allow", "write-deny":
		return "write", fmt.Sprintf(`{"path":%q,"content":"approved"}`, operation+".txt")
	case "bash-allow", "bash-deny", "bash-timeout", "bash-close":
		return "bash", fmt.Sprintf(`{"command":%q}`, "printf approved > "+operation+".txt")
	case "select":
		return "agent_sdk_ask", `{"question":"Choose an answer","choices":["Red","Green"]}`
	case "input":
		return "agent_sdk_ask", `{"question":"Choose an answer"}`
	default:
		return "", ""
	}
}
