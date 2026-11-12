// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/mcp"
)

func TestExternalMCPStdioArguments(t *testing.T) {
	argv := captureArgs(t, func(options *Options) {
		options.MCPServers = []mcp.Server{{
			Name:      "local",
			Transport: mcp.TransportStdio,
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
		transport mcp.Transport
	}{
		{name: "http", transport: mcp.TransportHTTP},
		{name: "sse", transport: mcp.TransportSSE},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argv := captureArgs(t, func(options *Options) {
				options.StrictMCPConfig = true
				options.MCPServers = []mcp.Server{{
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
		options.MCPServers = []mcp.Server{{
			Name:      "remote",
			Transport: mcp.TransportHTTP,
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
		servers []mcp.Server
	}{
		{
			name:    "empty transport",
			servers: []mcp.Server{{Name: "server", Command: "fixture-mcp"}},
		},
		{
			name:    "unknown transport",
			servers: []mcp.Server{{Name: "server", Transport: mcp.Transport("oauth"), Command: "fixture-mcp"}},
		},
		{
			name:    "stdio missing command",
			servers: []mcp.Server{{Name: "server", Transport: mcp.TransportStdio}},
		},
		{
			name: "stdio with url",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportStdio, Command: "fixture-mcp", URL: "https://example.invalid/mcp",
			}},
		},
		{
			name: "stdio with headers",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportStdio, Command: "fixture-mcp", Headers: map[string]string{"Authorization": "secret"},
			}},
		},
		{
			name:    "http missing url",
			servers: []mcp.Server{{Name: "server", Transport: mcp.TransportHTTP}},
		},
		{
			name: "stdio with cwd",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportStdio, Command: "fixture-mcp", Cwd: "/synthetic",
			}},
		},
		{
			name: "http with env headers",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportHTTP, URL: "https://example.invalid/mcp",
				EnvHeaders: map[string]string{"X-API-Key": "API_KEY"},
			}},
		},
		{
			name: "http with bearer token environment variable",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportHTTP, URL: "https://example.invalid/mcp",
				BearerTokenEnvVar: "TOKEN",
			}},
		},
		{
			name: "http with command",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportHTTP, Command: "fixture-mcp", URL: "https://example.invalid/mcp",
			}},
		},
		{
			name: "http with args",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportHTTP, Args: []string{"--stdio"}, URL: "https://example.invalid/mcp",
			}},
		},
		{
			name: "http with env",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportHTTP, Env: map[string]string{"TOKEN": "secret"}, URL: "https://example.invalid/mcp",
			}},
		},
		{
			name: "sse with stdio fields",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportSSE, Command: "fixture-mcp", Args: []string{"--stdio"}, Env: map[string]string{"TOKEN": "secret"}, URL: "https://example.invalid/mcp",
			}},
		},
		{
			name:    "empty name",
			servers: []mcp.Server{{Transport: mcp.TransportStdio, Command: "fixture-mcp"}},
		},
		{
			name:    "invalid UTF-8 name",
			servers: []mcp.Server{{Name: string([]byte{0xff}), Transport: mcp.TransportStdio, Command: "fixture-mcp"}},
		},
		{
			name: "duplicate external name",
			servers: []mcp.Server{
				{Name: "server", Transport: mcp.TransportStdio, Command: "first"},
				{Name: "server", Transport: mcp.TransportStdio, Command: "second"},
			},
		},
		{
			name:    "conflicts with SDK name",
			sdk:     []string{"server"},
			servers: []mcp.Server{{Name: "server", Transport: mcp.TransportStdio, Command: "fixture-mcp"}},
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
		servers []mcp.Server
	}{
		{
			name: "stdio environment",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportStdio, Command: "fixture-mcp",
				Env: map[string]string{"TOKEN": secret}, URL: "https://example.invalid/mcp",
			}},
		},
		{
			name: "http headers",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportHTTP, Command: "fixture-mcp",
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
	servers := []mcp.Server{
		{
			Name:      "local",
			Transport: mcp.TransportStdio,
			Command:   "fixture-mcp",
			Args:      args,
			Env:       env,
		},
		{
			Name:      "remote",
			Transport: mcp.TransportHTTP,
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

func requireMCPLaunchRejected(t *testing.T, sdk []string, servers []mcp.Server) error {
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
