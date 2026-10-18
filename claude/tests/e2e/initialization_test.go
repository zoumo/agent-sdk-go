// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	claude "github.com/zoumo/agent-sdk-go/claude"
)

// TestLiveCLIInitialization is an opt-in real process startup check, not a
// model turn or the full manual compatibility checklist. Never enable in CI.
func TestLiveCLIInitialization(t *testing.T) {
	switch os.Getenv("CLAUDE_GO_LIVE") {
	case "", "0":
		t.Skip("LIVE SKIPPED: CLAUDE_GO_LIVE=0 (default); no real Agent/CLI or model calls")
	case "1":
		require.EqualValues(t, "", os.Getenv("CI"), "live CLI initialization is forbidden in CI")
	default:
		require.FailNow(t, "verification failed", "CLAUDE_GO_LIVE must be 0 or 1")
	}
	path := os.Getenv("CLAUDE_GO_LIVE_CLI")
	require.True(t, filepath.IsAbs(path), "CLAUDE_GO_LIVE=1 requires an explicit absolute CLAUDE_GO_LIVE_CLI path")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := claude.Open(ctx, claude.Options{CLIPath: path, WorkDir: t.TempDir()})
	require.True(t, err == nil, "live CLI initialization failed (diagnostics withheld; inspect privately)")
	{
		err := client.Close()
		require.True(t, err == nil, "live CLI shutdown failed (diagnostics withheld; inspect privately)")
	}
}
