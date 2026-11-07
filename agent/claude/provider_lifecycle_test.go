package agentclaude_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
)

func TestProviderLifecycleIsIdempotentAndTerminal(t *testing.T) {
	ctx := context.Background()
	provider := agentclaude.New(agentclaude.Config{})

	capabilities, err := provider.Start(ctx)
	require.NoError(t, err)
	assert.True(t, capabilities.Supports(agent.CapabilityTextInput))
	assert.True(t, capabilities.Supports(agent.CapabilityPermission))
	assert.True(t, capabilities.Supports(agent.CapabilityForm))

	repeated, err := provider.Start(ctx)
	require.NoError(t, err)
	assert.Equal(t, capabilities, repeated)

	require.NoError(t, provider.Stop(ctx))
	require.NoError(t, provider.Stop(ctx))

	_, err = provider.Start(ctx)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
}

func TestProviderRequiresStartBeforeOpen(t *testing.T) {
	provider := agentclaude.New(agentclaude.Config{})

	_, err := provider.Open(context.Background(), agent.OpenOptions{Cwd: t.TempDir()})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))
}
