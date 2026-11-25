package simulate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSimulationRunsWithoutStandaloneRunner(t *testing.T) {
	output, err := exec.CommandContext(t.Context(), "go", "list", "-f", "{{.Name}}", ".").CombinedOutput()
	require.NoError(t, err, string(output))
	require.NotEqual(t, "main", strings.TrimSpace(string(output)))
	_, err = os.Stat("../../../scripts/simulation.sh")
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestGoTestOutputFlagsPreserveCallerOwnedEvidence(t *testing.T) {
	for _, outputFlag := range []string{"--simulation.output", "--simulation.output=", "-simulation.output", "-simulation.output="} {
		t.Run(outputFlag, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "caller output")
			arguments := []string{"-test.run=^TestSimulationCase$", "-test.v", outputFlag, output}
			if strings.HasSuffix(outputFlag, "=") {
				arguments = []string{"-test.run=^TestSimulationCase$", "-test.v", outputFlag + output}
			}
			log, err := exec.CommandContext(t.Context(), os.Args[0], arguments...).CombinedOutput()
			require.NoError(t, err, string(log))
			require.Contains(t, string(log), "Simulation artifacts: "+output)
			var result summary
			require.NoError(t, readJSON(filepath.Join(output, "summary.json"), &result))
			require.Equal(t, "passed", result.Status)
			require.FileExists(t, filepath.Join(output, "preserve", "sdk", "stderr.log"))
		})
	}
}

func TestGoTestLongOutputArchivesClaudeHistory(t *testing.T) {
	entrypoint := prepareGateEntrypoint(t)
	output := filepath.Join(entrypoint.root, "claude evidence")
	require.Greater(t, len(output), 200)
	command := exec.CommandContext(t.Context(), os.Args[0],
		"-test.run=^TestSimulationCase$", "-test.v",
		"-simulation.provider=claude", "-simulation.case=SDK-CASE-029", "-simulation.output="+output)
	log, err := command.CombinedOutput()
	require.NoError(t, err, string(log))
	require.Contains(t, string(log), "Simulation artifacts: "+output)

	var result summary
	require.NoError(t, readJSON(filepath.Join(output, "summary.json"), &result))
	require.Equal(t, "passed", result.Status)
	require.Len(t, result.Runs, 1)
	run := result.Runs[0]
	require.Equal(t, "passed", run.Status)
	physicalTemp, err := filepath.EvalSymlinks("/tmp")
	require.NoError(t, err)
	runtime := filepath.Dir(filepath.Dir(run.Output))
	require.Equal(t, physicalTemp, filepath.Dir(runtime))
	require.NoDirExists(t, runtime)

	var evidence sessionEvidence
	require.NoError(t, readJSON(filepath.Join(output, run.Policy, "evidence.json"), &evidence))
	require.Equal(t, "passed", evidence.Status)
	require.Len(t, evidence.Executions, 1)
	require.True(t, evidence.Executions[0].Complete)
	require.Len(t, evidence.Executions[0].Connections, 1)
	connection := evidence.Executions[0].Connections[0]
	require.Equal(t, "passed", connection.Status)
	require.True(t, connection.FullConsumption)
	require.True(t, connection.ClientEOF)
	require.True(t, connection.CleanupComplete)
	require.False(t, connection.NativeLaunched)
}

func TestGoTestGateRejectsIncompleteCoverageAndRetainsEvidence(t *testing.T) {
	output := filepath.Join(t.TempDir(), "gate")
	command := exec.CommandContext(t.Context(), os.Args[0],
		"-test.run=^TestSimulationGate$", "-test.v",
		"-simulation.recording="+gateRecordingCapture(t), "-simulation.output="+output)
	log, err := command.CombinedOutput()
	require.Error(t, err, string(log))
	require.Contains(t, string(log), "--- FAIL: TestSimulationGate")
	require.Contains(t, string(log), "Simulation artifacts: "+output)
	var report coverage
	require.NoError(t, readJSON(filepath.Join(output, "coverage.json"), &report))
	require.Len(t, report.Cases, 84)
	require.Positive(t, report.RequiredBlocking)
	require.Equal(t, 1, report.Counts["passed"])
	require.Equal(t, 83, report.Counts["unverified"])
}
