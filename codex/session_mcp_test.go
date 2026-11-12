package codex_test

import (
	"context"
	"encoding/json"
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/mcp"
)

func TestSessionExternalMCPStdioStart(t *testing.T) {
	f := startFake(t, "normal", codex.Limits{})
	rawConfig := map[string]json.RawMessage{
		"model_reasoning_effort": json.RawMessage(`"low"`),
	}
	servers := []mcp.Server{{
		Name:      "local",
		Transport: mcp.TransportStdio,
		Command:   "fixture-mcp",
		Args:      []string{"--stdio", "literal value"},
		Env:       map[string]string{"FIXTURE_TOKEN": "${FIXTURE_TOKEN}"},
		Cwd:       "/synthetic/mcp",
	}}
	wantRawConfig := maps.Clone(rawConfig)
	wantServers := []mcp.Server{{
		Name:      "local",
		Transport: mcp.TransportStdio,
		Command:   "fixture-mcp",
		Args:      []string{"--stdio", "literal value"},
		Env:       map[string]string{"FIXTURE_TOKEN": "${FIXTURE_TOKEN}"},
		Cwd:       "/synthetic/mcp",
	}}

	s, err := codex.Open(context.Background(), codex.SessionOptions{
		Client:     f.c,
		Thread:     types.ThreadStartParams{Config: types.Some(rawConfig)},
		Features:   map[codex.Feature]bool{codex.FeatureShellTool: false},
		MCPServers: servers,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	config := configFromFrame(t, requireFrame(t, f, "thread/start"))
	mcpConfig, ok := config["mcp_servers.local"]
	assert.True(t, ok)
	delete(config, "mcp_servers.local")
	assert.Equal(t, map[string]json.RawMessage{
		"model_reasoning_effort": json.RawMessage(`"low"`),
		"features.shell_tool":    json.RawMessage(`false`),
	}, config)
	assert.JSONEq(t, `{"command":"fixture-mcp","args":["--stdio","literal value"],"env":{"FIXTURE_TOKEN":"${FIXTURE_TOKEN}"},"cwd":"/synthetic/mcp"}`, string(mcpConfig))
	assert.Equal(t, wantRawConfig, rawConfig)
	assert.Equal(t, wantServers, servers)
}

func TestSessionExternalMCPHTTPResume(t *testing.T) {
	f := startFake(t, "resume-normal", codex.Limits{})
	rawConfig := map[string]json.RawMessage{
		"model_reasoning_effort": json.RawMessage(`"high"`),
	}
	resume := &types.ThreadResumeParams{
		ThreadID: resumedNativeID,
		Config:   types.Some(rawConfig),
	}
	servers := []mcp.Server{{
		Name:              "remote",
		Transport:         mcp.TransportHTTP,
		URL:               "https://example.invalid/mcp",
		Headers:           map[string]string{"Authorization": "Bearer ${REMOTE_TOKEN}"},
		EnvHeaders:        map[string]string{"X-API-Key": "REMOTE_API_KEY"},
		BearerTokenEnvVar: "REMOTE_BEARER_TOKEN",
	}}
	wantRawConfig := maps.Clone(rawConfig)
	wantServers := []mcp.Server{{
		Name:              "remote",
		Transport:         mcp.TransportHTTP,
		URL:               "https://example.invalid/mcp",
		Headers:           map[string]string{"Authorization": "Bearer ${REMOTE_TOKEN}"},
		EnvHeaders:        map[string]string{"X-API-Key": "REMOTE_API_KEY"},
		BearerTokenEnvVar: "REMOTE_BEARER_TOKEN",
	}}

	s, err := codex.Open(context.Background(), codex.SessionOptions{
		Client:     f.c,
		Resume:     resume,
		Tools:      map[codex.Tool]bool{codex.ToolUpdatePlan: false},
		MCPServers: servers,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	config := configFromFrame(t, requireFrame(t, f, "thread/resume"))
	mcpConfig, ok := config["mcp_servers.remote"]
	assert.True(t, ok)
	delete(config, "mcp_servers.remote")
	assert.Equal(t, map[string]json.RawMessage{
		"model_reasoning_effort":    json.RawMessage(`"high"`),
		"tools.update_plan.enabled": json.RawMessage(`false`),
	}, config)
	assert.JSONEq(t, `{"url":"https://example.invalid/mcp","http_headers":{"Authorization":"Bearer ${REMOTE_TOKEN}"},"env_http_headers":{"X-API-Key":"REMOTE_API_KEY"},"bearer_token_env_var":"REMOTE_BEARER_TOKEN"}`, string(mcpConfig))
	assert.Equal(t, wantRawConfig, rawConfig)
	callerConfig, ok := resume.Config.Get()
	assert.True(t, ok)
	assert.Equal(t, wantRawConfig, callerConfig)
	assert.Equal(t, wantServers, servers)
}

func TestSessionExternalMCPRejectsInvalidConfiguration(t *testing.T) {
	const secret = "MCP-SECRET-MUST-NOT-LEAK"
	f := startFake(t, "normal", codex.Limits{})
	cases := []struct {
		name       string
		servers    []mcp.Server
		wantFields []string
	}{
		{
			name:       "empty name",
			servers:    []mcp.Server{{Transport: mcp.TransportStdio, Command: "fixture-mcp"}},
			wantFields: []string{"name"},
		},
		{
			name:       "invalid UTF-8 name",
			servers:    []mcp.Server{{Name: string([]byte{0xff}), Transport: mcp.TransportStdio, Command: "fixture-mcp"}},
			wantFields: []string{"name"},
		},
		{
			name:       "dotted name",
			servers:    []mcp.Server{{Name: "local.fixture", Transport: mcp.TransportStdio, Command: "fixture-mcp"}},
			wantFields: []string{"name", "dot"},
		},
		{
			name: "duplicate name",
			servers: []mcp.Server{
				{Name: "local", Transport: mcp.TransportStdio, Command: "first"},
				{Name: "local", Transport: mcp.TransportStdio, Command: "second"},
			},
			wantFields: []string{"name"},
		},
		{
			name:       "missing transport",
			servers:    []mcp.Server{{Name: "server"}},
			wantFields: []string{"transport"},
		},
		{
			name: "unsupported SSE",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportSSE, URL: "https://example.invalid/sse",
			}},
			wantFields: []string{"sse", "not supported"},
		},
		{
			name:       "stdio with URL",
			servers:    []mcp.Server{{Name: "server", Transport: mcp.TransportStdio, Command: "fixture-mcp", URL: "https://example.invalid/mcp"}},
			wantFields: []string{"url"},
		},
		{
			name: "stdio with headers",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportStdio, Command: "fixture-mcp", Headers: map[string]string{"Authorization": secret},
			}},
			wantFields: []string{"stdio", "headers"},
		},
		{
			name: "stdio with environment headers",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportStdio, Command: "fixture-mcp", EnvHeaders: map[string]string{"X-API-Key": "API_KEY"},
			}},
			wantFields: []string{"stdio", "env_headers"},
		},
		{
			name: "stdio with bearer token environment variable",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportStdio, Command: "fixture-mcp", BearerTokenEnvVar: secret,
			}},
			wantFields: []string{"stdio", "bearer_token_env_var"},
		},
		{
			name: "HTTP with args",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportHTTP, URL: "https://example.invalid/mcp", Args: []string{secret},
			}},
			wantFields: []string{"HTTP", "args"},
		},
		{
			name: "HTTP with environment",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportHTTP, URL: "https://example.invalid/mcp", Env: map[string]string{"TOKEN": secret},
			}},
			wantFields: []string{"HTTP", "env"},
		},
		{
			name: "HTTP with working directory",
			servers: []mcp.Server{{
				Name: "server", Transport: mcp.TransportHTTP, URL: "https://example.invalid/mcp", Cwd: "/synthetic/" + secret,
			}},
			wantFields: []string{"HTTP", "cwd"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c, MCPServers: tc.servers})
			require.Nil(t, s)
			require.ErrorIs(t, err, codex.ErrInvalidInput)
			assert.NotContains(t, err.Error(), secret)
			for _, field := range tc.wantFields {
				assert.Contains(t, err.Error(), field)
			}
		})
	}
	assertMethods(t, f, "initialize", "initialized")
}

func TestSessionExternalMCPRejectsInvalidUTF8(t *testing.T) {
	const secret = "MCP-UTF8-SECRET-MUST-NOT-LEAK"
	bad := secret + "\xff"
	f := startFake(t, "resume-normal", codex.Limits{})
	cases := []struct {
		name       string
		server     mcp.Server
		wantFields []string
	}{
		{
			name:       "command",
			server:     mcp.Server{Transport: mcp.TransportStdio, Command: bad},
			wantFields: []string{"command"},
		},
		{
			name: "argument",
			server: mcp.Server{
				Transport: mcp.TransportStdio,
				Command:   "fixture-mcp",
				Args:      []string{"--stdio", bad},
			},
			wantFields: []string{"args"},
		},
		{
			name: "environment key",
			server: mcp.Server{
				Transport: mcp.TransportStdio,
				Command:   "fixture-mcp",
				Env:       map[string]string{bad: "VALUE"},
			},
			wantFields: []string{"env key"},
		},
		{
			name: "environment value",
			server: mcp.Server{
				Transport: mcp.TransportStdio,
				Command:   "fixture-mcp",
				Env:       map[string]string{"TOKEN": bad},
			},
			wantFields: []string{"env value"},
		},
		{
			name:       "working directory",
			server:     mcp.Server{Transport: mcp.TransportStdio, Command: "fixture-mcp", Cwd: bad},
			wantFields: []string{"cwd"},
		},
		{
			name:       "URL",
			server:     mcp.Server{Transport: mcp.TransportHTTP, URL: bad},
			wantFields: []string{"url"},
		},
		{
			name: "header key",
			server: mcp.Server{
				Transport: mcp.TransportHTTP,
				URL:       "https://example.invalid/mcp",
				Headers:   map[string]string{bad: "VALUE"},
			},
			wantFields: []string{"headers key"},
		},
		{
			name: "header value",
			server: mcp.Server{
				Transport: mcp.TransportHTTP,
				URL:       "https://example.invalid/mcp",
				Headers:   map[string]string{"Authorization": bad},
			},
			wantFields: []string{"headers value"},
		},
		{
			name: "environment header key",
			server: mcp.Server{
				Transport:  mcp.TransportHTTP,
				URL:        "https://example.invalid/mcp",
				EnvHeaders: map[string]string{bad: "VALUE"},
			},
			wantFields: []string{"env_headers key"},
		},
		{
			name: "environment header value",
			server: mcp.Server{
				Transport:  mcp.TransportHTTP,
				URL:        "https://example.invalid/mcp",
				EnvHeaders: map[string]string{"X-API-Key": bad},
			},
			wantFields: []string{"env_headers value"},
		},
		{
			name: "bearer token environment variable",
			server: mcp.Server{
				Transport:         mcp.TransportHTTP,
				URL:               "https://example.invalid/mcp",
				BearerTokenEnvVar: bad,
			},
			wantFields: []string{"bearer_token_env_var"},
		},
	}
	for _, resume := range []bool{false, true} {
		mode := "start"
		if resume {
			mode = "resume"
		}
		for _, tc := range cases {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				server := tc.server
				server.Name = "server"
				options := codex.SessionOptions{Client: f.c, MCPServers: []mcp.Server{server}}
				if resume {
					options.Resume = &types.ThreadResumeParams{ThreadID: resumedNativeID}
				}

				s, err := codex.Open(context.Background(), options)
				require.Nil(t, s)
				require.ErrorIs(t, err, codex.ErrInvalidInput)
				for _, field := range tc.wantFields {
					assert.Contains(t, err.Error(), field)
				}
				assert.NotContains(t, err.Error(), secret)
				assert.NotContains(t, err.Error(), "\xff")
				assert.NotContains(t, err.Error(), "\uFFFD")
			})
		}
	}
	assertMethods(t, f, "initialize", "initialized")
}

func TestSessionExternalMCPRejectsRawConfigConflicts(t *testing.T) {
	const (
		rawSecret    = "RAW-CONFIG-SECRET-MUST-NOT-LEAK"
		serverSecret = "MCP-FIELD-SECRET-MUST-NOT-LEAK"
	)
	f := startFake(t, "resume-normal", codex.Limits{})
	servers := []mcp.Server{{
		Name:      "remote",
		Transport: mcp.TransportHTTP,
		URL:       "https://example.invalid/mcp",
		Headers:   map[string]string{"Authorization": "Bearer " + serverSecret},
	}}
	for _, rawPath := range []string{
		"mcp_servers",
		"mcp_servers.remote",
		"mcp_servers.remote.url",
	} {
		for _, resume := range []bool{false, true} {
			name := "start/" + rawPath
			if resume {
				name = "resume/" + rawPath
			}
			t.Run(name, func(t *testing.T) {
				raw := map[string]json.RawMessage{
					rawPath: json.RawMessage(`{"secret":"` + rawSecret + `"}`),
				}
				options := codex.SessionOptions{Client: f.c, MCPServers: servers}
				var callerResume *types.ThreadResumeParams
				if resume {
					callerResume = &types.ThreadResumeParams{ThreadID: resumedNativeID, Config: types.Some(raw)}
					options.Resume = callerResume
				} else {
					options.Thread.Config = types.Some(raw)
				}

				var first string
				for range 2 {
					s, err := codex.Open(context.Background(), options)
					require.Nil(t, s)
					require.ErrorIs(t, err, codex.ErrInvalidInput)
					assert.Contains(t, err.Error(), rawPath)
					assert.Contains(t, err.Error(), "mcp_servers.remote")
					assert.NotContains(t, err.Error(), rawSecret)
					assert.NotContains(t, err.Error(), serverSecret)
					if first == "" {
						first = err.Error()
					} else {
						assert.Equal(t, first, err.Error())
					}
				}
				assert.Equal(t, map[string]json.RawMessage{
					rawPath: json.RawMessage(`{"secret":"` + rawSecret + `"}`),
				}, raw)
				if callerResume != nil {
					callerConfig, ok := callerResume.Config.Get()
					assert.True(t, ok)
					assert.Equal(t, raw, callerConfig)
				}
			})
		}
	}
	assertMethods(t, f, "initialize", "initialized")
}

func TestSessionExternalMCPConfigIsolationAndCopies(t *testing.T) {
	f := startFake(t, "session-normal", codex.Limits{})
	rawConfig := map[string]json.RawMessage{
		"model_reasoning_effort": json.RawMessage(`"low"`),
		"mcp_servers.ambient":    json.RawMessage(`{"url":"https://ambient.invalid/mcp"}`),
	}
	args := []string{"--stdio"}
	env := map[string]string{"FIXTURE_TOKEN": "${FIXTURE_TOKEN}"}
	servers := []mcp.Server{{
		Name:      "first",
		Transport: mcp.TransportStdio,
		Command:   "fixture-mcp",
		Args:      args,
		Env:       env,
	}}

	first, err := codex.Open(context.Background(), codex.SessionOptions{
		Client:     f.c,
		Thread:     types.ThreadStartParams{Config: types.Some(rawConfig)},
		MCPServers: servers,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })
	assert.Equal(t, map[string]json.RawMessage{
		"model_reasoning_effort": json.RawMessage(`"low"`),
		"mcp_servers.ambient":    json.RawMessage(`{"url":"https://ambient.invalid/mcp"}`),
	}, rawConfig)
	assert.Equal(t, []mcp.Server{{
		Name:      "first",
		Transport: mcp.TransportStdio,
		Command:   "fixture-mcp",
		Args:      []string{"--stdio"},
		Env:       map[string]string{"FIXTURE_TOKEN": "${FIXTURE_TOKEN}"},
	}}, servers)

	args[0] = "--changed"
	env["FIXTURE_TOKEN"] = "changed"
	servers[0].Name = "changed"
	rawConfig["model_reasoning_effort"] = json.RawMessage(`"changed"`)
	rawConfig["mcp_servers.ambient"] = json.RawMessage(`{"url":"https://changed.invalid/mcp"}`)
	headers := map[string]string{"Authorization": "Bearer ${SECOND_TOKEN}"}
	envHeaders := map[string]string{"X-API-Key": "SECOND_API_KEY"}
	secondServers := []mcp.Server{{
		Name:              "second",
		Transport:         mcp.TransportHTTP,
		URL:               "https://second.invalid/mcp",
		Headers:           headers,
		EnvHeaders:        envHeaders,
		BearerTokenEnvVar: "SECOND_BEARER_TOKEN",
	}}
	second, err := codex.Open(context.Background(), codex.SessionOptions{
		Client:     f.c,
		MCPServers: secondServers,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })
	headers["Authorization"] = "changed"
	envHeaders["X-API-Key"] = "changed"
	secondServers[0].Name = "changed"
	assert.NotEqual(t, first.ThreadID(), second.ThreadID())

	var configs []map[string]json.RawMessage
	for _, frame := range trace(t, f.trace) {
		if frame.Method == "thread/start" {
			configs = append(configs, configFromFrame(t, frame))
		}
	}
	require.Len(t, configs, 2)
	firstWithoutMCP := maps.Clone(configs[0])
	delete(firstWithoutMCP, "mcp_servers.first")
	assert.Equal(t, map[string]json.RawMessage{
		"model_reasoning_effort": json.RawMessage(`"low"`),
		"mcp_servers.ambient":    json.RawMessage(`{"url":"https://ambient.invalid/mcp"}`),
	}, firstWithoutMCP)
	assert.JSONEq(t, `{"command":"fixture-mcp","args":["--stdio"],"env":{"FIXTURE_TOKEN":"${FIXTURE_TOKEN}"}}`, string(configs[0]["mcp_servers.first"]))
	assert.Len(t, configs[1], 1)
	assert.JSONEq(t, `{"url":"https://second.invalid/mcp","http_headers":{"Authorization":"Bearer ${SECOND_TOKEN}"},"env_http_headers":{"X-API-Key":"SECOND_API_KEY"},"bearer_token_env_var":"SECOND_BEARER_TOKEN"}`, string(configs[1]["mcp_servers.second"]))
}
