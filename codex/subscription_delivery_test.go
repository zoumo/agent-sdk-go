package codex

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestSubscriptionDeliveryLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error
		abort bool
	}{
		{name: "close drains"},
		{name: "failure drains", cause: errors.New("trailing disconnect")},
		{name: "abort abandons", abort: true},
		{name: "abort preserves failure", cause: errors.New("first terminal cause"), abort: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := lineageSubscription(t, 8)
			s.client.mu.Lock()
			for i := 0; i < 8192; i++ {
				s.emitLocked(types.Event{Method: "synthetic", Data: i})
			}
			s.finishLocked(tc.cause)
			s.client.mu.Unlock()
			select {
			case <-s.Done():
			case <-time.After(time.Second):
				require.FailNow(t, "Done waited for drain")
			}
			assert.Equal(t, tc.cause, s.Err())
			if tc.abort {
				require.NoError(t, s.Abort())
				assert.Zero(t, s.queue.Stats().Items)
				_, ok := <-s.Events()
				assert.False(t, ok)
				select {
				case <-s.deliveryDone:
				default:
					require.FailNow(t, "Abort left delivery running")
				}
			} else {
				count := 0
				for e := range s.Events() {
					require.Equal(t, count, e.Data)
					count++
				}
				assert.Equal(t, 8192, count)
			}
			assert.Equal(t, tc.cause, s.Err())
		})
	}
}

func TestSubscriptionConcurrentAbort(t *testing.T) {
	s := lineageSubscription(t, 8)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = s.Close(); _ = s.Abort(); _ = s.Err() }()
	}
	wg.Wait()
	require.Zero(t, s.queue.Stats().Items)
	_, ok := <-s.Events()
	assert.False(t, ok)
}

func TestSubscriptionBatchAdmissionOrder(t *testing.T) {
	s := lineageSubscription(t, 8)
	s.client.mu.Lock()
	s.batching = true
	evidence := &types.SubagentEvidence{Method: "synthetic"}
	s.emitLocked(types.Event{Data: &types.SubagentAssociated{ThreadID: "child", Evidence: evidence}})
	s.emitLocked(types.Event{Method: "native"})
	s.commitBatchLocked()
	evidence.Method = "mutated"
	s.finishLocked(context.Canceled)
	s.client.mu.Unlock()
	first := <-s.Events()
	require.Equal(t, "synthetic", first.Data.(*types.SubagentAssociated).Evidence.Method)
	assert.Equal(t, "native", (<-s.Events()).Method)
	_, ok := <-s.Events()
	assert.False(t, ok)
	assert.ErrorIs(t, s.Err(), context.Canceled)
}
