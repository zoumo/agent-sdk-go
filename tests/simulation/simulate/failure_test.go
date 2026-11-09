package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
	agenttestify "github.com/zoumo/agent-sdk-testify"
)

func TestUnavailableAndCorruptMaterialFailExplicitly(t *testing.T) {
	t.Run("unavailable builtin", func(t *testing.T) {
		output := filepath.Join(t.TempDir(), "run")
		cmd := exec.CommandContext(t.Context(), runnerBinary, "--provider", "codex", "--case", "SDK-CASE-012", "--output", output, "--driver", driverBinary)
		b, err := cmd.CombinedOutput()
		require.Error(t, err, string(b))
		var result summary
		require.NoError(t, readJSON(filepath.Join(output, "summary.json"), &result))
		require.Len(t, result.Runs, 1)
		require.Equal(t, materialStatusUnavailable, result.Runs[0].MaterialStatus)
		require.Empty(t, result.Runs[0].MaterialError)
		require.NoDirExists(t, filepath.Join(output, "preserve", "session"))
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

func TestRemoteCancellationRetainsTypedEvidence(t *testing.T) {
	output := filepath.Join(t.TempDir(), "run")
	host := filepath.Join(t.TempDir(), "idle-remote-host")
	hostReady := filepath.Join(t.TempDir(), "host-ready")
	require.NoError(t, os.WriteFile(host, []byte("#!/bin/sh\nprintf ready > "+quote(hostReady)+"\nsleep 60\n"), 0o700))
	cmd := exec.CommandContext(context.Background(), runnerBinary,
		"--provider", "codex", "--case", "SDK-CASE-032",
		"--output", output, "--driver", host)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	require.Eventually(t, func() bool {
		_, err := os.Stat(hostReady)
		return err == nil
	}, 10*time.Second, 10*time.Millisecond)
	require.NoError(t, cmd.Process.Signal(os.Interrupt))
	require.Error(t, cmd.Wait())

	var result summary
	require.NoError(t, readJSON(filepath.Join(output, "summary.json"), &result))
	require.Equal(t, "failed", result.Status)
	require.Len(t, result.Runs, 1)
	require.Contains(t, result.Runs[0].BridgeError, "0/2 required connections")
	evidence := readSessionEvidence(t, filepath.Join(output, "preserve", "evidence.json"))
	require.Equal(t, "failed", evidence.Status)
	require.Len(t, evidence.Executions, 1)
	require.True(t, evidence.Executions[0].Complete)
	require.Len(t, evidence.Executions[0].Connections, 2)
	for _, connection := range evidence.Executions[0].Connections {
		require.Equal(t, agenttestify.ConnectionStatusFailed, agenttestify.ConnectionStatus(connection.Status))
		requireEmbeddedConnectionFacts(t, connection)
		require.Equal(t, string(agenttestify.FailureClassCanceled), connection.Failure)
	}
	require.NoDirExists(t, filepath.Join(output, "preserve", "session"))
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

func TestBusinessFailureRetainsTypedExecutionAndCleansFixture(t *testing.T) {
	output := filepath.Join(t.TempDir(), "run")
	host := filepath.Join(t.TempDir(), "host")
	resultPath := quote(filepath.Join(output, "preserve", "sdk", "result.json"))
	script := "#!/bin/sh\nset -e\n" + quote(driverBinary) + " \"$@\"\nsed 's/\"passed\"/\"failed\"/' " + resultPath + " > " + resultPath + ".tmp\nmv " + resultPath + ".tmp " + resultPath + "\n"
	require.NoError(t, os.WriteFile(host, []byte(script), 0o700))
	cmd := exec.CommandContext(t.Context(), runnerBinary, "--recording", controlledCapture(t), "--output", output, "--driver", host)
	b, err := cmd.CombinedOutput()
	require.Error(t, err, string(b))
	var result summary
	require.NoError(t, readJSON(filepath.Join(output, "summary.json"), &result))
	require.Equal(t, "failed", result.Status)
	require.Len(t, result.Runs, 1)
	require.Contains(t, result.Runs[0].Error, "SDK business failed")
	require.Empty(t, result.Runs[0].CleanupError)
	evidence := readSessionEvidence(t, filepath.Join(output, "preserve", "evidence.json"))
	require.Equal(t, "passed", evidence.Status)
	require.True(t, evidence.Executions[0].Complete)
	require.True(t, evidence.Executions[0].Connections[0].CleanupComplete)
	require.NoDirExists(t, filepath.Join(output, "preserve", "session"))
}

func TestCleanupFailureIsRetainedAfterCancellation(t *testing.T) {
	tool, err := discoverTool()
	require.NoError(t, err)
	root := t.TempDir()
	session, err := prepareSession(t.Context(), tool, sourceSelection{Recording: controlledCapture(t)}, filepath.Join(root, "session"), "preserve")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cleanupPreparedSession(session.Dir)) })
	// Deny unlinking the fixture itself, while allowing its contents to be removed.
	require.NoError(t, os.Chmod(root, 0o500))
	t.Cleanup(func() { require.NoError(t, os.Chmod(root, 0o700)) })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	outcome := finalizePreparedSession(ctx, session, nil, "", t.TempDir(), false)
	require.ErrorContains(t, outcome.Error, "remove prepared session")
	require.Equal(t, "abandoned", outcome.Evidence.Status)
	require.NotEmpty(t, outcome.Evidence.ValidationError)
}

func TestCanceledStdioExecutionRetainsTypedFailureBeforeRemoval(t *testing.T) {
	tool, err := discoverTool()
	require.NoError(t, err)
	root := t.TempDir()
	session, err := prepareSession(t.Context(), tool, sourceSelection{Recording: controlledCapture(t)}, filepath.Join(root, "session"), "preserve")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cleanupPreparedSession(session.Dir)) })
	ctx, cancel := context.WithCancel(t.Context())
	run, err := session.Runner.NewRun(ctx, mocktest.RunOptions{Connection: "main", NativeArgs: []string{"app-server", "--listen", "stdio://"}})
	require.NoError(t, err)
	cancel()
	waitCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	_, err = run.Wait(waitCtx)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, run.CloseSDK())
	report, err := session.Fixture.ExecutionReport(run.Start.ExecutionID)
	require.NoError(t, err)
	require.Equal(t, agenttestify.ExecutionStatusFailed, report.Status)
	require.Len(t, report.Connections, 1)
	connection := report.Connections[0]
	require.Equal(t, agenttestify.FailureClassCanceled, connection.Failure)
	requireEmbeddedConnectionFacts(t, evidenceExecutionView(report).Connections[0])
	outcome := finalizePreparedSession(ctx, session, nil, run.Start.ExecutionID, root, false)
	require.NotNil(t, outcome.Evidence)
	require.Equal(t, "failed", outcome.Evidence.Status)
	require.NoDirExists(t, session.Dir)
	require.NoError(t, waitCtx.Err())
}
