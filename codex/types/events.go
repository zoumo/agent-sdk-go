package types

import "encoding/json"

// Event preserves a notification's method and parameter JSON as received.
// Params is nil when the member was absent; explicit null remains the bytes "null". Data is a pointer to
// a supported protocol notification or nil for unknown methods/decode failures. SDK observations are separate
// Received values.
// DecodeError is non-nil only for malformed supported payloads. Unknown fields
// and union variants remain in the native Data and in Params.
//
// Each receiving Session owns its event's buffers and typed data independently.
// Internal history projection uses this type with History set and has no
// received Raw envelope. Thread identity stays in the notification payload.
type Event struct {
	// Raw preserves the received notification envelope, excluding transport delimiters.
	// History projections have no received envelope; see HistorySource.
	Raw         json.RawMessage
	Method      string
	Params      json.RawMessage
	Data        any
	DecodeError error
	// History is SDK-only source evidence for a projected retained snapshot.
	// It is nil for notifications received on the live connection.
	History *HistorySource `json:"-"`
}

// HistorySource preserves the exact original item JSON for an item snapshot,
// or full turn JSON for a terminal/unknown turn snapshot. ItemID is empty for
// turn snapshots. MissingFields lists notification fields not recorded in history;
// their typed zero values are unknown, not observed values. In particular, full
// turns do not contain the required live item completion timestamp.
type HistorySource struct {
	ThreadID      string
	TurnID        string
	ItemID        string
	Raw           json.RawMessage
	MissingFields []string
}
