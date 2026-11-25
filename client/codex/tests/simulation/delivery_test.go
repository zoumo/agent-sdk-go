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
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(p), "report.json"), []byte(`{"receipts":[{"child-detail-selection":{"rootId":"root","childIds":["child"]}}]}`), 0o600))
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
			beforeCase, afterCase := codexDelivery()
			switch kind {
			case "no-difference":
				afterCase = beforeCase
			case "lifecycle":
				afterCase = append(afterCase[:1], afterCase[2:]...)
			case "source":
				afterCase[1] = map[string]any{"kind": "event", "origin": "live", "source": map[string]any{"ThreadID": "other"}, "raw": afterCase[1]["raw"]}
			case "reorder":
				afterCase[0], afterCase[1] = afterCase[1], afterCase[0]
			case "raw":
				afterCase[1] = map[string]any{"kind": "event", "origin": "live", "source": afterCase[1]["source"], "raw": afterCase[1]["raw"].(string) + " "}
			case "extension":
				beforeCase[1]["raw"] = `{"method":"item/agentMessage/delta","extension":"protected","params":{"threadId":"child","turnId":"turn","itemId":"item","delta":"hello"}}`
			case "unconfirmed":
				beforeCase = beforeCase[1:]
				afterCase = afterCase[1:]
			case "non-text":
				beforeCase[1]["raw"] = `{"method":"item/agentMessage/delta","params":{"threadId":"child","turnId":"turn","itemId":"item","delta":false}}`
			case "root-terminal":
				beforeCase = beforeCase[:3]
				afterCase = afterCase[:2]
			}
			require.Error(t, CompareDelivery(writeDelivery(t, beforeCase), writeDelivery(t, afterCase)))
		})
	}
	before, after = codexDelivery()
	source := writeDelivery(t, before)
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(source), "report.json"), []byte(`{"receipts":[{"child-detail-selection":{"rootId":"root","childIds":["other"]}}]}`), 0o600))
	require.Error(t, CompareDelivery(source, writeDelivery(t, after)))
}

func TestChildPolicyReasoningProgress(t *testing.T) {
	for _, params := range []string{
		`"method":"item/reasoning/summaryPartAdded","params":{"threadId":"child","turnId":"turn","itemId":"item","summaryIndex":0}`,
		`"method":"item/reasoning/summaryTextDelta","params":{"threadId":"child","turnId":"turn","itemId":"item","summaryIndex":0,"delta":"thinking"}`,
		`"method":"item/reasoning/textDelta","params":{"threadId":"child","turnId":"turn","itemId":"item","contentIndex":0,"delta":"thinking"}`,
	} {
		for _, mutation := range []string{"valid", "extension", "invalid-index", "invalid-timestamp", "missing-identity"} {
			t.Run(params+mutation, func(t *testing.T) {
				before, after := codexDelivery()
				var wire map[string]any
				require.NoError(t, json.Unmarshal([]byte("{"+params+`,"emittedAtMs":1793457783332}`), &wire))
				fields := wire["params"].(map[string]any)
				switch mutation {
				case "extension":
					fields["unknown"] = true
				case "invalid-index":
					fields["summaryIndex"] = "zero"
				case "invalid-timestamp":
					wire["emittedAtMs"] = nil
				case "missing-identity":
					delete(fields, "itemId")
				}
				raw, err := json.Marshal(wire)
				require.NoError(t, err)
				before[1]["raw"] = string(raw)
				err = CompareDelivery(writeDelivery(t, before), writeDelivery(t, after))
				if mutation == "valid" {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
			})
		}
	}
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
			require.NoError(t, os.Mkdir(filepath.Join(dir, "raw"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "raw", "raw.jsonl"), []byte(`{"direction":"s2c","wire":`+child["raw"].(string)+"}\n"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "report.json"), []byte(`{"childEvidence":[{"childThreadId":"child","parentThreadId":"root"}]}`), 0o600))
			err := ValidateChildDelivery(dir, true)
			if kind == "passed" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
