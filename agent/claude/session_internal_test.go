package agentclaude

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestUnknownStateClearsPreviousStateAndRemainsActive(t *testing.T) {
	session := &session{}
	assert.Nil(t, session.updateState(agent.StateRequiresAction))

	previous := session.markUnknownState()
	require.NotNil(t, previous)
	assert.Equal(t, agent.StateRequiresAction, *previous)
	assert.True(t, session.workActive)
	assert.Nil(t, session.state)

	assert.Nil(t, session.updateState(agent.StateIdle))
	assert.False(t, session.workActive)
}
