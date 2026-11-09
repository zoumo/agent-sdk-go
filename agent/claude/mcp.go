package agentclaude

import (
	"fmt"
	"maps"
	"slices"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/claude"
)

func claudeMCPServers(servers []agent.MCPServer) ([]claude.MCPServer, error) {
	if len(servers) == 0 {
		return nil, nil
	}
	converted := make([]claude.MCPServer, len(servers))
	for index, server := range servers {
		switch {
		case server.Cwd != "":
			return nil, unsupportedClaudeMCPField(index, "cwd")
		case len(server.EnvHeaders) != 0:
			return nil, unsupportedClaudeMCPField(index, "env_headers")
		case server.BearerTokenEnvVar != "":
			return nil, unsupportedClaudeMCPField(index, "bearer_token_env_var")
		}
		converted[index] = claude.MCPServer{
			Name:      server.Name,
			Transport: claude.MCPTransport(server.Transport),
			Command:   server.Command,
			Args:      slices.Clone(server.Args),
			Env:       maps.Clone(server.Env),
			URL:       server.URL,
			Headers:   maps.Clone(server.Headers),
		}
	}
	return converted, nil
}

func unsupportedClaudeMCPField(index int, field string) error {
	return adapterError(
		agent.ErrorUnsupported,
		fmt.Sprintf("mcp_server_%d_%s_unsupported", index, field),
		fmt.Sprintf("Claude MCP server %d field %s is not supported", index, field),
		nil,
	)
}
