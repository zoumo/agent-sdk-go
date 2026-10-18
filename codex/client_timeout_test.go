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
