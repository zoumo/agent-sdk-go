package agent

import (
	"context"
	"reflect"
	"time"
)

// EventType identifies the single payload branch carried by an Event.
type EventType string

const (
	EventUserMessage               EventType = "user.message"
	EventAgentMessage              EventType = "agent.message"
	EventAgentThinking             EventType = "agent.thinking"
	EventAgentToolCall             EventType = "agent.tool_call"
	EventAgentToolResult           EventType = "agent.tool_result"
	EventAgentMessageChunk         EventType = "agent.message_chunk"
	EventAgentThinkingChunk        EventType = "agent.thinking_chunk"
	EventAgentToolCallChunk        EventType = "agent.tool_call_chunk"
	EventAgentPlan                 EventType = "agent.plan"
	EventSessionStateUpdate        EventType = "session.state_update"
	EventSessionError              EventType = "session.error"
	EventSessionInfoUpdate         EventType = "session.info_update"
	EventSessionConfigOptionUpdate EventType = "session.config_option_update"
	EventSessionCommandsUpdate     EventType = "session.available_commands_update"
	EventSessionUsageUpdate        EventType = "session.usage_update"
	EventInteractionRequested      EventType = "session.interaction_requested"
	EventInteractionClosed         EventType = "session.interaction_closed"
)

// Payload is a closed event-payload union. Its unexported method prevents
// implementations outside this package.
type Payload interface {
	eventType() EventType
}

// Event is one provider-neutral live event. EventID identifies this event once;
// it is not an order or a persistent history cursor.
//
//nolint:recvcheck // JSON decoding mutates an event; reads and encoding accept values.
type Event struct {
	EventID   string
	SessionID string
	Time      time.Time
	ItemID    string
	Payload   Payload
	Meta      map[string]any
}

// Type returns the payload's fixed event type. It is empty when no payload is
// present.
func (e Event) Type() EventType {
	if e.Payload == nil || isNilPayload(e.Payload) {
		return ""
	}
	return e.Payload.eventType()
}

// Validate checks the event envelope, the closed payload branch, and JSON-safe
// extension metadata.
func (e Event) Validate() error {
	if !validUUID(e.EventID) {
		return invalidArgument("invalid_event_id", "event ID must be a UUID")
	}
	if e.SessionID == "" {
		return invalidArgument("missing_session_id", "event session ID is required")
	}
	if e.Time.IsZero() {
		return invalidArgument("missing_event_time", "event time is required")
	}
	payload, err := concretePayload(e.Payload)
	if err != nil {
		return err
	}
	if err := validatePayload(payload); err != nil {
		return err
	}
	if payloadRequiresItem(payload) && e.ItemID == "" {
		return invalidArgument("missing_item_id", "event payload requires an item ID")
	}
	if err := validateJSONValue(e.Meta); err != nil {
		return invalidArgument("invalid_meta", "event metadata is not JSON encodable")
	}
	return nil
}

// EventStream is the single-consumer stream for one session.
type EventStream interface {
	// Next returns the next accepted event. io.EOF means normal completion;
	// other errors are stable abnormal termination causes. One stream must not
	// be read concurrently.
	Next(ctx context.Context) (Event, error)
}

func concretePayload(payload Payload) (Payload, error) {
	if payload == nil {
		return nil, invalidArgument("missing_payload", "event payload is required")
	}
	value := reflect.ValueOf(payload)
	if value.Kind() != reflect.Pointer {
		return payload, nil
	}
	if value.IsNil() {
		return nil, invalidArgument("missing_payload", "event payload is required")
	}
	concrete, ok := value.Elem().Interface().(Payload)
	if !ok {
		return nil, invalidArgument("unsupported_payload", "event payload is not supported")
	}
	return concrete, nil
}

func isNilPayload(payload Payload) bool {
	value := reflect.ValueOf(payload)
	return value.Kind() == reflect.Pointer && value.IsNil()
}

func payloadRequiresItem(payload Payload) bool {
	switch payload.(type) {
	case UserMessage,
		AgentMessage,
		AgentThinking,
		ToolCallPayload,
		ToolResultPayload,
		MessageChunk,
		ThinkingChunk,
		ToolChunkPayload,
		PlanPayload:
		return true
	default:
		return false
	}
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index := range len(value) {
		switch index {
		case 8, 13, 18, 23:
			if value[index] != '-' {
				return false
			}
		default:
			if !isHex(value[index]) {
				return false
			}
		}
	}
	return true
}

func isHex(value byte) bool {
	return value >= '0' && value <= '9' ||
		value >= 'a' && value <= 'f' ||
		value >= 'A' && value <= 'F'
}
