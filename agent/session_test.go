package agent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestInputResultValuesAreStable(t *testing.T) {
	assert.Equal(t, agent.InputResult("applied_current_work"), agent.InputAppliedCurrentWork)
	assert.Equal(t, agent.InputResult("applied_new_work"), agent.InputAppliedNewWork)
	assert.Equal(t, agent.InputResult("applied_best_effort"), agent.InputAppliedBestEffort)
	assert.Equal(t, agent.InputResult("not_applied"), agent.InputNotApplied)
}
