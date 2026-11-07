package agentclaude_test

import (
	"context"
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
	claudeTestLauncherEnv    = "AGENT_CLAUDE_TEST_NATIVE_LAUNCHER"
	claudeTestUnsupportedArg = "--disallowedTools="
)

func TestMain(m *testing.M) {
	if launcher := os.Getenv(claudeTestLauncherEnv); launcher != "" {
		os.Exit(runClaudeTestLauncher(launcher, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// The external synthetic peer accepts only reviewed native startup arguments.
// Production still emits --disallowedTools for a real Claude CLI; this test
// launcher removes only that peer-unsupported declaration before exec.
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
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if !strings.HasPrefix(arg, claudeTestUnsupportedArg) {
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
