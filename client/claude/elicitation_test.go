package claude

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const nativeElicitationFrame = `{"type":"control_request","request_id":"outer","request":{"subtype":"elicitation","mcp_server_name":"sample","message":"Authenticate","mode":"url","url":"https://example.test/auth","elicitation_id":"external"}}`

func TestNativeElicitationDefaultDispatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wire.jsonl")
	session := openSessionFixture(t, func(o *SessionOptions) {
		o.Env = append(o.Env, "CLAUDE_GO_TRANSCRIPT="+path)
	})
	submitFrame(t, session, nativeElicitationFrame)
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(path)
		return err == nil && bytes.Contains(data, []byte("Unsupported control request subtype: elicitation"))
	}, 3*time.Second, time.Millisecond)
	require.NoError(t, session.Close())
	responses := permissionResponses(t, permissionTranscript(t, path))
	require.Equal(t, []map[string]any{{"type": "control_response", "response": map[string]any{
		"subtype": "error", "request_id": "outer", "error": "Unsupported control request subtype: elicitation",
	}}}, responses)
}

func TestNativeElicitationOriginalAuthority(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session := openSessionFixture(t, func(o *SessionOptions) { o.ReceiveElicitation = true })
	submitFrame(t, session, nativeElicitationFrame)
	var req *IncomingRequest
	for req == nil {
		entry, err := session.Receive(ctx)
		require.NoError(t, err)
		req = entry.Request
	}
	data, err := req.Elicitation()
	require.NoError(t, err)
	require.Equal(t, "external", data.ElicitationID)
	require.Equal(t, "sample", data.ServerName)
	require.Equal(t, "outer", req.ID())
	require.ErrorIs(t, req.Approve(ctx, ApprovalOptions{}), ErrUnsupported)
	require.ErrorIs(t, req.Handle().Answer(ctx, PermissionAnswer{Behavior: PermissionAllow}), ErrUnsupported)
	require.ErrorIs(t, req.ReplyElicitation(ctx, "accept", map[string]any{}), ErrInvalidInput)
	require.ErrorIs(t, req.ReplyElicitation(ctx, "unknown", nil), ErrInvalidInput)
	require.NoError(t, req.Context().Err())
	copyOfRequest := *req
	require.NoError(t, req.ReplyElicitation(ctx, "accept", nil))
	require.ErrorIs(t, copyOfRequest.ReplyElicitation(ctx, "cancel", nil), ErrPermissionUsed)

	permission := sessionPermission(t, session, "tool", "Read", `{}`)
	require.ErrorIs(t, permission.ReplyElicitation(ctx, "accept", nil), ErrUnsupported)
	require.NoError(t, permission.Approve(ctx, ApprovalOptions{}))
	require.NoError(t, session.Close())
}
