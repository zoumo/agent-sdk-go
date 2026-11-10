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
			require.NoError(t, os.Mkdir(filepath.Join(root, "raw"), 0o700))
			plan, err := json.Marshal(map[string]string{"Project": root, "Config": root, "DriverProfile": "continuation"})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(root, "plan.json"), plan, 0o600))
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
			source := filepath.Join(root, "raw", "raw.jsonl")
			require.NoError(t, os.WriteFile(source, []byte(capture.String()), 0o600))
			session, err := mocktest.Prepare(t.TempDir(), "claude", []byte(capture.String()), mocktest.Options{})
			require.NoError(t, err)
			wantStatus := "passed"
			if changedResult {
				wantStatus = "failed"
			}
			t.Cleanup(func() { verifyMockSession(t, session, wantStatus) })
			if changedResult {
				require.NoError(t, os.WriteFile(source, []byte(strings.Replace(capture.String(), `"result":"background"`, `"result":"wrong"`, 1)), 0o600))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err = AcceptSavedHistory(ctx, session, root, preparedHost(t, session), claude.ChildDetailPreserve, io.Discard)
			if changedResult {
				require.ErrorContains(t, err, "terminal differs")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
