package agentcodex_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestSessionConvertsCodexEventSurfaceInNativeOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"run","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		`<{"method":"turn/started","params":{"threadId":"root-a","turn":{"id":"turn-a","rootTurnId":"root-turn","items":[],"status":"inProgress","startedAt":1700000100},"future":true}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000100123,"item":{"type":"userMessage","id":"user","content":[{"type":"text","text":"question"}]}}}`,
		`<{"method":"item/agentMessage/delta","params":{"threadId":"root-a","turnId":"turn-a","itemId":"agent","delta":"answer"}}`,
		`<{"method":"item/reasoning/summaryTextDelta","params":{"threadId":"root-a","turnId":"turn-a","itemId":"reason","delta":"summary"}}`,
		`<{"method":"item/reasoning/textDelta","params":{"threadId":"root-a","turnId":"turn-a","itemId":"reason","delta":"raw"}}`,
		`<{"method":"item/reasoning/summaryPartAdded","params":{"threadId":"root-a","turnId":"turn-a","itemId":"reason"}}`,
		`<{"method":"item/plan/delta","params":{"threadId":"root-a","turnId":"turn-a","itemId":"plan","delta":"step"}}`,
		`<{"method":"item/commandExecution/outputDelta","params":{"threadId":"root-a","turnId":"turn-a","itemId":"cmd","delta":"out"}}`,
		`<{"method":"item/fileChange/outputDelta","params":{"threadId":"root-a","turnId":"turn-a","itemId":"file","delta":"patch"}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000100200,"item":{"type":"commandExecution","id":"cmd","command":"echo hello","commandActions":[],"cwd":"/synthetic","status":"inProgress"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000100300,"item":{"type":"commandExecution","id":"cmd","command":"echo hello","commandActions":[],"cwd":"/synthetic","status":"completed","aggregatedOutput":"hello\n","exitCode":0,"durationMs":12}}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000100400,"item":{"type":"fileChange","id":"file","changes":[{"path":"/synthetic/a.txt","kind":{"type":"add"},"diff":"hello\n"}],"status":"inProgress"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000100500,"item":{"type":"fileChange","id":"file","changes":[{"path":"/synthetic/a.txt","kind":{"type":"add"},"diff":"hello\n"}],"status":"completed"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000100600,"item":{"type":"reasoning","id":"reason","summary":["think-1","think-2"],"content":["raw thought"]}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000100700,"item":{"type":"agentMessage","id":"agent","text":"answer complete","phase":"final_answer","future":{"big":9007199254740993}}}}`,
		`<{"method":"turn/plan/updated","params":{"threadId":"root-a","turnId":"turn-a","explanation":"working","plan":[{"step":"inspect","status":"completed"},{"step":"change","status":"inProgress"}]}}`,
		`<{"method":"thread/name/updated","params":{"threadId":"root-a","threadName":"Synthetic title"}}`,
		`<{"method":"thread/tokenUsage/updated","params":{"threadId":"root-a","turnId":"turn-a","tokenUsage":{"total":{"totalTokens":99},"last":{"totalTokens":15},"modelContextWindow":0}}}`,
		`<{"method":"thread/settings/updated","params":{"threadId":"root-a","threadSettings":{"cwd":"/synthetic","approvalPolicy":"on-request","approvalsReviewer":"user","sandboxPolicy":{"type":"readOnly"},"model":"synthetic-model","modelProvider":"mock","collaborationMode":{"mode":"default","settings":{"model":"synthetic-model"}}}}}`,
		`<{"method":"thread/archived","params":{"threadId":"root-a"}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000100800,"item":{"type":"futureTool","id":"future","payload":7}}}`,
		`<{"method":"future/notification","params":{"threadId":"root-a"}}`,
		`<{"method":"error","params":{"threadId":"root-a","turnId":"turn-a","willRetry":false,"error":{"message":"native failure"}}}`,
		`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","rootTurnId":"root-turn","items":[],"status":"completed","startedAt":1700000100,"completedAt":1700000102}}}`,
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
	_, err = session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{agent.TextBlock{Text: "run"}},
	})
	require.NoError(t, err)

	wantTypes := []agent.EventType{
		agent.EventSessionStateUpdate,
		agent.EventUserMessage,
		agent.EventAgentMessageChunk,
		agent.EventAgentThinkingChunk,
		agent.EventAgentThinkingChunk,
		agent.EventSessionError,
		agent.EventAgentToolCallChunk,
		agent.EventAgentToolCallChunk,
		agent.EventAgentToolCall,
		agent.EventAgentToolResult,
		agent.EventAgentToolCall,
		agent.EventAgentToolResult,
		agent.EventAgentThinking,
		agent.EventAgentMessage,
		agent.EventSessionInfoUpdate,
		agent.EventSessionUsageUpdate,
		agent.EventSessionError,
		agent.EventSessionError,
		agent.EventSessionError,
		agent.EventSessionError,
		agent.EventSessionError,
		agent.EventSessionStateUpdate,
	}
	events := make([]agent.Event, 0, len(wantTypes))
	for index, wantType := range wantTypes {
		event, nextErr := stream.Next(ctx)
		require.NoErrorf(t, nextErr, "event %d (%s)", index, wantType)
		require.NoError(t, event.Validate())
		require.Equal(t, wantType, event.Type())
		events = append(events, event)
	}

	assert.Equal(t, agent.UserMessage{
		MessageID: "user",
		Content:   []agent.ContentBlock{agent.TextBlock{Text: "question"}},
	}, events[1].Payload)
	assert.Equal(t, time.UnixMilli(1700000100123).UTC(), events[1].Time)
	assert.Equal(t, agent.MessageChunk{
		MessageID: "agent",
		Content:   []agent.ContentBlock{agent.TextBlock{Text: "answer"}},
	}, events[2].Payload)
	assert.Equal(t, agent.ThinkingChunk{
		MessageID: "reason",
		Content:   []agent.ContentBlock{agent.TextBlock{Text: "summary"}},
	}, events[3].Payload)
	assert.Equal(t, agent.ToolChunkPayload{
		ID:      "cmd",
		Content: []agent.ContentBlock{agent.TextBlock{Text: "out"}},
	}, events[6].Payload)
	assert.Equal(t, agent.ToolChunkPayload{
		ID:      "file",
		Content: []agent.ContentBlock{agent.TextBlock{Text: "patch"}},
	}, events[7].Payload)

	call := payloadOf[agent.ToolCallPayload](t, events[8])
	assert.Equal(t, "cmd", call.ID)
	assert.Equal(t, "execute", call.Kind)
	assert.Equal(t, "echo hello", call.Title)
	assert.Equal(t, "in_progress", call.Status)
	rawInput, ok := call.RawInput.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "echo hello", rawInput["command"])

	result := payloadOf[agent.ToolResultPayload](t, events[9])
	assert.Equal(t, "completed", result.Status)
	require.NotNil(t, result.ExitCode)
	assert.Equal(t, int32(0), *result.ExitCode)
	require.NotNil(t, result.DurationMs)
	assert.InDelta(t, float64(12), *result.DurationMs, 0)
	require.NotNil(t, result.Output)
	assert.Equal(t, "provided", result.Output.Availability)
	assert.Equal(t, "retained", result.Output.Completeness)
	assert.Equal(t, []agent.ContentBlock{agent.TextBlock{Text: "hello\n"}}, result.Content)
	require.NotNil(t, result.RawOutput)
	assert.Equal(t, "hello\n", *result.RawOutput)

	fileResult := payloadOf[agent.ToolResultPayload](t, events[11])
	assert.Equal(t, "edit", fileResult.Kind)
	assert.Equal(t, "file_change", fileResult.Name)
	assert.Equal(t, "completed", fileResult.Status)

	assert.Equal(t, agent.AgentThinking{
		MessageID: "reason",
		Content: []agent.ContentBlock{
			agent.TextBlock{Text: "think-1"},
			agent.TextBlock{Text: "think-2"},
		},
	}, events[12].Payload)
	assert.Equal(t, agent.AgentMessage{
		MessageID: "agent",
		Content:   []agent.ContentBlock{agent.TextBlock{Text: "answer complete"}},
		Phase:     "final_answer",
	}, events[13].Payload)
	assert.Equal(t, agent.InfoUpdatePayload{Title: new("Synthetic title")}, events[14].Payload)
	assert.Equal(t, agent.UsageUpdatePayload{Used: 15, Size: 0}, events[15].Payload)

	assert.Equal(t, "reasoning_part_boundary_unsupported", sessionErrorReason(t, events[5]))
	assert.Equal(t, "config_options_unsupported", sessionErrorReason(t, events[16]))
	assert.Equal(t, "archive_state_unsupported", sessionErrorReason(t, events[17]))
	assert.Equal(t, "unsupported_native_item", sessionErrorReason(t, events[18]))
	assert.Equal(t, "future", events[18].ItemID)
	assert.Equal(t, "unsupported_native_notification", sessionErrorReason(t, events[19]))
	assert.Equal(t, "native_error", sessionErrorReason(t, events[20]))

	idle := agent.StateIdle
	assert.Equal(t, agent.StateUpdatePayload{
		PreviousState: new(agent.StateRunning),
		State:         &idle,
		StopReason:    "completed",
	}, events[21].Payload)
	assert.Contains(t, rawCodexEvent(t, events[21]), `"rootTurnId":"root-turn"`)
	assert.Contains(t, rawCodexEvent(t, events[13]), `"big":9007199254740993`)
	assert.Equal(t, true, eventCodexMeta(t, events[15])["modelContextWindowAbsent"])
}

func TestThreadStatusChangeExpiresInteractions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"status","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		`<{"method":"turn/started","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"inProgress","startedAt":1700000200}}}`,
		`<{"method":"thread/status/changed","params":{"threadId":"root-a","status":{"type":"active","activeFlags":["waitingOnApproval"]}}}`,
		`<{"method":"thread/status/changed","params":{"threadId":"root-a","status":{"type":"idle"}}}`,
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
	_, err = session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{agent.TextBlock{Text: "status"}},
	})
	require.NoError(t, err)

	started := nextEvent(t, ctx, stream)
	require.Equal(t, agent.EventSessionStateUpdate, started.Type())
	active := nextEvent(t, ctx, stream)
	require.Equal(t, agent.EventSessionStateUpdate, active.Type())
	requiresAction := agent.StateRequiresAction
	assert.Equal(t, agent.StateUpdatePayload{
		PreviousState: new(agent.StateRunning),
		State:         &requiresAction,
	}, active.Payload)
	idle := nextEvent(t, ctx, stream)
	require.Equal(t, agent.EventSessionStateUpdate, idle.Type())
	idleState := agent.StateIdle
	assert.Equal(t, agent.StateUpdatePayload{
		PreviousState: &requiresAction,
		State:         &idleState,
	}, idle.Payload)
}

func TestThreadIdleBeforeTurnCompletionPreservesTerminalFact(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"run","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		`<{"method":"turn/started","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"inProgress","startedAt":1700000100}}}`,
		`<{"method":"thread/status/changed","params":{"threadId":"root-a","status":{"type":"idle"}}}`,
		`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"completed","startedAt":1700000100,"completedAt":1700000102}}}`,
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
		Content: []agent.ContentBlock{agent.TextBlock{Text: "run"}},
	})
	require.NoError(t, err)

	running := nextStatePayload(t, ctx, stream)
	require.NotNil(t, running.State)
	assert.Equal(t, agent.StateRunning, *running.State)
	idleFromStatus := nextStatePayload(t, ctx, stream)
	require.NotNil(t, idleFromStatus.State)
	assert.Equal(t, agent.StateIdle, *idleFromStatus.State)

	completed := nextStatePayload(t, ctx, stream)
	require.NotNil(t, completed.PreviousState)
	require.NotNil(t, completed.State)
	assert.Equal(t, agent.StateIdle, *completed.PreviousState)
	assert.Equal(t, agent.StateIdle, *completed.State)
	assert.Equal(t, "completed", completed.StopReason)
}

func TestSessionRejectsUnrelatedTurnAfterIdleObservation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"run","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		codexTurnStartedFrame,
		`<{"method":"thread/status/changed","params":{"threadId":"root-a","status":{"type":"idle"}}}`,
		`<{"method":"item/agentMessage/delta","params":{"threadId":"root-a","turnId":"other-turn","itemId":"other-item","delta":"unrelated"}}`,
		`<{"method":"turn/started","params":{"threadId":"root-a","turn":{"id":"other-turn","items":[],"status":"inProgress","startedAt":1700000201}}}`,
		`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"completed","startedAt":1700000200,"completedAt":1700000202}}}`,
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
		Content: []agent.ContentBlock{agent.TextBlock{Text: "run"}},
	})
	require.NoError(t, err)

	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	require.Equal(t, agent.StateIdle, nextState(t, ctx, stream))
	for index := range 2 {
		gap := nextEvent(t, ctx, stream)
		require.Equalf(t, agent.EventSessionError, gap.Type(), "unrelated event %d", index)
		assert.Equal(t, "stale_native_turn", sessionErrorReason(t, gap))
	}
	completed := nextStatePayload(t, ctx, stream)
	require.NotNil(t, completed.State)
	assert.Equal(t, agent.StateIdle, *completed.State)
	assert.Equal(t, "completed", completed.StopReason)
}

func TestSessionIgnoresCodexPlanNotifications(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := interactionSessionFrames(
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"turn-a","startedAtMs":1700000300100,"item":{"type":"plan","id":"plan-item","text":"not authoritative yet"}}}`,
		`<{"method":"item/plan/delta","params":{"threadId":"root-a","turnId":"turn-a","itemId":"plan-item","delta":"draft"}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000300200,"item":{"type":"plan","id":"plan-item","text":"authoritative plan text"}}}`,
		`<{"method":"turn/plan/updated","params":{"threadId":"root-a","turnId":"turn-a","explanation":"working","plan":[{"step":"inspect","status":"completed"},{"step":"change","status":"inProgress"}]}}`,
		`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"completed","startedAt":1700000300,"completedAt":1700000302}}}`,
	)
	frames = append(frames, codexCloseFrames(94, "root-a")...)
	session, stream := openInteractionSession(t, ctx, frames, agent.Features{})

	submitInteractionTurn(t, ctx, session)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	require.Equal(t, agent.StateIdle, nextState(t, ctx, stream))
}

func TestSessionRejectsStaleMainTurnEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"run","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		codexTurnStartedFrame,
		`<{"method":"item/agentMessage/delta","params":{"threadId":"root-a","turnId":"old-turn","itemId":"old-message","delta":"stale"}}`,
		`<{"method":"item/started","params":{"threadId":"root-a","turnId":"old-turn","startedAtMs":1700000200100,"item":{"type":"commandExecution","id":"old-command","command":"false","commandActions":[],"cwd":"/synthetic","status":"inProgress"}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"old-turn","completedAtMs":1700000200200,"item":{"type":"commandExecution","id":"old-command","command":"false","commandActions":[],"cwd":"/synthetic","status":"completed","aggregatedOutput":"stale","exitCode":1}}}`,
		`<{"method":"turn/plan/updated","params":{"threadId":"root-a","turnId":"old-turn","plan":[{"step":"stale","status":"pending"}]}}`,
		`<{"method":"turn/started","params":{"threadId":"root-a","turn":{"id":"other-turn","items":[],"status":"inProgress","startedAt":1700000200}}}`,
		`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"old-turn","items":[],"status":"completed","startedAt":1700000199,"completedAt":1700000199}}}`,
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
		Content: []agent.ContentBlock{agent.TextBlock{Text: "run"}},
	})
	require.NoError(t, err)

	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	for index := range 5 {
		gap := nextEvent(t, ctx, stream)
		require.Equalf(t, agent.EventSessionError, gap.Type(), "stale event %d", index)
		assert.Equal(t, "stale_native_turn", sessionErrorReason(t, gap))
		assert.Equal(t, "root-a", gap.SessionID)
	}
	completed := nextStatePayload(t, ctx, stream)
	require.NotNil(t, completed.State)
	assert.Equal(t, agent.StateIdle, *completed.State)
	assert.Equal(t, "completed", completed.StopReason)
}

func TestSessionPreservesChildEvidenceAndLineage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"delegate","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		codexTurnStartedFrame,
		`<{"method":"thread/started","params":{"thread":{"id":"child-a","parentThreadId":"root-a","future":{"big":9007199254740993}}}}`,
		`<{"method":"turn/started","params":{"threadId":"child-a","turn":{"id":"child-turn","items":[],"status":"inProgress","startedAt":1700000101}}}`,
		`<{"method":"item/agentMessage/delta","params":{"threadId":"child-a","turnId":"child-turn","itemId":"child-message","delta":"child "}}`,
		`<{"method":"item/completed","params":{"threadId":"child-a","turnId":"child-turn","completedAtMs":1700000101200,"item":{"type":"agentMessage","id":"child-message","text":"child complete","phase":"final_answer"}}}`,
		`<{"method":"turn/completed","params":{"threadId":"child-a","turn":{"id":"child-turn","items":[],"status":"completed","startedAt":1700000101,"completedAt":1700000102}}}`,
		`<{"method":"item/completed","params":{"threadId":"root-a","turnId":"turn-a","completedAtMs":1700000101300,"item":{"type":"collabAgentToolCall","id":"collab-root","agentsStates":{},"receiverThreadIds":["outside"],"senderThreadId":"root-a","status":"completed","tool":"sendInput"}}}`,
		`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"completed","startedAt":1700000100,"completedAt":1700000102}}}`,
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
		Content: []agent.ContentBlock{agent.TextBlock{Text: "delegate"}},
	})
	require.NoError(t, err)

	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))

	threadStarted := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventSessionError, threadStarted.Type())
	assert.Equal(t, "child_thread_notification_unsupported", sessionErrorReason(t, threadStarted))
	assert.Equal(t, "root-a", threadStarted.SessionID)
	assert.Equal(t, "child-a", eventCodexMeta(t, threadStarted)["sourceThreadId"])
	assert.Contains(t, rawCodexEvent(t, threadStarted), `"parentThreadId":"root-a"`)

	childTurnStarted := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventSessionError, childTurnStarted.Type())
	assert.Equal(t, "child_thread_notification_unsupported", sessionErrorReason(t, childTurnStarted))
	assert.Equal(t, "child-a", eventCodexMeta(t, childTurnStarted)["sourceThreadId"])
	assert.Equal(t, "root-a", eventCodexMeta(t, childTurnStarted)["parentThreadId"])

	chunk := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventAgentMessageChunk, chunk.Type())
	assert.Equal(t, "root-a", chunk.SessionID)
	assert.Equal(t, "child-message", chunk.ItemID)
	assert.Equal(t, "child-a", eventCodexMeta(t, chunk)["sourceThreadId"])
	assert.Equal(t, "root-a", eventCodexMeta(t, chunk)["parentThreadId"])
	assert.Equal(t, agent.MessageChunk{
		MessageID: "child-message",
		Content:   []agent.ContentBlock{agent.TextBlock{Text: "child "}},
	}, chunk.Payload)

	message := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventAgentMessage, message.Type())
	assert.Equal(t, "root-a", message.SessionID)
	assert.Equal(t, "child-a", eventCodexMeta(t, message)["sourceThreadId"])
	assert.Equal(t, "root-a", eventCodexMeta(t, message)["parentThreadId"])
	assert.Equal(t, agent.AgentMessage{
		MessageID: "child-message",
		Content:   []agent.ContentBlock{agent.TextBlock{Text: "child complete"}},
		Phase:     "final_answer",
	}, message.Payload)

	childTurnCompleted := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventSessionError, childTurnCompleted.Type())
	assert.Equal(t, "child_thread_notification_unsupported", sessionErrorReason(t, childTurnCompleted))
	assert.Equal(t, "root-a", childTurnCompleted.SessionID)

	collaboration := payloadOf[agent.ToolResultPayload](t, nextEvent(t, ctx, stream))
	assert.Equal(t, "collab-root", collaboration.ID)
	assert.Equal(t, "collaboration", collaboration.Kind)
	assert.Equal(t, "sendInput", collaboration.Name)

	observation := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventSessionError, observation.Type())
	assert.Equal(t, "native_observation", sessionErrorReason(t, observation))
	observationMeta := eventCodexMeta(t, observation)
	assert.Equal(t, "outside", observationMeta["threadId"])
	assert.Equal(t, "communication-target-outside-confirmed-scope", observationMeta["observationReason"])

	idle := nextStatePayload(t, ctx, stream)
	require.NotNil(t, idle.State)
	assert.Equal(t, agent.StateIdle, *idle.State)
	assert.Equal(t, "completed", idle.StopReason)
}

func nextEvent(t *testing.T, ctx context.Context, stream agent.EventStream) agent.Event {
	t.Helper()
	event, err := stream.Next(ctx)
	require.NoError(t, err)
	require.NoError(t, event.Validate())
	return event
}

func sessionErrorReason(t *testing.T, event agent.Event) string {
	t.Helper()
	return payloadOf[agent.SessionErrorPayload](t, event).Reason
}

func payloadOf[T any](t *testing.T, event agent.Event) T {
	t.Helper()
	payload, ok := event.Payload.(T)
	require.Truef(t, ok, "payload %T is not %T", event.Payload, *new(T))
	return payload
}

func eventCodexMeta(t *testing.T, event agent.Event) map[string]any {
	t.Helper()
	meta, ok := event.Meta["codex"].(map[string]any)
	require.True(t, ok)
	return meta
}
