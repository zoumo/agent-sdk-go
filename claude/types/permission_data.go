// SPDX-License-Identifier: Apache-2.0

package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// PermissionMetadata is the detached nested can_use_tool request. It has no
// connection, cancellation state, or answer authority. Suggestions are data only.
type PermissionMetadata struct {
	Subtype               string                `json:"subtype"`
	ToolName              string                `json:"tool_name"`
	Input                 json.RawMessage       `json:"input"`
	ToolUseID             string                `json:"tool_use_id,omitempty"`
	AgentID               string                `json:"agent_id,omitempty"`
	PermissionSuggestions json.RawMessage       `json:"permission_suggestions,omitempty"`
	BlockedPath           *string               `json:"blocked_path,omitempty"`
	DecisionReason        string                `json:"decision_reason,omitempty"`
	Title                 string                `json:"title,omitempty"`
	DisplayName           string                `json:"display_name,omitempty"`
	Description           string                `json:"description,omitempty"`
	Raw                   json.RawMessage       `json:"-"`
	Fields                map[string]FieldState `json:"-"`
}

// PermissionMetadata decodes observable data only; it cannot create a handle.
func (v ControlRequest) PermissionMetadata() (PermissionMetadata, error) {
	var metadata PermissionMetadata
	err := json.Unmarshal(v.Request, &metadata)
	return metadata, err
}

// PermissionResult is the wire decision inside a success control response.
// Unlike PermissionAnswer (a runtime command), it never substitutes original
// input: absent updatedInput remains absent. Persistent updates are unsupported.
type PermissionResult struct {
	Behavior     PermissionBehavior    `json:"behavior"`
	UpdatedInput json.RawMessage       `json:"updatedInput,omitempty"`
	Message      string                `json:"message,omitempty"`
	Raw          json.RawMessage       `json:"-"`
	Fields       map[string]FieldState `json:"-"`
}

// ControlResponse is a complete envelope, with correlation in Response rather
// than at the top level. It is data, not a public control sending method.
type ControlResponse struct {
	Type     string                `json:"type"`
	Response ControlResponseData   `json:"response"`
	Raw      json.RawMessage       `json:"-"`
	Fields   map[string]FieldState `json:"-"`
}

type ControlResponseData struct {
	Subtype   string                `json:"subtype"`
	RequestID string                `json:"request_id"`
	Response  json.RawMessage       `json:"response,omitempty"`
	Error     string                `json:"error,omitempty"`
	Raw       json.RawMessage       `json:"-"`
	Fields    map[string]FieldState `json:"-"`
}

func validObject(data []byte, validate func(map[string]json.RawMessage) error) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("invalid UTF-8")
	}
	if err := validateJSONDocument(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	return validate(fields)
}
func requiredString(m map[string]json.RawMessage, key string) (string, error) {
	var s string
	if err := json.Unmarshal(m[key], &s); err != nil || s == "" {
		return "", fmt.Errorf("%s requires a nonempty string", key)
	}
	return s, nil
}
func validateMetadata(m map[string]json.RawMessage) error {
	s, err := requiredString(m, "subtype")
	if err != nil {
		return err
	}
	if s != "can_use_tool" {
		return fmt.Errorf("not a can_use_tool request")
	}
	if _, err = requiredString(m, "tool_name"); err != nil {
		return err
	}
	if err = objectOnly(m["input"]); err != nil {
		return fmt.Errorf("permission input: %w", err)
	}
	if b, ok := m["permission_suggestions"]; ok && !bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		var a []json.RawMessage
		if err := json.Unmarshal(b, &a); err != nil {
			return err
		}
		for _, v := range a {
			if err := objectOnly(v); err != nil {
				return err
			}
		}
	}
	return nil
}
func validatePermissionResult(m map[string]json.RawMessage) error {
	behavior, err := requiredString(m, "behavior")
	if err != nil {
		return err
	}
	switch PermissionBehavior(behavior) {
	case PermissionAllow:
		if _, ok := m["message"]; ok {
			return fmt.Errorf("allow cannot contain message")
		}
		if b, ok := m["updatedInput"]; ok {
			return objectOnly(b)
		}
	case PermissionDeny:
		if _, ok := m["updatedInput"]; ok {
			return fmt.Errorf("deny cannot contain updatedInput")
		}
		var message string
		b, ok := m["message"]
		if !ok || bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
			return fmt.Errorf("deny requires string message")
		}
		return json.Unmarshal(b, &message)
	default:
		return fmt.Errorf("unsupported permission behavior %q", behavior)
	}
	return nil
}
func validateResponseData(m map[string]json.RawMessage) error {
	s, err := requiredString(m, "subtype")
	if err != nil {
		return err
	}
	if _, err = requiredString(m, "request_id"); err != nil {
		return err
	}
	switch s {
	case "success":
		if _, ok := m["error"]; ok {
			return fmt.Errorf("success cannot contain error")
		}
		if b, ok := m["response"]; ok && !bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
			return objectOnly(b)
		}
	case "error":
		if _, ok := m["response"]; ok {
			return fmt.Errorf("error cannot contain response")
		}
		var message string
		b, ok := m["error"]
		if !ok || bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
			return fmt.Errorf("error response requires string error")
		}
		return json.Unmarshal(b, &message)
	default:
		return fmt.Errorf("unsupported control response subtype %q", s)
	}
	return nil
}
func validateResponse(m map[string]json.RawMessage) error {
	s, err := requiredString(m, "type")
	if err != nil {
		return err
	}
	if s != "control_response" {
		return fmt.Errorf("not a control_response")
	}
	return validObject(m["response"], validateResponseData)
}

func (v *PermissionMetadata) UnmarshalJSON(data []byte) error {
	*v = PermissionMetadata{}
	if err := validObject(data, validateMetadata); err != nil {
		return err
	}
	type wire PermissionMetadata
	var next wire
	if err := unmarshalExact(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = PermissionMetadata(next)
	return nil
}
func (v PermissionMetadata) MarshalJSON() ([]byte, error) {
	type wire PermissionMetadata
	b, err := objectJSON(wire(v), v.Raw, v.Fields)
	if err != nil {
		return nil, err
	}
	return b, validObject(b, validateMetadata)
}
func (v *PermissionResult) UnmarshalJSON(data []byte) error {
	*v = PermissionResult{}
	if err := validObject(data, validatePermissionResult); err != nil {
		return err
	}
	type wire PermissionResult
	var next wire
	if err := unmarshalExact(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = PermissionResult(next)
	return nil
}
func (v PermissionResult) MarshalJSON() ([]byte, error) {
	type wire PermissionResult
	states := v.Fields
	// A newly constructed denial always includes its required, possibly empty message.
	if len(v.Raw) == 0 && v.Behavior == PermissionDeny && states["message"] == Default {
		states = cloneStates(states)
		states["message"] = Present
	}
	b, err := objectJSON(wire(v), v.Raw, states)
	if err != nil {
		return nil, err
	}
	return b, validObject(b, validatePermissionResult)
}
func cloneStates(v map[string]FieldState) map[string]FieldState {
	next := make(map[string]FieldState, len(v)+1)
	for k, s := range v {
		next[k] = s
	}
	return next
}
func (v *ControlResponseData) UnmarshalJSON(data []byte) error {
	*v = ControlResponseData{}
	if err := validObject(data, validateResponseData); err != nil {
		return err
	}
	type wire ControlResponseData
	var next wire
	if err := unmarshalExact(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = ControlResponseData(next)
	return nil
}
func (v ControlResponseData) MarshalJSON() ([]byte, error) {
	type wire ControlResponseData
	states := v.Fields
	if len(v.Raw) == 0 && v.Subtype == "error" && states["error"] == Default {
		states = cloneStates(states)
		states["error"] = Present
	}
	b, err := objectJSON(wire(v), v.Raw, states)
	if err != nil {
		return nil, err
	}
	return b, validObject(b, validateResponseData)
}
func (v *ControlResponse) UnmarshalJSON(data []byte) error {
	*v = ControlResponse{}
	if err := validObject(data, validateResponse); err != nil {
		return err
	}
	type wire ControlResponse
	var next wire
	if err := unmarshalExact(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = ControlResponse(next)
	return nil
}
func (v ControlResponse) MarshalJSON() ([]byte, error) {
	type wire ControlResponse
	b, err := objectJSON(wire(v), v.Raw, v.Fields)
	if err != nil {
		return nil, err
	}
	return b, validObject(b, validateResponse)
}
