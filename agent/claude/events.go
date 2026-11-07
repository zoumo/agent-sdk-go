package agentclaude

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/internal/uuidv4"
)

func (s *session) convertReceived(received claude.Received) ([]agent.Event, error) {
	switch received.Kind {
	case claude.ReceivedNative:
		return s.convertEvent(received)
	case claude.ReceivedRequest:
		return s.requestGap(received)
	case claude.ReceivedObservation:
		return nil, nil
	default:
		return nil, nil
	}
}

func (s *session) convertEvent(received claude.Received) ([]agent.Event, error) {
	event := received.Event
	if event == nil || event.IsReplay != nil && *event.IsReplay {
		return nil, nil
	}

	var (
		events []agent.Event
		err    error
	)
	switch {
	case event.Type == "system" && event.Subtype == "init":
		return nil, nil
	case event.Assistant != nil:
		events, err = s.assistantEvent(received)
	case event.Type == "system" && event.Subtype == "session_state_changed":
		events, err = s.stateEvent(received)
	case event.Result != nil:
		events, err = s.resultEvent(received)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if event.Error != nil && *event.Error != "" &&
		(event.Result == nil || !event.Result.IsError) {
		errorEvents, errorErr := s.envelopeErrorEvent(received)
		if errorErr != nil {
			return nil, errorErr
		}
		events = append(events, errorEvents...)
	}
	return events, nil
}

func (s *session) assistantEvent(
	received claude.Received,
) ([]agent.Event, error) {
	message := received.Event.Assistant
	if message == nil {
		return s.gap(
			received,
			"invalid_assistant_event",
			"Claude assistant event has no message",
			nil,
		)
	}
	if message.ID == "" {
		return s.gap(
			received,
			"assistant_message_identity_missing",
			"Claude assistant message has no native message identity",
			map[string]any{"uuid": received.Event.UUID},
		)
	}

	var content []agent.ContentBlock
	switch {
	case message.Content.Text != nil:
		content = []agent.ContentBlock{agent.TextBlock{Text: *message.Content.Text}}
	case len(message.Content.Blocks) != 0:
		for _, block := range message.Content.Blocks {
			if block.Text == nil {
				return s.gap(
					received,
					"assistant_content_unsupported",
					"Claude assistant message contains content outside this adapter's minimal event set",
					map[string]any{"blockType": block.Type},
				)
			}
			content = append(content, agent.TextBlock{Text: block.Text.Text})
		}
	default:
		return s.gap(
			received,
			"assistant_content_missing",
			"Claude assistant message has no content",
			nil,
		)
	}

	event, err := s.newEvent(
		received,
		message.ID,
		agent.AgentMessage{
			MessageID: message.ID,
			Content:   content,
		},
		nativeEventTime(received.Raw),
		map[string]any{"messageId": message.ID},
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) stateEvent(
	received claude.Received,
) ([]agent.Event, error) {
	event := received.Event
	if event.System == nil || event.System.State == nil || *event.System.State == "" {
		return s.gap(
			received,
			"session_state_missing",
			"Claude session state event has no state",
			nil,
		)
	}

	var state agent.SessionState
	switch *event.System.State {
	case string(agent.StateIdle):
		state = agent.StateIdle
	case string(agent.StateRunning):
		state = agent.StateRunning
	case string(agent.StateRequiresAction):
		state = agent.StateRequiresAction
	default:
		previous := s.markUnknownState()
		return s.gap(
			received,
			"session_state_unsupported",
			"Claude session state is not in the unified state set",
			map[string]any{
				"nativeState":   *event.System.State,
				"previousState": stringOrNil(previous),
			},
		)
	}

	previous := s.updateState(state)
	next := state
	unified, err := s.newEvent(
		received,
		"",
		agent.StateUpdatePayload{
			PreviousState: previous,
			State:         &next,
		},
		nativeEventTime(received.Raw),
		map[string]any{"nativeState": *event.System.State},
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{unified}, nil
}

func (s *session) resultEvent(
	received claude.Received,
) ([]agent.Event, error) {
	result := received.Event.Result
	if result == nil {
		return nil, nil
	}
	if !result.IsError {
		return nil, nil
	}
	message := strings.TrimSpace(result.Result)
	if message == "" && len(result.Errors) != 0 {
		message = strings.Join(result.Errors, "; ")
	}
	if message == "" {
		message = "Claude returned an error result"
	}
	unified, err := s.newEvent(
		received,
		"",
		agent.SessionErrorPayload{
			Message: message,
			Reason:  "native_result_error",
		},
		nativeEventTime(received.Raw),
		map[string]any{
			"resultSubtype":  result.Subtype,
			"terminalReason": result.TerminalReason,
		},
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{unified}, nil
}

func (s *session) envelopeErrorEvent(
	received claude.Received,
) ([]agent.Event, error) {
	unified, err := s.newEvent(
		received,
		"",
		agent.SessionErrorPayload{
			Message: *received.Event.Error,
			Reason:  "native_error",
		},
		nativeEventTime(received.Raw),
		nil,
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{unified}, nil
}

func (s *session) requestGap(
	received claude.Received,
) ([]agent.Event, error) {
	if received.Request == nil {
		return nil, nil
	}
	return s.gap(
		received,
		"interaction_request_unsupported",
		"Claude interaction requests are not supported by this adapter",
		map[string]any{
			"requestId": received.Request.ID(),
			"method":    received.Request.Method(),
		},
	)
}

func (s *session) gap(
	received claude.Received,
	reason string,
	message string,
	extras map[string]any,
) ([]agent.Event, error) {
	event, err := s.newEvent(
		received,
		"",
		agent.SessionErrorPayload{Message: message, Reason: reason},
		nativeEventTime(received.Raw),
		extras,
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) newEvent(
	received claude.Received,
	itemID string,
	payload agent.Payload,
	eventTime time.Time,
	extras map[string]any,
) (agent.Event, error) {
	eventID, err := uuidv4.New()
	if err != nil {
		return agent.Event{}, adapterError(
			agent.ErrorUnavailable,
			"event_id_failed",
			"create unified event identity",
			err,
		)
	}
	if eventTime.IsZero() {
		eventTime = time.Now().UTC()
	}

	raw := received.Raw
	eventType := ""
	subtype := ""
	if received.Event != nil {
		raw = received.Event.Raw
		eventType = received.Event.Type
		subtype = received.Event.Subtype
	}
	if received.Request != nil {
		eventType = "control_request"
		subtype = received.Request.Method()
	}

	claudeMeta := map[string]any{}
	if len(raw) != 0 {
		claudeMeta["raw"] = append(json.RawMessage(nil), raw...)
	}
	if eventType != "" {
		claudeMeta["type"] = eventType
	}
	if subtype != "" {
		claudeMeta["subtype"] = subtype
	}
	if received.Event != nil {
		if received.Event.UUID != "" {
			claudeMeta["uuid"] = received.Event.UUID
		}
		if received.Event.AgentID != nil {
			claudeMeta["agentId"] = *received.Event.AgentID
		}
		if received.Event.ParentToolUseID != nil {
			claudeMeta["parentToolUseId"] = *received.Event.ParentToolUseID
		}
	}
	for key, value := range extras {
		if _, reserved := claudeMeta[key]; !reserved {
			claudeMeta[key] = value
		}
	}

	event := agent.Event{
		EventID:   eventID,
		SessionID: s.ID(),
		Time:      eventTime,
		ItemID:    itemID,
		Payload:   payload,
		Meta: map[string]any{
			"claude": claudeMeta,
		},
	}
	if err := event.Validate(); err != nil {
		return agent.Event{}, adapterError(
			agent.ErrorUnavailable,
			"event_invalid",
			"convert native Claude event",
			err,
		)
	}
	return event, nil
}

func nativeEventTime(raw json.RawMessage) time.Time {
	var envelope struct {
		Timestamp json.RawMessage `json:"timestamp"`
	}
	var value string
	if json.Unmarshal(raw, &envelope) == nil &&
		json.Unmarshal(envelope.Timestamp, &value) == nil &&
		value != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Now().UTC()
}

func stringOrNil(value *agent.SessionState) any {
	if value == nil {
		return nil
	}
	return string(*value)
}
