package agentcodex_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
)

func TestResumeDoesNotReplaySavedHistoryAndNativeClientUsesSameConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/resume","params":{"cwd":"/synthetic","excludeTurns":true,"threadId":"root-resume"}}`,
		`<{"id":92,"result":`+codexThreadResumeResult("root-resume")+`}`,
		`>{"id":93,"method":"thread/read","params":{"includeTurns":false,"threadId":"root-resume"}}`,
		`<{"id":93,"result":{"thread":`+codexThread("root-resume", "")+`}}`,
		`<{"method":"item/agentMessage/delta","params":{"threadId":"root-resume","turnId":"live-turn","itemId":"live-item","delta":"live"}}`,
		`>{"id":94,"method":"thread/unsubscribe","params":{"threadId":"root-resume"}}`,
		`<{"id":94,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID:   "root-resume",
		OpenOptions: agent.OpenOptions{Cwd: "/synthetic"},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	assert.Equal(t, "root-resume", session.ID())

	client, err := provider.NativeClient()
	require.NoError(t, err)
	read, err := client.ReadThread(ctx, codextypes.ThreadReadParams{
		ThreadID:     "root-resume",
		IncludeTurns: codextypes.Some(false),
	})
	require.NoError(t, err)
	assert.Equal(t, "root-resume", read.Thread.ID)

	stream, err := session.Events()
	require.NoError(t, err)
	event, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventAgentMessageChunk, event.Type())
	assert.Equal(t, agent.MessageChunk{
		MessageID: "live-item",
		Content:   []agent.ContentBlock{agent.TextBlock{Text: "live"}},
	}, event.Payload)

	require.NoError(t, session.Close(ctx))
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func codexThreadResumeResult(threadID string) string {
	const savedTurns = `[
		{
			"id": "saved-turn",
			"items": [
				{"type": "agentMessage", "id": "saved-item", "text": "saved history"}
			],
			"status": "completed",
			"startedAt": 1699999990,
			"completedAt": 1699999991
		}
	]`
	return strings.Replace(
		codexThreadStartResult(threadID),
		`"turns": []`,
		`"turns": `+savedTurns,
		1,
	)
}

func codexThread(threadID, turns string) string {
	if turns == "" {
		turns = "[]"
	}
	return `{
		"id": "` + threadID + `",
		"sessionId": "synthetic",
		"cliVersion": "0.162.1",
		"createdAt": 1,
		"updatedAt": 2,
		"cwd": "/synthetic",
		"ephemeral": false,
		"modelProvider": "synthetic",
		"preview": "",
		"projectId": null,
		"source": "appServer",
		"status": {"type": "idle"},
		"turns": ` + turns + `
	}`
}
