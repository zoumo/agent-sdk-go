package agentclaude_test

import (
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/claude"
)

func TestClaudeLiveStreamPreservesBlockIdentityAndToolLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-parity","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
		`>{"type":"user","session_id":"","parent_tool_use_id":null,"message":{"role":"user","content":"inspect"}}`,
		`<{"type":"system","subtype":"session_state_changed","session_id":"native-parity","state":"running","timestamp":"2026-11-07T13:00:00Z"}`,
		`<{"type":"stream_event","session_id":"native-parity","parent_tool_use_id":null,"event":{"type":"message_start","message":{"id":"response-1","model":"synthetic-model","content":[]}}}`,
		`<{"type":"stream_event","session_id":"native-parity","parent_tool_use_id":null,"event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}}`,
		`<{"type":"stream_event","session_id":"native-parity","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me inspect."}}}`,
		`<{"type":"assistant","session_id":"native-parity","parent_tool_use_id":null,"uuid":"thinking-frame","timestamp":"2026-11-07T13:00:01Z","message":{"id":"response-1","role":"assistant","model":"synthetic-model","content":[{"type":"thinking","thinking":"Let me inspect.","signature":"sig"}],"usage":{"input_tokens":0,"output_tokens":0}}}`,
		`<{"type":"stream_event","session_id":"native-parity","parent_tool_use_id":null,"event":{"type":"content_block_stop","index":0}}`,
		`<{"type":"stream_event","session_id":"native-parity","parent_tool_use_id":null,"event":{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}}`,
		`<{"type":"stream_event","session_id":"native-parity","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"I will read the file."}}}`,
		`<{"type":"assistant","session_id":"native-parity","parent_tool_use_id":null,"uuid":"text-frame","timestamp":"2026-11-07T13:00:02Z","message":{"id":"response-1","role":"assistant","model":"synthetic-model","content":[{"type":"text","text":"I will read the file."}]}}`,
		`<{"type":"stream_event","session_id":"native-parity","parent_tool_use_id":null,"event":{"type":"content_block_stop","index":1}}`,
		`<{"type":"stream_event","session_id":"native-parity","parent_tool_use_id":null,"event":{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tool-1","name":"Read","input":{}}}}`,
		`<{"type":"stream_event","session_id":"native-parity","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.txt\""}}}`,
		`<{"type":"stream_event","session_id":"native-parity","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":",\"precise\":9007199254740993}"}}}`,
		`<{"type":"assistant","session_id":"native-parity","parent_tool_use_id":null,"uuid":"tool-frame","timestamp":"2026-11-07T13:00:03Z","message":{"id":"response-1","role":"assistant","model":"synthetic-model","content":[{"type":"tool_use","id":"tool-1","name":"Read","input":{"path":"a.txt","precise":9007199254740993}}]}}`,
		`<{"type":"stream_event","session_id":"native-parity","parent_tool_use_id":null,"event":{"type":"content_block_stop","index":2}}`,
		`<{"type":"stream_event","session_id":"native-parity","parent_tool_use_id":null,"event":{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":15,"output_tokens":7,"cache_read_input_tokens":2,"cache_creation_input_tokens":1}}}`,
		`<{"type":"stream_event","session_id":"native-parity","parent_tool_use_id":null,"event":{"type":"message_stop"}}`,
		`<{"type":"user","session_id":"native-parity","parent_tool_use_id":null,"uuid":"result-1","timestamp":"2026-11-07T13:00:04Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"file contents","is_error":false}]},"tool_use_result":{"stdout":"file contents","precise":9007199254740993}}`,
		`<{"type":"result","subtype":"success","session_id":"native-parity","is_error":false,"timestamp":"2026-11-07T13:00:05Z","total_cost_usd":0.25,"usage":{"input_tokens":15,"output_tokens":7,"cache_read_input_tokens":2,"cache_creation_input_tokens":1},"modelUsage":{"synthetic-model":{"inputTokens":15,"outputTokens":7,"cacheReadInputTokens":2,"cacheCreationInputTokens":1,"costUSD":0.25,"contextWindow":1000000}}}`,
		`<{"type":"system","subtype":"session_state_changed","session_id":"native-parity","state":"idle","timestamp":"2026-11-07T13:00:06Z"}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)

	receipt, err := session.Submit(ctx, agent.Input{
		Content: []agent.ContentBlock{agent.TextBlock{Text: "inspect"}},
	})
	require.NoError(t, err)
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.True(t, *receipt.Acceptance.Sent)

	running := nextEvent(t, ctx, stream)
	assertStateUpdate(t, running, nil, agent.StateRunning, time.Date(2026, 11, 7, 13, 0, 0, 0, time.UTC))

	thinkingChunk := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventAgentThinkingChunk, thinkingChunk.Type())
	assert.Equal(t, "response-1:0", thinkingChunk.ItemID)
	assert.Equal(t, agent.ThinkingChunk{
		MessageID: "response-1:0",
		Content:   []agent.ContentBlock{agent.TextBlock{Text: "Let me inspect."}},
	}, thinkingChunk.Payload)
	assertBlockMeta(t, thinkingChunk, "response-1", 0)

	thinking := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventAgentThinking, thinking.Type())
	assert.Equal(t, "response-1:0", thinking.ItemID)
	assert.Equal(t, time.Date(2026, 11, 7, 13, 0, 1, 0, time.UTC), thinking.Time)
	assert.Equal(t, agent.AgentThinking{
		MessageID: "response-1:0",
		Content:   []agent.ContentBlock{agent.TextBlock{Text: "Let me inspect."}},
	}, thinking.Payload)
	assertBlockMeta(t, thinking, "response-1", 0)

	messageChunk := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventAgentMessageChunk, messageChunk.Type())
	assert.Equal(t, "response-1:1", messageChunk.ItemID)
	assert.Equal(t, agent.MessageChunk{
		MessageID: "response-1:1",
		Content:   []agent.ContentBlock{agent.TextBlock{Text: "I will read the file."}},
	}, messageChunk.Payload)
	assertBlockMeta(t, messageChunk, "response-1", 1)

	message := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventAgentMessage, message.Type())
	assert.Equal(t, "response-1:1", message.ItemID)
	assert.Equal(t, time.Date(2026, 11, 7, 13, 0, 2, 0, time.UTC), message.Time)
	assert.Equal(t, agent.AgentMessage{
		MessageID: "response-1:1",
		Content:   []agent.ContentBlock{agent.TextBlock{Text: "I will read the file."}},
	}, message.Payload)
	assertBlockMeta(t, message, "response-1", 1)

	toolChunkOne := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventAgentToolCallChunk, toolChunkOne.Type())
	assert.Equal(t, "tool-1", toolChunkOne.ItemID)
	assert.Equal(t, agent.ToolChunkPayload{
		ID:      "tool-1",
		Content: []agent.ContentBlock{agent.TextBlock{Text: `{"path":"a.txt"`}},
	}, toolChunkOne.Payload)
	assertBlockMeta(t, toolChunkOne, "response-1", 2)

	toolChunkTwo := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventAgentToolCallChunk, toolChunkTwo.Type())
	assert.Equal(t, agent.ToolChunkPayload{
		ID:      "tool-1",
		Content: []agent.ContentBlock{agent.TextBlock{Text: `,"precise":9007199254740993}`}},
	}, toolChunkTwo.Payload)

	toolCall := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventAgentToolCall, toolCall.Type())
	assert.Equal(t, "tool-1", toolCall.ItemID)
	assert.Equal(t, time.Date(2026, 11, 7, 13, 0, 3, 0, time.UTC), toolCall.Time)
	callPayload, ok := toolCall.Payload.(agent.ToolCallPayload)
	require.True(t, ok)
	assert.Equal(t, "tool-1", callPayload.ID)
	assert.Equal(t, agent.ToolKindRead, callPayload.Kind)
	assert.Equal(t, "Read", callPayload.ToolName)
	assert.Equal(t, agent.ToolProviderAgent, callPayload.Provider)
	assert.Empty(t, callPayload.Status)
	input, ok := callPayload.RawInput.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "a.txt", input["path"])
	assert.Equal(t, json.Number("9007199254740993"), input["precise"])

	toolResult := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventAgentToolResult, toolResult.Type())
	assert.Equal(t, "tool-1", toolResult.ItemID)
	assert.NotEqual(t, toolCall.EventID, toolResult.EventID)
	assert.Equal(t, time.Date(2026, 11, 7, 13, 0, 4, 0, time.UTC), toolResult.Time)
	resultPayload, ok := toolResult.Payload.(agent.ToolResultPayload)
	require.True(t, ok)
	assert.Equal(t, "tool-1", resultPayload.ID)
	assert.Equal(t, agent.ToolKindRead, resultPayload.Kind)
	assert.Equal(t, "Read", resultPayload.ToolName)
	assert.Equal(t, agent.ToolProviderAgent, resultPayload.Provider)
	assert.Equal(t, "completed", resultPayload.Status)
	assert.Equal(t, []agent.ContentBlock{agent.TextBlock{Text: "file contents"}}, resultPayload.Content)
	require.NotNil(t, resultPayload.RawOutput)
	rawOutput, ok := (*resultPayload.RawOutput).(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "file contents", rawOutput["stdout"])
	assert.Equal(t, json.Number("9007199254740993"), rawOutput["precise"])

	usage := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventSessionUsageUpdate, usage.Type())
	assert.Equal(t, time.Date(2026, 11, 7, 13, 0, 5, 0, time.UTC), usage.Time)
	assert.Equal(t, agent.UsageUpdatePayload{
		Used: 25,
		Size: 1000000,
		Cost: &agent.UsageCost{Amount: json.Number("0.25"), Currency: "USD"},
	}, usage.Payload)

	idle := nextEvent(t, ctx, stream)
	assertStateUpdate(t, idle, statePtr(agent.StateRunning), agent.StateIdle, time.Date(2026, 11, 7, 13, 0, 6, 0, time.UTC))

	require.NoError(t, session.Close(ctx))
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestClaudeToolIdentityMapping(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	tests := []struct {
		name     string
		kind     agent.ToolKind
		provider agent.ToolProvider
		failed   bool
	}{
		{name: "Read", kind: agent.ToolKindRead, provider: agent.ToolProviderAgent},
		{name: "Edit", kind: agent.ToolKindEdit, provider: agent.ToolProviderAgent},
		{name: "Write", kind: agent.ToolKindEdit, provider: agent.ToolProviderAgent},
		{name: "MultiEdit", kind: agent.ToolKindEdit, provider: agent.ToolProviderAgent},
		{name: "NotebookEdit", kind: agent.ToolKindEdit, provider: agent.ToolProviderAgent},
		{name: "Bash", kind: agent.ToolKindExecute, provider: agent.ToolProviderAgent},
		{name: "Glob", kind: agent.ToolKindSearch, provider: agent.ToolProviderAgent},
		{name: "Grep", kind: agent.ToolKindSearch, provider: agent.ToolProviderAgent},
		{name: "WebSearch", kind: agent.ToolKindSearch, provider: agent.ToolProviderAgent},
		{name: "WebFetch", kind: agent.ToolKindFetch, provider: agent.ToolProviderAgent},
		{name: "TodoWrite", kind: agent.ToolKindThink, provider: agent.ToolProviderAgent},
		{name: "TaskCreate", kind: agent.ToolKindThink, provider: agent.ToolProviderAgent},
		{name: "TaskUpdate", kind: agent.ToolKindThink, provider: agent.ToolProviderAgent},
		{name: "ExitPlanMode", kind: agent.ToolKindSwitchMode, provider: agent.ToolProviderAgent},
		{name: "Agent", kind: agent.ToolKindOther, provider: agent.ToolProviderAgent},
		{name: "Task", kind: agent.ToolKindOther, provider: agent.ToolProviderAgent},
		{name: "FutureNativeTool", kind: agent.ToolKindOther, provider: agent.ToolProviderAgent},
		{
			name:     "mcp__filesystem__Read",
			kind:     agent.ToolKindOther,
			provider: agent.ToolProvider("mcp:filesystem"),
		},
		{
			name:     "mcp__filesystem:prod__read_file",
			kind:     agent.ToolKindOther,
			provider: agent.ToolProvider("mcp:filesystem:prod"),
		},
		{
			name:     "mcp__filesystem__read__file",
			kind:     agent.ToolKindOther,
			provider: agent.ToolProviderMCP,
			failed:   true,
		},
		{
			name:     "mcp__filesystem___read_file",
			kind:     agent.ToolKindOther,
			provider: agent.ToolProviderMCP,
		},
		{
			name:     "mcp__filesystem__",
			kind:     agent.ToolKindOther,
			provider: agent.ToolProviderMCP,
		},
		{
			name:     "mcp____read_file",
			kind:     agent.ToolKindOther,
			provider: agent.ToolProviderMCP,
		},
	}

	var callBlocks []string
	var resultBlocks []string
	for index, test := range tests {
		id := "tool-" + strconv.Itoa(index)
		callBlocks = append(callBlocks,
			`{"type":"tool_use","id":`+jsonString(t, id)+
				`,"name":`+jsonString(t, test.name)+
				`,"input":{"case":`+jsonString(t, test.name)+`}}`)
		resultBlocks = append(resultBlocks,
			`{"type":"tool_result","tool_use_id":`+jsonString(t, id)+
				`,"content":`+jsonString(t, "result-"+test.name)+
				`,"is_error":`+strconv.FormatBool(test.failed)+`}`)
	}

	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-tool-identity","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
		`<{"type":"assistant","session_id":"native-tool-identity","message":{`+
			`"id":"response-tools","content":[`+strings.Join(callBlocks, ",")+`]}}`,
		`<{"type":"user","session_id":"native-tool-identity","message":{`+
			`"role":"user","content":[`+strings.Join(resultBlocks, ",")+`]}}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)

	for index, test := range tests {
		id := "tool-" + strconv.Itoa(index)
		event := nextEvent(t, ctx, stream)
		assert.Equal(t, agent.EventAgentToolCall, event.Type())
		assert.Equal(t, id, event.ItemID)
		call, ok := event.Payload.(agent.ToolCallPayload)
		require.True(t, ok)
		assert.Equal(t, id, call.ID)
		assert.Equal(t, test.kind, call.Kind)
		assert.Equal(t, test.name, call.ToolName)
		assert.Equal(t, test.provider, call.Provider)
		assert.Empty(t, call.Status)
		assert.Equal(t, map[string]any{"case": test.name}, call.RawInput)
	}

	for index, test := range tests {
		id := "tool-" + strconv.Itoa(index)
		event := nextEvent(t, ctx, stream)
		assert.Equal(t, agent.EventAgentToolResult, event.Type())
		assert.Equal(t, id, event.ItemID)
		result, ok := event.Payload.(agent.ToolResultPayload)
		require.True(t, ok)
		assert.Equal(t, id, result.ID)
		assert.Equal(t, test.kind, result.Kind)
		assert.Equal(t, test.name, result.ToolName)
		assert.Equal(t, test.provider, result.Provider)
		assert.Equal(t, []agent.ContentBlock{
			agent.TextBlock{Text: "result-" + test.name},
		}, result.Content)
		if test.failed {
			assert.Equal(t, "failed", result.Status)
		} else {
			assert.Equal(t, "completed", result.Status)
		}
	}
}

func TestClaudeToolResultCopiesObservedIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-tool-result","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
		`<{"type":"assistant","session_id":"native-tool-result","message":{`+
			`"id":"response-result","content":[`+
			`{"type":"tool_use","id":"tool-1","name":"Read","input":{"path":"a.txt"}}]}}`,
		`<{"type":"user","session_id":"native-tool-result",`+
			`"tool_use_result":{"toolName":"Edit","kind":"execute","provider":"mcp:wrong"},`+
			`"message":{"role":"user","content":[`+
			`{"type":"tool_result","tool_use_id":"tool-1","content":"Bash","is_error":false}]}}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)

	callEvent := nextEvent(t, ctx, stream)
	call, ok := callEvent.Payload.(agent.ToolCallPayload)
	require.True(t, ok)
	assert.Equal(t, agent.ToolKindRead, call.Kind)
	assert.Equal(t, "Read", call.ToolName)
	assert.Equal(t, agent.ToolProviderAgent, call.Provider)

	resultEvent := nextEvent(t, ctx, stream)
	result, ok := resultEvent.Payload.(agent.ToolResultPayload)
	require.True(t, ok)
	assert.Equal(t, call.Kind, result.Kind)
	assert.Equal(t, call.ToolName, result.ToolName)
	assert.Equal(t, call.Provider, result.Provider)
	assert.Equal(t, "completed", result.Status)
	require.NotNil(t, result.RawOutput)
	assert.Equal(t, map[string]any{
		"toolName": "Edit",
		"kind":     "execute",
		"provider": "mcp:wrong",
	}, *result.RawOutput)
}

func TestClaudeAssistantBlockIdentityGapsWithoutGuessing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-gap","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
		`<{"type":"assistant","session_id":"native-gap","uuid":"unproven-frame","message":{"id":"response-gap","content":[{"type":"text","text":"first"},{"type":"text","text":"second"}]}}`,
		`<{"type":"user","session_id":"native-gap","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"missing-tool","content":"orphan","is_error":false}]}}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)

	first := nextEvent(t, ctx, stream)
	assertGap(t, first, "assistant_block_coordinate_missing")
	second := nextEvent(t, ctx, stream)
	assertGap(t, second, "assistant_block_coordinate_missing")
	orphan := nextEvent(t, ctx, stream)
	assertGap(t, orphan, "tool_result_without_call")
}

func TestClaudeUserTextPreservesNativeMessageIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-user","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
		`<{"type":"user","session_id":"native-user","uuid":"event-uuid",`+
			`"user_message_uuid":"user-message-uuid",`+
			`"timestamp":"2026-11-07T13:09:00Z",`+
			`"message":{"id":"user-message","role":"user","content":"child prompt"}}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)

	message := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventUserMessage, message.Type())
	assert.Equal(t, "user-message", message.ItemID)
	assert.Equal(t, time.Date(2026, 11, 7, 13, 9, 0, 0, time.UTC), message.Time)
	assert.Equal(t, agent.UserMessage{
		MessageID: "user-message",
		Content:   []agent.ContentBlock{agent.TextBlock{Text: "child prompt"}},
	}, message.Payload)
	meta := requireClaudeMeta(t, message)
	assert.Equal(t, "event-uuid", meta["uuid"])
	assert.Equal(t, "user-message-uuid", meta["userMessageUuid"])
}

func TestClaudeMixedUserContentIsUnsupported(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-user-order","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
		`<{"type":"assistant","session_id":"native-user-order","uuid":"tool-frame",`+
			`"message":{"id":"response-tools","content":[`+
			`{"type":"tool_use","id":"tool-1","name":"Read","input":{"path":"a.txt"}},`+
			`{"type":"tool_use","id":"tool-2","name":"Read","input":{"path":"b.txt"}}]}}`,
		`<{"type":"user","session_id":"native-user-order","uuid":"mixed-frame",`+
			`"message":{"id":"mixed-message","role":"user","content":[`+
			`{"type":"text","text":"before"},`+
			`{"type":"tool_result","tool_use_id":"tool-1","content":"a"},`+
			`{"type":"text","text":"between"}]}}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)

	toolOne := nextEvent(t, ctx, stream)
	toolTwo := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventAgentToolCall, toolOne.Type())
	assert.Equal(t, "tool-1", toolOne.ItemID)
	assert.Equal(t, agent.EventAgentToolCall, toolTwo.Type())
	assert.Equal(t, "tool-2", toolTwo.ItemID)

	gap := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventSessionError, gap.Type())
	assert.Empty(t, gap.ItemID)
	assert.Equal(t, agent.SessionErrorPayload{
		Message: "Claude user message mixes text and tool_result blocks",
		Reason:  "user_message_mixed_content_unsupported",
	}, gap.Payload)
	meta := requireClaudeMeta(t, gap)
	assert.Equal(t, "mixed-frame", meta["uuid"])
	assert.Equal(t, 3, meta["blockCount"])

	require.NoError(t, session.Close(ctx))
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestClaudeAssistantFrameDeduplicationIsResponseScoped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frame := `<{"type":"assistant","session_id":"native-dedupe","uuid":"same-frame","timestamp":"2026-11-07T13:10:00Z","message":{"id":"response-dedupe","content":[{"type":"text","text":"once"}]}}`
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-dedupe","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
		`<{"type":"stream_event","session_id":"native-dedupe","parent_tool_use_id":null,"event":{"type":"message_start","message":{"id":"response-dedupe"}}}`,
		`<{"type":"stream_event","session_id":"native-dedupe","parent_tool_use_id":null,"event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`,
		frame,
		frame,
		`<{"type":"stream_event","session_id":"native-dedupe","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"late"}}}`,
		`<{"type":"stream_event","session_id":"native-dedupe","parent_tool_use_id":null,"event":{"type":"message_start","message":{"id":"response-new"}}}`,
		`<{"type":"stream_event","session_id":"native-dedupe","parent_tool_use_id":null,"event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`,
		`<{"type":"assistant","session_id":"native-dedupe","uuid":"same-frame","timestamp":"2026-11-07T13:10:01Z","message":{"id":"response-new","content":[{"type":"text","text":"next"}]}}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)

	first := nextEvent(t, ctx, stream)
	second := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventAgentMessage, first.Type())
	assert.Equal(t, agent.EventAgentMessage, second.Type())
	assert.Equal(t, "response-dedupe:0", first.ItemID)
	assert.Equal(t, "response-new:0", second.ItemID)
}

func TestClaudeAssistantFrameReusesStreamedResponseIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-fallback","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
		`<{"type":"stream_event","session_id":"native-fallback","parent_tool_use_id":null,"event":{"type":"message_start","message":{"id":"response-fallback"}}}`,
		`<{"type":"stream_event","session_id":"native-fallback","parent_tool_use_id":null,"event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`,
		`<{"type":"assistant","session_id":"native-fallback","uuid":"fallback-frame","message":{"role":"assistant","content":[{"type":"text","text":"fallback"}]}}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)

	message := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventAgentMessage, message.Type())
	assert.Equal(t, "response-fallback:0", message.ItemID)
	assert.Equal(t, agent.AgentMessage{
		MessageID: "response-fallback:0",
		Content:   []agent.ContentBlock{agent.TextBlock{Text: "fallback"}},
	}, message.Payload)
	assertBlockMeta(t, message, "response-fallback", 0)
}

func TestClaudeMalformedAssistantTextAndThinkingBecomeStableGaps(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-malformed","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
		`<{"type":"assistant","session_id":"native-malformed","uuid":"malformed-frame",`+
			`"message":{"id":"response-malformed","content":[`+
			`{"type":"text","text":null,"apiBlockIndex":0},`+
			`{"type":"thinking","apiBlockIndex":1}]}}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)

	textGap := nextEvent(t, ctx, stream)
	assertGap(t, textGap, "assistant_block_content_invalid")
	textMeta := requireClaudeMeta(t, textGap)
	assert.Equal(t, "text", textMeta["blockType"])
	assert.Equal(t, 0, textMeta["blockIndex"])

	thinkingGap := nextEvent(t, ctx, stream)
	assertGap(t, thinkingGap, "assistant_block_content_invalid")
	thinkingMeta := requireClaudeMeta(t, thinkingGap)
	assert.Equal(t, "thinking", thinkingMeta["blockType"])
	assert.Equal(t, 1, thinkingMeta["blockIndex"])
}

func TestClaudeWrongAssistantBlockTypeBecomesDecodeGapBeforeNativeTermination(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-decode-gap","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
		// Keep the malformed frame behind a client frame so Open cannot race it.
		`>{"type":"user","session_id":"","parent_tool_use_id":null,`+
			`"message":{"role":"user","content":"trigger malformed decode"}}`,
		`<{"type":"assistant","session_id":"native-decode-gap","uuid":"wrong-type-frame",`+
			`"message":{"id":"response-wrong-type","content":[`+
			`{"type":"text","text":42,"apiBlockIndex":0}]}}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	stream, err := session.Events()
	require.NoError(t, err)
	_, err = session.Submit(ctx, agent.Input{
		Content: []agent.ContentBlock{agent.TextBlock{Text: "trigger malformed decode"}},
	})
	require.NoError(t, err)

	gap := nextEvent(t, ctx, stream)
	assertGap(t, gap, "assistant_block_content_invalid")
	meta := requireClaudeMeta(t, gap)
	assert.Equal(t, "text", meta["blockType"])
	assert.Equal(t, 0, meta["blockIndex"])
	assert.Equal(t, "response-wrong-type", meta["responseId"])
	assert.Equal(t, true, meta["decodeFailure"])

	_, terminalErr := stream.Next(ctx)
	require.Error(t, terminalErr)
	assert.ErrorIs(t, terminalErr, claude.ErrProtocol)
	closeErr := session.Close(ctx)
	require.Error(t, closeErr)
	assert.ErrorIs(t, closeErr, claude.ErrProtocol)
}

func TestClaudePlanLikeToolsRemainNativeToolCalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-plan","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
		`<{"type":"assistant","session_id":"native-plan","uuid":"plan-frame",`+
			`"message":{"id":"response-plan","content":[`+
			`{"type":"tool_use","id":"todo-1","name":"TodoWrite","input":{"todos":[`+
			`{"content":"inspect","status":"completed"},`+
			`{"content":"change","status":"in_progress"},`+
			`{"content":"verify","status":"pending"}]}},`+
			`{"type":"tool_use","id":"task-create","name":"TaskCreate","input":{"subject":"change"}},`+
			`{"type":"tool_use","id":"task-update","name":"TaskUpdate","input":{"taskId":"1","status":"completed"}}]}}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)

	todoCall := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventAgentToolCall, todoCall.Type())
	assert.Equal(t, "todo-1", todoCall.ItemID)
	todo, ok := todoCall.Payload.(agent.ToolCallPayload)
	require.True(t, ok)
	assert.Equal(t, agent.ToolKindThink, todo.Kind)
	assert.Equal(t, "TodoWrite", todo.ToolName)
	assert.Equal(t, agent.ToolProviderAgent, todo.Provider)
	encodedInput, err := json.Marshal(todo.RawInput)
	require.NoError(t, err)
	assert.JSONEq(t,
		`{"todos":[`+
			`{"content":"inspect","status":"completed"},`+
			`{"content":"change","status":"in_progress"},`+
			`{"content":"verify","status":"pending"}]}`,
		string(encodedInput),
	)

	taskCreate := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventAgentToolCall, taskCreate.Type())
	assert.Equal(t, "task-create", taskCreate.ItemID)
	create, ok := taskCreate.Payload.(agent.ToolCallPayload)
	require.True(t, ok)
	assert.Equal(t, agent.ToolKindThink, create.Kind)
	assert.Equal(t, "TaskCreate", create.ToolName)
	assert.Equal(t, agent.ToolProviderAgent, create.Provider)

	taskUpdate := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventAgentToolCall, taskUpdate.Type())
	assert.Equal(t, "task-update", taskUpdate.ItemID)
	update, ok := taskUpdate.Payload.(agent.ToolCallPayload)
	require.True(t, ok)
	assert.Equal(t, agent.ToolKindThink, update.Kind)
	assert.Equal(t, "TaskUpdate", update.ToolName)
	assert.Equal(t, agent.ToolProviderAgent, update.Provider)
}

func TestClaudeInitialAndChangedCommandsPreserveOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"session_state_changed","session_id":"native-commands","state":"running","timestamp":"2026-11-07T13:20:00Z"}`,
		`<{"type":"system","subtype":"init","session_id":"native-commands","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258","slash_commands":["review","init"],"timestamp":"2026-11-07T13:20:01Z"}`,
		`<{"type":"system","subtype":"commands_changed","session_id":"native-commands","timestamp":"2026-11-07T13:20:02Z","commands":[{"name":"review","description":"Review code","argumentHint":"[path]","aliases":["r"],"builtin":true,"future":9007199254740993},{"name":"deploy","description":"Deploy app","argumentHint":"","aliases":[],"builtin":false}]}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)

	running := nextEvent(t, ctx, stream)
	assertStateUpdate(t, running, nil, agent.StateRunning, time.Date(2026, 11, 7, 13, 20, 0, 0, time.UTC))

	initial := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventSessionCommandsUpdate, initial.Type())
	assert.Equal(t, time.Date(2026, 11, 7, 13, 20, 1, 0, time.UTC), initial.Time)
	assert.Equal(t, agent.AvailableCommandsUpdatePayload{
		AvailableCommands: []agent.AvailableCommand{
			{Name: "review"},
			{Name: "init"},
		},
	}, initial.Payload)

	changed := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventSessionCommandsUpdate, changed.Type())
	assert.Equal(t, time.Date(2026, 11, 7, 13, 20, 2, 0, time.UTC), changed.Time)
	assert.Equal(t, agent.AvailableCommandsUpdatePayload{
		AvailableCommands: []agent.AvailableCommand{
			{Name: "review", Description: "Review code"},
			{Name: "deploy", Description: "Deploy app"},
		},
	}, changed.Payload)
	meta := requireClaudeMeta(t, changed)
	raw, ok := meta["raw"].(json.RawMessage)
	require.True(t, ok)
	assert.Contains(t, string(raw), `"future":9007199254740993`)
}

func nextEvent(t *testing.T, ctx context.Context, stream agent.EventStream) agent.Event {
	t.Helper()
	event, err := stream.Next(ctx)
	require.NoError(t, err)
	require.NoError(t, event.Validate())
	return event
}

func assertBlockMeta(t *testing.T, event agent.Event, response string, index int) {
	t.Helper()
	meta := requireClaudeMeta(t, event)
	assert.Equal(t, response, meta["responseId"])
	if number, ok := meta["blockIndex"].(json.Number); ok {
		parsed, err := number.Int64()
		require.NoError(t, err)
		assert.Equal(t, index, int(parsed))
	} else {
		assert.Equal(t, index, meta["blockIndex"])
	}
}

func assertStateUpdate(
	t *testing.T,
	event agent.Event,
	previous *agent.SessionState,
	state agent.SessionState,
	at time.Time,
) {
	t.Helper()
	assert.Equal(t, agent.EventSessionStateUpdate, event.Type())
	assert.Equal(t, at, event.Time)
	payload, ok := event.Payload.(agent.StateUpdatePayload)
	require.True(t, ok)
	assert.Equal(t, previous, payload.PreviousState)
	require.NotNil(t, payload.State)
	assert.Equal(t, state, *payload.State)
}

func assertGap(t *testing.T, event agent.Event, reason string) {
	t.Helper()
	assert.Equal(t, agent.EventSessionError, event.Type())
	payload, ok := event.Payload.(agent.SessionErrorPayload)
	require.True(t, ok)
	assert.Equal(t, reason, payload.Reason)
	assert.NotEmpty(t, payload.Message)
}

func statePtr(state agent.SessionState) *agent.SessionState {
	return new(state)
}
