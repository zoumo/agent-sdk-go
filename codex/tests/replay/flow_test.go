package recording

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

func TestFlowReproducesConfigurationApprovalAndNativeFacts(t *testing.T) {
	for _, decision := range []string{"accept", "decline"} {
		t.Run(decision, func(t *testing.T) {
			fs := rootPrefix(t)
			// Real initializations may emit notifications before initialized.
			fs = append(fs[:2], append([]Frame{frame(0, "s2c", `{"method":"remoteControl/status/changed","params":{"enabled":false}}`)}, fs[2:]...)...)
			addRoot(&fs, "c2s", `{"id":103,"method":"thread/settings/update","params":{"threadId":"thread-001","model":"synthetic-model"}}`)
			addRoot(&fs, "s2c", `{"id":103,"result":{"futureReceipt":true}}`)
			addRoot(&fs, "c2s", `{"id":104,"method":"turn/start","params":{"threadId":"thread-001","input":[{"type":"text","text":"synthetic input"}]}}`)
			addRoot(&fs, "s2c", `{"id":104,"result":{"turn":{"id":"turn-1","items":[],"status":"inProgress"}}}`)
			addRoot(&fs, "s2c", `{"id":"permission","method":"item/commandExecution/requestApproval","params":{"threadId":"thread-001","turnId":"turn-1","itemId":"item-1","command":"DO NOT EXECUTE"}}`)
			addRoot(&fs, "c2s", `{"id":"permission","result":{"decision":"`+decision+`"}}`)
			addRoot(&fs, "s2c", `{"method":"turn/completed","params":{"threadId":"thread-001","turn":{"id":"turn-1","items":[],"status":"completed","error":null}}}`)
			var capture bytes.Buffer
			for i, f := range fs {
				row := struct {
					Ordinal   int             `json:"ordinal"`
					Direction string          `json:"direction"`
					Channel   string          `json:"channel"`
					Epoch     int             `json:"epoch"`
					ElapsedMS int             `json:"elapsedMs"`
					Wire      json.RawMessage `json:"wire"`
				}{i + 1, f.Direction, "protocol", 1, 0, f.Wire}
				require.NoError(t, json.NewEncoder(&capture).Encode(row))
			}
			root := t.TempDir()
			source, receipt := filepath.Join(root, "raw.jsonl"), filepath.Join(root, "peer.json")
			require.NoError(t, os.WriteFile(source, capture.Bytes(), 0600))
			peer, err := mocktest.Executable()
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.NoError(t, AcceptFlow(ctx, peer, source, receipt, io.Discard))
			b, err := os.ReadFile(receipt)
			require.NoError(t, err)
			var got struct{ FullConsumption, ClientEOF, NativeLaunched bool }
			require.NoError(t, json.Unmarshal(b, &got))
			require.True(t, got.FullConsumption)
			require.True(t, got.ClientEOF)
			require.False(t, got.NativeLaunched)
		})
	}
}
