package agent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestInputResultValuesAreStable(t *testing.T) {
	assert.Equal(t, agent.InputAppliedCurrentWork, agent.InputResult("applied_current_work"))
	assert.Equal(t, agent.InputAppliedNewWork, agent.InputResult("applied_new_work"))
	assert.Equal(t, agent.InputAppliedBestEffort, agent.InputResult("applied_best_effort"))
	assert.Equal(t, agent.InputNotApplied, agent.InputResult("not_applied"))
}
