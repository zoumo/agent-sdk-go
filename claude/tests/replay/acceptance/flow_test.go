package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

func TestRecordedContinuationWithoutHistoryQueries(t *testing.T) {
	for _, mcp := range []bool{false, true} {
		t.Run(fmt.Sprint("startup-mcp=", mcp), func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(root, "raw"), 0700))
			plan, _ := json.Marshal(map[string]string{"Project": root, "Config": root, "DriverProfile": "continuation"})
			require.NoError(t, os.WriteFile(filepath.Join(root, "plan.json"), plan, 0600))
			rows := []string{`>{"type":"control_request","request_id":"init","request":{"subtype":"initialize","hooks":null}}`}
			if mcp {
				rows = append(rows,
					`<{"type":"control_request","request_id":"mcp","request":{"subtype":"mcp_message","server_name":"fixture","message":{"jsonrpc":"2.0","id":0,"method":"initialize"}}}`,
					`>{"type":"control_response","response":{"subtype":"success","request_id":"mcp","response":{"mcp_response":{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":"2025-11-25"}}}}}`)
			}
			rows = append(rows,
				`<{"type":"control_response","response":{"subtype":"success","request_id":"init","response":{}}}`,
				`>{"type":"user","session_id":"default","parent_tool_use_id":null,"message":{"role":"user","content":"first"}}`,
				`<{"type":"system","subtype":"init","session_id":"root","claude_code_version":"2.1.258"}`,
				`<{"type":"result","session_id":"root","subtype":"success","is_error":false,"result":"first"}`,
				`<{"type":"system","subtype":"session_state_changed","session_id":"root","state":"idle"}`,
				`>{"type":"user","session_id":"default","parent_tool_use_id":null,"message":{"role":"user","content":"second"}}`,
				`<{"type":"result","session_id":"root","subtype":"success","is_error":false,"result":"second"}`)
			var capture strings.Builder
			for i, row := range rows {
				direction := "s2c"
				if row[0] == '>' {
					direction = "c2s"
				}
				fmt.Fprintf(&capture, `{"ordinal":%d,"direction":%q,"channel":"protocol","epoch":1,"elapsedMs":0,"wire":%s}`+"\n", i+1, direction, row[1:])
			}
			source := filepath.Join(root, "raw/raw.jsonl")
			require.NoError(t, os.WriteFile(source, []byte(capture.String()), 0600))
			receipt := filepath.Join(root, "peer.json")
			launcher, err := mocktest.Launcher(root, "claude", source, receipt)
			require.NoError(t, err)
			// Match the external adapter: native launch options belong to the Go SDK;
			// the frozen mock receives only its explicit recording arguments.
			launch, err := os.ReadFile(launcher)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(launcher, []byte(strings.ReplaceAll(string(launch), ` "$@"`, "")), 0700))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.NoError(t, AcceptFlow(ctx, launcher, root, io.Discard))
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
