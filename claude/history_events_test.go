package claude_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude"
)

func TestLoadEventsUseRealtimeConsumer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	o := transcript(t,
		map[string]any{"type": "assistant", "uuid": "tool", "parentUuid": nil, "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "thinking", "thinking": "retained thought", "signature": "sig"}, map[string]any{"type": "tool_use", "id": "read-1", "name": "Read", "input": map[string]any{"path": "not executed"}}, map[string]any{"type": "future_block", "opaque": true}}}},
		map[string]any{"type": "user", "uuid": "result", "parentUuid": "tool", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "read-1", "content": "retained output", "is_error": true}}}},
	)
	source, err := os.ReadFile(historyPath(t, o))
	require.NoError(t, err)
	sourceLines := bytes.Split(bytes.TrimSpace(source), []byte{'\n'})
	h, err := claude.Load(ctx, loadOptions(t, o))
	require.NoError(t, err)
	defer h.Close()
	var kinds []string
	consume := func(received claude.ReceivedEvent) {
		if e, ok := received.(claude.Event); ok {
			kinds = append(kinds, e.Type)
		}
	}
	i := 0
	for received := range h.Events() {
		e, ok := received.(claude.Event)
		require.True(t, ok)
		require.NotNil(t, e.History)
		require.Less(t, i, len(sourceLines))
		require.Equal(t, sourceLines[i], []byte(e.History.Raw))
		require.Equal(t, o.SessionID, e.SessionID)
		if e.Assistant != nil {
			require.Equal(t, "retained thought", e.Assistant.Content.Blocks[0].Thinking.Thinking)
			require.Equal(t, "read-1", e.Assistant.Content.Blocks[1].ToolUse.ID)
			require.JSONEq(t, `{"type":"future_block","opaque":true}`, string(e.Assistant.Content.Blocks[2].Raw))
		}
		if e.User != nil {
			require.Equal(t, "read-1", e.User.Content.Blocks[0].ToolResult.ToolUseID)
			require.True(t, e.User.Content.Blocks[0].ToolResult.IsError)
		}
		encoded, err := json.Marshal(e)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), `"History"`)
		consume(received)
		i++
	}
	<-h.Done()
	require.NoError(t, h.Err())
	require.Equal(t, []string{"assistant", "user"}, kinds)
	c, err := h.Client()
	require.NoError(t, err)
	// The offline peer emits a normal init after input; no real CLI is started.
	require.NoError(t, c.Send(ctx, "synthetic continuation"))
	for len(kinds) < 4 {
		received, err := c.Receive(ctx)
		require.NoError(t, err)
		if e, ok := received.(claude.Event); ok {
			require.Nil(t, e.History)
		}
		consume(received)
	}
	require.Equal(t, []string{"assistant", "user", "echo", "system"}, kinds)
}
