// Package mcp defines one external MCP server configuration shared by the
// native Codex and Claude SDKs and the adapter-neutral agent layer.
package mcp

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Transport identifies how an external MCP server is reached.
type Transport string

const (
	// TransportStdio starts a local MCP server and communicates over stdio.
	TransportStdio Transport = "stdio"
	// TransportHTTP selects MCP Streamable HTTP.
	TransportHTTP Transport = "http"
	// TransportSSE selects the legacy HTTP+SSE transport supported by Claude.
	TransportSSE Transport = "sse"
)

// Server declares one external MCP server. Name is the case-sensitive,
// configuration-local key. Transport is required, and every entry point rejects
// a transport it cannot represent instead of inferring or rewriting it.
//
// Stdio uses Command, Args, Env, and Cwd. HTTP and SSE use URL and Headers.
// EnvHeaders and BearerTokenEnvVar are HTTP-only Codex extensions. Values are
// forwarded without resolving secrets.
type Server struct {
	Name      string
	Transport Transport

	Command string
	Args    []string
	Env     map[string]string
	Cwd     string

	URL               string
	Headers           map[string]string
	EnvHeaders        map[string]string
	BearerTokenEnvVar string
}

// ValidationError reports a structural MCP server error without exposing
// configuration values.
type ValidationError struct {
	Reason  string
	Message string
}

func (e *ValidationError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Message
}

// ValidateServers checks transport-independent structure and fields shared by
// every supported provider. Provider-specific support is checked by its SDK.
func ValidateServers(servers []Server) error {
	seenNames := make(map[string]struct{}, len(servers))
	for index, server := range servers {
		switch {
		case server.Name == "":
			return invalidArgument(index, "name", "required", "must not be empty")
		case !utf8.ValidString(server.Name):
			return invalidArgument(index, "name", "invalid_utf8", "must be valid UTF-8")
		case strings.ContainsRune(server.Name, '.'):
			return invalidArgument(index, "name", "dot", "must not contain a dot")
		}
		if _, duplicate := seenNames[server.Name]; duplicate {
			return invalidArgument(index, "name", "duplicate", "must be unique")
		}
		seenNames[server.Name] = struct{}{}

		switch server.Transport {
		case TransportStdio:
			if err := validateStdio(index, server); err != nil {
				return err
			}
		case TransportHTTP, TransportSSE:
			if err := validateRemote(index, server); err != nil {
				return err
			}
		case "":
			return invalidArgument(index, "transport", "required", "must be stdio, http or sse")
		default:
			return invalidArgument(index, "transport", "unknown", "must be stdio, http or sse")
		}
	}
	return nil
}

// CloneServers copies the server slice and its nested slices and maps.
func CloneServers(servers []Server) []Server {
	if servers == nil {
		return nil
	}
	cloned := make([]Server, len(servers))
	for index, server := range servers {
		cloned[index] = server
		cloned[index].Args = slices.Clone(server.Args)
		cloned[index].Env = maps.Clone(server.Env)
		cloned[index].Headers = maps.Clone(server.Headers)
		cloned[index].EnvHeaders = maps.Clone(server.EnvHeaders)
	}
	return cloned
}

func validateStdio(index int, server Server) error {
	if server.Command == "" {
		return invalidArgument(index, "command", "required", "must not be empty")
	}
	switch {
	case server.URL != "":
		return invalidArgument(index, "url", "forbidden", "is not allowed for stdio")
	case len(server.Headers) != 0:
		return invalidArgument(index, "headers", "forbidden", "is not allowed for stdio")
	case len(server.EnvHeaders) != 0:
		return invalidArgument(index, "env_headers", "forbidden", "is not allowed for stdio")
	case server.BearerTokenEnvVar != "":
		return invalidArgument(
			index,
			"bearer_token_env_var",
			"forbidden",
			"is not allowed for stdio",
		)
	}
	if err := validateString(index, "command", server.Command); err != nil {
		return err
	}
	for _, argument := range server.Args {
		if err := validateString(index, "args", argument); err != nil {
			return err
		}
	}
	if err := validateStringMap(index, "env", server.Env); err != nil {
		return err
	}
	return validateString(index, "cwd", server.Cwd)
}

func validateRemote(index int, server Server) error {
	if server.URL == "" {
		return invalidArgument(index, "url", "required", "must not be empty")
	}
	switch {
	case server.Command != "":
		return invalidArgument(index, "command", "forbidden", "is not allowed for HTTP or SSE")
	case len(server.Args) != 0:
		return invalidArgument(index, "args", "forbidden", "is not allowed for HTTP or SSE")
	case len(server.Env) != 0:
		return invalidArgument(index, "env", "forbidden", "is not allowed for HTTP or SSE")
	case server.Cwd != "":
		return invalidArgument(index, "cwd", "forbidden", "is not allowed for HTTP or SSE")
	}
	if err := validateString(index, "url", server.URL); err != nil {
		return err
	}
	if err := validateStringMap(index, "headers", server.Headers); err != nil {
		return err
	}
	if err := validateStringMap(index, "env_headers", server.EnvHeaders); err != nil {
		return err
	}
	return validateString(index, "bearer_token_env_var", server.BearerTokenEnvVar)
}

func invalidArgument(index int, field, reason, message string) error {
	position := strconv.Itoa(index)
	return &ValidationError{
		Reason:  "mcp_server_" + position + "_" + strings.ReplaceAll(field, " ", "_") + "_" + reason,
		Message: "MCP server " + position + " field " + field + " " + message,
	}
}

func validateString(index int, field, value string) error {
	if utf8.ValidString(value) {
		return nil
	}
	return invalidArgument(index, field, "invalid_utf8", "must be valid UTF-8")
}

func validateStringMap(index int, field string, values map[string]string) error {
	for key := range values {
		if err := validateString(index, field+" key", key); err != nil {
			return err
		}
	}
	for _, value := range values {
		if err := validateString(index, field+" value", value); err != nil {
			return err
		}
	}
	return nil
}
