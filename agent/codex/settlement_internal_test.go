package agentcodex

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/codex"
)

func TestInteractionSettlementWaitsForAdmittedAnswer(t *testing.T) {
	s := &session{}
	answerDone := make(chan struct{})
	interaction := &pendingInteraction{
		phase:      interactionAnswering,
		answerDone: answerDone,
	}
	type result struct {
		outcome agent.InteractionOutcome
		reason  string
	}
	settled := make(chan result, 1)
	go func() {
		outcome, reason := s.settlement(
			interaction,
			agent.InteractionCancelled,
			"session_closed",
		)
		settled <- result{outcome: outcome, reason: reason}
	}()

	select {
	case got := <-settled:
		require.FailNowf(t, "settlement did not wait", "got %+v", got)
	case <-time.After(20 * time.Millisecond):
	}

	s.mu.Lock()
	interaction.answered = true
	close(answerDone)
	interaction.answerDone = nil
	s.mu.Unlock()

	select {
	case got := <-settled:
		assert.Equal(t, agent.InteractionAnswered, got.outcome)
		assert.Equal(t, "session_closed", got.reason)
	case <-time.After(time.Second):
		require.FailNow(t, "settlement did not resume")
	}
}

func TestInteractionSettlementPreservesUnknownAnswerOutcome(t *testing.T) {
	tests := []struct {
		name        string
		answerErr   error
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
			answerErr: &codex.AnswerError{
				Dispatch: codex.NotSent,
				Cause:    codex.ErrRequestResolved,
			},
			wantOutcome: agent.InteractionExpired,
			wantReason:  "turn_completed",
		},
		{
			name: "outcome-unknown",
			answerErr: &codex.AnswerError{
				Dispatch: codex.OutcomeUnknown,
				Cause:    errors.New("synthetic write failure"),
			},
			wantOutcome: agent.InteractionFailed,
			wantReason:  "answer_outcome_unknown",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := &session{}
			outcome, reason := s.settlement(
				&pendingInteraction{answerErr: test.answerErr},
				agent.InteractionExpired,
				"turn_completed",
			)
			assert.Equal(t, test.wantOutcome, outcome)
			assert.Equal(t, test.wantReason, reason)
		})
	}
}
