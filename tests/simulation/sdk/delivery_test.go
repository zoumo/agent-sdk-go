package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChildPolicyKeepsProtectedFacts(t *testing.T) {
	member := map[string]any{"observation": map[string]any{"Reason": "member-confirmed", "ThreadID": "child"}}
	delta := map[string]any{"kind": "event", "origin": "live", "source": map[string]any{"ThreadID": "child"}, "raw": `{"method":"item/agentMessage/delta","params":{"threadId":"child","turnId":"turn","itemId":"item","delta":"hello"}}`}
	request := map[string]any{"kind": "request", "raw": "original approval"}
	write := func(values ...map[string]any) string {
		p := filepath.Join(t.TempDir(), "stream.jsonl")
		var b []byte
		for _, v := range values {
			row, e := json.Marshal(v)
			require.NoError(t, e)
			b = append(append(b, row...), '\n')
		}
		require.NoError(t, os.WriteFile(p, b, 0600))
		return p
	}
	preserved := write(member, delta, request)
	require.NoError(t, compareDelivery("codex", preserved, write(member, request)))
	require.Error(t, compareDelivery("codex", preserved, write(member)))
	delta["raw"] = `{"method":"item/agentMessage/delta","extension":"protected","params":{"threadId":"child","turnId":"turn","itemId":"item","delta":"hello"}}`
	require.Error(t, compareDelivery("codex", write(member, delta, request), write(member, request)))
}
