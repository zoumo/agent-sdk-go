//go:build unix

package claudesim

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

const (
	mcpShimControl = "CLAUDE_MCP_SESSION_SHIM"
	mcpShimCommand = "CLAUDE_MCP_SESSION_COMMAND"
)

type shimCommand struct {
	Command string
	Args    []string
}

func TestMain(m *testing.M) {
	if encoded := os.Getenv(mcpShimCommand); os.Getenv(mcpShimControl) == "1" && encoded != "" {
		os.Exit(runMCPSessionShim(encoded))
	}
	os.Exit(m.Run())
}

func TestRecordedContinuationWithStartupMCP(t *testing.T) {
	runRecordedContinuation(t, true, mcpSessionLauncher)
}

func TestMCPSessionShimFiltersOnlySyntheticConfig(t *testing.T) {
	args := []string{
		"--output-format", "stream-json",
		`--mcp-config={"mcpServers":{"fixture":{"type":"sdk","name":"fixture"}}}`,
		"--verbose",
	}
	filtered, err := filterMCPConfig(args)
	require.NoError(t, err)
	require.Equal(t, []string{"--output-format", "stream-json", "--verbose"}, filtered)

	_, err = filterMCPConfig([]string{`--mcp-config={"mcpServers":{"fixture":{"type":"stdio","name":"fixture"}}}`})
	require.ErrorContains(t, err, "not the SDK synthetic declaration")
}

// mcpSessionLauncher uses the test binary as a shim for the one native argument
// absent from the tool's startup whitelist. The shim validates and removes that
// known SDK-generated declaration, then execs the session command with every
// other native argument intact.
func mcpSessionLauncher(t *testing.T, session *mocktest.Session) sessionLauncher {
	t.Helper()
	command, args, err := session.Command("main")
	require.NoError(t, err)
	encoded, err := json.Marshal(shimCommand{Command: command, Args: args})
	require.NoError(t, err)
	executable, err := os.Executable()
	require.NoError(t, err)
	return sessionLauncher{
		Path: executable,
		Env:  []string{mcpShimControl + "=1", mcpShimCommand + "=" + string(encoded)},
	}
}

func runMCPSessionShim(encoded string) int {
	var spec shimCommand
	if err := json.Unmarshal([]byte(encoded), &spec); err != nil || !filepath.IsAbs(spec.Command) {
		fmt.Fprintln(os.Stderr, "invalid MCP session shim command")
		return 64
	}
	filtered, err := filterMCPConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 64
	}
	args := append(append([]string(nil), spec.Args...), "--")
	args = append(args, filtered...)
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, mcpShimCommand+"=") || strings.HasPrefix(entry, mcpShimControl+"=") {
			continue
		}
		env = append(env, entry)
	}
	if err := syscall.Exec(spec.Command, append([]string{spec.Command}, args...), env); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 127
	}
	return 0
}

func filterMCPConfig(args []string) ([]string, error) {
	filtered := make([]string, 0, len(args))
	seen := 0
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--mcp-config":
			if i+1 == len(args) {
				return nil, errors.New("missing MCP config value")
			}
			i++
			if err := validateSyntheticMCPConfig(args[i]); err != nil {
				return nil, err
			}
			seen++
		case strings.HasPrefix(arg, "--mcp-config="):
			if err := validateSyntheticMCPConfig(strings.TrimPrefix(arg, "--mcp-config=")); err != nil {
				return nil, err
			}
			seen++
		default:
			filtered = append(filtered, arg)
		}
	}
	if seen != 1 {
		return nil, errors.New("synthetic MCP config argument count is not one")
	}
	return filtered, nil
}

func validateSyntheticMCPConfig(raw string) error {
	var config struct {
		Servers map[string]struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"mcpServers"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil || len(config.Servers) == 0 {
		return errors.New("invalid synthetic MCP config")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("invalid synthetic MCP config")
	}
	for name, server := range config.Servers {
		if name == "" || server.Type != "sdk" || server.Name != name {
			return errors.New("MCP config is not the SDK synthetic declaration")
		}
	}
	return nil
}
