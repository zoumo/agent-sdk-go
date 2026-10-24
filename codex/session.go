package codex

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/lineage"
	"github.com/zoumo/agent-sdk-go/internal/queue"
)

var ErrOwnershipConflict = errors.New("codex: session ownership conflict")
var ErrInvalidInput = errors.New("codex: invalid or unsupported input")

// SessionOptions selects an existing connection or an explicitly owned process.
// Exactly one of Client or Stdio must be supplied. Thread uses native start options.
type SessionOptions struct {
	Client *Client
	Stdio  *StdioOptions
	Thread types.ThreadStartParams
	// MaxMembers bounds observation identities independently of parent knowledge.
	MaxMembers int
	// MaxParentDepth bounds parent knowledge, never membership eligibility.
	MaxParentDepth int
}

type ReceivedKind string

const (
	ReceivedLoad        ReceivedKind = "load"
	ReceivedEvent       ReceivedKind = "event"
	ReceivedRequest     ReceivedKind = "request"
	ReceivedObservation ReceivedKind = "observation"
)

// Observation describes SDK knowledge, never a native notification.
type Observation struct{ ThreadID, ParentThreadID, Method, Reason string }

// Received retains one native or SDK fact with an explicit origin. Kind selects
// exactly one of Event, Request, Observation or Load.
type Received struct {
	Load        *LoadState
	Observation *Observation
	Request     *IncomingRequest
	Kind        ReceivedKind
	Event       *types.Event
	Raw         json.RawMessage
	Params      json.RawMessage
	Source      types.EventIdentity
	Origin      string
}

type Submission struct{ Response types.TurnStartResponse }
type InterruptOptions struct{ TurnID string }

// Session owns main identity, admission and a single FIFO. All mutable state
// except cleanupOnce/cleanupErr is protected by the connection mutex.
type Session struct {
	load          LoadState
	loadDone      chan struct{}
	resumed       bool
	resumeUnknown error
	client        *Client
	root          string
	owned         bool
	queue         queue.Queue[Received]
	done          <-chan struct{}
	closed        bool
	cause         error
	cleanupOnce   sync.Once
	cleanupErr    error
	cancel        context.CancelFunc
	members       map[string]struct{}
	parents       *lineage.Graph[string]
	maxMembers    int
}

func Open(ctx context.Context, options SessionOptions) (*Session, error) {
	if ctx == nil {
		return nil, errors.New("codex: nil context")
	}
	if (options.Client == nil) == (options.Stdio == nil) {
		return nil, errors.New("codex: select Client or Stdio")
	}
	if options.MaxMembers < 0 || options.MaxParentDepth < 0 {
		return nil, errors.New("codex: negative observation limit")
	}
	if options.MaxMembers == 0 {
		options.MaxMembers = 4096
	}
	if options.MaxParentDepth == 0 {
		options.MaxParentDepth = 4096
	}
	graph, err := lineage.New[string](lineage.Limits{MaxNodes: options.MaxMembers, MaxRelations: options.MaxMembers, MaxDepth: options.MaxParentDepth})
	if err != nil {
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
	c.mu.Lock()
	if c.cause != nil {
		err = c.cause
	} else if len(c.sessions)+c.sessionReservations >= c.limits.Subscriptions {
		err = ErrLimit
	} else {
		c.sessionReservations++
	}
	c.mu.Unlock()
	if err != nil {
		if owned {
			_ = c.Close()
		}
		return nil, err
	}
	defer func() { c.mu.Lock(); c.sessionReservations--; c.mu.Unlock() }()
	lifetime, cancel := context.WithCancel(context.Background())
	s := &Session{client: c, owned: owned, done: lifetime.Done(), cancel: cancel, parents: graph, members: make(map[string]struct{}), maxMembers: options.MaxMembers}
	var response types.ThreadStartResponse
	err = c.callAccept(ctx, "thread/start", options.Thread, &response, false, func(raw json.RawMessage) error {
		var r types.ThreadStartResponse
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		if r.Thread.ID == "" {
			return ErrProtocol
		}
		if err := s.bindLocked(r.Thread.ID); err != nil {
			return err
		}
		c.attachments[r.Thread.ID].managed = true
		return nil
	})
	if err != nil {
		cancel()
		if owned {
			_ = c.Close()
		}
		return nil, err
	}
	go func() { <-s.done; _ = s.Close() }()
	return s, nil
}

// bindLocked is also the registration seam for explicit Load. The reader binds
// the identity before processing the next wire record, not after the RPC wakes.
func (s *Session) bindLocked(id string) error {
	c := s.client
	if c.threadOperations[id] > 0 || c.sessions[id] != nil {
		return ErrOwnershipConflict
	}
	for sub := range c.subscriptions {
		if sub.root == id {
			return ErrOwnershipConflict
		}
	}
	if len(c.sessions) >= c.limits.Subscriptions {
		return ErrLimit
	}
	if c.sessions == nil {
		c.sessions = make(map[string]*Session)
	}
	if err := s.parents.Root(id); err != nil {
		return err
	}
	if !s.retainAttachmentLocked(id) {
		return ErrOwnershipConflict
	}
	s.members[id] = struct{}{}
	s.root = id
	c.sessions[id] = s
	return nil
}
func (s *Session) ThreadID() string                              { return s.root }
func (s *Session) Receive(ctx context.Context) (Received, error) { return s.queue.Pop(ctx) }
func (s *Session) Done() <-chan struct{}                         { return s.done }
func (s *Session) Err() error                                    { s.client.mu.Lock(); defer s.client.mu.Unlock(); return s.cause }

func (s *Session) Submit(ctx context.Context, input Input) (Submission, error) {
	for _, part := range input.Input {
		if len(part.Unknown) > 0 {
			return Submission{}, ErrInvalidInput
		}
	}
	p := input.native(s.root)
	var response types.TurnStartResponse
	err := s.client.callOwned(ctx, "turn/start", p, &response, false, nil, s)
	return Submission{Response: response}, err
}
func (s *Session) Interrupt(ctx context.Context, options InterruptOptions) error {
	if options.TurnID == "" {
		return errors.New("codex: explicit turn ID required")
	}
	var response types.TurnInterruptResponse
	err := s.client.callOwned(ctx, "turn/interrupt", types.TurnInterruptParams{ThreadID: s.root, TurnID: options.TurnID}, &response, false, nil, s)
	return err
}
func (s *Session) finishLocked(cause error) {
	if s.closed {
		return
	}
	if s.load.Status == LoadPending {
		reason := cause
		if reason == nil {
			reason = ErrClosed
		}
		s.load.Status = LoadFailed
		s.load.Err = reason
		state := s.loadSnapshotLocked()
		_ = s.queue.Push(Received{Kind: ReceivedLoad, Load: &state, Origin: "sdk"}, 0)
	}
	s.closed = true
	s.cause = cause
	// End this owner's wait without canceling an already-dispatched write.
	// The connection writer retains its own deadline and genuine fault handling.
	for _, p := range s.client.pending {
		if p.owner == s {
			dispatch := NotSent
			if p.started {
				dispatch = OutcomeUnknown
			}
			reason := cause
			if reason == nil {
				reason = ErrClosed
			}
			s.client.finishLocked(p, nil, &CallError{Method: p.method, ID: p.id, Dispatch: dispatch, Cause: reason})
		}
	}
	for _, r := range s.client.reverse {
		if r.owner == s {
			reason := cause
			if reason == nil {
				reason = ErrClosed
			}
			r.invalidateLocked(reason)
		}
	}
	// Terminal identity protection needs no reference to a closed owner/FIFO.
	for _, r := range s.client.requestHistory {
		if r.owner == s {
			r.owner = nil
		}
	}
	s.cancel()
	_ = s.queue.Close(cause)
	s.members = nil
	s.parents = nil
}

// Close stops admission before cleanup, preserves accepted facts, and never
// waits for a consumer. Shared physical attachments detach only after their
// last Session observer releases them.
func (s *Session) Close() error {
	s.cleanupOnce.Do(func() {
		c := s.client
		c.mu.Lock()
		s.finishLocked(nil)
		c.mu.Unlock()
		if s.loadDone != nil {
			<-s.loadDone
		}
		if s.owned {
			s.cleanupErr = c.Close()
			_ = s.releaseAttachments()
		} else {
			s.cleanupErr = s.releaseAttachments()
		}
		c.mu.Lock()
		if c.sessions[s.root] == s && (s.cleanupErr == nil || c.cause != nil) {
			delete(c.sessions, s.root)
		}
		c.mu.Unlock()
	})
	return s.cleanupErr
}

// Abort explicitly abandons consumption, releases the unread FIFO and performs
// the same cleanup as Close. A Receive already completed may still return a fact.
func (s *Session) Abort() error {
	c := s.client
	c.mu.Lock()
	s.finishLocked(nil)
	cause := s.cause
	c.mu.Unlock()
	_ = s.queue.Abort(cause)
	return s.Close()
}

// Unattributed returns the shared connection's bounded diagnostic snapshot,
// including when Open owns and hides the underlying process connection.
func (s *Session) Unattributed() UnattributedSnapshot { return s.client.Unattributed() }
