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

type messagePayloadWire struct {
	MessageID string            `json:"messageId"`
	Content   []json.RawMessage `json:"content"`
	Phase     string            `json:"phase"`
}

type toolCallPayloadWire struct {
	ID                  string            `json:"id"`
	Kind                string            `json:"kind"`
	Name                string            `json:"name"`
	Title               string            `json:"title"`
	Phase               string            `json:"phase"`
	InputRepresentation string            `json:"inputRepresentation"`
	RawInput            any               `json:"rawInput"`
	RawOutput           *any              `json:"rawOutput"`
	Content             []json.RawMessage `json:"content"`
	ExitCode            *int32            `json:"exitCode"`
	DurationMs          *float64          `json:"durationMs"`
	Output              *ToolOutput       `json:"output"`
	Status              string            `json:"status"`
}

type toolResultPayloadWire struct {
	ID                  string            `json:"id"`
	Kind                string            `json:"kind"`
	Name                string            `json:"name"`
	Title               string            `json:"title"`
	Phase               string            `json:"phase"`
	InputRepresentation string            `json:"inputRepresentation"`
	RawInput            *any              `json:"rawInput"`
	RawOutput           *any              `json:"rawOutput"`
	Content             []json.RawMessage `json:"content"`
	ExitCode            *int32            `json:"exitCode"`
	DurationMs          *float64          `json:"durationMs"`
	Output              *ToolOutput       `json:"output"`
	Status              string            `json:"status"`
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
	if len(wire.Payload) == 0 {
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
		return unmarshalToolCallPayload(data)
	case EventAgentToolResult:
		return unmarshalToolResultPayload(data)
	case EventAgentToolCallChunk:
		var wire struct {
			ID      string            `json:"id"`
			Content []json.RawMessage `json:"content"`
		}
		if err := decodeStrictJSON(data, &wire); err != nil {
			return nil, err
		}
		content, err := unmarshalContentBlocks(wire.Content)
		if err != nil {
			return nil, err
		}
		return ToolChunkPayload{ID: wire.ID, Content: content}, nil
	case EventAgentPlan:
		var payload PlanPayload
		if err := decodeStrictJSON(data, &payload); err != nil {
			return nil, err
		}
		return payload, nil
	case EventSessionStateUpdate:
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
	var wire messagePayloadWire
	if err := decodeStrictJSON(data, &wire); err != nil {
		return MessagePayload{}, err
	}
	content, err := unmarshalContentBlocks(wire.Content)
	if err != nil {
		return MessagePayload{}, err
	}
	return MessagePayload{
		MessageID: wire.MessageID,
		Content:   content,
		Phase:     wire.Phase,
	}, nil
}

func unmarshalToolCallPayload(data []byte) (Payload, error) {
	var wire toolCallPayloadWire
	if err := decodeStrictJSON(data, &wire); err != nil {
		return nil, err
	}
	content, err := unmarshalContentBlocks(wire.Content)
	if err != nil {
		return nil, err
	}
	return ToolCallPayload{
		ID:                  wire.ID,
		Kind:                wire.Kind,
		Name:                wire.Name,
		Title:               wire.Title,
		Phase:               wire.Phase,
		InputRepresentation: wire.InputRepresentation,
		RawInput:            wire.RawInput,
		RawOutput:           wire.RawOutput,
		Content:             content,
		ExitCode:            wire.ExitCode,
		DurationMs:          wire.DurationMs,
		Output:              wire.Output,
		Status:              wire.Status,
	}, nil
}

func unmarshalToolResultPayload(data []byte) (Payload, error) {
	var wire toolResultPayloadWire
	if err := decodeStrictJSON(data, &wire); err != nil {
		return nil, err
	}
	content, err := unmarshalContentBlocks(wire.Content)
	if err != nil {
		return nil, err
	}
	return ToolResultPayload{
		ID:                  wire.ID,
		Kind:                wire.Kind,
		Name:                wire.Name,
		Title:               wire.Title,
		Phase:               wire.Phase,
		InputRepresentation: wire.InputRepresentation,
		RawInput:            wire.RawInput,
		RawOutput:           wire.RawOutput,
		Content:             content,
		ExitCode:            wire.ExitCode,
		DurationMs:          wire.DurationMs,
		Output:              wire.Output,
		Status:              wire.Status,
	}, nil
}

func unmarshalContentBlocks(raw []json.RawMessage) ([]ContentBlock, error) {
	if raw == nil {
		return nil, nil
	}
	content := make([]ContentBlock, len(raw))
	for index, block := range raw {
		decoded, err := unmarshalContentBlock(block)
		if err != nil {
			return nil, err
		}
		content[index] = decoded
	}
	return content, nil
}
