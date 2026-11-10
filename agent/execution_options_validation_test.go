package agent_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

const executionOptionSentinel = "execution-option-secret-validation"

type executionOptionValidationError struct {
	reason  string
	message string
}

type executionOptionValidationRuntime struct {
	applyCalls int
}

func (*executionOptionValidationRuntime) EffectiveExecutionOptions() agent.ExecutionOptionsReport {
	return agent.ExecutionOptionsReport{}
}

func (*executionOptionValidationRuntime) Lock()   {}
func (*executionOptionValidationRuntime) Unlock() {}

func (*executionOptionValidationRuntime) RequireOpen() error { return nil }

func (r *executionOptionValidationRuntime) Apply(
	context.Context,
	agent.ExecutionOptionChange,
) error {
	r.applyCalls++
	return nil
}

func (*executionOptionValidationRuntime) ConvertError(err error) error { return err }

func (*executionOptionValidationRuntime) PrepareAccepted(
	agent.ExecutionOptionChange,
) func() {
	return func() {}
}

func (*executionOptionValidationRuntime) ClearUncertain(agent.ExecutionOption) {}

func (*executionOptionValidationRuntime) Acceptance(error) agent.Acceptance {
	return agent.Acceptance{}
}

func TestExecutionOptionsValidateMatchesRuntimeUpdateRules(t *testing.T) {
	tests := []struct {
		name      string
		execution agent.ExecutionOptions
		change    agent.ExecutionOptionChange
		openError executionOptionValidationError
		updateErr executionOptionValidationError
	}{
		{
			name:   "empty",
			change: agent.ExecutionOptionChange{Option: agent.ExecutionOptionModel},
			updateErr: executionOptionValidationError{
				reason:  "execution_option_empty_value",
				message: "execution option value must be nonempty",
			},
		},
		{
			name: "valid model",
			execution: agent.ExecutionOptions{
				Model: "literal/model value",
			},
			change: agent.ExecutionOptionChange{
				Option: agent.ExecutionOptionModel,
				Value:  "literal/model value",
			},
		},
		{
			name: "valid reasoning effort",
			execution: agent.ExecutionOptions{
				ReasoningEffort: "high",
			},
			change: agent.ExecutionOptionChange{
				Option: agent.ExecutionOptionReasoningEffort,
				Value:  "high",
			},
		},
		{
			name: "model invalid UTF-8",
			execution: agent.ExecutionOptions{
				Model: executionOptionSentinel + string([]byte{0xff}),
			},
			change: agent.ExecutionOptionChange{
				Option: agent.ExecutionOptionModel,
				Value:  executionOptionSentinel + string([]byte{0xff}),
			},
			openError: executionOptionValidationError{
				reason:  "execution_option_invalid_utf8",
				message: "model must be valid UTF-8",
			},
			updateErr: executionOptionValidationError{
				reason:  "execution_option_invalid_utf8",
				message: "model must be valid UTF-8",
			},
		},
		{
			name: "reasoning effort invalid UTF-8",
			execution: agent.ExecutionOptions{
				ReasoningEffort: executionOptionSentinel + string([]byte{0xff}),
			},
			change: agent.ExecutionOptionChange{
				Option: agent.ExecutionOptionReasoningEffort,
				Value:  executionOptionSentinel + string([]byte{0xff}),
			},
			openError: executionOptionValidationError{
				reason:  "execution_option_invalid_utf8",
				message: "reasoning effort must be valid UTF-8",
			},
			updateErr: executionOptionValidationError{
				reason:  "execution_option_invalid_utf8",
				message: "reasoning effort must be valid UTF-8",
			},
		},
		{
			name: "model NUL",
			execution: agent.ExecutionOptions{
				Model: executionOptionSentinel + "\x00tail",
			},
			change: agent.ExecutionOptionChange{
				Option: agent.ExecutionOptionModel,
				Value:  executionOptionSentinel + "\x00tail",
			},
			openError: executionOptionValidationError{
				reason:  "execution_option_contains_nul",
				message: "model must not contain NUL",
			},
			updateErr: executionOptionValidationError{
				reason:  "execution_option_contains_nul",
				message: "model must not contain NUL",
			},
		},
		{
			name: "reasoning effort NUL",
			execution: agent.ExecutionOptions{
				ReasoningEffort: executionOptionSentinel + "\x00tail",
			},
			change: agent.ExecutionOptionChange{
				Option: agent.ExecutionOptionReasoningEffort,
				Value:  executionOptionSentinel + "\x00tail",
			},
			openError: executionOptionValidationError{
				reason:  "execution_option_contains_nul",
				message: "reasoning effort must not contain NUL",
			},
			updateErr: executionOptionValidationError{
				reason:  "execution_option_contains_nul",
				message: "reasoning effort must not contain NUL",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			openErr := (agent.OpenOptions{
				Cwd:       t.TempDir(),
				Execution: test.execution,
			}).Validate()
			assertExecutionOptionValidation(
				t,
				openErr,
				test.openError,
			)

			executionRuntime := &executionOptionValidationRuntime{}
			_, updateErr := agent.SetExecutionOption(
				context.Background(),
				test.change,
				executionRuntime,
			)
			assertExecutionOptionValidation(
				t,
				updateErr,
				test.updateErr,
			)
			if test.updateErr.reason == "" {
				assert.Equal(t, 1, executionRuntime.applyCalls)
			} else {
				assert.Zero(t, executionRuntime.applyCalls)
			}
		})
	}
}

func TestExecutionOptionsValidateUsesStableFieldOrder(t *testing.T) {
	tests := []struct {
		name      string
		execution agent.ExecutionOptions
		want      executionOptionValidationError
	}{
		{
			name: "invalid UTF-8 before NUL",
			execution: agent.ExecutionOptions{
				Model: executionOptionSentinel + "\x00" + string([]byte{0xff}),
			},
			want: executionOptionValidationError{
				reason:  "execution_option_invalid_utf8",
				message: "model must be valid UTF-8",
			},
		},
		{
			name: "model before reasoning effort",
			execution: agent.ExecutionOptions{
				Model:           executionOptionSentinel + "\x00",
				ReasoningEffort: executionOptionSentinel + string([]byte{0xff}),
			},
			want: executionOptionValidationError{
				reason:  "execution_option_contains_nul",
				message: "model must not contain NUL",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertExecutionOptionValidation(
				t,
				test.execution.Validate(),
				test.want,
			)
		})
	}
}

func assertExecutionOptionValidation(
	t *testing.T,
	err error,
	want executionOptionValidationError,
) {
	t.Helper()
	if want.reason == "" {
		require.NoError(t, err)
		return
	}

	require.Error(t, err)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
	var classified *agent.Error
	require.ErrorAs(t, err, &classified)
	assert.Equal(t, want.reason, classified.Reason)
	assert.Equal(t, want.message, classified.Message)
	assert.NotContains(t, err.Error(), executionOptionSentinel)
}
