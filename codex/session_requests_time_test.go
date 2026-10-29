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
	for _, policy := range []ChildDetailPolicy{"", ChildDetailSuppress} {
		t.Run(string(policy), func(t *testing.T) { testSessionDelayedRequestWithControlledTime(t, policy) })
	}
}

func testSessionDelayedRequestWithControlledTime(t *testing.T, policy ChildDetailPolicy) {
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
		go func() {
			s, err := Open(context.Background(), SessionOptions{ChildDetailPolicy: policy, Client: c})
			opened <- s
			errors <- err
		}()
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

// A blocked Receive survives a successful terminal event and receives the next
// turn on the same Session. synctest proves it is parked before Submit, without
// depending on a wall-clock grace period or a fabricated post-result idle.
func TestSessionReceiveContinuesAcrossSuccessfulTurns(t *testing.T) {
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
		go func() {
			s, err := Open(context.Background(), SessionOptions{Client: c})
			opened <- s
			errors <- err
		}()
		scan := bufio.NewScanner(reader)
		require.True(t, scan.Scan())
		var request struct {
			ID ID `json:"id"`
		}
		require.NoError(t, json.Unmarshal(scan.Bytes(), &request))
		require.NoError(t, c.routeFrame([]byte(fmt.Sprintf(`{"id":%s,"result":{"thread":{"id":"thread","sessionId":"session","cliVersion":"0.162.1","createdAt":1,"updatedAt":2,"cwd":"/synthetic","ephemeral":true,"modelProvider":"mock","preview":"","projectId":null,"source":"appServer","status":{"type":"idle"},"turns":[]},"approvalPolicy":"never","approvalsReviewer":"user","cwd":"/synthetic","model":"fake","modelProvider":"mock","sandbox":{"type":"readOnly"}}}`, request.ID))))
		s := <-opened
		require.NoError(t, <-errors)
		type received struct {
			value Received
			err   error
		}
		events := make(chan received, 4)
		go func() {
			for range 4 {
				value, err := s.Receive(context.Background())
				events <- received{value, err}
				if err != nil {
					return
				}
			}
		}()
		for n := 1; n <= 2; n++ {
			synctest.Wait()
			select {
			case value := <-events:
				t.Fatalf("Receive ended before input %d: %v", n, value.err)
			default:
			}
			select {
			case <-s.Done():
				t.Fatal("result terminated Session")
			default:
			}
			submit := make(chan error, 1)
			go func() {
				_, err := s.Submit(context.Background(), Input{})
				submit <- err
			}()
			require.True(t, scan.Scan())
			var input struct {
				ID     ID     `json:"id"`
				Method string `json:"method"`
				Params struct {
					ThreadID string `json:"threadId"`
				} `json:"params"`
			}
			require.NoError(t, json.Unmarshal(scan.Bytes(), &input))
			require.Equal(t, "turn/start", input.Method)
			require.Equal(t, "thread", input.Params.ThreadID)
			require.NoError(t, c.routeFrame([]byte(fmt.Sprintf(`{"id":%s,"result":{"turn":{"id":"turn-%d","status":"inProgress","items":[]}}}`, input.ID, n))))
			require.NoError(t, <-submit)
			require.NoError(t, c.routeFrame([]byte(`{"method":"thread/status/changed","params":{"threadId":"thread","status":{"type":"idle"}}}`)))
			require.NoError(t, c.routeFrame([]byte(fmt.Sprintf(`{"method":"turn/completed","params":{"threadId":"thread","turn":{"id":"turn-%d","status":"completed","items":[]}}}`, n))))
			idle, terminal := <-events, <-events
			require.NoError(t, idle.err)
			require.NoError(t, terminal.err)
			require.Equal(t, "thread/status/changed", idle.value.Event.Method)
			require.Equal(t, "turn/completed", terminal.value.Event.Method)
			require.Equal(t, fmt.Sprintf("turn-%d", n), terminal.value.Source.TurnID)
		}
		require.NoError(t, s.Err())
	})
}
