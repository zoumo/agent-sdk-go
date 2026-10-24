// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/zoumo/agent-sdk-go/internal/lineage"
	"github.com/zoumo/agent-sdk-go/internal/queue"
)

// SessionOptions carries native owned-process options and observation bounds.
type SessionOptions Options

type ReceivedKind string

const (
	ReceivedNative      ReceivedKind = "event"
	ReceivedRequest     ReceivedKind = "request"
	ReceivedObservation ReceivedKind = "observation"
	ReceivedLoad        ReceivedKind = "load"
)

// Received retains the original native envelope. Parent knowledge is optional;
// it does not gate membership or determine the order of the live FIFO.
type Received struct {
	Kind        ReceivedKind
	Event       *Event
	Request     *IncomingRequest
	Load        *LoadState
	Observation *Observation
	Raw         json.RawMessage
	Params      json.RawMessage
	Source      SubagentIdentity
	Parent      *SubagentIdentity
	Origin      string
}
type Observation struct {
	Reason, Evidence string
	Source           SubagentIdentity
	Parent           *SubagentIdentity
}

// Submission proves only a complete local write, never CLI acceptance.
type Submission struct{ Written bool }
type InterruptOptions struct{}

// Session owns admission, native identity, observation knowledge and one FIFO.
// interaction supplies process I/O and authoritative native control correlation; it
// does not enqueue a second native stream when owned by a Session.
type Session struct {
	discovery        discoveryScope
	client           *interaction
	done             chan struct{}
	clientReady      chan struct{}
	requestedID      string
	load             LoadState
	loadCancel       context.CancelFunc
	loadDone         chan struct{}
	mu               sync.Mutex
	fifo             queue.Queue[Received]
	root             string
	nativeVersion    string
	members          map[string]SubagentIdentity
	parents          *lineage.Graph[string]
	tools            map[string]string
	limits           SubagentOptions
	seeds            []SubagentHistory
	childTools       map[string]string
	quarantine       []UnattributedRecord
	quarantineBytes  int
	expiryTimer      *time.Timer
	dropped, expired uint64
	closed           bool
	cause            error
}

// Open starts one owned native process and fixes the observed main identity
// from received evidence. The context bounds startup, not Session lifetime.
func Open(ctx context.Context, options SessionOptions) (*Session, error) {
	if ctx == nil {
		return nil, queue.ErrNilContext
	}
	o := Options(options)
	if err := o.defaults(); err != nil {
		return nil, err
	}
	s, err := newSession(o)
	if err != nil {
		return nil, err
	}
	_, err = openInteraction(ctx, o, "", s)
	if err != nil {
		_ = s.fifo.Abort(err)
		return nil, err
	}
	return s, nil
}

func newSession(o Options) (*Session, error) {
	scope := freezeDiscoveryScope(&o)
	// Reuse bounds validation, not the old tree attribution or consumer.
	limits, err := normalizedSubagentOptions(o)
	if err != nil {
		return nil, err
	}
	graph, err := lineage.New[string](lineage.Limits{MaxNodes: limits.MaxNodes + 1, MaxRelations: limits.MaxNodes, MaxDepth: limits.MaxDepth})
	if err != nil {
		return nil, err
	}
	s := &Session{done: make(chan struct{}), clientReady: make(chan struct{}), requestedID: o.ResumeID, load: LoadState{Status: LoadNotRequested}, discovery: scope, members: make(map[string]SubagentIdentity), tools: make(map[string]string), parents: graph, limits: limits, childTools: make(map[string]string), seeds: append([]SubagentHistory(nil), o.Subagents.History...)}
	_ = s.fifo.Push(s.observation("forwarding-not-confirmed", "initialize-forwardSubagentText-is-a-request", SubagentIdentity{}, nil), 0)
	return s, nil
}

// bindClient publishes the process before initialize, so pending Load can answer
// real requests and interrupt without waiting for startup or history consumption.
func (s *Session) bindClient(c *interaction) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = c
	close(s.clientReady)
	return s.closed
}

func (s *Session) Receive(ctx context.Context) (Received, error) { return s.fifo.Pop(ctx) }
func (s *Session) SessionID() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root, s.root != ""
}
func (s *Session) RequestedResumeID() string { return s.requestedID }
func (s *Session) Done() <-chan struct{}     { return s.done }
func (s *Session) Err() error                { s.mu.Lock(); defer s.mu.Unlock(); return s.cause }
func (s *Session) Submit(ctx context.Context, input Input) (Submission, error) {
	if err := s.inputAllowed(); err != nil {
		return Submission{}, dispatchFailure("submit", err)
	}
	err := s.client.sendInput(ctx, input)
	return Submission{Written: err == nil}, err
}
func (s *Session) Interrupt(ctx context.Context, _ InterruptOptions) error {
	if ctx == nil {
		return queue.ErrNilContext
	}
	select {
	case <-s.clientReady:
		s.mu.Lock()
		c := s.client
		s.mu.Unlock()
		return c.Interrupt(ctx)
	case <-s.done:
		if err := s.Err(); err != nil {
			return err
		}
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Session) Close() error {
	s.mu.Lock()
	cancel, loaded := s.loadCancel, s.loadDone
	c := s.client
	s.mu.Unlock()
	if cancel != nil {
		// Close wins before cancellation wakes startup; retain the first cause.
		if c != nil {
			_ = c.Close()
		} else {
			s.finish(nil)
		}
		cancel()
	}
	if loaded != nil {
		<-loaded
	}
	s.mu.Lock()
	c = s.client
	s.mu.Unlock()
	if c != nil {
		return c.Close()
	}
	return nil
}
func (s *Session) Abort() error { err := s.Close(); _ = s.fifo.Abort(s.Err()); return err }
func (s *Session) finish(cause error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.cause = cause
	if s.load.Status == LoadPending {
		s.load.Status = LoadFailed
		s.load.Err = cause
		if s.load.Err == nil {
			s.load.Err = ErrClosed
		}
		state := s.loadSnapshotLocked()
		_ = s.fifo.Push(Received{Kind: ReceivedLoad, Load: &state, Origin: "sdk"}, 0)
	}
	close(s.done)
	s.members = nil
	s.parents = nil
	s.tools = nil
	s.childTools = nil
	s.seeds = nil
	_ = s.fifo.Close(cause)
}
func (c *interaction) acceptEvent(e Event) error { return c.session.accept(e) }

// accept runs in the native reader. A same-session reviewed envelope is enough
// observation evidence; a complete parent path and an inferred task identity are
// deliberately unnecessary. Unknown envelope fields never establish membership.
func (s *Session) accept(e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	var observations []Received
	knownEnvelope := isTreeRootEvent(e) || e.RateLimit != nil || e.Reset != nil
	rootFrame := knownEnvelope && (e.AgentID == nil || *e.AgentID == "") && (e.ParentToolUseID == nil || *e.ParentToolUseID == "")
	if s.root == "" && rootFrame && e.SessionID != "" {
		s.root = e.SessionID
		if s.load.Status == LoadPending {
			s.load.ObservedID = e.SessionID
		}
		_ = s.parents.Root("")
		for _, seed := range s.seeds {
			if seed.SessionID != s.root {
				continue
			}
			key := "task:" + seed.TaskID
			s.members[key] = SubagentIdentity{SessionID: s.root, TaskID: seed.TaskID, AssociationID: key}
		}
		for _, seed := range s.seeds {
			if seed.SessionID != s.root || seed.ParentTaskID == "" {
				continue
			}
			key := "task:" + seed.TaskID
			r := s.confirmParent(key, "task:"+seed.ParentTaskID, s.members[key])
			r.Observation.Evidence = "caller-verified-history"
			observations = append(observations, r)
		}
		s.seeds = nil
	}
	if e.SessionID != "" && s.root != "" && e.SessionID != s.root {
		s.isolate(e, "different-session")
		return nil
	}
	owned := knownEnvelope && s.root != "" && e.SessionID == s.root
	// These records use the sole owned process connection, not an arbitrary
	// agent_id target. T05 retains native request authority in this same path.
	if e.Control != nil || e.Cancel != nil {
		owned = true
	}
	if !owned {
		s.isolate(e, "membership-unconfirmed")
		return nil
	}
	if e.System != nil && e.Subtype == "init" && rootFrame && e.SessionID == s.root {
		s.nativeVersion = e.System.ClaudeCodeVersion
		if s.nativeVersion != "2.1.258" {
			observations = append(observations, s.observation("relationship-version-unverified", "supported-native-version:2.1.258", SubagentIdentity{SessionID: s.root}, nil))
		}
	}
	source := SubagentIdentity{SessionID: e.SessionID}
	if e.AgentID != nil {
		source.AgentID = *e.AgentID
	} // unverified extension, never task equality
	key := ""
	// The fixed SDK 0.3.258 task_started local_agent + task_id + tool_use_id
	// contract is separate from newer/unknown message agent_id and parent_task_id.
	if isTreeTaskEvent(e) && e.System.TaskID != "" {
		source.TaskID = e.System.TaskID
		candidate := "task:" + source.TaskID
		if e.Subtype == "task_started" && e.System.TaskType != nil && *e.System.TaskType == "local_agent" && s.nativeVersion == "2.1.258" {
			key = candidate
			source.AssociationID = key
			if e.System.ToolUseID != nil {
				source.ToolUseID = *e.System.ToolUseID
			}
			if _, ok := s.members[key]; !ok && len(s.members) >= s.limits.MaxNodes {
				_ = s.fifo.Push(Received{Kind: ReceivedNative, Event: &e, Raw: e.Raw, Source: source, Origin: "live"}, int64(len(e.Raw)))
				return &ResourceLimitError{Resource: "session members", Limit: s.limits.MaxNodes}
			}
			if old, ok := s.members[key]; ok && old.ToolUseID != "" && source.ToolUseID != "" && old.ToolUseID != source.ToolUseID {
				observations = append(observations, s.observation("tool-reference-conflict", "native-task", source, s.parent(key)))
				source.ToolUseID = old.ToolUseID
			}
			if source.ToolUseID == "" {
				source.ToolUseID = s.members[key].ToolUseID
			}
			member := source
			member.AgentID = "" // unknown extensions never become authoritative aliases
			s.members[key] = member
			observations = append(observations, s.observation("member-confirmed", "cli-2.1.258-local-agent-task", source, s.parent(key)))
			if source.ToolUseID != "" {
				if alias, ok := s.childTools[source.ToolUseID]; ok && alias != key {
					observations = append(observations, s.observation("tool-alias-conflict", "native-task-tool-mapping", source, s.parent(key)))
				} else {
					s.childTools[source.ToolUseID] = key
				}
			}
		} else if member, ok := s.members[candidate]; ok {
			key = candidate
			source = member
		}
	}
	// Forwarded stream/message parent_tool_use_id can use an already confirmed
	// native task/tool alias. Matching an unknown agent_id string cannot do so.
	if (e.Assistant != nil || e.User != nil || e.Stream != nil) && e.ParentToolUseID != nil && *e.ParentToolUseID != "" {
		source.ToolUseID = *e.ParentToolUseID
		if alias, ok := s.childTools[source.ToolUseID]; ok {
			key = alias
			source.TaskID = s.members[key].TaskID
			source.AssociationID = key
		}
	}
	if key != "" && source.ToolUseID != "" {
		if owner, ok := s.tools[source.ToolUseID]; ok && s.childTools[source.ToolUseID] == key {
			observations = append(observations, s.confirmParent(key, owner, source))
		}
	}
	if e.AgentID != nil && *e.AgentID != "" {
		observations = append(observations, s.observation("identity-extension-unverified", "cli-2.1.258-does-not-declare-message-agent-id", source, s.parent(key)))
	}
	if e.System != nil && e.System.ParentTaskID != nil {
		observations = append(observations, s.observation("parent-extension-unverified", "cli-2.1.258-does-not-declare-parent-task-id", source, s.parent(key)))
	}
	if e.Assistant != nil && s.nativeVersion == "2.1.258" {
		// Root tool ownership needs the reviewed root envelope, or a verified task
		// alias. An unknown agent extension cannot silently become a root owner.
		ownerKnown := key != "" || rootFrame
		for _, b := range e.Assistant.Content.Blocks {
			if b.ToolUse == nil || b.ToolUse.ID == "" {
				continue
			}
			if !ownerKnown {
				observations = append(observations, s.observation("tool-owner-unconfirmed", "native-tool-use", source, nil))
				continue
			}
			id := b.ToolUse.ID
			if owner, ok := s.tools[id]; ok {
				if owner != key {
					observations = append(observations, s.observation("tool-owner-conflict", "native-tool-use", source, nil))
				}
			} else if len(s.tools) >= s.limits.MaxNodes {
				observations = append(observations, s.observation("tool-knowledge-limit", "native-tool-use", source, nil))
			} else {
				s.tools[id] = key
				if child, ok := s.childTools[id]; ok {
					observations = append(observations, s.confirmParent(child, key, s.members[child]))
				}
			}
		}
	}
	// Native first; the following bounded per-frame knowledge is not a second FIFO.
	items := []queue.Item[Received]{{Value: Received{Kind: ReceivedNative, Event: &e, Raw: e.Raw, Source: source, Parent: s.parent(key), Origin: "live"}, Bytes: int64(len(e.Raw))}}
	for _, r := range observations {
		items = append(items, queue.Item[Received]{Value: r})
	}
	return s.fifo.PushBatch(items)
}

func (s *Session) confirmParent(child, parent string, source SubagentIdentity) Received {
	var err error
	if parent == "" {
		err = s.parents.ConfirmRoot(child, "")
	} else {
		err = s.parents.Confirm(child, parent)
	}
	reason := "parent-confirmed"
	if err != nil {
		reason = "parent-knowledge-rejected: " + err.Error()
	}
	return s.observation(reason, "cli-2.1.258-task-tool-owner", source, s.parent(child))
}
func (s *Session) parent(key string) *SubagentIdentity {
	if key == "" {
		return nil
	}
	p, ok := s.parents.Parent(key)
	if !ok {
		return nil
	}
	if p == "" {
		return &SubagentIdentity{SessionID: s.root}
	}
	id, ok := s.members[p]
	if !ok {
		return &SubagentIdentity{SessionID: s.root, AssociationID: p}
	}
	return &id
}
func (s *Session) observation(reason, evidence string, source SubagentIdentity, parent *SubagentIdentity) Received {
	return Received{Kind: ReceivedObservation, Observation: &Observation{Reason: reason, Evidence: evidence, Source: source, Parent: parent}, Origin: "live"}
}

// UnattributedRecord is diagnostic material, never a live fact or answer handle.
type UnattributedRecord struct {
	Event      Event
	ReceivedAt time.Time
	Reason     string
}

// UnattributedSnapshot declares exactly the bounded retention and coverage gap.
type UnattributedSnapshot struct {
	Records              []UnattributedRecord
	Bytes                int
	Dropped, Expired     uint64
	MaxRecords, MaxBytes int
	Retention            time.Duration
	CoverageGap          bool
}

func (s *Session) expire(now time.Time) {
	for len(s.quarantine) > 0 && now.Sub(s.quarantine[0].ReceivedAt) >= s.limits.MaxUnresolvedWait {
		s.quarantineBytes -= len(s.quarantine[0].Event.Raw)
		s.quarantine[0] = UnattributedRecord{}
		s.quarantine = s.quarantine[1:]
		s.expired++
	}
}
func (s *Session) isolate(e Event, reason string) {
	now := time.Now()
	s.expire(now)
	if len(e.Raw) > s.limits.MaxUnresolvedBytes {
		s.dropped++
		return
	}
	for len(s.quarantine) >= s.limits.MaxUnresolvedEvents || s.quarantineBytes+len(e.Raw) > s.limits.MaxUnresolvedBytes {
		s.quarantineBytes -= len(s.quarantine[0].Event.Raw)
		s.quarantine[0] = UnattributedRecord{}
		s.quarantine = s.quarantine[1:]
		s.dropped++
	}
	s.quarantine = append(s.quarantine, UnattributedRecord{Event: e, ReceivedAt: now, Reason: reason})
	s.quarantineBytes += len(e.Raw)
	s.scheduleExpiry()
}
func (s *Session) Unattributed() UnattributedSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	records := append([]UnattributedRecord(nil), s.quarantine...)
	// Parse a private copy so diagnostics cannot mutate retained native evidence.
	for i := range records {
		records[i].Event, _ = parseEvent(records[i].Event.Raw)
	}
	return UnattributedSnapshot{Records: records, Bytes: s.quarantineBytes, Dropped: s.dropped, Expired: s.expired, MaxRecords: s.limits.MaxUnresolvedEvents, MaxBytes: s.limits.MaxUnresolvedBytes, Retention: s.limits.MaxUnresolvedWait, CoverageGap: len(records) > 0 || s.dropped+s.expired > 0}
}

// Retention is active even when the consumer never reads diagnostics again.
// At most one timer belongs to this bounded quarantine, not one per record.
func (s *Session) scheduleExpiry() {
	if s.expiryTimer != nil {
		s.expiryTimer.Stop()
		s.expiryTimer = nil
	}
	if len(s.quarantine) == 0 {
		return
	}
	delay := time.Until(s.quarantine[0].ReceivedAt.Add(s.limits.MaxUnresolvedWait))
	s.expiryTimer = time.AfterFunc(max(delay, 0), func() { s.mu.Lock(); defer s.mu.Unlock(); s.expire(time.Now()); s.scheduleExpiry() })
}
