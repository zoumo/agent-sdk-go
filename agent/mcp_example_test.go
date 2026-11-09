package agent_test

import (
	"fmt"

	"github.com/zoumo/agent-sdk-go/agent"
)

// ExampleMCPServer shows the final provider-neutral external MCP list accepted by
// Open and Resume. EnvHeaders and BearerTokenEnvVar use environment-variable names
// for Codex; the Claude adapter accepts the common stdio and HTTP URL/Headers
// subset and rejects those two Codex-specific HTTP references.
//
// This example only validates parameters. It does not resolve a secret, launch a
// native CLI, or connect to an MCP server.
func ExampleMCPServer() {
	options := agent.OpenOptions{
		Cwd: "/synthetic/workspace",
		MCPServers: []agent.MCPServer{
			{
				Name:      "local-tools",
				Transport: agent.MCPTransportStdio,
				Command:   "mcp-local",
				Args:      []string{"--stdio"},
				Env:       map[string]string{"LOCAL_TOOLS_MODE": "read-only"},
			},
			{
				Name:              "remote-tools",
				Transport:         agent.MCPTransportHTTP,
				URL:               "https://mcp.example.invalid/tools",
				Headers:           map[string]string{"X-Workspace": "sdk"},
				EnvHeaders:        map[string]string{"Authorization": "REMOTE_AUTHORIZATION"},
				BearerTokenEnvVar: "REMOTE_TOKEN",
			},
		},
	}
	if err := options.Validate(); err != nil {
		fmt.Println("invalid:", err)
		return
	}

	fmt.Printf("validated %d final MCP servers\n", len(options.MCPServers))
	for _, server := range options.MCPServers {
		fmt.Printf("%s transport=%s\n", server.Name, server.Transport)
	}

	resume := agent.ResumeOptions{
		SessionID:   "saved-session",
		OpenOptions: options,
	}
	if err := resume.Validate(); err != nil {
		fmt.Println("invalid resume:", err)
		return
	}
	fmt.Printf("resume parameters validated with %d servers\n", len(resume.MCPServers))

	// Output:
	// validated 2 final MCP servers
	// local-tools transport=stdio
	// remote-tools transport=http
	// resume parameters validated with 2 servers
}
