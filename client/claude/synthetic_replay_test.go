// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"bytes"
	"context"
	"time"

	"github.com/stretchr/testify/require"

	transcript "github.com/zoumo/agent-sdk-go/claude/tests/replay"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

func (s *syntheticObservationSuite) TestSyntheticTaskFailureReplay() {
	t := s.T()
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fixture, err := transcript.Prepare(dir, bytes.NewReader(transcript.TaskFailureWire))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fixture.Close()) })
	options := SessionOptions{CLIPath: "must-not-start", InitializeTimeout: time.Second, ShutdownTimeout: time.Second}
	args, err := NativeArgs(options)
	require.NoError(t, err)
	run, err := fixture.NewRun(ctx, mocktest.RunOptions{NativeArgs: args})
	require.NoError(t, err)
	t.Cleanup(func() { _ = run.Abort(); _ = run.CloseSDK() })
	options.WorkDir, options.Env = run.Start.Workspace, run.Environment()
	actualArgs, err := NativeArgs(options)
	require.NoError(t, err)
	require.Equal(t, run.Start.NativeArgs, actualArgs)
	require.Equal(t, run.Start.Config, run.Start.Env["CLAUDE_CONFIG_DIR"])
	client, err := OpenStreams(ctx, options, StreamTransport{
		Reader: run.Reader, Writer: run.Writer, Stderr: run.Stderr, Wait: run.WaitError,
	})
	require.NoError(t, err)
	defer client.Abort()
	halfClosed := run.CloseInputWhenComplete(client.CloseInput)
	prompt := transcript.TaskFailurePrompt
	_, err = client.Submit(ctx, Input{Text: prompt})
	require.NoError(t, err)
	nativeCount := 0
	resultSequence := uint64(0)
	idleSequence := uint64(0)
	rawCount := 0
	for idleSequence == 0 {
		record, err := client.Receive(ctx)
		require.NoError(t, err)
		require.True(t, record.Observation == nil || record.Observation.Reason != "created", "error fixture cannot prove creation")
		native := record.Event
		if native == nil {
			continue
		}
		nativeCount++
		if bytes.Contains(transcript.TaskFailureWire, native.Raw) {
			rawCount++
		} else {
			require.FailNow(t, "verification failed", "Raw changed")
		}
		if native.Result != nil {
			require.False(t, !native.Result.IsError || native.Subtype != "success", "lost native error semantics")
			resultSequence = uint64(nativeCount)
			require.True(t, bytes.Contains(native.Raw, []byte(`"terminal_reason":"synthetic_error"`)), "unknown native field missing")
		}
		if native.Subtype == "session_state_changed" && native.System != nil && native.System.State != nil && *native.System.State == "idle" {
			idleSequence = uint64(nativeCount)
		}
	}
	require.Falsef(t, nativeCount != 2 || rawCount != 2 || resultSequence == 0 || idleSequence <= resultSequence, "unexpected observations native=%d result=%d idle=%d", nativeCount, resultSequence, idleSequence)
	require.NoError(t, <-halfClosed)
	select {
	case <-client.Done():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, client.Err())
	require.NoError(t, client.Close())
	result, err := run.Wait(ctx)
	require.NoError(t, err)
	require.True(t, result.Execution.Complete)
	require.Len(t, result.Execution.Connections, 1)
	connection := result.Execution.Connections[0]
	require.True(t, connection.FullConsumption)
	require.True(t, connection.ClientEOF)
	require.True(t, connection.CleanupComplete)
	require.False(t, connection.NativeLaunched)
	require.Nil(t, connection.ExitCode)
	require.Empty(t, connection.ProcessReceiptPath)
}
