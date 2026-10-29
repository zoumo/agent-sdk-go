package codex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// Public APIs cross the same independent JSONL peer over owned stdio, ws and wss.
// The peer's fixtures follow the pinned native protocol; they execute no agent.
func TestIndependentHistoryQueries(t *testing.T) {
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			t.Run("discovery", testQueryDiscovery)
			t.Run("pages", testQueryPages)
			t.Run("pre-canceled", testQueryPreCanceled)
			t.Run("live-and-cancellation", testQueryWithSession)
		})
	}
}

func queryContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// Query responses deliberately contain non-chronological/repeated IDs. The SDK
// must neither impose an ordering nor interpret them as completed live events.
const queryTurn = `{"id":"z","status":"inProgress","items":[],"itemsView":"summary","error":null,"future":9007199254740993}`
const queryThread = `{"id":"native-thread-42","sessionId":"native-session-7","cliVersion":"0.162.1","createdAt":3,"updatedAt":2,"cwd":"/server/only","ephemeral":false,"modelProvider":"mock","preview":"preview only","projectId":null,"source":{"type":"futureSource","x":1},"status":{"type":"futureStatus","x":null},"turns":[],"readState":{"unread":true},"future":{"large":9007199254740993}}`

func testQueryDiscovery(t *testing.T) {
	f := startFake(t, "query-pages", codex.Limits{})
	ctx := queryContext(t)
	first, err := f.c.ListThreads(ctx, types.ThreadListParams{})
	require.NoError(t, err)
	require.Len(t, first.Data, 1)
	require.Equal(t, "opaque/+:next", first.NextCursor.ValueOr(""))
	require.Equal(t, "opaque/back", first.BackwardsCursor.ValueOr(""))
	assert.JSONEq(t, `{"native-thread-42":{"unread":true}}`, string(first.UnknownFields["readStates"]))
	assert.JSONEq(t, `{"type":"futureStatus","x":null}`, string(first.Data[0].Status.Unknown))
	assert.JSONEq(t, `{"type":"futureSource","x":1}`, string(first.Data[0].Source.Unknown))
	second, err := f.c.ListThreads(ctx, types.ThreadListParams{Cursor: first.NextCursor})
	require.NoError(t, err)
	assert.Empty(t, second.Data)
	assert.True(t, second.NextCursor.IsNull())
	assert.False(t, second.BackwardsCursor.IsSet())
	// Preserve omitted/null/value and all native scope knobs, including false/0/[];
	// no SDK defaults or local/hosted filter policy is substituted.
	params := types.ThreadListParams{
		Archived: types.Some(true), Cursor: types.Null[string](), Limit: types.Some(uint32(0)),
		ModelProviders: types.Some([]string{}), SourceKinds: types.Some([]types.ThreadSourceKind{types.ThreadSourceKindCLI}),
		Cwd:       types.Some(types.ThreadListCwdFilter{Value: types.Ptr("/server/only")}),
		ProjectID: types.Null[string](), SectionID: types.Some("section"), ParentThreadID: types.Some("parent"),
		SearchTerm: types.Some("title"), SortKey: types.Some(types.ThreadSortKeyUpdatedAt), SortDirection: types.Some(types.SortDirectionAsc),
		UseStateDbOnly: types.Some(true), UnknownFields: map[string]json.RawMessage{"futureFilter": json.RawMessage(`{"x":null}`)},
	}
	_, err = f.c.ListThreads(ctx, params)
	require.NoError(t, err)
	_, err = f.c.ListThreads(ctx, types.ThreadListParams{AncestorThreadID: types.Some("ancestor"), Archived: types.Some(false), UseStateDbOnly: types.Some(false)})
	require.NoError(t, err)
	_, err = f.c.ListThreads(ctx, types.ThreadListParams{Originators: types.Some([]string{"hosted-only"})})
	checkQueryRPCError(t, err, -32602, "originator filtering is not supported by the local app-server")
	_, err = f.c.ListThreads(ctx, types.ThreadListParams{Cursor: types.Some("bad")})
	checkQueryRPCError(t, err, -32600, "invalid cursor")
	read, err := f.c.ReadThread(ctx, types.ThreadReadParams{ThreadID: "native-thread-42", IncludeTurns: types.Some(false)})
	require.NoError(t, err)
	assert.Equal(t, "preview only", read.Thread.Preview)
	assert.JSONEq(t, `{"unread":true}`, string(read.UnknownFields["readState"]))
	assert.Empty(t, read.Thread.Turns)
	// Mutating a returned map, slice, or raw union does not affect another result.
	require.NotEmpty(t, first.Data[0].UnknownFields["readState"])
	require.NotEmpty(t, first.Data[0].Status.Unknown)
	first.Data[0].UnknownFields["readState"][0] = 'x'
	first.Data[0].Status.Unknown[0] = 'x'
	first.Data[0].ID = "mutated"
	again, err := f.c.ListThreads(ctx, types.ThreadListParams{})
	require.NoError(t, err)
	require.Len(t, again.Data, 1)
	assert.Equal(t, "native-thread-42", again.Data[0].ID)
	assert.JSONEq(t, `{"unread":true}`, string(again.Data[0].UnknownFields["readState"]))
	assert.JSONEq(t, `{"type":"futureStatus","x":null}`, string(again.Data[0].Status.Unknown))
	frames := queryTrace(t, f, 0)
	require.Len(t, frames, 8)
	assert.JSONEq(t, `{}`, string(frames[0].Params))
	assert.JSONEq(t, `{"cursor":"opaque/+:next"}`, string(frames[1].Params))
	assert.JSONEq(t, `{"archived":true,"cursor":null,"limit":0,"modelProviders":[],"sourceKinds":["cli"],"cwd":"/server/only","projectId":null,"sectionId":"section","parentThreadId":"parent","searchTerm":"title","sortKey":"updated_at","sortDirection":"asc","useStateDbOnly":true,"futureFilter":{"x":null}}`, string(frames[2].Params))
	assert.JSONEq(t, `{"ancestorThreadId":"ancestor","archived":false,"useStateDbOnly":false}`, string(frames[3].Params))
	assert.JSONEq(t, `{"threadId":"native-thread-42","includeTurns":false}`, string(frames[6].Params))
}

func testQueryPages(t *testing.T) {
	f := startFake(t, "query-pages", codex.Limits{})
	ctx := queryContext(t)
	turns, err := f.c.ListThreadTurns(ctx, types.ThreadTurnsListParams{ThreadID: "native-thread-42"})
	require.NoError(t, err)
	require.Len(t, turns.Data, 3)
	assert.Equal(t, []string{"z", "a", "z"}, []string{turns.Data[0].ID, turns.Data[1].ID, turns.Data[2].ID})
	assert.Equal(t, "9007199254740993", string(turns.Data[0].UnknownFields["future"]))
	tail, err := f.c.ListThreadTurns(ctx, types.ThreadTurnsListParams{ThreadID: "native-thread-42", Cursor: turns.NextCursor})
	require.NoError(t, err)
	assert.Empty(t, tail.Data)
	assert.True(t, tail.NextCursor.IsNull())
	reverse, err := f.c.ListThreadTurns(ctx, types.ThreadTurnsListParams{ThreadID: "native-thread-42", Cursor: turns.BackwardsCursor, SortDirection: types.Some(types.SortDirectionAsc), ItemsView: types.Some(types.TurnItemsViewFull), Limit: types.Some(uint32(101))})
	require.NoError(t, err)
	require.Len(t, reverse.Data, 1)
	assert.Equal(t, "z", reverse.Data[0].ID, "reverse turn anchor may be included again")
	require.Len(t, reverse.Data[0].Items, 1)
	assert.JSONEq(t, `{"type":"futureItem","id":"old","opaque":9007199254740993}`, string(reverse.Data[0].Items[0].Unknown))
	items, err := f.c.ListThreadItems(ctx, types.ThreadItemsListParams{ThreadID: "native-thread-42"})
	require.NoError(t, err)
	require.Len(t, items.Data, 1)
	assert.JSONEq(t, `{"type":"futureItem","id":"old","opaque":9007199254740993}`, string(items.Data[0].Item.Unknown))
	itemTail, err := f.c.ListThreadItems(ctx, types.ThreadItemsListParams{ThreadID: "native-thread-42", Cursor: types.Some(types.ThreadItemsListCursor{Value: types.Ptr(items.NextCursor.ValueOr(""))})})
	require.NoError(t, err)
	assert.Empty(t, itemTail.Data)
	assert.True(t, itemTail.NextCursor.IsNull())
	back, err := f.c.ListThreadItems(ctx, types.ThreadItemsListParams{ThreadID: "native-thread-42", SortDirection: types.Some(types.SortDirectionDesc), Cursor: types.Some(types.ThreadItemsListCursor{Value: types.Ptr(items.BackwardsCursor.ValueOr(""))})})
	require.NoError(t, err)
	assert.True(t, back.NextCursor.IsNull())
	anchor := types.Some(types.ThreadItemsListCursor{ItemThreadItemsListAnchor: &types.ItemThreadItemsListAnchor{Type: types.ItemThreadItemsListAnchorTypeItem, ItemID: "old"}})
	_, err = f.c.ListThreadItems(ctx, types.ThreadItemsListParams{ThreadID: "native-thread-42", Cursor: anchor})
	checkQueryRPCError(t, err, -32602, "turnId is required when cursor is an item anchor")
	_, err = f.c.ListThreadItems(ctx, types.ThreadItemsListParams{ThreadID: "native-thread-42", TurnID: types.Some(""), Cursor: anchor})
	checkQueryRPCError(t, err, -32602, "turnId is required when cursor is an item anchor")
	after, err := f.c.ListThreadItems(ctx, types.ThreadItemsListParams{ThreadID: "native-thread-42", TurnID: types.Some("z"), Cursor: anchor, Limit: types.Some(uint32(1))})
	require.NoError(t, err)
	require.Len(t, after.Data, 1)
	require.NotNil(t, after.Data[0].Item.AgentMessage)
	assert.Equal(t, "after", after.Data[0].Item.AgentMessage.ID, "item anchor is exclusive")
	for _, method := range []string{"turns", "items"} {
		if method == "turns" {
			_, err = f.c.ListThreadTurns(ctx, types.ThreadTurnsListParams{ThreadID: "native-thread-42", Cursor: types.Some("bad")})
		} else {
			_, err = f.c.ListThreadItems(ctx, types.ThreadItemsListParams{ThreadID: "native-thread-42", Cursor: types.Some(types.ThreadItemsListCursor{Value: types.Ptr("bad")})})
		}
		checkQueryRPCError(t, err, -32600, "invalid cursor")
	}
	_, err = f.c.ListThreadItems(ctx, types.ThreadItemsListParams{ThreadID: "legacy"})
	checkQueryRPCError(t, err, -32601, "thread/items/list is not supported yet")
	require.NotEmpty(t, reverse.Data[0].Items[0].Unknown)
	reverse.Data[0].Items[0].Unknown[0] = 'x'
	assert.JSONEq(t, `{"type":"futureItem","id":"old","opaque":9007199254740993}`, string(items.Data[0].Item.Unknown))
	frames := queryTrace(t, f, 0)
	require.Len(t, frames, 12)
	assert.JSONEq(t, `{"threadId":"native-thread-42"}`, string(frames[0].Params))
	assert.JSONEq(t, `{"threadId":"native-thread-42","cursor":"turn/back","sortDirection":"asc","itemsView":"full","limit":101}`, string(frames[2].Params))
	assert.JSONEq(t, `{"threadId":"native-thread-42"}`, string(frames[3].Params))
	assert.JSONEq(t, `{"threadId":"native-thread-42","cursor":"item/back","sortDirection":"desc"}`, string(frames[5].Params))
	assert.JSONEq(t, `{"threadId":"native-thread-42","turnId":"z","cursor":{"type":"item","itemId":"old"},"limit":1}`, string(frames[8].Params))
}

func testQueryPreCanceled(t *testing.T) {
	f := startFake(t, "query-pages", codex.Limits{})
	ctx, cancel := context.WithCancel(queryContext(t))
	cancel()
	_, listErr := f.c.ListThreads(ctx, types.ThreadListParams{})
	_, readErr := f.c.ReadThread(ctx, types.ThreadReadParams{ThreadID: "native-thread-42"})
	_, turnsErr := f.c.ListThreadTurns(ctx, types.ThreadTurnsListParams{ThreadID: "native-thread-42"})
	_, itemsErr := f.c.ListThreadItems(ctx, types.ThreadItemsListParams{ThreadID: "native-thread-42"})
	for _, err := range []error{listErr, readErr, turnsErr, itemsErr} {
		require.ErrorIs(t, err, context.Canceled)
		var call *codex.CallError
		require.ErrorAs(t, err, &call)
		assert.Equal(t, codex.NotSent, call.Dispatch)
	}
	require.Empty(t, queryTrace(t, f, 0))
}

func testQueryWithSession(t *testing.T) {
	f := startFake(t, "query-pages", codex.Limits{})
	ctx := queryContext(t)
	s, err := codex.Open(ctx, codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	_, err = f.c.ReadThread(ctx, types.ThreadReadParams{ThreadID: s.ThreadID(), IncludeTurns: types.Some(false)})
	require.NoError(t, err)
	_, err = f.c.ListThreads(ctx, types.ThreadListParams{})
	require.NoError(t, err)
	_, err = f.c.ListThreadTurns(ctx, types.ThreadTurnsListParams{ThreadID: s.ThreadID()})
	require.NoError(t, err)
	_, err = f.c.ListThreadItems(ctx, types.ThreadItemsListParams{ThreadID: s.ThreadID()})
	require.NoError(t, err)
	checkLive := func(want string) {
		t.Helper()
		received, receiveErr := s.Receive(ctx)
		require.NoError(t, receiveErr)
		require.NotNil(t, received.Event)
		assert.Equal(t, "item/agentMessage/delta", received.Event.Method)
		native, ok := received.Event.Data.(*types.AgentMessageDeltaNotification)
		require.True(t, ok, "native delta must remain typed")
		assert.Equal(t, want, native.Delta)
		assert.Equal(t, s.ThreadID(), native.ThreadID)
		assert.Equal(t, "live-turn", native.TurnID)
		assert.Equal(t, "live-item", native.ItemID)
		require.Nil(t, received.Event.History)
		require.Nil(t, received.Event.DecodeError)
		require.Nil(t, received.Request)
		require.Nil(t, received.Observation)
	}
	// The held read emits an explicit peer barrier, then keeps the query pending.
	pending, cancel := context.WithCancel(ctx)
	finished := make(chan error, 1)
	go func() { _, e := f.c.ReadThread(pending, types.ThreadReadParams{ThreadID: "wait"}); finished <- e }()
	checkLive("during query")
	r, err := s.Receive(ctx)
	require.NoError(t, err)
	require.NotNil(t, r.Event)
	assert.Equal(t, "fixture/query-held", r.Event.Method, "history must not be projected before the barrier")
	require.Nil(t, r.Request)
	cancel()
	select {
	case err = <-finished:
		require.ErrorIs(t, err, context.Canceled)
		var call *codex.CallError
		require.ErrorAs(t, err, &call)
		assert.Equal(t, codex.OutcomeUnknown, call.Dispatch)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Release the canceled response before another typed delta and marker.
	// Only native live notifications enter Receive, in peer order.
	require.NoError(t, f.c.Call(ctx, "query/release", nil, nil))
	checkLive("after query")
	r, err = s.Receive(ctx)
	require.NoError(t, err)
	require.NotNil(t, r.Event)
	assert.Equal(t, "fixture/query-end", r.Event.Method)
	require.Nil(t, r.Request)
	require.NoError(t, s.Close())
	_, err = s.Receive(ctx)
	require.ErrorIs(t, err, io.EOF)
	queryTrace(t, f, 1)
}

func checkQueryRPCError(t *testing.T, err error, code int64, message string) {
	t.Helper()
	var rpc *codex.RPCError
	require.ErrorAs(t, err, &rpc)
	assert.Equal(t, code, rpc.Code)
	assert.Equal(t, message, rpc.Message)
	assert.JSONEq(t, `{"detail":9007199254740993}`, string(rpc.Data))
	assert.Contains(t, string(rpc.Raw), `"future":null`)
	var call *codex.CallError
	require.ErrorAs(t, err, &call)
	assert.Equal(t, codex.Responded, call.Dispatch)
}

func queryTrace(t *testing.T, f fixture, starts int) []fakeEnvelope {
	expectedUnsubscribes := starts
	unsubscribes := 0
	t.Helper()
	// Response is an explicit barrier after every earlier request was traced.
	require.NoError(t, f.c.Call(queryContext(t), "echo", json.RawMessage(`{}`), nil))
	var queries []fakeEnvelope
	for _, frame := range trace(t, f.trace) {
		t.Logf("query trace: %s %s", frame.Method, frame.Params)
		switch frame.Method {
		case "initialize", "initialized", "echo", "query/release":
		case "thread/unsubscribe":
			unsubscribes++
		case "thread/start":
			starts--
		case "thread/list", "thread/read", "thread/turns/list", "thread/items/list":
			queries = append(queries, frame)
		default:
			t.Errorf("unexpected operation/answer during query: %s", frame.Method)
		}
	}
	assert.Zero(t, starts, "unexpected start count")
	assert.Equal(t, expectedUnsubscribes, unsubscribes, "only explicit Session close may unsubscribe")
	return queries
}

// Independent list -> choose child -> read content must never create a Session.
func TestIndependentSavedChildQueries(t *testing.T) {
	f := startFake(t, "query-children", codex.Limits{})
	ctx := queryContext(t)
	page, err := f.c.ListThreads(ctx, types.ThreadListParams{ParentThreadID: types.Some("saved-root")})
	require.NoError(t, err)
	require.Len(t, page.Data, 1)
	require.Equal(t, "saved-child", page.Data[0].ID)
	encoded, err := json.Marshal(page.Data[0])
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"parentThreadId":"saved-root"`)
	content, err := f.c.ReadThread(ctx, types.ThreadReadParams{ThreadID: page.Data[0].ID, IncludeTurns: types.Some(true)})
	require.NoError(t, err)
	require.Equal(t, "saved-child", content.Thread.ID)
	require.Len(t, content.Thread.Turns, 1)
	require.Len(t, content.Thread.Turns[0].Items, 1)
	require.Equal(t, "SYNTHETIC CHILD CONTENT", content.Thread.Turns[0].Items[0].AgentMessage.Text)
	frames := queryTrace(t, f, 0)
	require.Len(t, frames, 2)
	require.JSONEq(t, `{"parentThreadId":"saved-root"}`, string(frames[0].Params))
	require.JSONEq(t, `{"threadId":"saved-child","includeTurns":true}`, string(frames[1].Params))
}

// Owned by the subprocess's single-threaded peer, never shared across clients.
var queryHeldID json.RawMessage

func handleHistoryQueryPeer(mode string, m fakeEnvelope, send func(json.RawMessage, string)) bool {
	if mode == "query-children" {
		child := strings.ReplaceAll(queryThread, "native-thread-42", "saved-child")
		child = strings.Replace(child, `"turns":[]`, `"parentThreadId":"saved-root","turns":[{"id":"child-turn","status":"completed","items":[{"type":"agentMessage","id":"child-item","text":"SYNTHETIC CHILD CONTENT"}]}]`, 1)
		switch m.Method {
		case "thread/list":
			send(m.ID, `{"data":[`+child+`],"nextCursor":null}`)
		case "thread/read":
			send(m.ID, `{"thread":`+child+`}`)
		default:
			return false
		}
		return true
	}
	if mode != "query-pages" {
		return false
	}
	var p map[string]json.RawMessage
	_ = json.Unmarshal(m.Params, &p)
	var cursor, thread string
	_ = json.Unmarshal(p["cursor"], &cursor)
	_ = json.Unmarshal(p["threadId"], &thread)
	reject := func(code int, message string) {
		fmt.Fprintf(os.Stdout, `{"id":%s,"error":{"code":%d,"message":%q,"data":{"detail":9007199254740993},"future":null}}`+"\n", m.ID, code, message)
	}
	if strings.HasPrefix(m.Method, "thread/") && cursor == "bad" {
		reject(-32600, "invalid cursor")
		return true
	}
	switch m.Method {
	case "thread/list":
		if len(p["originators"]) > 0 {
			reject(-32602, "originator filtering is not supported by the local app-server")
		} else if cursor != "" {
			send(m.ID, `{"data":[],"nextCursor":null}`)
		} else {
			send(m.ID, `{"data":[`+queryThread+`],"nextCursor":"opaque/+:next","backwardsCursor":"opaque/back","readStates":{"native-thread-42":{"unread":true}},"future":null}`)
		}
	case "thread/read":
		if thread == "wait" {
			queryHeldID = append(json.RawMessage(nil), m.ID...)
			fmt.Fprintln(os.Stdout, `{"method":"item/agentMessage/delta","params":{"threadId":"native-thread-42","turnId":"live-turn","itemId":"live-item","delta":"during query"}}`)
			fmt.Fprintln(os.Stdout, `{"method":"fixture/query-held","params":{"threadId":"native-thread-42"}}`)
		} else {
			send(m.ID, `{"thread":`+queryThread+`,"readState":{"unread":true}}`)
		}
	case "thread/turns/list":
		switch cursor {
		case "turn/next":
			send(m.ID, `{"data":[],"nextCursor":null}`)
		case "turn/back":
			send(m.ID, `{"data":[{"id":"z","status":"inProgress","itemsView":"full","items":[{"type":"futureItem","id":"old","opaque":9007199254740993}],"error":null}],"nextCursor":null}`)
		default:
			send(m.ID, `{"data":[`+queryTurn+`,{"id":"a","status":"completed","items":[],"error":null},`+queryTurn+`],"nextCursor":"turn/next","backwardsCursor":"turn/back"}`)
		}
	case "thread/items/list":
		var anchor struct{ Type, ItemID string }
		_ = json.Unmarshal(p["cursor"], &anchor)
		var turn string
		_ = json.Unmarshal(p["turnId"], &turn)
		switch {
		case thread == "legacy":
			reject(-32601, "thread/items/list is not supported yet")
		case anchor.Type == "item" && turn == "":
			reject(-32602, "turnId is required when cursor is an item anchor")
		case anchor.Type == "item":
			send(m.ID, `{"data":[{"turnId":"z","item":{"type":"agentMessage","id":"after","text":"next item"}}],"nextCursor":null}`)
		case cursor != "":
			send(m.ID, `{"data":[],"nextCursor":null}`)
		default:
			send(m.ID, `{"data":[{"turnId":"z","item":{"type":"futureItem","id":"old","opaque":9007199254740993}}],"nextCursor":"item/next","backwardsCursor":"item/back"}`)
		}
	case "query/release":
		send(queryHeldID, `{"thread":`+queryThread+`,"readState":{"unread":true}}`)
		fmt.Fprintln(os.Stdout, `{"method":"item/agentMessage/delta","params":{"threadId":"native-thread-42","turnId":"live-turn","itemId":"live-item","delta":"after query"}}`)
		fmt.Fprintln(os.Stdout, `{"method":"fixture/query-end","params":{"threadId":"native-thread-42"}}`)
		send(m.ID, `{}`)
	default:
		return false
	}
	return true
}
