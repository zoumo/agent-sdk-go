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
func TestNativeRequestLifetimeWithControlledTime(t *testing.T) {
	for _, cause := range []error{ErrRequestCanceled, ErrRequestResolved} {
		t.Run(cause.Error(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				limits := DefaultLimits()
				limits.RequestTimeout = time.Second
				limits.ActiveRequests = 1
				c := newClient(limits)
				old := receiveLifecycleRequest(t, c)
				time.Sleep(6 * time.Minute)
				synctest.Wait()
				require.NoError(t, old.Handle().Err(), "deprecated local timeout must not expire native requests")
				if cause == ErrRequestCanceled {
					require.NoError(t, old.Handle().Cancel())
				} else {
					require.NoError(t, c.routeFrame([]byte(`{"method":"serverRequest/resolved","params":{"threadId":"thread","requestId":"reused"}}`)))
				}
				checkLifecycleTerminal(t, old, cause)
				fresh := receiveLifecycleRequest(t, c)
				checkLifecycleTerminal(t, old, cause)
				time.Sleep(6 * time.Minute)
				synctest.Wait()
				require.NoError(t, fresh.Handle().Err())
				require.NoError(t, fresh.Handle().Cancel())
				checkLifecycleTerminal(t, fresh, ErrRequestCanceled)
				require.Empty(t, c.reverse)
				require.Empty(t, c.queue)
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
