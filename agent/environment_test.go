package agent_test

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestEnvironmentOptionsValidation(t *testing.T) {
	for _, scope := range []string{"process", "shell"} {
		for _, tc := range []struct {
			name  string
			env   map[string]string
			valid bool
		}{
			{"nil", nil, true}, {"empty", map[string]string{}, true},
			{"literal and empty values", map[string]string{"TOKEN": "literal=$HOME=附加", "EMPTY": ""}, true},
			{"empty key", map[string]string{"": "secret-value"}, false},
			{"equals key", map[string]string{"A=B": "secret-value"}, false},
			{"nul key", map[string]string{"A\x00B": "secret-value"}, false},
			{"invalid key", map[string]string{string([]byte{0xff}): "secret-value"}, false},
			{"nul value", map[string]string{"TOKEN": "secret-value\x00"}, false},
			{"invalid value", map[string]string{"TOKEN": "secret-value" + string([]byte{0xff})}, false},
			{"case duplicate", map[string]string{"Path": "secret-value", "PATH": "other-secret"}, false},
			{"ASCII folding only", map[string]string{"ä": "one", "Ä": "two"}, true},
		} {
			t.Run(scope+"/"+tc.name, func(t *testing.T) {
				before := maps.Clone(tc.env)
				options := agent.OpenOptions{Cwd: t.TempDir()}
				if scope == "process" {
					options.ProcessEnv = tc.env
				} else {
					options.ShellEnv = tc.env
				}
				err := options.Validate()
				assert.Equal(t, before, tc.env)
				if tc.valid {
					require.NoError(t, err)
					return
				}
				require.Error(t, err)
				assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
				var classified *agent.Error
				require.ErrorAs(t, err, &classified)
				assert.Equal(t, agent.ErrorReasonEnvironmentInvalid, classified.Reason)
				assert.NotContains(t, err.Error(), "secret-value")
				assert.NotContains(t, err.Error(), "other-secret")
				for range 20 {
					assert.EqualError(t, options.Validate(), err.Error())
				}
			})
		}
	}
}

func TestEnvironmentCapabilitiesAreKnown(t *testing.T) {
	caps := agent.Capabilities{agent.CapabilityProcessEnv: true, agent.CapabilityShellEnv: true}
	require.NoError(t, caps.Validate())
	assert.True(t, caps.Supports(agent.CapabilityProcessEnv))
	assert.True(t, caps.Supports(agent.CapabilityShellEnv))
}
