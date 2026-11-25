package simulate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type gateEntrypoint struct {
	root, outputRecord string
}

func prepareGateEntrypoint(t *testing.T) gateEntrypoint {
	t.Helper()
	root := filepath.Join(t.TempDir(), strings.Repeat("deep-worktree-", 12))
	entrypoint := gateEntrypoint{
		root:         root,
		outputRecord: filepath.Join(t.TempDir(), "output"),
	}
	deepTemp := filepath.Join(root, strings.Repeat("deep-tmp-", 10))
	require.NoError(t, os.MkdirAll(deepTemp, 0o700))
	t.Setenv("TMPDIR", deepTemp)
	t.Cleanup(func() {
		content, err := os.ReadFile(entrypoint.outputRecord)
		if err == nil {
			parent := filepath.Dir(strings.TrimSpace(string(content)))
			if strings.HasPrefix(filepath.Base(parent), "sdk-simulation.") {
				require.NoError(t, os.RemoveAll(parent))
			}
		}
	})
	return entrypoint
}

func (entrypoint gateEntrypoint) archive(t *testing.T) string {
	t.Helper()
	archives, err := filepath.Glob(filepath.Join(entrypoint.root, ".artifacts", "simulation", "gates", "gate.*"))
	require.NoError(t, err)
	require.Len(t, archives, 1)
	return archives[0]
}

func (entrypoint gateEntrypoint) writeEvidence(output string) error {
	if err := os.MkdirAll(filepath.Join(output, "claude-SDK-CASE-029"), 0o700); err != nil {
		return err
	}
	for path, content := range map[string]string{
		filepath.Join(output, "runtime-root.txt"):                    output + "\n",
		filepath.Join(output, "coverage.json"):                       "{\"counts\":{\"passed\":1},\"marker\":\"unchanged\"}\n",
		filepath.Join(output, "claude-SDK-CASE-029", "summary.json"): "{\"Status\":\"passed\",\"marker\":\"unchanged\"}\n",
		entrypoint.outputRecord:                                      output + "\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (entrypoint gateEntrypoint) archiveRoot() string {
	return filepath.Join(entrypoint.root, ".artifacts", "simulation", "gates")
}

func requireGateEvidence(t *testing.T, output string) {
	t.Helper()
	coverage, err := os.ReadFile(filepath.Join(output, "coverage.json"))
	require.NoError(t, err)
	require.Equal(t, digest([]byte("{\"counts\":{\"passed\":1},\"marker\":\"unchanged\"}\n")), digest(coverage))
	summary, err := os.ReadFile(filepath.Join(output, "claude-SDK-CASE-029", "summary.json"))
	require.NoError(t, err)
	require.Equal(t, digest([]byte("{\"Status\":\"passed\",\"marker\":\"unchanged\"}\n")), digest(summary))
}

func TestDefaultGateArchivesShortRuntimeFromDeepCheckout(t *testing.T) {
	for _, status := range []string{"0", "37"} {
		t.Run("exit-"+status, func(t *testing.T) {
			entrypoint := prepareGateEntrypoint(t)
			var runErr error
			if status == "37" {
				runErr = errors.New("simulation exited with status 37")
			}
			artifacts, err := withSimulationOutput("", entrypoint.archiveRoot(), func(output string) error {
				return errors.Join(entrypoint.writeEvidence(output), runErr)
			})
			if status == "0" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, runErr)
			}
			archive := entrypoint.archive(t)
			require.Equal(t, archive, artifacts)
			requireGateEvidence(t, archive)
			runtime, err := os.ReadFile(filepath.Join(archive, "runtime-root.txt"))
			require.NoError(t, err)
			executionOutput := strings.TrimSpace(string(runtime))
			physicalRoot, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(executionOutput)))
			require.NoError(t, err)
			physicalOutput := filepath.Join(physicalRoot, filepath.Base(filepath.Dir(executionOutput)), filepath.Base(executionOutput))
			workspace := filepath.Join(physicalOutput,
				"claude-SDK-CASE-029", "preserve", "session", "runtime", "executions",
				strings.Repeat("0", 32), "workspace")
			require.LessOrEqual(t, len(workspace), 200)
			require.Equal(t, physicalOutput, executionOutput)
			require.NotContains(t, executionOutput, entrypoint.root)
			require.NoDirExists(t, filepath.Dir(executionOutput))
			info, err := os.Stat(archive)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
		})
	}
}

func TestGateExplicitOutputRemainsCallerOwned(t *testing.T) {
	for _, name := range []string{"caller output", "caller-output"} {
		t.Run(name, func(t *testing.T) {
			entrypoint := prepareGateEntrypoint(t)
			output := filepath.Join(t.TempDir(), name)
			artifacts, err := withSimulationOutput(output, entrypoint.archiveRoot(), entrypoint.writeEvidence)
			require.NoError(t, err)
			require.Equal(t, output, artifacts)
			requireGateEvidence(t, output)
			recorded, err := os.ReadFile(entrypoint.outputRecord)
			require.NoError(t, err)
			executionOutput := strings.TrimSpace(string(recorded))
			require.NotEqual(t, output, executionOutput)
			require.NoDirExists(t, filepath.Dir(executionOutput))
			require.NoDirExists(t, filepath.Join(entrypoint.root, ".artifacts", "simulation", "gates"))
		})
	}
}

func TestDefaultGateArchiveFailureRetainsRuntime(t *testing.T) {
	entrypoint := prepareGateEntrypoint(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(entrypoint.archiveRoot()), 0o700))
	require.NoError(t, os.WriteFile(entrypoint.archiveRoot(), []byte("blocked"), 0o600))
	artifacts, runErr := withSimulationOutput("", entrypoint.archiveRoot(), entrypoint.writeEvidence)
	require.Error(t, runErr)
	runtime, err := os.ReadFile(entrypoint.outputRecord)
	require.NoError(t, err)
	executionOutput := strings.TrimSpace(string(runtime))
	requireGateEvidence(t, executionOutput)
	require.Equal(t, executionOutput, artifacts)
	require.ErrorContains(t, runErr, executionOutput)
}

func TestGateExistingArchiveRetainsRuntimeAndCallerEvidence(t *testing.T) {
	entrypoint := prepareGateEntrypoint(t)
	output := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(output, "coverage.json"), []byte("existing evidence\n"), 0o600))
	artifacts, err := withSimulationOutput(output, entrypoint.archiveRoot(), entrypoint.writeEvidence)
	require.ErrorIs(t, err, os.ErrExist)
	require.ErrorContains(t, err, artifacts)
	requireGateEvidence(t, artifacts)
	original, err := os.ReadFile(filepath.Join(output, "coverage.json"))
	require.NoError(t, err)
	require.Equal(t, "existing evidence\n", string(original))
}

func TestDefaultGateCancellationWaitsAndArchivesEvidence(t *testing.T) {
	entrypoint := prepareGateEntrypoint(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ready, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	type result struct {
		artifacts string
		err       error
	}
	done := make(chan result, 1)
	go func() {
		artifacts, err := withSimulationOutput("", entrypoint.archiveRoot(), func(output string) error {
			if err := entrypoint.writeEvidence(output); err != nil {
				return err
			}
			close(ready)
			<-ctx.Done()
			close(canceled)
			<-release
			return errors.Join(ctx.Err(), os.WriteFile(filepath.Join(output, "canceled.txt"), []byte("canceled\n"), 0o600))
		})
		done <- result{artifacts, err}
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("simulation did not start")
	}
	cancel()
	<-canceled
	select {
	case <-done:
		t.Fatal("archived before the canceled simulation finished")
	default:
	}
	close(release)
	var completed result
	select {
	case completed = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("simulation did not finish after cancellation")
	}
	require.ErrorIs(t, completed.err, context.Canceled)
	archive := entrypoint.archive(t)
	require.Equal(t, archive, completed.artifacts)
	requireGateEvidence(t, archive)
	require.FileExists(t, filepath.Join(archive, "canceled.txt"))
	runtime, err := os.ReadFile(entrypoint.outputRecord)
	require.NoError(t, err)
	require.NoDirExists(t, filepath.Dir(strings.TrimSpace(string(runtime))))
}
