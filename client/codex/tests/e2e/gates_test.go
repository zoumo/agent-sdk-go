package e2e_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Re-execute only gate entry points with a deliberately nonexistent CLI and a
// replacement environment. These checks cannot inherit an account or run a model.
// The empty-PATH case uses a missing executable inside a fresh temporary directory.
func TestE2EGates(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	for _, entry := range []struct{ test, flag string }{
		{"TestStdioLifecycleHistory", "CODEX_E2E_STDIO"},
		{"TestStdioInteractionsInterrupt", "CODEX_E2E_INTERACTIONS"},
		{"TestWebSocketLifecycleHistory", "CODEX_E2E_WEBSOCKET"},
	} {
		for _, gate := range []struct {
			name, enabled, ci, want string
			fails                   bool
		}{
			{"disabled", "0", "", "--- SKIP:", false},
			{"CI refuses opt-in", "1", "1", "real-agent E2E tests are forbidden in CI", true},
		} {
			t.Run(entry.test+"/"+gate.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, executable, "-test.v", "-test.run=^"+entry.test+"$")
				cmd.Env = []string{entry.flag + "=" + gate.enabled, "CI=" + gate.ci, "CODEX_E2E_CLI=/nonexistent-e2e-cli", "PATH=", "GORACE=atexit_sleep_ms=0"}
				output, err := cmd.CombinedOutput()
				require.NoError(t, ctx.Err(), "gate check timed out")
				if gate.fails {
					require.Error(t, err)
				} else {
					require.NoError(t, err, "%s", output)
				}
				require.Contains(t, string(output), gate.want)
			})
		}
	}
	t.Run("empty PATH reaches CLI startup without inspection tools", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, executable, "-test.v", "-test.run=^TestWebSocketLifecycleHistory$")
		cmd.Env = []string{"CODEX_E2E_WEBSOCKET=1", "CODEX_E2E_CLI=" + filepath.Join(t.TempDir(), "missing-cli"), "CI=", "PATH=", "GORACE=atexit_sleep_ms=0"}
		output, err := cmd.CombinedOutput()
		require.NoError(t, ctx.Err())
		require.Error(t, err, "a deliberately missing CLI must fail to start")
		require.Contains(t, string(output), "owned server start failed")
		require.NotContains(t, string(output), "--- SKIP:", "an empty PATH must not skip the scenario")
	})
}
