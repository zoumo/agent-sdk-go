// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExternalMCPStdioArguments(t *testing.T) {
	argv := captureArgs(t, func(options *Options) {
		options.MCPServers = []MCPServer{{
			Name:      "local",
			Transport: MCPTransportStdio,
			Command:   "fixture-mcp",
			Args:      []string{"--stdio", "literal value"},
			Env:       map[string]string{"FIXTURE_TOKEN": "${FIXTURE_TOKEN}"},
		}}
	})

	wantConfig := `{"mcpServers":{"local":{"args":["--stdio","literal value"],"command":"fixture-mcp","env":{"FIXTURE_TOKEN":"${FIXTURE_TOKEN}"},"type":"stdio"}}}`
	want := append(append([]string(nil), baseInteractionArgs...), "--mcp-config="+wantConfig)
	require.Equal(t, want, argv)
}

func TestExternalMCPRemoteArgumentsAndStrictOption(t *testing.T) {
	for _, tc := range []struct {
		name      string
		transport MCPTransport
	}{
		{name: "http", transport: MCPTransportHTTP},
		{name: "sse", transport: MCPTransportSSE},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argv := captureArgs(t, func(options *Options) {
				options.StrictMCPConfig = true
				options.MCPServers = []MCPServer{{
					Name:      "remote",
					Transport: tc.transport,
					URL:       "https://example.invalid/mcp",
					Headers:   map[string]string{"Authorization": "Bearer ${REMOTE_TOKEN}"},
				}}
			})

			wantConfig := `{"mcpServers":{"remote":{"headers":{"Authorization":"Bearer ${REMOTE_TOKEN}"},"type":"` + string(tc.transport) + `","url":"https://example.invalid/mcp"}}}`
			want := append(append([]string(nil), baseInteractionArgs...), "--mcp-config="+wantConfig, "--strict-mcp-config")
			require.Equal(t, want, argv)
		})
	}
}

func TestExternalMCPMergesWithSDKMCPServers(t *testing.T) {
	argv := captureArgs(t, func(options *Options) {
		options.SDKMCPServers = []string{"fixture"}
		options.MCPServers = []MCPServer{{
			Name:      "remote",
			Transport: MCPTransportHTTP,
			URL:       "https://example.invalid/mcp",
		}}
	})

	var configs []string
	for _, arg := range argv {
		if config, ok := strings.CutPrefix(arg, "--mcp-config="); ok {
			configs = append(configs, config)
		}
	}
	require.Len(t, configs, 1)
	require.JSONEq(t, `{"mcpServers":{"fixture":{"type":"sdk","name":"fixture"},"remote":{"type":"http","url":"https://example.invalid/mcp"}}}`, configs[0])
}

func TestExternalMCPRejectsInvalidConfigurationBeforeLaunch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sdk     []string
		servers []MCPServer
	}{
		{
			name:    "empty transport",
			servers: []MCPServer{{Name: "server", Command: "fixture-mcp"}},
		},
		{
			name:    "unknown transport",
			servers: []MCPServer{{Name: "server", Transport: MCPTransport("oauth"), Command: "fixture-mcp"}},
		},
		{
			name:    "stdio missing command",
			servers: []MCPServer{{Name: "server", Transport: MCPTransportStdio}},
		},
		{
			name: "stdio with url",
			servers: []MCPServer{{
				Name: "server", Transport: MCPTransportStdio, Command: "fixture-mcp", URL: "https://example.invalid/mcp",
			}},
		},
		{
			name: "stdio with headers",
			servers: []MCPServer{{
				Name: "server", Transport: MCPTransportStdio, Command: "fixture-mcp", Headers: map[string]string{"Authorization": "secret"},
			}},
		},
		{
			name:    "http missing url",
			servers: []MCPServer{{Name: "server", Transport: MCPTransportHTTP}},
		},
		{
			name: "http with command",
			servers: []MCPServer{{
				Name: "server", Transport: MCPTransportHTTP, Command: "fixture-mcp", URL: "https://example.invalid/mcp",
			}},
		},
		{
			name: "http with args",
			servers: []MCPServer{{
				Name: "server", Transport: MCPTransportHTTP, Args: []string{"--stdio"}, URL: "https://example.invalid/mcp",
			}},
		},
		{
			name: "http with env",
			servers: []MCPServer{{
				Name: "server", Transport: MCPTransportHTTP, Env: map[string]string{"TOKEN": "secret"}, URL: "https://example.invalid/mcp",
			}},
		},
		{
			name: "sse with stdio fields",
			servers: []MCPServer{{
				Name: "server", Transport: MCPTransportSSE, Command: "fixture-mcp", Args: []string{"--stdio"}, Env: map[string]string{"TOKEN": "secret"}, URL: "https://example.invalid/mcp",
			}},
		},
		{
			name:    "empty name",
			servers: []MCPServer{{Transport: MCPTransportStdio, Command: "fixture-mcp"}},
		},
		{
			name:    "invalid UTF-8 name",
			servers: []MCPServer{{Name: string([]byte{0xff}), Transport: MCPTransportStdio, Command: "fixture-mcp"}},
		},
		{
			name: "duplicate external name",
			servers: []MCPServer{
				{Name: "server", Transport: MCPTransportStdio, Command: "first"},
				{Name: "server", Transport: MCPTransportStdio, Command: "second"},
			},
		},
		{
			name:    "conflicts with SDK name",
			sdk:     []string{"server"},
			servers: []MCPServer{{Name: "server", Transport: MCPTransportStdio, Command: "fixture-mcp"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireMCPLaunchRejected(t, tc.sdk, tc.servers)
		})
	}
}

func TestExternalMCPValidationDoesNotExposeSecretValues(t *testing.T) {
	const secret = "TOP-SECRET-MCP-VALUE"
	for _, tc := range []struct {
		name    string
		servers []MCPServer
	}{
		{
			name: "stdio environment",
			servers: []MCPServer{{
				Name: "server", Transport: MCPTransportStdio, Command: "fixture-mcp",
				Env: map[string]string{"TOKEN": secret}, URL: "https://example.invalid/mcp",
			}},
		},
		{
			name: "http headers",
			servers: []MCPServer{{
				Name: "server", Transport: MCPTransportHTTP, Command: "fixture-mcp",
				Headers: map[string]string{"Authorization": "Bearer " + secret}, URL: "https://example.invalid/mcp",
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := requireMCPLaunchRejected(t, nil, tc.servers)
			require.NotContains(t, err.Error(), secret)
		})
	}
}

func TestExternalMCPOptionsCopyCallerData(t *testing.T) {
	args := []string{"--stdio"}
	env := map[string]string{"LOCAL_TOKEN": "${LOCAL_TOKEN}"}
	headers := map[string]string{"Authorization": "Bearer ${REMOTE_TOKEN}"}
	servers := []MCPServer{
		{
			Name:      "local",
			Transport: MCPTransportStdio,
			Command:   "fixture-mcp",
			Args:      args,
			Env:       env,
		},
		{
			Name:      "remote",
			Transport: MCPTransportHTTP,
			URL:       "https://example.invalid/mcp",
			Headers:   headers,
		},
	}
	c := openFixture(t, "echo", func(options *Options) {
		options.MCPServers = servers
	})

	servers[0].Name = "changed"
	args[0] = "--changed"
	env["LOCAL_TOKEN"] = "changed"
	headers["Authorization"] = "changed"
	servers[1].Name = "changed"

	require.Equal(t, "local", c.opts.MCPServers[0].Name)
	require.Equal(t, []string{"--stdio"}, c.opts.MCPServers[0].Args)
	require.Equal(t, "${LOCAL_TOKEN}", c.opts.MCPServers[0].Env["LOCAL_TOKEN"])
	require.Equal(t, "remote", c.opts.MCPServers[1].Name)
	require.Equal(t, "Bearer ${REMOTE_TOKEN}", c.opts.MCPServers[1].Headers["Authorization"])
}

func requireMCPLaunchRejected(t *testing.T, sdk []string, servers []MCPServer) error {
	t.Helper()
	opts := fixtureOptions(t, "echo")
	capture := filepath.Join(opts.WorkDir, "must-not-start.json")
	opts.CLIPath = filepath.Join(opts.WorkDir, "must-not-exist")
	opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
	opts.SDKMCPServers = sdk
	opts.MCPServers = servers
	c, err := openTestInteraction(context.Background(), opts)
	require.Nil(t, c)
	require.ErrorIs(t, err, ErrInvalidInput)
	_, statErr := os.Stat(capture)
	require.ErrorIs(t, statErr, os.ErrNotExist)
	return err
}
