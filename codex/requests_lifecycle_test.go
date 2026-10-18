package codex

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
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
					{
						err := c.routeFrame([]byte(`{"method":"serverRequest/resolved","params":{"threadId":"thread","requestId":"reused"}}`))
						require.NoError(t, err, err)
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
				require.Empty(t, c.reverse, "terminal request retained admission")
				require.Empty(t, c.queue, "terminal request emitted a response")
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
					{
						err := c.routeFrame([]byte(`{"method":"serverRequest/resolved","params":{"threadId":"thread","requestId":"reused"}}`))
						require.NoError(t, err, err)
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
				require.NoError(t, fresh.Handle().Err(), "stale handle affected reused ID")
				select {
				case <-fresh.Handle().Done():
					require.FailNow(t, "test invariant", "fresh request already closed")
				default:
				}
				{
					err := c.routeFrame([]byte(`{"id":"reused","method":"future/approval","params":{"threadId":"thread"}}`))
					require.ErrorIs(t, err, ErrProtocol, "fresh active ID lost its admission: %v", err)
				}
				{
					err := fresh.Handle().Cancel()
					require.NoError(t, err, err)
				}
				checkLifecycleTerminal(t, fresh, ErrRequestCanceled)
				require.Empty(t, c.queue, "stale answer emitted a response")
			})
		})
	}
}

func receiveLifecycleRequest(t *testing.T, c *Client) Request {
	t.Helper()
	{
		err := c.routeFrame([]byte(`{"id":"reused","method":"future/approval","params":{"threadId":"thread"}}`))
		require.NoError(t, err, err)
	}
	return <-c.Requests()
}

func checkLifecycleTerminal(t *testing.T, r Request, want error) {
	t.Helper()
	h := r.Handle()
	select {
	case <-h.Done():
	default:
		require.FailNow(t, "test invariant", "terminal handle is still open")
	}
	require.ErrorIs(t, h.Err(), want, "terminal cause")
	require.ErrorIs(t, h.Cancel(), want, "cancel must retain terminal cause")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"answer", h.Answer(context.Background(), "stale")},
		{"reject", h.Reject(context.Background(), &RPCError{Code: -1, Message: "stale"})},
	} {
		var ae *AnswerError
		require.ErrorAs(t, tc.err, &ae, tc.name)
		require.Equal(t, NotSent, ae.Dispatch, tc.name)
		require.Equal(t, r.ID(), ae.ID, tc.name)
		require.Equal(t, r.Method(), ae.Method, tc.name)
		require.ErrorIs(t, tc.err, want, tc.name)
	}
}
