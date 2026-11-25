package agentpi

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/mcp"
)

func TestSessionMCPRejectsInvalidConfiguration(t *testing.T) {
	stdio := mcp.Server{Name: "tools", Transport: mcp.TransportStdio, Command: "mcp-fixture"}
	http := mcp.Server{Name: "http", Transport: mcp.TransportHTTP, URL: "http://127.0.0.1/mcp"}
	for _, tc := range []struct {
		name    string
		servers []mcp.Server
		code    agent.ErrorCode
	}{
		{name: "normalized collision", servers: []mcp.Server{
			{Name: "some-tools", Transport: stdio.Transport, Command: stdio.Command},
			{Name: "some_tools", Transport: stdio.Transport, Command: stdio.Command},
		}, code: agent.ErrorInvalidArgument},
		{name: "unreadable file", servers: []mcp.Server{{
			Name: stdio.Name, Transport: stdio.Transport, Command: stdio.Command,
			EnvFrom: []mcp.ImportSource{{FileRef: &mcp.FileRef{Path: "missing.env"}}},
		}}, code: agent.ErrorInvalidArgument},
		{name: "duplicate auth header", servers: []mcp.Server{{
			Name: http.Name, Transport: http.Transport, URL: http.URL,
			Headers: []mcp.NamedValue{mcp.Literal("authorization", "sensitive-test-literal")}, BearerTokenEnvVar: "TEST_TOKEN",
		}}, code: agent.ErrorInvalidArgument},
		{name: "invalid scheme", servers: []mcp.Server{{
			Name: http.Name, Transport: http.Transport,
			URL: "file:///sensitive-test-literal",
		}}, code: agent.ErrorInvalidArgument},
		{name: "legacy transport", servers: []mcp.Server{{Name: http.Name, Transport: mcp.TransportSSE, URL: http.URL}}, code: agent.ErrorInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core := New(Config{})
			_, err := core.Start(t.Context())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, core.Stop(t.Context())) })
			_, err = core.Open(t.Context(), agent.OpenOptions{Cwd: t.TempDir(), MCPServers: tc.servers, MCPFiles: fstest.MapFS{}})
			var adapterErr *agent.Error
			require.ErrorAs(t, err, &adapterErr)
			require.Equal(t, tc.code, adapterErr.Code)
			require.NotContains(t, err.Error(), "sensitive-test-literal")
		})
	}
}
