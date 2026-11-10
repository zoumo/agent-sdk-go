package interactionledger_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/interactionledger"
)

func TestSlotAdmitsOneAnswerAndReopensOnlyAfterNotSent(t *testing.T) {
	var slot interactionledger.Slot

	require.True(t, slot.Claim())
	assert.Equal(t, interactionledger.Answering, slot.State())
	assert.False(t, slot.Claim())

	require.True(t, slot.ReleaseNotSent())
	assert.Equal(t, interactionledger.Pending, slot.State())
	require.True(t, slot.Claim())
	assert.Equal(t, interactionledger.Answering, slot.State())
	require.True(t, slot.CompleteSent(agent.InteractionAnswered))
	assert.Equal(t, interactionledger.Sent, slot.State())
	assert.False(t, slot.Claim())
	assert.False(t, slot.ReleaseNotSent())
}

func TestSlotSettlementPrecedence(t *testing.T) {
	tests := []struct {
		name        string
		prepare     func(*interactionledger.Slot)
		wantOutcome agent.InteractionOutcome
		wantReason  string
	}{
		{
			name: "terminal result",
			prepare: func(slot *interactionledger.Slot) {
				require.True(t, slot.Claim())
				require.True(t, slot.CompleteSent(agent.InteractionCancelled))
				require.True(t, slot.CompleteTerminal(
					agent.InteractionFailed,
					"answer_outcome_unknown",
				))
			},
			wantOutcome: agent.InteractionFailed,
			wantReason:  "answer_outcome_unknown",
		},
		{
			name: "sent outcome",
			prepare: func(slot *interactionledger.Slot) {
				require.True(t, slot.Claim())
				require.True(t, slot.CompleteSent(agent.InteractionCompleted))
			},
			wantOutcome: agent.InteractionCompleted,
			wantReason:  "answer_cancelled",
		},
		{
			name:        "fallback",
			prepare:     func(*interactionledger.Slot) {},
			wantOutcome: agent.InteractionExpired,
			wantReason:  "turn_completed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var slot interactionledger.Slot
			test.prepare(&slot)

			result := slot.Resolve(interactionledger.Result{
				Outcome: agent.InteractionExpired,
				Reason:  "turn_completed",
			}, "answer_cancelled")

			assert.Equal(t, test.wantOutcome, result.Outcome)
			assert.Equal(t, test.wantReason, result.Reason)
		})
	}
}

func TestSlotWaitsForAnswerCompletionWithoutAdapterLock(t *testing.T) {
	var slot interactionledger.Slot
	require.True(t, slot.Claim())

	result := make(chan interactionledger.Result, 1)
	go func() {
		result <- slot.Resolve(interactionledger.Result{
			Outcome: agent.InteractionCancelled,
			Reason:  "session_closed",
		}, "session_closed")
	}()

	select {
	case got := <-result:
		require.FailNowf(t, "settlement did not wait", "got %+v", got)
	case <-time.After(20 * time.Millisecond):
	}

	require.True(t, slot.CompleteSent(agent.InteractionAnswered))

	select {
	case got := <-result:
		assert.Equal(t, interactionledger.Result{
			Outcome: agent.InteractionAnswered,
			Reason:  "session_closed",
		}, got)
	case <-time.After(time.Second):
		require.FailNow(t, "settlement did not resume")
	}
}

func TestSlotTerminalResultConsumesRetryAuthority(t *testing.T) {
	var slot interactionledger.Slot
	require.True(t, slot.Claim())
	require.True(t, slot.CompleteTerminal(
		agent.InteractionFailed,
		"answer_outcome_unknown",
	))
	assert.Equal(t, interactionledger.Settled, slot.State())
	assert.False(t, slot.Claim())
	assert.False(t, slot.ReleaseNotSent())

	result := slot.Resolve(interactionledger.Result{
		Outcome: agent.InteractionExpired,
		Reason:  "turn_completed",
	}, "turn_completed")
	assert.Equal(t, interactionledger.Result{
		Outcome: agent.InteractionFailed,
		Reason:  "answer_outcome_unknown",
	}, result)
}

func TestSlotConcurrentClaimAdmitsExactlyOneAnswer(t *testing.T) {
	var slot interactionledger.Slot
	const workers = 64
	var admitted atomic.Int32
	start := make(chan struct{})
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			if slot.Claim() {
				admitted.Add(1)
			}
		}()
	}
	close(start)
	wait.Wait()

	assert.Equal(t, int32(1), admitted.Load())
	assert.Equal(t, interactionledger.Answering, slot.State())
}

func TestSlotConcurrentResolutionIsStable(t *testing.T) {
	var slot interactionledger.Slot
	require.True(t, slot.Claim())
	require.True(t, slot.CompleteSent(agent.InteractionCancelled))

	const workers = 32
	results := make(chan interactionledger.Result, workers)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results <- slot.Resolve(interactionledger.Result{
				Outcome: agent.InteractionExpired,
				Reason:  "turn_completed",
			}, "answer_cancelled")
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	want := interactionledger.Result{
		Outcome: agent.InteractionCancelled,
		Reason:  "answer_cancelled",
	}
	for result := range results {
		assert.Equal(t, want, result)
	}
	assert.Equal(t, interactionledger.Settled, slot.State())
}
