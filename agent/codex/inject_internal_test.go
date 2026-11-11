package agentcodex

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestInjectDoesNotWriteDuringOverlappingOrUnknownInput(t *testing.T) {
	for name, session := range map[string]*session{
		"query in flight":     {submitting: true},
		"injection in flight": {injecting: true},
		"unknown query":       {submitUnknown: true},
	} {
		t.Run(name, func(t *testing.T) {
			receipt, err := session.Inject(context.Background(), agent.Input{
				Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "context"}}},
			})
			require.Error(t, err)
			assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))
			assert.Equal(t, agent.Acceptance{Sent: new(false)}, receipt.Acceptance)
			assert.Empty(t, session.activeTurn)
		})
	}
}

func TestInjectInFlightDoesNotAdmitQueryOrEstablishTurnOwnership(t *testing.T) {
	session := &session{injecting: true, idleTurn: "previous", lastFinished: "previous"}
	receipt, err := session.StartOrSteer(context.Background(), agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "query"}}},
	})
	require.NoError(t, err)
	assert.Equal(t, agent.InputNotApplied, receipt.Result)
	assert.Equal(t, agent.Acceptance{Sent: new(false)}, receipt.Acceptance)
	assert.False(t, session.ownsMainTurn("unrelated"))
	assert.Empty(t, session.activeTurn)
}
