package codex_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestLoadEventsUseRealtimeConsumer(t *testing.T) {
	f := startFake(t, "history-events", codex.Limits{})
	h, err := codex.Load(context.Background(), codex.LoadOptions{Client: f.c, HistoryOptions: codex.HistoryOptions{ThreadID: "native-thread-42", CWD: "/synthetic", CodexHome: "/synthetic-home", PageSize: 1}})
	require.NoError(t, err)
	defer h.Close()
	var methods, messages []string
	consume := func(e types.Event) {
		methods = append(methods, e.Method)
		if n, ok := e.Data.(*types.ItemCompletedNotification); ok && n.Item.AgentMessage != nil {
			messages = append(messages, n.Item.AgentMessage.Text)
		}
	}
	for e := range historyEvents(t, h) {
		require.NotNil(t, e.History)
		require.True(t, json.Valid(e.History.Raw))
		require.Empty(t, e.Raw, "projected history is not a received wire envelope")
		consume(e)
	}
	require.NotEqual(t, codex.LoadPending, h.LoadState().Status)
	require.NoError(t, h.Err())
	require.Equal(t, []string{"item/completed", "turn/completed", "item/completed", "turn/completed"}, methods)
	require.Equal(t, []string{"done"}, messages)
	_, err = h.Submit(context.Background(), codex.Input{Input: []types.UserInput{}})
	require.NoError(t, err)
	for len(messages) < 2 {
		receiveCtx, stop := context.WithTimeout(context.Background(), time.Second)
		record, err := h.Receive(receiveCtx)
		stop()
		require.NoError(t, err)
		require.NotNil(t, record.Event)
		e := *record.Event
		require.Nil(t, e.History)
		consume(e)

	}
	require.Equal(t, []string{"done", "live answer"}, messages)
	require.NoError(t, h.Close())
}

func TestLoadEventsRetainToolsUnknownsAndInterruptedStatus(t *testing.T) {
	f := startFake(t, "history-rich", codex.Limits{})
	h, err := codex.Load(context.Background(), codex.LoadOptions{Client: f.c, HistoryOptions: codex.HistoryOptions{ThreadID: "native-thread-42", CWD: "/synthetic", CodexHome: "/synthetic-home"}})
	require.NoError(t, err)
	defer h.Close()
	var ids []string
	for e := range historyEvents(t, h) {
		require.NotNil(t, e.History)
		switch n := e.Data.(type) {
		case *types.ItemCompletedNotification:
			ids = append(ids, e.History.ItemID)
			require.Equal(t, []string{"completedAtMs"}, e.History.MissingFields)
			var params map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(e.Params, &params))
			require.NotContains(t, params, "completedAtMs")
			if n.Item.CommandExecution != nil {
				require.Equal(t, "failed", string(n.Item.CommandExecution.Status))
				require.Equal(t, int32(7), n.Item.CommandExecution.ExitCode.ValueOr(0))
				require.Equal(t, "retained failure", n.Item.CommandExecution.AggregatedOutput.ValueOr(""))
				require.JSONEq(t, `true`, string(n.Item.CommandExecution.UnknownFields["future"]))
			}
			if e.History.ItemID == "future" {
				require.JSONEq(t, `{"id":"future","type":"futureItem","opaque":{"retained":true}}`, string(n.Item.Unknown))
				require.Equal(t, `{"id":"future","type":"futureItem","opaque":{"retained":true}}`, string(e.History.Raw))
			}
		case *types.TurnCompletedNotification:
			require.Equal(t, types.TurnStatusInterrupted, n.Turn.Status)
			require.Contains(t, string(e.History.Raw), `"futureTurn":true`)
		default:
			t.Fatalf("unexpected replay value %T", e.Data)
		}
	}
	require.NotEqual(t, codex.LoadPending, h.LoadState().Status)
	require.NoError(t, h.Err())
	require.Equal(t, []string{"tool", "compact", "future"}, ids)
	require.Empty(t, f.c.Unattributed().Records)
}

func TestLoadEventsPreserveUnknownTurnWithoutInventingCompletion(t *testing.T) {
	f := startFake(t, "history-future-status", codex.Limits{})
	h, err := codex.Load(context.Background(), codex.LoadOptions{Client: f.c, HistoryOptions: codex.HistoryOptions{ThreadID: "native-thread-42", CWD: "/synthetic", CodexHome: "/synthetic-home"}})
	require.NoError(t, err)
	defer h.Close()
	var events []types.Event
	for e := range historyEvents(t, h) {
		events = append(events, e)
	}
	require.NotEqual(t, codex.LoadPending, h.LoadState().Status)
	require.NoError(t, h.Err())
	require.Len(t, events, 1)
	require.Empty(t, events[0].Method)
	turn, ok := events[0].Data.(*types.Turn)
	require.True(t, ok)
	require.Equal(t, types.TurnStatus("futureStatus"), turn.Status)
	require.Contains(t, string(events[0].History.Raw), `"opaque":true`)
}

func TestLoadHistoricalV2ActivityKeepsFactsWithoutLiveMembership(t *testing.T) {
	f := startFake(t, "history-v2-activity", codex.Limits{})
	s, err := codex.Load(context.Background(), codex.LoadOptions{Client: f.c, HistoryOptions: codex.HistoryOptions{ThreadID: "native-thread-42", CWD: "/synthetic", CodexHome: "/synthetic-home"}})
	require.NoError(t, err)
	defer s.Close()
	var events []types.Event
	for e := range historyEvents(t, s) {
		events = append(events, e)
	}
	require.Equal(t, codex.LoadSucceeded, s.LoadState().Status)
	require.Len(t, events, 2)
	e := events[0]
	require.Empty(t, e.Raw, "history projection is not a live envelope")
	require.NotNil(t, e.History)
	require.Equal(t, "historical-spawn", e.History.ItemID)
	var params struct {
		Item json.RawMessage `json:"item"`
	}
	require.NoError(t, json.Unmarshal([]byte(v2Activity("native-thread-42", "child-1", "historical-spawn", "started")), &params))
	require.Equal(t, string(params.Item), string(e.History.Raw))
	n, ok := e.Data.(*types.ItemCompletedNotification)
	require.True(t, ok)
	require.NotNil(t, n.Item.SubAgentActivity)
	require.Equal(t, "child-1", n.Item.SubAgentActivity.AgentThreadID)
	require.JSONEq(t, `[null,9007199254740993]`, string(n.Item.SubAgentActivity.UnknownFields["future"]))
	child := treeFrame("item/agentMessage/delta", `{"threadId":"child-1","turnId":"ct","itemId":"ci","delta":"history cannot authorize live delivery"}`)
	emitRequests(t, f.c, child)
	snap := f.c.Unattributed()
	require.Len(t, snap.Records, 1)
	require.Equal(t, child, string(snap.Records[0].Raw))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.Receive(ctx)
	require.ErrorIs(t, err, context.Canceled)
}
