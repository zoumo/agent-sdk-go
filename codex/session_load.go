package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/lineage"
)

// LoadOptions selects exactly one connection or owned process and persisted main
// identity. History reading is bounded; the Session FIFO is limited by memory.
type LoadOptions struct {
	Client *Client
	Stdio  *StdioOptions
	HistoryOptions
}

type LoadStatus string

const (
	LoadNotRequested LoadStatus = "not-requested"
	LoadPending      LoadStatus = "pending"
	LoadSucceeded    LoadStatus = "succeeded"
	LoadFailed       LoadStatus = "failed"
)

// LoadState is independent of interaction termination and consumer drain.
type LoadState struct {
	// ResumeRaw retains the actual native result, including malformed/mismatched evidence.
	ResumeRaw         json.RawMessage
	Status            LoadStatus
	RequestedIdentity string
	ObservedIdentity  string
	Confirmation      string
	Err               error
}

func (s *Session) LoadState() LoadState {
	s.client.mu.Lock()
	defer s.client.mu.Unlock()
	state := s.loadSnapshotLocked()
	if state.Status == "" {
		state.Status = LoadNotRequested
	}
	return state
}

func (s *Session) loadSnapshotLocked() LoadState {
	state := s.load
	state.ResumeRaw = append(json.RawMessage(nil), state.ResumeRaw...)
	return state
}

var ErrIdentityMismatch = errors.New("codex: resumed identity mismatch")
var ErrResumeUnconfirmed = errors.New("codex: resume unconfirmed")

// ResumeError classifies failed confirmation and keeps original native evidence.
// Raw returns a copy, independently of LoadState and boundary snapshots.
type ResumeError struct {
	kind, cause error
	raw         json.RawMessage
}

func (e *ResumeError) Error() string        { return fmt.Sprintf("%v: %v", e.kind, e.cause) }
func (e *ResumeError) Unwrap() []error      { return []error{e.kind, e.cause} }
func (e *ResumeError) Raw() json.RawMessage { return append(json.RawMessage(nil), e.raw...) }

// Load explicitly resumes the selected identity, then progressively admits full
// main history to Receive alongside live facts. Active/ephemeral history is not
// supported. Pagination is non-atomic: external writers must be quiescent.
// The context owns only startup and replay, never the successful Session.
func Load(ctx context.Context, options LoadOptions) (*Session, error) {
	if ctx == nil {
		return nil, errors.New("codex load: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if (options.Client == nil) == (options.Stdio == nil) {
		return nil, errors.New("codex load: select Client or Stdio")
	}
	o := options.HistoryOptions
	if err := normalizeHistoryOptions(&o); err != nil {
		return nil, err
	}
	c := options.Client
	owned := c == nil
	if owned {
		var err error
		c, err = NewStdio(ctx, *options.Stdio)
		if err != nil {
			return nil, err
		}
	}
	fail := func(err error) (*Session, error) {
		if owned {
			_ = c.Close()
		}
		return nil, err
	}
	if filepath.Clean(string(c.ServerInfo().CodexHome)) != filepath.Clean(o.CodexHome) {
		return fail(errors.New("codex load: data-home identity mismatch"))
	}
	graph, err := lineage.New[string](lineage.Limits{MaxNodes: 4096, MaxRelations: 4096, MaxDepth: 4096})
	if err != nil {
		return fail(err)
	}
	lifetime, cancel := context.WithCancel(context.Background())
	s := &Session{client: c, owned: owned, done: lifetime.Done(), cancel: cancel, members: make(map[string]struct{}), parents: graph, maxMembers: 4096, load: LoadState{Status: LoadPending, RequestedIdentity: o.ThreadID}, loadDone: make(chan struct{})}
	c.mu.Lock()
	if c.cause != nil {
		err = c.cause
	} else if c.threadOperations[o.ThreadID] > 0 {
		err = ErrOwnershipConflict
	} else {
		err = s.bindLocked(o.ThreadID)
	}
	c.mu.Unlock()
	if err != nil {
		cancel()
		return fail(err)
	}
	go s.runLoad(o)
	go func() {
		select {
		case <-ctx.Done():
			c.mu.Lock()
			if s.load.Status == LoadPending {
				s.finishLocked(ctx.Err())
			}
			c.mu.Unlock()
		case <-s.loadDone:
		}
	}()
	go func() { <-s.done; _ = s.Close() }()
	return s, nil
}

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

// History RPC waits belong to the Session. Ending this owner never cancels an
// already started shared-connection write; its connection deadline still applies.
func (c *Client) historyCall(ctx context.Context, method string, params, result any, owner *Session) error {
	if owner != nil {
		ctx = context.Background()
	}
	return c.callOwned(ctx, method, params, result, false, nil, owner)
}
func (s *Session) runLoad(o HistoryOptions) {
	defer close(s.loadDone)
	c := s.client
	ctx := context.Background()
	before, err := c.historyThreadOwned(ctx, o, s)
	if err == nil {
		var response types.ThreadResumeResponse
		err = c.callOwned(ctx, "thread/resume", types.ThreadResumeParams{ThreadID: o.ThreadID, ExcludeTurns: types.Some(true)}, &response, false, func(raw json.RawMessage) error {
			s.resumed = true
			c.attachments[o.ThreadID].managed = true
			s.load.ResumeRaw = append(json.RawMessage(nil), raw...)
			var r types.ThreadResumeResponse
			if e := json.Unmarshal(raw, &r); e != nil {
				return &ResumeError{kind: ErrResumeUnconfirmed, cause: e, raw: append(json.RawMessage(nil), raw...)}
			}
			// A successful native reply establishes an attachment even if identity
			// validation fails. Cleanup must still release the requested attachment.
			s.load.ObservedIdentity = r.Thread.ID
			if r.Thread.ID != o.ThreadID || filepath.Clean(string(r.Thread.Cwd)) != filepath.Clean(o.CWD) || filepath.Clean(string(r.Cwd)) != filepath.Clean(o.CWD) {
				return &ResumeError{kind: ErrIdentityMismatch, cause: fmt.Errorf("requested thread %q cwd %q; received thread %q thread cwd %q response cwd %q", o.ThreadID, o.CWD, r.Thread.ID, r.Thread.Cwd, r.Cwd), raw: append(json.RawMessage(nil), raw...)}
			}
			s.load.Confirmation = "thread/resume response"
			return nil
		}, s)
		if err != nil {
			c.mu.Lock()
			var call *CallError
			if !s.resumed && errors.As(err, &call) {
				if call.Dispatch == OutcomeUnknown {
					err = &ResumeError{kind: ErrResumeUnconfirmed, cause: err}
					s.resumeUnknown = err
				}
				// A native error can occur after warm resume adds the connection.
				// A known response is releasable, but not proof of no attachment.
				if call.Dispatch == Responded {
					c.attachments[o.ThreadID].managed = true
				}
			}
			c.mu.Unlock()
		}
	}
	if err == nil {
		err = c.replayHistoryOwned(ctx, o, func(event types.Event) error {
			c.mu.Lock()
			defer c.mu.Unlock()
			if s.closed {
				return ErrClosed
			}
			s.acceptLocked(Received{Kind: ReceivedEvent, Event: &event, Params: event.Params, Source: types.EventIdentity{ThreadID: o.ThreadID, TurnID: event.History.TurnID, ItemID: event.History.ItemID}, Origin: "history"})
			return s.cause
		}, s, &before)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.closed {
		return
	}
	if err != nil {
		s.finishLocked(err)
		return
	}
	s.load.Status = LoadSucceeded
	state := s.loadSnapshotLocked()
	s.acceptLocked(Received{Kind: ReceivedLoad, Load: &state, Origin: "sdk"})
}

func (s *Session) loadGateLocked() error {
	switch s.load.Status {
	case LoadPending:
		return ErrLoadPending
	case LoadFailed:
		return errors.Join(ErrLoadFailed, s.load.Err)
	}
	return nil
}

var ErrLoadFailed = errors.New("codex: session load failed")

func (s *Session) releaseLoadError() error {
	if s.resumeUnknown != nil {
		return fmt.Errorf("codex load: resume outcome unconfirmed; close original Client: %w", s.resumeUnknown)
	}
	return nil
}
