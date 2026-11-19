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
