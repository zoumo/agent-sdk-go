package codex

import (
	"encoding/json"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

// publishLocked shares the connection reader across independent root observations.
// Each subscription decodes into independently owned native storage.
func (c *Client) publishLocked(method string, params json.RawMessage, raw []byte) {
	c.publishSessionsLocked(method, params, raw)
	for s := range c.subscriptions {
		s.publishLocked(method, params, raw)
	}
}

func decodeEvent(method string, params json.RawMessage) types.Event {
	event := types.Event{Method: method, Params: append(json.RawMessage(nil), params...)}
	var data any
	switch method {
	case "thread/settings/updated":
		data = new(types.ThreadSettingsUpdatedNotification)
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
