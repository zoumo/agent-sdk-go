package types

import (
	"encoding/json"
	"time"
)

// SubscriptionOptions bounds one local observation. Zero values select defaults;
// negative values are invalid. PendingAge limits association waiting, not work.
type SubscriptionOptions struct {
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
