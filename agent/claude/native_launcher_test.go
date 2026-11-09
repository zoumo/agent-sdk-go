package agentclaude_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

const (
	claudeTestLauncherEnv          = "AGENT_CLAUDE_TEST_NATIVE_LAUNCHER"
	claudeTestArgvCaptureEnv       = "AGENT_CLAUDE_TEST_ARGV_CAPTURE"
	claudeTestUnsupportedArg       = "--disallowedTools="
	claudeTestUnsupportedPromptArg = "--append-system-prompt="
	claudeTestUnsupportedMCP       = "--mcp-config="
)

func TestMain(m *testing.M) {
	if launcher := os.Getenv(claudeTestLauncherEnv); launcher != "" {
		os.Exit(runClaudeTestLauncher(launcher, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// The external synthetic peer accepts only its prepared native startup
// arguments. Production still emits these declarations for a real Claude CLI;
// this launcher captures them first, then removes the peer-unsupported values
// before exec.
func launchClaudeMockWithoutUnsupportedArgs(
	t testing.TB,
	peerSession *mocktest.Session,
) (string, []string) {
	t.Helper()
	launcher, err := peerSession.Launcher("main")
	require.NoError(t, err)
	executable, err := os.Executable()
	require.NoError(t, err)
	env := append([]string(nil), peerSession.Env...)
	env = append(env, claudeTestLauncherEnv+"="+launcher)
	return executable, env
}

func runClaudeTestLauncher(launcher string, args []string) int {
	if capture := os.Getenv(claudeTestArgvCaptureEnv); capture != "" {
		data, err := json.Marshal(args)
		if err == nil {
			err = os.WriteFile(capture, data, 0o600)
		}
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Claude test argv capture: %v\n", err)
			return 126
		}
	}
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if !strings.HasPrefix(arg, claudeTestUnsupportedArg) &&
			!strings.HasPrefix(arg, claudeTestUnsupportedPromptArg) &&
			!strings.HasPrefix(arg, claudeTestUnsupportedMCP) {
			filtered = append(filtered, arg)
		}
	}
	command := exec.CommandContext(context.Background(), launcher, filtered...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = os.Environ()
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		_, _ = fmt.Fprintf(os.Stderr, "Claude test launcher: %v\n", err)
		return 127
	}
	return 0
}
