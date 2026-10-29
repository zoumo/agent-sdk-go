package claude

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func mcpRequestFixture(t *testing.T) (*Session, string, *IncomingRequest) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wire.jsonl")
	s := openSessionFixture(t, func(o *SessionOptions) {
		o.SDKMCPServers = []string{"fixture"}
		o.Env = append(o.Env, "CLAUDE_GO_TRANSCRIPT="+path)
	})
	submitFrame(t, s, `{"type":"control_request","request_id":"mcp-call","request":{"subtype":"mcp_message","server_name":"fixture","message":{"jsonrpc":"2.0","id":9007199254740993,"method":"tools/call","params":{"name":"fixture_read","arguments":{"path":"synthetic.txt"}},"future":null}}}`)
	for {
		r, err := s.Receive(context.Background())
		require.NoError(t, err)
		if r.Request != nil {
			return s, path, r.Request
		}
	}
}
func TestNativeSDKMCPOriginalAuthority(t *testing.T) {
	s, path, r := mcpRequestFixture(t)
	require.Equal(t, "mcp_message", r.Method())
	request, err := r.MCPRequest()
	require.NoError(t, err)
	require.Equal(t, "fixture", request.ServerName)
	require.Contains(t, string(request.Message), "9007199254740993")
	require.ErrorIs(t, r.Approve(context.Background(), ApprovalOptions{}), ErrUnsupported)
	require.ErrorIs(t, r.Handle().Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow}), ErrUnsupported)
	require.Error(t, r.ReplyMCP(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{}}`)))
	require.NoError(t, r.Context().Err(), "invalid response cannot consume authority")
	const answer = `{"jsonrpc":"2.0","id":9007199254740993,"result":{"content":[{"type":"text","text":"SYNTHETIC TOOL RESULT"}],"future":null}}`
	require.NoError(t, r.ReplyMCP(context.Background(), json.RawMessage(answer)))
	require.ErrorIs(t, r.ReplyMCP(context.Background(), json.RawMessage(answer)), ErrPermissionUsed)
	require.NoError(t, s.Interrupt(context.Background(), InterruptOptions{}))
	require.NoError(t, s.Close())
	responses := permissionResponses(t, permissionTranscript(t, path))
	require.Len(t, responses, 1)
	lines := permissionTranscript(t, path)
	found := false
	for _, raw := range lines {
		var frame struct {
			Type     string `json:"type"`
			Response struct {
				RequestID string                     `json:"request_id"`
				Data      map[string]json.RawMessage `json:"response"`
			} `json:"response"`
		}
		require.NoError(t, json.Unmarshal(raw, &frame))
		if frame.Type == "control_response" {
			require.Equal(t, "mcp-call", frame.Response.RequestID)
			require.JSONEq(t, answer, string(frame.Response.Data["mcp_response"]))
			require.Contains(t, string(frame.Response.Data["mcp_response"]), "9007199254740993")
			found = true
		}
	}
	require.True(t, found)
}
func TestNativeSDKMCPRevocationAndInvalidConfiguration(t *testing.T) {
	for _, mode := range []string{"cancel", "close"} {
		t.Run(mode, func(t *testing.T) {
			s, _, r := mcpRequestFixture(t)
			if mode == "cancel" {
				submitFrame(t, s, `{"type":"control_cancel_request","request_id":"mcp-call"}`)
				require.NoError(t, s.Interrupt(context.Background(), InterruptOptions{}))
			} else {
				require.NoError(t, s.Close())
			}
			err := r.ReplyMCP(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":9007199254740993,"result":{}}`))
			require.Error(t, err)
			var dispatch *DispatchError
			require.ErrorAs(t, err, &dispatch)
			require.Equal(t, NotSent, dispatch.State)
		})
	}
	for _, names := range [][]string{{""}, {"a", "a"}, {string([]byte{255})}} {
		_, err := Open(context.Background(), SessionOptions{CLIPath: "/must-not-start", SDKMCPServers: names})
		require.ErrorIs(t, err, ErrInvalidInput)
	}
}
