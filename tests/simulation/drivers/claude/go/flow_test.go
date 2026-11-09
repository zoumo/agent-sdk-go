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

func TestRecordedContinuationWithoutHistoryQueries(t *testing.T) {
	runRecordedContinuation(t, false)
}

func runRecordedContinuation(t *testing.T, mcp bool) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "raw"), 0o700))
	plan, err := json.Marshal(map[string]string{"Project": root, "Config": root, "DriverProfile": "continuation"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "plan.json"), plan, 0o600))
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
	source := filepath.Join(root, "raw", "raw.jsonl")
	require.NoError(t, os.WriteFile(source, []byte(capture.String()), 0o600))
	session, err := mocktest.Prepare(t.TempDir(), "claude", []byte(capture.String()), mocktest.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { verifyMockSession(t, session, "passed") })
	host := preparedHost(t, session)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, AcceptFlow(ctx, session, root, host, claude.ChildDetailPreserve, io.Discard))
}

func preparedHost(t *testing.T, session *mocktest.Session) Host {
	t.Helper()
	config := ""
	for _, entry := range session.Env {
		if key, value, ok := strings.Cut(entry, "="); ok && key == "CLAUDE_CONFIG_DIR" {
			config = value
		}
	}
	require.NotEmpty(t, config)
	return Host{Workspace: session.Workspace, Config: config, Env: append([]string(nil), session.Env...)}
}

func verifyMockSession(t *testing.T, session *mocktest.Session, want string) {
	t.Helper()
	report, err := session.Report()
	require.NotNil(t, report)
	require.NoError(t, err)
	require.Equal(t, want, report.Status)
	require.NoError(t, session.Close())
}
