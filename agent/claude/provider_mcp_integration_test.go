package agentclaude_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/mcp"
)

func TestProviderOpenPassesFinalMCPListToClaudeOptions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"mcp-open","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	var observed []claude.SessionOptions
	provider := newTestClaudeProviderWithObserver(
		t,
		agentclaude.Config{},
		func(options claude.SessionOptions) {
			observed = append(observed, options)
		},
		peerSession,
	)
	t.Cleanup(func() { require.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	args := []string{"--stdio", "literal value"}
	envValues := map[string]string{"LOCAL_TOKEN": "${LOCAL_TOKEN}"}
	headers := map[string]string{
		"Authorization": "Bearer ${REMOTE_TOKEN}",
		"X-Workspace":   "synthetic",
	}
	servers := []mcp.Server{
		{
			Name:      "local",
			Transport: mcp.TransportStdio,
			Command:   "fixture-mcp",
			Args:      args,
			Env:       envValues,
		},
		{
			Name:      "remote",
			Transport: mcp.TransportHTTP,
			URL:       "https://example.invalid/mcp",
			Headers:   headers,
		},
	}
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd:        cwd,
		MCPServers: servers,
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)

	require.Len(t, observed, 1)
	argv, err := claude.NativeArgs(observed[0])
	require.NoError(t, err)
	require.JSONEq(t, `{
		"mcpServers": {
			"local": {
				"type": "stdio",
				"command": "fixture-mcp",
				"args": ["--stdio", "literal value"],
				"env": {"LOCAL_TOKEN": "${LOCAL_TOKEN}"}
			},
			"remote": {
				"type": "http",
				"url": "https://example.invalid/mcp",
				"headers": {
					"Authorization": "Bearer ${REMOTE_TOKEN}",
					"X-Workspace": "synthetic"
				}
			}
		}
	}`, claudeMCPConfig(t, argv))
	assert.NotContains(t, argv, "--strict-mcp-config")
	assert.NotContains(t, strings.Join(argv, "\n"), `"type":"sse"`)
	assert.NotContains(t, strings.Join(argv, "\n"), `"type":"sdk"`)

	before := claudeMCPConfig(t, argv)
	servers[0].Command = "mutated"
	args[0] = "--mutated"
	envValues["LOCAL_TOKEN"] = "mutated"
	headers["Authorization"] = "mutated"
	servers[1].Name = "mutated"
	after, err := claude.NativeArgs(observed[0])
	require.NoError(t, err)
	require.JSONEq(t, before, claudeMCPConfig(t, after))
}

func TestProviderResumeRepassesProvidedMCPList(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	openFrames := append([]string(nil), mockClaudeInit...)
	openFrames = append(openFrames,
		`<{"type":"system","subtype":"init","session_id":"mcp-open","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
	)
	openPeer := prepareClaudePeer(t, "passed", openFrames...)
	var observed []claude.SessionOptions
	openProvider := newTestClaudeProviderWithObserver(
		t,
		agentclaude.Config{},
		func(options claude.SessionOptions) {
			observed = append(observed, options)
		},
		openPeer,
	)
	configDir := newResumeConfigDir(t)
	writeResumeTranscript(
		t,
		configDir,
		"project",
		claudeResumePeerSessionID,
		cwd,
	)
	resumeArgv := filepath.Join(t.TempDir(), "resume-argv.json")
	resumePeer := newClaudeResumePeer(t, claudeResumePeerScenario{
		CWD:      cwd,
		Frames:   []string{resumeInitFrame(t, claudeResumePeerSessionID, cwd)},
		ArgvPath: resumeArgv,
	})
	resumeProvider := agentclaude.New(agentclaude.Config{
		CLIPath:   resumePeer.CLIPath,
		Env:       resumePeer.Env,
		ConfigDir: configDir,
	})
	t.Cleanup(func() {
		require.NoError(t, resumeProvider.Stop(context.Background()))
	})

	_, err := openProvider.Start(ctx)
	require.NoError(t, err)
	first, err := openProvider.Open(ctx, agent.OpenOptions{
		Cwd: cwd,
		MCPServers: []mcp.Server{{
			Name:      "open-only",
			Transport: mcp.TransportStdio,
			Command:   "fixture-mcp",
		}},
	})
	require.NoError(t, err)
	require.NoError(t, first.Close(ctx))
	require.Len(t, observed, 1)
	openArgv, err := claude.NativeArgs(observed[0])
	require.NoError(t, err)
	require.JSONEq(t, `{"mcpServers":{"open-only":{
		"type":"stdio","command":"fixture-mcp"
	}}}`, claudeMCPConfig(t, openArgv))

	_, err = resumeProvider.Start(ctx)
	require.NoError(t, err)
	second, err := resumeProvider.Resume(ctx, agent.ResumeOptions{
		SessionID: claudeResumePeerSessionID,
		OpenOptions: agent.OpenOptions{
			Cwd: cwd,
			MCPServers: []mcp.Server{{
				Name:      "resume-only",
				Transport: mcp.TransportHTTP,
				URL:       "https://example.invalid/resume",
			}},
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, second)
	require.JSONEq(t, `{"mcpServers":{"resume-only":{
		"type":"http","url":"https://example.invalid/resume"
	}}}`, claudeMCPConfig(t, readClaudeArgv(t, resumeArgv)))
}

func TestProviderOpenOmitsMCPConfigForEmptyFinalList(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"mcp-empty","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	var observed []claude.SessionOptions
	provider := newTestClaudeProviderWithObserver(
		t,
		agentclaude.Config{},
		func(options claude.SessionOptions) {
			observed = append(observed, options)
		},
		peerSession,
	)
	t.Cleanup(func() { require.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd:        cwd,
		MCPServers: []mcp.Server{},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)

	require.Len(t, observed, 1)
	argv, err := claude.NativeArgs(observed[0])
	require.NoError(t, err)
	for _, arg := range argv {
		assert.False(t, strings.HasPrefix(arg, "--mcp-config"))
	}
}

func TestProviderRejectsUnsupportedAndInvalidMCPBeforeLaunch(t *testing.T) {
	const sentinel = "mcp-secret-sentinel-t03"
	tests := []struct {
		name       string
		server     mcp.Server
		wantCode   agent.ErrorCode
		wantReason string
	}{
		{
			name: "stdio working directory",
			server: mcp.Server{
				Name:      "local",
				Transport: mcp.TransportStdio,
				Command:   "fixture-mcp",
				Cwd:       "/synthetic/" + sentinel,
			},
			wantCode:   agent.ErrorUnsupported,
			wantReason: agentclaude.ErrorReasonMCPServerFieldUnsupported(0, "cwd"),
		},
		{
			name: "HTTP environment headers",
			server: mcp.Server{
				Name:       "remote",
				Transport:  mcp.TransportHTTP,
				URL:        "https://example.invalid/mcp",
				EnvHeaders: map[string]string{"Authorization": sentinel},
			},
			wantCode:   agent.ErrorUnsupported,
			wantReason: agentclaude.ErrorReasonMCPServerFieldUnsupported(0, "env_headers"),
		},
		{
			name: "HTTP bearer environment variable",
			server: mcp.Server{
				Name:              "remote",
				Transport:         mcp.TransportHTTP,
				URL:               "https://example.invalid/mcp",
				BearerTokenEnvVar: sentinel,
			},
			wantCode:   agent.ErrorUnsupported,
			wantReason: agentclaude.ErrorReasonMCPServerFieldUnsupported(0, "bearer_token_env_var"),
		},
		{
			name: "unknown transport",
			server: mcp.Server{
				Name:      "legacy",
				Transport: mcp.TransportSSE,
				URL:       "https://example.invalid/sse",
			},
			wantCode:   agent.ErrorInvalidArgument,
			wantReason: "mcp_server_0_transport_unknown",
		},
	}

	for _, tt := range tests {
		for _, operation := range []string{"open", "resume"} {
			t.Run(tt.name+"/"+operation, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				calls := 0
				provider := newTestClaudeProviderWithObserver(
					t,
					agentclaude.Config{},
					func(claude.SessionOptions) {
						calls++
					},
				)
				_, err := provider.Start(ctx)
				require.NoError(t, err)
				t.Cleanup(func() {
					require.NoError(t, provider.Stop(context.Background()))
				})

				options := agent.OpenOptions{
					Cwd:        t.TempDir(),
					MCPServers: []mcp.Server{tt.server},
				}
				if operation == "open" {
					_, err = provider.Open(ctx, options)
				} else {
					_, err = provider.Resume(ctx, agent.ResumeOptions{
						SessionID:   "mcp-existing",
						OpenOptions: options,
					})
				}
				assert.Equal(t, 0, calls)
				require.Error(t, err)
				require.Equal(t, tt.wantCode, agent.CodeOf(err))
				var classified *agent.Error
				require.ErrorAs(t, err, &classified)
				require.Equal(t, tt.wantReason, classified.Reason)
				if tt.wantCode == agent.ErrorUnsupported {
					assert.Contains(t, err.Error(), "Claude")
				}
				assert.NotContains(t, err.Error(), sentinel)
				assert.NotContains(t, fmt.Sprintf("%+v", err), sentinel)
			})
		}
	}
}

func claudeMCPConfig(t *testing.T, argv []string) string {
	t.Helper()
	var found []string
	for _, arg := range argv {
		if config, ok := strings.CutPrefix(arg, "--mcp-config="); ok {
			found = append(found, config)
		}
	}
	require.Len(t, found, 1)
	return found[0]
}
