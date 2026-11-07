package agentclaude

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/claude"
	claudetypes "github.com/zoumo/agent-sdk-go/claude/types"
)

func TestConfigIsCopiedAndNativeOptionsTargetResume(t *testing.T) {
	forward := true
	env := []string{}
	config := Config{
		CLIPath: "claude-fixture",
		Env:     env,
		Initialize: &claudetypes.InitializeOptions{
			Hooks:               json.RawMessage(`{"hook":"fixed"}`),
			Agents:              json.RawMessage(`{"agent":"fixed"}`),
			ForwardSubagentText: &forward,
		},
		ChildDetailPolicy: claude.ChildDetailPreserve,
		Subagents: claude.SubagentOptions{
			History: []claude.SubagentHistory{{SessionID: "session", TaskID: "task"}},
		},
	}
	provider := New(config)

	config.Env = append(config.Env, "MUTATED=1")
	forward = false
	config.Initialize.Hooks[1] = 'X'
	config.Initialize.Agents[1] = 'X'
	config.Subagents.History[0].TaskID = "mutated"

	assert.NotNil(t, provider.config.Env)
	assert.Empty(t, provider.config.Env)
	require.NotNil(t, provider.config.Initialize)
	require.NotNil(t, provider.config.Initialize.ForwardSubagentText)
	assert.True(t, *provider.config.Initialize.ForwardSubagentText)
	assert.JSONEq(t, `{"hook":"fixed"}`, string(provider.config.Initialize.Hooks))
	assert.JSONEq(t, `{"agent":"fixed"}`, string(provider.config.Initialize.Agents))
	assert.Equal(t, "task", provider.config.Subagents.History[0].TaskID)

	options := provider.nativeOptions("/workspace", "resume-id")
	assert.Equal(t, "claude-fixture", options.CLIPath)
	assert.Equal(t, "/workspace", options.WorkDir)
	assert.Equal(t, "resume-id", options.ResumeID)
	assert.Equal(t, claude.ChildDetailPreserve, options.ChildDetailPolicy)
}

func TestNilEnvironmentStaysNil(t *testing.T) {
	provider := New(Config{})
	assert.Nil(t, provider.config.Env)
}

func TestInitBufferRejectsExcessEventsAndBytes(t *testing.T) {
	t.Run("events", func(t *testing.T) {
		var buffer initBuffer
		for range maxInitBufferedEvents {
			require.NoError(t, buffer.add(claude.Received{}))
		}
		err := buffer.add(claude.Received{})
		assert.Equal(t, agent.ErrorResourceExhausted, agent.CodeOf(err))
	})

	t.Run("bytes", func(t *testing.T) {
		var buffer initBuffer
		err := buffer.add(claude.Received{Raw: make([]byte, maxInitBufferedBytes+1)})
		assert.Equal(t, agent.ErrorResourceExhausted, agent.CodeOf(err))
	})
}
