package codex

import (
	"encoding/json"

	"github.com/zoumo/codex-sdk-go/types"
)

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
// Method is empty for caller-supplied snapshots, which are not notifications.
// ReceiverThreadIDs preserves every receiver, in native order, including repeats.
type TreeEvidence struct {
	Method            string
	Provenance        string // "live" or "history" supporting evidence
	ParentThreadID    string
	Source            TreeIdentity
	Tool              string
	ReceiverThreadIDs []string
	// Activity retains the native V2 creation item; Tool/receivers remain collab-only.
	Activity *types.SubAgentActivityThreadItem
}

// TreeEvent wraps an independently owned native Event or an SDK-derived record.
// Kind and Origin are SDK metadata, never fields added to Event, native types,
// Params, or Raw. Derived records have nil Native/Raw and no fabricated method.
// Raw is the exact notification envelope, excluding transport delimiters, only
// for Native. Origin is "native" or "sdk-derived"; Provenance is "live" or "history".
// Sequence is local delivery order, not a replay cursor. Epoch identifies this
// Client connection within this process, not a durable provider identity.
// Scope is "root", "child", "unresolved", "unscoped", or "observation". Unscoped events do
// not establish membership. UsageScope is "unknown"; no usage is aggregated.
// Created confirms creation via a live successful spawnAgent completed item or
// a live completed SubAgentActivity item of kind started at the pinned version.
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
	// PreviousEvidence retains the accepted edge when Evidence conflicts with it.
	PreviousEvidence *TreeEvidence
	Native           *Event
	Raw              json.RawMessage
	// State and Reason describe observation limits, never provider work success.
	State  string
	Reason string
}
