package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/pi"
)

func TestOfficialPiQueueModes(t *testing.T) {
	executable := os.Getenv("PI_E2E_CLI")
	if executable == "" {
		t.Skip("set PI_E2E_CLI to the absolute Pi 1.6.0 executable to run the loopback-only test")
	}
	require.True(t, filepath.IsAbs(executable))
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, executable, "--version").Output()
	require.NoError(t, err)
	require.Equal(t, "1.6.0", strings.TrimSpace(string(version)))

	for _, test := range []struct {
		mode  pi.QueueMode
		batch []int
	}{
		{pi.QueueAll, []int{2, 4}},
		{pi.QueueOneAtATime, []int{1, 2, 3, 4}},
	} {
		t.Run(string(test.mode), func(t *testing.T) {
			testQueueMode(t, executable, test.mode, test.batch)
		})
	}
}

func testQueueMode(t *testing.T, executable string, mode pi.QueueMode, batches []int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	s, calls := openControlledPi(t, ctx, executable)
	entryBefore, err := s.Entries(ctx, "")
	require.NoError(t, err)

	require.ErrorContains(t, s.SetFollowUpMode(ctx, "invalid"), "invalid queue mode")
	require.NoError(t, s.SetSteeringMode(ctx, mode))
	require.NoError(t, s.SetFollowUpMode(ctx, mode))
	start, err := s.Prompt(ctx, "SDK_INITIAL_INPUT", nil, "")
	require.NoError(t, err)
	require.Equal(t, "started", start.Disposition)
	assert.Contains(t, nextCall(t, ctx, calls), "SDK_INITIAL_INPUT")
	for {
		event, err := s.Receive(ctx)
		require.NoError(t, err)
		if event.Type == "message_update" && strings.Contains(string(event.Raw), "SDK_CONTROLLED_REPLY") {
			break
		}
	}
	pending := []string{"SDK_STEER_FIRST", "SDK_STEER_SECOND", "SDK_FOLLOW_FIRST", "SDK_FOLLOW_SECOND"}
	for _, input := range pending[:2] {
		result, err := s.Steer(ctx, input, nil)
		require.NoError(t, err)
		assert.Equal(t, "queued", result.Disposition)
	}
	for _, input := range pending[2:] {
		result, err := s.FollowUp(ctx, input, nil)
		require.NoError(t, err)
		assert.Equal(t, "queued", result.Disposition)
	}
	waiting, err := s.State(ctx)
	require.NoError(t, err)
	assert.True(t, waiting.IsStreaming)
	assert.Equal(t, mode, waiting.SteeringMode)
	assert.Equal(t, mode, waiting.FollowUpMode)
	assert.Equal(t, len(pending), waiting.PendingMessageCount)
	require.NoError(t, s.Abort(ctx))
	assert.Contains(t, string(receiveSettled(t, ctx, s)), `"aborted":true`)
	idle, err := s.State(ctx)
	require.NoError(t, err)
	assert.False(t, idle.IsStreaming)
	assert.Equal(t, len(pending), idle.PendingMessageCount)
	assert.Equal(t, waiting.SessionID, idle.SessionID)

	cursor := ""
	if len(entryBefore.Entries) != 0 {
		cursor = entryBefore.Entries[len(entryBefore.Entries)-1].ID
	}
	afterAbort, err := s.Entries(ctx, cursor)
	require.NoError(t, err)
	require.NotEmpty(t, afterAbort.Entries)
	cursor = afterAbort.Entries[len(afterAbort.Entries)-1].ID

	start, err = s.Prompt(ctx, "SDK_AFTER_ABORT", nil, "")
	require.NoError(t, err)
	require.Equal(t, "started", start.Disposition)
	for _, count := range batches {
		call := nextCall(t, ctx, calls)
		assert.Contains(t, call, "SDK_AFTER_ABORT")
		for _, delivered := range pending[:count] {
			assert.Contains(t, call, delivered)
		}
		for _, retained := range pending[count:] {
			assert.NotContains(t, call, retained)
		}
	}
	assert.Contains(t, string(receiveSettled(t, ctx, s)), `"aborted":false`)
	select {
	case extra := <-calls:
		t.Fatalf("received an additional request: %s", extra)
	default:
	}
	final, err := s.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, final.PendingMessageCount)
	assert.Equal(t, s.ID(), final.SessionID)

	entries, err := s.Entries(ctx, cursor)
	require.NoError(t, err)
	require.NotEmpty(t, entries.Entries)
	assert.Equal(t, new(cursor), entries.Entries[0].ParentID)
	end, err := s.Entries(ctx, entries.Entries[len(entries.Entries)-1].ID)
	require.NoError(t, err)
	assert.Empty(t, end.Entries)
	assert.Equal(t, entries.LeafID, end.LeafID)
	tree, err := s.Tree(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, tree.Roots)
	assert.Equal(t, end.LeafID, tree.LeafID)
	messages, err := s.Messages(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, messages)
	stats, err := s.Stats(ctx)
	require.NoError(t, err)
	assert.Equal(t, s.ID(), stats.SessionID)
	assert.Equal(t, int64(2+len(pending)), stats.UserMessages)
	levels, err := s.AvailableThinkingLevels(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"off"}, levels)
	models, err := s.AvailableModels(ctx)
	require.NoError(t, err)
	assert.True(t, slices.ContainsFunc(models, func(model json.RawMessage) bool {
		return strings.Contains(string(model), `"id":"loopback"`)
	}))
	assert.Contains(t, string(final.Model), `"id":"loopback"`)
	require.NoError(t, s.Close())
}

func openControlledPi(t *testing.T, ctx context.Context, executable string) (*pi.Session, <-chan string) {
	t.Helper()
	calls := make(chan string, 16)
	var requestNumber atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		first := requestNumber.Add(1) == 1
		select {
		case calls <- string(body):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"local\",\"created\":1,\"object\":\"chat.completion.chunk\",\"model\":\"loopback\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"SDK_CONTROLLED_REPLY\"},\"finish_reason\":null}]}\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			return
		}
		if first {
			select {
			case <-r.Context().Done():
			case <-ctx.Done():
			}
			return
		}
		_, _ = io.WriteString(w, "data: {\"id\":\"local\",\"created\":1,\"object\":\"chat.completion.chunk\",\"model\":\"loopback\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	root := t.TempDir()
	model := map[string]any{"providers": map[string]any{"inspection": map[string]any{
		"baseUrl": server.URL + "/v1", "api": "openai-completions", "apiKey": "loopback-only",
		"models": []map[string]string{{"id": "loopback"}},
	}}}
	config, err := json.Marshal(model)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "models.json"), config, 0o600))
	s, err := pi.Open(ctx, pi.Options{
		Command: executable, Cwd: root, SessionDir: root, Provider: "inspection", Model: "loopback",
		Env:          []string{"HOME=" + root, "PI_CODING_AGENT_DIR=" + root, "NO_PROXY=127.0.0.1"},
		NoExtensions: true, NoTools: true, ExtraArgs: []string{"--offline", "--no-mcp", "--no-context-files", "--no-approve"},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, s.CloseAndDiscard())
	})
	return s, calls
}

func nextCall(t *testing.T, ctx context.Context, calls <-chan string) string {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return ""
	}
}

func receiveSettled(t *testing.T, ctx context.Context, s *pi.Session) json.RawMessage {
	t.Helper()
	for {
		event, err := s.Receive(ctx)
		require.NoError(t, err)
		if event.Type == "agent_settled" {
			return event.Raw
		}
	}
}
