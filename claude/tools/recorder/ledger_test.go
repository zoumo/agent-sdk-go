// SPDX-License-Identifier: Apache-2.0
package recorder_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude/tools/recorder"
)

func TestDurableReservationsSurviveReopenAndFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger")
	l, err := recorder.OpenLedger(path)
	require.NoError(t, err)
	require.NoError(t, l.Reserve("r1"))
	require.NoError(t, l.Turn("r1"))
	l, err = recorder.OpenLedger(path)
	require.NoError(t, err)
	require.Error(t, l.Reserve("r2"), "unfinished attempt cannot be retried or bypassed")
	require.NoError(t, l.Turn("r1"))
	require.Error(t, l.Turn("r1"), "extra turn must not be dispatched")
	require.NoError(t, l.Finish("r1", false))
	l, err = recorder.OpenLedger(path)
	require.NoError(t, err)
	require.Error(t, l.Reserve("r1"))
	require.Error(t, l.Reserve("r2"), "failure stops dependent activity")
	data, err := os.ReadFile(filepath.Join(path, "attempts.jsonl"))
	require.NoError(t, err)
	require.Contains(t, string(data), `"outcome":"failed"`)
}

func TestAggregateBudgetAndCorruptLedgerFailClosed(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	l, err := recorder.OpenLedger(filepath.Join(base, "ledger"))
	require.NoError(t, err)
	for _, scene := range []string{"r1", "r2", "r3"} {
		require.NoError(t, l.Reserve(scene))
		require.NoError(t, l.Turn(scene))
		require.NoError(t, l.Turn(scene))
		require.NoError(t, l.Finish(scene, true))
	}
	require.Error(t, l.Reserve("s1"), "one ledger cannot grant additional roots or turns")
	require.NoError(t, os.WriteFile(filepath.Join(base, "ledger", "attempts.jsonl"), []byte(`{"sequence":1,"scene":"s1","action":"reserve"}`), 0600))
	require.Error(t, l.Reserve("s2"), "torn append cannot be healed or refunded")
}

func TestLedgerRefusesLegacySymlinkAndCrashLock(t *testing.T) {
	for _, name := range []string{"legacy", "symlink", "crash lock"} {
		t.Run(name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			path := filepath.Join(base, "ledger")
			require.NoError(t, os.Mkdir(path, 0700))
			switch name {
			case "legacy":
				require.NoError(t, os.WriteFile(filepath.Join(path, "s1.attempt"), nil, 0600))
				_, err = recorder.OpenLedger(path)
				require.Error(t, err)
			case "symlink":
				require.NoError(t, os.Symlink(path, filepath.Join(base, "alias")))
				_, err = recorder.OpenLedger(filepath.Join(base, "alias"))
				require.Error(t, err)
			case "crash lock":
				require.NoError(t, os.Mkdir(filepath.Join(path, "locked"), 0700))
				l, err := recorder.OpenLedger(path)
				require.NoError(t, err)
				require.Error(t, l.Reserve("s1"))
			}
		})
	}
}

func TestConcurrentReservationsChargeOnlyOneAttempt(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	path := filepath.Join(base, "ledger")
	l, err := recorder.OpenLedger(path)
	require.NoError(t, err)
	outcomes := make(chan error, 8)
	for range 8 {
		go func() { outcomes <- l.Reserve("r1") }()
	}
	succeeded := 0
	for range 8 {
		if <-outcomes == nil {
			succeeded++
		}
	}
	require.Equal(t, 1, succeeded)
}
