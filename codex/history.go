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
// Paths use the server's native absolute-path syntax.
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

// ListThreadTurns returns one native page without resuming or projecting events.
// Direction, item detail, limits and opaque cursors are interpreted by the server.
func (c *Client) ListThreadTurns(ctx context.Context, p types.ThreadTurnsListParams) (types.ThreadTurnsListResponse, error) {
	var r types.ThreadTurnsListResponse
	err := c.Call(ctx, "thread/turns/list", p, &r)
	return r, err
}

// ListThreadItems returns one native saved-item page, not an event log.
// An item anchor requires a non-empty TurnID under the native server contract.
func (c *Client) ListThreadItems(ctx context.Context, p types.ThreadItemsListParams) (types.ThreadItemsListResponse, error) {
	var r types.ThreadItemsListResponse
	err := c.Call(ctx, "thread/items/list", p, &r)
	return r, err
}

func (c *Client) historyThreadOwned(ctx context.Context, o HistoryOptions, owner *Session) (types.Thread, json.RawMessage, error) {
	var raw json.RawMessage
	err := c.historyCall(ctx, "thread/read", types.ThreadReadParams{ThreadID: o.ThreadID, IncludeTurns: types.Some(false)}, &raw, owner)
	var r types.ThreadReadResponse
	if err == nil {
		err = json.Unmarshal(raw, &r)
	}
	if err != nil {
		return types.Thread{}, raw, err
	}
	t := r.Thread
	if t.ID != o.ThreadID || filepath.Clean(string(t.Cwd)) != filepath.Clean(o.CWD) {
		return t, raw, errors.New("codex history: thread/cwd identity mismatch")
	}
	if t.Ephemeral || t.Status.Active != nil || t.Status.SystemError != nil || len(t.Status.Unknown) > 0 {
		return t, raw, errors.New("codex history: thread is not stable persisted history")
	}
	return t, raw, nil
}

func (c *Client) replayHistoryOwned(ctx context.Context, o HistoryOptions, send func(types.Event) error, owner *Session, baseline *types.Thread) (err error) {
	var raw json.RawMessage
	cursor := types.Optional[string]{}
	stage := "thread/read"
	accepted := 0
	defer func() {
		if err != nil {
			err = &HistoryError{threadID: o.ThreadID, cursor: cursor, stage: stage, accepted: accepted, raw: append(json.RawMessage(nil), raw...), cause: err}
		}
	}()
	emit := func(event types.Event) error {
		if e := send(event); e != nil {
			return e
		}
		accepted++
		return nil
	}
	var first types.Thread
	first, raw, err = c.historyThreadOwned(ctx, o, owner)
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
		// Freeze the next uncovered position before checking admission limits.
		// No response exists for this page until the native call returns.
		stage = "thread/turns/list"
		cursor = p.Cursor
		raw = nil
		if err := countHistoryEntry(); err != nil {
			return err
		}
		if err := c.historyCall(ctx, "thread/turns/list", p, &raw, owner); err != nil {
			return err
		}
		var page types.ThreadTurnsListResponse
		if err := json.Unmarshal(raw, &page); err != nil {
			return fmt.Errorf("codex history: page: %w", err)
		}
		if !page.NextCursor.IsSet() {
			return errors.New("codex history: missing explicit pagination end/cursor")
		}
		wire, err := historyArray(raw, "data")
		if err != nil {
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
			items, err := historyArray(wire[i], "items")
			if err != nil {
				return err
			}
			for _, rawItem := range items {
				fields, err := objectFields(rawItem)
				if err != nil {
					return err
				}
				var item struct{ ID string }
				if err := json.Unmarshal(fields["id"], &item.ID); err != nil {
					return err
				}
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
			if err := replayTurn(o.ThreadID, wire[i], emit); err != nil {
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
	stage = "thread/read after replay"
	cursor = types.Optional[string]{}
	var last types.Thread
	last, raw, err = c.historyThreadOwned(ctx, o, owner)
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
	fields, err := objectFields(raw)
	if err != nil {
		return err
	}
	var source struct {
		ID    string
		Items []json.RawMessage
	}
	if err := json.Unmarshal(fields["id"], &source.ID); err != nil {
		return err
	}
	source.Items, err = historyArray(raw, "items")
	if err != nil {
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
		fields, err := objectFields(item)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(fields["id"], &identity.ID); err != nil {
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

// historyArray splits original records using the same exact keys as native types.
func historyArray(raw json.RawMessage, key string) ([]json.RawMessage, error) {
	fields, err := objectFields(raw)
	if err != nil {
		return nil, err
	}
	var values []json.RawMessage
	if err := json.Unmarshal(fields[key], &values); err != nil {
		return nil, err
	}
	return values, nil
}

// HistoryError retains the failed native response and replay position. Raw is
// never included in Error's text; each accessor returns detached native data.
// Accepted counts history events admitted before this failure, not consumed ones.
type HistoryError struct {
	threadID string
	stage    string
	cursor   types.Optional[string]
	accepted int
	raw      json.RawMessage
	cause    error
}

func (e *HistoryError) Error() string                  { return fmt.Sprintf("codex history (%s): %v", e.stage, e.cause) }
func (e *HistoryError) Unwrap() error                  { return e.cause }
func (e *HistoryError) ThreadID() string               { return e.threadID }
func (e *HistoryError) Stage() string                  { return e.stage }
func (e *HistoryError) Cursor() types.Optional[string] { return e.cursor }
func (e *HistoryError) Accepted() int                  { return e.accepted }
func (e *HistoryError) Raw() json.RawMessage           { return append(json.RawMessage(nil), e.raw...) }

func normalizeHistoryOptions(o *HistoryOptions) error {
	if o.ThreadID == "" || !filepath.IsAbs(o.CWD) || !filepath.IsAbs(o.CodexHome) {
		return errors.New("codex history: thread ID and absolute cwd/data home required")
	}
	if o.MaxRecords < 0 || o.MaxIndexBytes < 0 || o.PageSize > 1000 {
		return errors.New("codex history: invalid limit")
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
	return nil
}

// historyCall is shared by the retained internal history reader. Queries do not
// resume or attach the selected thread.
func (c *Client) historyCall(ctx context.Context, method string, params, result any, owner *Session) error {
	return c.callOwned(ctx, method, params, result, false, nil, owner)
}
