package agent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

const configSentinel = "session-config-secret-validation"

func TestSessionConfigValidation(t *testing.T) {
	tests := []struct {
		name        string
		config      agent.SessionConfig
		wantReason  string
		wantMessage string
	}{
		{name: "empty"},
		{
			name: "literal values",
			config: agent.SessionConfig{
				Model:           " literal/model ",
				ReasoningEffort: " Native/High ",
			},
		},
		{
			name: "model invalid UTF-8",
			config: agent.SessionConfig{
				Model: configSentinel + string([]byte{0xff}),
			},
			wantReason:  "config_value_invalid_utf8",
			wantMessage: "model must be valid UTF-8",
		},
		{
			name: "effort NUL",
			config: agent.SessionConfig{
				ReasoningEffort: configSentinel + "\x00tail",
			},
			wantReason:  "config_value_contains_nul",
			wantMessage: "reasoning effort must not contain NUL",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertConfigValidation(
				t,
				(agent.OpenOptions{Cwd: t.TempDir(), Config: test.config}).Validate(),
				test.wantReason,
				test.wantMessage,
			)
		})
	}
}

func TestConfigChangeValidation(t *testing.T) {
	tests := []struct {
		name        string
		change      agent.ConfigChange
		wantReason  string
		wantMessage string
	}{
		{
			name:        "unknown ID",
			change:      agent.ConfigChange{ID: "sandbox", Value: "read-only"},
			wantReason:  "config_id_unknown",
			wantMessage: "configuration ID is not supported",
		},
		{
			name:        "empty value",
			change:      agent.ConfigChange{ID: agent.ConfigModel},
			wantReason:  "config_value_empty",
			wantMessage: "configuration value must be nonempty",
		},
		{
			name: "model invalid UTF-8",
			change: agent.ConfigChange{
				ID:    agent.ConfigModel,
				Value: configSentinel + string([]byte{0xff}),
			},
			wantReason:  "config_value_invalid_utf8",
			wantMessage: "model must be valid UTF-8",
		},
		{
			name: "effort NUL",
			change: agent.ConfigChange{
				ID:    agent.ConfigEffort,
				Value: configSentinel + "\x00tail",
			},
			wantReason:  "config_value_contains_nul",
			wantMessage: "reasoning effort must not contain NUL",
		},
		{
			name: "valid model",
			change: agent.ConfigChange{
				ID:    agent.ConfigModel,
				Value: " literal/model ",
			},
		},
		{
			name: "valid effort",
			change: agent.ConfigChange{
				ID:    agent.ConfigEffort,
				Value: " Native/High ",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertConfigValidation(
				t,
				test.change.Validate(),
				test.wantReason,
				test.wantMessage,
			)
		})
	}
}

func TestSessionConfigValidationUsesStableFieldOrder(t *testing.T) {
	tests := []struct {
		name        string
		config      agent.SessionConfig
		wantReason  string
		wantMessage string
	}{
		{
			name: "invalid UTF-8 before NUL",
			config: agent.SessionConfig{
				Model: configSentinel + "\x00" + string([]byte{0xff}),
			},
			wantReason:  "config_value_invalid_utf8",
			wantMessage: "model must be valid UTF-8",
		},
		{
			name: "model before reasoning effort",
			config: agent.SessionConfig{
				Model:           configSentinel + "\x00",
				ReasoningEffort: configSentinel + string([]byte{0xff}),
			},
			wantReason:  "config_value_contains_nul",
			wantMessage: "model must not contain NUL",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertConfigValidation(
				t,
				test.config.Validate(),
				test.wantReason,
				test.wantMessage,
			)
		})
	}
}

func assertConfigValidation(
	t *testing.T,
	err error,
	wantReason string,
	wantMessage string,
) {
	t.Helper()
	if wantReason == "" {
		require.NoError(t, err)
		return
	}

	require.Error(t, err)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
	var classified *agent.Error
	require.ErrorAs(t, err, &classified)
	assert.Equal(t, wantReason, classified.Reason)
	assert.Equal(t, wantMessage, classified.Message)
	assert.NotContains(t, err.Error(), configSentinel)
}
