package agent

import (
	"encoding/json"
	"errors"
	"time"
)

type eventJSON struct {
	EventID   string         `json:"eventId"`
	SessionID string         `json:"sessionId"`
	Time      time.Time      `json:"time"`
	ItemID    string         `json:"itemId,omitempty"`
	Type      EventType      `json:"type"`
	Payload   Payload        `json:"payload"`
	Meta      map[string]any `json:"_meta,omitempty"`
}

type eventJSONWire struct {
	EventID   string          `json:"eventId"`
	SessionID string          `json:"sessionId"`
	Time      time.Time       `json:"time"`
	ItemID    string          `json:"itemId"`
	Type      EventType       `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	Meta      map[string]any  `json:"_meta"`
}

func (e Event) MarshalJSON() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	payload, err := concretePayload(e.Payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(eventJSON{
		EventID:   e.EventID,
		SessionID: e.SessionID,
		Time:      e.Time,
		ItemID:    e.ItemID,
		Type:      e.Type(),
		Payload:   payload,
		Meta:      e.Meta,
	})
}

func (e *Event) UnmarshalJSON(data []byte) error {
	var wire eventJSONWire
	if err := decodeStrictJSON(data, &wire); err != nil {
		return invalidArgument("invalid_event_json", "decode event: "+err.Error())
	}
	if len(wire.Payload) == 0 || isJSONNull(wire.Payload) {
		return invalidArgument("missing_payload", "event payload is required")
	}
	payload, err := unmarshalPayload(wire.Type, wire.Payload)
	if err != nil {
		return invalidArgument("invalid_event_payload", err.Error())
	}
	decoded := Event{
		EventID:   wire.EventID,
		SessionID: wire.SessionID,
		Time:      wire.Time,
		ItemID:    wire.ItemID,
		Payload:   payload,
		Meta:      wire.Meta,
	}
	if decoded.Type() != wire.Type {
		return invalidArgument(
			"payload_type_mismatch",
			"event type does not match payload",
		)
	}
	if err := decoded.Validate(); err != nil {
		return err
	}
	*e = decoded
	return nil
}

func unmarshalPayload(eventType EventType, data []byte) (Payload, error) {
	switch eventType {
	case EventUserMessage:
		payload, err := unmarshalMessagePayload(data)
		return UserMessage(payload), err
	case EventAgentMessage:
		payload, err := unmarshalMessagePayload(data)
		return AgentMessage(payload), err
	case EventAgentThinking:
		payload, err := unmarshalMessagePayload(data)
		return AgentThinking(payload), err
	case EventAgentMessageChunk:
		payload, err := unmarshalMessagePayload(data)
		return MessageChunk(payload), err
	case EventAgentThinkingChunk:
		payload, err := unmarshalMessagePayload(data)
		return ThinkingChunk(payload), err
	case EventAgentToolCall:
		var payload ToolCallPayload
		if err := decodeStrictJSON(data, &payload); err != nil {
			return nil, err
		}
		return payload, nil
	case EventAgentToolResult:
		var payload ToolResultPayload
		if err := decodeStrictJSON(data, &payload); err != nil {
			return nil, err
		}
		return payload, nil
	case EventAgentToolCallChunk:
		var payload ToolChunkPayload
		if err := decodeStrictJSON(data, &payload); err != nil {
			return nil, err
		}
		return payload, nil
	case EventSessionStateUpdate:
		if err := requireJSONMemberPresence(data, "state", "previousState"); err != nil {
			return nil, err
		}
		var payload StateUpdatePayload
		if err := decodeStrictJSON(data, &payload); err != nil {
			return nil, err
		}
		return payload, nil
	case EventSessionError:
		var payload SessionErrorPayload
		if err := decodeStrictJSON(data, &payload); err != nil {
			return nil, err
		}
		return payload, nil
	case EventSessionInfoUpdate:
		var payload InfoUpdatePayload
		if err := decodeStrictJSON(data, &payload); err != nil {
			return nil, err
		}
		return payload, nil
	case EventSessionConfigOptionUpdate:
		var payload ConfigOptionUpdatePayload
		if err := decodeStrictJSON(data, &payload); err != nil {
			return nil, err
		}
		return payload, nil
	case EventSessionCommandsUpdate:
		var payload AvailableCommandsUpdatePayload
		if err := decodeStrictJSON(data, &payload); err != nil {
			return nil, err
		}
		return payload, nil
	case EventSessionUsageUpdate:
		if err := requireJSONMembers(data, "used", "size"); err != nil {
			return nil, err
		}
		var payload UsageUpdatePayload
		if err := decodeStrictJSON(data, &payload); err != nil {
			return nil, err
		}
		return payload, nil
	case EventInteractionRequested:
		var payload InteractionRequestedPayload
		if err := decodeStrictJSON(data, &payload); err != nil {
			return nil, err
		}
		return payload, nil
	case EventInteractionClosed:
		var payload InteractionClosedPayload
		if err := decodeStrictJSON(data, &payload); err != nil {
			return nil, err
		}
		return payload, nil
	default:
		return nil, errors.New("unsupported event type")
	}
}

func unmarshalMessagePayload(data []byte) (MessagePayload, error) {
	var payload MessagePayload
	if err := decodeStrictJSON(data, &payload); err != nil {
		return MessagePayload{}, err
	}
	return payload, nil
}

func optionalDynamicJSONMember(data []byte, name string) (*any, error) {
	members, err := jsonMembers(data)
	if err != nil {
		return nil, err
	}
	raw, ok := members[name]
	if !ok {
		return nil, nil
	}
	var value any
	if err := decodeStrictJSON(raw, &value); err != nil {
		return nil, err
	}
	return &value, nil
}
