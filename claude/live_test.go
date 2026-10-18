// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLiveCLIInitialization is an opt-in real process startup check, not a
// model turn or the full manual compatibility checklist. Never enable in CI.
func TestLiveCLIInitialization(t *testing.T) {
	switch os.Getenv("CLAUDE_GO_LIVE") {
	case "", "0":
		t.Skip("LIVE SKIPPED: CLAUDE_GO_LIVE=0 (default); no real Agent/CLI or model calls")
	case "1":
		if os.Getenv("CI") != "" {
			t.Fatal("live CLI initialization is forbidden in CI")
		}
	default:
		t.Fatal("CLAUDE_GO_LIVE must be 0 or 1")
	}
	path := os.Getenv("CLAUDE_GO_LIVE_CLI")
	if !filepath.IsAbs(path) {
		t.Fatal("CLAUDE_GO_LIVE=1 requires an explicit absolute CLAUDE_GO_LIVE_CLI path")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := Open(ctx, Options{CLIPath: path, WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal("live CLI initialization failed (diagnostics withheld; inspect privately)")
	}
	if err := client.Close(); err != nil {
		t.Fatal("live CLI shutdown failed (diagnostics withheld; inspect privately)")
	}
}
