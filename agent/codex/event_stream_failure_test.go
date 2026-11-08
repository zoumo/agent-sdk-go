package agentcodex_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestEventStreamFailureIsStableAndTerminatesSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`<{"method":"item/agentMessage/delta","params":{"threadId":"root-a","turnId":"turn-a","itemId":"item-a"}}`,
		`>{"id":93,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":93,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	select {
	case <-session.Done():
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}
	stream, err := session.Events()
	require.NoError(t, err)

	_, firstErr := stream.Next(ctx)
	require.Error(t, firstErr)
	assert.Equal(t, agent.ErrorUnavailable, agent.CodeOf(firstErr))
	assert.Equal(t, agent.ErrorUnavailable, agent.CodeOf(session.Err()))
	assert.Equal(t, session.Err().Error(), firstErr.Error())
	select {
	case <-session.Done():
	default:
		require.Fail(t, "event conversion failure did not terminate the session")
	}

	_, secondErr := stream.Next(ctx)
	require.Error(t, secondErr)
	assert.Equal(t, firstErr.Error(), secondErr.Error())

	_, err = stream.Next(ctx)
	require.Error(t, err)
	assert.Equal(t, firstErr.Error(), err.Error())
	assert.NotErrorIs(t, err, context.Canceled)
	require.NoError(t, session.Close(ctx))
}
