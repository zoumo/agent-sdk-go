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
// Thread identity stays in the notification payload.
type Event struct {
	// Raw preserves the received notification envelope, excluding transport delimiters.
	Raw         json.RawMessage
	Method      string
	Params      json.RawMessage
	Data        any
	DecodeError error
}
