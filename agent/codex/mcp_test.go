package agentcodex_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentcodex "github.com/zoumo/agent-sdk-go/agent/codex"
	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
)

const codexMCPPeerGateDir = "AGENT_CODEX_MCP_PEER_GATE_DIR"

func TestProviderMapsFinalMCPServersToOpenAndResumeThreadConfig(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":`+expectedCodexMCPStartParams("open")+`}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-open")+`}`,
	)
	frames = append(frames, codexCloseFrames(93, "root-open")...)
	frames = append(frames,
		`>{"id":94,"method":"thread/resume","params":`+expectedCodexMCPResumeParams("resume")+`}`,
		`<{"id":94,"result":`+codexThreadResumeResult("root-resume")+`}`,
	)
	frames = append(frames, codexCloseFrames(95, "root-resume")...)

	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	openServers := codexMCPTestServers("open")
	opened, err := provider.Open(ctx, agent.OpenOptions{
		Cwd:        "/synthetic",
		MCPServers: openServers,
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, opened)
	mutateCodexMCPServers(openServers)
	require.NoError(t, opened.Close(ctx))

	resumeServers := codexMCPTestServers("resume")
	resumed, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID: "root-resume",
		OpenOptions: agent.OpenOptions{
			Cwd:        "/synthetic",
			MCPServers: resumeServers,
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, resumed)
	mutateCodexMCPServers(resumeServers)
	require.NoError(t, resumed.Close(ctx))
}

func TestProviderCopiesMCPConfigurationBeforeThreadRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	gateDir := t.TempDir()
	provider := newCodexMCPProtocolPeerProvider(t, gateDir)
	var (
		opened             chan codexMCPResult
		openResult         codexMCPResult
		openResultReceived bool
	)
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(gateDir, "start.release"), nil, 0o600)
		if opened != nil && !openResultReceived {
			select {
			case openResult = <-opened:
				openResultReceived = true
			case <-time.After(5 * time.Second):
			}
		}
		if openResult.session != nil {
			_ = openResult.session.Close(context.Background())
		}
		assert.NoError(t, provider.Stop(context.Background()))
	})

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	servers := codexMCPTestServers("open")
	opened = make(chan codexMCPResult, 1)
	go func() {
		session, openErr := provider.Open(ctx, agent.OpenOptions{
			Cwd:        "/synthetic",
			MCPServers: servers,
		})
		opened <- codexMCPResult{session: session, err: openErr}
	}()

	waitCodexMCPGate(t, ctx, gateDir, "start")
	startParams := readCodexMCPParam(t, gateDir, "start")
	mutateCodexMCPServers(servers)
	releaseCodexMCPGate(t, gateDir, "start")

	openResult = awaitCodexMCPResult(t, ctx, opened)
	openResultReceived = true
	require.NoError(t, openResult.err)
	require.NoError(t, openResult.session.Close(ctx))
	assert.JSONEq(t, expectedCodexMCPStartParams("open"), string(startParams))
}

func TestProviderRejectsInvalidMCPServersBeforeThreadRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-open")+`}`,
	)
	frames = append(frames, codexCloseFrames(93, "root-open")...)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	const secret = "MCP-SECRET-MUST-NOT-LEAK"
	invalidServers := []agent.MCPServer{{
		Name:      "local",
		Transport: agent.MCPTransportStdio,
		Command:   "fixture-mcp",
		Headers:   map[string]string{"Authorization": secret},
	}}
	for _, reject := range []struct {
		name string
		call func() error
	}{
		{
			name: "open",
			call: func() error {
				_, openErr := provider.Open(ctx, agent.OpenOptions{
					Cwd:        "/synthetic",
					MCPServers: invalidServers,
				})
				return openErr
			},
		},
		{
			name: "resume",
			call: func() error {
				_, resumeErr := provider.Resume(ctx, agent.ResumeOptions{
					SessionID: "root-resume",
					OpenOptions: agent.OpenOptions{
						Cwd:        "/synthetic",
						MCPServers: invalidServers,
					},
				})
				return resumeErr
			},
		},
	} {
		t.Run(reject.name, func(t *testing.T) {
			err := reject.call()
			require.Error(t, err)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
			var classified *agent.Error
			require.ErrorAs(t, err, &classified)
			assert.Equal(t, "mcp_server_0_headers_forbidden", classified.Reason)
			assert.NotContains(t, fmt.Sprintf("%v %+v", err, err), secret)
		})
	}

	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	require.NoError(t, session.Close(ctx))
}

type codexMCPResult struct {
	session agent.Session
	err     error
}

func newCodexMCPProtocolPeerProvider(t *testing.T, gateDir string) *agentcodex.Provider {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	environment := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, codexProtocolPeerMode+"=") ||
			strings.HasPrefix(entry, codexMCPPeerGateDir+"=") {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment,
		codexProtocolPeerMode+"=mcp-snapshot",
		codexMCPPeerGateDir+"="+gateDir,
	)
	return agentcodex.New(agentcodex.Config{Stdio: codex.StdioOptions{
		Executable: executable,
		Args:       []string{"-test.run=^TestAgentCodexProtocolPeer$"},
		Env:        environment,
		Dir:        gateDir,
		Initialize: codextypes.InitializeParams{
			ClientInfo: codextypes.ClientInfo{Name: "agent-codex-mcp-test", Version: "1"},
		},
	}})
}

func codexMCPTestServers(phase string) []agent.MCPServer {
	return []agent.MCPServer{
		{
			Name:      "local",
			Transport: agent.MCPTransportStdio,
			Command:   "fixture-mcp-" + phase,
			Args:      []string{"--stdio", phase},
			Env:       map[string]string{"FIXTURE_TOKEN": "fixture-token-" + phase},
			Cwd:       "/synthetic/mcp",
		},
		{
			Name:              "remote",
			Transport:         agent.MCPTransportHTTP,
			URL:               "https://example.invalid/mcp/" + phase,
			Headers:           map[string]string{"Authorization": "Bearer fixture-" + phase},
			EnvHeaders:        map[string]string{"X-API-Key": "REMOTE_API_KEY_" + strings.ToUpper(phase)},
			BearerTokenEnvVar: "REMOTE_BEARER_" + strings.ToUpper(phase),
		},
	}
}

func mutateCodexMCPServers(servers []agent.MCPServer) {
	servers[0].Command = "mutated-command"
	servers[0].Args[0] = "mutated-arg"
	servers[0].Env["FIXTURE_TOKEN"] = "mutated-token"
	servers[0].Cwd = "/synthetic/mutated"
	servers[1].URL = "https://mutated.invalid/mcp"
	servers[1].Headers["Authorization"] = "Bearer mutated"
	servers[1].EnvHeaders["X-API-Key"] = "MUTATED_API_KEY"
	servers[1].BearerTokenEnvVar = "MUTATED_BEARER_TOKEN"
}

func expectedCodexMCPStartParams(phase string) string {
	return fmt.Sprintf(`{
		"config": {
			"mcp_servers.local": {
				"command": "fixture-mcp-%[1]s",
				"args": ["--stdio", "%[1]s"],
				"env": {"FIXTURE_TOKEN": "fixture-token-%[1]s"},
				"cwd": "/synthetic/mcp"
			},
			"mcp_servers.remote": {
				"url": "https://example.invalid/mcp/%[1]s",
				"http_headers": {"Authorization": "Bearer fixture-%[1]s"},
				"env_http_headers": {"X-API-Key": "REMOTE_API_KEY_%[2]s"},
				"bearer_token_env_var": "REMOTE_BEARER_%[2]s"
			}
		},
		"cwd": "/synthetic"
	}`, phase, strings.ToUpper(phase))
}

func expectedCodexMCPResumeParams(phase string) string {
	return fmt.Sprintf(`{
		"config": {
			"mcp_servers.local": {
				"command": "fixture-mcp-%[1]s",
				"args": ["--stdio", "%[1]s"],
				"env": {"FIXTURE_TOKEN": "fixture-token-%[1]s"},
				"cwd": "/synthetic/mcp"
			},
			"mcp_servers.remote": {
				"url": "https://example.invalid/mcp/%[1]s",
				"http_headers": {"Authorization": "Bearer fixture-%[1]s"},
				"env_http_headers": {"X-API-Key": "REMOTE_API_KEY_%[2]s"},
				"bearer_token_env_var": "REMOTE_BEARER_%[2]s"
			}
		},
		"cwd": "/synthetic",
		"excludeTurns": true,
		"threadId": "root-resume"
	}`, phase, strings.ToUpper(phase))
}

func waitCodexMCPGate(t *testing.T, ctx context.Context, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name+".ready")
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			require.NoError(t, err)
		}
		select {
		case <-ctx.Done():
			require.NoError(t, ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func readCodexMCPParam(t *testing.T, dir, name string) []byte {
	t.Helper()
	params, err := os.ReadFile(filepath.Join(dir, name+".params"))
	require.NoError(t, err)
	return params
}

func releaseCodexMCPGate(t *testing.T, dir, name string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".release"), nil, 0o600))
}

func awaitCodexMCPResult(
	t *testing.T,
	ctx context.Context,
	results <-chan codexMCPResult,
) codexMCPResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
		return codexMCPResult{}
	}
}

func holdCodexMCPPeerRequest(dir, name string, params []byte) {
	if dir == "" {
		os.Exit(80)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".params"), params, 0o600); err != nil {
		os.Exit(85)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".ready"), nil, 0o600); err != nil {
		os.Exit(86)
	}
	deadline := time.Now().Add(5 * time.Second)
	release := filepath.Join(dir, name+".release")
	for time.Now().Before(deadline) {
		if _, err := os.Stat(release); err == nil {
			return
		} else if !os.IsNotExist(err) {
			os.Exit(87)
		}
		time.Sleep(5 * time.Millisecond)
	}
	os.Exit(88)
}
