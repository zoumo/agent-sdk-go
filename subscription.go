package codex

import (
	"encoding/json"

	"github.com/zoumo/codex-sdk-go/types"
)

// Event preserves a notification's method and parameter JSON as received.
// Params is nil when the member was absent; explicit null remains the bytes "null". Data is a pointer to
// a supported protocol notification, or nil for unknown methods/decode failures.
// DecodeError is non-nil only for malformed supported payloads. Unknown fields
// and union variants remain in the native Data and in Params.
//
// Each subscriber owns its event's buffers and typed data independently. There
// is no replay; the Subscription wrapper supplies root/child scope and identity.
type Event struct {
	Method      string
	Params      json.RawMessage
	Data        any
	DecodeError error
}

// publishLocked shares the connection reader across bounded root observations.
// Each subscription decodes into independently owned native storage.
func (c *Client) publishLocked(method string, params json.RawMessage, raw []byte) {
	for s := range c.subscriptions {
		s.publishLocked(method, params, raw)
	}
}

func decodeEvent(method string, params json.RawMessage) Event {
	event := Event{Method: method, Params: append(json.RawMessage(nil), params...)}
	var data any
	switch method {
	case "serverRequest/resolved":
		data = new(types.ServerRequestResolvedNotification)
	case "turn/started":
		data = new(types.TurnStartedNotification)
	case "turn/completed":
		data = new(types.TurnCompletedNotification)
	case "item/started":
		data = new(types.ItemStartedNotification)
	case "item/completed":
		data = new(types.ItemCompletedNotification)
	case "item/agentMessage/delta":
		data = new(types.AgentMessageDeltaNotification)
	case "error":
		data = new(types.ErrorNotification)
	default:
		return event
	}
	if err := json.Unmarshal(params, data); err != nil {
		event.DecodeError = &DecodeError{Method: method, Cause: err}
	} else {
		event.Data = data
	}
	return event
}
