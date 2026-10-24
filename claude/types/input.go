// SPDX-License-Identifier: Apache-2.0

package types

import "encoding/json"

// InputMessage is the native user MessageParam, distinct from output Message.
// Raw retains additional MessageParam fields. Role must be user when sent.
type InputMessage struct {
	Role    string                `json:"role"`
	Content Content               `json:"content"`
	Raw     json.RawMessage       `json:"-"`
	Fields  map[string]FieldState `json:"-"`
}

// UserInput carries SDKUserMessage data from SDK 0.3.258. Session fixes the
// outer type/session_id/parent_tool_use_id. Raw/Fields preserve extension and
// presence data; they cannot select another execution target.
type UserInput struct {
	Message       InputMessage          `json:"message"`
	UUID          *string               `json:"uuid,omitempty"`
	Priority      *string               `json:"priority,omitempty"`
	Origin        *MessageOrigin        `json:"origin,omitempty"`
	ShouldQuery   *bool                 `json:"shouldQuery,omitempty"`
	Timestamp     *string               `json:"timestamp,omitempty"`
	ToolUseResult json.RawMessage       `json:"tool_use_result,omitempty"`
	Raw           json.RawMessage       `json:"-"`
	Fields        map[string]FieldState `json:"-"`
}

func (v *InputMessage) UnmarshalJSON(data []byte) error {
	if err := objectOnly(data); err != nil {
		return err
	}
	type wire InputMessage
	var next wire
	if err := unmarshalExact(data, &next); err != nil {
		return err
	}
	next.Raw = append(json.RawMessage(nil), data...)
	*v = InputMessage(next)
	return nil
}
func (v InputMessage) MarshalJSON() ([]byte, error) {
	type wire InputMessage
	return objectJSON(wire(v), v.Raw, v.Fields)
}
func (v *UserInput) UnmarshalJSON(data []byte) error {
	if err := objectOnly(data); err != nil {
		return err
	}
	type wire UserInput
	var next wire
	if err := unmarshalExact(data, &next); err != nil {
		return err
	}
	next.Raw = append(json.RawMessage(nil), data...)
	*v = UserInput(next)
	return nil
}
func (v UserInput) MarshalJSON() ([]byte, error) {
	type wire UserInput
	return objectJSON(wire(v), v.Raw, v.Fields)
}
