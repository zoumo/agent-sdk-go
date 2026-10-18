package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"testing/synctest"
	"time"
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
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				c.stdin = writer
				writerDone := make(chan struct{})
				go func() { defer close(writerDone); c.writeLoop() }()
				defer func() { c.fail(ErrClosed); <-writerDone }()
				scan := bufio.NewScanner(reader)
				readCall := func() ID {
					t.Helper()
					if !scan.Scan() {
						t.Fatalf("missing dispatched call: %v", scan.Err())
					}
					var frame struct {
						ID     ID     `json:"id"`
						Method string `json:"method"`
					}
					if err := json.Unmarshal(scan.Bytes(), &frame); err != nil || frame.Method != "wait" {
						t.Fatalf("unexpected dispatched frame: %s (%v)", scan.Bytes(), err)
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
					t.Fatalf("call completed before deadline: %v", err)
				default:
				}
				time.Sleep(time.Nanosecond)
				synctest.Wait()
				select {
				case err := <-result:
					var ce *CallError
					if !errors.As(err, &ce) || ce.Method != "wait" || ce.ID != id || ce.Dispatch != OutcomeUnknown || !errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("timeout lost dispatch/correlation: %v", err)
					}
				default:
					t.Fatal("call survived its exact deadline")
				}
				if time.Since(start) != tc.wantTimeout || len(c.pending) != 0 || len(c.queue) != 0 || c.Err() != nil {
					t.Fatal("timeout changed timing, retained a call, replayed, or closed the connection")
				}
				// A late response cannot resurrect the expired call or satisfy a
				// fresh call. Exercise real routing and the same writer again.
				if err := c.routeFrame([]byte(fmt.Sprintf(`{"id":%s,"result":null}`, id))); err != nil {
					t.Fatal(err)
				}
				go func() { result <- c.Call(context.Background(), "wait", nil, nil) }()
				freshID := readCall()
				synctest.Wait()
				if freshID == id {
					t.Fatal("expired call ID reused")
				}
				if err := c.routeFrame([]byte(fmt.Sprintf(`{"id":%s,"result":null}`, freshID))); err != nil {
					t.Fatal(err)
				}
				if err := <-result; err != nil {
					t.Fatalf("timeout damaged subsequent call: %v", err)
				}
			})
		})
	}
}
