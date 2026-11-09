package agentcodex_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestSessionSubmitsTextAndDeliversMinimalOrderedEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"hello","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		`>{"id":94,"method":"turn/start","params":{"input":[{"text":"overlap","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":94,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		`<{"method":"turn/started","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"inProgress","startedAt":1700000000,"future":true}},"futureEnvelope":1}`,
		`<{"method":"item/agentMessage/delta","params":{"threadId":"root-a","turnId":"turn-a","itemId":"item-a","delta":"hello","future":null}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000000123,"item":{"type":"agentMessage","id":"item-a","text":"hello","phase":"final_answer","future":{"big":9007199254740993}},"future":true}}`,
		`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"completed","startedAt":1700000000,"completedAt":1700000001,"future":null}}}`,
		`>{"id":95,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":95,"result":{"status":"unsubscribed"}}`,
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
	canceled, cancelWait := context.WithCancel(ctx)
	cancelWait()
	_, err = stream.Next(canceled)
	require.ErrorIs(t, err, context.Canceled)
	select {
	case <-session.Done():
		require.Fail(t, "canceling one event wait terminated the session")
	default:
	}

	receipt, err := session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}},
	})
	require.NoError(t, err)
	assert.Equal(t, agent.InputAppliedNewWork, receipt.Result)
	assert.Equal(t, "turn-a", receipt.Submission)
	require.NotNil(t, receipt.Acceptance.Sent)
	require.NotNil(t, receipt.Acceptance.NativeAccepted)
	assert.True(t, *receipt.Acceptance.Sent)
	assert.True(t, *receipt.Acceptance.NativeAccepted)
	overlap, err := session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{agent.TextBlock{Text: "overlap"}},
	})
	require.NoError(t, err)
	assert.Equal(t, agent.InputAppliedCurrentWork, overlap.Result)
	assert.Equal(t, receipt.Submission, overlap.Submission)

	events := make([]agent.Event, 0, 4)
	for range 4 {
		event, nextErr := stream.Next(ctx)
		require.NoError(t, nextErr)
		require.NoError(t, event.Validate())
		assert.Equal(t, "root-a", event.SessionID)
		events = append(events, event)
	}

	assert.Equal(t, agent.EventSessionStateUpdate, events[0].Type())
	running := agent.StateRunning
	assert.Equal(t, agent.StateUpdatePayload{State: &running}, events[0].Payload)
	assert.Equal(t, time.Unix(1700000000, 0).UTC(), events[0].Time)
	assert.Contains(t, rawCodexEvent(t, events[0]), `"future":true`)

	assert.Equal(t, agent.EventAgentMessageChunk, events[1].Type())
	assert.Equal(t, "item-a", events[1].ItemID)
	assert.Equal(t, agent.MessageChunk{
		MessageID: "item-a",
		Content:   []agent.ContentBlock{agent.TextBlock{Text: "hello"}},
	}, events[1].Payload)
	assert.Contains(t, rawCodexEvent(t, events[1]), `"future":null`)

	assert.Equal(t, agent.EventAgentMessage, events[2].Type())
	assert.Equal(t, "item-a", events[2].ItemID)
	assert.Equal(t, agent.AgentMessage{
		MessageID: "item-a",
		Content:   []agent.ContentBlock{agent.TextBlock{Text: "hello"}},
		Phase:     "final_answer",
	}, events[2].Payload)
	assert.Equal(t, time.UnixMilli(1700000000123).UTC(), events[2].Time)
	assert.Contains(t, rawCodexEvent(t, events[2]), `"big":9007199254740993`)

	assert.Equal(t, agent.EventSessionStateUpdate, events[3].Type())
	idle := agent.StateIdle
	assert.Equal(t, agent.StateUpdatePayload{
		PreviousState: &running,
		State:         &idle,
		StopReason:    "completed",
	}, events[3].Payload)
	assert.Equal(t, time.Unix(1700000001, 0).UTC(), events[3].Time)

	require.NoError(t, session.Close(ctx))
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestSessionCloseDrainsAcceptedEventsBeforeEOF(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`<{"method":"item/agentMessage/delta","params":{"threadId":"root-a","turnId":"turn-a","itemId":"item-a","delta":"drained"}}`,
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
	stream, err := session.Events()
	require.NoError(t, err)
	require.NoError(t, session.Close(ctx))

	event, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventAgentMessageChunk, event.Type())
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func rawCodexEvent(t *testing.T, event agent.Event) string {
	t.Helper()
	codexMeta, ok := event.Meta["codex"].(map[string]any)
	require.True(t, ok)
	raw, ok := codexMeta["raw"].(json.RawMessage)
	require.True(t, ok)
	return string(raw)
}
