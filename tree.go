package codex

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"

	"github.com/zoumo/codex-sdk-go/protocol"
)

var connectionEpoch atomic.Uint64

// TreeOptions bounds one local observation. Zero values use the connection's
// EventQueue and 4096 graph nodes (including the root); negatives are invalid.
// This first slice follows direct children evidenced by completed collab items.
// It has no history seed, recursive discovery, or pre-relation event replay.
type TreeOptions struct {
	EventQueue int
	MaxNodes   int
}

// TreeEventKind distinguishes native notifications from SDK records. These
// values are SDK API names, never app-server methods or protocol union variants.
type TreeEventKind string

const (
	TreeNative             TreeEventKind = "Native"
	TreeSubagentCreated    TreeEventKind = "SubagentCreated"
	TreeSubagentAssociated TreeEventKind = "SubagentAssociated"
	TreeObservationState   TreeEventKind = "ObservationState"
)

// TreeIdentity contains native identities only when present in the notification.
// A turn is not an agent entity, and an item is not renamed to a task/run ID.
type TreeIdentity struct {
	ThreadID string
	TurnID   string
	ItemID   string
}

// TreeEvidence identifies the native notification supporting a direct relation.
// ReceiverThreadIDs preserves every receiver, in native order, including repeats.
type TreeEvidence struct {
	Method            string
	Source            TreeIdentity
	Tool              string
	ReceiverThreadIDs []string
}

// TreeEvent wraps an independently owned native Event or an SDK-derived record.
// Raw is the exact notification envelope, excluding transport delimiters, only
// for Native. Origin is "native" or "sdk-derived"; Provenance is "live".
// Sequence is local delivery order, not a replay cursor. Epoch identifies this
// Client connection within this process, not a durable provider identity.
// Scope is "root", "child", "unscoped", or "observation". Unscoped events do
// not establish membership. UsageScope is "unknown"; no usage is aggregated.
// Created confirms creation only via a live successful spawnAgent completed item.
// Associated alone never confirms creation. Neither kind grants request authority.
type TreeEvent struct {
	Kind              TreeEventKind
	Origin            string
	Provider          string
	Epoch             uint64
	RootThreadID      string
	Source            TreeIdentity
	ParentThreadID    string
	Sequence          uint64
	Provenance        string
	Scope             string
	UsageScope        string
	RelationKey       string
	CreationConfirmed bool
	Evidence          *TreeEvidence
	Native            *Event
	Raw               json.RawMessage
	// State and Reason describe observation limits, never provider work success.
	State  string
	Reason string
}

type treeRelation struct{ created bool }

// TreeSubscription is a bounded, local direct-child observation on the existing
// connection. SubscribeTree registers synchronously, before the caller starts
// root work. Closing it never interrupts work or sends unsubscribe/resume RPCs.
// Events closes on termination; queued events remain readable. Done and Err work
// even when the event queue is full. Requests() remains the only answer authority.
type TreeSubscription struct {
	client   *Client
	root     string
	events   chan TreeEvent
	done     chan struct{}
	cause    error
	closed   bool
	stop     func() bool
	sequence uint64
	maxNodes int
	children map[string]treeRelation
}

// SubscribeTree observes root and proven direct-child notifications without
// starting or resuming any thread. It shares the connection reader, with its own
// buffers and queue. It consumes one Limits.Subscriptions slot.
// The initial ObservationState is incomplete: native auto-attach is best-effort
// at the pinned 0.160.1 source, runtime coverage is unverified, and this slice
// cannot recover notifications arriving before a relation or follow descendants.
func (c *Client) SubscribeTree(ctx context.Context, rootThreadID string, options TreeOptions) (*TreeSubscription, error) {
	if ctx == nil {
		return nil, errors.New("codex: nil context")
	}
	if rootThreadID == "" || options.EventQueue < 0 || options.MaxNodes < 0 {
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
	if len(c.subscriptions)+len(c.trees) >= c.limits.Subscriptions {
		return nil, ErrLimit
	}
	if options.EventQueue == 0 {
		options.EventQueue = c.limits.EventQueue
	}
	if options.MaxNodes == 0 {
		options.MaxNodes = 4096
	}
	t := &TreeSubscription{client: c, root: rootThreadID, events: make(chan TreeEvent, options.EventQueue), done: make(chan struct{}), maxNodes: options.MaxNodes, children: make(map[string]treeRelation)}
	c.trees[t] = struct{}{}
	t.stop = context.AfterFunc(ctx, func() { c.mu.Lock(); defer c.mu.Unlock(); t.finishLocked(ctx.Err()) })
	t.emitLocked(TreeEvent{Kind: TreeObservationState, Scope: "observation", State: "incomplete", Reason: "direct-children-only; pre-relation-gap; best-effort-auto-attach-unverified"})
	return t, nil
}
func (t *TreeSubscription) Events() <-chan TreeEvent { return t.events }
func (t *TreeSubscription) Done() <-chan struct{}    { return t.done }
func (t *TreeSubscription) Err() error {
	t.client.mu.Lock()
	defer t.client.mu.Unlock()
	return t.cause
}
func (t *TreeSubscription) Close() error {
	t.client.mu.Lock()
	defer t.client.mu.Unlock()
	t.finishLocked(nil)
	return nil
}
func (t *TreeSubscription) finishLocked(cause error) {
	if t.closed {
		return
	}
	t.closed = true
	t.cause = cause
	delete(t.client.trees, t)
	if t.stop != nil {
		t.stop()
	}
	t.children = nil
	close(t.done)
	close(t.events)
}
func (t *TreeSubscription) emitLocked(e TreeEvent) {
	if t.closed {
		return
	}
	t.sequence++
	e.Sequence = t.sequence
	e.Provider = "codex"
	e.Epoch = t.client.epoch
	e.RootThreadID = t.root
	e.Provenance = "live"
	e.UsageScope = "unknown"
	e.Origin = "sdk-derived"
	if e.Kind == TreeNative {
		e.Origin = "native"
	}
	select {
	case t.events <- e:
	default:
		t.finishLocked(&SubscriptionOverflowError{Capacity: cap(t.events)})
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
		if *target != "" {
			continue
		}
		nested, err := objectFields(f[key])
		if err == nil {
			_ = json.Unmarshal(nested["id"], target)
		}
	}
	return id
}
func (t *TreeSubscription) publishLocked(method string, params json.RawMessage, raw []byte) {
	source := treeIdentity(params)
	scope := "root"
	parent := ""
	if source.ThreadID == "" {
		scope = "unscoped"
	} else if source.ThreadID != t.root {
		if _, ok := t.children[source.ThreadID]; !ok {
			return
		}
		scope = "child"
		parent = t.root
	}
	native := decodeEvent(method, params)
	native.DecodeError = t.client.safeError(native.DecodeError)
	// Relation evidence must be from a successfully decoded, completed root item.
	// No path inference, activity-start inference, or server-side observation RPC.
	if source.ThreadID == t.root && method == "item/completed" && native.DecodeError == nil {
		t.discoverLocked(native, source)
	}
	t.emitLocked(TreeEvent{Kind: TreeNative, Source: source, ParentThreadID: parent, Scope: scope, Native: &native, Raw: append(json.RawMessage(nil), raw...)})
}

func (t *TreeSubscription) discoverLocked(native Event, source TreeIdentity) {
	n, ok := native.Data.(*protocol.ItemCompletedNotification)
	if !ok || n.Item.CollabAgentToolCall == nil {
		return
	}
	item := n.Item.CollabAgentToolCall
	if item.Status != protocol.CollabAgentToolCallStatusCompleted || item.SenderThreadID != t.root {
		return
	}
	for _, child := range item.ReceiverThreadIDs {
		if child == "" || child == t.root {
			continue
		}
		relation, exists := t.children[child]
		created := item.Tool == protocol.CollabAgentToolSpawnAgent
		if exists && (!created || relation.created) {
			continue
		}
		if !exists && len(t.children)+1 >= t.maxNodes {
			t.finishLocked(ErrLimit)
			return
		}
		relation.created = relation.created || created
		t.children[child] = relation
		kind := TreeSubagentAssociated
		reason := "discovered"
		if created {
			kind = TreeSubagentCreated
			reason = "successful-live-spawn"
		}
		t.emitLocked(TreeEvent{Kind: kind, Source: TreeIdentity{ThreadID: child}, ParentThreadID: t.root, Scope: "child", RelationKey: child, CreationConfirmed: relation.created, Reason: reason, Evidence: &TreeEvidence{Method: native.Method, Source: source, Tool: string(item.Tool), ReceiverThreadIDs: append([]string(nil), item.ReceiverThreadIDs...)}})
		if t.closed {
			return
		}
	}
}
