package agentclaude_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

const (
	claudeTestLauncherEnv    = "AGENT_CLAUDE_TEST_NATIVE_LAUNCHER"
	claudeTestArgvCaptureEnv = "AGENT_CLAUDE_TEST_ARGV_CAPTURE"
	claudeTestUnsupportedArg = "--disallowedTools="
	claudeTestUnsupportedMCP = "--mcp-config="
	claudeTestGateRequestEnv = "AGENT_CLAUDE_TEST_GATE_REQUEST"
	claudeTestGateSignalEnv  = "AGENT_CLAUDE_TEST_GATE_SIGNAL"
	claudeTestGateReleaseEnv = "AGENT_CLAUDE_TEST_GATE_RELEASE"
)

func TestMain(m *testing.M) {
	if scenario := os.Getenv(claudeResumePeerEnv); scenario != "" {
		os.Exit(runClaudeResumePeer(scenario, os.Args[1:]))
	}
	if launcher := os.Getenv(claudeTestLauncherEnv); launcher != "" {
		os.Exit(runClaudeTestLauncher(launcher, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// The external synthetic peer accepts only its prepared native startup
// arguments. Production still emits these declarations for a real Claude CLI;
// this launcher captures the exact production argv first, then removes only the
// peer-unsupported declarations before exec.
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

func launchClaudeMockCapturingArgs(
	t testing.TB,
	peerSession *mocktest.Session,
) (string, []string, string) {
	t.Helper()
	executable, env := launchClaudeMockWithoutUnsupportedArgs(t, peerSession)
	capture := filepath.Join(t.TempDir(), "argv.json")
	env = append(env, claudeTestArgvCaptureEnv+"="+capture)
	return executable, env, capture
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
			!strings.HasPrefix(arg, claudeTestUnsupportedMCP) &&
			!strings.HasPrefix(arg, "--effort=") {
			filtered = append(filtered, arg)
		}
	}
	command := exec.CommandContext(context.Background(), launcher, filtered...)
	command.Stderr = os.Stderr
	command.Env = os.Environ()
	if request := os.Getenv(claudeTestGateRequestEnv); request != "" {
		return runGatedClaudeTestLauncher(
			command,
			request,
			os.Getenv(claudeTestGateSignalEnv),
			os.Getenv(claudeTestGateReleaseEnv),
		)
	}
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	return waitClaudeTestLauncher(command)
}

func runGatedClaudeTestLauncher(
	command *exec.Cmd,
	request, signalPath, releasePath string,
) int {
	stdin, err := command.StdinPipe()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Claude test launcher: stdin: %v\n", err)
		return 127
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Claude test launcher: stdout: %v\n", err)
		return 127
	}
	if err := command.Start(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Claude test launcher: start: %v\n", err)
		return 127
	}

	go pumpClaudeTestInput(stdin, request, signalPath)
	outputDone := make(chan error, 1)
	go func() {
		outputDone <- pumpClaudeTestOutput(stdout, signalPath, releasePath)
	}()
	if err := <-outputDone; err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Claude test launcher: output: %v\n", err)
	}
	return waitStartedClaudeTestLauncher(command)
}

func pumpClaudeTestInput(stdin io.WriteCloser, request, signalPath string) {
	defer func() { _ = stdin.Close() }()
	reader := bufio.NewReader(os.Stdin)
	signaled := false
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if !signaled && bytes.Contains(line, []byte(request)) {
				signaled = true
				_ = os.WriteFile(signalPath, []byte("request\n"), 0o600)
			}
			if _, writeErr := stdin.Write(line); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func pumpClaudeTestOutput(reader io.Reader, signalPath, releasePath string) error {
	lines := bufio.NewReader(reader)
	holding := false
	for {
		line, err := lines.ReadBytes('\n')
		if len(line) > 0 {
			if !holding && fileExists(signalPath) && !fileExists(releasePath) {
				holding = true
			}
			if holding && !waitForClaudeTestFile(releasePath) {
				return errors.New("timed out waiting for the response gate release")
			}
			if _, writeErr := os.Stdout.Write(line); writeErr != nil {
				return writeErr
			}
			holding = false
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func waitForClaudeTestFile(path string) bool {
	deadline := time.Now().Add(10 * time.Second)
	for {
		if fileExists(path) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func waitClaudeTestLauncher(command *exec.Cmd) int {
	return claudeTestExitCode(command.Run())
}

func waitStartedClaudeTestLauncher(command *exec.Cmd) int {
	return claudeTestExitCode(command.Wait())
}

func claudeTestExitCode(err error) int {
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		_, _ = fmt.Fprintf(os.Stderr, "Claude test launcher: %v\n", err)
		return 127
	}
	return 0
}
