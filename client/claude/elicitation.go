// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bytes"
	"context"
	"encoding/json"
)

// ElicitationRequest describes a standalone MCP prompt. Params retains the full native request.
type ElicitationRequest struct {
	ServerName      string
	Message         string
	Mode            string
	URL             string
	ElicitationID   string
	RequestedSchema map[string]any
}

// Elicitation decodes the original control request. Omitted mode selects form.
func (r IncomingRequest) Elicitation() (ElicitationRequest, error) {
	if r.method != "elicitation" || r.permission == nil {
		return ElicitationRequest{}, ErrUnsupported
	}
	var fields map[string]any
	decoder := json.NewDecoder(bytes.NewReader(r.Params()))
	decoder.UseNumber()
	if err := decoder.Decode(&fields); err != nil {
		return ElicitationRequest{}, ErrInvalidInput
	}
	request := ElicitationRequest{Mode: "form"}
	request.ServerName, _ = fields["mcp_server_name"].(string)
	request.Message, _ = fields["message"].(string)
	if request.ServerName == "" || request.Message == "" {
		return ElicitationRequest{}, ErrInvalidInput
	}
	if mode, present := fields["mode"]; present {
		request.Mode, _ = mode.(string)
	}
	switch request.Mode {
	case "form":
		if schema, present := fields["requested_schema"]; present {
			request.RequestedSchema, _ = schema.(map[string]any)
			if request.RequestedSchema == nil {
				return ElicitationRequest{}, ErrInvalidInput
			}
		}
	case "url":
		request.URL, _ = fields["url"].(string)
		request.ElicitationID, _ = fields["elicitation_id"].(string)
		if request.URL == "" {
			return ElicitationRequest{}, ErrInvalidInput
		}
	default:
		return ElicitationRequest{}, ErrUnsupported
	}
	return request, nil
}

// ReplyElicitation sends accept, decline or cancel using the original control
// request ID. Only accepted forms carry content. An empty map sends {}.
func (r IncomingRequest) ReplyElicitation(ctx context.Context, action string, content map[string]any) error {
	const op = "answer elicitation"
	if r.method != "elicitation" || r.permission == nil {
		return dispatchFailure(op, ErrUnsupported)
	}
	result := map[string]any{"action": action}
	switch action {
	case "accept":
		params, err := r.Elicitation()
		if err != nil {
			return dispatchFailure(op, err)
		}
		if content != nil {
			if params.Mode != "form" {
				return dispatchFailure(op, ErrInvalidInput)
			}
			raw, err := json.Marshal(content)
			if err != nil || !validElicitationContent(raw) {
				return dispatchFailure(op, ErrInvalidInput)
			}
			result["content"] = json.RawMessage(raw)
		}
	case "decline", "cancel":
		if content != nil {
			return dispatchFailure(op, ErrInvalidInput)
		}
	default:
		return dispatchFailure(op, ErrInvalidInput)
	}
	if err := r.permission.reserveAnswer(ctx); err != nil {
		return dispatchFailure(op, err)
	}
	return dispatchFailure(op, r.permission.writeResponse(ctx, map[string]any{
		"subtype": "success", "request_id": r.ID(), "response": result,
	}))
}

// The MCP ElicitResult record accepts scalar strings, numbers, booleans and string arrays.
func validElicitationContent(raw []byte) bool {
	var fields map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&fields); err != nil {
		return false
	}
	for _, field := range fields {
		switch value := field.(type) {
		case string, bool, json.Number:
		case []any:
			for _, entry := range value {
				if _, ok := entry.(string); !ok {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}
