package agentcodex

import (
	"maps"
	"slices"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/codex"
)

func nativeMCPServers(servers []agent.MCPServer) []codex.MCPServer {
	if servers == nil {
		return nil
	}
	converted := make([]codex.MCPServer, len(servers))
	for index, server := range servers {
		// Unified validation has already selected stdio or HTTP. Codex
		// distinguishes them by the command or URL carried in this value.
		converted[index] = codex.MCPServer{
			Name:              server.Name,
			Command:           server.Command,
			Args:              slices.Clone(server.Args),
			Env:               maps.Clone(server.Env),
			Cwd:               server.Cwd,
			URL:               server.URL,
			Headers:           maps.Clone(server.Headers),
			EnvHeaders:        maps.Clone(server.EnvHeaders),
			BearerTokenEnvVar: server.BearerTokenEnvVar,
		}
	}
	return converted
}
