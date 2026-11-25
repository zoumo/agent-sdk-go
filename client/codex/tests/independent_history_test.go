package codex_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/codex"
	ct "github.com/zoumo/agent-sdk-go/client/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/testsupport"
)

func TestIndependentHistoryBrowser(t *testing.T) {
	t.Run("history-query/codex", codexIndependentHistory)
}

func TestIndependentHistoryCodex(t *testing.T) {
	t.Run("history-query/codex", codexIndependentHistory)
}

func codexIndependentHistory(t *testing.T) {
	ctx := testsupport.Deadline(t)
	exe, err := os.Executable()
	require.NoError(t, err)
	c, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: exe, Args: []string{}, Env: []string{"SDK_INTEGRATION_PEER=codex", "SDK_INTEGRATION=history-query", "GORACE=atexit_sleep_ms=0"}, Initialize: ct.InitializeParams{ClientInfo: ct.ClientInfo{Name: "history-scenario", Version: "synthetic"}}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	page, err := c.ListThreads(ctx, ct.ThreadListParams{})
	require.NoError(t, err)
	require.Len(t, page.Data, 1)
	id := page.Data[0].ID
	assert.Equal(t, "saved", id)
	read, err := c.ReadThread(ctx, ct.ThreadReadParams{ThreadID: id, IncludeTurns: ct.Some(false)})
	require.NoError(t, err)
	assert.Equal(t, "preview", read.Thread.Preview)
	assert.Empty(t, read.Thread.Turns)
	assert.JSONEq(t, `{"future":true}`, string(read.UnknownFields["readState"]))
	turns, err := c.ListThreadTurns(ctx, ct.ThreadTurnsListParams{ThreadID: id})
	require.NoError(t, err)
	require.Len(t, turns.Data, 1)
	assert.Equal(t, "turn", turns.Data[0].ID)
	assert.Empty(t, turns.Data[0].Items, "summary is not full content")
	tail, err := c.ListThreadTurns(ctx, ct.ThreadTurnsListParams{ThreadID: id, Cursor: turns.NextCursor})
	require.NoError(t, err)
	assert.Empty(t, tail.Data)
	assert.True(t, tail.NextCursor.IsNull())
	items, err := c.ListThreadItems(ctx, ct.ThreadItemsListParams{ThreadID: id, TurnID: ct.Some("turn")})
	require.NoError(t, err)
	require.Len(t, items.Data, 1)
	assert.JSONEq(t, `{"type":"futureItem","id":"item","n":9007199254740993}`, string(items.Data[0].Item.Unknown))
	var methods []string
	require.NoError(t, c.Call(ctx, "fixture/query-trace", nil, &methods))
	assert.Equal(t, []string{"initialize", "initialized", "thread/list", "thread/read", "thread/turns/list", "thread/turns/list", "thread/items/list", "fixture/query-trace"}, methods, "query require.NoError not start/resume/subscribe/submit/answer")
}

// Deliberately strict independent peer: no execution or Session operation exists.
func historyQueryScenarioPeer() error {
	const thread = `{"id":"saved","sessionId":"s","cliVersion":"0.162.1","createdAt":1,"updatedAt":2,"cwd":"/server","ephemeral":false,"modelProvider":"synthetic","preview":"preview","projectId":null,"source":"cli","status":{"type":"notLoaded"},"turns":[]}`
	var methods []string
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		var m struct {
			ID     json.RawMessage
			Method string
			Params struct{ Cursor string }
		}
		if err := json.Unmarshal(scan.Bytes(), &m); err != nil {
			return err
		}
		methods = append(methods, m.Method)
		var result string
		switch m.Method {
		case "initialize":
			result = `{"userAgent":"synthetic","codexHome":"/server","platformFamily":"unix","platformOs":"linux"}`
		case "initialized":
			continue
		case "thread/list":
			result = `{"data":[` + thread + `],"nextCursor":null}`
		case "thread/read":
			result = `{"thread":` + thread + `,"readState":{"future":true}}`
		case "thread/turns/list":
			if m.Params.Cursor == "" {
				result = `{"data":[{"id":"turn","items":[],"itemsView":"summary","status":"completed","error":null}],"nextCursor":"opaque"}`
			} else {
				result = `{"data":[],"nextCursor":null}`
			}
		case "thread/items/list":
			result = `{"data":[{"turnId":"turn","item":{"type":"futureItem","id":"item","n":9007199254740993}}],"nextCursor":null}`
		case "fixture/query-trace":
			raw, err := json.Marshal(methods)
			if err != nil {
				return err
			}
			result = string(raw)
		default:
			return fmt.Errorf("unexpected history operation %q", m.Method)
		}
		fmt.Fprintf(os.Stdout, `{"id":%s,"result":%s}`+"\n", m.ID, result)
	}
	return scan.Err()
}
