package codex

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/lineage"
	"github.com/zoumo/agent-sdk-go/internal/queue"
)

type treeRelation struct {
	history                     bool
	evidence                    types.SubagentEvidence
	createdEvidence             *types.SubagentEvidence
	announced, createdAnnounced bool
}
type treePending struct {
	method  string
	params  json.RawMessage
	raw     []byte
	arrived time.Time
}

// Subscription is a local recursive observation on the existing
// connection. Subscribe registers synchronously, before the caller starts
// root work. Closing it never interrupts work or sends unsubscribe/resume RPCs.
// Events closes after accepted events drain. Done and Err report termination
// independently of consumers. Abort releases an abandoned event stream.
// Requests() remains the only answer authority.
type Subscription struct {
	client         *Client
	root           string
	events         chan types.Event
	queue          queue.Queue[types.Event]
	deliveryCancel context.CancelFunc
	deliveryDone   chan struct{}
	done           chan struct{}
	cause          error
	closed         bool
	stop           func() bool
	batching       bool
	batch          []types.Event
	seeding        bool
	historyIDs     map[string]struct{}
	lineage        *lineage.Graph[string]
	children       map[string]*treeRelation
	relationOrder  []string
	options        types.SubscriptionOptions
	pending        []treePending
	pendingBytes   int
	timer          *time.Timer
	seen           map[string]struct{}
	seenOrder      []string
}

// Subscribe observes root and proven descendant notifications without
// starting or resuming any thread. It shares the connection reader, with its own
// buffers and queue. It consumes one Limits.Subscriptions slot.
// The initial ObservationState is incomplete: native auto-attach is best-effort
// at the pinned 0.162.1 source and runtime coverage is unverified. It
// cannot guarantee recovery across disconnects or missed server notifications.
// rootThreadID must be a nonempty native thread ID. Registration does not replay
// notifications received before it; obtain the root ID first, then Subscribe, then
// StartTurn. Zero options enable automatic descendant observation.
func (c *Client) Subscribe(ctx context.Context, rootThreadID string, options types.SubscriptionOptions) (*Subscription, error) {
	if ctx == nil {
		return nil, errors.New("codex: nil context")
	}
	if rootThreadID == "" || options.EventQueue < 0 || options.MaxNodes < 0 || options.MaxDepth < 0 || options.PendingEvents < 0 || options.PendingBytes < 0 || options.PendingAge < 0 || options.DedupEntries < 0 {
		return nil, errors.New("codex: invalid subscription options or root")
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
	if c.sessions[rootThreadID] != nil {
		return nil, ErrOwnershipConflict
	}
	if len(c.subscriptions) >= c.limits.Subscriptions {
		return nil, ErrLimit
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
	graph, err := lineage.New[string](lineage.Limits{MaxNodes: options.MaxNodes, MaxRelations: options.MaxNodes - 1, MaxDepth: options.MaxDepth})
	if err != nil {
		return nil, err
	}
	if err := graph.Root(rootThreadID); err != nil {
		return nil, err
	}
	t := &Subscription{client: c, root: rootThreadID, events: make(chan types.Event), done: make(chan struct{}), lineage: graph, children: make(map[string]*treeRelation), options: options, seen: make(map[string]struct{}), historyIDs: historyIDs}
	t.startDelivery()
	c.subscriptions[t] = struct{}{}
	t.stop = context.AfterFunc(ctx, func() { c.mu.Lock(); defer c.mu.Unlock(); t.finishLocked(ctx.Err()) })
	t.emitLocked(types.Event{Data: &types.ObservationState{State: "incomplete", Reason: "best-effort-auto-attach-unverified"}})
	if len(options.History) > 0 {
		t.emitLocked(types.Event{Data: &types.ObservationState{State: "incomplete", Reason: "history-recovery-gap"}})
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
func (t *Subscription) Events() <-chan types.Event { return t.events }
func (t *Subscription) Done() <-chan struct{}      { return t.done }
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
	t.lineage = nil
	t.children = nil
	t.historyIDs = nil
	t.relationOrder = nil
	close(t.done)
	_ = t.queue.Close(cause)
}

// Disconnect follows all accepted native records and preserves the connection cause.
func (t *Subscription) disconnectLocked(cause error) {
	if t.closed {
		return
	}
	t.enqueueLocked(types.Event{Data: &types.ObservationState{State: "disconnected", Reason: "permanent-gap"}})
	t.finishLocked(cause)
}

func (t *Subscription) emitLocked(e types.Event) {
	if t.closed {
		return
	}
	if t.batching {
		t.batch = append(t.batch, e)
		return
	}
	t.enqueueLocked(e)
}

// Admit one notification fanout (including newly resolved pending records)
// atomically and contiguously, preserving derived/native relationships.
func (t *Subscription) commitBatchLocked() {
	batch := t.batch
	t.batch = nil
	t.batching = false
	if t.closed {
		return
	}
	items := make([]queue.Item[types.Event], len(batch))
	for i, e := range batch {
		e = cloneSubscriptionEvent(e)
		items[i] = queue.Item[types.Event]{Value: e, Bytes: int64(len(e.Raw)) + int64(len(e.Params))}
	}
	if err := t.queue.PushBatch(items); err != nil {
		t.finishLocked(err)
	}
}

func (t *Subscription) enqueueLocked(e types.Event) {
	if t.closed {
		return
	}
	e = cloneSubscriptionEvent(e)
	if err := t.queue.Push(e, int64(len(e.Raw))+int64(len(e.Params))); err != nil {
		t.finishLocked(err)
	}
}

func cloneSubscriptionEvent(e types.Event) types.Event {
	// Derived evidence points into retained relation state; copy on admission.
	switch data := e.Data.(type) {
	case *types.SubagentCreated:
		copy := *data
		copy.Evidence = cloneSubagentEvidence(data.Evidence)
		e.Data = &copy
	case *types.SubagentAssociated:
		copy := *data
		copy.Evidence = cloneSubagentEvidence(data.Evidence)
		e.Data = &copy
	case *types.ObservationState:
		copy := *data
		copy.Evidence = cloneSubagentEvidence(data.Evidence)
		copy.PreviousEvidence = cloneSubagentEvidence(data.PreviousEvidence)
		e.Data = &copy
	}
	return e
}

// startDelivery owns the channel independently of the connection reader and its
// workers. One in-flight event may sit outside the FIFO while awaiting a receiver.
func (t *Subscription) startDelivery() {
	ctx, cancel := context.WithCancel(context.Background())
	t.deliveryCancel = cancel
	t.deliveryDone = make(chan struct{})
	go func() {
		defer close(t.deliveryDone)
		defer close(t.events)
		for {
			e, err := t.queue.Pop(ctx)
			if err != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case t.events <- e:
			}
			e = types.Event{} // Release the delivered payload before waiting for more.
		}
	}()
}

// Abort ends local observation and discards unread events. Use it when abandoning
// Events instead of draining it after Close. It never interrupts provider work.
// A channel receive racing Abort may still receive the in-flight event.
func (t *Subscription) Abort() error {
	t.client.mu.Lock()
	t.finishLocked(nil)
	_ = t.queue.Abort(t.cause)
	t.deliveryCancel()
	t.client.mu.Unlock()
	<-t.deliveryDone
	return nil
}

// Extract only native identity fields; unknown methods remain routable without
// inventing typed protocol payloads. Ambiguous duplicate fields give no identity.
func treeIdentity(params json.RawMessage) types.EventIdentity {
	f, err := objectFields(params)
	if err != nil {
		return types.EventIdentity{}
	}
	var id types.EventIdentity
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
		t.deliverLocked(method, params, raw)
		return
	}
	// Unknown sources may belong to other roots. Retain only within these bounds;
	// expired/evicted records follow an unresolved-source diagnostic, never proof of membership.
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
	return t.lineage.Reaches(id, t.root)
}

func (t *Subscription) diagnostic(reason, child, parent string, evidence *types.SubagentEvidence) {
	t.emitLocked(types.Event{Data: &types.ObservationState{State: "incomplete", Reason: reason, UnresolvedThreadID: child, ParentThreadID: parent, Evidence: evidence}})
}

func (t *Subscription) relationLocked(child, parent string, evidence types.SubagentEvidence, created bool) {
	if t.closed {
		return
	}
	evidence.ParentThreadID = parent
	evidence.Provenance = "live"
	if t.seeding {
		evidence.Provenance = "history"
		evidence.Method = "" // A snapshot is not a native notification.
	}
	if child == "" || parent == "" {
		return
	}
	r := t.children[child]
	if err := t.lineage.Confirm(child, parent); err != nil {
		switch {
		case errors.Is(err, lineage.ErrCycle):
			t.diagnostic("relation-cycle", child, parent, &evidence)
		case errors.Is(err, lineage.ErrConflict):
			var previous *types.SubagentEvidence
			if r != nil {
				previous = &r.evidence
			}
			t.emitLocked(types.Event{Data: &types.ObservationState{State: "incomplete", Reason: "relation-conflict", UnresolvedThreadID: child, ParentThreadID: parent, Evidence: &evidence, PreviousEvidence: previous}})
		default:
			t.finishLocked(ErrLimit)
		}
		return
	}
	if r == nil {
		_, historical := t.historyIDs[child]
		r = &treeRelation{evidence: evidence, history: t.seeding || historical}
		t.children[child] = r
		t.relationOrder = append(t.relationOrder, child)
	}
	if created && !t.seeding && !r.history && r.createdEvidence == nil {
		e := evidence
		r.createdEvidence = &e
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
		parent, known := t.lineage.Parent(child)
		if !known {
			return
		}
		announce(parent)
		if !r.announced {
			r.announced = true
			if r.createdEvidence != nil {
				r.createdAnnounced = true
				t.emitLocked(types.Event{Data: &types.SubagentCreated{ThreadID: child, ParentThreadID: parent, Evidence: r.createdEvidence}})
			} else {
				t.emitLocked(types.Event{Data: &types.SubagentAssociated{ThreadID: child, ParentThreadID: parent, Evidence: &r.evidence}})
			}
		}
		if r.createdEvidence != nil && !r.createdAnnounced {
			r.createdAnnounced = true
			t.emitLocked(types.Event{Data: &types.SubagentCreated{ThreadID: child, ParentThreadID: parent, Evidence: r.createdEvidence}})
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
				source := types.EventIdentity{ThreadID: threadID, TurnID: turnID, ItemID: itemID}
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

func (t *Subscription) discoverLocked(native types.Event, source types.EventIdentity) {
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
		evidence := types.SubagentEvidence{Method: native.Method, Source: source}
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

func (t *Subscription) activityLocked(activity *types.SubAgentActivityThreadItem, source types.EventIdentity) {
	// At the pinned source, V2 Started follows successful spawn and initial input
	// admission. Other activities prove neither creation nor a parent relation.
	// History uses the same edge parser; relationLocked suppresses creation.
	if activity.Kind != types.SubAgentActivityKindStarted || source.ThreadID == "" || source.TurnID == "" || source.ItemID == "" || source.ItemID != activity.ID {
		return
	}
	evidence := types.SubagentEvidence{Method: "item/completed", Source: source, Activity: activity}
	t.relationLocked(activity.AgentThreadID, source.ThreadID, evidence, true)
}

func (t *Subscription) collabLocked(item *types.CollabAgentToolCallThreadItem, source types.EventIdentity) {
	if item.Status != types.CollabAgentToolCallStatusCompleted || item.SenderThreadID != source.ThreadID {
		return
	}
	for _, child := range item.ReceiverThreadIDs {
		if t.closed {
			return
		}
		evidence := types.SubagentEvidence{Method: "item/completed", Source: source, Tool: string(item.Tool), ReceiverThreadIDs: append([]string(nil), item.ReceiverThreadIDs...)}
		t.relationLocked(child, item.SenderThreadID, evidence, item.Tool == types.CollabAgentToolSpawnAgent)
	}
}

func (t *Subscription) deliverLocked(method string, params json.RawMessage, raw []byte) {
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
	native.Raw = append(json.RawMessage(nil), raw...)
	t.emitLocked(native)
}

func (t *Subscription) unresolvedLocked(method string, params json.RawMessage, raw []byte) {
	t.diagnostic("unresolved-source", treeIdentity(params).ThreadID, "", nil)
	t.deliverLocked(method, params, raw)
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
		t.deliverLocked(p.method, p.params, p.raw)
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
