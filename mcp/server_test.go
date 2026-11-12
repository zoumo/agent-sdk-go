package mcp_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/mcp"
)

func TestValidateServersAcceptsAllSharedTransportShapes(t *testing.T) {
	servers := []mcp.Server{
		{
			Name:      "local",
			Transport: mcp.TransportStdio,
			Command:   "fixture-mcp",
			Args:      []string{"--stdio"},
			Env:       map[string]string{"TOKEN": "${TOKEN}"},
			Cwd:       "/synthetic",
		},
		{
			Name:       "remote",
			Transport:  mcp.TransportHTTP,
			URL:        "https://example.invalid/mcp",
			Headers:    map[string]string{"Authorization": "Bearer ${TOKEN}"},
			EnvHeaders: map[string]string{"X-API-Key": "API_KEY"},
		},
		{
			Name:      "legacy",
			Transport: mcp.TransportSSE,
			URL:       "https://example.invalid/sse",
			Headers:   map[string]string{"Authorization": "Bearer ${TOKEN}"},
		},
	}

	require.NoError(t, mcp.ValidateServers(servers))
}

func TestValidateServersRequiresExplicitTransport(t *testing.T) {
	for _, tc := range []struct {
		name      string
		transport mcp.Transport
		reason    string
	}{
		{name: "missing", reason: "mcp_server_0_transport_required"},
		{name: "unknown", transport: mcp.Transport("oauth"), reason: "mcp_server_0_transport_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := mcp.ValidateServers([]mcp.Server{{
				Name:      "server",
				Transport: tc.transport,
				Command:   "fixture-mcp",
			}})
			var validation *mcp.ValidationError
			require.ErrorAs(t, err, &validation)
			assert.Equal(t, tc.reason, validation.Reason)
			assert.NotContains(t, err.Error(), "fixture-mcp")
		})
	}
}

func TestValidateServersRejectsCrossTransportFields(t *testing.T) {
	for _, tc := range []struct {
		name      string
		server    mcp.Server
		wantField string
	}{
		{
			name: "stdio URL",
			server: mcp.Server{
				Name: "server", Transport: mcp.TransportStdio,
				Command: "fixture-mcp", URL: "https://example.invalid/mcp",
			},
			wantField: "url",
		},
		{
			name: "HTTP command",
			server: mcp.Server{
				Name: "server", Transport: mcp.TransportHTTP,
				URL: "https://example.invalid/mcp", Command: "fixture-mcp",
			},
			wantField: "command",
		},
		{
			name: "SSE cwd",
			server: mcp.Server{
				Name: "server", Transport: mcp.TransportSSE,
				URL: "https://example.invalid/sse", Cwd: "/synthetic",
			},
			wantField: "cwd",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := mcp.ValidateServers([]mcp.Server{tc.server})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantField)
		})
	}
}

func TestValidateServersRejectsInvalidNamesWithoutLeakingValues(t *testing.T) {
	const secret = "MCP-CONFIG-SECRET"
	for _, tc := range []struct {
		name   string
		server mcp.Server
		reason string
	}{
		{
			name:   "empty",
			server: mcp.Server{Transport: mcp.TransportStdio, Command: "fixture-mcp"},
			reason: "mcp_server_0_name_required",
		},
		{
			name:   "dot",
			server: mcp.Server{Name: "tools.local", Transport: mcp.TransportStdio, Command: "fixture-mcp"},
			reason: "mcp_server_0_name_dot",
		},
		{
			name:   "duplicate",
			server: mcp.Server{Name: "tools", Transport: mcp.TransportStdio, Command: "fixture-mcp"},
			reason: "mcp_server_1_name_duplicate",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			servers := []mcp.Server{tc.server}
			if tc.reason == "mcp_server_1_name_duplicate" {
				servers = append(servers, mcp.Server{
					Name:      "tools",
					Transport: mcp.TransportHTTP,
					URL:       "https://example.invalid/" + secret,
				})
			}
			err := mcp.ValidateServers(servers)
			var validation *mcp.ValidationError
			require.ErrorAs(t, err, &validation)
			assert.Equal(t, tc.reason, validation.Reason)
			assert.NotContains(t, err.Error(), secret)
		})
	}
}

func TestCloneServersCopiesNestedValues(t *testing.T) {
	args := []string{"--stdio"}
	env := map[string]string{"TOKEN": "${TOKEN}"}
	headers := map[string]string{"Authorization": "Bearer ${TOKEN}"}
	envHeaders := map[string]string{"X-API-Key": "API_KEY"}
	servers := []mcp.Server{{
		Name:       "server",
		Transport:  mcp.TransportStdio,
		Command:    "fixture-mcp",
		Args:       args,
		Env:        env,
		Headers:    headers,
		EnvHeaders: envHeaders,
	}}

	cloned := mcp.CloneServers(servers)
	require.Len(t, cloned, 1)
	args[0] = "--changed"
	env["TOKEN"] = "changed"
	headers["Authorization"] = "changed"
	envHeaders["X-API-Key"] = "changed"
	servers[0].Name = "changed"

	assert.Equal(t, []string{"--stdio"}, cloned[0].Args)
	assert.Equal(t, "${TOKEN}", cloned[0].Env["TOKEN"])
	assert.Equal(t, "Bearer ${TOKEN}", cloned[0].Headers["Authorization"])
	assert.Equal(t, "API_KEY", cloned[0].EnvHeaders["X-API-Key"])
	assert.Equal(t, "server", cloned[0].Name)
	assert.Nil(t, mcp.CloneServers(nil))
}
