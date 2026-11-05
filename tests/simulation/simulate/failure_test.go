package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMissingToolAndUnsupportedDescriptorFailExplicitly(t *testing.T) {
	source := controlledCapture(t)
	t.Run("missing tool", func(t *testing.T) {
		cmd := exec.CommandContext(t.Context(), runnerBinary, "--recording", source, "--output", filepath.Join(t.TempDir(), "run"), "--driver", driverBinary)
		env := []string{}
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "AGENT_SDK_TESTIFY_BIN=") {
				env = append(env, entry)
			}
		}
		cmd.Env = env
		b, err := cmd.CombinedOutput()
		require.Error(t, err)
		require.Contains(t, string(b), "no published testify pin")
	})
	t.Run("unsupported session schema", func(t *testing.T) {
		tool := fakeTool(t, `if [ "$1" = "prepare" ]; then
  last=""
  for arg in "$@"; do last="$arg"; done
  mkdir -p "$last"
  printf '%s\n' '{"schemaVersion":99}' > "$last/session.json"
  exit 0
fi
exit 0`)
		cmd := exec.CommandContext(t.Context(), runnerBinary, "--recording", source, "--output", filepath.Join(t.TempDir(), "run"), "--driver", driverBinary)
		cmd.Env = hostEnvironment(map[string]string{"AGENT_SDK_TESTIFY_BIN": tool})
		b, err := cmd.CombinedOutput()
		require.Error(t, err)
		require.Contains(t, string(b), "unsupported session schemaVersion 99")
	})
	t.Run("unavailable builtin", func(t *testing.T) {
		tool := fakeTool(t, `if [ "$1" = "prepare" ]; then
  echo 'material unavailable' >&2
  exit 1
fi
exit 0`)
		cmd := exec.CommandContext(t.Context(), runnerBinary, "--provider", "codex", "--case", "SDK-CASE-012", "--output", filepath.Join(t.TempDir(), "run"), "--driver", driverBinary)
		cmd.Env = hostEnvironment(map[string]string{"AGENT_SDK_TESTIFY_BIN": tool})
		b, err := cmd.CombinedOutput()
		require.Error(t, err)
		require.Contains(t, string(b), "material unavailable")
	})
	t.Run("corrupt source", func(t *testing.T) {
		root := controlledCapture(t)
		require.NoError(t, os.WriteFile(filepath.Join(root, "raw", "raw.jsonl"), []byte("{}\n"), 0o600))
		output := filepath.Join(t.TempDir(), "run")
		cmd := exec.CommandContext(t.Context(), runnerBinary, "--recording", root, "--output", output, "--driver", driverBinary)
		b, err := cmd.CombinedOutput()
		require.Error(t, err, string(b))
		require.FileExists(t, filepath.Join(output, "summary.json"))
		require.NoDirExists(t, filepath.Join(output, "preserve", "session"))
	})
}

func fakeTool(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-testify")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then echo 'fake 1'; exit 0; fi\n" + body + "\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700))
	return path
}

func TestCancellationAlwaysFinalizesPreparedSession(t *testing.T) {
	source := controlledCapture(t)
	output := filepath.Join(t.TempDir(), "run")
	host := filepath.Join(t.TempDir(), "idle-host")
	hostReady := filepath.Join(t.TempDir(), "host-ready")
	require.NoError(t, os.WriteFile(host, []byte("#!/bin/sh\nprintf ready > "+quote(hostReady)+"\nsleep 60\n"), 0o700))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, runnerBinary, "--recording", source, "--output", output, "--driver", host)
	require.NoError(t, cmd.Start())
	sessionPath := filepath.Join(output, "preserve", "session")
	require.Eventually(t, func() bool {
		_, err := os.Stat(hostReady)
		return err == nil
	}, 10*time.Second, 10*time.Millisecond)
	prepared, err := readPreparedSession(sessionPath)
	require.NoError(t, err)
	require.NoError(t, cmd.Process.Signal(os.Interrupt))
	require.Error(t, cmd.Wait())
	require.NoError(t, ctx.Err())
	require.NoDirExists(t, prepared.ResourceRoot)

	require.Equal(t, "abandoned", readSessionEvidence(t, filepath.Join(output, "preserve", "evidence.json")).Status)
	require.FileExists(t, filepath.Join(output, "summary.json"))
}

func TestPassedBusinessWithoutMockExecutionFails(t *testing.T) {
	source := controlledCapture(t)
	output := filepath.Join(t.TempDir(), "run")
	host := filepath.Join(t.TempDir(), "lying-host")
	script := "#!/bin/sh\nset -e\nout=$(awk -F'\"' '/\"output\"/ {print $4; exit}' \"$2\")\nprintf '{\"status\":\"passed\"}\\n' > \"$out/result.json\"\n"
	require.NoError(t, os.WriteFile(host, []byte(script), 0o700))
	cmd := exec.CommandContext(t.Context(), runnerBinary, "--recording", source, "--output", output, "--driver", host)
	b, err := cmd.CombinedOutput()
	require.Error(t, err, string(b))
	require.Contains(t, string(b), "passed without a mock execution")
}
