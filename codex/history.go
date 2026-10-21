package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/history"
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

type History = history.Stream[types.Event]

var (
	ErrLoadPending  = errors.New("codex: session load has not completed")
	ErrLoadConflict = errors.New("codex: thread has an existing load owner")
)

// LoadedThread owns this connection's resumed-thread attachment and its replay.
// Drain Events then check Err before StartTurn. Close releases the attachment;
// it never closes the caller-owned Client or interrupts native work.
type LoadedThread struct {
	replay        *History
	client        *Client
	threadID      string
	response      types.ThreadResumeResponse
	resumed       bool
	resumeUnknown error
	releaseOnce   sync.Once
	releaseErr    error
	closeMu       sync.Mutex
	closed        bool
}

func (h *LoadedThread) Events() <-chan types.Event { return h.replay.Records() }
func (h *LoadedThread) Done() <-chan struct{}      { return h.replay.Done() }
func (h *LoadedThread) Err() error                 { return h.replay.Err() }
func (h *LoadedThread) ResumeResponse() (types.ThreadResumeResponse, error) {
	select {
	case <-h.Done():
	default:
		return types.ThreadResumeResponse{}, ErrLoadPending
	}
	if err := h.Err(); err != nil {
		return types.ThreadResumeResponse{}, err
	}
	h.closeMu.Lock()
	defer h.closeMu.Unlock()
	if h.closed {
		return types.ThreadResumeResponse{}, ErrClosed
	}
	// Return an independent native result, matching the mutable native value API.
	data, err := json.Marshal(h.response)
	if err != nil {
		return types.ThreadResumeResponse{}, err
	}
	var r types.ThreadResumeResponse
	err = json.Unmarshal(data, &r)
	return r, err
}
func (h *LoadedThread) release() error {
	h.releaseOnce.Do(func() {
		if h.resumeUnknown != nil {
			h.releaseErr = fmt.Errorf("codex load: resume outcome is unconfirmed; close the original Client to release ownership: %w", h.resumeUnknown)
			return
		}
		if !h.resumed {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), h.client.limits.ShutdownTimeout)
		defer cancel()
		r, err := h.client.UnsubscribeThread(ctx, types.ThreadUnsubscribeParams{ThreadID: h.threadID})
		if err == nil && r.Status != types.ThreadUnsubscribeStatusUnsubscribed && r.Status != types.ThreadUnsubscribeStatusNotLoaded && r.Status != types.ThreadUnsubscribeStatusNotSubscribed {
			err = errors.New("codex load: unknown unsubscribe result")
		}
		h.releaseErr = err
	})
	return h.releaseErr
}
func (h *LoadedThread) Close() error {
	h.closeMu.Lock()
	h.closed = true
	h.closeMu.Unlock()
	_ = h.replay.Close()
	err := h.release()
	if err == nil {
		h.client.mu.Lock()
		if h.client.loads[h.threadID] == h {
			delete(h.client.loads, h.threadID)
		}
		h.client.mu.Unlock()
	}
	return err
}

func (c *Client) beginThreadOperation(id string, resuming bool) (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if h := c.loads[id]; h != nil {
		if resuming {
			return nil, ErrLoadConflict
		}
		h.closeMu.Lock()
		closed := h.closed
		h.closeMu.Unlock()
		if closed {
			return nil, ErrClosed
		}
		select {
		case <-h.Done():
			if err := h.Err(); err != nil {
				return nil, err
			}
		default:
			return nil, ErrLoadPending
		}
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

// Load resumes the same native thread and asynchronously replays all retained
// full turns oldest-first. Ordinary ResumeThread does not perform this replay.
// ctx owns the replay lifetime, not only admission. Drain Events then check Err;
// Close cancels blocked delivery/pagination without closing the Client. Missing
// or unsupported sources fail through Err; no fallback creates a thread.
// Rejects active threads and observed metadata changes. Pagination is not an
// atomic snapshot: callers must quiesce external writers for a stable view.
func (c *Client) Load(ctx context.Context, o HistoryOptions) (*LoadedThread, error) {
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
	c.mu.Lock()
	if c.cause != nil {
		c.mu.Unlock()
		cancel()
		return nil, ErrClosed
	}
	if c.loads[o.ThreadID] != nil || c.threadOperations[o.ThreadID] > 0 {
		c.mu.Unlock()
		cancel()
		return nil, ErrLoadConflict
	}
	if len(c.loads) >= c.limits.PendingCalls {
		c.mu.Unlock()
		cancel()
		return nil, ErrLimit
	}
	if c.loads == nil {
		c.loads = make(map[string]*LoadedThread)
	}
	h := &LoadedThread{client: c, threadID: o.ThreadID}
	c.loads[o.ThreadID] = h
	h.replay = history.Start(replayCtx, func(ctx context.Context, send func(types.Event) error) (err error) {
		if _, err = c.historyThread(ctx, o); err != nil {
			return err
		}
		h.response, err = c.resumeThread(ctx, types.ThreadResumeParams{ThreadID: o.ThreadID, ExcludeTurns: types.Some(true)})
		if err != nil {
			var callErr *CallError
			if errors.As(err, &callErr) && callErr.Dispatch != NotSent {
				h.resumeUnknown = err
			}
			return err
		}
		h.resumed = true
		if filepath.Clean(string(h.response.Thread.Cwd)) != filepath.Clean(o.CWD) || filepath.Clean(string(h.response.Cwd)) != filepath.Clean(o.CWD) {
			return errors.New("codex load: resumed cwd mismatch")
		}
		return c.replayHistory(ctx, o, send)
	}, func(err error) error {
		if err != nil {
			return errors.Join(err, h.release())
		}
		return nil
	})
	c.mu.Unlock()
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

func (c *Client) replayHistory(ctx context.Context, o HistoryOptions, send func(types.Event) error) error {
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
	last, err := c.historyThread(ctx, o)
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
