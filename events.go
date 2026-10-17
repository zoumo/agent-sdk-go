// SPDX-License-Identifier: Apache-2.0

package claude

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
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	UUID      string          `json:"uuid,omitempty"`
	Raw       json.RawMessage `json:"-"`
	System    *SystemEvent    `json:"-"`
	Assistant *Message        `json:"-"`
	User      *Message        `json:"-"`
	Result    *ResultEvent    `json:"-"`
	Stream    *StreamEvent    `json:"-"`
	Control   *ControlRequest `json:"-"`
}

// SystemEvent includes session initialization and other system notifications.
type SystemEvent struct {
	Subtype   string          `json:"subtype"`
	SessionID string          `json:"session_id"`
	CWD       string          `json:"cwd"`
	Model     string          `json:"model"`
	Tools     []string        `json:"tools"`
	Raw       json.RawMessage `json:"-"`
}

// Message is the nested message of an assistant or user event. Envelope fields
// such as parent_tool_use_id remain in Event.Raw.
type Message struct {
	ID         string          `json:"id"`
	Role       string          `json:"role"`
	Model      string          `json:"model"`
	Content    Content         `json:"content"`
	StopReason *string         `json:"stop_reason"`
	Usage      json.RawMessage `json:"usage"`
	Raw        json.RawMessage `json:"-"`
}

// Content is either a text string or an ordered array of blocks.
type Content struct {
	Text   *string
	Blocks []ContentBlock
	Raw    json.RawMessage
}

func (c *Content) UnmarshalJSON(data []byte) error {
	c.Raw = bytes.Clone(data)
	if len(data) > 0 && data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		c.Text = &text
		return nil
	}
	if len(data) == 0 || data[0] != '[' {
		return fmt.Errorf("message content must be a string or array")
	}
	return json.Unmarshal(data, &c.Blocks)
}

type ContentBlock struct {
	Type       string
	Raw        json.RawMessage
	Text       *TextBlock
	Thinking   *ThinkingBlock
	ToolUse    *ToolUseBlock
	ToolResult *ToolResultBlock
}
type TextBlock struct {
	Text string `json:"text"`
}
type ThinkingBlock struct {
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
}
type ToolUseBlock struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}
type ToolResultBlock struct {
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

func (b *ContentBlock) UnmarshalJSON(data []byte) error {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return err
	}
	if head.Type == "" {
		return fmt.Errorf("content block has no type")
	}
	b.Type, b.Raw = head.Type, bytes.Clone(data)
	switch b.Type {
	case "text":
		b.Text = new(TextBlock)
		return json.Unmarshal(data, b.Text)
	case "thinking":
		b.Thinking = new(ThinkingBlock)
		return json.Unmarshal(data, b.Thinking)
	case "tool_use":
		b.ToolUse = new(ToolUseBlock)
		return json.Unmarshal(data, b.ToolUse)
	case "tool_result":
		b.ToolResult = new(ToolResultBlock)
		return json.Unmarshal(data, b.ToolResult)
	}
	return nil
}

type ResultEvent struct {
	Subtype          string          `json:"subtype"`
	SessionID        string          `json:"session_id"`
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	Errors           []string        `json:"errors"`
	NumTurns         int             `json:"num_turns"`
	DurationMS       int64           `json:"duration_ms"`
	DurationAPIMS    int64           `json:"duration_api_ms"`
	TotalCostUSD     *float64        `json:"total_cost_usd"`
	Usage            json.RawMessage `json:"usage"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	TerminalReason   string          `json:"terminal_reason"`
	Raw              json.RawMessage `json:"-"`
}

// StreamEvent exposes the API event and common delta fields without discarding
// unknown API event shapes.
type StreamEvent struct {
	Event           json.RawMessage `json:"event"`
	ParentToolUseID *string         `json:"parent_tool_use_id"`
	Delta           *StreamDelta    `json:"-"`
	Raw             json.RawMessage `json:"-"`
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

func parseEvent(frame []byte) (Event, error) {
	var event Event
	if !utf8.Valid(frame) {
		return event, fmt.Errorf("frame is not valid UTF-8")
	}
	if err := json.Unmarshal(frame, &event); err != nil {
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
