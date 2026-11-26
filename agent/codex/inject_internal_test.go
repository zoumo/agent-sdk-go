package agentcodex

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestInjectDoesNotWriteDuringOverlappingOrUnknownInput(t *testing.T) {
	for name, setup := range map[string]func(*testing.T, *mainTurnOwnership){
		"query in flight":     func(t *testing.T, work *mainTurnOwnership) { beginTestSubmit(t, work) },
		"injection in flight": func(t *testing.T, work *mainTurnOwnership) { require.True(t, work.beginInject()) },
		"unknown query":       setUnknownTestSubmit,
	} {
		t.Run(name, func(t *testing.T) {
			session := &session{provider: New(Config{})}
			setup(t, &session.mainTurn)
			receipt, err := session.Inject(context.Background(), agent.Input{
				Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "context"}}},
			})
			require.Error(t, err)
			assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))
			assert.Equal(t, agent.Acceptance{Sent: new(false)}, receipt.Acceptance)
			assert.Empty(t, session.mainTurn.activeTurnID())
		})
	}
}

func TestInjectInFlightDoesNotAdmitQueryOrEstablishTurnOwnership(t *testing.T) {
	session := &session{provider: New(Config{})}
	require.True(t, session.mainTurn.observeStarted("previous"))
	session.mainTurn.observeIdle()
	require.True(t, session.mainTurn.observeCompleted("previous"))
	require.True(t, session.mainTurn.beginInject())
	receipt, err := session.StartOrSteer(context.Background(), agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "query"}}},
	})
	require.NoError(t, err)
	assert.Equal(t, agent.InputNotApplied, receipt.Result)
	assert.Equal(t, agent.Acceptance{Sent: new(false)}, receipt.Acceptance)
	assert.False(t, session.acceptsMainTurnContent("unrelated"))
	assert.Empty(t, session.mainTurn.activeTurnID())
}
