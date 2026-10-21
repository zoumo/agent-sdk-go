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
	sub, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
	require.NoError(t, err)
	defer sub.Abort()
	h, err := f.c.Load(context.Background(), codex.HistoryOptions{ThreadID: "native-thread-42", CWD: "/synthetic", CodexHome: "/synthetic-home", PageSize: 1})
	require.NoError(t, err)
	defer h.Close()
	var methods, messages []string
	consume := func(e types.Event) {
		methods = append(methods, e.Method)
		if n, ok := e.Data.(*types.ItemCompletedNotification); ok && n.Item.AgentMessage != nil {
			messages = append(messages, n.Item.AgentMessage.Text)
		}
	}
	for e := range h.Events() {
		require.NotNil(t, e.History)
		require.True(t, json.Valid(e.History.Raw))
		require.Empty(t, e.Raw, "projected history is not a received wire envelope")
		consume(e)
	}
	<-h.Done()
	require.NoError(t, h.Err())
	require.Equal(t, []string{"item/completed", "turn/completed", "item/completed", "turn/completed"}, methods)
	require.Equal(t, []string{"done"}, messages)
	_, err = f.c.StartTurn(context.Background(), types.TurnStartParams{ThreadID: "native-thread-42", Input: []types.UserInput{}})
	require.NoError(t, err)
	for len(messages) < 2 {
		select {
		case e := <-sub.Events():
			require.Nil(t, e.History)
			consume(e)
		case <-time.After(time.Second):
			t.Fatal("real-time event missing")
		}
	}
	require.Equal(t, []string{"done", "live answer"}, messages)
	require.NoError(t, h.Close())
}

func TestLoadEventsRetainToolsUnknownsAndInterruptedStatus(t *testing.T) {
	f := startFake(t, "history-rich", codex.Limits{})
	h, err := f.c.Load(context.Background(), codex.HistoryOptions{ThreadID: "native-thread-42", CWD: "/synthetic", CodexHome: "/synthetic-home"})
	require.NoError(t, err)
	defer h.Close()
	var ids []string
	for e := range h.Events() {
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
	<-h.Done()
	require.NoError(t, h.Err())
	require.Equal(t, []string{"tool", "compact", "future"}, ids)
	require.Empty(t, f.c.Requests())
}

func TestLoadEventsPreserveUnknownTurnWithoutInventingCompletion(t *testing.T) {
	f := startFake(t, "history-future-status", codex.Limits{})
	h, err := f.c.Load(context.Background(), codex.HistoryOptions{ThreadID: "native-thread-42", CWD: "/synthetic", CodexHome: "/synthetic-home"})
	require.NoError(t, err)
	defer h.Close()
	var events []types.Event
	for e := range h.Events() {
		events = append(events, e)
	}
	<-h.Done()
	require.NoError(t, h.Err())
	require.Len(t, events, 1)
	require.Empty(t, events[0].Method)
	turn, ok := events[0].Data.(*types.Turn)
	require.True(t, ok)
	require.Equal(t, types.TurnStatus("futureStatus"), turn.Status)
	require.Contains(t, string(events[0].History.Raw), `"opaque":true`)
}
