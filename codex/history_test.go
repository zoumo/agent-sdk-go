package codex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func handleHistoryPeer(mode string, m fakeEnvelope, send func(json.RawMessage, string)) bool {
	if !strings.HasPrefix(mode, "history") {
		return false
	}
	switch m.Method {
	case "thread/read":
		thread := strings.ReplaceAll(threadJSON, `"ephemeral":true`, `"ephemeral":false`)
		if mode == "history-isolation" {
			var p struct {
				ThreadID string `json:"threadId"`
			}
			_ = json.Unmarshal(m.Params, &p)
			thread = strings.ReplaceAll(thread, `native-thread-42`, p.ThreadID)
		}
		if mode == "history-active" {
			thread = strings.ReplaceAll(thread, `"type":"idle"`, `"type":"active","activeFlags":[]`)
		}
		if mode == "history-missing" {
			fmt.Fprintf(os.Stdout, "{\"id\":%s,\"error\":{\"code\":-32602,\"message\":\"not found\"}}\n", m.ID)
			return true
		}
		send(m.ID, `{"thread":`+thread+`}`)
		return true
	case "thread/turns/list":
		var p map[string]any
		_ = json.Unmarshal(m.Params, &p)
		if p["sortDirection"] != "asc" || p["itemsView"] != "full" {
			send(m.ID, `{"data":[],"nextCursor":"wrong-request"}`)
			return true
		}
		if p["cursor"] == nil {
			send(m.ID, `{"data":[{"id":"t1","status":"completed","items":[{"id":"i1","type":"userMessage","content":[{"type":"text","text":"hello"}]}],"error":null}],"nextCursor":"page2"}`)
		} else {
			if err := os.WriteFile(os.Getenv("CODEX_SDK_TRACE")+".history-page2", []byte("received"), 0600); err != nil {
				os.Exit(88)
			}
			switch mode {
			case "history-blocked":
				return true
			case "history-late":
				fmt.Fprintf(os.Stdout, "{\"id\":%s,\"error\":{\"code\":-32601,\"message\":\"unsupported\"}}\n", m.ID)
				return true
			case "history-cursor":
				send(m.ID, `{"data":[],"nextCursor":"page2"}`)
				return true
			case "history-malformed":
				send(m.ID, `{"data":[{"id":"t2","items":[]}],"nextCursor":null}`)
				return true
			}
			data := `{"data":[{"id":"t2","status":"completed","items":[{"id":"i2","type":"agentMessage","text":"done"}],"error":null}],"nextCursor":null}`
			switch mode {
			case "history-duplicate":
				data = strings.ReplaceAll(data, `"t2"`, `"t1"`)
			case "history-item-duplicate":
				data = strings.ReplaceAll(data, `"i2"`, `"i1"`)
			case "history-summary":
				data = strings.ReplaceAll(data, `"status":"completed"`, `"status":"completed","itemsView":"summary"`)
			case "history-unknown":
				data = strings.ReplaceAll(data, `"type":"agentMessage","text":"done"`, `"type":"futureItem","future":true`)
			}
			send(m.ID, data)
		}
		return true
	case "thread/items/list":
		send(m.ID, `{"data":[{"turnId":"t","item":{"type":"agentMessage","id":"i","text":"x"},"startedAtMs":123}],"nextCursor":null}`)
		return true
	}
	return false
}

func TestHistorySessionsAndNativeItemPage(t *testing.T) {
	f := startFake(t, "history-isolation", codex.Limits{})
	o := codex.HistoryOptions{ThreadID: "first", CWD: "/synthetic", CodexHome: "/synthetic-home", PageSize: 1}
	slow, e := f.c.Load(context.Background(), o)
	require.NoError(t, e)
	defer slow.Close()
	o.ThreadID = "second"
	fast, e := f.c.Load(context.Background(), o)
	require.NoError(t, e)
	defer fast.Close()
	var count int
	for r := range fast.Records() {
		require.Equal(t, "second", r.ThreadID)
		count++
	}
	require.NoError(t, fast.Err())
	require.Equal(t, 2, count)
	require.NoError(t, slow.Close())
	require.Error(t, slow.Err())
	page, e := f.c.ListThreadItems(context.Background(), types.ThreadItemsListParams{ThreadID: "second", SortDirection: types.Some(types.SortDirectionAsc), Cursor: types.Some(types.ThreadItemsListCursor{Value: types.Ptr("native-cursor")})})
	require.NoError(t, e)
	require.Len(t, page.Data, 1)
	require.Equal(t, int64(123), page.Data[0].StartedAtMs.ValueOr(0))
	turns, e := f.c.ListThreadTurns(context.Background(), types.ThreadTurnsListParams{ThreadID: "second", SortDirection: types.Some(types.SortDirectionAsc), ItemsView: types.Some(types.TurnItemsViewFull)})
	require.NoError(t, e)
	require.Equal(t, "page2", turns.NextCursor.ValueOr(""))
}

func TestHistoryFailuresStayFailures(t *testing.T) {
	for _, mode := range []string{"late", "cursor", "malformed", "duplicate", "item-duplicate", "summary", "missing", "active", "unknown", "cwd", "home", "identity", "limit"} {
		t.Run(mode, func(t *testing.T) {
			f := startFake(t, "history-"+mode, codex.Limits{})
			o := codex.HistoryOptions{ThreadID: "native-thread-42", CWD: "/synthetic", CodexHome: "/synthetic-home", PageSize: 1}
			switch mode {
			case "cwd":
				o.CWD = "/wrong"
			case "home":
				o.CodexHome = "/wrong"
			case "identity":
				o.ThreadID = "foreign"
			case "limit":
				o.MaxRecords = 3
			}
			h, e := f.c.Load(context.Background(), o)
			if mode == "home" {
				require.Error(t, e)
				return
			}
			require.NoError(t, e)
			defer h.Close()
			var ids []string
			for r := range h.Records() {
				ids = append(ids, r.Turn.ID)
				if mode == "unknown" && r.Turn.ID == "t2" {
					require.NotEmpty(t, r.Turn.Items[0].Unknown)
					require.Contains(t, string(r.Raw), "futureItem")
				}
			}
			<-h.Done()
			if mode == "unknown" {
				require.NoError(t, h.Err())
				require.Len(t, ids, 2)
			} else {
				require.Error(t, h.Err())
				if mode == "late" {
					require.Equal(t, []string{"t1"}, ids)
				}
			}
		})
	}
}

func TestHistoryCloseAndCancelBlockedPagination(t *testing.T) {
	for _, mode := range []string{"slow-close", "rpc-close", "rpc-cancel", "client-close", "slow-client-close"} {
		t.Run(mode, func(t *testing.T) {
			f := startFake(t, "history-blocked", codex.Limits{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h, e := f.c.Load(ctx, codex.HistoryOptions{ThreadID: "native-thread-42", CWD: "/synthetic", CodexHome: "/synthetic-home", PageSize: 1})
			require.NoError(t, e)
			defer h.Close()
			if mode != "slow-close" && mode != "slow-client-close" {
				select {
				case r := <-h.Records():
					require.Equal(t, "t1", r.Turn.ID)
				case <-time.After(time.Second):
					t.Fatal("first event lost")
				}
				require.Eventually(t, func() bool { _, err := os.Stat(f.trace + ".history-page2"); return err == nil }, time.Second, time.Millisecond)
			}
			switch mode {
			case "rpc-cancel":
				cancel()
			case "client-close", "slow-client-close":
				require.NoError(t, f.c.Close())
			default:
				require.NoError(t, h.Close())
			}
			select {
			case <-h.Done():
			case <-time.After(time.Second):
				t.Fatal("history worker leaked")
			}
			require.Error(t, h.Err())
			if mode != "client-close" && mode != "slow-client-close" {
				var result string
				require.NoError(t, f.c.Call(context.Background(), "echo", "usable", &result))
				require.Equal(t, "usable", result)
			}
		})
	}
}

func TestHistoryPagesFullTurnsInOrder(t *testing.T) {
	f := startFake(t, "history", codex.Limits{})
	h, err := f.c.Load(context.Background(), codex.HistoryOptions{ThreadID: "native-thread-42", CWD: "/synthetic", CodexHome: "/synthetic-home", PageSize: 1})
	require.NoError(t, err)
	defer h.Close()
	var ids []string
	for r := range h.Records() {
		ids = append(ids, r.Turn.ID)
		require.True(t, json.Valid(r.Raw))
		require.Equal(t, "native-thread-42", r.ThreadID)
	}
	<-h.Done()
	require.NoError(t, h.Err())
	require.Equal(t, []string{"t1", "t2"}, ids)
	for _, r := range trace(t, f.trace) {
		require.NotContains(t, []string{"thread/resume", "turn/start", "thread/start"}, r.Method)
	}
}
