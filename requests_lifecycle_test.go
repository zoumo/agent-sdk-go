package codex

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// These tests route native frames through the real request lifecycle. Unlike
// the subprocess/WebSocket contract tests, the bubble can control every timer
// and establish ordering without racing a real transport against a deadline.
func TestRequestTerminalOrderingWithControlledTime(t *testing.T) {
	for _, resolutionFirst := range []bool{true, false} {
		name, want := "expiry-first", ErrRequestExpired
		if resolutionFirst {
			name, want = "resolution-first", ErrRequestResolved
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				limits := DefaultLimits()
				limits.RequestTimeout = time.Second
				c := newClient(limits)
				r := receiveLifecycleRequest(t, c)
				resolve := func() {
					t.Helper()
					if err := c.routeFrame([]byte(`{"method":"serverRequest/resolved","params":{"threadId":"thread","requestId":"reused"}}`)); err != nil {
						t.Fatal(err)
					}
				}
				if resolutionFirst {
					time.Sleep(time.Second / 2)
					resolve()
					checkLifecycleTerminal(t, r, want)
					time.Sleep(time.Second)
				} else {
					time.Sleep(time.Second)
					synctest.Wait()
					checkLifecycleTerminal(t, r, want)
					resolve()
				}
				synctest.Wait()
				checkLifecycleTerminal(t, r, want)
				if len(c.reverse) != 0 || len(c.queue) != 0 {
					t.Fatal("terminal request retained admission or emitted a response")
				}
			})
		})
	}
}

func TestTerminalRequestIDReuseWithControlledTime(t *testing.T) {
	for _, cause := range []error{ErrRequestExpired, ErrRequestResolved} {
		t.Run(cause.Error(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				limits := DefaultLimits()
				limits.RequestTimeout = time.Second
				limits.ActiveRequests = 1
				c := newClient(limits)
				old := receiveLifecycleRequest(t, c)
				if cause == ErrRequestResolved {
					time.Sleep(time.Second / 2)
					if err := c.routeFrame([]byte(`{"method":"serverRequest/resolved","params":{"threadId":"thread","requestId":"reused"}}`)); err != nil {
						t.Fatal(err)
					}
				} else {
					time.Sleep(time.Second)
				}
				synctest.Wait()
				checkLifecycleTerminal(t, old, cause)

				fresh := receiveLifecycleRequest(t, c)
				checkLifecycleTerminal(t, old, cause)
				// The resolved case now crosses the old deadline while the fresh
				// request still has lifetime left. Its stopped timer must not affect reuse.
				time.Sleep(time.Second / 2)
				synctest.Wait()
				if fresh.Handle().Err() != nil {
					t.Fatal("stale handle affected reused ID")
				}
				select {
				case <-fresh.Handle().Done():
					t.Fatal("fresh request already closed")
				default:
				}
				if err := c.routeFrame([]byte(`{"id":"reused","method":"future/approval","params":{"threadId":"thread"}}`)); !errors.Is(err, ErrProtocol) {
					t.Fatalf("fresh active ID lost its admission: %v", err)
				}
				if err := fresh.Handle().Cancel(); err != nil {
					t.Fatal(err)
				}
				checkLifecycleTerminal(t, fresh, ErrRequestCanceled)
				if len(c.queue) != 0 {
					t.Fatal("stale answer emitted a response")
				}
			})
		})
	}
}

func receiveLifecycleRequest(t *testing.T, c *Client) Request {
	t.Helper()
	if err := c.routeFrame([]byte(`{"id":"reused","method":"future/approval","params":{"threadId":"thread"}}`)); err != nil {
		t.Fatal(err)
	}
	return <-c.Requests()
}

func checkLifecycleTerminal(t *testing.T, r Request, want error) {
	t.Helper()
	h := r.Handle()
	select {
	case <-h.Done():
	default:
		t.Fatal("terminal handle is still open")
	}
	if !errors.Is(h.Err(), want) || !errors.Is(h.Cancel(), want) {
		t.Fatalf("terminal cause = %v, want %v", h.Err(), want)
	}
	for _, err := range []error{
		h.Answer(context.Background(), "stale"),
		h.Reject(context.Background(), &RPCError{Code: -1, Message: "stale"}),
	} {
		var ae *AnswerError
		if !errors.As(err, &ae) || ae.Dispatch != NotSent || ae.ID != r.ID() || ae.Method != r.Method() || !errors.Is(err, want) {
			t.Fatalf("late answer = %v, want NotSent with %v and original identity", err, want)
		}
	}
}
