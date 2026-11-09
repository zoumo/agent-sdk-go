package agent

import (
	"encoding/json"
	"time"
)

type MessagePayload struct {
	MessageID string         `json:"messageId"`
	Content   []ContentBlock `json:"content"`
	Phase     string         `json:"phase,omitempty"`
}

type UserMessage MessagePayload

func (UserMessage) eventType() EventType { return EventUserMessage }

type AgentMessage MessagePayload

func (AgentMessage) eventType() EventType { return EventAgentMessage }

type AgentThinking MessagePayload

func (AgentThinking) eventType() EventType { return EventAgentThinking }

type MessageChunk MessagePayload

func (MessageChunk) eventType() EventType { return EventAgentMessageChunk }

type ThinkingChunk MessagePayload

func (ThinkingChunk) eventType() EventType { return EventAgentThinkingChunk }

type ToolOutput struct {
	Availability string      `json:"availability"`
	Completeness string      `json:"completeness"`
	Truncation   *Truncation `json:"truncation,omitempty"`
}

type Truncation struct {
	RetainedBytes int64  `json:"retainedBytes"`
	OmittedBytes  *int64 `json:"omittedBytes,omitempty"`
}

type ToolCallPayload struct {
	ID                  string         `json:"id"`
	Kind                ToolKind       `json:"kind"`
	ToolName            string         `json:"toolName"`
	Provider            ToolProvider   `json:"provider"`
	Title               string         `json:"title,omitempty"`
	Phase               string         `json:"phase,omitempty"`
	InputRepresentation string         `json:"inputRepresentation,omitempty"`
	RawInput            any            `json:"rawInput"`
	RawOutput           *any           `json:"rawOutput,omitempty"`
	Content             []ContentBlock `json:"content,omitempty"`
	ExitCode            *int32         `json:"exitCode,omitempty"`
	DurationMs          *float64       `json:"durationMs,omitempty"`
	Output              *ToolOutput    `json:"output,omitempty"`
	Status              string         `json:"status,omitempty"`
}

func (ToolCallPayload) eventType() EventType { return EventAgentToolCall }

func (p ToolCallPayload) MarshalJSON() ([]byte, error) {
	type payload ToolCallPayload
	return marshalToolContent(payload(p), p.Content)
}

type ToolResultPayload struct {
	ID                  string         `json:"id"`
	Kind                ToolKind       `json:"kind"`
	ToolName            string         `json:"toolName"`
	Provider            ToolProvider   `json:"provider"`
	Title               string         `json:"title,omitempty"`
	Phase               string         `json:"phase,omitempty"`
	InputRepresentation string         `json:"inputRepresentation,omitempty"`
	RawInput            *any           `json:"rawInput,omitempty"`
	RawOutput           *any           `json:"rawOutput,omitempty"`
	Content             []ContentBlock `json:"content,omitempty"`
	ExitCode            *int32         `json:"exitCode,omitempty"`
	DurationMs          *float64       `json:"durationMs,omitempty"`
	Output              *ToolOutput    `json:"output,omitempty"`
	Status              string         `json:"status"`
}

func (ToolResultPayload) eventType() EventType { return EventAgentToolResult }

func (p ToolResultPayload) MarshalJSON() ([]byte, error) {
	type payload ToolResultPayload
	return marshalToolContent(payload(p), p.Content)
}

type ToolChunkPayload struct {
	ID      string         `json:"id"`
	Content []ContentBlock `json:"content"`
}

func (ToolChunkPayload) eventType() EventType { return EventAgentToolCallChunk }

type SessionState string

const (
	StateIdle           SessionState = "idle"
	StateRunning        SessionState = "running"
	StateRequiresAction SessionState = "requires_action"
)

type ObservedError struct {
	Code    any    `json:"code,omitempty"`
	Message string `json:"message"`
}

type StateUpdatePayload struct {
	PreviousState *SessionState  `json:"previousState"`
	State         *SessionState  `json:"state"`
	StopReason    string         `json:"stopReason,omitempty"`
	Error         *ObservedError `json:"error,omitempty"`
}

func (StateUpdatePayload) eventType() EventType { return EventSessionStateUpdate }

type SessionErrorPayload struct {
	Message string `json:"message"`
	Reason  string `json:"reason,omitempty"`
}

func (SessionErrorPayload) eventType() EventType { return EventSessionError }

type InfoUpdatePayload struct {
	Title     *string    `json:"title,omitempty"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}

func (InfoUpdatePayload) eventType() EventType { return EventSessionInfoUpdate }

type ConfigOptionValue struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type ConfigOptionGroup struct {
	Group   string              `json:"group"`
	Name    string              `json:"name"`
	Options []ConfigOptionValue `json:"options"`
}

type ConfigOption struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Category     string `json:"category,omitempty"`
	Type         string `json:"type"`
	CurrentValue any    `json:"currentValue"`
	Options      any    `json:"options,omitempty"`
}

type ConfigOptionUpdatePayload struct {
	ConfigOptions []ConfigOption `json:"configOptions"`
}

func (ConfigOptionUpdatePayload) eventType() EventType {
	return EventSessionConfigOptionUpdate
}

type AvailableCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type AvailableCommandsUpdatePayload struct {
	AvailableCommands []AvailableCommand `json:"availableCommands"`
}

func (AvailableCommandsUpdatePayload) eventType() EventType {
	return EventSessionCommandsUpdate
}

type UsageCost struct {
	Amount   json.Number `json:"amount"`
	Currency string      `json:"currency"`
}

type UsageUpdatePayload struct {
	Used int64 `json:"used"`
	// Size is zero when the provider did not report a context-window size.
	Size int64      `json:"size"`
	Cost *UsageCost `json:"cost,omitempty"`
}

func (UsageUpdatePayload) eventType() EventType { return EventSessionUsageUpdate }

type InteractionRequestedPayload struct {
	Interaction InteractionRef     `json:"interaction"`
	Request     InteractionRequest `json:"request"`
	Blocking    bool               `json:"blocking"`
}

func (InteractionRequestedPayload) eventType() EventType {
	return EventInteractionRequested
}

type InteractionClosedPayload struct {
	Interaction InteractionRef     `json:"interaction"`
	Outcome     InteractionOutcome `json:"outcome"`
	Reason      string             `json:"reason,omitempty"`
}

func (InteractionClosedPayload) eventType() EventType {
	return EventInteractionClosed
}

func marshalToolContent(value any, content []ContentBlock) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil || content == nil {
		return data, err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return nil, err
	}
	members["content"], err = json.Marshal(content)
	if err != nil {
		return nil, err
	}
	return json.Marshal(members)
}
