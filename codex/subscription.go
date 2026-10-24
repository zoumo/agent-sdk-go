package codex

import (
	"encoding/json"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

// publishLocked routes native notifications to the owning Sessions.
func (c *Client) publishLocked(method string, params json.RawMessage, raw []byte) {
	c.publishSessionsLocked(method, params, raw)
}

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
