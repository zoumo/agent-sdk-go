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
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "tools"}}},
	})
	require.NoError(t, err)

	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	want := []struct {
		id       string
		kind     agent.ToolKind
		name     string
		provider agent.ToolProvider
		title    string
		status   string
		output   bool
	}{
		{"command-startup", agent.ToolKindExecute, "exec_command", agent.ToolProviderAgent, "echo startup", "completed", true},
		{"command-interaction", agent.ToolKindExecute, "write_stdin", agent.ToolProviderAgent, "echo interaction", "completed", true},
		{"command-agent", agent.ToolKindExecute, "commandExecution", agent.ToolProviderAgent, "echo agent", "completed", true},
		{"command-user-shell", agent.ToolKindExecute, "commandExecution", agent.ToolProviderAgent, "echo user", "completed", true},
		{"command-missing", agent.ToolKindExecute, "commandExecution", agent.ToolProviderAgent, "echo missing", "completed", true},
		{"command-unknown", agent.ToolKindExecute, "commandExecution", agent.ToolProviderAgent, "echo unknown", "completed", true},
		{"file", agent.ToolKindEdit, "fileChange", agent.ToolProviderAgent, "", "completed", false},
		{"function", agent.ToolKindOther, "nslookup", agent.ToolProviderDynamic, "", "completed", true},
		{"function-empty", agent.ToolKindOther, "lookup", agent.ToolProviderDynamic, "", "completed", true},
		{"mcp", agent.ToolKindOther, "echo", agent.ToolProvider("mcp:srv"), "echo", "completed", true},
		{"mcp-empty", agent.ToolKindOther, "empty", agent.ToolProviderMCP, "empty", "completed", true},
		{"mcp-colon", agent.ToolKindOther, "colon", agent.ToolProvider("mcp:srv:prod"), "colon", "completed", true},
		{"dynamic", agent.ToolKindOther, "nsdyn", agent.ToolProviderDynamic, "dyn", "completed", true},
		{"dynamic-empty", agent.ToolKindOther, "plain", agent.ToolProviderDynamic, "plain", "completed", true},
		{"collab", agent.ToolKindOther, "spawnAgent", agent.ToolProviderAgent, "", "completed", true},
		{"web", agent.ToolKindSearch, "webSearch", agent.ToolProviderAgent, "synthetic query", "completed", true},
		{"image", agent.ToolKindRead, "view_image", agent.ToolProviderAgent, "/synthetic/image.png", "completed", false},
		{"sleep", agent.ToolKindOther, "sleep", agent.ToolProviderAgent, "Sleep for 5ms", "completed", false},
		{"generated", agent.ToolKindOther, "imageGeneration", agent.ToolProviderAgent, "draw", "completed", true},
	}
	for _, expected := range want {
		callEvent := nextEvent(t, ctx, stream)
		require.Equal(t, agent.EventAgentToolCall, callEvent.Type(), "call %s", expected.id)
		call := payloadOf[agent.ToolCallPayload](t, callEvent)
		assert.Equal(t, expected.id, call.ID)
		assert.Equal(t, expected.kind, call.Kind)
		assert.Equal(t, expected.name, call.ToolName)
		assert.Equal(t, expected.provider, call.Provider)
		assert.Equal(t, expected.title, call.Title)
		assert.Equal(t, "in_progress", call.Status)

		resultEvent := nextEvent(t, ctx, stream)
		require.Equal(t, agent.EventAgentToolResult, resultEvent.Type(), "result %s", expected.id)
		result := payloadOf[agent.ToolResultPayload](t, resultEvent)
		assert.Equal(t, expected.id, result.ID)
		assert.Equal(t, expected.kind, result.Kind)
		assert.Equal(t, expected.name, result.ToolName)
		assert.Equal(t, expected.provider, result.Provider)
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
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200100,"item":{"type":"commandExecution","id":"command-startup","command":"echo startup","commandActions":[],"cwd":"/synthetic","source":"unifiedExecStartup","status":"inProgress"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200110,"item":{"type":"commandExecution","id":"command-startup","command":"echo startup","commandActions":[],"cwd":"/synthetic","status":"completed","aggregatedOutput":"startup\n","exitCode":0,"durationMs":7}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200120,"item":{"type":"commandExecution","id":"command-interaction","command":"echo interaction","commandActions":[],"cwd":"/synthetic","source":"unifiedExecInteraction","status":"inProgress"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200130,"item":{"type":"commandExecution","id":"command-interaction","command":"echo interaction","commandActions":[],"cwd":"/synthetic","status":"completed","aggregatedOutput":"interaction\n","exitCode":0,"durationMs":8}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200140,"item":{"type":"commandExecution","id":"command-agent","command":"echo agent","commandActions":[],"cwd":"/synthetic","source":"agent","status":"inProgress"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200150,"item":{"type":"commandExecution","id":"command-agent","command":"echo agent","commandActions":[],"cwd":"/synthetic","source":"agent","status":"completed","aggregatedOutput":"agent\n","exitCode":0,"durationMs":9}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200160,"item":{"type":"commandExecution","id":"command-user-shell","command":"echo user","commandActions":[],"cwd":"/synthetic","source":"userShell","status":"inProgress"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200170,"item":{"type":"commandExecution","id":"command-user-shell","command":"echo user","commandActions":[],"cwd":"/synthetic","source":"userShell","status":"completed","aggregatedOutput":"user\n","exitCode":0,"durationMs":10}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200180,"item":{"type":"commandExecution","id":"command-missing","command":"echo missing","commandActions":[],"cwd":"/synthetic","status":"inProgress"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200190,"item":{"type":"commandExecution","id":"command-missing","command":"echo missing","commandActions":[],"cwd":"/synthetic","status":"completed","aggregatedOutput":"missing\n","exitCode":0,"durationMs":11}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200120,"item":{"type":"commandExecution","id":"command-unknown","command":"echo unknown","commandActions":[],"cwd":"/synthetic","source":"futureSource","status":"inProgress"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200130,"item":{"type":"commandExecution","id":"command-unknown","command":"echo unknown","commandActions":[],"cwd":"/synthetic","source":"futureSource","status":"completed","aggregatedOutput":"unknown\n","exitCode":0,"durationMs":12}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200200,"item":{"type":"fileChange","id":"file","changes":[{"path":"/synthetic/a.txt","kind":{"type":"add"},"diff":"+hello\n"}],"status":"inProgress"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200210,"item":{"type":"fileChange","id":"file","changes":[{"path":"/synthetic/a.txt","kind":{"type":"add"},"diff":"+hello\n"}],"status":"completed"}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200300,"item":{"type":"functionCallOutput","id":"function","name":"lookup","namespace":"ns","output":"value"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200310,"item":{"type":"functionCallOutput","id":"function","name":"lookup","output":"value"}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200320,"item":{"type":"functionCallOutput","id":"function-empty","name":"lookup","output":"value"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200330,"item":{"type":"functionCallOutput","id":"function-empty","name":"lookup","output":"value"}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200400,"item":{"type":"mcpToolCall","id":"mcp","arguments":{"query":"synthetic"},"server":"srv","status":"inProgress","tool":"echo"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200410,"item":{"type":"mcpToolCall","id":"mcp","arguments":{"query":"synthetic"},"durationMs":9,"result":{"content":[{"type":"text","text":"ok"}]},"server":"","status":"completed","tool":"echo"}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200420,"item":{"type":"mcpToolCall","id":"mcp-empty","arguments":{},"server":"","status":"inProgress","tool":"empty"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200430,"item":{"type":"mcpToolCall","id":"mcp-empty","arguments":{},"result":{"content":[]},"server":"","status":"completed","tool":"empty"}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200440,"item":{"type":"mcpToolCall","id":"mcp-colon","arguments":{},"server":"srv:prod","status":"inProgress","tool":"colon"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200450,"item":{"type":"mcpToolCall","id":"mcp-colon","arguments":{},"result":{"content":[]},"server":"","status":"completed","tool":"colon"}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200500,"item":{"type":"dynamicToolCall","id":"dynamic","arguments":{"value":1},"namespace":"ns","status":"inProgress","tool":"dyn"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200510,"item":{"type":"dynamicToolCall","id":"dynamic","arguments":{"value":1},"contentItems":[{"type":"inputText","text":"done"}],"durationMs":11,"status":"completed","success":true,"tool":"dyn"}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000200520,"item":{"type":"dynamicToolCall","id":"dynamic-empty","arguments":{"value":2},"status":"inProgress","tool":"plain"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000200530,"item":{"type":"dynamicToolCall","id":"dynamic-empty","arguments":{"value":2},"contentItems":[{"type":"inputText","text":"done"}],"durationMs":12,"status":"completed","success":true,"tool":"plain"}}}`,
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
