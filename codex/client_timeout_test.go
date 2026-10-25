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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultCallTimeoutWithControlledTime(t *testing.T) {
	for _, tc := range []struct {
		name          string
		callerTimeout time.Duration
		wantTimeout   time.Duration
	}{
		{"background", 0, 50 * time.Millisecond},
		{"later-caller-deadline", time.Second, 50 * time.Millisecond},
		{"earlier-caller-deadline", 20 * time.Millisecond, 20 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				limits := DefaultLimits()
				limits.CallTimeout = 50 * time.Millisecond
				c := newClient(limits)
				// Initialization is covered by the real process/socket contract
				// tests. Start ready here so only the call consumes fake time.
				c.ready = true
				reader, writer, err := os.Pipe()
				require.NoError(t, err)
				defer reader.Close()
				c.stdin = writer
				writerDone := make(chan struct{})
				go func() { defer close(writerDone); c.writeLoop() }()
				defer func() { c.fail(ErrClosed); <-writerDone }()
				scan := bufio.NewScanner(reader)
				readCall := func() ID {
					t.Helper()
					require.True(t, scan.Scan(), "missing dispatched call: %v", scan.Err())
					var frame struct {
						ID     ID     `json:"id"`
						Method string `json:"method"`
					}
					{
						err := json.Unmarshal(scan.Bytes(), &frame)
						require.NoError(t, err)
						require.Equal(t, "wait", frame.Method, "unexpected dispatched frame: %s", scan.Bytes())
					}
					return frame.ID
				}
				ctx := context.Background()
				if tc.callerTimeout != 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, tc.callerTimeout)
					defer cancel()
				}
				start := time.Now()
				result := make(chan error, 1)
				go func() { result <- c.Call(ctx, "wait", nil, nil) }()
				id := readCall()
				synctest.Wait() // Actual write settled; no active write is canceled.
				time.Sleep(tc.wantTimeout - time.Nanosecond)
				synctest.Wait()
				select {
				case err := <-result:
					require.FailNowf(t, "protocol scenario failed", "call completed before deadline: %v", err)
				default:
				}
				time.Sleep(time.Nanosecond)
				synctest.Wait()
				select {
				case err := <-result:
					var ce *CallError
					require.ErrorAs(t, err, &ce)
					assert.Equal(t, "wait", ce.Method)
					assert.Equal(t, id, ce.ID)
					assert.Equal(t, OutcomeUnknown, ce.Dispatch)
					assert.ErrorIs(t, err, context.DeadlineExceeded)
				default:
					require.FailNow(t, "call survived its exact deadline")
				}
				assert.Equal(t, tc.wantTimeout, time.Since(start))
				assert.Empty(t, c.pending)
				assert.Empty(t, c.queue)
				assert.NoError(t, c.Err())
				// A late response cannot resurrect the expired call or satisfy a
				// fresh call. Exercise real routing and the same writer again.
				{
					err := c.routeFrame([]byte(fmt.Sprintf(`{"id":%s,"result":null}`, id)))
					require.NoError(t, err)
				}
				go func() { result <- c.Call(context.Background(), "wait", nil, nil) }()
				freshID := readCall()
				synctest.Wait()
				require.NotEqual(t, id, freshID, "expired call ID reused")
				{
					err := c.routeFrame([]byte(fmt.Sprintf(`{"id":%s,"result":null}`, freshID)))
					require.NoError(t, err)
				}
				{
					err := <-result
					require.NoError(t, err, "timeout damaged subsequent call: %v", err)
				}
			})
		})
	}
}

// TestSessionConfigurationTimeoutWithControlledTime covers the timeout branch of
// TestSessionConfigurationUnknownAfterCancelAndClose. Process initialization stays
// in the public protocol tests; this existing pipe seam lets a standard context
// deadline expire only after the configuration frame and write have settled.
func TestSessionConfigurationTimeoutWithControlledTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newClient(DefaultLimits())
		c.ready = true
		c.experimentalAPI = true
		reader, writer, err := os.Pipe()
		require.NoError(t, err)
		defer reader.Close()
		c.stdin = writer
		writerDone := make(chan struct{})
		go func() { defer close(writerDone); c.writeLoop() }()
		defer func() { c.fail(ErrClosed); <-writerDone }()
		scan := bufio.NewScanner(reader)
		var methods []string
		readFrame := func(wantMethod string) (ID, json.RawMessage) {
			t.Helper()
			require.True(t, scan.Scan(), "missing dispatched %s: %v", wantMethod, scan.Err())
			var frame struct {
				ID     ID              `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			require.NoError(t, json.Unmarshal(scan.Bytes(), &frame))
			require.Equal(t, wantMethod, frame.Method)
			methods = append(methods, frame.Method)
			return frame.ID, frame.Params
		}
		respond := func(id ID, response string) {
			t.Helper()
			require.NoError(t, c.routeFrame([]byte(fmt.Sprintf(`{"id":%s,"result":%s}`, id, response))))
		}
		type opened struct {
			session *Session
			err     error
		}
		opening := make(chan opened, 1)
		go func() { s, err := Open(context.Background(), SessionOptions{Client: c}); opening <- opened{s, err} }()
		id, _ := readFrame("thread/start")
		respond(id, `{"thread":{"id":"main-1","sessionId":"session-1","cliVersion":"0.160.1","createdAt":1,"updatedAt":1,"cwd":"/synthetic","ephemeral":true,"modelProvider":"mock","preview":"","projectId":null,"source":"appServer","status":{"type":"idle"},"turns":[]},"approvalPolicy":"never","approvalsReviewer":"user","cwd":"/synthetic","model":"fake","modelProvider":"mock","sandbox":{"type":"readOnly"}}`)
		got := <-opening
		require.NoError(t, got.err)
		s := got.session
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		type configured struct {
			receipt ConfigurationReceipt
			err     error
		}
		result := make(chan configured, 1)
		start := time.Now()
		go func() { r, err := s.SetModel(ctx, "native-withhold"); result <- configured{r, err} }()
		id, params := readFrame("thread/settings/update")
		require.JSONEq(t, `{"threadId":"main-1","model":"native-withhold"}`, string(params))
		synctest.Wait() // The complete frame was read and the writer is idle.
		require.NoError(t, ctx.Err(), "deadline expired before dispatch")
		time.Sleep(50*time.Millisecond - time.Nanosecond)
		synctest.Wait()
		select {
		case got := <-result:
			require.FailNowf(t, "configuration completed before deadline", "%v", got.err)
		default:
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		select {
		case got := <-result:
			var call *CallError
			require.ErrorAs(t, got.err, &call)
			assert.Equal(t, "thread/settings/update", call.Method)
			assert.Equal(t, id, call.ID)
			assert.Equal(t, OutcomeUnknown, call.Dispatch)
			assert.ErrorIs(t, got.err, context.DeadlineExceeded)
			assert.Equal(t, context.DeadlineExceeded, call.Cause)
			assert.False(t, got.receipt.NativeAccepted)
			assert.Equal(t, "native-withhold", got.receipt.Requested.Model.ValueOr(""))
		default:
			require.FailNow(t, "configuration survived its exact deadline")
		}
		assert.Equal(t, 50*time.Millisecond, time.Since(start))
		// A fresh configuration must be the next frame, rather than a retry.
		go func() { r, err := s.SetModel(context.Background(), "still-usable"); result <- configured{r, err} }()
		freshID, params := readFrame("thread/settings/update")
		require.NotEqual(t, id, freshID)
		require.JSONEq(t, `{"threadId":"main-1","model":"still-usable"}`, string(params))
		respond(freshID, `{}`)
		fresh := <-result
		require.NoError(t, fresh.err)
		assert.True(t, fresh.receipt.NativeAccepted)
		assert.NoError(t, c.Err())
		assert.NoError(t, s.Err())
		assert.Equal(t, []string{"thread/start", "thread/settings/update", "thread/settings/update"}, methods)
	})
}
