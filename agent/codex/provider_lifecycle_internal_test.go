package agentcodex

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestProviderStartFailurePreservesPublicClassification(t *testing.T) {
	acquireFailure := errors.New("synthetic stream acquisition failure")
	provider := New(Config{})
	provider.streams = func(context.Context) (streamStart, error) {
		return streamStart{}, acquireFailure
	}

	_, err := provider.Start(context.Background())
	require.Error(t, err)
	assert.Equal(t, agent.ErrorUnavailable, agent.CodeOf(err))
	assert.ErrorIs(t, err, acquireFailure)
	var classified *agent.Error
	require.ErrorAs(t, err, &classified)
	assert.Equal(t, ErrorReasonCoreStartFailed, classified.Reason)

	client, err := provider.NativeClient()
	require.Error(t, err)
	assert.Nil(t, client)
	assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))

	require.NoError(t, provider.Stop(context.Background()))
	_, err = provider.Start(context.Background())
	require.Error(t, err)
	assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
}
