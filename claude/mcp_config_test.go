// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

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
			Env:       []mcp.NamedValue{mcp.Literal("FIXTURE_TOKEN", "${FIXTURE_TOKEN}")},
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
					Headers:   []mcp.NamedValue{mcp.Literal("Authorization", "Bearer ${REMOTE_TOKEN}")},
				}}
			})

			wantConfig := `{"mcpServers":{"remote":{"headers":{"Authorization":"Bearer ${REMOTE_TOKEN}"},"type":"` + string(tc.transport) + `","url":"https://example.invalid/mcp"}}}`
			want := append(append([]string(nil), baseInteractionArgs...), "--mcp-config="+wantConfig, "--strict-mcp-config")
			require.Equal(t, want, argv)
		})
	}
}

func TestExternalMCPFilesBecomeNativeArgumentMaps(t *testing.T) {
	options := fixtureOptions(t, "echo")
	capture := filepath.Join(options.WorkDir, "mcp-capture.json")
	options.Env = append(options.Env, "CLAUDE_GO_CAPTURE="+capture)
	options.MCPFiles = fstest.MapFS{
		"env":     {Data: []byte("TOKEN='synthetic-env'\nEMPTY=''\n")},
		"headers": {Data: []byte("AUTH='Bearer synthetic-header'\n")},
	}
	options.MCPServers = []mcp.Server{
		{
			Name: "local", Transport: mcp.TransportStdio, Command: "fixture-mcp",
			EnvFrom: []mcp.ImportSource{{FileRef: &mcp.FileRef{Path: "env"}}},
		},
		{
			Name: "remote", Transport: mcp.TransportHTTP, URL: "https://example.invalid/mcp",
			Headers: []mcp.NamedValue{{
				Name: "Authorization",
				ValueFrom: &mcp.ValueSource{FileKeyRef: &mcp.FileKeyRef{
					Path: "headers", Key: "AUTH",
				}},
			}},
		},
	}
	peer, err := openTestInteraction(t.Context(), options)
	require.NoError(t, err)
	t.Cleanup(func() { _ = peer.Abort() })
	data, err := os.ReadFile(capture)
	require.NoError(t, err)
	var captured struct {
		Argv   []string
		MCPEnv []string
	}
	require.NoError(t, json.Unmarshal(data, &captured))
	config, ok := strings.CutPrefix(captured.Argv[len(captured.Argv)-1], "--mcp-config=")
	require.True(t, ok)
	require.NotContains(t, config, "synthetic")
	var native struct {
		Servers map[string]struct {
			Env     map[string]string
			Headers map[string]string
		} `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal([]byte(config), &native))
	require.Empty(t, native.Servers["local"].Env["EMPTY"])
	require.Contains(t, native.Servers["local"].Env, "EMPTY")
	require.ElementsMatch(t, []string{
		strings.Trim(native.Servers["local"].Env["TOKEN"], "${}") + "=synthetic-env",
		strings.Trim(native.Servers["remote"].Headers["Authorization"], "${}") + "=Bearer synthetic-header",
	}, captured.MCPEnv)

	_, err = NativeArgs(SessionOptions(options))
	require.ErrorIs(t, err, ErrInvalidInput)
	prepared, err := PrepareMCP(SessionOptions(options))
	require.NoError(t, err)
	args, err := NativeArgs(prepared)
	require.NoError(t, err)
	require.Contains(t, args[len(args)-1], *prepared.MCPServers[1].Headers[0].Value)
}

func TestExternalMCPRejectsFileExpansionBeforeLaunch(t *testing.T) {
	const source = "secret-${AMBIENT}"
	options := fixtureOptions(t, "echo")
	options.CLIPath = filepath.Join(options.WorkDir, "must-not-exist")
	options.MCPFiles = fstest.MapFS{
		"headers": {Data: []byte("AUTHORIZATION='" + source + "'\n")},
	}
	options.MCPServers = []mcp.Server{{
		Name: "remote", Transport: mcp.TransportHTTP, URL: "https://example.invalid/mcp",
		HeadersFrom: []mcp.ImportSource{{FileRef: &mcp.FileRef{Path: "headers"}}},
	}}
	session, err := Open(t.Context(), SessionOptions(options))
	require.Nil(t, session)
	require.ErrorIs(t, err, ErrInvalidInput)
	require.ErrorContains(t, err, "file-backed headers contains Claude interpolation syntax")
	require.NotContains(t, err.Error(), source)

	options.MCPServers[0].Headers = []mcp.NamedValue{mcp.Literal("Authorization", "${AMBIENT}")}
	prepared, err := PrepareMCP(SessionOptions(options))
	require.NoError(t, err)
	require.Equal(t, options.Env, prepared.Env)
	require.Equal(t, "${AMBIENT}", *prepared.MCPServers[0].Headers[0].Value)
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
				Name: "server", Transport: mcp.TransportStdio, Command: "fixture-mcp", Headers: []mcp.NamedValue{mcp.Literal("Authorization", "secret")},
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
				Name: "server", Transport: mcp.TransportHTTP, Env: []mcp.NamedValue{mcp.Literal("TOKEN", "secret")}, URL: "https://example.invalid/mcp",
			}},
		},
		{
			name: "sse with stdio fields",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportSSE, Command: "fixture-mcp", Args: []string{"--stdio"}, Env: []mcp.NamedValue{mcp.Literal("TOKEN", "secret")}, URL: "https://example.invalid/mcp",
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
				Env: []mcp.NamedValue{mcp.Literal("TOKEN", secret)}, URL: "https://example.invalid/mcp",
			}},
		},
		{
			name: "http headers",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportHTTP, Command: "fixture-mcp",
				Headers: []mcp.NamedValue{mcp.Literal("Authorization", "Bearer "+secret)}, URL: "https://example.invalid/mcp",
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
	env := []mcp.NamedValue{mcp.Literal("LOCAL_TOKEN", "${LOCAL_TOKEN}")}
	headers := []mcp.NamedValue{mcp.Literal("Authorization", "Bearer ${REMOTE_TOKEN}")}
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
	*env[0].Value = "changed"
	*headers[0].Value = "changed"
	servers[1].Name = "changed"

	require.Equal(t, "local", c.opts.MCPServers[0].Name)
	require.Equal(t, []string{"--stdio"}, c.opts.MCPServers[0].Args)
	require.Equal(t, "${LOCAL_TOKEN}", *c.opts.MCPServers[0].Env[0].Value)
	require.Equal(t, "remote", c.opts.MCPServers[1].Name)
	require.Equal(t, "Bearer ${REMOTE_TOKEN}", *c.opts.MCPServers[1].Headers[0].Value)
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
