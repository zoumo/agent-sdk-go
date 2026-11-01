package claudesim

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
	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

func TestHistorySetupConsumesBackgroundResultsBeforeNextInput(t *testing.T) {
	for _, changedResult := range []bool{false, true} {
		t.Run(fmt.Sprint("changed-result=", changedResult), func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(root, "raw"), 0700))
			plan, err := json.Marshal(map[string]string{"Project": root, "Config": root, "DriverProfile": "continuation"})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(root, "plan.json"), plan, 0600))
			rows := []string{
				`>{"type":"control_request","request_id":"init","request":{"subtype":"initialize","hooks":null}}`,
				`<{"type":"control_response","response":{"subtype":"success","request_id":"init","response":{}}}`,
				`>{"type":"user","session_id":"default","parent_tool_use_id":null,"message":{"role":"user","content":"first"}}`,
				`<{"type":"system","subtype":"init","session_id":"root","claude_code_version":"2.1.258"}`,
				`<{"type":"result","session_id":"root","subtype":"success","is_error":false,"result":"first"}`,
				`<{"type":"result","session_id":"root","subtype":"success","is_error":false,"result":"background"}`,
				`>{"type":"user","session_id":"default","parent_tool_use_id":null,"message":{"role":"user","content":"second"}}`,
				`<{"type":"result","session_id":"root","subtype":"success","is_error":false,"result":"second"}`,
			}
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
			launch, err := os.ReadFile(launcher)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(launcher, []byte(strings.ReplaceAll(string(launch), ` "$@"`, "")), 0700))
			if changedResult {
				require.NoError(t, os.WriteFile(source, []byte(strings.Replace(capture.String(), `"result":"background"`, `"result":"wrong"`, 1)), 0600))
				// The peer reads its frozen source, while the driver checks a different expectation.
				peerSource := filepath.Join(root, "peer-source.jsonl")
				require.NoError(t, os.WriteFile(peerSource, []byte(capture.String()), 0600))
				require.NoError(t, os.WriteFile(launcher, []byte(strings.ReplaceAll(strings.ReplaceAll(string(launch), ` "$@"`, ""), source, peerSource)), 0700))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err = AcceptSavedHistory(ctx, launcher, root, claude.ChildDetailPreserve, io.Discard)
			if changedResult {
				require.ErrorContains(t, err, "terminal differs")
			} else {
				require.NoError(t, err)
				var got struct{ FullConsumption, ClientEOF bool }
				b, err := os.ReadFile(receipt)
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(b, &got))
				require.True(t, got.FullConsumption)
				require.True(t, got.ClientEOF)
			}
		})
	}
}
