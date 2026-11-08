package agent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestCapabilitiesDoNotAssumeMissingOrUnknownSupport(t *testing.T) {
	capabilities := agent.Capabilities{
		agent.CapabilityTextInput:  true,
		agent.CapabilityPermission: false,
		"futureCapability":         true,
	}

	assert.True(t, capabilities.Supports(agent.CapabilityTextInput))
	assert.False(t, capabilities.Supports(agent.CapabilityImageInput))
	assert.False(t, capabilities.Supports(agent.CapabilityPermission))
	assert.False(t, capabilities.Supports("futureCapability"))
}

func TestFeaturesUseEnabledDefaultAndPreserveExplicitValues(t *testing.T) {
	disabled := false
	enabled := true

	assert.True(t, (agent.Features{}).AgentQuestionsEnabled())
	assert.False(t, (agent.Features{AgentQuestions: &disabled}).AgentQuestionsEnabled())
	assert.True(t, (agent.Features{AgentQuestions: &enabled}).AgentQuestionsEnabled())
}
