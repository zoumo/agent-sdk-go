package recording

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// QueryEvidence is saved-content/metadata evidence, never a raw event log,
// execution claim or restored answer handle. Calls retain native optional states
// and unknown values. Missing continuation is not an explicit page end.
type QueryEvidence struct {
	Scope       string
	Calls       []QueryCall
	ExplicitEnd bool
}
type QueryCall struct {
	Method string
	Params json.RawMessage
	Result json.RawMessage
	Error  string
}

// QuerySavedRoot uses an independently initialized reader, at most six query
// RPCs and no Session, start, resume, subscribe or turn. Only a caller-supplied
// fresh synthetic root in an exact isolated scope may be read. It never scans
// local history/configuration; native list uses the state DB only.
func QuerySavedRoot(ctx context.Context, c *codex.Client, scope, id string) (out QueryEvidence, err error) {
	out.Scope = scope
	if c == nil || !filepath.IsAbs(scope) || filepath.Clean(scope) != scope || id == "" {
		return out, errors.New("independent client, exact synthetic cwd and root ID required")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	record := func(method string, params, result any, e error) {
		p, _ := json.Marshal(params)
		r, _ := json.Marshal(result)
		call := QueryCall{Method: method, Params: p, Result: r}
		if e != nil {
			call.Error = e.Error()
		}
		out.Calls = append(out.Calls, call)
	}
	lp := types.ThreadListParams{Cwd: types.Some(types.ThreadListCwdFilter{Value: &scope}), Limit: types.Some(uint32(1)), UseStateDbOnly: types.Some(true)}
	list, e := c.ListThreads(ctx, lp)
	record("thread/list", lp, list, e)
	if e != nil {
		return out, e
	}
	if len(list.Data) != 1 || list.Data[0].ID != id || list.Data[0].Cwd != scope {
		return out, errors.New("synthetic root missing or unexpected query scope")
	}
	rp := types.ThreadReadParams{ThreadID: id, IncludeTurns: types.Some(false)}
	read, e := c.ReadThread(ctx, rp)
	record("thread/read", rp, read, e)
	if e != nil {
		return out, e
	}
	if read.Thread.ID != id || read.Thread.Cwd != scope {
		return out, errors.New("metadata identity/scope mismatch")
	}
	tp := types.ThreadTurnsListParams{ThreadID: id, Limit: types.Some(uint32(25)), ItemsView: types.Some(types.TurnItemsViewSummary)}
	summary, e := c.ListThreadTurns(ctx, tp)
	record("thread/turns/list", tp, summary, e)
	if e != nil {
		return out, e
	}
	tp.ItemsView = types.Some(types.TurnItemsViewFull)
	full, e := c.ListThreadTurns(ctx, tp)
	record("thread/turns/list", tp, full, e)
	if e != nil {
		return out, e
	}
	ip := types.ThreadItemsListParams{ThreadID: id, Limit: types.Some(uint32(25))}
	items, e := c.ListThreadItems(ctx, ip)
	record("thread/items/list", ip, items, e)
	if e != nil {
		return out, e
	}
	if next, ok := items.NextCursor.Get(); ok {
		ip.Cursor = types.Some(types.ThreadItemsListCursor{Value: &next})
		items, e = c.ListThreadItems(ctx, ip)
		record("thread/items/list", ip, items, e)
		if e != nil {
			return out, e
		}
	}
	// complete means only these bounded page samples explicitly ended. It never
	// implies atomic snapshots or a complete native event log.
	out.ExplicitEnd = list.NextCursor.IsNull() && summary.NextCursor.IsNull() && full.NextCursor.IsNull() && items.NextCursor.IsNull()
	return out, nil
}
