package agentcodex_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentcodex "github.com/zoumo/agent-sdk-go/agent/codex"
)

func TestProviderRequiresStartBeforeOpenAndNativeClient(t *testing.T) {
	provider := agentcodex.New(agentcodex.Config{})
	options := agent.OpenOptions{Cwd: t.TempDir()}

	_, err := provider.Open(context.Background(), options)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))

	client, err := provider.NativeClient()
	require.Error(t, err)
	assert.Nil(t, client)
	assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))
}
