package agent

import (
	"errors"
	"strconv"
	"strings"

	"github.com/zoumo/agent-sdk-go/mcp"
)

func validateMCPServers(servers []mcp.Server) error {
	if err := mcp.ValidateServers(servers); err != nil {
		var validation *mcp.ValidationError
		if errors.As(err, &validation) {
			return invalidArgument(validation.Reason, validation.Message)
		}
		return invalidArgument("mcp_servers_invalid", "invalid MCP server configuration")
	}
	for index, server := range servers {
		if server.Transport == mcp.TransportSSE {
			return invalidMCPArgument(
				index,
				"transport",
				"unknown",
				"must be stdio or http",
			)
		}
	}
	return nil
}

func invalidMCPArgument(index int, field, reason, message string) error {
	position := strconv.Itoa(index)
	return invalidArgument(
		"mcp_server_"+position+"_"+strings.ReplaceAll(field, " ", "_")+"_"+reason,
		"MCP server "+position+" field "+field+" "+message,
	)
}
