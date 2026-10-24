package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

// HistoryOptions selects a stored thread on this connection's native data home.
// Paths use the server's native absolute-path syntax. Load resumes this thread.
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

var ErrLoadPending = errors.New("codex: session load has not completed")

func (c *Client) beginThreadOperation(id string) (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.attachmentBusyLocked(id) {
		return nil, ErrOwnershipConflict
	}
	if s := c.sessions[id]; s != nil {
		return nil, ErrOwnershipConflict
	}

	if c.threadOperations == nil {
		c.threadOperations = make(map[string]int)
	}
	c.threadOperations[id]++
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.threadOperations[id]--
		if c.threadOperations[id] == 0 {
			delete(c.threadOperations, id)
		}
	}, nil
}

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

func (c *Client) historyThreadOwned(ctx context.Context, o HistoryOptions, owner *Session) (types.Thread, error) {
	var r types.ThreadReadResponse
	err := c.historyCall(ctx, "thread/read", types.ThreadReadParams{ThreadID: o.ThreadID, IncludeTurns: types.Some(false)}, &r, owner)
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

func (c *Client) replayHistoryOwned(ctx context.Context, o HistoryOptions, send func(types.Event) error, owner *Session, baseline *types.Thread) error {
	first, err := c.historyThreadOwned(ctx, o, owner)
	if err != nil {
		return err
	}
	if baseline != nil && (first.UpdatedAt != baseline.UpdatedAt || first.CreatedAt != baseline.CreatedAt || first.SessionID != baseline.SessionID) {
		return errors.New("codex history: thread changed during resume")
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
		if err := c.historyCall(ctx, "thread/turns/list", p, &raw, owner); err != nil {
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
			if err := replayTurn(o.ThreadID, wire.Data[i], send); err != nil {
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
	last, err := c.historyThreadOwned(ctx, o, owner)
	if err != nil {
		return err
	}
	if last.UpdatedAt != first.UpdatedAt || last.CreatedAt != first.CreatedAt || last.SessionID != first.SessionID {
		return errors.New("codex history: thread changed during replay")
	}
	return nil
}

// replayTurn projects retained snapshots without claiming live lifecycle timing.
// Each delivered event owns its typed values and original source bytes.
func replayTurn(threadID string, raw json.RawMessage, send func(types.Event) error) error {
	var source struct {
		ID    string            `json:"id"`
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return err
	}
	for _, item := range source.Items {
		n := &types.ItemCompletedNotification{ThreadID: threadID, TurnID: source.ID}
		if err := json.Unmarshal(item, &n.Item); err != nil {
			return err
		}
		var identity struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(item, &identity); err != nil {
			return err
		}
		// completedAtMs is absent: a typed zero is not a recorded timestamp.
		params, err := json.Marshal(map[string]any{"threadId": threadID, "turnId": source.ID, "item": n.Item})
		if err != nil {
			return err
		}
		if err := send(types.Event{Method: "item/completed", Params: params, Data: n, History: &types.HistorySource{ThreadID: threadID, TurnID: source.ID, ItemID: identity.ID, Raw: append(json.RawMessage(nil), item...), MissingFields: []string{"completedAtMs"}}}); err != nil {
			return err
		}
	}
	n := &types.TurnCompletedNotification{ThreadID: threadID}
	if err := json.Unmarshal(raw, &n.Turn); err != nil {
		return err
	}
	// Unknown future statuses cannot establish a terminal lifecycle event.
	switch n.Turn.Status {
	case types.TurnStatusCompleted, types.TurnStatusFailed, types.TurnStatusInterrupted:
		params, err := json.Marshal(n)
		if err != nil {
			return err
		}
		return send(types.Event{Method: "turn/completed", Params: params, Data: n, History: &types.HistorySource{ThreadID: threadID, TurnID: source.ID, Raw: append(json.RawMessage(nil), raw...)}})
	default:
		return send(types.Event{Data: &n.Turn, History: &types.HistorySource{ThreadID: threadID, TurnID: source.ID, Raw: append(json.RawMessage(nil), raw...)}})
	}
}
