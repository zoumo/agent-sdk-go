package agent_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestOpenOptionsValidateRequiresExplicitAbsoluteCwd(t *testing.T) {
	tests := []struct {
		name string
		cwd  string
	}{
		{name: "empty", cwd: ""},
		{name: "relative", cwd: "relative/path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (agent.OpenOptions{Cwd: tt.cwd}).Validate()

			require.Error(t, err)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
		})
	}
}

func TestOpenOptionsValidateAcceptsAbsoluteCwdWithoutImplicitDefaults(t *testing.T) {
	cwd := t.TempDir()
	require.True(t, filepath.IsAbs(cwd))

	require.NoError(t, (agent.OpenOptions{Cwd: cwd}).Validate())
}

func TestResumeOptionsValidateRequiresSessionIdentity(t *testing.T) {
	err := (agent.ResumeOptions{}).Validate()

	require.Error(t, err)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))

	err = (agent.ResumeOptions{
		SessionID: "session-1",
		OpenOptions: agent.OpenOptions{
			Cwd: t.TempDir(),
		},
	}).Validate()
	require.NoError(t, err)
}

func TestExecutionOptionsHaveOnlyModelAndReasoningEffort(t *testing.T) {
	optionType := reflect.TypeFor[agent.ExecutionOptions]()

	require.Equal(t, 2, optionType.NumField())
	assert.Equal(t, "Model", optionType.Field(0).Name)
	assert.Equal(t, reflect.TypeFor[string](), optionType.Field(0).Type)
	assert.Equal(t, "ReasoningEffort", optionType.Field(1).Name)
	assert.Equal(t, reflect.TypeFor[string](), optionType.Field(1).Type)
}

func TestResumeOptionsInheritOpenExecutionOptions(t *testing.T) {
	execution := agent.ExecutionOptions{
		Model:           "native-model",
		ReasoningEffort: "native-effort",
	}
	opened := agent.OpenOptions{
		Cwd:       t.TempDir(),
		Execution: execution,
	}
	resumed := agent.ResumeOptions{
		SessionID:   "session-1",
		OpenOptions: opened,
	}

	assert.Equal(t, execution, opened.Execution)
	assert.Equal(t, execution, resumed.Execution)
}
