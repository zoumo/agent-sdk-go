package e2e_test

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// No credentials are inherited; CI and disabled cases must return before Open.
func TestE2EGates(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	for _, gate := range []struct {
		name, enabled, ci, want string
		fails                   bool
	}{
		{"disabled", "0", "", "--- SKIP:", false},
		{"CI refuses opt-in", "1", "1", "real CLI initialization is forbidden in CI", true},
		{"invalid switch", "invalid", "", "CLAUDE_E2E must be 0 or 1", true},
	} {
		t.Run(gate.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.v", "-test.run=^TestCLIInitialization$")
			cmd.Env = []string{"CLAUDE_E2E=" + gate.enabled, "CI=" + gate.ci, "CLAUDE_E2E_CLI=/nonexistent-e2e-cli", "PATH=", "GORACE=atexit_sleep_ms=0"}
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
