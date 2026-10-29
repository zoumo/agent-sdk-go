package codex_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestPlannedCaseDuplicateOwner(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{})
	session, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	duplicate, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.ErrorIs(t, err, codex.ErrOwnershipConflict)
	require.Nil(t, duplicate)
}

func TestPlannedCaseMultipleQuestions(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{})
	session, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	emitRequests(t, f.c, requestFrame(`"multi"`, "item/tool/requestUserInput", `{"threadId":"native-thread-42","turnId":"t","itemId":"i","questions":[{"id":"color","header":"Color","question":"Which color?","isOther":true,"isSecret":false,"options":[]},{"id":"note","header":"Note","question":"Which note?","isOther":true,"isSecret":false,"options":[]}]}`))
	request := sessionRequest(t, session)
	const expected = `{"answers":{"color":{"answers":["Amber"]},"note":{"answers":["synthetic free text"]}}}`
	var answer types.ToolRequestUserInputResponse
	require.NoError(t, json.Unmarshal([]byte(expected), &answer))
	require.NoError(t, request.Answer(context.Background(), answer))
	responseBarrier(t, f)
	frames := answerFrames(t, f.trace)
	require.Len(t, frames, 1)
	require.JSONEq(t, expected, string(frames[0]["result"]))
}

func TestPlannedCaseMixedInput(t *testing.T) {
	f := startFake(t, "session-config", codex.Limits{})
	session, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	const raw = `[{"type":"text","text":"before","text_elements":[]},{"type":"image","url":"data:image/png;base64,SYNTHETIC"},{"type":"text","text":"after","text_elements":[]}]`
	var items []types.UserInput
	require.NoError(t, json.Unmarshal([]byte(raw), &items))
	_, err = session.Submit(context.Background(), codex.Input{Input: items})
	require.NoError(t, err)
	var submitted []json.RawMessage
	for _, frame := range trace(t, f.trace) {
		if frame.Method == "turn/start" {
			var params map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(frame.Params, &params))
			submitted = append(submitted, params["input"])
		}
	}
	require.Len(t, submitted, 1)
	require.JSONEq(t, raw, string(submitted[0]))
}

func TestPlannedCaseApplicationTool(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{})
	session, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	emitRequests(t, f.c, requestFrame(`"tool"`, "item/tool/call", `{"threadId":"native-thread-42","turnId":"t","callId":"app-call","tool":"fixture_read","arguments":{"path":"numbers.txt"}}`))
	request := sessionRequest(t, session)
	require.Equal(t, "item/tool/call", request.Method())
	const response = `{"contentItems":[{"type":"inputText","text":"SYNTHETIC TOOL RESULT"}],"success":true}`
	require.NoError(t, request.Handle().Answer(context.Background(), json.RawMessage(response)))
	responseBarrier(t, f)
	frames := answerFrames(t, f.trace)
	require.Len(t, frames, 1)
	require.Equal(t, `"tool"`, string(frames[0]["id"]))
	require.JSONEq(t, response, string(frames[0]["result"]))
}

func TestGapConfigurationBetweenCompletedExecutions(t *testing.T) {
	f := startFake(t, "session-config", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	for i := 0; i < 2; i++ {
		if i == 1 {
			receipt, e := s.SetModel(context.Background(), "second-model")
			require.NoError(t, e)
			require.True(t, receipt.NativeAccepted)
		}
		_, e := s.Submit(context.Background(), codex.Input{Input: []types.UserInput{}})
		require.NoError(t, e)
		emitRequests(t, f.c, `{"method":"turn/completed","params":{"threadId":"main-1","turn":{"id":"completed","status":"completed","items":[]}}}`)
		for {
			got, e := s.Receive(context.Background())
			require.NoError(t, e)
			if got.Event != nil && got.Event.Method == "turn/completed" {
				break
			}
		}
	}
	var methods []string
	for _, frame := range trace(t, f.trace) {
		if frame.Method == "turn/start" || frame.Method == "thread/settings/update" {
			methods = append(methods, frame.Method)
		}
	}
	require.Equal(t, []string{"turn/start", "thread/settings/update", "turn/start"}, methods)
}

func TestGapCreationThinkingAndOptionalValues(t *testing.T) {
	f := startFake(t, "session-config", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c, Thread: types.ThreadStartParams{Config: types.Some(map[string]json.RawMessage{"model_reasoning_effort": json.RawMessage(`"low"`)})}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	starts := 0
	for _, frame := range trace(t, f.trace) {
		if frame.Method == "thread/start" {
			starts++
			require.Contains(t, string(frame.Params), `"model_reasoning_effort":"low"`)
		}
	}
	require.Equal(t, 1, starts)
	for _, effort := range []types.Optional[types.ReasoningEffort]{{}, types.Null[types.ReasoningEffort]()} {
		_, err = s.SetThinking(context.Background(), codex.ThinkingChange{Effort: effort})
		require.NoError(t, err)
	}
	var params []json.RawMessage
	for _, frame := range trace(t, f.trace) {
		if frame.Method == "thread/settings/update" {
			params = append(params, frame.Params)
		}
	}
	require.Len(t, params, 2)
	require.JSONEq(t, `{"threadId":"main-1"}`, string(params[0]))
	require.JSONEq(t, `{"threadId":"main-1","effort":null}`, string(params[1]))
	var invalid types.ThreadSettingsUpdateParams
	require.Error(t, json.Unmarshal([]byte(`{"threadId":"main-1","effort":0}`), &invalid), "numeric zero is invalid native effort, not omission/null")
}

func TestGapChildInheritanceRemainsUnknown(t *testing.T) {
	f := startFake(t, "session-config", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	emitRequests(t, f.c, `{"method":"thread/started","params":{"thread":{"id":"child","parentThreadId":"main-1"}}}`)
	receipt, err := s.SetModel(context.Background(), "root-requested")
	require.NoError(t, err)
	require.True(t, receipt.NativeAccepted)
	require.Equal(t, "main-1", receipt.Requested.ThreadID)
	raw, err := receipt.Response.MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(raw))
	const child = `{"method":"future/child-config","params":{"threadId":"child","model":null,"inheritance":null}}`
	emitRequests(t, f.c, child)
	for {
		got, e := s.Receive(context.Background())
		require.NoError(t, e)
		if got.Event != nil && got.Event.Method == "future/child-config" {
			require.JSONEq(t, child, string(got.Raw))
			break
		}
	}
	settings := 0
	for _, frame := range trace(t, f.trace) {
		if frame.Method == "thread/settings/update" {
			settings++
			require.JSONEq(t, `{"threadId":"main-1","model":"root-requested"}`, string(frame.Params))
		}
	}
	require.Equal(t, 1, settings, "root acknowledgment must not configure a child")
}

func TestGapNativeThinkingSummary(t *testing.T) {
	f := startFake(t, "session-config", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	for _, summary := range []types.Optional[types.ReasoningSummary]{types.Some(types.ReasoningSummaryNone), types.Some(types.ReasoningSummaryConcise), types.Null[types.ReasoningSummary](), {}} {
		receipt, err := s.SetThinking(context.Background(), codex.ThinkingChange{Summary: summary})
		require.NoError(t, err)
		require.True(t, receipt.NativeAccepted)
		require.Equal(t, summary, receipt.Requested.Summary)
		require.False(t, receipt.Requested.Effort.IsSet())
	}
	var params []string
	for _, frame := range trace(t, f.trace) {
		if frame.Method == "thread/settings/update" {
			params = append(params, string(frame.Params))
		}
	}
	require.Len(t, params, 4)
	for i, want := range []string{`{"threadId":"main-1","summary":"none"}`, `{"threadId":"main-1","summary":"concise"}`, `{"threadId":"main-1","summary":null}`, `{"threadId":"main-1"}`} {
		require.JSONEq(t, want, params[i])
	}
	_, err = s.SetThinking(context.Background(), codex.ThinkingChange{Summary: types.Some[types.ReasoningSummary]("")})
	require.ErrorIs(t, err, codex.ErrInvalidInput)
}
