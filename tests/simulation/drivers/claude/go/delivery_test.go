package claudesim

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeDelivery(t *testing.T, values []map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stream.jsonl")
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(path), "report.json"), []byte(`{"receipts":[{"child-detail-selection":{"rootId":"root","childIds":["agent"]}}]}`), 0600))
	file, err := os.Create(path)
	require.NoError(t, err)
	defer file.Close()
	for _, value := range values {
		require.NoError(t, json.NewEncoder(file).Encode(value))
	}
	return path
}

func TestChildPolicyRetainsContextUnknownContentAndMixedFacts(t *testing.T) {
	for _, context := range []any{nil, map[string]any{}, []any{}, "", "context", true, false, 0, map[string]any{"edits": []any{}}} {
		member := map[string]any{"observation": map[string]any{"Reason": "member-confirmed", "Source": map[string]any{"association_id": "task:child"}}}
		message := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "child"}}, "context_management": context}
		wire := map[string]any{"type": "assistant", "session_id": "root", "uuid": "message", "parent_tool_use_id": "call", "message": message, "timestamp": "synthetic", "subagent_type": "general-purpose", "task_description": "reply"}
		raw, err := json.Marshal(wire)
		require.NoError(t, err)
		detail := map[string]any{"kind": "event", "origin": "live", "source": map[string]any{"association_id": "task:child", "tool_use_id": "call"}, "raw": string(raw)}
		terminal := map[string]any{"kind": "event", "origin": "live", "raw": `{"type":"result","session_id":"root","is_error":false}`}
		before, after := []map[string]any{member, detail, terminal}, []map[string]any{member, terminal}
		err = CompareDelivery(writeDelivery(t, before), writeDelivery(t, after))
		if context == nil {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
		if context != nil {
			continue
		}
		for _, kind := range []string{"tool", "unknown", "unconfirmed", "other-session", "extension", "root-result"} {
			var changed map[string]any
			encoded, _ := json.Marshal(wire)
			require.NoError(t, json.Unmarshal(encoded, &changed))
			switch kind {
			case "tool":
				mapping(changed["message"])["content"] = []any{map[string]any{"type": "tool_use", "id": "tool", "name": "Read", "input": map[string]any{}}}
			case "unknown":
				mapping(changed["message"])["content"] = []any{map[string]any{"type": "new-content", "data": "unknown"}}
			case "extension":
				changed["future"] = true
			case "other-session":
				changed["session_id"] = "other"
			}
			encoded, _ = json.Marshal(changed)
			candidate := map[string]any{"kind": "event", "origin": "live", "source": detail["source"], "raw": string(encoded)}
			before, after = []map[string]any{member, candidate, terminal}, []map[string]any{member, terminal}
			if kind == "unconfirmed" {
				before = before[1:]
				after = after[1:]
			}
			if kind == "root-result" {
				before = before[:2]
				after = after[:1]
			}
			require.Error(t, CompareDelivery(writeDelivery(t, before), writeDelivery(t, after)), kind)
		}
	}
}
