package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHistoryEventsPreserveNativeContent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	o := historyTranscript(t,
		map[string]any{"type": "assistant", "uuid": "tool", "parentUuid": nil, "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "thinking", "thinking": "retained thought", "signature": "sig"}, map[string]any{"type": "tool_use", "id": "read-1", "name": "Read", "input": map[string]any{"path": "not executed"}}, map[string]any{"type": "future_block", "opaque": true}}}},
		map[string]any{"type": "user", "uuid": "result", "parentUuid": "tool", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "read-1", "content": "retained output", "is_error": true}}}},
	)
	source, err := os.ReadFile(historyPath(t, o))
	require.NoError(t, err)
	sourceLines := bytes.Split(bytes.TrimSpace(source), []byte{'\n'})
	h, err := readTranscriptForTest(ctx, o, nil)
	require.NoError(t, err)
	defer h.Close()
	var kinds []string
	consume := func(received Event) {
		if e := received; true {
			kinds = append(kinds, e.Type)
		}
	}
	i := 0
	for received := range h.Records() {
		e := received
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
}
