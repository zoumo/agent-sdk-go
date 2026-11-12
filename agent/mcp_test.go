package agent_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/mcp"
)

func TestOpenOptionsValidateAcceptsMCPTransportShapes(t *testing.T) {
	tests := []struct {
		name    string
		servers []mcp.Server
	}{
		{
			name: "stdio",
			servers: []mcp.Server{
				{
					Name:      "local-tools",
					Transport: mcp.TransportStdio,
					Command:   "/usr/local/bin/local-tools",
					Args:      []string{"serve", "--stdio"},
					Env:       map[string]string{"LOCAL_TOOLS_TOKEN": "synthetic-token"},
					Cwd:       "/synthetic/workspace",
				},
			},
		},
		{
			name: "streamable HTTP",
			servers: []mcp.Server{
				{
					Name:              "remote-tools",
					Transport:         mcp.TransportHTTP,
					URL:               "https://mcp.example.test/tools",
					Headers:           map[string]string{"X-Workspace": "synthetic"},
					EnvHeaders:        map[string]string{"Authorization": "REMOTE_TOOLS_AUTHORIZATION"},
					BearerTokenEnvVar: "REMOTE_TOOLS_TOKEN",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := agent.OpenOptions{
				Cwd:        t.TempDir(),
				MCPServers: tt.servers,
			}

			require.NoError(t, options.Validate())
		})
	}
}

func TestMCPServersZeroValueIsEmptyAndInheritedByResume(t *testing.T) {
	options := agent.OpenOptions{Cwd: t.TempDir()}
	require.Empty(t, options.MCPServers)
	require.NoError(t, options.Validate())

	resume := agent.ResumeOptions{
		SessionID:   "session-1",
		OpenOptions: options,
	}
	require.Empty(t, resume.MCPServers)
	require.NoError(t, resume.Validate())

	assert.Nil(t, resume.MCPServers)
}

func TestResumeOptionsValidatesEmbeddedMCPServers(t *testing.T) {
	options := agent.ResumeOptions{
		SessionID: "session-1",
		OpenOptions: agent.OpenOptions{
			Cwd: t.TempDir(),
			MCPServers: []mcp.Server{
				{Transport: mcp.TransportStdio, Command: "server"},
			},
		},
	}

	err := options.Validate()
	require.Error(t, err)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))

	var classified *agent.Error
	require.ErrorAs(t, err, &classified)
	assert.Equal(t, "mcp_server_0_name_required", classified.Reason)
}

func TestOpenOptionsValidateRejectsInvalidMCPServerIdentityAndTransport(t *testing.T) {
	tests := []struct {
		name       string
		servers    []mcp.Server
		wantReason string
		wantText   string
	}{
		{
			name: "empty name",
			servers: []mcp.Server{
				{Transport: mcp.TransportStdio, Command: "server"},
			},
			wantReason: "mcp_server_0_name_required",
			wantText:   "MCP server 0 field name",
		},
		{
			name: "invalid UTF-8 name",
			servers: []mcp.Server{
				{Name: string([]byte{0xff}), Transport: mcp.TransportStdio, Command: "server"},
			},
			wantReason: "mcp_server_0_name_invalid_utf8",
			wantText:   "MCP server 0 field name",
		},
		{
			name: "dotted name",
			servers: []mcp.Server{
				{Name: "tools.local", Transport: mcp.TransportStdio, Command: "server"},
			},
			wantReason: "mcp_server_0_name_dot",
			wantText:   "MCP server 0 field name",
		},
		{
			name: "duplicate name",
			servers: []mcp.Server{
				{Name: "tools", Transport: mcp.TransportStdio, Command: "server"},
				{Name: "tools", Transport: mcp.TransportHTTP, URL: "https://example.test/mcp"},
			},
			wantReason: "mcp_server_1_name_duplicate",
			wantText:   "MCP server 1 field name",
		},
		{
			name: "missing transport",
			servers: []mcp.Server{
				{Name: "tools", Command: "server"},
			},
			wantReason: "mcp_server_0_transport_required",
			wantText:   "MCP server 0 field transport",
		},
		{
			name: "unknown transport",
			servers: []mcp.Server{
				{Name: "tools", Transport: mcp.TransportSSE, URL: "https://example.test/mcp"},
			},
			wantReason: "mcp_server_0_transport_unknown",
			wantText:   "MCP server 0 field transport",
		},
		{
			name: "stdio missing command",
			servers: []mcp.Server{
				{Name: "tools", Transport: mcp.TransportStdio},
			},
			wantReason: "mcp_server_0_command_required",
			wantText:   "MCP server 0 field command",
		},
		{
			name: "HTTP missing URL",
			servers: []mcp.Server{
				{Name: "tools", Transport: mcp.TransportHTTP},
			},
			wantReason: "mcp_server_0_url_required",
			wantText:   "MCP server 0 field url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := agent.OpenOptions{
				Cwd:        t.TempDir(),
				MCPServers: tt.servers,
			}

			err := options.Validate()
			require.Error(t, err)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
			assert.Contains(t, err.Error(), tt.wantText)

			var classified *agent.Error
			require.ErrorAs(t, err, &classified)
			assert.Equal(t, tt.wantReason, classified.Reason)
		})
	}
}

func TestOpenOptionsValidateRejectsMCPFieldsFromTheOtherTransport(t *testing.T) {
	tests := []struct {
		name       string
		server     mcp.Server
		wantReason string
		wantField  string
	}{
		{
			name: "stdio URL",
			server: mcp.Server{
				Name:      "tools",
				Transport: mcp.TransportStdio,
				Command:   "server",
				URL:       "https://example.test/mcp",
			},
			wantReason: "mcp_server_0_url_forbidden",
			wantField:  "url",
		},
		{
			name: "stdio headers",
			server: mcp.Server{
				Name:      "tools",
				Transport: mcp.TransportStdio,
				Command:   "server",
				Headers:   map[string]string{"Authorization": "Bearer synthetic"},
			},
			wantReason: "mcp_server_0_headers_forbidden",
			wantField:  "headers",
		},
		{
			name: "stdio environment headers",
			server: mcp.Server{
				Name:       "tools",
				Transport:  mcp.TransportStdio,
				Command:    "server",
				EnvHeaders: map[string]string{"Authorization": "TOOLS_TOKEN"},
			},
			wantReason: "mcp_server_0_env_headers_forbidden",
			wantField:  "env_headers",
		},
		{
			name: "stdio bearer token environment variable",
			server: mcp.Server{
				Name:              "tools",
				Transport:         mcp.TransportStdio,
				Command:           "server",
				BearerTokenEnvVar: "TOOLS_TOKEN",
			},
			wantReason: "mcp_server_0_bearer_token_env_var_forbidden",
			wantField:  "bearer_token_env_var",
		},
		{
			name: "HTTP command",
			server: mcp.Server{
				Name:      "tools",
				Transport: mcp.TransportHTTP,
				URL:       "https://example.test/mcp",
				Command:   "server",
			},
			wantReason: "mcp_server_0_command_forbidden",
			wantField:  "command",
		},
		{
			name: "HTTP args",
			server: mcp.Server{
				Name:      "tools",
				Transport: mcp.TransportHTTP,
				URL:       "https://example.test/mcp",
				Args:      []string{"serve"},
			},
			wantReason: "mcp_server_0_args_forbidden",
			wantField:  "args",
		},
		{
			name: "HTTP environment",
			server: mcp.Server{
				Name:      "tools",
				Transport: mcp.TransportHTTP,
				URL:       "https://example.test/mcp",
				Env:       map[string]string{"TOOLS_TOKEN": "synthetic"},
			},
			wantReason: "mcp_server_0_env_forbidden",
			wantField:  "env",
		},
		{
			name: "HTTP working directory",
			server: mcp.Server{
				Name:      "tools",
				Transport: mcp.TransportHTTP,
				URL:       "https://example.test/mcp",
				Cwd:       "/synthetic/workspace",
			},
			wantReason: "mcp_server_0_cwd_forbidden",
			wantField:  "cwd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := agent.OpenOptions{
				Cwd:        t.TempDir(),
				MCPServers: []mcp.Server{tt.server},
			}

			err := options.Validate()
			require.Error(t, err)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
			assert.Contains(t, err.Error(), "MCP server 0 field "+tt.wantField)

			var classified *agent.Error
			require.ErrorAs(t, err, &classified)
			assert.Equal(t, tt.wantReason, classified.Reason)
		})
	}
}

func TestOpenOptionsValidateRejectsInvalidMCPUTF8(t *testing.T) {
	invalid := string([]byte{0xff})
	tests := []struct {
		name       string
		server     mcp.Server
		wantReason string
		wantField  string
	}{
		{
			name: "stdio command",
			server: mcp.Server{
				Name:      "tools",
				Transport: mcp.TransportStdio,
				Command:   invalid,
			},
			wantReason: "mcp_server_0_command_invalid_utf8",
			wantField:  "command",
		},
		{
			name: "stdio argument",
			server: mcp.Server{
				Name:      "tools",
				Transport: mcp.TransportStdio,
				Command:   "server",
				Args:      []string{"serve", invalid},
			},
			wantReason: "mcp_server_0_args_invalid_utf8",
			wantField:  "args",
		},
		{
			name: "stdio environment key",
			server: mcp.Server{
				Name:      "tools",
				Transport: mcp.TransportStdio,
				Command:   "server",
				Env:       map[string]string{invalid: "value"},
			},
			wantReason: "mcp_server_0_env_key_invalid_utf8",
			wantField:  "env key",
		},
		{
			name: "stdio environment value",
			server: mcp.Server{
				Name:      "tools",
				Transport: mcp.TransportStdio,
				Command:   "server",
				Env:       map[string]string{"TOOLS_TOKEN": invalid},
			},
			wantReason: "mcp_server_0_env_value_invalid_utf8",
			wantField:  "env value",
		},
		{
			name: "stdio working directory",
			server: mcp.Server{
				Name:      "tools",
				Transport: mcp.TransportStdio,
				Command:   "server",
				Cwd:       invalid,
			},
			wantReason: "mcp_server_0_cwd_invalid_utf8",
			wantField:  "cwd",
		},
		{
			name: "HTTP URL",
			server: mcp.Server{
				Name:      "tools",
				Transport: mcp.TransportHTTP,
				URL:       invalid,
			},
			wantReason: "mcp_server_0_url_invalid_utf8",
			wantField:  "url",
		},
		{
			name: "HTTP header key",
			server: mcp.Server{
				Name:      "tools",
				Transport: mcp.TransportHTTP,
				URL:       "https://example.test/mcp",
				Headers:   map[string]string{invalid: "value"},
			},
			wantReason: "mcp_server_0_headers_key_invalid_utf8",
			wantField:  "headers key",
		},
		{
			name: "HTTP header value",
			server: mcp.Server{
				Name:      "tools",
				Transport: mcp.TransportHTTP,
				URL:       "https://example.test/mcp",
				Headers:   map[string]string{"Authorization": invalid},
			},
			wantReason: "mcp_server_0_headers_value_invalid_utf8",
			wantField:  "headers value",
		},
		{
			name: "HTTP environment header key",
			server: mcp.Server{
				Name:       "tools",
				Transport:  mcp.TransportHTTP,
				URL:        "https://example.test/mcp",
				EnvHeaders: map[string]string{invalid: "TOOLS_TOKEN"},
			},
			wantReason: "mcp_server_0_env_headers_key_invalid_utf8",
			wantField:  "env_headers key",
		},
		{
			name: "HTTP environment header value",
			server: mcp.Server{
				Name:       "tools",
				Transport:  mcp.TransportHTTP,
				URL:        "https://example.test/mcp",
				EnvHeaders: map[string]string{"Authorization": invalid},
			},
			wantReason: "mcp_server_0_env_headers_value_invalid_utf8",
			wantField:  "env_headers value",
		},
		{
			name: "HTTP bearer token environment variable",
			server: mcp.Server{
				Name:              "tools",
				Transport:         mcp.TransportHTTP,
				URL:               "https://example.test/mcp",
				BearerTokenEnvVar: invalid,
			},
			wantReason: "mcp_server_0_bearer_token_env_var_invalid_utf8",
			wantField:  "bearer_token_env_var",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := agent.OpenOptions{
				Cwd:        t.TempDir(),
				MCPServers: []mcp.Server{tt.server},
			}

			err := options.Validate()
			require.Error(t, err)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
			assert.Contains(t, err.Error(), "MCP server 0 field "+tt.wantField)

			var classified *agent.Error
			require.ErrorAs(t, err, &classified)
			assert.Equal(t, tt.wantReason, classified.Reason)
		})
	}
}

func TestMCPValidationDoesNotExposeSecretValues(t *testing.T) {
	const sentinel = "mcp-secret-sentinel-7f0267e2"
	tests := []struct {
		name    string
		servers []mcp.Server
	}{
		{
			name: "stdio",
			servers: []mcp.Server{
				{
					Name:      "local-tools",
					Transport: mcp.TransportStdio,
					Command:   "server",
					Env:       map[string]string{"TOOLS_TOKEN": sentinel},
				},
				{Transport: mcp.TransportStdio, Command: "invalid-server"},
			},
		},
		{
			name: "HTTP",
			servers: []mcp.Server{
				{
					Name:      "remote-tools",
					Transport: mcp.TransportHTTP,
					URL:       "https://example.test/mcp",
					Headers:   map[string]string{"Authorization": sentinel},
					EnvHeaders: map[string]string{
						"X-Token": sentinel,
					},
					BearerTokenEnvVar: sentinel,
				},
				{Transport: mcp.TransportHTTP, URL: "https://example.test/invalid"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := agent.OpenOptions{
				Cwd:        t.TempDir(),
				MCPServers: tt.servers,
			}
			err := options.Validate()
			require.Error(t, err)
			assert.NotContains(t, err.Error(), sentinel)
			assert.NotContains(t, fmt.Sprintf("%+v", err), sentinel)

			var classified *agent.Error
			require.ErrorAs(t, err, &classified)

			event := agent.Event{
				EventID:   "7c9b2c0e-3f2a-4a25-9d7d-84658fb76b10",
				SessionID: "session-1",
				Time:      time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC),
				Payload: agent.SessionErrorPayload{
					Message: "MCP configuration rejected",
					Reason:  classified.Reason,
				},
				Meta: map[string]any{
					"errorCode": agent.CodeOf(err),
					"reason":    classified.Reason,
				},
			}
			encoded, encodeErr := json.Marshal(event)
			require.NoError(t, encodeErr)
			assert.NotContains(t, string(encoded), sentinel)

			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			logger.Error(
				"MCP configuration rejected",
				"error", err,
				"reason", classified.Reason,
			)
			assert.NotContains(t, logs.String(), sentinel)
		})
	}
}
