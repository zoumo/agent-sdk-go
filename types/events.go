// SPDX-License-Identifier: Apache-2.0

package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// Event retains the complete JSON frame (excluding its newline) alongside typed
// views. Unknown message kinds, fields and content blocks remain in Raw.
// Raw values belong to the caller and are never reused by the reader.
type Event struct {
	// AgentID is the native envelope identity, not a session or tool-use ID.
	AgentID         *string                 `json:"agent_id,omitempty"`
	SubagentType    *string                 `json:"subagent_type,omitempty"`
	Type            string                  `json:"type"`
	Subtype         string                  `json:"subtype,omitempty"`
	SessionID       string                  `json:"session_id,omitempty"`
	UUID            string                  `json:"uuid,omitempty"`
	ParentToolUseID *string                 `json:"parent_tool_use_id,omitempty"`
	Error           *string                 `json:"error,omitempty"`
	ToolUseResult   json.RawMessage         `json:"tool_use_result,omitempty"`
	Origin          *MessageOrigin          `json:"origin,omitempty"`
	RequestID       *string                 `json:"request_id,omitempty"`
	UserMessageUUID *string                 `json:"user_message_uuid,omitempty"`
	IsSynthetic     *bool                   `json:"isSynthetic,omitempty"`
	IsReplay        *bool                   `json:"isReplay,omitempty"`
	Fields          map[string]FieldState   `json:"-"`
	Raw             json.RawMessage         `json:"-"`
	System          *SystemEvent            `json:"-"`
	Assistant       *Message                `json:"-"`
	User            *Message                `json:"-"`
	Result          *ResultEvent            `json:"-"`
	Stream          *StreamEvent            `json:"-"`
	Control         *ControlRequest         `json:"-"`
	Response        *ControlResponse        `json:"-"`
	Cancel          *ControlCancelRequest   `json:"-"`
	RateLimit       *RateLimitEvent         `json:"-"`
	Reset           *ConversationResetEvent `json:"-"`
}

// SystemEvent includes session initialization and other system notifications.
type SystemEvent struct {
	UUID              string          `json:"uuid,omitempty"`
	Agents            []string        `json:"agents,omitempty"`
	APIKeySource      string          `json:"apiKeySource,omitempty"`
	ClaudeCodeVersion string          `json:"claude_code_version,omitempty"`
	PermissionMode    string          `json:"permissionMode,omitempty"`
	MCPServers        json.RawMessage `json:"mcp_servers,omitempty"`
	SlashCommands     []string        `json:"slash_commands,omitempty"`
	OutputStyle       string          `json:"output_style,omitempty"`
	Skills            []string        `json:"skills,omitempty"`
	Plugins           json.RawMessage `json:"plugins,omitempty"`
	Capabilities      []string        `json:"capabilities,omitempty"`
	RunID             *string         `json:"run_id,omitempty"`
	ParentTaskID      *string         `json:"parent_task_id,omitempty"`
	SpawnDepth        *int            `json:"spawn_depth,omitempty"`
	IsBackgrounded    *bool           `json:"is_backgrounded,omitempty"`
	TaskID            string          `json:"task_id,omitempty"`
	Description       string          `json:"description,omitempty"`
	ToolUseID         *string         `json:"tool_use_id,omitempty"`
	TaskType          *string         `json:"task_type,omitempty"`
	Status            string          `json:"status,omitempty"`
	OutputFile        string          `json:"output_file,omitempty"`
	Summary           string          `json:"summary,omitempty"`
	Usage             json.RawMessage `json:"usage,omitempty"`
	LastToolName      *string         `json:"last_tool_name,omitempty"`
	Patch             json.RawMessage `json:"patch,omitempty"`
	CompactMetadata   json.RawMessage `json:"compact_metadata,omitempty"`

	// State is the native session_state_changed state, not inferred from result.
	// Known states are idle, running and requires_action; future values survive.
	State *string `json:"state,omitempty"`
	// Tasks is a REPLACE level snapshot for background_tasks_changed. Keep each
	// entry raw to preserve unknown members and absent/null/value distinctions.
	Tasks []json.RawMessage `json:"tasks,omitempty"`

	Fields    map[string]FieldState `json:"-"`
	Subtype   string                `json:"subtype"`
	SessionID string                `json:"session_id"`
	CWD       string                `json:"cwd"`
	Model     string                `json:"model"`
	Tools     []string              `json:"tools"`
	Raw       json.RawMessage       `json:"-"`
}

// Message is the nested message of an assistant or user event. Envelope fields
// such as parent_tool_use_id are exposed on Event.
type Message struct {
	Type         string                `json:"type,omitempty"`
	StopSequence *string               `json:"stop_sequence,omitempty"`
	Fields       map[string]FieldState `json:"-"`
	ID           string                `json:"id,omitempty"`
	Role         string                `json:"role,omitempty"`
	Model        string                `json:"model,omitempty"`
	Content      Content               `json:"content"`
	StopReason   *string               `json:"stop_reason"`
	Usage        json.RawMessage       `json:"usage"`
	Raw          json.RawMessage       `json:"-"`
}

// Content is either a text string or an ordered array of blocks.
type Content struct {
	Text   *string
	Blocks []ContentBlock
	Raw    json.RawMessage
}

type ContentBlock struct {
	Type             string
	Raw              json.RawMessage
	Text             *TextBlock
	Thinking         *ThinkingBlock
	ToolUse          *ToolUseBlock
	ToolResult       *ToolResultBlock
	ServerToolUse    *ServerToolUseBlock
	ServerToolResult *ServerToolResultBlock
	RedactedThinking *RedactedThinkingBlock
}
type TextBlock struct {
	Raw    json.RawMessage       `json:"-"`
	Fields map[string]FieldState `json:"-"`
	Text   string                `json:"text"`
}
type ThinkingBlock struct {
	Raw       json.RawMessage       `json:"-"`
	Fields    map[string]FieldState `json:"-"`
	Thinking  string                `json:"thinking"`
	Signature string                `json:"signature"`
}
type ToolUseBlock struct {
	Raw    json.RawMessage       `json:"-"`
	Fields map[string]FieldState `json:"-"`
	ID     string                `json:"id"`
	Name   string                `json:"name"`
	Input  json.RawMessage       `json:"input,omitempty"`
}
type ToolResultBlock struct {
	Raw       json.RawMessage       `json:"-"`
	Fields    map[string]FieldState `json:"-"`
	ToolUseID string                `json:"tool_use_id"`
	Content   json.RawMessage       `json:"content"`
	IsError   bool                  `json:"is_error"`
}

type ResultEvent struct {
	UUID              string                `json:"uuid,omitempty"`
	StopReason        *string               `json:"stop_reason,omitempty"`
	ModelUsage        map[string]ModelUsage `json:"modelUsage,omitempty"`
	PermissionDenials json.RawMessage       `json:"permission_denials,omitempty"`
	DeferredToolUse   *ToolUseBlock         `json:"deferred_tool_use,omitempty"`
	APIErrorStatus    *int                  `json:"api_error_status,omitempty"`
	Origin            *MessageOrigin        `json:"origin,omitempty"`

	Fields           map[string]FieldState `json:"-"`
	Subtype          string                `json:"subtype"`
	SessionID        string                `json:"session_id"`
	IsError          bool                  `json:"is_error"`
	Result           string                `json:"result,omitempty"`
	Errors           []string              `json:"errors,omitempty"`
	NumTurns         int                   `json:"num_turns"`
	DurationMS       int64                 `json:"duration_ms"`
	DurationAPIMS    int64                 `json:"duration_api_ms"`
	TotalCostUSD     *float64              `json:"total_cost_usd"`
	Usage            json.RawMessage       `json:"usage"`
	StructuredOutput json.RawMessage       `json:"structured_output"`
	TerminalReason   string                `json:"terminal_reason,omitempty"`
	Raw              json.RawMessage       `json:"-"`
}

// StreamEvent exposes the API event and common delta fields without discarding
// unknown API event shapes.
type StreamEvent struct {
	Fields          map[string]FieldState `json:"-"`
	Event           json.RawMessage       `json:"event"`
	ParentToolUseID *string               `json:"parent_tool_use_id"`
	Delta           *StreamDelta          `json:"-"`
	Raw             json.RawMessage       `json:"-"`
}
type StreamDelta struct {
	Type         string          `json:"type"`
	Index        *int            `json:"index"`
	Delta        json.RawMessage `json:"delta"`
	ContentBlock json.RawMessage `json:"content_block"`
}

// ControlRequest is an observable incoming control request. Configured
// PermissionHandlers separately receive the sole answerable permission handle;
// this raw event is not an answer authority. Unsupported requests get an error.
type ControlRequest struct {
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
	Subtype   string          `json:"-"`
}

// ControlCancelRequest reports the CLI abandoning an incoming request. It is
// observable even for an unknown or already completed request ID. Unknown
// fields remain in Raw. Observing cancellation does not retract an OS write
// that was already authorized.
type ControlCancelRequest struct {
	RequestID string          `json:"request_id"`
	Raw       json.RawMessage `json:"-"`
}

// ParseEvent decodes one complete JSON frame, excluding its newline. It retains
// unknown data in Raw and does not perform transport or permission processing.
func ParseEvent(frame []byte) (Event, error) {
	var event Event
	type eventWire Event
	if !utf8.Valid(frame) {
		return event, fmt.Errorf("frame is not valid UTF-8")
	}
	if err := json.Unmarshal(frame, (*eventWire)(&event)); err != nil {
		return event, err
	}
	if event.Type == "" {
		return event, fmt.Errorf("frame has no nonempty string type")
	}
	event.Raw = bytes.Clone(frame)
	var err error
	switch event.Type {
	case "system":
		event.System = new(SystemEvent)
		err = json.Unmarshal(frame, event.System)
		event.System.Raw = event.Raw
	case "assistant", "user":
		var envelope struct {
			Message json.RawMessage `json:"message"`
		}
		err = json.Unmarshal(frame, &envelope)
		if err != nil {
			break
		}
		if len(envelope.Message) == 0 || envelope.Message[0] != '{' {
			return event, fmt.Errorf("%s message must be an object", event.Type)
		}
		m := new(Message)
		err = json.Unmarshal(envelope.Message, m)
		m.Raw = envelope.Message
		if err == nil && len(m.Content.Raw) == 0 {
			err = fmt.Errorf("message has no content")
		}
		if event.Type == "assistant" {
			event.Assistant = m
		} else {
			event.User = m
		}
	case "result":
		event.Result = new(ResultEvent)
		err = json.Unmarshal(frame, event.Result)
		event.Result.Raw = event.Raw
	case "stream_event":
		event.Stream = new(StreamEvent)
		err = json.Unmarshal(frame, event.Stream)
		event.Stream.Raw = event.Raw
		if err == nil {
			event.Stream.Delta = new(StreamDelta)
			err = json.Unmarshal(event.Stream.Event, event.Stream.Delta)
		}
	case "rate_limit_event":
		event.RateLimit = new(RateLimitEvent)
		err = json.Unmarshal(frame, event.RateLimit)
	case "conversation_reset":
		event.Reset = new(ConversationResetEvent)
		err = json.Unmarshal(frame, event.Reset)
	case "control_response":
		event.Response = new(ControlResponse)
		err = json.Unmarshal(frame, event.Response)
	case "control_cancel_request":
		event.Cancel = new(ControlCancelRequest)
		err = json.Unmarshal(frame, event.Cancel)
		event.Cancel.Raw = event.Raw
		if err == nil && event.Cancel.RequestID == "" {
			err = fmt.Errorf("control cancellation has no request_id")
		}
	case "control_request":
		event.Control = new(ControlRequest)
		err = json.Unmarshal(frame, event.Control)
		if err == nil {
			var h struct {
				Subtype string `json:"subtype"`
			}
			err = json.Unmarshal(event.Control.Request, &h)
			event.Control.Subtype = h.Subtype
		}
		if err == nil && (event.Control.RequestID == "" || event.Control.Subtype == "") {
			err = fmt.Errorf("control request has no request_id or subtype")
		}
	}
	return event, err
}
