package codex

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	"github.com/zoumo/codex-sdk-go/types"
)

var connectionEpoch atomic.Uint64

// TreeOptions bounds one local observation. Zero values select defaults;
// negative values are invalid. PendingAge limits association waiting, not work.
type TreeOptions struct {
	// History contains caller-supplied native Thread snapshots (not RPC envelopes).
	// Only lineage, completed collab and V2 Started relations are imported, never native events,
	// execution state or creation. Total bytes are bounded by PendingBytes and
	// snapshot count by MaxNodes. The SDK performs no history/read/resume RPC.
	History       []json.RawMessage
	EventQueue    int
	MaxNodes      int
	MaxDepth      int
	PendingEvents int
	PendingBytes  int
	PendingAge    time.Duration
	DedupEntries  int
}

type treeRelation struct {
	parent                      string
	history                     bool
	evidence                    TreeEvidence
	createdEvidence             *TreeEvidence
	announced, createdAnnounced bool
}
type treePending struct {
	method  string
	params  json.RawMessage
	raw     []byte
	arrived time.Time
}

// Subscription is a bounded, local recursive observation on the existing
// connection. Subscribe registers synchronously, before the caller starts
// root work. Closing it never interrupts work or sends unsubscribe/resume RPCs.
// Events closes on termination; queued events remain readable. Done and Err work
// even when the event queue is full. Requests() remains the only answer authority.
type Subscription struct {
	client        *Client
	root          string
	events        chan TreeEvent
	done          chan struct{}
	cause         error
	closed        bool
	stop          func() bool
	sequence      uint64
	batching      bool
	batch         []TreeEvent
	seeding       bool
	historyIDs    map[string]struct{}
	maxNodes      int
	children      map[string]*treeRelation
	relationOrder []string
	options       TreeOptions
	pending       []treePending
	pendingBytes  int
	timer         *time.Timer
	seen          map[string]struct{}
	seenOrder     []string
}

// Subscribe observes root and proven descendant notifications without
// starting or resuming any thread. It shares the connection reader, with its own
// buffers and queue. It consumes one Limits.Subscriptions slot.
// The initial ObservationState is incomplete: native auto-attach is best-effort
// at the pinned 0.162.1 source and runtime coverage is unverified. It
// cannot guarantee recovery across disconnects or missed server notifications.
// rootThreadID must be a nonempty native thread ID. Registration does not replay
// notifications received before it; obtain the root ID first, then Subscribe, then
// StartTurn. Zero options always enable managed observation.
func (c *Client) Subscribe(ctx context.Context, rootThreadID string, options TreeOptions) (*Subscription, error) {
	if ctx == nil {
		return nil, errors.New("codex: nil context")
	}
	if rootThreadID == "" || options.EventQueue < 0 || options.MaxNodes < 0 || options.MaxDepth < 0 || options.PendingEvents < 0 || options.PendingBytes < 0 || options.PendingAge < 0 || options.DedupEntries < 0 {
		return nil, errors.New("codex: invalid tree options or root")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.cause != nil {
		return nil, c.cause
	}
	if !c.ready {
		return nil, ErrProtocol
	}
	if len(c.subscriptions) >= c.limits.Subscriptions {
		return nil, ErrLimit
	}
	if options.EventQueue == 0 {
		options.EventQueue = c.limits.EventQueue
	}
	// Leave room for the reserved disconnect record, including a queue default
	// inherited from Client limits. Reject arithmetic overflow before allocation.
	if options.EventQueue == int(^uint(0)>>1) {
		return nil, errors.New("codex: tree event capacity overflows reserved slot")
	}
	if options.MaxNodes == 0 {
		options.MaxNodes = 4096
	}
	if options.MaxDepth == 0 {
		options.MaxDepth = 32
	}
	if options.PendingEvents == 0 {
		options.PendingEvents = 128
	}
	if options.PendingBytes == 0 {
		options.PendingBytes = 4 << 20
	}
	if options.PendingAge == 0 {
		options.PendingAge = 5 * time.Second
	}
	if options.DedupEntries == 0 {
		options.DedupEntries = 8192
	}
	// Validate the complete seed before registering. It is bounded separately from
	// live pending storage, which is initially empty. Snapshots are not retained.
	if len(options.History) > options.MaxNodes {
		return nil, ErrLimit
	}
	historyIDs := make(map[string]struct{}, len(options.History))
	remaining := options.PendingBytes
	for _, snapshot := range options.History {
		if len(snapshot) > remaining {
			return nil, ErrLimit
		}
		remaining -= len(snapshot)
		fields, err := objectFields(snapshot)
		var id string
		if err != nil || json.Unmarshal(fields["id"], &id) != nil || id == "" {
			return nil, errors.New("codex: history seed requires native Thread snapshots with IDs")
		}
		historyIDs[id] = struct{}{}
	}
	t := &Subscription{client: c, root: rootThreadID, events: make(chan TreeEvent, options.EventQueue+1), done: make(chan struct{}), maxNodes: options.MaxNodes, children: make(map[string]*treeRelation), options: options, seen: make(map[string]struct{}), historyIDs: historyIDs}
	c.subscriptions[t] = struct{}{}
	t.stop = context.AfterFunc(ctx, func() { c.mu.Lock(); defer c.mu.Unlock(); t.finishLocked(ctx.Err()) })
	t.emitLocked(TreeEvent{Kind: TreeObservationState, Scope: "observation", State: "incomplete", Reason: "best-effort-auto-attach-unverified"})
	if len(options.History) > 0 {
		t.emitLocked(TreeEvent{Kind: TreeObservationState, Scope: "observation", State: "incomplete", Reason: "history-recovery-gap", Provenance: "history"})
		t.seeding = true
		for _, snapshot := range options.History {
			t.seedLocked(snapshot)
			if t.closed {
				break
			}
		}
		t.announceLocked()
		t.seeding = false
	}
	t.options.History = nil
	return t, nil
}
func (t *Subscription) Events() <-chan TreeEvent { return t.events }
func (t *Subscription) Done() <-chan struct{}    { return t.done }
func (t *Subscription) Err() error {
	t.client.mu.Lock()
	defer t.client.mu.Unlock()
	return t.cause
}

// Close ends local observation, preserving queued records and the first cause.
// It is idempotent, never waits for consumers, and does not cancel provider work.
func (t *Subscription) Close() error {
	t.client.mu.Lock()
	defer t.client.mu.Unlock()
	t.finishLocked(nil)
	return nil
}
func (t *Subscription) finishLocked(cause error) {
	if t.closed {
		return
	}
	t.closed = true
	t.cause = cause
	delete(t.client.subscriptions, t)
	if t.stop != nil {
		t.stop()
	}
	if t.timer != nil {
		t.timer.Stop()
	}
	t.pending = nil
	t.pendingBytes = 0
	t.batch = nil
	t.seen = nil
	t.seenOrder = nil
	t.children = nil
	t.historyIDs = nil
	t.relationOrder = nil
	close(t.done)
	close(t.events)
}

// One reserved slot ensures disconnect remains observable even at queue capacity.
// It never evicts accepted native records or changes the connection failure cause.
func (t *Subscription) disconnectLocked(cause error) {
	if t.closed {
		return
	}
	t.enqueueLocked(TreeEvent{Kind: TreeObservationState, Scope: "observation", State: "disconnected", Reason: "permanent-gap"})
	t.finishLocked(cause)
}

func (t *Subscription) emitLocked(e TreeEvent) {
	if t.closed {
		return
	}
	if len(t.events)+len(t.batch) >= t.options.EventQueue {
		t.finishLocked(&SubscriptionOverflowError{Capacity: t.options.EventQueue})
		return
	}
	if t.batching {
		t.batch = append(t.batch, e)
		return
	}
	t.enqueueLocked(e)
}

// Stage one bounded notification fanout (including newly resolved pending
// records) before admitting any of it. Overflow preserves earlier accepted
// records, without exposing an association whose native event was rejected.
func (t *Subscription) commitBatchLocked() {
	batch := t.batch
	t.batch = nil
	t.batching = false
	if t.closed {
		return
	}
	for _, e := range batch {
		t.enqueueLocked(e)
	}
}

func (t *Subscription) enqueueLocked(e TreeEvent) {
	if t.closed {
		return
	}
	cloneEvidence := func(e *TreeEvidence) *TreeEvidence {
		if e == nil {
			return nil
		}
		copy := *e
		copy.ReceiverThreadIDs = append([]string(nil), e.ReceiverThreadIDs...)
		if e.Activity != nil {
			activity := *e.Activity
			if e.Activity.UnknownFields != nil {
				activity.UnknownFields = make(map[string]json.RawMessage, len(e.Activity.UnknownFields))
				for key, raw := range e.Activity.UnknownFields {
					activity.UnknownFields[key] = append(json.RawMessage(nil), raw...)
				}
			}
			copy.Activity = &activity
		}
		return &copy
	}
	e.Evidence = cloneEvidence(e.Evidence)
	e.PreviousEvidence = cloneEvidence(e.PreviousEvidence)
	t.sequence++
	e.Sequence = t.sequence
	e.Provider = "codex"
	e.Epoch = t.client.epoch
	e.RootThreadID = t.root
	if e.Provenance == "" {
		e.Provenance = "live"
		if t.seeding {
			e.Provenance = "history"
		}
	}
	e.UsageScope = "unknown"
	e.Origin = "sdk-derived"
	if e.Kind == TreeNative {
		e.Origin = "native"
	}
	select {
	case t.events <- e:
	default:
		t.finishLocked(&SubscriptionOverflowError{Capacity: t.options.EventQueue})
	}
}

// Extract only native identity fields; unknown methods remain routable without
// inventing typed protocol payloads. Ambiguous duplicate fields give no identity.
func treeIdentity(params json.RawMessage) TreeIdentity {
	f, err := objectFields(params)
	if err != nil {
		return TreeIdentity{}
	}
	var id TreeIdentity
	_ = json.Unmarshal(f["threadId"], &id.ThreadID)
	_ = json.Unmarshal(f["turnId"], &id.TurnID)
	_ = json.Unmarshal(f["itemId"], &id.ItemID)
	for key, target := range map[string]*string{"thread": &id.ThreadID, "turn": &id.TurnID, "item": &id.ItemID} {
		nested, err := objectFields(f[key])
		if err != nil {
			continue
		}
		var nestedID string
		_ = json.Unmarshal(nested["id"], &nestedID)
		if *target != "" && nestedID != "" && *target != nestedID {
			*target = "" // Conflicting identities cannot support routing or deduplication.
		} else if *target == "" {
			*target = nestedID
		}
	}

	return id
}

// publishLocked only receives notifications from the existing connection.
func (t *Subscription) publishLocked(method string, params json.RawMessage, raw []byte) {
	if t.closed {
		return
	}
	t.batching = true
	defer t.commitBatchLocked()
	t.flushLocked(true)
	if t.closed {
		return
	}
	native := decodeEvent(method, params)
	source := treeIdentity(params)
	t.discoverLocked(native, source)
	t.announceLocked()
	t.flushLocked(false)
	if t.closed {
		return
	}
	if source.ThreadID == "" || t.member(source.ThreadID) {
		t.deliverLocked(method, params, raw, "")
		return
	}
	// Unknown sources may belong to other roots. Retain only within these bounds;
	// expired/evicted records explicitly carry unresolved scope, never membership.
	for len(t.pending) > 0 && (len(t.pending) >= t.options.PendingEvents || t.pendingBytes+len(raw)+len(params) > t.options.PendingBytes) {
		t.releasePendingLocked(0, true)
		if t.closed {
			return
		}
	}
	if len(raw)+len(params) > t.options.PendingBytes {
		t.unresolvedLocked(method, params, raw)
		return
	}
	t.pending = append(t.pending, treePending{method, append(json.RawMessage(nil), params...), append([]byte(nil), raw...), time.Now()})
	t.pendingBytes += len(raw) + len(params)
	t.scheduleLocked()
}

func (t *Subscription) member(id string) bool {
	for depth := 0; depth <= t.options.MaxDepth; depth++ {
		if id == t.root {
			return true
		}
		r := t.children[id]
		if r == nil {
			return false
		}
		id = r.parent
	}
	return false
}

func (t *Subscription) diagnostic(reason, child, parent string, evidence *TreeEvidence) {
	t.emitLocked(TreeEvent{Kind: TreeObservationState, Scope: "observation", State: "incomplete", Reason: reason, Source: TreeIdentity{ThreadID: child}, ParentThreadID: parent, Evidence: evidence})
}

func (t *Subscription) relationLocked(child, parent string, evidence TreeEvidence, created bool) {
	evidence.ParentThreadID = parent
	evidence.Provenance = "live"
	if t.seeding {
		evidence.Provenance = "history"
		evidence.Method = "" // A snapshot is not a native notification.
	}
	if child == "" || parent == "" {
		return
	}
	// Reject self-links and back edges, including cycles not yet rooted.
	id := parent
	for depth := 0; ; depth++ {
		if id == child {
			t.diagnostic("relation-cycle", child, parent, &evidence)
			return
		}
		r := t.children[id]
		if r == nil {
			break
		}
		if depth >= t.options.MaxDepth {
			t.finishLocked(ErrLimit)
			return
		}
		id = r.parent
	}
	if child == t.root {
		t.diagnostic("relation-conflict", child, parent, &evidence)
		return
	}
	r := t.children[child]
	if r != nil && r.parent != parent {
		t.emitLocked(TreeEvent{Kind: TreeObservationState, Scope: "observation", State: "incomplete", Reason: "relation-conflict", Source: TreeIdentity{ThreadID: child}, ParentThreadID: parent, Evidence: &evidence, PreviousEvidence: &r.evidence})
		return
	}
	if r == nil {
		if len(t.children)+1 >= t.maxNodes {
			t.finishLocked(ErrLimit)
			return
		}
		_, historical := t.historyIDs[child]
		r = &treeRelation{parent: parent, evidence: evidence, history: t.seeding || historical}
		t.children[child] = r
		t.relationOrder = append(t.relationOrder, child)
	}
	if created && !t.seeding && !r.history && r.createdEvidence == nil {
		e := evidence
		r.createdEvidence = &e
	}
	// A newly supplied ancestor must not make a stored descendant exceed depth.
	for node := range t.children {
		id = node
		for depth := 0; ; depth++ {
			if id == t.root {
				break
			}
			next := t.children[id]
			if next == nil {
				break
			}
			if depth >= t.options.MaxDepth {
				t.finishLocked(ErrLimit)
				return
			}
			id = next.parent
		}
	}
}

func (t *Subscription) announceLocked() {
	// Follow parent pointers to emit proven ancestors before descendants. Arrival
	// order among siblings is retained in pending notifications, not map order.
	var announce func(string)
	announce = func(child string) {
		if t.closed || child == t.root {
			return
		}
		r := t.children[child]
		if r == nil || !t.member(child) {
			return
		}
		announce(r.parent)
		if !r.announced {
			r.announced = true
			kind, reason, evidence := TreeSubagentAssociated, "discovered", &r.evidence
			if r.evidence.Provenance == "history" {
				reason = "history"
			}
			if r.createdEvidence != nil {
				kind, reason, evidence = TreeSubagentCreated, "successful-live-spawn", r.createdEvidence
				r.createdAnnounced = true
			}
			t.emitLocked(TreeEvent{Kind: kind, Source: TreeIdentity{ThreadID: child}, ParentThreadID: r.parent, Scope: "child", RelationKey: child, CreationConfirmed: r.createdEvidence != nil, Reason: reason, Evidence: evidence, Provenance: evidence.Provenance})
		}
		if r.createdEvidence != nil && !r.createdAnnounced {
			r.createdAnnounced = true
			t.emitLocked(TreeEvent{Kind: TreeSubagentCreated, Source: TreeIdentity{ThreadID: child}, ParentThreadID: r.parent, Scope: "child", RelationKey: child, CreationConfirmed: true, Reason: "creation-confirmed", Evidence: r.createdEvidence})
		}
	}
	// Stable insertion order also makes multi-receiver discovery deterministic.
	for _, child := range t.relationOrder {
		announce(child)
	}
}

// Seed uses the same native lineage parser, but never delivers history as live
// notifications or seeds occurrence deduplication. Missing history stays a gap.
func (t *Subscription) seedLocked(snapshot json.RawMessage) {
	params, _ := json.Marshal(map[string]json.RawMessage{"thread": snapshot})
	t.discoverLocked(decodeEvent("thread/started", params), treeIdentity(params))
	fields, _ := objectFields(snapshot)
	var threadID string
	_ = json.Unmarshal(fields["id"], &threadID)
	var turns []json.RawMessage
	_ = json.Unmarshal(fields["turns"], &turns)
	for _, turn := range turns {
		f, err := objectFields(turn)
		if err != nil {
			continue
		}
		var turnID string
		_ = json.Unmarshal(f["id"], &turnID)
		var items []json.RawMessage
		_ = json.Unmarshal(f["items"], &items)
		for _, item := range items {
			if t.closed {
				return
			}
			var decoded types.ThreadItem
			if json.Unmarshal(item, &decoded) == nil {
				f, _ := objectFields(item)
				var itemID string
				_ = json.Unmarshal(f["id"], &itemID)
				source := TreeIdentity{ThreadID: threadID, TurnID: turnID, ItemID: itemID}
				if decoded.CollabAgentToolCall != nil {
					t.collabLocked(decoded.CollabAgentToolCall, source)
				}
				if decoded.SubAgentActivity != nil {
					t.activityLocked(decoded.SubAgentActivity, source)
				}
			}
		}
	}
}

func (t *Subscription) discoverLocked(native Event, source TreeIdentity) {
	if t.closed {
		return
	}
	if native.Method == "thread/started" {
		// Read only the pinned native Thread shape. No agentPath inference; duplicate
		// JSON fields are rejected by objectFields, and Raw remains untouched.
		fields, err := objectFields(native.Params)
		if err != nil {
			return
		}
		thread, err := objectFields(fields["thread"])
		if err != nil {
			return
		}
		var child, parent string
		_ = json.Unmarshal(thread["id"], &child)
		_ = json.Unmarshal(thread["parentThreadId"], &parent)
		evidence := TreeEvidence{Method: native.Method, Source: source}
		if parent != "" {
			t.relationLocked(child, parent, evidence, false)
		}
		src, _ := objectFields(thread["source"])
		sub, _ := objectFields(src["subAgent"])
		spawn, _ := objectFields(sub["thread_spawn"])
		parent = ""
		_ = json.Unmarshal(spawn["parent_thread_id"], &parent)
		if parent != "" && !t.closed {
			t.relationLocked(child, parent, evidence, false)
		}
		return
	}
	n, ok := native.Data.(*types.ItemCompletedNotification)
	if native.DecodeError != nil || !ok {
		return
	}
	if n.Item.CollabAgentToolCall != nil {
		t.collabLocked(n.Item.CollabAgentToolCall, source)
	}
	if n.Item.SubAgentActivity != nil {
		t.activityLocked(n.Item.SubAgentActivity, source)
	}
}

func (t *Subscription) activityLocked(activity *types.SubAgentActivityThreadItem, source TreeIdentity) {
	// At the pinned source, V2 Started follows successful spawn and initial input
	// admission. Other activities prove neither creation nor a parent relation.
	// History uses the same edge parser; relationLocked suppresses creation.
	if activity.Kind != types.SubAgentActivityKindStarted || source.ThreadID == "" || source.TurnID == "" || source.ItemID == "" || source.ItemID != activity.ID {
		return
	}
	evidence := TreeEvidence{Method: "item/completed", Source: source, Activity: activity}
	t.relationLocked(activity.AgentThreadID, source.ThreadID, evidence, true)
}

func (t *Subscription) collabLocked(item *types.CollabAgentToolCallThreadItem, source TreeIdentity) {
	if item.Status != types.CollabAgentToolCallStatusCompleted || item.SenderThreadID != source.ThreadID {
		return
	}
	for _, child := range item.ReceiverThreadIDs {
		if t.closed {
			return
		}
		evidence := TreeEvidence{Method: "item/completed", Source: source, Tool: string(item.Tool), ReceiverThreadIDs: append([]string(nil), item.ReceiverThreadIDs...)}
		t.relationLocked(child, item.SenderThreadID, evidence, item.Tool == types.CollabAgentToolSpawnAgent)
	}
}

func (t *Subscription) deliverLocked(method string, params json.RawMessage, raw []byte, scope string) {
	if t.closed {
		return
	}
	source := treeIdentity(params)
	native := decodeEvent(method, params)
	native.DecodeError = t.client.safeError(native.DecodeError)
	// Only a successfully decoded terminal item has a reliable occurrence identity
	// in this pinned protocol. Item IDs identify entities, never delta occurrences.
	terminal, terminalOK := native.Data.(*types.ItemCompletedNotification)
	if method == "item/completed" && terminalOK && len(terminal.Item.Unknown) == 0 && native.DecodeError == nil && source.ThreadID != "" && source.TurnID != "" && source.ItemID != "" {
		keyBytes, _ := json.Marshal([]string{source.ThreadID, source.TurnID, method, source.ItemID})
		key := string(keyBytes) // epoch is implicit in this connection-owned cache
		if _, exists := t.seen[key]; exists {
			return
		}
		if len(t.seenOrder) >= t.options.DedupEntries {
			delete(t.seen, t.seenOrder[0])
			t.seenOrder = t.seenOrder[1:]
		}
		t.seen[key] = struct{}{}
		t.seenOrder = append(t.seenOrder, key)
	}
	parent := ""
	if r := t.children[source.ThreadID]; r != nil {
		parent = r.parent
	}
	if scope == "" {
		switch {
		case source.ThreadID == "":
			scope = "unscoped"
		case source.ThreadID == t.root:
			scope = "root"
		default:
			scope = "child"
		}
	}
	t.emitLocked(TreeEvent{Kind: TreeNative, Source: source, ParentThreadID: parent, Scope: scope, Native: &native, Raw: append(json.RawMessage(nil), raw...)})
}

func (t *Subscription) unresolvedLocked(method string, params json.RawMessage, raw []byte) {
	t.diagnostic("unresolved-source", treeIdentity(params).ThreadID, "", nil)
	t.deliverLocked(method, params, raw, "unresolved")
}
func (t *Subscription) releasePendingLocked(i int, unresolved bool) {
	p := t.pending[i]
	t.pendingBytes -= len(p.raw) + len(p.params)
	copy(t.pending[i:], t.pending[i+1:])
	t.pending[len(t.pending)-1] = treePending{}
	t.pending = t.pending[:len(t.pending)-1]
	if unresolved {
		t.unresolvedLocked(p.method, p.params, p.raw)
	} else {
		t.deliverLocked(p.method, p.params, p.raw, "")
	}
}
func (t *Subscription) flushLocked(expire bool) {
	now := time.Now()
	for i := 0; i < len(t.pending) && !t.closed; {
		if t.member(treeIdentity(t.pending[i].params).ThreadID) {
			t.releasePendingLocked(i, false)
		} else if expire && now.Sub(t.pending[i].arrived) >= t.options.PendingAge {
			t.releasePendingLocked(i, true)
		} else {
			i++
		}
	}
	t.scheduleLocked()
}
func (t *Subscription) scheduleLocked() {
	// Keep one scheduled callback per observation. If its original oldest event
	// resolves early, an early wake simply schedules the next actual deadline.
	if t.closed || len(t.pending) == 0 || t.timer != nil {
		return
	}
	wait := time.Until(t.pending[0].arrived.Add(t.options.PendingAge))
	t.timer = time.AfterFunc(wait, func() {
		t.client.mu.Lock()
		defer t.client.mu.Unlock()
		t.timer = nil
		if !t.closed {
			t.batching = true
			t.flushLocked(true)
			t.commitBatchLocked()
		}
	})
}
