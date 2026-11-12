package agentclaude

import (
	"fmt"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/mcp"
)

func claudeMCPServers(servers []mcp.Server) ([]mcp.Server, error) {
	for index, server := range servers {
		switch {
		case server.Cwd != "":
			return nil, unsupportedClaudeMCPField(index, "cwd")
		case len(server.EnvHeaders) != 0:
			return nil, unsupportedClaudeMCPField(index, "env_headers")
		case server.BearerTokenEnvVar != "":
			return nil, unsupportedClaudeMCPField(index, "bearer_token_env_var")
		}
	}
	return mcp.CloneServers(servers), nil
}

func unsupportedClaudeMCPField(index int, field string) error {
	return adapterError(
		agent.ErrorUnsupported,
		ErrorReasonMCPServerFieldUnsupported(index, field),
		fmt.Sprintf("Claude MCP server %d field %s is not supported", index, field),
		nil,
	)
}
