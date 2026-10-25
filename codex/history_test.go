package codex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// Owned by the single-threaded subprocess protocol peer.
var historyStarts int

func handleHistoryPeer(mode string, m fakeEnvelope, send func(json.RawMessage, string)) bool {
	if mode != "history-isolation" && mode != "history-overlap" {
		return false
	}
	switch m.Method {
	case "request-frames":
		var frames []string
		_ = json.Unmarshal(m.Params, &frames)
		for _, frame := range frames {
			fmt.Fprintln(os.Stdout, frame)
		}
		send(m.ID, `{}`)
		return true
	case "thread/start":
		if mode != "history-overlap" {
			return false
		}
		historyStarts++
		id := "native-thread-42"
		if historyStarts > 1 {
			id = "child"
		}
		send(m.ID, resumeResponseJSON(id))
		return true
	case "thread/turns/list":
		var p types.ThreadTurnsListParams
		if json.Unmarshal(m.Params, &p) != nil || p.SortDirection.ValueOr("") != types.SortDirectionAsc || p.ItemsView.ValueOr("") != types.TurnItemsViewFull {
			os.Exit(89)
		}
		send(m.ID, `{"data":[{"id":"t1","status":"completed","items":[{"id":"i1","type":"userMessage","content":[{"type":"text","text":"hello"}]}],"error":null}],"nextCursor":"page2"}`)
		return true
	case "thread/items/list":
		send(m.ID, `{"data":[{"turnId":"t","item":{"type":"agentMessage","id":"i","text":"x"},"startedAtMs":123}],"nextCursor":null}`)
		return true
	}
	return false
}

func TestHistoryNativePagesWithoutResume(t *testing.T) {
	f := startFake(t, "history-isolation", codex.Limits{})
	page, err := f.c.ListThreadItems(context.Background(), types.ThreadItemsListParams{ThreadID: "second", SortDirection: types.Some(types.SortDirectionAsc), Cursor: types.Some(types.ThreadItemsListCursor{Value: types.Ptr("native-cursor")})})
	require.NoError(t, err)
	require.Len(t, page.Data, 1)
	require.Equal(t, int64(123), page.Data[0].StartedAtMs.ValueOr(0))
	turns, err := f.c.ListThreadTurns(context.Background(), types.ThreadTurnsListParams{ThreadID: "second", SortDirection: types.Some(types.SortDirectionAsc), ItemsView: types.Some(types.TurnItemsViewFull)})
	require.NoError(t, err)
	require.Equal(t, "page2", turns.NextCursor.ValueOr(""))
	for _, entry := range trace(t, f.trace) {
		require.NotEqual(t, "thread/resume", entry.Method)
		require.NotEqual(t, "thread/start", entry.Method)
	}
}
