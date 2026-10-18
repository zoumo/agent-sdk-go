// SPDX-License-Identifier: Apache-2.0

package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// FieldState overrides JSON presence without changing the compatible scalar API.
// Default preserves decoded absence/null; new omitempty zero fields are absent.
// Present emits the typed value (including false/0); Null emits null; Absent omits.
type FieldState uint8

const (
	Default FieldState = iota
	Present
	Null
	Absent
)

// objectJSON retains unknown fields and unchanged null/absence. Typed edits win
// over Raw. Raw is a baseline, never a cached serialization of the typed view.
func objectJSON(v any, raw json.RawMessage, states map[string]FieldState) ([]byte, error) {
	out := map[string]json.RawMessage{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		if out == nil {
			return nil, fmt.Errorf("expected JSON object")
		}
	}
	rv := reflect.ValueOf(v)
	rt := rv.Type()
	for i := 0; i < rv.NumField(); i++ {
		tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		f := rv.Field(i)
		state := states[tag]
		switch state {
		case Absent:
			delete(out, tag)
			continue
		case Null:
			out[tag] = json.RawMessage("null")
			continue
		case Default, Present:
		default:
			return nil, fmt.Errorf("invalid field state for %s", tag)
		}
		if state == Default {
			if old, ok := out[tag]; ok {
				original := reflect.New(f.Type())
				if err := json.Unmarshal(old, original.Interface()); err == nil && reflect.DeepEqual(original.Elem().Interface(), f.Interface()) {
					continue
				}
			} else if f.IsZero() && (len(raw) > 0 || strings.Contains(rt.Field(i).Tag.Get("json"), "omitempty")) {
				continue
			}
		}
		b, err := json.Marshal(f.Interface())
		if err != nil {
			return nil, err
		}
		out[tag] = b
	}
	return json.Marshal(out)
}
func objectOnly(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' || !json.Valid(data) {
		return fmt.Errorf("expected JSON object")
	}
	return nil
}

func (c *Content) UnmarshalJSON(data []byte) error {
	*c = Content{}
	data = bytes.TrimSpace(data)
	var next Content
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		next.Text = &s
	} else if len(data) > 0 && data[0] == '[' {
		if err := json.Unmarshal(data, &next.Blocks); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("message content must be a string or array")
	}
	next.Raw = bytes.Clone(data)
	*c = next
	return nil
}
func (c Content) MarshalJSON() ([]byte, error) {
	if c.Text != nil {
		if c.Blocks != nil {
			return nil, fmt.Errorf("content has both text and blocks")
		}
		return json.Marshal(*c.Text)
	}
	if c.Blocks != nil {
		return json.Marshal(c.Blocks)
	}
	// A zero Content constructs an empty block array. Raw-only construction is
	// supported; typed content always takes precedence once supplied.
	if len(c.Raw) > 0 {
		var parsed Content
		if err := json.Unmarshal(c.Raw, &parsed); err != nil {
			return nil, err
		}
		return parsed.MarshalJSON()
	}
	return []byte("[]"), nil
}

func (b *ContentBlock) UnmarshalJSON(data []byte) error {
	*b = ContentBlock{}
	if err := objectOnly(data); err != nil {
		return err
	}
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return err
	}
	if head.Type == "" {
		return fmt.Errorf("content block has no type")
	}
	next := ContentBlock{Type: head.Type, Raw: bytes.Clone(data)}
	var dst any
	switch head.Type {
	case "text":
		next.Text = new(TextBlock)
		dst = next.Text
	case "thinking":
		next.Thinking = new(ThinkingBlock)
		dst = next.Thinking
	case "redacted_thinking":
		next.RedactedThinking = new(RedactedThinkingBlock)
		dst = next.RedactedThinking
	case "tool_use":
		next.ToolUse = new(ToolUseBlock)
		dst = next.ToolUse
	case "tool_result":
		next.ToolResult = new(ToolResultBlock)
		dst = next.ToolResult
	case "server_tool_use":
		next.ServerToolUse = new(ServerToolUseBlock)
		dst = next.ServerToolUse
	case "web_search_tool_result", "web_fetch_tool_result", "code_execution_tool_result", "bash_code_execution_tool_result", "text_editor_code_execution_tool_result", "tool_search_tool_result", "advisor_tool_result":
		next.ServerToolResult = new(ServerToolResultBlock)
		dst = next.ServerToolResult
	}
	if dst != nil {
		if err := json.Unmarshal(data, dst); err != nil {
			return err
		}
	}
	if err := validateBlock(next); err != nil {
		return err
	}
	*b = next
	return nil
}
func (b ContentBlock) MarshalJSON() ([]byte, error) {
	if err := validateBlock(b); err != nil {
		return nil, err
	}
	var dst any
	var kind string
	count := 0
	choose := func(ok bool, v any, k string) {
		if ok {
			count++
			dst = v
			kind = k
		}
	}
	choose(b.Text != nil, b.Text, "text")
	choose(b.Thinking != nil, b.Thinking, "thinking")
	choose(b.RedactedThinking != nil, b.RedactedThinking, "redacted_thinking")
	choose(b.ToolUse != nil, b.ToolUse, "tool_use")
	choose(b.ToolResult != nil, b.ToolResult, "tool_result")
	choose(b.ServerToolUse != nil, b.ServerToolUse, "server_tool_use")
	choose(b.ServerToolResult != nil, b.ServerToolResult, b.Type)
	if count > 1 {
		return nil, fmt.Errorf("content block has multiple branches")
	}
	if b.Type == "" || count == 1 && b.Type != kind {
		return nil, fmt.Errorf("content block discriminator disagrees with branch")
	}
	if count == 0 {
		var parsed ContentBlock
		if err := json.Unmarshal(b.Raw, &parsed); err != nil {
			return nil, err
		}
		if parsed.Type != b.Type {
			return nil, fmt.Errorf("raw content type mismatch")
		}
		if parsed.Text != nil || parsed.Thinking != nil || parsed.ToolUse != nil || parsed.ToolResult != nil || parsed.ServerToolUse != nil || parsed.ServerToolResult != nil || parsed.RedactedThinking != nil {
			return nil, fmt.Errorf("known block requires typed branch")
		}
		return bytes.Clone(b.Raw), nil
	}
	if b.ServerToolResult != nil && !isServerResult(b.Type) {
		return nil, fmt.Errorf("invalid server result type")
	}
	data, err := json.Marshal(dst)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err = json.Unmarshal(b.Raw, &m); len(b.Raw) > 0 && err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	rv := reflect.TypeOf(dst).Elem()
	for i := 0; i < rv.NumField(); i++ {
		key := strings.Split(rv.Field(i).Tag.Get("json"), ",")[0]
		if key != "" && key != "-" {
			delete(m, key)
		}
	}
	for k, v := range fields {
		m[k] = v
	}
	m["type"], _ = json.Marshal(b.Type)
	return json.Marshal(m)
}
func isServerResult(t string) bool {
	switch t {
	case "web_search_tool_result", "web_fetch_tool_result", "code_execution_tool_result", "bash_code_execution_tool_result", "text_editor_code_execution_tool_result", "tool_search_tool_result", "advisor_tool_result":
		return true
	}
	return false
}

func (e *Event) UnmarshalJSON(data []byte) error {
	*e = Event{}
	next, err := ParseEvent(data)
	if err != nil {
		return err
	}
	*e = next
	return nil
}
func (e Event) MarshalJSON() ([]byte, error) {
	if e.Type == "" {
		return nil, fmt.Errorf("event has no type")
	}
	var branch any
	count := 0
	kind := ""
	choose := func(ok bool, v any, k string) {
		if ok {
			count++
			branch = v
			kind = k
		}
	}
	choose(e.System != nil, e.System, "system")
	choose(e.Assistant != nil, e.Assistant, "assistant")
	choose(e.User != nil, e.User, "user")
	choose(e.Result != nil, e.Result, "result")
	choose(e.Stream != nil, e.Stream, "stream_event")
	choose(e.Control != nil, e.Control, "control_request")
	choose(e.Response != nil, e.Response, "control_response")
	choose(e.Cancel != nil, e.Cancel, "control_cancel_request")
	choose(e.RateLimit != nil, e.RateLimit, "rate_limit_event")
	choose(e.Reset != nil, e.Reset, "conversation_reset")
	if count > 1 || count == 1 && kind != e.Type {
		return nil, fmt.Errorf("event discriminator disagrees with branch")
	}
	if count == 0 {
		switch e.Type {
		case "assistant", "user", "system", "result", "stream_event", "control_request", "control_response", "control_cancel_request", "rate_limit_event", "conversation_reset":
			return nil, fmt.Errorf("known event requires typed branch")
		}
	}
	type wire Event
	data, err := objectJSON(wire(e), e.Raw, e.Fields)
	if err != nil {
		return nil, err
	}
	var out map[string]json.RawMessage
	_ = json.Unmarshal(data, &out)
	if branch != nil {
		b, err := json.Marshal(branch)
		if err != nil {
			return nil, err
		}
		if kind == "assistant" || kind == "user" {
			out["message"] = b
		} else {
			var fields, baseline map[string]json.RawMessage
			if err = json.Unmarshal(b, &fields); err != nil {
				return nil, err
			}
			_ = json.Unmarshal(e.Raw, &baseline)
			for k, v := range fields { // Envelope edits win; unchanged duplicate metadata does not mask branch edits.
				if e.Fields[k] != Default {
					continue
				}
				if old, ok := out[k]; !ok || jsonEqual(old, baseline[k]) {
					out[k] = v
				}
			}
			// Honor only explicit branch omissions; raw control views do not
			// model every envelope field and must retain unknown data.
			stateField := reflect.ValueOf(branch).Elem().FieldByName("Fields")
			if stateField.IsValid() {
				for k, state := range stateField.Interface().(map[string]FieldState) {
					if state == Absent && e.Fields[k] == Default {
						delete(out, k)
					}
				}
			}
		}
	}
	out["type"], _ = json.Marshal(e.Type)
	return json.Marshal(out)
}
func jsonEqual(a, b []byte) bool {
	var x, y any
	da, db := json.NewDecoder(bytes.NewReader(a)), json.NewDecoder(bytes.NewReader(b))
	da.UseNumber()
	db.UseNumber()
	if da.Decode(&x) != nil || db.Decode(&y) != nil {
		return bytes.Equal(a, b)
	}
	return reflect.DeepEqual(x, y)
}

func (v *SystemEvent) UnmarshalJSON(data []byte) error {
	*v = SystemEvent{}
	if err := objectOnly(data); err != nil {
		return err
	}
	type wire SystemEvent
	var next wire
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = SystemEvent(next)
	return nil
}
func (v SystemEvent) MarshalJSON() ([]byte, error) {
	type wire SystemEvent
	return objectJSON(wire(v), v.Raw, v.Fields)
}

func (v *Message) UnmarshalJSON(data []byte) error {
	*v = Message{}
	if err := objectOnly(data); err != nil {
		return err
	}
	type wire Message
	var next wire
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = Message(next)
	return nil
}
func (v Message) MarshalJSON() ([]byte, error) {
	type wire Message
	return objectJSON(wire(v), v.Raw, v.Fields)
}

func (v *ResultEvent) UnmarshalJSON(data []byte) error {
	*v = ResultEvent{}
	if err := objectOnly(data); err != nil {
		return err
	}
	type wire ResultEvent
	var next wire
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = ResultEvent(next)
	return nil
}
func (v ResultEvent) MarshalJSON() ([]byte, error) {
	type wire ResultEvent
	return objectJSON(wire(v), v.Raw, v.Fields)
}

func (v *TextBlock) UnmarshalJSON(data []byte) error {
	*v = TextBlock{}
	if err := objectOnly(data); err != nil {
		return err
	}
	type wire TextBlock
	var next wire
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = TextBlock(next)
	return nil
}
func (v TextBlock) MarshalJSON() ([]byte, error) {
	type wire TextBlock
	return objectJSON(wire(v), v.Raw, v.Fields)
}

func (v *ThinkingBlock) UnmarshalJSON(data []byte) error {
	*v = ThinkingBlock{}
	if err := objectOnly(data); err != nil {
		return err
	}
	type wire ThinkingBlock
	var next wire
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = ThinkingBlock(next)
	return nil
}
func (v ThinkingBlock) MarshalJSON() ([]byte, error) {
	type wire ThinkingBlock
	return objectJSON(wire(v), v.Raw, v.Fields)
}

func (v *ToolUseBlock) UnmarshalJSON(data []byte) error {
	*v = ToolUseBlock{}
	if err := objectOnly(data); err != nil {
		return err
	}
	type wire ToolUseBlock
	var next wire
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = ToolUseBlock(next)
	return nil
}
func (v ToolUseBlock) MarshalJSON() ([]byte, error) {
	type wire ToolUseBlock
	return objectJSON(wire(v), v.Raw, v.Fields)
}

func (v *ToolResultBlock) UnmarshalJSON(data []byte) error {
	*v = ToolResultBlock{}
	if err := objectOnly(data); err != nil {
		return err
	}
	type wire ToolResultBlock
	var next wire
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = ToolResultBlock(next)
	return nil
}
func (v ToolResultBlock) MarshalJSON() ([]byte, error) {
	type wire ToolResultBlock
	return objectJSON(wire(v), v.Raw, v.Fields)
}

func (v *ServerToolUseBlock) UnmarshalJSON(data []byte) error {
	*v = ServerToolUseBlock{}
	if err := objectOnly(data); err != nil {
		return err
	}
	type wire ServerToolUseBlock
	var next wire
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = ServerToolUseBlock(next)
	return nil
}
func (v ServerToolUseBlock) MarshalJSON() ([]byte, error) {
	type wire ServerToolUseBlock
	return objectJSON(wire(v), v.Raw, v.Fields)
}

func (v *ServerToolResultBlock) UnmarshalJSON(data []byte) error {
	*v = ServerToolResultBlock{}
	if err := objectOnly(data); err != nil {
		return err
	}
	type wire ServerToolResultBlock
	var next wire
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = ServerToolResultBlock(next)
	return nil
}
func (v ServerToolResultBlock) MarshalJSON() ([]byte, error) {
	type wire ServerToolResultBlock
	return objectJSON(wire(v), v.Raw, v.Fields)
}

func (v *RedactedThinkingBlock) UnmarshalJSON(data []byte) error {
	*v = RedactedThinkingBlock{}
	if err := objectOnly(data); err != nil {
		return err
	}
	type wire RedactedThinkingBlock
	var next wire
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = RedactedThinkingBlock(next)
	return nil
}
func (v RedactedThinkingBlock) MarshalJSON() ([]byte, error) {
	type wire RedactedThinkingBlock
	return objectJSON(wire(v), v.Raw, v.Fields)
}

func (v *ModelUsage) UnmarshalJSON(data []byte) error {
	*v = ModelUsage{}
	if err := objectOnly(data); err != nil {
		return err
	}
	type wire ModelUsage
	var next wire
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = ModelUsage(next)
	return nil
}
func (v ModelUsage) MarshalJSON() ([]byte, error) {
	type wire ModelUsage
	return objectJSON(wire(v), v.Raw, v.Fields)
}

func (v *MessageOrigin) UnmarshalJSON(data []byte) error {
	*v = MessageOrigin{}
	if err := objectOnly(data); err != nil {
		return err
	}
	type wire MessageOrigin
	var next wire
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = MessageOrigin(next)
	return nil
}
func (v MessageOrigin) MarshalJSON() ([]byte, error) {
	type wire MessageOrigin
	return objectJSON(wire(v), v.Raw, v.Fields)
}

func (v *RateLimitEvent) UnmarshalJSON(data []byte) error {
	*v = RateLimitEvent{}
	if err := objectOnly(data); err != nil {
		return err
	}
	type wire RateLimitEvent
	var next wire
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = RateLimitEvent(next)
	return nil
}
func (v RateLimitEvent) MarshalJSON() ([]byte, error) {
	type wire RateLimitEvent
	return objectJSON(wire(v), v.Raw, v.Fields)
}

func (v *RateLimitInfo) UnmarshalJSON(data []byte) error {
	*v = RateLimitInfo{}
	if err := objectOnly(data); err != nil {
		return err
	}
	type wire RateLimitInfo
	var next wire
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = RateLimitInfo(next)
	return nil
}
func (v RateLimitInfo) MarshalJSON() ([]byte, error) {
	type wire RateLimitInfo
	return objectJSON(wire(v), v.Raw, v.Fields)
}

func (v *ConversationResetEvent) UnmarshalJSON(data []byte) error {
	*v = ConversationResetEvent{}
	if err := objectOnly(data); err != nil {
		return err
	}
	type wire ConversationResetEvent
	var next wire
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = ConversationResetEvent(next)
	return nil
}
func (v ConversationResetEvent) MarshalJSON() ([]byte, error) {
	type wire ConversationResetEvent
	return objectJSON(wire(v), v.Raw, v.Fields)
}

func validateBlock(b ContentBlock) error {
	var input json.RawMessage
	if b.ToolUse != nil {
		input = b.ToolUse.Input
	}
	if b.ServerToolUse != nil {
		input = b.ServerToolUse.Input
	}
	if len(input) > 0 {
		if err := objectOnly(input); err != nil {
			return fmt.Errorf("tool input: %w", err)
		}
	}
	if b.ToolResult != nil && len(b.ToolResult.Content) > 0 {
		c := bytes.TrimSpace(b.ToolResult.Content)
		if !json.Valid(c) || !(bytes.Equal(c, []byte("null")) || c[0] == '"' || c[0] == '[') {
			return fmt.Errorf("tool result content must be string, array or null")
		}
		if c[0] == '[' {
			var blocks []json.RawMessage
			_ = json.Unmarshal(c, &blocks)
			for _, block := range blocks {
				if err := objectOnly(block); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (v *StreamEvent) UnmarshalJSON(data []byte) error {
	*v = StreamEvent{}
	if err := objectOnly(data); err != nil {
		return err
	}
	type wire StreamEvent
	var next wire
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	if len(next.Event) > 0 && !bytes.Equal(bytes.TrimSpace(next.Event), []byte("null")) {
		next.Delta = new(StreamDelta)
		if err := json.Unmarshal(next.Event, next.Delta); err != nil {
			return err
		}
	}
	next.Raw = bytes.Clone(data)
	*v = StreamEvent(next)
	return nil
}
func (v StreamEvent) MarshalJSON() ([]byte, error) {
	type wire StreamEvent
	if v.Delta != nil {
		b, err := objectJSON(*v.Delta, v.Event, nil)
		if err != nil {
			return nil, err
		}
		v.Event = b
	}
	return objectJSON(wire(v), v.Raw, v.Fields)
}
