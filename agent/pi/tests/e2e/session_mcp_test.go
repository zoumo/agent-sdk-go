//go:build darwin || linux

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentpi "github.com/zoumo/agent-sdk-go/agent/pi"
	"github.com/zoumo/agent-sdk-go/mcp"
)

func TestOfficialPiSessionMCP(t *testing.T) {
	cli := os.Getenv("AGENT_SDK_TEST_PI_CLI")
	if cli == "" {
		t.Skip("set AGENT_SDK_TEST_PI_CLI to the absolute Pi 1.6.0 executable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, cli, "--version").Output()
	require.NoError(t, err)
	require.Equal(t, "1.6.0", strings.TrimSpace(string(version)))

	rejected := make(chan struct{}, 1)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/chat/completions":
			serveMCPModel(t, w, r)
		case "/mcp":
			if r.Header.Get("Authorization") != "Bearer mcp-beta-token" || r.Header.Get("X-Probe") != "beta-only" {
				http.Error(w, "unexpected dedicated MCP environment", http.StatusUnauthorized)
				return
			}
			serveProbeMCP(t, w, r)
		case "/unavailable":
			select {
			case rejected <- struct{}{}:
			default:
			}
			http.Error(w, "controlled MCP connection failure", http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(endpoint.Close)
	root, cwd, home, agentDir, sessionDir := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("TMPDIR", root)
	require.NoError(t, os.Mkdir(filepath.Join(cwd, ".pi"), 0o700))
	binary, err := os.Executable()
	require.NoError(t, err)
	peerArgs := []string{"-test.run=^TestPiMCPStdioPeer$"}
	fileServers := func(name, label string) []byte {
		data, err := json.Marshal(map[string]any{"mcpServers": map[string]any{name: map[string]any{
			"command": binary, "args": peerArgs, "cwd": cwd, "exposure": "direct",
			"env": map[string]string{"SDK_PI_MCP_VALUE": label, "SDK_PI_MCP_PID": filepath.Join(root, label+".pid")},
		}}})
		require.NoError(t, err)
		return data
	}
	configFiles := map[string][]byte{
		filepath.Join(agentDir, "models.json"): fmt.Appendf(nil, `{"providers":{"magpie":{"api":"openai-completions","baseUrl":%q,"apiKey":"$SDK_PI_MCP_MODEL","models":[{"id":%q}]}}}`, endpoint.URL, controlledModel),
		filepath.Join(agentDir, "auth.json"):   []byte("{}"),
		filepath.Join(agentDir, "mcp.json"):    fileServers("common", "native-user"),
		filepath.Join(cwd, ".pi", "mcp.json"):  fileServers("project-probe", "native-project"),
	}
	for path, data := range configFiles {
		require.NoError(t, os.WriteFile(path, data, 0o600))
	}
	core := agentpi.New(agentpi.Config{
		CLIPath: cli, Provider: "magpie", SessionDir: sessionDir,
		Args: []string{"--offline", "--no-skills", "--no-context-files", "--no-prompt-templates", "--approve"},
		Env:  []string{"HOME=" + home, "PI_CODING_AGENT_DIR=" + agentDir},
	})
	_, err = core.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	stdio := func(name, pidFile, value string) mcp.Server {
		return mcp.Server{
			Name: name, Transport: mcp.TransportStdio, Command: binary, Args: peerArgs, Cwd: cwd,
			Env: []mcp.NamedValue{mcp.Literal("SDK_PI_MCP_PID", filepath.Join(root, pidFile+".pid")), mcp.Literal("SDK_PI_MCP_VALUE", value)},
		}
	}
	const literal = "!literal-$SDK_PI_MCP_MODEL"
	alpha := agent.OpenOptions{
		Cwd: cwd, Config: agent.SessionConfig{Model: controlledModel},
		ProcessEnv: map[string]string{"SDK_PI_MCP_MODEL": "model-alpha"},
		MCPServers: []mcp.Server{
			stdio("alpha", "alpha", literal), stdio("common", "collision-user", "wrong"),
			stdio("project_probe", "collision-project", "wrong"),
		},
	}
	beta := agent.OpenOptions{
		Cwd: cwd, Config: agent.SessionConfig{Model: controlledModel},
		ProcessEnv: map[string]string{"SDK_PI_MCP_MODEL": "model-beta", "SDK_PI_MCP_REMOTE": "mcp-beta-token"},
		MCPServers: []mcp.Server{
			{Name: "beta", Transport: mcp.TransportHTTP, URL: endpoint.URL + "/mcp", BearerTokenEnvVar: "SDK_PI_MCP_REMOTE", Headers: []mcp.NamedValue{{
				Name: "X-Probe", ValueFrom: &mcp.ValueSource{FileKeyRef: &mcp.FileKeyRef{Path: "fixture.env", Key: "PROBE"}},
			}}},
			{Name: "unavailable", Transport: mcp.TransportHTTP, URL: endpoint.URL + "/unavailable", BearerTokenEnvVar: "SDK_PI_MCP_REMOTE"},
		},
		MCPFiles: fstest.MapFS{"fixture.env": {Data: []byte("PROBE='beta-only'\n")}},
	}
	a, err := core.Open(ctx, alpha)
	require.NoError(t, err)
	b, err := core.Open(ctx, beta)
	require.NoError(t, err)
	select {
	case <-rejected:
	case <-ctx.Done():
		t.Fatal("Open completed but unavailable MCP connection was not attempted")
	}
	aEvents, err := a.Events()
	require.NoError(t, err)
	bEvents, err := b.Events()
	require.NoError(t, err)
	runMCPProbe(t, ctx, a, aEvents, "alpha", literal)
	runMCPProbe(t, ctx, b, bEvents, "beta", "streamable-http")
	runMCPProbe(t, ctx, a, aEvents, "common", "native-user")
	runMCPProbe(t, ctx, b, bEvents, "project_probe", "native-project")
	require.NoFileExists(t, filepath.Join(root, "collision-user.pid"))
	require.NoFileExists(t, filepath.Join(root, "collision-project.pid"))

	alphaPID := readPiPID(t, root, "alpha")
	require.NoError(t, a.Close(ctx))
	assert.Eventually(t, func() bool { return syscall.Kill(alphaPID, 0) == syscall.ESRCH }, 2*time.Second, 20*time.Millisecond)
	runMCPProbe(t, ctx, b, bEvents, "beta", "streamable-http")

	alpha.MCPServers[0] = stdio("alpha", "resumed", "resumed-stdio")
	resumed, err := core.Resume(ctx, agent.ResumeOptions{SessionID: a.ID(), OpenOptions: alpha})
	require.NoError(t, err)
	require.Equal(t, a.ID(), resumed.ID())
	resumedEvents, err := resumed.Events()
	require.NoError(t, err)
	runMCPProbe(t, ctx, resumed, resumedEvents, "alpha", "resumed-stdio")
	resumedPID := readPiPID(t, root, "resumed")
	require.NotEqual(t, alphaPID, resumedPID)
	require.NoError(t, resumed.Close(ctx))
	assert.Eventually(t, func() bool { return syscall.Kill(resumedPID, 0) == syscall.ESRCH }, 2*time.Second, 20*time.Millisecond)
	require.NoError(t, b.Close(ctx))
	assertPiExtensionCount(t, root, 0)
	for path, before := range configFiles {
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, before, after, path)
	}
}

func runMCPProbe(t *testing.T, ctx context.Context, session agent.Session, stream agent.EventStream, target, want string) {
	t.Helper()
	receipt, err := session.StartOrSteer(ctx, agent.Input{Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "probe " + target}}}})
	require.NoError(t, err)
	require.Equal(t, agent.InputAppliedNewWork, receipt.Result)
	seen := false
	for {
		event, err := stream.Next(ctx)
		require.NoError(t, err)
		switch payload := event.Payload.(type) {
		case agent.ToolResultPayload:
			require.Equal(t, "mcp__"+target+"__report", payload.ToolName)
			require.Equal(t, agent.ToolProviderMCP, payload.Provider)
			require.Equal(t, "completed", payload.Status)
			raw, err := json.Marshal(payload.RawOutput)
			require.NoError(t, err)
			require.Contains(t, string(raw), want)
			seen = true
		case agent.StateUpdatePayload:
			if payload.State != nil && *payload.State == agent.StateIdle {
				require.Nil(t, payload.Error)
				require.True(t, seen, "no MCP tool result for %s", target)
				return
			}
		}
	}
}

type mcpProbeRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		ProtocolVersion string `json:"protocolVersion"`
	} `json:"params"`
}

func TestPiMCPStdioPeer(t *testing.T) {
	pidFile, enabled := os.LookupEnv("SDK_PI_MCP_PID")
	if !enabled {
		t.Skip("Pi launches this helper as an MCP server")
	}
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		os.Exit(2)
	}
	input, output := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	for {
		var request mcpProbeRequest
		if input.Decode(&request) != nil {
			os.Exit(0)
		}
		if len(request.ID) != 0 {
			if err := output.Encode(probeMCPReply(request, os.Getenv("SDK_PI_MCP_VALUE"))); err != nil {
				os.Exit(2)
			}
		}
	}
}

func serveProbeMCP(t *testing.T, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var request mcpProbeRequest
	if json.NewDecoder(r.Body).Decode(&request) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if len(request.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	assert.NoError(t, json.NewEncoder(w).Encode(probeMCPReply(request, "streamable-http")))
}

func probeMCPReply(request mcpProbeRequest, value string) map[string]any {
	reply := map[string]any{"jsonrpc": "2.0", "id": request.ID}
	switch request.Method {
	case "initialize":
		reply["result"] = map[string]any{
			"protocolVersion": request.Params.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "controlled-probe", "version": "1"},
		}
	case "tools/list":
		reply["result"] = map[string]any{"tools": []any{map[string]any{
			"name": "report", "description": "Report a test value",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		}}}
	case "tools/call":
		reply["result"] = map[string]any{"content": []any{map[string]string{"type": "text", "text": value}}}
	default:
		reply["error"] = map[string]any{"code": -32601, "message": "Method not found"}
	}
	return reply
}

func serveMCPModel(t *testing.T, w http.ResponseWriter, r *http.Request) {
	label := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer model-")
	var request struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil || request.Model != controlledModel ||
		(label != "alpha" && label != "beta") || len(request.Messages) == 0 {
		t.Logf("unexpected model route: label=%s model=%s messages=%d", label, request.Model, len(request.Messages))
		http.Error(w, "wrong model route", http.StatusBadRequest)
		return
	}
	last := request.Messages[len(request.Messages)-1]
	delta, finish := map[string]any{"role": "assistant", "content": "MCP probe done"}, "stop"
	if last.Role == "user" {
		other := "alpha"
		if label == "alpha" {
			other = "beta"
		}
		names := make([]string, 0, len(request.Tools))
		for _, tool := range request.Tools {
			names = append(names, tool.Function.Name)
		}
		target := ""
		for _, candidate := range []string{label, "common", "project_probe"} {
			if strings.Contains(string(last.Content), "probe "+candidate) && slices.Contains(names, "mcp__"+candidate+"__report") {
				target = candidate
			}
		}
		if target == "" || !slices.Contains(names, "mcp__"+label+"__report") || slices.Contains(names, "mcp__"+other+"__report") ||
			!slices.Contains(names, "mcp__common__report") || !slices.Contains(names, "mcp__project_probe__report") {
			t.Logf("model %s received tools %v for %s", label, names, last.Content)
			http.Error(w, "incorrect MCP tool set", http.StatusBadRequest)
			return
		}
		delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
			"index": 0, "id": "call_" + target, "type": "function",
			"function": map[string]string{"name": "mcp__" + target + "__report", "arguments": "{}"},
		}}}
		finish = "tool_calls"
	}
	chunk, err := json.Marshal(map[string]any{
		"id": "controlled-mcp-chunk", "object": "chat.completion.chunk", "created": 1,
		"model": controlledModel, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
	})
	if err != nil {
		http.Error(w, "invalid probe response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
}
