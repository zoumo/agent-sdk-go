package agent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestCapabilitiesRecognizeEveryDeclaredCapability(t *testing.T) {
	declared := []string{
		agent.CapabilityTextInput,
		agent.CapabilityImageInput,
		agent.CapabilityAudioInput,
		agent.CapabilityResourceInput,
		agent.CapabilityResourceLinkInput,
		agent.CapabilityPermission,
		agent.CapabilityForm,
		agent.CapabilityURL,
	}
	capabilities := make(agent.Capabilities, len(declared))
	for _, capability := range declared {
		capabilities[capability] = true
	}
	require.NoError(t, capabilities.Validate())

	for _, capability := range declared {
		t.Run(capability, func(t *testing.T) {
			assert.True(t, capabilities.Supports(capability))
			assert.False(t, (agent.Capabilities{}).Supports(capability))

			capabilities[capability] = false
			assert.False(t, capabilities.Supports(capability))
			capabilities[capability] = true
		})
	}
}

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

func TestCapabilitiesValidateRejectsUnknownKeys(t *testing.T) {
	require.NoError(t, agent.Capabilities{
		agent.CapabilityTextInput: false,
		agent.CapabilityForm:      true,
	}.Validate())

	cases := []struct {
		name         string
		capabilities agent.Capabilities
		message      string
	}{
		{
			name:         "one unknown key",
			capabilities: agent.Capabilities{"futureCapability": true},
			message:      `unknown capability "futureCapability"`,
		},
		{
			name: "multiple unknown keys are sorted",
			capabilities: agent.Capabilities{
				"zFutureCapability": true,
				"aFutureCapability": false,
			},
			message: `unknown capabilities ["aFutureCapability" "zFutureCapability"]`,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := test.capabilities.Validate()
			require.Error(t, err)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))

			var capabilityError *agent.Error
			require.ErrorAs(t, err, &capabilityError)
			assert.Equal(t, "unknown_capability", capabilityError.Reason)
			assert.Equal(t, test.message, capabilityError.Message)
		})
	}
}

func TestFeaturesUseEnabledDefaultAndPreserveExplicitValues(t *testing.T) {
	disabled := false
	enabled := true

	assert.True(t, (agent.Features{}).AgentQuestionsEnabled())
	assert.False(t, (agent.Features{AgentQuestions: &disabled}).AgentQuestionsEnabled())
	assert.True(t, (agent.Features{AgentQuestions: &enabled}).AgentQuestionsEnabled())
}
