package agentclaude_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

func TestProviderOpenPassesFinalMCPListToClaudeArgv(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"mcp-open","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	launcher, env := launchClaudeMockWithoutUnsupportedArgs(t, peerSession)
	capture := filepath.Join(t.TempDir(), "argv.json")
	env = append(env, claudeTestArgvCaptureEnv+"="+capture)
	provider := agentclaude.New(agentclaude.Config{
		CLIPath: launcher,
		Env:     env,
	})
	t.Cleanup(func() { require.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	args := []string{"--stdio", "literal value"}
	envValues := map[string]string{"LOCAL_TOKEN": "${LOCAL_TOKEN}"}
	headers := map[string]string{
		"Authorization": "Bearer ${REMOTE_TOKEN}",
		"X-Workspace":   "synthetic",
	}
	servers := []agent.MCPServer{
		{
			Name:      "local",
			Transport: agent.MCPTransportStdio,
			Command:   "fixture-mcp",
			Args:      args,
			Env:       envValues,
		},
		{
			Name:      "remote",
			Transport: agent.MCPTransportHTTP,
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

	argv := readClaudeArgv(t, capture)
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

	servers[0].Command = "mutated"
	args[0] = "--mutated"
	envValues["LOCAL_TOKEN"] = "mutated"
	headers["Authorization"] = "mutated"
	servers[1].Name = "mutated"
	require.Equal(t, argv, readClaudeArgv(t, capture))
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
	openLauncher, openCaptures := captureSequencedClaudeLauncher(t, openPeer)
	openProvider := agentclaude.New(agentclaude.Config{CLIPath: openLauncher})
	t.Cleanup(func() { require.NoError(t, openProvider.Stop(context.Background())) })
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
		MCPServers: []agent.MCPServer{{
			Name:      "open-only",
			Transport: agent.MCPTransportStdio,
			Command:   "fixture-mcp",
		}},
	})
	require.NoError(t, err)
	require.NoError(t, first.Close(ctx))
	require.JSONEq(t, `{"mcpServers":{"open-only":{
		"type":"stdio","command":"fixture-mcp"
	}}}`, claudeMCPConfig(t, readClaudeArgv(t, openCaptures[0])))

	_, err = resumeProvider.Start(ctx)
	require.NoError(t, err)
	second, err := resumeProvider.Resume(ctx, agent.ResumeOptions{
		SessionID: claudeResumePeerSessionID,
		OpenOptions: agent.OpenOptions{
			Cwd: cwd,
			MCPServers: []agent.MCPServer{{
				Name:      "resume-only",
				Transport: agent.MCPTransportHTTP,
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
	launcher, env := launchClaudeMockWithoutUnsupportedArgs(t, peerSession)
	capture := filepath.Join(t.TempDir(), "argv.json")
	env = append(env, claudeTestArgvCaptureEnv+"="+capture)
	provider := agentclaude.New(agentclaude.Config{
		CLIPath: launcher,
		Env:     env,
	})
	t.Cleanup(func() { require.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd:        cwd,
		MCPServers: []agent.MCPServer{},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)

	for _, arg := range readClaudeArgv(t, capture) {
		assert.False(t, strings.HasPrefix(arg, "--mcp-config"))
	}
}

func TestProviderRejectsUnsupportedAndInvalidMCPBeforeLaunch(t *testing.T) {
	const sentinel = "mcp-secret-sentinel-t03"
	tests := []struct {
		name       string
		server     agent.MCPServer
		wantCode   agent.ErrorCode
		wantReason string
	}{
		{
			name: "stdio working directory",
			server: agent.MCPServer{
				Name:      "local",
				Transport: agent.MCPTransportStdio,
				Command:   "fixture-mcp",
				Cwd:       "/synthetic/" + sentinel,
			},
			wantCode:   agent.ErrorUnsupported,
			wantReason: "mcp_server_0_cwd_unsupported",
		},
		{
			name: "HTTP environment headers",
			server: agent.MCPServer{
				Name:       "remote",
				Transport:  agent.MCPTransportHTTP,
				URL:        "https://example.invalid/mcp",
				EnvHeaders: map[string]string{"Authorization": sentinel},
			},
			wantCode:   agent.ErrorUnsupported,
			wantReason: "mcp_server_0_env_headers_unsupported",
		},
		{
			name: "HTTP bearer environment variable",
			server: agent.MCPServer{
				Name:              "remote",
				Transport:         agent.MCPTransportHTTP,
				URL:               "https://example.invalid/mcp",
				BearerTokenEnvVar: sentinel,
			},
			wantCode:   agent.ErrorUnsupported,
			wantReason: "mcp_server_0_bearer_token_env_var_unsupported",
		},
		{
			name: "unknown transport",
			server: agent.MCPServer{
				Name:      "legacy",
				Transport: agent.MCPTransport("sse"),
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
				cwd := t.TempDir()
				capture := filepath.Join(cwd, "rejected-argv")
				launcher := filepath.Join(cwd, "must-not-start")
				script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " +
					shellQuote(capture) + "\nexit 69\n"
				require.NoError(t, os.WriteFile(launcher, []byte(script), 0o700))
				provider := agentclaude.New(agentclaude.Config{
					CLIPath: launcher,
				})
				_, err := provider.Start(ctx)
				require.NoError(t, err)
				t.Cleanup(func() {
					require.NoError(t, provider.Stop(context.Background()))
				})

				options := agent.OpenOptions{
					Cwd:        cwd,
					MCPServers: []agent.MCPServer{tt.server},
				}
				if operation == "open" {
					_, err = provider.Open(ctx, options)
				} else {
					_, err = provider.Resume(ctx, agent.ResumeOptions{
						SessionID:   "mcp-existing",
						OpenOptions: options,
					})
				}
				_, statErr := os.Stat(capture)
				assert.ErrorIs(t, statErr, os.ErrNotExist)
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

func captureSequencedClaudeLauncher(
	t *testing.T,
	peers ...*mocktest.Session,
) (string, []string) {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	captureDir := t.TempDir()
	captures := make([]string, len(peers))
	launches := make([]claudeSequencedLaunch, len(peers))
	for index, peer := range peers {
		launcher, err := peer.Launcher("main")
		require.NoError(t, err)
		captures[index] = filepath.Join(captureDir, fmt.Sprintf("argv-%d.json", index))
		env := append([]string(nil), peer.Env...)
		env = append(
			env,
			claudeTestLauncherEnv+"="+launcher,
			claudeTestArgvCaptureEnv+"="+captures[index],
		)
		launches[index] = claudeSequencedLaunch{
			command: executable,
			env:     env,
		}
	}
	return sequencedClaudeCommands(t, launches...), captures
}

func readClaudeArgv(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var argv []string
	require.NoError(t, json.Unmarshal(data, &argv))
	return argv
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
