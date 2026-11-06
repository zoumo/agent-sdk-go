package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"
)

// MCPServer declares one external MCP server for a Session.
type MCPServer struct {
	Name              string
	Command           string
	Args              []string
	Env               map[string]string
	Cwd               string
	URL               string
	Headers           map[string]string
	EnvHeaders        map[string]string
	BearerTokenEnvVar string
}

type nativeMCPServerConfig struct {
	Command           string            `json:"command,omitempty"`
	Args              []string          `json:"args,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
	Cwd               string            `json:"cwd,omitempty"`
	URL               string            `json:"url,omitempty"`
	Headers           map[string]string `json:"http_headers,omitempty"`
	EnvHeaders        map[string]string `json:"env_http_headers,omitempty"`
	BearerTokenEnvVar string            `json:"bearer_token_env_var,omitempty"`
}

func addMCPServerConfig(generated map[string]json.RawMessage, servers []MCPServer) error {
	seen := make(map[string]struct{}, len(servers))
	for i, server := range servers {
		if err := validateMCPName(seen, server.Name); err != nil {
			return fmt.Errorf("%w: mcp server %d: %w", ErrInvalidInput, i, err)
		}
		if err := validateMCPServerText(i, server); err != nil {
			return err
		}
		hasCommand := server.Command != ""
		hasURL := server.URL != ""
		if hasCommand == hasURL {
			return fmt.Errorf("%w: mcp server %d requires exactly one of command or url", ErrInvalidInput, i)
		}
		config := nativeMCPServerConfig{
			Command:           server.Command,
			Args:              slices.Clone(server.Args),
			Env:               maps.Clone(server.Env),
			Cwd:               server.Cwd,
			URL:               server.URL,
			Headers:           maps.Clone(server.Headers),
			EnvHeaders:        maps.Clone(server.EnvHeaders),
			BearerTokenEnvVar: server.BearerTokenEnvVar,
		}
		if hasCommand {
			switch {
			case len(config.Headers) > 0:
				return invalidMCPField(i, "stdio", "headers")
			case len(config.EnvHeaders) > 0:
				return invalidMCPField(i, "stdio", "env_headers")
			case config.BearerTokenEnvVar != "":
				return invalidMCPField(i, "stdio", "bearer_token_env_var")
			}
		} else {
			switch {
			case len(config.Args) > 0:
				return invalidMCPField(i, "streamable HTTP", "args")
			case len(config.Env) > 0:
				return invalidMCPField(i, "streamable HTTP", "env")
			case config.Cwd != "":
				return invalidMCPField(i, "streamable HTTP", "cwd")
			}
		}
		raw, err := json.Marshal(config)
		if err != nil {
			return ErrInvalidInput
		}
		generated["mcp_servers."+server.Name] = raw
	}
	return nil
}

func validateMCPName(seen map[string]struct{}, name string) error {
	if name == "" || !utf8.ValidString(name) {
		return errors.New("name must be nonempty valid UTF-8")
	}
	if strings.Contains(name, ".") {
		return errors.New(`name must not contain "."`)
	}
	if _, ok := seen[name]; ok {
		return errors.New("name must be unique")
	}
	seen[name] = struct{}{}
	return nil
}

func validateMCPServerText(index int, server MCPServer) error {
	for _, field := range []struct {
		path  string
		value string
	}{
		{path: "command", value: server.Command},
		{path: "cwd", value: server.Cwd},
		{path: "url", value: server.URL},
		{path: "bearer_token_env_var", value: server.BearerTokenEnvVar},
	} {
		if !utf8.ValidString(field.value) {
			return invalidMCPUTF8(index, field.path)
		}
	}
	for argIndex, arg := range server.Args {
		if !utf8.ValidString(arg) {
			return invalidMCPUTF8(index, fmt.Sprintf("args[%d]", argIndex))
		}
	}
	for _, field := range []struct {
		path   string
		values map[string]string
	}{
		{path: "env", values: server.Env},
		{path: "headers", values: server.Headers},
		{path: "env_headers", values: server.EnvHeaders},
	} {
		if err := validateMCPMapText(index, field.path, field.values); err != nil {
			return err
		}
	}
	return nil
}

func validateMCPMapText(index int, path string, values map[string]string) error {
	for _, key := range slices.Sorted(maps.Keys(values)) {
		if !utf8.ValidString(key) {
			return invalidMCPUTF8(index, path+" key")
		}
		if !utf8.ValidString(values[key]) {
			return invalidMCPUTF8(index, path+" value")
		}
	}
	return nil
}

func invalidMCPUTF8(index int, path string) error {
	return fmt.Errorf("%w: mcp server %d: %s must be valid UTF-8", ErrInvalidInput, index, path)
}

func invalidMCPField(index int, transport, field string) error {
	return fmt.Errorf("%w: mcp server %d: %s does not allow %s", ErrInvalidInput, index, transport, field)
}
