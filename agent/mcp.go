package agent

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// MCPTransport identifies how an external MCP server is reached.
type MCPTransport string

const (
	MCPTransportStdio MCPTransport = "stdio"
	// MCPTransportHTTP is MCP Streamable HTTP, not the legacy HTTP+SSE
	// transport.
	MCPTransportHTTP MCPTransport = "http"
)

// MCPServer is one provider-neutral external MCP server declaration.
// OpenOptions validates the final list before a provider adapter forwards it
// without connecting to the server.
type MCPServer struct {
	Name      string
	Transport MCPTransport

	// Command, Args, Env, and Cwd are valid only for stdio.
	Command string
	Args    []string
	Env     map[string]string
	Cwd     string

	// URL, Headers, EnvHeaders, and BearerTokenEnvVar are valid only for HTTP.
	// EnvHeaders values and BearerTokenEnvVar name environment variables rather
	// than carrying secret values.
	URL               string
	Headers           map[string]string
	EnvHeaders        map[string]string
	BearerTokenEnvVar string
}

func validateMCPServers(servers []MCPServer) error {
	seenNames := make(map[string]struct{}, len(servers))
	for index, server := range servers {
		switch {
		case server.Name == "":
			return invalidMCPArgument(index, "name", "required", "must not be empty")
		case !utf8.ValidString(server.Name):
			return invalidMCPArgument(index, "name", "invalid_utf8", "must be valid UTF-8")
		case strings.ContainsRune(server.Name, '.'):
			return invalidMCPArgument(index, "name", "dot", "must not contain a dot")
		}
		if _, duplicate := seenNames[server.Name]; duplicate {
			return invalidMCPArgument(index, "name", "duplicate", "must be unique")
		}
		seenNames[server.Name] = struct{}{}

		switch server.Transport {
		case MCPTransportStdio:
			if err := validateStdioMCPServer(index, server); err != nil {
				return err
			}
		case MCPTransportHTTP:
			if err := validateHTTPMCPServer(index, server); err != nil {
				return err
			}
		case "":
			return invalidMCPArgument(index, "transport", "required", "must be stdio or http")
		default:
			return invalidMCPArgument(index, "transport", "unknown", "must be stdio or http")
		}
	}
	return nil
}

func validateStdioMCPServer(index int, server MCPServer) error {
	if server.Command == "" {
		return invalidMCPArgument(index, "command", "required", "must not be empty")
	}
	switch {
	case server.URL != "":
		return invalidMCPArgument(index, "url", "forbidden", "is not allowed for stdio")
	case len(server.Headers) != 0:
		return invalidMCPArgument(index, "headers", "forbidden", "is not allowed for stdio")
	case len(server.EnvHeaders) != 0:
		return invalidMCPArgument(index, "env_headers", "forbidden", "is not allowed for stdio")
	case server.BearerTokenEnvVar != "":
		return invalidMCPArgument(
			index,
			"bearer_token_env_var",
			"forbidden",
			"is not allowed for stdio",
		)
	}
	if err := validateMCPString(index, "command", server.Command); err != nil {
		return err
	}
	for _, argument := range server.Args {
		if err := validateMCPString(index, "args", argument); err != nil {
			return err
		}
	}
	if err := validateMCPStringMap(index, "env", server.Env); err != nil {
		return err
	}
	return validateMCPString(index, "cwd", server.Cwd)
}

func validateHTTPMCPServer(index int, server MCPServer) error {
	if server.URL == "" {
		return invalidMCPArgument(index, "url", "required", "must not be empty")
	}
	switch {
	case server.Command != "":
		return invalidMCPArgument(index, "command", "forbidden", "is not allowed for HTTP")
	case len(server.Args) != 0:
		return invalidMCPArgument(index, "args", "forbidden", "is not allowed for HTTP")
	case len(server.Env) != 0:
		return invalidMCPArgument(index, "env", "forbidden", "is not allowed for HTTP")
	case server.Cwd != "":
		return invalidMCPArgument(index, "cwd", "forbidden", "is not allowed for HTTP")
	}
	if err := validateMCPString(index, "url", server.URL); err != nil {
		return err
	}
	if err := validateMCPStringMap(index, "headers", server.Headers); err != nil {
		return err
	}
	if err := validateMCPStringMap(index, "env_headers", server.EnvHeaders); err != nil {
		return err
	}
	return validateMCPString(index, "bearer_token_env_var", server.BearerTokenEnvVar)
}

func invalidMCPArgument(index int, field, reason, message string) error {
	position := strconv.Itoa(index)
	return invalidArgument(
		"mcp_server_"+position+"_"+strings.ReplaceAll(field, " ", "_")+"_"+reason,
		"MCP server "+position+" field "+field+" "+message,
	)
}

func validateMCPString(index int, field, value string) error {
	if utf8.ValidString(value) {
		return nil
	}
	return invalidMCPArgument(index, field, "invalid_utf8", "must be valid UTF-8")
}

func validateMCPStringMap(index int, field string, values map[string]string) error {
	for key := range values {
		if err := validateMCPString(index, field+" key", key); err != nil {
			return err
		}
	}
	for _, value := range values {
		if err := validateMCPString(index, field+" value", value); err != nil {
			return err
		}
	}
	return nil
}
