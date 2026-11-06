package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"unicode/utf8"

	"github.com/zoumo/agent-sdk-go/claude/types"
)

// MCPTransport selects one native Claude MCP transport.
type MCPTransport = types.MCPTransport

const (
	MCPTransportStdio = types.MCPTransportStdio
	MCPTransportHTTP  = types.MCPTransportHTTP
	MCPTransportSSE   = types.MCPTransportSSE
)

// MCPServer declares one external MCP server for a Session.
type MCPServer = types.MCPServer

// SDKMCPRequest preserves the original native server name and JSON-RPC message.
// Metadata does not grant authority: only IncomingRequest.ReplyMCP can answer it.
type SDKMCPRequest struct {
	ServerName string
	Message    json.RawMessage
}

func nativeMCPServerConfig(server MCPServer) map[string]any {
	config := map[string]any{"type": string(server.Transport)}
	switch server.Transport {
	case MCPTransportStdio:
		config["command"] = server.Command
		if len(server.Args) > 0 {
			config["args"] = server.Args
		}
		if len(server.Env) > 0 {
			config["env"] = server.Env
		}
	case MCPTransportHTTP, MCPTransportSSE:
		config["url"] = server.URL
		if len(server.Headers) > 0 {
			config["headers"] = server.Headers
		}
	}
	return config
}

func validateSDKMCPServers(names []string) error {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if err := addMCPName(seen, name); err != nil {
			return ErrInvalidInput
		}
	}
	return nil
}

func validateMCPServers(servers []MCPServer, sdkNames []string) error {
	used := make(map[string]struct{}, len(sdkNames)+len(servers))
	for _, name := range sdkNames {
		if err := addMCPName(used, name); err != nil {
			return ErrInvalidInput
		}
	}
	for i, server := range servers {
		if err := addMCPName(used, server.Name); err != nil {
			return fmt.Errorf("%w: mcp server %d has an invalid or duplicate name", ErrInvalidInput, i)
		}
		switch server.Transport {
		case MCPTransportStdio:
			if server.Command == "" {
				return fmt.Errorf("%w: mcp server %d: stdio requires command", ErrInvalidInput, i)
			}
			if server.URL != "" || len(server.Headers) > 0 {
				return fmt.Errorf("%w: mcp server %d: stdio does not allow url or headers", ErrInvalidInput, i)
			}
		case MCPTransportHTTP, MCPTransportSSE:
			if server.URL == "" {
				return fmt.Errorf("%w: mcp server %d: %s requires url", ErrInvalidInput, i, server.Transport)
			}
			if server.Command != "" || len(server.Args) > 0 || len(server.Env) > 0 {
				return fmt.Errorf("%w: mcp server %d: %s does not allow command, args or env", ErrInvalidInput, i, server.Transport)
			}
		default:
			return fmt.Errorf("%w: mcp server %d requires transport stdio, http or sse", ErrInvalidInput, i)
		}
	}
	return nil
}

func addMCPName(seen map[string]struct{}, name string) error {
	if name == "" || !utf8.ValidString(name) {
		return ErrInvalidInput
	}
	if _, ok := seen[name]; ok {
		return ErrInvalidInput
	}
	seen[name] = struct{}{}
	return nil
}

func cloneMCPServers(servers []MCPServer) []MCPServer {
	if servers == nil {
		return nil
	}
	cloned := make([]MCPServer, len(servers))
	for i, server := range servers {
		cloned[i] = server
		cloned[i].Args = slices.Clone(server.Args)
		cloned[i].Env = maps.Clone(server.Env)
		cloned[i].Headers = maps.Clone(server.Headers)
	}
	return cloned
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
	return slices.Contains(c.opts.SDKMCPServers, metadata.ServerName)
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
	if e := validateInputEncoding(response); e != nil {
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
	if e := r.permission.reserveAnswer(ctx); e != nil {
		return e
	}
	return r.permission.writeResponse(ctx, map[string]any{"subtype": "success", "request_id": r.ID(), "response": map[string]any{"mcp_response": json.RawMessage(bytes.Clone(response))}})
}
