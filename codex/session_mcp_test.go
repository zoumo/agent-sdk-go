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
)

func TestSessionExternalMCPStdioStart(t *testing.T) {
	f := startFake(t, "normal", codex.Limits{})
	rawConfig := map[string]json.RawMessage{
		"model_reasoning_effort": json.RawMessage(`"low"`),
	}
	servers := []codex.MCPServer{{
		Name:    "local",
		Command: "fixture-mcp",
		Args:    []string{"--stdio", "literal value"},
		Env:     map[string]string{"FIXTURE_TOKEN": "${FIXTURE_TOKEN}"},
		Cwd:     "/synthetic/mcp",
	}}
	wantRawConfig := maps.Clone(rawConfig)
	wantServers := []codex.MCPServer{{
		Name:    "local",
		Command: "fixture-mcp",
		Args:    []string{"--stdio", "literal value"},
		Env:     map[string]string{"FIXTURE_TOKEN": "${FIXTURE_TOKEN}"},
		Cwd:     "/synthetic/mcp",
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
	servers := []codex.MCPServer{{
		Name:              "remote",
		URL:               "https://example.invalid/mcp",
		Headers:           map[string]string{"Authorization": "Bearer ${REMOTE_TOKEN}"},
		EnvHeaders:        map[string]string{"X-API-Key": "REMOTE_API_KEY"},
		BearerTokenEnvVar: "REMOTE_BEARER_TOKEN",
	}}
	wantRawConfig := maps.Clone(rawConfig)
	wantServers := []codex.MCPServer{{
		Name:              "remote",
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
		servers    []codex.MCPServer
		wantFields []string
	}{
		{
			name:       "empty name",
			servers:    []codex.MCPServer{{Command: "fixture-mcp"}},
			wantFields: []string{"name"},
		},
		{
			name:       "invalid UTF-8 name",
			servers:    []codex.MCPServer{{Name: string([]byte{0xff}), Command: "fixture-mcp"}},
			wantFields: []string{"name"},
		},
		{
			name:       "dotted name",
			servers:    []codex.MCPServer{{Name: "local.fixture", Command: "fixture-mcp"}},
			wantFields: []string{"name", "."},
		},
		{
			name: "duplicate name",
			servers: []codex.MCPServer{
				{Name: "local", Command: "first"},
				{Name: "local", Command: "second"},
			},
			wantFields: []string{"name"},
		},
		{
			name:       "missing command and URL",
			servers:    []codex.MCPServer{{Name: "server"}},
			wantFields: []string{"command", "url"},
		},
		{
			name:       "both command and URL",
			servers:    []codex.MCPServer{{Name: "server", Command: "fixture-mcp", URL: "https://example.invalid/mcp"}},
			wantFields: []string{"command", "url"},
		},
		{
			name: "stdio with headers",
			servers: []codex.MCPServer{{
				Name: "server", Command: "fixture-mcp", Headers: map[string]string{"Authorization": secret},
			}},
			wantFields: []string{"stdio", "headers"},
		},
		{
			name: "stdio with environment headers",
			servers: []codex.MCPServer{{
				Name: "server", Command: "fixture-mcp", EnvHeaders: map[string]string{"X-API-Key": "API_KEY"},
			}},
			wantFields: []string{"stdio", "env_headers"},
		},
		{
			name: "stdio with bearer token environment variable",
			servers: []codex.MCPServer{{
				Name: "server", Command: "fixture-mcp", BearerTokenEnvVar: secret,
			}},
			wantFields: []string{"stdio", "bearer_token_env_var"},
		},
		{
			name: "HTTP with args",
			servers: []codex.MCPServer{{
				Name: "server", URL: "https://example.invalid/mcp", Args: []string{secret},
			}},
			wantFields: []string{"streamable HTTP", "args"},
		},
		{
			name: "HTTP with environment",
			servers: []codex.MCPServer{{
				Name: "server", URL: "https://example.invalid/mcp", Env: map[string]string{"TOKEN": secret},
			}},
			wantFields: []string{"streamable HTTP", "env"},
		},
		{
			name: "HTTP with working directory",
			servers: []codex.MCPServer{{
				Name: "server", URL: "https://example.invalid/mcp", Cwd: "/synthetic/" + secret,
			}},
			wantFields: []string{"streamable HTTP", "cwd"},
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

func TestSessionExternalMCPRejectsRawConfigConflicts(t *testing.T) {
	const (
		rawSecret    = "RAW-CONFIG-SECRET-MUST-NOT-LEAK"
		serverSecret = "MCP-FIELD-SECRET-MUST-NOT-LEAK"
	)
	f := startFake(t, "resume-normal", codex.Limits{})
	servers := []codex.MCPServer{{
		Name:    "remote",
		URL:     "https://example.invalid/mcp",
		Headers: map[string]string{"Authorization": "Bearer " + serverSecret},
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
	servers := []codex.MCPServer{{
		Name:    "first",
		Command: "fixture-mcp",
		Args:    args,
		Env:     env,
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
	assert.Equal(t, []codex.MCPServer{{
		Name:    "first",
		Command: "fixture-mcp",
		Args:    []string{"--stdio"},
		Env:     map[string]string{"FIXTURE_TOKEN": "${FIXTURE_TOKEN}"},
	}}, servers)

	args[0] = "--changed"
	env["FIXTURE_TOKEN"] = "changed"
	servers[0].Name = "changed"
	rawConfig["model_reasoning_effort"] = json.RawMessage(`"changed"`)
	rawConfig["mcp_servers.ambient"] = json.RawMessage(`{"url":"https://changed.invalid/mcp"}`)
	headers := map[string]string{"Authorization": "Bearer ${SECOND_TOKEN}"}
	envHeaders := map[string]string{"X-API-Key": "SECOND_API_KEY"}
	secondServers := []codex.MCPServer{{
		Name:              "second",
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
