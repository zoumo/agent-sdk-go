package claude

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/zoumo/agent-sdk-go/client/claude/types"
	"github.com/zoumo/agent-sdk-go/mcp"
)

const mcpValueEnvPrefix = "AGENT_SDK_MCP_VALUE_"

// PrepareMCP resolves external MCP files and returns a startup snapshot.
// Stream callers should use the returned Env to start the peer, NativeArgs to
// build its arguments, and pass the same options to OpenStreams. Prepare the
// original declarations again for each open or resume.
func PrepareMCP(options SessionOptions) (SessionOptions, error) {
	prepared := Options(options)
	if err := prepared.prepareMCP(); err != nil {
		return SessionOptions{}, err
	}
	return SessionOptions(prepared), nil
}

func (o *Options) prepareMCP() error {
	if err := validateSDKMCPServers(o.SDKMCPServers); err != nil {
		return err
	}
	if err := validateMCPServers(o.MCPServers, o.SDKMCPServers); err != nil {
		return err
	}
	resolved, err := mcp.ResolveServers(o.MCPServers, o.MCPFiles)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	var injected []string
	for i, server := range resolved {
		original := o.MCPServers[i]
		if err := prepareMCPValues(i, "env", original.Env, server.Env, &injected); err != nil {
			return err
		}
		if err := prepareMCPValues(i, "headers", original.Headers, server.Headers, &injected); err != nil {
			return err
		}
	}
	if len(injected) != 0 {
		if o.Env == nil {
			o.Env = os.Environ()
		} else {
			o.Env = slices.Clone(o.Env)
		}
		o.Env = append(o.Env, injected...)
	}
	o.MCPServers = resolved
	o.MCPFiles = nil
	return nil
}

func prepareMCPValues(index int, field string, declarations, resolved []mcp.NamedValue, env *[]string) error {
	explicit := make(map[string]struct{}, len(declarations))
	for _, entry := range declarations {
		if entry.Value != nil {
			// The resolver retains the spelling of the explicit entry that replaced an import.
			explicit[entry.Name] = struct{}{}
		}
	}
	for i, entry := range resolved {
		value := *entry.Value
		if _, ok := explicit[entry.Name]; ok || value == "" {
			continue
		}
		if strings.Contains(value, "${") {
			return fmt.Errorf("%w: mcp server %d: file-backed %s contains Claude interpolation syntax", ErrInvalidInput, index, field)
		}
		name := mcpValueEnvPrefix + rand.Text()
		*env = append(*env, name+"="+value)
		resolved[i] = mcp.Literal(entry.Name, "${"+name+"}")
	}
	return nil
}

func hasMCPFileSources(servers []mcp.Server) bool {
	hasSource := func(entry mcp.NamedValue) bool { return entry.ValueFrom != nil }
	for _, server := range servers {
		if len(server.EnvFrom) != 0 || len(server.HeadersFrom) != 0 ||
			slices.ContainsFunc(server.Env, hasSource) || slices.ContainsFunc(server.Headers, hasSource) {
			return true
		}
	}
	return false
}

// SDKMCPRequest preserves the original native server name and JSON-RPC message.
// Metadata does not grant authority: only IncomingRequest.ReplyMCP can answer it.
type SDKMCPRequest struct {
	ServerName string
	Message    json.RawMessage
}

func nativeMCPServerConfig(server mcp.Server) (map[string]any, error) {
	config := map[string]any{"type": string(server.Transport)}
	switch server.Transport {
	case mcp.TransportStdio:
		config["command"] = server.Command
		if len(server.Args) > 0 {
			config["args"] = server.Args
		}
		if len(server.Env) > 0 {
			values, err := mcp.LiteralValues(server.Env)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrInvalidInput, err)
			}
			config["env"] = values
		}
	case mcp.TransportHTTP, mcp.TransportSSE:
		config["url"] = server.URL
		if len(server.Headers) > 0 {
			values, err := mcp.LiteralValues(server.Headers)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrInvalidInput, err)
			}
			config["headers"] = values
		}
	}
	return config, nil
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

func validateSDKMCPServerManifests(names []string, manifests map[string]types.SDKMCPServerManifest) error {
	isObject := func(raw json.RawMessage) bool {
		raw = bytes.TrimSpace(raw)
		return len(raw) != 0 && raw[0] == '{' && json.Valid(raw) && utf8.Valid(raw)
	}
	for name, manifest := range manifests {
		if !slices.Contains(names, name) || !isObject(manifest.InitializeResult) ||
			(manifest.ToolsListResult != nil && !isObject(manifest.ToolsListResult)) {
			return fmt.Errorf("%w: invalid SDK MCP manifest for %q", ErrInvalidInput, name)
		}
	}
	return nil
}

func validateMCPServers(servers []mcp.Server, sdkNames []string) error {
	if err := mcp.ValidateServers(servers); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
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
		switch {
		case server.Cwd != "":
			return fmt.Errorf("%w: mcp server %d: cwd is not supported", ErrInvalidInput, i)
		case len(server.EnvHeaders) != 0:
			return fmt.Errorf("%w: mcp server %d: env_headers is not supported", ErrInvalidInput, i)
		case server.BearerTokenEnvVar != "":
			return fmt.Errorf("%w: mcp server %d: bearer_token_env_var is not supported", ErrInvalidInput, i)
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
