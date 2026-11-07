package agentcodex_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestSessionReportsNativeErrorAndFailedTurnWithoutTerminating(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"fail","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		`<{"method":"error","params":{"threadId":"root-a","turnId":"turn-a","willRetry":false,"error":{"message":"native failure"}}}`,
		`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"failed","startedAt":1700000020,"completedAt":1700000021,"error":{"message":"native failure"}}}}`,
		`>{"id":94,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":94,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)
	_, err = session.Submit(ctx, agent.Input{
		Content: []agent.ContentBlock{agent.TextBlock{Text: "fail"}},
	})
	require.NoError(t, err)

	event, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventSessionError, event.Type())
	assert.Equal(t, agent.SessionErrorPayload{
		Message: "native failure",
		Reason:  "native_error",
	}, event.Payload)

	event, err = stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventSessionStateUpdate, event.Type())
	idle := agent.StateIdle
	assert.Equal(t, agent.StateUpdatePayload{
		State:      &idle,
		StopReason: "failed",
		Error:      &agent.ObservedError{Message: "native failure"},
	}, event.Payload)
	select {
	case <-session.Done():
		t.Fatal("a failed turn terminated the whole session")
	default:
	}
	require.NoError(t, session.Close(ctx))
}
