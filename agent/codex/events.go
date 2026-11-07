package agentcodex

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
)

func (s *session) convertEvent(received codex.Received) (agent.Event, bool, error) {
	event := received.Event
	if event == nil {
		return agent.Event{}, false, nil
	}
	if received.Source.ThreadID != "" && received.Source.ThreadID != s.ID() {
		return agent.Event{}, false, nil
	}
	if event.DecodeError != nil {
		return agent.Event{}, false, adapterError(
			agent.ErrorUnavailable,
			"event_decode_failed",
			"decode native Codex event",
			event.DecodeError,
		)
	}

	switch event.Method {
	case "turn/started":
		data, ok := event.Data.(*codextypes.TurnStartedNotification)
		if !ok || data.Turn.ID == "" {
			return agent.Event{}, false, invalidNativeEvent(event.Method)
		}
		state := agent.StateRunning
		payload := s.updateState(&state, "", nil)
		s.mu.Lock()
		s.activeTurn = data.Turn.ID
		if s.lastFinished == data.Turn.ID {
			s.lastFinished = ""
		}
		s.mu.Unlock()
		return newEvent(received, "", payload, optionalSeconds(data.Turn.StartedAt))

	case "item/agentMessage/delta":
		data, ok := event.Data.(*codextypes.AgentMessageDeltaNotification)
		if !ok || data.ItemID == "" || data.TurnID == "" {
			return agent.Event{}, false, invalidNativeEvent(event.Method)
		}
		payload := agent.MessageChunk{
			MessageID: data.ItemID,
			Content:   []agent.ContentBlock{agent.TextBlock{Text: data.Delta}},
		}
		return newEvent(received, data.ItemID, payload, time.Now().UTC())

	case "item/completed":
		data, ok := event.Data.(*codextypes.ItemCompletedNotification)
		if !ok || data.Item.AgentMessage == nil {
			return agent.Event{}, false, nil
		}
		item := data.Item.AgentMessage
		if item.ID == "" {
			return agent.Event{}, false, invalidNativeEvent(event.Method)
		}
		phase := ""
		if value, present := item.Phase.Get(); present {
			phase = string(value)
		}
		payload := agent.AgentMessage{
			MessageID: item.ID,
			Content:   []agent.ContentBlock{agent.TextBlock{Text: item.Text}},
			Phase:     phase,
		}
		return newEvent(
			received,
			item.ID,
			payload,
			time.UnixMilli(data.CompletedAtMs).UTC(),
		)

	case "turn/completed":
		data, ok := event.Data.(*codextypes.TurnCompletedNotification)
		if !ok || data.Turn.ID == "" {
			return agent.Event{}, false, invalidNativeEvent(event.Method)
		}
		state := agent.StateIdle
		var observed *agent.ObservedError
		if turnError, present := data.Turn.Error.Get(); present {
			observed = &agent.ObservedError{Message: turnError.Message}
		}
		payload := s.updateState(&state, string(data.Turn.Status), observed)
		s.mu.Lock()
		if s.activeTurn == data.Turn.ID {
			s.activeTurn = ""
		}
		s.lastFinished = data.Turn.ID
		s.mu.Unlock()
		return newEvent(
			received,
			"",
			payload,
			optionalSeconds(data.Turn.CompletedAt),
		)

	case "thread/status/changed":
		var data struct {
			Status codextypes.ThreadStatus `json:"status"`
		}
		if err := json.Unmarshal(event.Params, &data); err != nil {
			return agent.Event{}, false, invalidNativeEvent(event.Method)
		}
		var state agent.SessionState
		switch {
		case data.Status.Active != nil:
			state = agent.StateRunning
			if len(data.Status.Active.ActiveFlags) != 0 {
				state = agent.StateRequiresAction
			}
		case data.Status.Idle != nil:
			state = agent.StateIdle
		case data.Status.NotLoaded != nil:
			return newEvent(
				received,
				"",
				s.updateState(nil, "notLoaded", nil),
				time.Now().UTC(),
			)
		case data.Status.SystemError != nil:
			return newEvent(
				received,
				"",
				s.updateState(nil, "systemError", nil),
				time.Now().UTC(),
			)
		case data.Status.Unknown != nil:
			return newEvent(
				received,
				"",
				s.updateState(nil, "unknown", nil),
				time.Now().UTC(),
			)
		default:
			return agent.Event{}, false, nil
		}
		return newEvent(received, "", s.updateState(&state, "", nil), time.Now().UTC())

	case "error":
		data, ok := event.Data.(*codextypes.ErrorNotification)
		if !ok || data.Error.Message == "" {
			return agent.Event{}, false, invalidNativeEvent(event.Method)
		}
		payload := agent.SessionErrorPayload{
			Message: data.Error.Message,
			Reason:  "native_error",
		}
		return newEvent(received, "", payload, time.Now().UTC())

	default:
		return agent.Event{}, false, nil
	}
}

func (s *session) updateState(
	state *agent.SessionState,
	stopReason string,
	observed *agent.ObservedError,
) agent.StateUpdatePayload {
	s.mu.Lock()
	defer s.mu.Unlock()
	var previous *agent.SessionState
	if s.state != nil {
		value := *s.state
		previous = &value
	}
	if state != nil {
		value := *state
		s.state = &value
	}
	return agent.StateUpdatePayload{
		PreviousState: previous,
		State:         state,
		StopReason:    stopReason,
		Error:         observed,
	}
}

func newEvent(
	received codex.Received,
	itemID string,
	payload agent.Payload,
	eventTime time.Time,
) (agent.Event, bool, error) {
	eventID, err := newEventID()
	if err != nil {
		return agent.Event{}, false, adapterError(
			agent.ErrorUnavailable,
			"event_id_failed",
			"create unified event identity",
			err,
		)
	}
	raw := received.Raw
	if len(raw) == 0 && received.Event != nil {
		raw = received.Event.Raw
	}
	event := agent.Event{
		EventID:   eventID,
		SessionID: received.Source.ThreadID,
		Time:      eventTime,
		ItemID:    itemID,
		Payload:   payload,
		Meta: map[string]any{
			"codex": map[string]any{
				"method": received.Event.Method,
				"raw":    append(json.RawMessage(nil), raw...),
				"turnId": received.Source.TurnID,
				"itemId": received.Source.ItemID,
			},
		},
	}
	if event.SessionID == "" {
		return agent.Event{}, false, invalidNativeEvent(received.Event.Method)
	}
	if err := event.Validate(); err != nil {
		return agent.Event{}, false, adapterError(
			agent.ErrorUnavailable,
			"event_invalid",
			"convert native Codex event",
			err,
		)
	}
	return event, true, nil
}

func optionalSeconds(value codextypes.Optional[int64]) time.Time {
	seconds, ok := value.Get()
	if !ok || seconds < 0 {
		return time.Now().UTC()
	}
	return time.Unix(seconds, 0).UTC()
}

func invalidNativeEvent(method string) error {
	return adapterError(
		agent.ErrorUnavailable,
		"invalid_native_event",
		"invalid native Codex event: "+method,
		nil,
	)
}

func newEventID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	encoded := make([]byte, 32)
	hex.Encode(encoded, value[:])
	return fmt.Sprintf(
		"%s-%s-%s-%s-%s",
		encoded[0:8],
		encoded[8:12],
		encoded[12:16],
		encoded[16:20],
		encoded[20:32],
	), nil
}
