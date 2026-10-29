package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

// SDKMCPRequest preserves the original native server name and JSON-RPC message.
// Metadata does not grant authority: only IncomingRequest.ReplyMCP can answer it.
type SDKMCPRequest struct {
	ServerName string
	Message    json.RawMessage
}

func validateSDKMCPServers(names []string) error {
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" || !utf8.ValidString(name) || seen[name] {
			return ErrInvalidInput
		}
		seen[name] = true
	}
	return nil
}
func decodeSDKMCPRequest(raw json.RawMessage) (SDKMCPRequest, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return SDKMCPRequest{}, ErrInvalidInput
	}
	var name string
	if json.Unmarshal(fields["server_name"], &name) != nil || name == "" {
		return SDKMCPRequest{}, ErrInvalidInput
	}
	message := bytes.TrimSpace(fields["message"])
	if len(message) == 0 || message[0] != '{' || !json.Valid(message) {
		return SDKMCPRequest{}, ErrInvalidInput
	}
	return SDKMCPRequest{ServerName: name, Message: bytes.Clone(message)}, nil
}
func (c *interaction) ownsSDKMCPRequest(request *ControlRequest) bool {
	if request.Subtype != "mcp_message" {
		return false
	}
	metadata, e := decodeSDKMCPRequest(request.Request)
	if e != nil {
		return false
	}
	for _, name := range c.opts.SDKMCPServers {
		if name == metadata.ServerName {
			return true
		}
	}
	return false
}
func (r IncomingRequest) MCPRequest() (SDKMCPRequest, error) {
	if r.method != "mcp_message" || r.permission == nil {
		return SDKMCPRequest{}, ErrUnsupported
	}
	return decodeSDKMCPRequest(r.Params())
}

// ReplyMCP sends one native mcp_response through the original control authority.
// It preserves the JSON-RPC response and ID, including numeric lexemes/unknowns.
// No MCP matching engine or callback runs inside the SDK. Applications answer
// initialize/tools/list/tools/call themselves, and acknowledge notifications.
// A successful write does not prove CLI/model acceptance; OutcomeUnknown is not retryable.
func (r IncomingRequest) ReplyMCP(ctx context.Context, response json.RawMessage) (result error) {
	defer func() {
		if result != nil {
			state := NotSent
			if errors.Is(result, ErrUnknownOutcome) {
				state = OutcomeUnknown
			}
			result = &DispatchError{Op: "MCP answer", State: state, Err: result}
		}
	}()
	metadata, e := r.MCPRequest()
	if e != nil {
		return e
	}
	response = bytes.TrimSpace(response)
	if len(response) == 0 || response[0] != '{' || !json.Valid(response) || !utf8.Valid(response) {
		return ErrInvalidInput
	}
	if e = validateInputEncoding(response); e != nil {
		return e
	}
	var request, reply map[string]json.RawMessage
	if json.Unmarshal(metadata.Message, &request) != nil || json.Unmarshal(response, &reply) != nil {
		return ErrInvalidInput
	}
	var version string
	if json.Unmarshal(reply["jsonrpc"], &version) != nil || version != "2.0" {
		return ErrInvalidInput
	}
	compact := func(value json.RawMessage) string {
		var buf bytes.Buffer
		_ = json.Compact(&buf, value)
		return buf.String()
	}
	if id, ok := request["id"]; ok && compact(id) != compact(reply["id"]) {
		return ErrInvalidInput
	}
	_, hasResult := reply["result"]
	_, hasError := reply["error"]
	if hasResult == hasError {
		return ErrInvalidInput
	}
	if e = r.permission.reserveAnswer(ctx); e != nil {
		return e
	}
	return r.permission.writeResponse(ctx, map[string]any{"subtype": "success", "request_id": r.ID(), "response": map[string]any{"mcp_response": json.RawMessage(bytes.Clone(response))}})
}
