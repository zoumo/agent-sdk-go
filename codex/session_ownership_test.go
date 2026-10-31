package codex_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestSessionCapacityReservedUntilStartCompletes(t *testing.T) {
	f := startFake(t, "session-start-wait", codex.Limits{Subscriptions: 1})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type opened struct {
		session *codex.Session
		err     error
	}
	result := make(chan opened, 1)
	go func() {
		s, err := codex.Open(ctx, codex.SessionOptions{Client: f.c})
		result <- opened{s, err}
	}()
	waitTrace(t, f.trace, "thread/start")
	_, err := codex.Open(ctx, codex.SessionOptions{Client: f.c})
	require.ErrorIs(t, err, codex.ErrLimit)
	assertMethods(t, f, "initialize", "initialized", "thread/start")
	require.NoError(t, f.c.Call(ctx, "session-release-start", nil, nil))
	first := <-result
	require.NoError(t, first.err)
	t.Cleanup(func() { _ = first.session.Close() })
	require.NoError(t, first.session.Close())
	second, err := codex.Open(ctx, codex.SessionOptions{Client: f.c})
	require.NoError(t, err, "completed cleanup must release the single slot")
	t.Cleanup(func() { _ = second.Close() })
	require.NoError(t, second.Close())
}

func TestConcurrentNativeOperationsFenceAdmissionUntilLastCompletion(t *testing.T) {
	f := startFake(t, "session-operations", codex.Limits{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	parent, err := codex.Open(ctx, codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = parent.Close() })
	_, err = parent.Receive(ctx) // Native opening fact.
	require.NoError(t, err)
	firstCtx, cancelFirst := context.WithCancel(ctx)
	defer cancelFirst()
	first, second := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := f.c.StartTurn(firstCtx, types.TurnStartParams{ThreadID: "shared"})
		first <- err
	}()
	go func() {
		_, err := f.c.StartTurn(ctx, types.TurnStartParams{ThreadID: "shared"})
		second <- err
	}()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		count := 0
		for _, m := range trace(t, f.trace) {
			if m.Method == "turn/start" {
				count++
			}
		}
		if count == 2 {
			break
		}
		select {
		case <-ctx.Done():
			require.FailNow(t, "same-identity native operations did not coexist")
		case <-tick.C:
		}
	}
	options := codex.SessionOptions{Client: f.c, Resume: &types.ThreadResumeParams{ThreadID: "shared"}}
	_, err = codex.Open(ctx, options)
	require.ErrorIs(t, err, codex.ErrOwnershipConflict)
	emitRequests(t, f.c, `{"method":"thread/started","params":{"thread":{"id":"shared","parentThreadId":"main-1"}}}`)
	gap, err := parent.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, codex.ReceivedObservation, gap.Kind)
	require.Equal(t, "attachment-owned-or-release-unconfirmed", gap.Observation.Reason)
	cancelFirst()
	callError(t, <-first, codex.OutcomeUnknown, context.Canceled)
	_, err = codex.Open(ctx, options)
	require.ErrorIs(t, err, codex.ErrOwnershipConflict, "one completion must not clear another operation's reservation")
	require.NoError(t, f.c.Call(ctx, "session-release-operations", nil, nil))
	require.NoError(t, <-second)
	resumed, err := codex.Open(ctx, options)
	require.NoError(t, err, "the last completion must release the identity")
	t.Cleanup(func() { _ = resumed.Close() })
	require.NoError(t, resumed.Close())
}

func TestSharedObserversReleaseOnceAndKeepFailedReleaseFenced(t *testing.T) {
	for _, mode := range []string{"session-normal", "session-shared-detach-error"} {
		t.Run(mode, func(t *testing.T) {
			f := startFake(t, mode, codex.Limits{})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a, err := codex.Open(ctx, codex.SessionOptions{Client: f.c})
			require.NoError(t, err)
			t.Cleanup(func() { _ = a.Close() })
			b, err := codex.Open(ctx, codex.SessionOptions{Client: f.c})
			require.NoError(t, err)
			t.Cleanup(func() { _ = b.Close() })
			options := codex.SessionOptions{Client: f.c, Resume: &types.ThreadResumeParams{ThreadID: "shared"}}
			owner, err := codex.Open(ctx, options)
			require.NoError(t, err)
			t.Cleanup(func() { _ = owner.Close() })
			emitRequests(t, f.c,
				`{"method":"thread/started","params":{"thread":{"id":"shared","parentThreadId":"main-1"}}}`,
				`{"method":"thread/started","params":{"thread":{"id":"shared","parentThreadId":"main-2"}}}`)
			unsubscribeCount := func() int {
				count := 0
				for _, m := range trace(t, f.trace) {
					if m.Method == "thread/unsubscribe" {
						var params types.ThreadUnsubscribeParams
						require.NoError(t, json.Unmarshal(m.Params, &params))
						if params.ThreadID == "shared" {
							count++
						}
					}
				}
				return count
			}
			require.NoError(t, owner.Close())
			require.Zero(t, unsubscribeCount(), "main owner cannot detach remaining observers")
			_, err = f.c.ResumeThread(ctx, types.ThreadResumeParams{ThreadID: "shared"})
			require.ErrorIs(t, err, codex.ErrOwnershipConflict)
			require.NoError(t, a.Close())
			require.Zero(t, unsubscribeCount(), "one observer still needs the attachment")
			closeErr := b.Close()
			if mode == "session-normal" {
				require.NoError(t, closeErr)
			} else {
				require.Error(t, closeErr)
				require.Equal(t, closeErr, b.Close(), "Close must not retry failed release")
			}
			require.Equal(t, 1, unsubscribeCount())
			if mode == "session-normal" {
				reopened, err := codex.Open(ctx, options)
				require.NoError(t, err)
				t.Cleanup(func() { _ = reopened.Close() })
				require.NoError(t, reopened.Close())
			} else {
				_, err = codex.Open(ctx, options)
				require.ErrorIs(t, err, codex.ErrOwnershipConflict)
				_, err = f.c.UnsubscribeThread(ctx, types.ThreadUnsubscribeParams{ThreadID: "shared"})
				callError(t, err, codex.NotSent, codex.ErrOwnershipConflict)
				require.Equal(t, 1, unsubscribeCount(), "uncertain release must remain fenced without redispatch")
			}
			require.NoError(t, f.c.Call(ctx, "echo", json.RawMessage(`{}`), nil))
		})
	}
}
