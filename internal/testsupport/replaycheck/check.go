package replaycheck

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/internal/testsupport"
	"github.com/zoumo/agent-sdk-go/internal/testsupport/mocktest"
)

// These fixtures exercise embedded replay through native SDK streams.
// The self-reexecuting peers elsewhere exercise SDK-owned synthetic native
// process behavior. They never launch a replay tool.
func Start(t *testing.T, fixture *mocktest.Session, args []string) *mocktest.Run {
	t.Helper()
	t.Cleanup(func() { assert.NoError(t, fixture.Close()) })
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
	run, err := fixture.NewRun(ctx, mocktest.RunOptions{NativeArgs: args})
	if err != nil {
		cancel()
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		_ = run.CloseSDK()
		waitCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = run.Wait(waitCtx)
		cancel()
	})
	return run
}

func RequireSuccess(t *testing.T, run *mocktest.Run) {
	t.Helper()
	result, err := run.Wait(testsupport.Deadline(t))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Execution)
	report := result.Execution
	assert.Equal(t, run.Start.ExecutionID, report.ExecutionID)
	assert.EqualValues(t, "passed", report.Status)
	assert.True(t, report.Complete)
	require.Len(t, report.Connections, 1)
	connection := report.Connections[0]
	assert.True(t, connection.FullConsumption)
	assert.True(t, connection.ClientEOF)
	assert.True(t, connection.CleanupComplete)
	assert.False(t, connection.NativeLaunched)
	assert.Nil(t, connection.ExitCode)
	assert.Empty(t, connection.ProcessReceiptPath)
}
