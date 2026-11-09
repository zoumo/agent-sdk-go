package agentcodex_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestSessionConvertsSupportedCodexToolVariants(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"tools","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		codexTurnStartedFrame,
	)
	frames = append(frames, syntheticToolFrames()...)
	frames = append(frames,
		`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"completed","startedAt":1700000200,"completedAt":1700000201}}}`,
	)
	frames = append(frames, codexCloseFrames(94, "root-a")...)
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
	_, err = session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{agent.TextBlock{Text: "tools"}},
	})
	require.NoError(t, err)

	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	want := []struct {
		id     string
		kind   string
		name   string
		title  string
		status string
		output bool
	}{
		{"command", "execute", "command", "echo hello", "completed", true},
		{"file", "edit", "file_change", "", "completed", false},
		{"function", "function", "ns.lookup", "", "completed", true},
		{"mcp", "mcp", "srv.echo", "echo", "completed", true},
		{"dynamic", "dynamic", "ns.dyn", "dyn", "completed", true},
		{"collab", "collaboration", "spawnAgent", "", "completed", true},
		{"web", "search", "web_search", "synthetic query", "completed", true},
		{"image", "read", "image_view", "/synthetic/image.png", "completed", false},
		{"sleep", "wait", "sleep", "Sleep for 5ms", "completed", false},
		{"generated", "generate", "image_generation", "draw", "completed", true},
	}
	for _, expected := range want {
		callEvent := nextEvent(t, ctx, stream)
		require.Equal(t, agent.EventAgentToolCall, callEvent.Type(), "call %s", expected.id)
		call := payloadOf[agent.ToolCallPayload](t, callEvent)
		assert.Equal(t, expected.id, call.ID)
		assert.Equal(t, expected.kind, call.Kind)
		assert.Equal(t, expected.name, call.Name)
		assert.Equal(t, expected.title, call.Title)
		assert.Equal(t, "in_progress", call.Status)

		resultEvent := nextEvent(t, ctx, stream)
		require.Equal(t, agent.EventAgentToolResult, resultEvent.Type(), "result %s", expected.id)
		result := payloadOf[agent.ToolResultPayload](t, resultEvent)
		assert.Equal(t, expected.id, result.ID)
		assert.Equal(t, expected.kind, result.Kind)
		assert.Equal(t, expected.name, result.Name)
		assert.Equal(t, expected.title, result.Title)
		assert.Equal(t, expected.status, result.Status)
		require.NotNil(t, result.Output)
		if expected.output {
			require.NotNil(t, result.RawOutput)
		} else {
			assert.Nil(t, result.RawOutput)
		}
		if expected.id == "collab" {
			assert.Equal(t, "root-a", eventCodexMeta(t, resultEvent)["agentSenderThreadId"])
			assert.Equal(t, []string{"child-a"}, eventCodexMeta(t, resultEvent)["agentReceiverThreadIds"])
		}
	}

	idle := nextStatePayload(t, ctx, stream)
	require.NotNil(t, idle.State)
	assert.Equal(t, agent.StateIdle, *idle.State)
	assert.Equal(t, "completed", idle.StopReason)
}

func syntheticToolFrames() []string {
	return []string{
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200100,"item":{"type":"commandExecution","id":"command","command":"echo hello","commandActions":[],"cwd":"/synthetic","status":"inProgress"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200110,"item":{"type":"commandExecution","id":"command","command":"echo hello","commandActions":[],"cwd":"/synthetic","status":"completed","aggregatedOutput":"hello\n","exitCode":0,"durationMs":7}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200200,"item":{"type":"fileChange","id":"file","changes":[{"path":"/synthetic/a.txt","kind":{"type":"add"},"diff":"+hello\n"}],"status":"inProgress"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200210,"item":{"type":"fileChange","id":"file","changes":[{"path":"/synthetic/a.txt","kind":{"type":"add"},"diff":"+hello\n"}],"status":"completed"}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200300,"item":{"type":"functionCallOutput","id":"function","name":"lookup","namespace":"ns","output":"value"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200310,"item":{"type":"functionCallOutput","id":"function","name":"lookup","namespace":"ns","output":"value"}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200400,"item":{"type":"mcpToolCall","id":"mcp","arguments":{"query":"synthetic"},"server":"srv","status":"inProgress","tool":"echo"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200410,"item":{"type":"mcpToolCall","id":"mcp","arguments":{"query":"synthetic"},"durationMs":9,"result":{"content":[{"type":"text","text":"ok"}]},"server":"srv","status":"completed","tool":"echo"}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200500,"item":{"type":"dynamicToolCall","id":"dynamic","arguments":{"value":1},"namespace":"ns","status":"inProgress","tool":"dyn"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200510,"item":{"type":"dynamicToolCall","id":"dynamic","arguments":{"value":1},"contentItems":[{"type":"inputText","text":"done"}],"durationMs":11,"namespace":"ns","status":"completed","success":true,"tool":"dyn"}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200600,"item":{"type":"collabAgentToolCall","id":"collab","agentsStates":{"child-a":{"status":"running","message":null}},"receiverThreadIds":["child-a"],"senderThreadId":"root-a","status":"inProgress","tool":"spawnAgent"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200610,"item":{"type":"collabAgentToolCall","id":"collab","agentsStates":{"child-a":{"status":"completed","message":"done"}},"receiverThreadIds":["child-a"],"senderThreadId":"root-a","status":"completed","tool":"spawnAgent"}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200700,"item":{"type":"webSearch","id":"web","query":"synthetic query","action":{"type":"search","query":"synthetic query"}}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200710,"item":{"type":"webSearch","id":"web","query":"synthetic query","action":{"type":"search","query":"synthetic query"},"results":[{"title":"synthetic result"}]}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200800,"item":{"type":"imageView","id":"image","path":"/synthetic/image.png"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200810,"item":{"type":"imageView","id":"image","path":"/synthetic/image.png"}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200900,"item":{"type":"sleep","id":"sleep","durationMs":5}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200910,"item":{"type":"sleep","id":"sleep","durationMs":5}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000201000,"item":{"type":"imageGeneration","id":"generated","result":"","revisedPrompt":"draw","status":"inProgress","transparentBackground":true}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000201010,"item":{"type":"imageGeneration","id":"generated","result":"ZmFrZQ==","revisedPrompt":"draw","savedPath":"/synthetic/generated.png","status":"completed","transparentBackground":true}}}`,
	}
}
