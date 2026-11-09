package agentcodex_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestClosingOneSessionLeavesAnotherHealthyAndRetainsEarlyEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":93,"result":`+codexThreadStartResult("root-b")+`}`,
		`<{"method":"item/agentMessage/delta","params":{"threadId":"root-b","turnId":"turn-b","itemId":"early-item","delta":"early"}}`,
		`>{"id":94,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":94,"result":{"status":"unsubscribed"}}`,
		`>{"id":95,"method":"turn/start","params":{"input":[{"text":"second","type":"text"}],"threadId":"root-b"}}`,
		`<{"id":95,"result":{"turn":{"id":"turn-b","items":[],"status":"inProgress"}}}`,
		`<{"method":"turn/started","params":{"threadId":"root-b","turn":{"id":"turn-b","items":[],"status":"inProgress","startedAt":1700000010}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-b","turnId":"turn-b","completedAtMs":1700000010123,"item":{"type":"agentMessage","id":"answer-item","text":"second answer"}}}`,
		`<{"method":"turn/completed","params":{"threadId":"root-b","turn":{"id":"turn-b","items":[],"status":"completed","startedAt":1700000010,"completedAt":1700000011}}}`,
		`>{"id":96,"method":"thread/unsubscribe","params":{"threadId":"root-b"}}`,
		`<{"id":96,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	first, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	closeSessionOnCleanup(t, first)
	second, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	closeSessionOnCleanup(t, second)
	firstStream, err := first.Events()
	require.NoError(t, err)

	require.NoError(t, first.Close(ctx))
	_, err = firstStream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)

	secondStream, err := second.Events()
	require.NoError(t, err)
	early, err := secondStream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventAgentMessageChunk, early.Type())
	assert.Equal(t, "early", early.Payload.(agent.MessageChunk).Content[0].(agent.TextBlock).Text)

	_, err = second.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{agent.TextBlock{Text: "second"}},
	})
	require.NoError(t, err)
	for _, want := range []agent.EventType{
		agent.EventSessionStateUpdate,
		agent.EventAgentMessage,
		agent.EventSessionStateUpdate,
	} {
		event, nextErr := secondStream.Next(ctx)
		require.NoError(t, nextErr)
		assert.Equal(t, want, event.Type())
	}

	client, err := provider.NativeClient()
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NoError(t, second.Close(ctx))
}
