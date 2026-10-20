package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/history"
)

// HistoryOptions selects a stored thread on this connection's native data home.
// Paths use the server's native absolute-path syntax. No resume is performed.
type HistoryOptions struct {
	ThreadID  string
	CWD       string
	CodexHome string
	// Zero selects 50 turns per page and 100000 total turns/items/pages.
	PageSize   uint32
	MaxRecords int
	// MaxIndexBytes bounds retained turn/item IDs and cursors (default 16 MiB).
	MaxIndexBytes int
}

// HistoryRecord preserves an entire native full turn, including item identities,
// unknown variants and recorded timestamps. Raw is the original received turn
// JSON, not a synthesized item/started or item/completed notification.
type HistoryRecord struct {
	ThreadID string
	Turn     types.Turn
	Raw      json.RawMessage
}
type History = history.Stream[HistoryRecord]

func (c *Client) ListThreadTurns(ctx context.Context, p types.ThreadTurnsListParams) (types.ThreadTurnsListResponse, error) {
	var r types.ThreadTurnsListResponse
	err := c.Call(ctx, "thread/turns/list", p, &r)
	return r, err
}
func (c *Client) ListThreadItems(ctx context.Context, p types.ThreadItemsListParams) (types.ThreadItemsListResponse, error) {
	var r types.ThreadItemsListResponse
	err := c.Call(ctx, "thread/items/list", p, &r)
	return r, err
}

// Load starts asynchronous, read-only, oldest-first full-turn replay.
// ctx owns the replay lifetime, not only admission. Drain Records then check Err;
// Close cancels blocked delivery/pagination without closing the Client. Missing
// or unsupported sources fail through Err; no fallback creates/resumes a thread.
// Rejects active threads and observed metadata changes. Pagination is not an
// atomic snapshot: callers must quiesce external writers for a stable view.
func (c *Client) Load(ctx context.Context, o HistoryOptions) (*History, error) {
	if ctx == nil {
		return nil, errors.New("codex history: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if o.ThreadID == "" || !filepath.IsAbs(o.CWD) || !filepath.IsAbs(o.CodexHome) {
		return nil, errors.New("codex history: thread ID and absolute cwd/data home required")
	}
	if o.MaxRecords < 0 || o.MaxIndexBytes < 0 || o.PageSize > 1000 {
		return nil, errors.New("codex history: invalid limit")
	}
	if o.MaxRecords == 0 {
		o.MaxRecords = 100000
	}
	if o.PageSize == 0 {
		o.PageSize = 50
	}
	if o.MaxIndexBytes == 0 {
		o.MaxIndexBytes = 16 << 20
	}
	if filepath.Clean(string(c.ServerInfo().CodexHome)) != filepath.Clean(o.CodexHome) {
		return nil, errors.New("codex history: data-home identity mismatch")
	}
	replayCtx, cancel := context.WithCancel(ctx)
	h := history.Start(replayCtx, func(ctx context.Context, send func(HistoryRecord) error) error { return c.replayHistory(ctx, o, send) })
	go func() {
		defer cancel()
		select {
		case <-c.Done():
			cancel()
		case <-h.Done():
		}
	}()
	return h, nil
}

func (c *Client) historyThread(ctx context.Context, o HistoryOptions) (types.Thread, error) {
	r, err := c.ReadThread(ctx, types.ThreadReadParams{ThreadID: o.ThreadID, IncludeTurns: types.Some(false)})
	if err != nil {
		return types.Thread{}, err
	}
	t := r.Thread
	if t.ID != o.ThreadID || filepath.Clean(string(t.Cwd)) != filepath.Clean(o.CWD) {
		return t, errors.New("codex history: thread/cwd identity mismatch")
	}
	if t.Ephemeral || t.Status.Active != nil || t.Status.SystemError != nil || len(t.Status.Unknown) > 0 {
		return t, errors.New("codex history: thread is not stable persisted history")
	}
	return t, nil
}

func (c *Client) replayHistory(ctx context.Context, o HistoryOptions, send func(HistoryRecord) error) error {
	first, err := c.historyThread(ctx, o)
	if err != nil {
		return err
	}
	seenTurns := make(map[string]bool)
	seenItems := make(map[string]bool)
	cursors := make(map[string]bool)
	count := 0
	countHistoryEntry := func() error {
		count++
		if count > o.MaxRecords {
			return errors.New("codex history: record/page limit exceeded")
		}
		return nil
	}
	indexBytes := 0
	retainIdentity := func(value string) error {
		if len(value) > o.MaxIndexBytes-indexBytes {
			return errors.New("codex history: identity index byte limit exceeded")
		}
		indexBytes += len(value)
		return nil
	}
	p := types.ThreadTurnsListParams{ThreadID: o.ThreadID, Limit: types.Some(o.PageSize), SortDirection: types.Some(types.SortDirectionAsc), ItemsView: types.Some(types.TurnItemsViewFull)}
	for {
		if err := countHistoryEntry(); err != nil {
			return err
		}
		var raw json.RawMessage
		if err := c.Call(ctx, "thread/turns/list", p, &raw); err != nil {
			return err
		}
		var page types.ThreadTurnsListResponse
		if err := json.Unmarshal(raw, &page); err != nil {
			return fmt.Errorf("codex history: page: %w", err)
		}
		var wire struct {
			Data []json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &wire); err != nil {
			return err
		}
		if len(page.Data) > int(o.PageSize) {
			return errors.New("codex history: server exceeded requested page size")
		}
		for i, t := range page.Data {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := countHistoryEntry(); err != nil {
				return err
			}
			if t.ID == "" || seenTurns[t.ID] {
				return errors.New("codex history: missing/repeated turn identity")
			}
			if err := retainIdentity(t.ID); err != nil {
				return err
			}
			seenTurns[t.ID] = true
			if view, ok := t.ItemsView.Get(); ok && view != types.TurnItemsViewFull {
				return errors.New("codex history: turn items are not full")
			}
			if t.Status == types.TurnStatusInProgress {
				return errors.New("codex history: turn is active")
			}
			var items struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
			}
			if err := json.Unmarshal(wire.Data[i], &items); err != nil {
				return err
			}
			for _, item := range items.Items {
				if err := countHistoryEntry(); err != nil {
					return err
				}
				if item.ID == "" || seenItems[item.ID] {
					return errors.New("codex history: missing/repeated item identity")
				}
				if err := retainIdentity(item.ID); err != nil {
					return err
				}
				seenItems[item.ID] = true
			}
			if err := send(HistoryRecord{ThreadID: o.ThreadID, Turn: t, Raw: wire.Data[i]}); err != nil {
				return err
			}
		}
		next, ok := page.NextCursor.Get()
		if !ok {
			break
		}
		if next == "" || cursors[next] {
			return errors.New("codex history: nonadvancing pagination cursor")
		}
		if err := retainIdentity(next); err != nil {
			return err
		}
		cursors[next] = true
		p.Cursor = types.Some(next)
	}
	last, err := c.historyThread(ctx, o)
	if err != nil {
		return err
	}
	if last.UpdatedAt != first.UpdatedAt || last.CreatedAt != first.CreatedAt || last.SessionID != first.SessionID {
		return errors.New("codex history: thread changed during replay")
	}
	return nil
}
