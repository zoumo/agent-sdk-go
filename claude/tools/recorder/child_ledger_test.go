// SPDX-License-Identifier: Apache-2.0
package recorder_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude/tools/recorder"
)

func TestChildAttemptsAreDurableCumulativeAndNeverRefunded(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	path := filepath.Join(base, "ledger")
	l, err := recorder.OpenLedger(path)
	require.NoError(t, err)
	require.NoError(t, l.Reserve("r1"))
	require.NoError(t, l.ConfigureChildren("r1", recorder.ChildLimits{TotalAttempts: 3, Attempts: 3, Depth: 2, Concurrent: 2}))
	require.NoError(t, l.Turn("r1"))
	require.NoError(t, l.ChildAttempt("r1", "a", ""))
	require.NoError(t, l.ChildAttempt("r1", "c", "a"))
	require.Error(t, l.ChildAttempt("r1", "too-deep", "c"))
	require.Error(t, l.ChildAttempt("r1", "b", ""), "concurrent cap is distinct")
	require.NoError(t, l.ChildFinish("r1", "c", false))
	l, err = recorder.OpenLedger(path)
	require.NoError(t, err)
	require.Error(t, l.ConfigureChildren("r1", recorder.ChildLimits{TotalAttempts: 9, Attempts: 9, Depth: 9, Concurrent: 9}))
	require.Error(t, l.ChildAttempt("r1", "c", "a"), "failed IDs cannot be reused")
	require.NoError(t, l.ChildAttempt("r1", "b", ""))
	require.NoError(t, l.ChildFinish("r1", "b", true))
	require.NoError(t, l.ChildFinish("r1", "a", true))
	require.Error(t, l.ChildAttempt("r1", "fourth", ""), "completion does not refund attempts")
	require.NoError(t, l.Finish("r1", false))
	require.Error(t, l.ChildAttempt("r1", "late", ""))
}

func TestBackgroundCompletionAndAggregateChildrenAreSeparateFromRoot(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	l, err := recorder.OpenLedger(filepath.Join(base, "ledger"))
	require.NoError(t, err)
	limits := recorder.ChildLimits{TotalAttempts: 1, Attempts: 1, Depth: 1, Concurrent: 1}
	require.NoError(t, l.Reserve("r1"))
	require.NoError(t, l.ConfigureChildren("r1", limits))
	require.NoError(t, l.Turn("r1"))
	require.Error(t, l.ChildAttempt("r1", "nested", "unknown"))
	require.NoError(t, l.ChildAttempt("r1", "background", ""))
	require.NoError(t, l.Turn("r1"))
	require.Error(t, l.Finish("r1", true), "root result cannot complete background work")
	require.NoError(t, l.ChildFinish("r1", "background", true))
	require.NoError(t, l.Finish("r1", true))
	require.NoError(t, l.Reserve("r2"))
	require.NoError(t, l.ConfigureChildren("r2", limits))
	require.NoError(t, l.Turn("r2"))
	require.Error(t, l.ChildAttempt("r2", "new-root-child", ""), "new root never resets aggregate attempts")
}
