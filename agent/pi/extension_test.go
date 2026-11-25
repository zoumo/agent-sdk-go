package agentpi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentpi "github.com/zoumo/agent-sdk-go/agent/pi"
	"github.com/zoumo/agent-sdk-go/mcp"
)

func TestSessionExtensionCleanup(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("ShellEnv uses Pi's bash tool on macOS and Linux")
	}
	exe, err := os.Executable()
	require.NoError(t, err)
	for _, outcome := range []string{"close", "combined", "exit", "missing-extension", "process-start-failure"} {
		t.Run(outcome, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("TMPDIR", root)
			t.Setenv("SDK_PI_PROBE_PROCESS", "host")
			t.Setenv("SDK_PI_PROBE_BASE", "inherited")
			t.Setenv("SDK_PI_PROBE_SHELL", "outside-shell")
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			config := agentpi.Config{
				CLIPath:      exe,
				Args:         []string{"-test.run=^TestPiSessionExtensionPeer$", "--"},
				Env:          []string{"SDK_PI_PROBE_OUTCOME=" + outcome, "SDK_PI_PROBE_PROCESS=core"},
				EnableInject: outcome == "combined",
			}
			if outcome == "process-start-failure" {
				config.CLIPath = filepath.Join(root, "missing-executable")
			}
			core := agentpi.New(config)
			capabilities, err := core.Start(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { _ = core.Stop(context.Background()) })
			require.True(t, capabilities.Supports(agent.CapabilityProcessEnv))
			require.True(t, capabilities.Supports(agent.CapabilityShellEnv))
			options := agent.OpenOptions{
				Cwd: root, ProcessEnv: map[string]string{"SDK_PI_PROBE_PROCESS": "session"},
				ShellEnv: map[string]string{"SDK_PI_PROBE_SHELL": "only-shell"},
			}
			var specific agentpi.SessionOptions
			if outcome == "combined" {
				options.Features.AgentQuestions = new(true)
				options.MCPServers = []mcp.Server{{Name: "local", Transport: mcp.TransportStdio, Command: "fixture-mcp"}}
				specific = agentpi.SessionOptions{ToolApproval: true, InteractionTimeout: 2 * time.Second}
			}
			s, err := core.OpenWithOptions(ctx, options, specific)
			switch outcome {
			case "process-start-failure":
				require.Error(t, err)
			case "missing-extension":
				require.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
			default:
				require.NoError(t, err)
				t.Cleanup(func() { _ = s.Close(context.Background()) })
				require.True(t, s.Capabilities().Supports(agent.CapabilityShellEnv))
				if outcome == "combined" {
					require.Implements(t, (*agent.Injector)(nil), s)
					require.True(t, s.EffectiveFeatures().AgentQuestions)
				}
				if outcome == "close" || outcome == "combined" {
					files, err := filepath.Glob(filepath.Join(root, "agent-sdk-pi-*", "session.ts"))
					require.NoError(t, err)
					require.Len(t, files, 1)
					require.NoError(t, s.Close(ctx))
				} else {
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-s.Done():
					}
					require.Error(t, s.Err())
				}
			}
			files, err := filepath.Glob(filepath.Join(root, "agent-sdk-pi-*"))
			require.NoError(t, err)
			assert.Empty(t, files)
			assert.Equal(t, "host", os.Getenv("SDK_PI_PROBE_PROCESS"))
			assert.Equal(t, "outside-shell", os.Getenv("SDK_PI_PROBE_SHELL"))
		})
	}
}

func TestRestrictedSessionRejectsShellEnv(t *testing.T) {
	core := agentpi.New(agentpi.Config{CLIPath: filepath.Join(t.TempDir(), "unreachable")})
	ctx := t.Context()
	_, err := core.Start(ctx)
	require.NoError(t, err)
	_, err = core.Open(ctx, agent.OpenOptions{
		Cwd: t.TempDir(), ShellEnv: map[string]string{"SDK_PI_PROBE_SHELL": "only-shell"},
		Features: agent.Features{RestrictLocalTools: true},
	})
	require.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
	require.NoError(t, core.Stop(ctx))
}

func TestPiSessionExtensionPeer(t *testing.T) {
	outcome := os.Getenv("SDK_PI_PROBE_OUTCOME")
	if outcome == "" {
		return
	}
	if err := runExtensionPeer(outcome); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func runExtensionPeer(outcome string) error {
	index := slices.Index(os.Args, "-e")
	if index < 0 || index+1 >= len(os.Args) || !slices.Contains(os.Args, "--no-extensions") {
		return errors.New("missing explicit extension argv")
	}
	if os.Getenv("SDK_PI_PROBE_PROCESS") != "session" || os.Getenv("SDK_PI_PROBE_BASE") != "inherited" ||
		os.Getenv("SDK_PI_PROBE_SHELL") != "outside-shell" {
		return errors.New("unexpected Pi process environment")
	}
	path := os.Args[index+1]
	configPath := filepath.Join(filepath.Dir(path), "session.json")
	for file, want := range map[string]os.FileMode{filepath.Dir(path): 0o700, path: 0o600, configPath: 0o600} {
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm()&^want != 0 {
			return fmt.Errorf("invalid staged permission for %s", filepath.Base(file))
		}
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var config struct {
		ShellEnv             map[string]string       `json:"shellEnv"`
		MCPServers           []struct{ Name string } `json:"mcpServers"`
		Inject               bool                    `json:"inject"`
		ToolApproval         bool                    `json:"toolApproval"`
		AgentQuestions       bool                    `json:"agentQuestions"`
		InteractionTimeoutMS int64                   `json:"interactionTimeoutMs"`
	}
	if err := json.Unmarshal(data, &config); err != nil || config.ShellEnv["SDK_PI_PROBE_SHELL"] != "only-shell" {
		return errors.New("the private config did not carry ShellEnv")
	}
	if outcome == "combined" &&
		(len(config.MCPServers) != 1 || config.MCPServers[0].Name != "local" ||
			!config.Inject || !config.ToolApproval || !config.AgentQuestions || config.InteractionTimeoutMS != 2000 ||
			!slices.Contains(os.Args, "builtin:mcp")) {
		return errors.New("the private config did not carry all requested extension settings")
	}
	scanner := bufio.NewScanner(os.Stdin)
	writer := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			return err
		}
		var result any
		switch request.Type {
		case "get_state":
			result = map[string]string{"sessionId": "extension-test"}
		case "get_commands":
			commands := []any{}
			if outcome != "missing-extension" {
				commands = append(commands, map[string]any{
					"name": "agent-sdk-session-ready", "source": "extension", "sourceInfo": map[string]string{"path": path},
				})
			}
			if outcome == "combined" {
				commands = append(commands, map[string]any{
					"name": "agent-sdk-inject", "source": "extension", "sourceInfo": map[string]string{"path": path},
				})
			}
			result = map[string]any{"commands": commands}
		default:
			return fmt.Errorf("unexpected command %q", request.Type)
		}
		if err := writer.Encode(map[string]any{
			"id": request.ID, "type": "response", "command": request.Type, "success": true, "data": result,
		}); err != nil {
			return err
		}
		if outcome == "exit" && request.Type == "get_commands" {
			return nil
		}
	}
	return scanner.Err()
}
