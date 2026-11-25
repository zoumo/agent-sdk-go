package e2e_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
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
	"github.com/zoumo/agent-sdk-go/pi"
)

func TestOfficialPiSilentInject(t *testing.T) {
	executable := os.Getenv("PI_E2E_CLI")
	if executable == "" {
		t.Skip("set PI_E2E_CLI to the absolute Pi 1.6.0 executable to run the loopback-only test")
	}
	require.True(t, filepath.IsAbs(executable))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, executable, "--version").Output()
	require.NoError(t, err)
	require.Equal(t, "1.6.0", strings.TrimSpace(string(version)))
	for _, mode := range []string{"finish", "interrupt", "continue"} {
		t.Run(mode, func(t *testing.T) {
			testSilentInject(t, executable, mode)
		})
	}
}

func testSilentInject(t *testing.T, executable, mode string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cwd, configDir, sessionDir := t.TempDir(), t.TempDir(), t.TempDir()
	release := make(chan struct{})
	calls := make(chan string, 8)
	server := httptest.NewServer(injectModel(calls, release, ctx.Done()))
	t.Cleanup(server.Close)
	config, err := json.Marshal(map[string]any{
		"providers": map[string]any{"localinject": map[string]any{
			"api": "openai-completions", "apiKey": "loopback-only", "baseUrl": server.URL + "/v1",
			"models": []map[string]string{{"id": "fixture"}},
		}},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "models.json"), config, 0o600))
	core := agentpi.New(agentpi.Config{
		CLIPath: executable, Provider: "localinject", SessionDir: sessionDir, EnableInject: true,
		Env: []string{"PI_OFFLINE=1", "HOME=" + configDir, "PI_CODING_AGENT_DIR=" + configDir, "NO_PROXY=127.0.0.1"},
	})
	_, err = core.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, core.Stop(context.Background())) })
	session, err := core.Open(ctx, agent.OpenOptions{
		Cwd: cwd, Config: agent.SessionConfig{Model: "fixture"},
		Features: agent.Features{RestrictLocalTools: true},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close(context.Background())) })
	injector, ok := session.(agent.Injector)
	require.True(t, ok)
	stream, err := session.Events()
	require.NoError(t, err)
	scope := pi.HistoryScope{Cwd: cwd, SessionDir: sessionDir}

	submit(t, ctx, session, "SEED_REQUEST", agent.InputAppliedNewWork)
	assert.Contains(t, readModelCall(t, ctx, calls), "SEED_REQUEST")
	seed := collectUntilIdle(t, ctx, stream)
	assert.Contains(t, seed.assistantText, "SEED_RESULT")
	if mode == "finish" {
		// Pi acknowledges routing even when a slash command handler throws.
		submit(t, ctx, session, "/agent-sdk-inject invalid-json", agent.InputAppliedBestEffort)
		for {
			ev, err := stream.Next(ctx)
			require.NoError(t, err)
			if failure, ok := ev.Payload.(agent.SessionErrorPayload); ok {
				require.Equal(t, "pi_inject_extension_failed", failure.Reason)
				break
			}
		}
		expectNoModelCall(t, calls)
	}

	idleFact := "SDK_IDLE_" + rand.Text()
	inject(t, ctx, injector, "FACT="+idleFact)
	require.True(t, hasSavedInjection(t, ctx, scope, session.ID(), idleFact))
	for {
		ev, err := stream.Next(ctx)
		require.NoError(t, err)
		assert.NotEqual(t, agent.EventAgentMessage, ev.Type())
		if change, ok := ev.Payload.(agent.StateUpdatePayload); ok {
			require.NotEqual(t, new(agent.StateRunning), change.State)
		}
		if hasNativeMessage(ev, idleFact) {
			break
		}
	}
	expectNoModelCall(t, calls)
	submit(t, ctx, session, "READ_IDLE_FACT", agent.InputAppliedNewWork)
	require.Contains(t, readModelCall(t, ctx, calls), idleFact)
	idleResult := collectUntilIdle(t, ctx, stream)
	assert.Equal(t, idleFact, idleResult.assistantText)

	submit(t, ctx, session, "BEGIN_ACTIVE_REQUEST", agent.InputAppliedNewWork)
	first := readModelCall(t, ctx, calls)
	for {
		ev, err := stream.Next(ctx)
		require.NoError(t, err)
		if chunk, ok := ev.Payload.(agent.MessageChunk); ok && len(chunk.Content) == 1 &&
			chunk.Content[0].Text != nil && chunk.Content[0].Text.Text == "ACTIVE_PARTIAL" {
			break
		}
	}
	activeFact := "SDK_ACTIVE_" + rand.Text()
	inject(t, ctx, injector, "FACT="+activeFact)
	require.NotContains(t, first, activeFact)
	require.False(t, hasSavedInjection(t, ctx, scope, session.ID(), activeFact))
	expectNoModelCall(t, calls)
	switch mode {
	case "interrupt":
		receipt, err := session.Interrupt(ctx, agent.InterruptOptions{})
		require.NoError(t, err)
		require.Equal(t, agent.InterruptAcknowledged, receipt.Result)
	case "continue":
		submit(t, ctx, session, "CONTINUE_ACTIVE_REQUEST", agent.InputAppliedBestEffort)
		close(release)
		assert.Contains(t, readModelCall(t, ctx, calls), activeFact)
	default:
		close(release)
	}
	result := collectUntilIdle(t, ctx, stream)
	require.Equal(t, []string{"turn_end", "injected"}, result.turnAndInjectionOrder(activeFact))
	require.True(t, hasSavedInjection(t, ctx, scope, session.ID(), activeFact))
	switch mode {
	case "interrupt":
		assert.Equal(t, "aborted", result.stopReason)
		assert.NotContains(t, result.assistantText, activeFact)
	case "continue":
		assert.Contains(t, result.assistantText, activeFact)
	default:
		assert.NotContains(t, result.assistantText, activeFact)
	}
	expectNoModelCall(t, calls)
	submit(t, ctx, session, "READ_ACTIVE_FACT", agent.InputAppliedNewWork)
	require.Contains(t, readModelCall(t, ctx, calls), activeFact)
	final := collectUntilIdle(t, ctx, stream)
	assert.Equal(t, activeFact, final.assistantText)
	require.NoError(t, session.Close(ctx))
}

func injectModel(calls chan<- string, release, done <-chan struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var request struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var users []string
		for _, msg := range request.Messages {
			if msg.Role != "user" {
				continue
			}
			var blocks []struct{ Text string }
			if json.Unmarshal(msg.Content, &blocks) == nil {
				for _, block := range blocks {
					users = append(users, block.Text)
				}
				continue
			}
			var message string
			if json.Unmarshal(msg.Content, &message) == nil {
				users = append(users, message)
			}
		}
		select {
		case calls <- strings.Join(users, "\n"):
		case <-r.Context().Done():
			return
		case <-done:
			return
		}
		if len(users) == 0 {
			http.Error(w, "missing user request", http.StatusBadRequest)
			return
		}
		var reply, query string
		for _, user := range users {
			if !strings.HasPrefix(user, "FACT=") {
				query = user
			}
		}
		hold := query == "BEGIN_ACTIVE_REQUEST"
		switch query {
		case "SEED_REQUEST":
			reply = "SEED_RESULT"
		case "BEGIN_ACTIVE_REQUEST":
			reply = "ACTIVE_PARTIAL"
		case "READ_IDLE_FACT", "READ_ACTIVE_FACT", "CONTINUE_ACTIVE_REQUEST":
			prefix := "FACT=SDK_ACTIVE_"
			if query == "READ_IDLE_FACT" {
				prefix = "FACT=SDK_IDLE_"
			}
			reply = "missing-injected-fact"
			for _, user := range users {
				if strings.HasPrefix(user, prefix) {
					reply = strings.TrimPrefix(user, "FACT=")
				}
			}
		default:
			http.Error(w, "unexpected query", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"id\":\"local\",\"created\":1,\"object\":\"chat.completion.chunk\",\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":%q},\"finish_reason\":null}]}\n\n", reply)
		if err := http.NewResponseController(w).Flush(); err != nil {
			return
		}
		if hold {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			case <-done:
				return
			}
		}
		_, _ = io.WriteString(w, "data: {\"id\":\"local\",\"created\":1,\"object\":\"chat.completion.chunk\",\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	})
}

func input(text string) agent.Input {
	return agent.Input{Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: text}}}}
}

func submit(t *testing.T, ctx context.Context, s agent.Session, text string, want agent.InputResult) {
	t.Helper()
	got, err := s.StartOrSteer(ctx, input(text))
	require.NoError(t, err)
	require.Equal(t, want, got.Result)
}

func inject(t *testing.T, ctx context.Context, injector agent.Injector, text string) {
	t.Helper()
	got, err := injector.Inject(ctx, input(text))
	require.NoError(t, err)
	require.Equal(t, agent.Acceptance{Sent: new(true), NativeAccepted: new(true)}, got.Acceptance)
}

func readModelCall(t *testing.T, ctx context.Context, calls <-chan string) string {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return ""
	}
}

func expectNoModelCall(t *testing.T, calls <-chan string) {
	t.Helper()
	select {
	case extra := <-calls:
		t.Fatalf("unexpected model request: %s", extra)
	default:
	}
}

func hasSavedInjection(t *testing.T, ctx context.Context, scope pi.HistoryScope, id, fact string) bool {
	t.Helper()
	history, err := pi.ReadSessionHistory(ctx, scope, id, pi.HistoryWindow{})
	require.NoError(t, err)
	for _, entry := range history.Entries {
		raw := string(entry.Raw)
		if entry.Type == "custom_message" && strings.Contains(raw, `"customType":"agent-sdk-inject"`) && strings.Contains(raw, fact) {
			return true
		}
	}
	return false
}

func nativeEvent(event agent.Event) string {
	if payload, ok := event.Payload.(agent.UnsupportedNativeEventPayload); ok {
		return string(payload.Raw)
	}
	return ""
}

func hasNativeMessage(event agent.Event, fact string) bool {
	raw := nativeEvent(event)
	return strings.Contains(raw, `"type":"message_end"`) && strings.Contains(raw, `"customType":"agent-sdk-inject"`) && strings.Contains(raw, fact)
}

type outcome struct {
	events        []agent.Event
	assistantText string
	stopReason    string
}

func collectUntilIdle(t *testing.T, ctx context.Context, stream agent.EventStream) outcome {
	t.Helper()
	var result outcome
	var assistant strings.Builder
	for {
		ev, err := stream.Next(ctx)
		require.NoError(t, err)
		result.events = append(result.events, ev)
		switch event := ev.Payload.(type) {
		case agent.AgentMessage:
			for _, block := range event.Content {
				if block.Text != nil {
					assistant.WriteString(block.Text.Text)
				}
			}
		case agent.StateUpdatePayload:
			if event.State != nil && *event.State == agent.StateIdle {
				result.assistantText = assistant.String()
				result.stopReason = event.StopReason
				return result
			}
		}
	}
}

func (o outcome) turnAndInjectionOrder(fact string) []string {
	var result []string
	for _, ev := range o.events {
		if hasNativeMessage(ev, fact) {
			return append(result, "injected")
		}
		if strings.Contains(nativeEvent(ev), `"type":"turn_end"`) {
			result = append(result, "turn_end")
		}
	}
	return result
}
