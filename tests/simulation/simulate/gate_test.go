package simulate

import (
	"errors"
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	simulationOutput    = flag.String("simulation.output", "", "Caller-owned absolute evidence output; default archives a short private run")
	simulationRecording = flag.String("simulation.recording", "", "Absolute external capture; cannot fill builtin gate coverage")
	simulationProvider  = flag.String("simulation.provider", "codex", "Provider for TestSimulationCase")
	simulationCase      = flag.String("simulation.case", "SDK-CASE-001", "Builtin case for TestSimulationCase")
	simulationJobs      = flag.Int("simulation.jobs", 1, "Concurrent gate cases")
	simulationTimeout   = flag.Duration("simulation.timeout", 30*time.Second, "Per-case deadline")
)

func runSimulationTest(t *testing.T, gateMode bool) string {
	t.Helper()
	ctx, stop := signal.NotifyContext(t.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	output, err := withSimulationOutput(*simulationOutput, filepath.Join("..", "..", "..", ".artifacts", "simulation", "gates"), func(output string) error {
		err := execute(ctx, options{
			Output: output, Driver: driverBinary, Gate: gateMode,
			Provider: *simulationProvider, Case: *simulationCase, Recording: *simulationRecording,
			Jobs: *simulationJobs, Timeout: *simulationTimeout,
		})
		return errors.Join(err, ctx.Err())
	})
	t.Logf("Simulation artifacts: %s", output)
	require.NoError(t, err)
	return output
}

func TestSimulationGate(t *testing.T) {
	output := runSimulationTest(t, true)
	var report coverage
	require.NoError(t, readJSON(filepath.Join(output, "coverage.json"), &report))
	require.Equal(t, coverageMatrix{Cases: 42, Slots: 84, Required: 69, Optional: 15}, report.Matrix)
	require.Len(t, report.Cases, 84)
	require.Zero(t, report.RequiredBlocking)
	require.Zero(t, report.OptionalBlocking)
	require.Zero(t, report.MaterialFailed)
	t.Logf("Simulation counts: %v", report.Counts)
}

func TestSimulationCase(t *testing.T) {
	runSimulationTest(t, false)
}
