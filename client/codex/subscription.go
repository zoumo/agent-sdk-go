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
	for _, field := range []struct {
		direct, nested string
		target         *string
	}{
		{"threadId", "thread", &id.ThreadID}, {"turnId", "turn", &id.TurnID}, {"itemId", "item", &id.ItemID},
	} {
		direct, directOK := nativeIdentity(f[field.direct])
		nested, _ := objectFields(f[field.nested])
		nestedID, nestedOK := nativeIdentity(nested["id"])
		if !directOK || !nestedOK || direct != "" && nestedID != "" && direct != nestedID {
			continue
		}
		*field.target = direct
		if direct == "" {
			*field.target = nestedID
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

// An absent identity is unknown; a malformed one must never become U+FFFD.
func nativeIdentity(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", true
	}
	var value string
	if !json.Valid(raw) || raw[0] != '"' || !validStringEscapes(raw) || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}
