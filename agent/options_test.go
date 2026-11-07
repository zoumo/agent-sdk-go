package agent_test

import (
	"path/filepath"
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
