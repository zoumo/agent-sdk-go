package agent_test

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

const validEventID = "01234567-89ab-4cde-8fab-0123456789ab"

func TestEventTypeIsDerivedFromEveryPayloadBranch(t *testing.T) {
	running := agent.StateRunning
	title := "Session title"
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	initialState := agent.StateIdle

	permission := agent.InteractionRequest{
		Kind:       agent.InteractionPermission,
		ToolCallID: "tool-1",
		Options: []agent.PermissionOption{
			{OptionID: "allow", Name: "Allow", Kind: agent.PermissionAllowOnce},
		},
	}

	tests := []struct {
		name    string
		itemID  string
		payload agent.Payload
		want    agent.EventType
	}{
		{
			name:    "user message",
			itemID:  "item-1",
			payload: agent.UserMessage{MessageID: "message-1", Content: textContent("hello")},
			want:    agent.EventUserMessage,
		},
		{
			name:    "agent message",
			itemID:  "item-2",
			payload: agent.AgentMessage{MessageID: "message-2", Content: textContent("hi")},
			want:    agent.EventAgentMessage,
		},
		{
			name:    "agent thinking",
			itemID:  "item-3",
			payload: agent.AgentThinking{MessageID: "thinking-1", Content: textContent("thinking")},
			want:    agent.EventAgentThinking,
		},
		{
			name:   "tool call",
			itemID: "tool-1",
			payload: agent.ToolCallPayload{
				ID:       "tool-1",
				Kind:     "command",
				Status:   "in_progress",
				RawInput: map[string]any{"command": "go test"},
			},
			want: agent.EventAgentToolCall,
		},
		{
			name:   "tool result",
			itemID: "tool-1",
			payload: agent.ToolResultPayload{
				ID:        "tool-1",
				Kind:      "command",
				Status:    "completed",
				RawOutput: ptr[any]("ok"),
			},
			want: agent.EventAgentToolResult,
		},
		{
			name:    "message chunk",
			itemID:  "item-4",
			payload: agent.MessageChunk{MessageID: "message-4", Content: textContent("chunk")},
			want:    agent.EventAgentMessageChunk,
		},
		{
			name:    "thinking chunk",
			itemID:  "item-5",
			payload: agent.ThinkingChunk{MessageID: "thinking-5", Content: textContent("chunk")},
			want:    agent.EventAgentThinkingChunk,
		},
		{
			name:   "tool chunk",
			itemID: "tool-2",
			payload: agent.ToolChunkPayload{
				ID:      "tool-2",
				Content: textContent("chunk"),
			},
			want: agent.EventAgentToolCallChunk,
		},
		{
			name: "state update",
			payload: agent.StateUpdatePayload{
				PreviousState: &initialState,
				State:         &running,
			},
			want: agent.EventSessionStateUpdate,
		},
		{
			name:    "session error",
			payload: agent.SessionErrorPayload{Message: "native error", Reason: "transport"},
			want:    agent.EventSessionError,
		},
		{
			name:    "info update",
			payload: agent.InfoUpdatePayload{Title: &title, UpdatedAt: &now},
			want:    agent.EventSessionInfoUpdate,
		},
		{
			name: "config update",
			payload: agent.ConfigOptionUpdatePayload{
				ConfigOptions: []agent.ConfigOption{
					{
						ID:           "model",
						Name:         "Model",
						Type:         "select",
						CurrentValue: "gpt-6",
						Options: []agent.ConfigOptionValue{
							{Value: "gpt-6", Name: "GPT-6"},
						},
					},
				},
			},
			want: agent.EventSessionConfigOptionUpdate,
		},
		{
			name: "commands update",
			payload: agent.AvailableCommandsUpdatePayload{
				AvailableCommands: []agent.AvailableCommand{{Name: "help", Description: "Show help"}},
			},
			want: agent.EventSessionCommandsUpdate,
		},
		{
			name: "usage update",
			payload: agent.UsageUpdatePayload{
				Used: 10,
				Size: 100,
				Cost: &agent.UsageCost{Amount: json.Number("0.25"), Currency: "USD"},
			},
			want: agent.EventSessionUsageUpdate,
		},
		{
			name: "interaction requested",
			payload: agent.InteractionRequestedPayload{
				Interaction: "interaction-1",
				Request:     permission,
				Blocking:    true,
			},
			want: agent.EventInteractionRequested,
		},
		{
			name: "interaction closed",
			payload: agent.InteractionClosedPayload{
				Interaction: "interaction-1",
				Outcome:     agent.InteractionAnswered,
				Reason:      "answered",
			},
			want: agent.EventInteractionClosed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := validEvent(tt.itemID, tt.payload)

			assert.Equal(t, tt.want, event.Type())
			require.NoError(t, event.Validate())
		})
	}
}

func TestEventValidationRejectsInvalidEnvelopeAndPayloadFacts(t *testing.T) {
	validMessage := agent.AgentMessage{
		MessageID: "message-1",
		Content:   textContent("hello"),
	}
	nanDuration := math.NaN()
	infDuration := math.Inf(1)
	invalidTime := time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
	invalidMessage := validMessage
	invalidMessage.Content = []agent.ContentBlock{agent.ImageBlock{
		Data:     "not-base64",
		MIMEType: "image/png",
	}}
	invalidState := agent.SessionState("closing")

	tests := []struct {
		name  string
		event agent.Event
	}{
		{
			name: "malformed event ID",
			event: withEvent(validEvent("item-1", validMessage), func(event *agent.Event) {
				event.EventID = "not-a-uuid"
			}),
		},
		{
			name: "missing session ID",
			event: withEvent(validEvent("item-1", validMessage), func(event *agent.Event) {
				event.SessionID = ""
			}),
		},
		{
			name: "zero time",
			event: withEvent(validEvent("item-1", validMessage), func(event *agent.Event) {
				event.Time = time.Time{}
			}),
		},
		{
			name: "event time cannot encode",
			event: withEvent(validEvent("item-1", validMessage), func(event *agent.Event) {
				event.Time = invalidTime
			}),
		},
		{
			name: "nil payload",
			event: withEvent(validEvent("item-1", validMessage), func(event *agent.Event) {
				event.Payload = nil
			}),
		},
		{
			name:  "item payload without item ID",
			event: validEvent("", validMessage),
		},
		{
			name:  "invalid message content",
			event: validEvent("item-1", invalidMessage),
		},
		{
			name:  "message without identity",
			event: validEvent("item-1", agent.AgentMessage{Content: textContent("hello")}),
		},
		{
			name: "tool call without identity",
			event: validEvent("tool-1", agent.ToolCallPayload{
				Kind:     "command",
				RawInput: map[string]any{},
			}),
		},
		{
			name: "tool result without status",
			event: validEvent("tool-1", agent.ToolResultPayload{
				ID:       "tool-1",
				Kind:     "command",
				RawInput: ptr[any](map[string]any{}),
			}),
		},
		{
			name: "tool call with unsupported status",
			event: validEvent("tool-1", agent.ToolCallPayload{
				ID:       "tool-1",
				Kind:     "command",
				RawInput: map[string]any{},
				Status:   "running",
			}),
		},
		{
			name: "tool call with NaN duration",
			event: validEvent("tool-1", agent.ToolCallPayload{
				ID:         "tool-1",
				Kind:       "command",
				RawInput:   map[string]any{},
				DurationMs: &nanDuration,
			}),
		},
		{
			name: "tool call with infinite duration",
			event: validEvent("tool-1", agent.ToolCallPayload{
				ID:         "tool-1",
				Kind:       "command",
				RawInput:   map[string]any{},
				DurationMs: &infDuration,
			}),
		},
		{
			name: "invalid observed state",
			event: validEvent("", agent.StateUpdatePayload{
				State: &invalidState,
			}),
		},
		{
			name:  "empty session error",
			event: validEvent("", agent.SessionErrorPayload{}),
		},
		{
			name:  "empty info update",
			event: validEvent("", agent.InfoUpdatePayload{}),
		},
		{
			name:  "info update time cannot encode",
			event: validEvent("", agent.InfoUpdatePayload{UpdatedAt: &invalidTime}),
		},
		{
			name: "invalid interaction ref",
			event: validEvent("", agent.InteractionRequestedPayload{
				Request: agent.InteractionRequest{
					Kind:       agent.InteractionPermission,
					ToolCallID: "tool-1",
					Options: []agent.PermissionOption{
						{OptionID: "allow", Name: "Allow", Kind: agent.PermissionAllowOnce},
					},
				},
			}),
		},
		{
			name: "invalid interaction outcome",
			event: validEvent("", agent.InteractionClosedPayload{
				Interaction: "interaction-1",
				Outcome:     "pending",
			}),
		},
		{
			name: "non-JSON metadata",
			event: withEvent(validEvent("item-1", validMessage), func(event *agent.Event) {
				event.Meta = map[string]any{"bad": make(chan int)}
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.event.Validate()

			require.Error(t, err)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
		})
	}
}

func TestEventRoundTripsJSONWithPayloadTypeAndMeta(t *testing.T) {
	original := validEvent("item-1", agent.AgentMessage{
		MessageID: "message-1",
		Content: []agent.ContentBlock{
			agent.TextBlock{Text: "hello"},
			agent.ImageBlock{Data: "aGVsbG8=", MIMEType: "image/png"},
		},
		Phase: "final",
	})
	original.Meta = map[string]any{
		"source":  "codex",
		"attempt": json.Number("2"),
		"raw":     map[string]any{"future": true},
	}
	require.NoError(t, original.Validate())

	encoded, err := json.Marshal(original)
	require.NoError(t, err)

	var envelope map[string]any
	require.NoError(t, json.Unmarshal(encoded, &envelope))
	assert.Equal(t, string(agent.EventAgentMessage), envelope["type"])
	assert.Equal(t, validEventID, envelope["eventId"])
	assert.Equal(t, "item-1", envelope["itemId"])
	assert.Contains(t, envelope, "_meta")

	var decoded agent.Event
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, original.EventID, decoded.EventID)
	assert.Equal(t, original.SessionID, decoded.SessionID)
	assert.True(t, original.Time.Equal(decoded.Time))
	assert.Equal(t, original.ItemID, decoded.ItemID)
	assert.Equal(t, original.Type(), decoded.Type())
	assert.Equal(t, original.Payload, decoded.Payload)
	assert.Equal(t, original.Meta, decoded.Meta)
	require.NoError(t, decoded.Validate())
}

func TestEventJSONRejectsUnknownOrMismatchedPayloadType(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "unknown type",
			raw: `{
				"eventId":"01234567-89ab-4cde-8fab-0123456789ab",
				"sessionId":"session-1",
				"time":"2026-11-07T09:00:00Z",
				"type":"agent.video",
				"payload":{"id":"video-1"}
			}`,
		},
		{
			name: "type and payload shape mismatch",
			raw: `{
				"eventId":"01234567-89ab-4cde-8fab-0123456789ab",
				"sessionId":"session-1",
				"time":"2026-11-07T09:00:00Z",
				"itemId":"item-1",
				"type":"agent.message",
				"payload":{"id":"tool-1","kind":"command","rawInput":{}}
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var event agent.Event
			require.Error(t, json.Unmarshal([]byte(tt.raw), &event))
		})
	}
}

func TestEventJSONRejectsMissingOrNullRequiredPayloadFields(t *testing.T) {
	tests := []struct {
		name      string
		eventType agent.EventType
		itemID    string
		payload   string
	}{
		{
			name:      "null payload",
			eventType: agent.EventSessionStateUpdate,
			payload:   `null`,
		},
		{
			name:      "state update missing state",
			eventType: agent.EventSessionStateUpdate,
			payload:   `{"previousState":"idle"}`,
		},
		{
			name:      "state update missing previous state",
			eventType: agent.EventSessionStateUpdate,
			payload:   `{"state":"running"}`,
		},
		{
			name:      "usage update missing used",
			eventType: agent.EventSessionUsageUpdate,
			payload:   `{"size":100}`,
		},
		{
			name:      "usage update null used",
			eventType: agent.EventSessionUsageUpdate,
			payload:   `{"used":null,"size":100}`,
		},
		{
			name:      "usage update missing size",
			eventType: agent.EventSessionUsageUpdate,
			payload:   `{"used":10}`,
		},
		{
			name:      "tool call missing raw input",
			eventType: agent.EventAgentToolCall,
			itemID:    "tool-1",
			payload:   `{"id":"tool-1","kind":"command"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var event agent.Event

			err := json.Unmarshal(eventJSONForPayload(t, tt.eventType, tt.itemID, tt.payload), &event)

			require.Error(t, err)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
		})
	}
}

func TestEventJSONAllowsExplicitNullRequiredDynamicMembers(t *testing.T) {
	tests := []struct {
		name      string
		eventType agent.EventType
		itemID    string
		payload   string
		check     func(*testing.T, agent.Payload)
	}{
		{
			name:      "state update unknown values",
			eventType: agent.EventSessionStateUpdate,
			payload:   `{"state":null,"previousState":null}`,
			check: func(t *testing.T, payload agent.Payload) {
				state := payload.(agent.StateUpdatePayload)
				assert.Nil(t, state.State)
				assert.Nil(t, state.PreviousState)
			},
		},
		{
			name:      "tool call null raw input",
			eventType: agent.EventAgentToolCall,
			itemID:    "tool-1",
			payload:   `{"id":"tool-1","kind":"command","rawInput":null}`,
			check: func(t *testing.T, payload agent.Payload) {
				call := payload.(agent.ToolCallPayload)
				assert.Nil(t, call.RawInput)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := eventJSONForPayload(t, tt.eventType, tt.itemID, tt.payload)
			var event agent.Event

			require.NoError(t, json.Unmarshal(encoded, &event))
			tt.check(t, event.Payload)
		})
	}
}

func TestToolPayloadJSONPreservesExplicitNullAndEmptyContent(t *testing.T) {
	var null any
	explicitEmpty := []agent.ContentBlock{}

	tests := []struct {
		name    string
		payload agent.Payload
		check   func(*testing.T, agent.Payload)
	}{
		{
			name: "tool call",
			payload: agent.ToolCallPayload{
				ID:        "tool-1",
				Kind:      "command",
				RawInput:  nil,
				RawOutput: &null,
				Content:   explicitEmpty,
			},
			check: func(t *testing.T, payload agent.Payload) {
				call := payload.(agent.ToolCallPayload)
				require.NotNil(t, call.RawOutput)
				assert.Nil(t, *call.RawOutput)
				require.NotNil(t, call.Content)
				assert.Empty(t, call.Content)
			},
		},
		{
			name: "tool result",
			payload: agent.ToolResultPayload{
				ID:        "tool-1",
				Kind:      "command",
				Status:    "completed",
				RawInput:  &null,
				RawOutput: &null,
				Content:   explicitEmpty,
			},
			check: func(t *testing.T, payload agent.Payload) {
				result := payload.(agent.ToolResultPayload)
				require.NotNil(t, result.RawInput)
				assert.Nil(t, *result.RawInput)
				require.NotNil(t, result.RawOutput)
				assert.Nil(t, *result.RawOutput)
				require.NotNil(t, result.Content)
				assert.Empty(t, result.Content)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := validEvent("tool-1", tt.payload)
			encoded, err := json.Marshal(original)
			require.NoError(t, err)

			var envelope struct {
				Payload map[string]json.RawMessage `json:"payload"`
			}
			require.NoError(t, json.Unmarshal(encoded, &envelope))
			assert.Contains(t, envelope.Payload, "rawOutput")
			assert.JSONEq(t, `null`, string(envelope.Payload["rawOutput"]))
			assert.Contains(t, envelope.Payload, "content")
			assert.JSONEq(t, `[]`, string(envelope.Payload["content"]))

			var decoded agent.Event
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			tt.check(t, decoded.Payload)
		})
	}
}

func TestEventJSONRoundTripsEveryPayloadBranch(t *testing.T) {
	running := agent.StateRunning
	title := "Session title"
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	payloads := []struct {
		name   string
		itemID string
		value  agent.Payload
	}{
		{
			name:   "user message",
			itemID: "item-1",
			value:  agent.UserMessage{MessageID: "message-1", Content: textContent("hello")},
		},
		{
			name:   "agent message",
			itemID: "item-2",
			value:  agent.AgentMessage{MessageID: "message-2", Content: textContent("hello")},
		},
		{
			name:   "agent thinking",
			itemID: "item-3",
			value:  agent.AgentThinking{MessageID: "message-3", Content: textContent("thinking")},
		},
		{
			name:   "tool call",
			itemID: "tool-1",
			value: agent.ToolCallPayload{
				ID:       "tool-1",
				Kind:     "command",
				RawInput: map[string]any{"command": "go test"},
				Content:  textContent("running"),
				Status:   "in_progress",
			},
		},
		{
			name:   "tool result",
			itemID: "tool-1",
			value: agent.ToolResultPayload{
				ID:        "tool-1",
				Kind:      "command",
				RawInput:  ptr[any](map[string]any{"command": "go test"}),
				RawOutput: ptr[any]("ok"),
				Content:   textContent("done"),
				Status:    "completed",
			},
		},
		{
			name:   "message chunk",
			itemID: "item-4",
			value:  agent.MessageChunk{MessageID: "message-4", Content: textContent("chunk")},
		},
		{
			name:   "thinking chunk",
			itemID: "item-5",
			value:  agent.ThinkingChunk{MessageID: "message-5", Content: textContent("chunk")},
		},
		{
			name:   "tool chunk",
			itemID: "tool-2",
			value:  agent.ToolChunkPayload{ID: "tool-2", Content: textContent("chunk")},
		},
		{
			name:  "state update",
			value: agent.StateUpdatePayload{State: &running},
		},
		{
			name:  "session error",
			value: agent.SessionErrorPayload{Message: "failed", Reason: "native"},
		},
		{
			name:  "info update",
			value: agent.InfoUpdatePayload{Title: &title, UpdatedAt: &now},
		},
		{
			name: "config update",
			value: agent.ConfigOptionUpdatePayload{
				ConfigOptions: []agent.ConfigOption{
					{
						ID:           "model",
						Name:         "Model",
						Type:         "select",
						CurrentValue: "gpt-6",
						Options: []agent.ConfigOptionValue{
							{Value: "gpt-6", Name: "GPT-6"},
						},
					},
				},
			},
		},
		{
			name: "commands update",
			value: agent.AvailableCommandsUpdatePayload{
				AvailableCommands: []agent.AvailableCommand{{Name: "help", Description: "Show help"}},
			},
		},
		{
			name: "usage update",
			value: agent.UsageUpdatePayload{
				Used: 10,
				Size: 100,
				Cost: &agent.UsageCost{Amount: json.Number("0.25"), Currency: "USD"},
			},
		},
		{
			name: "interaction requested",
			value: agent.InteractionRequestedPayload{
				Interaction: "interaction-1",
				Request: agent.InteractionRequest{
					Kind:       agent.InteractionPermission,
					ToolCallID: "tool-1",
					Options: []agent.PermissionOption{
						{OptionID: "allow", Name: "Allow", Kind: agent.PermissionAllowOnce},
					},
					Content: textContent("run?"),
				},
				Blocking: true,
			},
		},
		{
			name: "interaction closed",
			value: agent.InteractionClosedPayload{
				Interaction: "interaction-1",
				Outcome:     agent.InteractionAnswered,
			},
		},
	}

	for _, tc := range payloads {
		t.Run(tc.name, func(t *testing.T) {
			original := validEvent(tc.itemID, tc.value)
			encoded, err := json.Marshal(original)
			require.NoError(t, err)

			var decoded agent.Event
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			assert.Equal(t, original.Type(), decoded.Type())
			assert.Equal(t, original.Payload, decoded.Payload)
		})
	}
}

func textContent(text string) []agent.ContentBlock {
	return []agent.ContentBlock{agent.TextBlock{Text: text}}
}

func validEvent(itemID string, payload agent.Payload) agent.Event {
	return agent.Event{
		EventID:   validEventID,
		SessionID: "session-1",
		Time:      time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC),
		ItemID:    itemID,
		Payload:   payload,
	}
}

func withEvent(event agent.Event, mutate func(*agent.Event)) agent.Event {
	mutate(&event)
	return event
}

func eventJSONForPayload(
	t *testing.T,
	eventType agent.EventType,
	itemID string,
	payload string,
) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"eventId":   validEventID,
		"sessionId": "session-1",
		"time":      "2026-11-07T09:00:00Z",
		"itemId":    itemID,
		"type":      eventType,
		"payload":   json.RawMessage(payload),
	})
	require.NoError(t, err)
	return data
}

func ptr[T any](value T) *T {
	return new(value)
}
