package agentpi

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/mcp"
)

type sessionMCPServer struct {
	Name   string           `json:"name"`
	Config sessionMCPConfig `json:"config"`
}

type sessionMCPConfig struct {
	Type     mcp.Transport     `json:"type"`
	Exposure string            `json:"exposure"`
	Command  string            `json:"command,omitempty"`
	Args     []string          `json:"args,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Cwd      string            `json:"cwd,omitempty"`
	URL      string            `json:"url,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}

var (
	piMCPName    = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	piMCPEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	piMCPLiteral = strings.NewReplacer("$", "$$", "!", "$!")
)

func sessionMCPServers(options agent.OpenOptions) ([]sessionMCPServer, error) {
	servers, err := mcp.ResolveServers(options.MCPServers, options.MCPFiles)
	if err != nil {
		var validation *mcp.ValidationError
		if errors.As(err, &validation) {
			return nil, &agent.Error{Code: agent.ErrorInvalidArgument, Reason: validation.Reason, Message: validation.Message}
		}
		return nil, err
	}
	result := make([]sessionMCPServer, 0, len(servers))
	names := make(map[string]bool, len(servers))
	for index, server := range servers {
		namespace := strings.ReplaceAll(server.Name, "-", "_")
		if !piMCPName.MatchString(server.Name) || names[namespace] {
			return nil, invalidPiMCP(index, "name", "requires a valid, distinct Pi server namespace")
		}
		names[namespace] = true
		native, err := sessionMCPConfiguration(index, server)
		if err != nil {
			return nil, err
		}
		result = append(result, sessionMCPServer{Name: server.Name, Config: native})
	}
	return result, nil
}

func sessionMCPConfiguration(index int, server mcp.Server) (sessionMCPConfig, error) {
	config := sessionMCPConfig{Type: server.Transport, Exposure: "direct"}
	switch server.Transport {
	case mcp.TransportStdio:
		for _, path := range append([]string{server.Command, server.Cwd}, server.Args...) {
			if strings.ContainsRune(path, 0) {
				return config, invalidPiMCP(index, "stdio", "cannot contain NUL")
			}
		}
		config.Command, config.Args, config.Cwd = server.Command, server.Args, server.Cwd
		config.Env = piMCPLiterals(server.Env)
	case mcp.TransportHTTP:
		address, err := url.Parse(server.URL)
		if err != nil || address.Hostname() == "" || address.User != nil || address.Fragment != "" ||
			(address.Scheme != "http" && address.Scheme != "https") {
			return config, invalidPiMCP(index, "url", "requires an HTTP(S) URL without credentials or a fragment")
		}
		config.URL = server.URL
		config.Headers, err = piMCPHeaders(index, server)
		if err != nil {
			return config, err
		}
	default:
		return config, unsupported("pi_mcp_transport_unsupported", "Pi MCP supports stdio and Streamable HTTP")
	}
	return config, nil
}

func piMCPLiterals(values []mcp.NamedValue) map[string]string {
	result := make(map[string]string, len(values))
	for _, value := range values {
		// Pi expands configuration templates. Values resolved by the SDK are literal.
		result[value.Name] = piMCPLiteral.Replace(*value.Value)
	}
	return result
}

func piMCPHeaders(index int, server mcp.Server) (map[string]string, error) {
	references := make([]mcp.NamedValue, 0, len(server.EnvHeaders)+1)
	for _, name := range slices.Sorted(maps.Keys(server.EnvHeaders)) {
		env := server.EnvHeaders[name]
		if !piMCPEnvName.MatchString(env) {
			return nil, invalidPiMCP(index, "env_headers", "requires a valid environment variable name")
		}
		references = append(references, mcp.Literal(name, "${"+env+"}"))
	}
	if env := server.BearerTokenEnvVar; env != "" {
		if !piMCPEnvName.MatchString(env) {
			return nil, invalidPiMCP(index, "bearer_token_env_var", "requires a valid environment variable name")
		}
		references = append(references, mcp.Literal("Authorization", "Bearer ${"+env+"}"))
	}
	// Validate the combined header names and case-insensitive collision rules.
	merged := append(slices.Clone(server.Headers), references...)
	if err := mcp.ValidateServers([]mcp.Server{{Name: server.Name, Transport: server.Transport, URL: server.URL, Headers: merged}}); err != nil {
		return nil, invalidPiMCP(index, "headers", "contains an invalid or duplicate header")
	}
	headers := piMCPLiterals(server.Headers)
	for _, value := range references {
		headers[value.Name] = *value.Value
	}
	return headers, nil
}

func invalidPiMCP(index int, field, message string) error {
	return &agent.Error{
		Code: agent.ErrorInvalidArgument, Reason: fmt.Sprintf("mcp_server_%d_%s_invalid", index, field),
		Message: fmt.Sprintf("Pi MCP server %d field %s %s", index, field, message),
	}
}
