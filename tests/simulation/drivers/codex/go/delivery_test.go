package codexsim

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeDelivery(t *testing.T, values []map[string]any) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stream.jsonl")
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(p), "report.json"), []byte(`{"receipts":[{"child-detail-selection":{"rootId":"root","childIds":["child"]}}]}`), 0600))
	f, e := os.Create(p)
	require.NoError(t, e)
	defer f.Close()
	for _, value := range values {
		require.NoError(t, json.NewEncoder(f).Encode(value))
	}
	return p
}
func codexDelivery() (before, after []map[string]any) {
	member := map[string]any{"observation": map[string]any{"Reason": "member-confirmed", "ThreadID": "child"}}
	detail := map[string]any{"kind": "event", "origin": "live", "source": map[string]any{"ThreadID": "child"}, "raw": `{"method":"item/agentMessage/delta","params":{"threadId":"child","turnId":"turn","itemId":"item","delta":"hello"}}`}
	child := map[string]any{"kind": "event", "origin": "live", "source": map[string]any{"ThreadID": "child"}, "raw": `{"method":"turn/completed","params":{"threadId":"child","turn":{"id":"turn","status":"completed"}}}`}
	terminal := map[string]any{"kind": "event", "origin": "live", "raw": `{"method":"turn/completed","params":{"threadId":"root"}}`}
	return []map[string]any{member, detail, child, terminal}, []map[string]any{member, child, terminal}
}

func TestChildPolicyKeepsProtectedFacts(t *testing.T) {
	before, after := codexDelivery()
	require.NoError(t, CompareDelivery(writeDelivery(t, before), writeDelivery(t, after)))
	for _, kind := range []string{"no-difference", "lifecycle", "source", "reorder", "raw", "extension", "unconfirmed", "non-text", "root-terminal"} {
		t.Run(kind, func(t *testing.T) {
			before, after := codexDelivery()
			switch kind {
			case "no-difference":
				after = before
			case "lifecycle":
				after = append(after[:1], after[2:]...)
			case "source":
				after[1] = map[string]any{"kind": "event", "origin": "live", "source": map[string]any{"ThreadID": "other"}, "raw": after[1]["raw"]}
			case "reorder":
				after[0], after[1] = after[1], after[0]
			case "raw":
				after[1] = map[string]any{"kind": "event", "origin": "live", "source": after[1]["source"], "raw": after[1]["raw"].(string) + " "}
			case "extension":
				before[1]["raw"] = `{"method":"item/agentMessage/delta","extension":"protected","params":{"threadId":"child","turnId":"turn","itemId":"item","delta":"hello"}}`
			case "unconfirmed":
				before = before[1:]
				after = after[1:]
			case "non-text":
				before[1]["raw"] = `{"method":"item/agentMessage/delta","params":{"threadId":"child","turnId":"turn","itemId":"item","delta":false}}`
			case "root-terminal":
				before = before[:3]
				after = after[:2]
			}
			require.Error(t, CompareDelivery(writeDelivery(t, before), writeDelivery(t, after)))
		})
	}
	before, after = codexDelivery()
	source := writeDelivery(t, before)
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(source), "report.json"), []byte(`{"receipts":[{"child-detail-selection":{"rootId":"root","childIds":["other"]}}]}`), 0600))
	require.Error(t, CompareDelivery(source, writeDelivery(t, after)))
}

func TestChildDeliveryRequiresRelationsCompletionAndBackgroundOrder(t *testing.T) {
	before, _ := codexDelivery()
	member, child, root := before[0], before[2], before[3]
	parent := map[string]any{"observation": map[string]any{"Reason": "parent-confirmed", "ThreadID": "child", "ParentThreadID": "root"}}
	for _, kind := range []string{"passed", "no-parent", "no-completion", "before-root"} {
		t.Run(kind, func(t *testing.T) {
			values := []map[string]any{member, parent, root, child}
			switch kind {
			case "no-parent":
				values = []map[string]any{member, root, child}
			case "no-completion":
				values = values[:3]
			case "before-root":
				values = []map[string]any{member, parent, child, root}
			}
			path := writeDelivery(t, values)
			dir := filepath.Dir(path)
			require.NoError(t, os.Rename(path, filepath.Join(dir, "deliveries.jsonl")))
			require.NoError(t, os.Mkdir(filepath.Join(dir, "raw"), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "raw/raw.jsonl"), []byte(`{"direction":"s2c","wire":`+child["raw"].(string)+"}\n"), 0600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "report.json"), []byte(`{"childEvidence":[{"childThreadId":"child","parentThreadId":"root"}]}`), 0600))
			err := ValidateChildDelivery(dir, true)
			if kind == "passed" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
