package agentclaude

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/interactionledger"
)

func TestClaudeInteractionSettlementWaitsForAdmittedAnswer(t *testing.T) {
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

	require.True(t, interaction.slot.CompleteSent(agent.InteractionCompleted))

	select {
	case got := <-settled:
		assert.Equal(t, interactionledger.Result{
			Outcome: agent.InteractionCompleted,
			Reason:  "session_closed",
		}, got)
	case <-time.After(time.Second):
		require.FailNow(t, "settlement did not resume")
	}
}

func TestClaudeInteractionSettlementPrecedence(t *testing.T) {
	tests := []struct {
		name        string
		prepare     func(*interactionledger.Slot)
		wantOutcome agent.InteractionOutcome
		wantReason  string
	}{
		{
			name:        "fallback",
			wantOutcome: agent.InteractionExpired,
			wantReason:  "native_state_advanced",
		},
		{
			name: "sent outcome",
			prepare: func(slot *interactionledger.Slot) {
				require.True(t, slot.Claim())
				require.True(t, slot.CompleteSent(agent.InteractionCancelled))
			},
			wantOutcome: agent.InteractionCancelled,
			wantReason:  "native_state_advanced",
		},
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
					Reason:  "native_state_advanced",
				},
				"native_state_advanced",
			)
			assert.Equal(t, test.wantOutcome, result.Outcome)
			assert.Equal(t, test.wantReason, result.Reason)
		})
	}
}
