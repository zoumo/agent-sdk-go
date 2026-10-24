package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// The private adapter controls only native I/O/time; all assertions use public
// Open, Receive, IncomingRequest and Client operations.
func TestSessionDelayedRequestWithControlledTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newClient(DefaultLimits())
		c.ready = true
		reader, writer, err := os.Pipe()
		require.NoError(t, err)
		defer reader.Close()
		c.stdin = writer
		writerDone := make(chan struct{})
		go func() { defer close(writerDone); c.writeLoop() }()
		defer func() { c.fail(ErrClosed); <-writerDone }()
		opened := make(chan *Session, 1)
		errors := make(chan error, 1)
		go func() { s, err := Open(context.Background(), SessionOptions{Client: c}); opened <- s; errors <- err }()
		scan := bufio.NewScanner(reader)
		require.True(t, scan.Scan())
		var request struct {
			ID ID `json:"id"`
		}
		require.NoError(t, json.Unmarshal(scan.Bytes(), &request))
		require.NoError(t, c.routeFrame([]byte(fmt.Sprintf(`{"id":%s,"result":{"thread":{"id":"thread","sessionId":"session","cliVersion":"0.162.1","createdAt":1,"updatedAt":2,"cwd":"/synthetic","ephemeral":true,"modelProvider":"mock","preview":"","projectId":null,"source":"appServer","status":{"type":"idle"},"turns":[]},"approvalPolicy":"never","approvalsReviewer":"user","cwd":"/synthetic","model":"fake","modelProvider":"mock","sandbox":{"type":"readOnly"}}}`, request.ID))))
		s := <-opened
		require.NoError(t, <-errors)
		require.NoError(t, c.routeFrame([]byte(`{"id":"approval","method":"item/commandExecution/requestApproval","params":{"threadId":"thread"}}`)))
		got, err := s.Receive(context.Background())
		require.NoError(t, err)
		require.NotNil(t, got.Request)
		synctest.Wait()
		time.Sleep(6 * time.Minute)
		synctest.Wait()
		require.NoError(t, got.Request.Context().Err())
		require.NoError(t, c.routeFrame([]byte(`{"method":"future/progress","params":{"threadId":"thread"}}`)))
		progress, err := s.Receive(context.Background())
		require.NoError(t, err)
		require.Equal(t, "future/progress", progress.Event.Method)
		done := make(chan error, 1)
		go func() { done <- got.Request.Approve(context.Background(), ApprovalOptions{}) }()
		require.True(t, scan.Scan())
		require.JSONEq(t, `{"id":"approval","result":{"decision":"accept"}}`, scan.Text())
		require.NoError(t, <-done)
		require.ErrorIs(t, got.Request.Context().Err(), context.Canceled)
	})
}
