package agentcodex

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/interactionledger"
)

func TestInteractionSettlementWaitsForAdmittedAnswer(t *testing.T) {
	interaction := &pendingInteraction{}
	require.True(t, interaction.slot.Claim())
	settled := make(chan interactionledger.Result, 1)
	go func() {
		settled <- interaction.slot.Resolve(
			interactionledger.Result{
				Outcome: agent.InteractionCancelled,
				Reason:  "session_closed",
			},
			"session_closed",
		)
	}()

	select {
	case got := <-settled:
		require.FailNowf(t, "settlement did not wait", "got %+v", got)
	case <-time.After(20 * time.Millisecond):
	}

	require.True(t, interaction.slot.CompleteSent(agent.InteractionAnswered))

	select {
	case got := <-settled:
		assert.Equal(t, interactionledger.Result{
			Outcome: agent.InteractionAnswered,
			Reason:  "session_closed",
		}, got)
	case <-time.After(time.Second):
		require.FailNow(t, "settlement did not resume")
	}
}

func TestInteractionSettlementPreservesUnknownAnswerOutcome(t *testing.T) {
	tests := []struct {
		name        string
		prepare     func(*interactionledger.Slot)
		wantOutcome agent.InteractionOutcome
		wantReason  string
	}{
		{
			name:        "pending",
			wantOutcome: agent.InteractionExpired,
			wantReason:  "turn_completed",
		},
		{
			name: "not-sent",
			prepare: func(slot *interactionledger.Slot) {
				require.True(t, slot.Claim())
				require.True(t, slot.ReleaseNotSent())
			},
			wantOutcome: agent.InteractionExpired,
			wantReason:  "turn_completed",
		},
		{
			name: "outcome-unknown",
			prepare: func(slot *interactionledger.Slot) {
				require.True(t, slot.Claim())
				require.True(t, slot.CompleteTerminal(
					agent.InteractionFailed,
					"answer_outcome_unknown",
				))
			},
			wantOutcome: agent.InteractionFailed,
			wantReason:  "answer_outcome_unknown",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			interaction := &pendingInteraction{}
			if test.prepare != nil {
				test.prepare(&interaction.slot)
			}
			result := interaction.slot.Resolve(
				interactionledger.Result{
					Outcome: agent.InteractionExpired,
					Reason:  "turn_completed",
				},
				"turn_completed",
			)
			assert.Equal(t, test.wantOutcome, result.Outcome)
			assert.Equal(t, test.wantReason, result.Reason)
		})
	}
}
