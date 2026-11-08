package agentclaude_test

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
	assert.Equal(t, "Read", callPayload.Kind)
	assert.Equal(t, "Read", callPayload.Name)
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
	assert.Equal(t, "Read", resultPayload.Kind)
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
