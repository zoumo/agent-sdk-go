package codex

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"slices"

	"github.com/zoumo/agent-sdk-go/mcp"
)

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

func addMCPServerConfig(generated map[string]json.RawMessage, servers []mcp.Server, files fs.FS) error {
	resolved, err := mcp.ResolveServers(servers, files)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	for index, server := range resolved {
		if server.Transport == mcp.TransportSSE {
			return fmt.Errorf(
				"%w: mcp server %d: transport sse is not supported",
				ErrInvalidInput,
				index,
			)
		}
		env, err := mcp.LiteralValues(server.Env)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidInput, err)
		}
		headers, err := mcp.LiteralValues(server.Headers)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidInput, err)
		}
		config := nativeMCPServerConfig{
			Command:           server.Command,
			Args:              slices.Clone(server.Args),
			Env:               env,
			Cwd:               server.Cwd,
			URL:               server.URL,
			Headers:           headers,
			EnvHeaders:        maps.Clone(server.EnvHeaders),
			BearerTokenEnvVar: server.BearerTokenEnvVar,
		}
		raw, err := json.Marshal(config)
		if err != nil {
			return ErrInvalidInput
		}
		generated["mcp_servers."+server.Name] = raw
	}
	return nil
}
