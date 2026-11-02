package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMissingToolAndUnsupportedDescriptorFailExplicitly(t *testing.T) {
	source := controlledCapture(t)
	t.Run("missing tool", func(t *testing.T) {
		cmd := exec.Command(runnerBinary, "--recording", source, "--output", filepath.Join(t.TempDir(), "run"), "--driver", driverBinary)
		env := []string{}
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "AGENT_SDK_TESTIFY_BIN=") {
				env = append(env, entry)
			}
		}
		cmd.Env = env
		b, e := cmd.CombinedOutput()
		require.Error(t, e)
		require.Contains(t, string(b), "no published testify pin")
	})
	t.Run("unsupported describe schema", func(t *testing.T) {
		tool := filepath.Join(t.TempDir(), "incompatible-tool")
		require.NoError(t, os.WriteFile(tool, []byte("#!/bin/sh\nif [ \"$1\" = version ]; then echo incompatible; else echo '{\"schemaVersion\":2}'; fi\n"), 0700))
		cmd := exec.Command(runnerBinary, "--recording", source, "--output", filepath.Join(t.TempDir(), "run"), "--driver", driverBinary)
		cmd.Env = hostEnvironment(map[string]string{"AGENT_SDK_TESTIFY_BIN": tool})
		b, e := cmd.CombinedOutput()
		require.Error(t, e)
		require.Contains(t, string(b), "unsupported describe schemaVersion")
	})
	t.Run("unavailable builtin", func(t *testing.T) {
		cmd := exec.Command(runnerBinary, "--provider", "codex", "--case", "SDK-CASE-012", "--output", filepath.Join(t.TempDir(), "run"), "--driver", driverBinary)
		b, e := cmd.CombinedOutput()
		require.Error(t, e)
		require.Contains(t, string(b), "material unavailable")
	})
	t.Run("corrupt source", func(t *testing.T) {
		root := controlledCapture(t)
		require.NoError(t, os.WriteFile(filepath.Join(root, "raw/raw.jsonl"), []byte("{}\n"), 0600))
		output := filepath.Join(t.TempDir(), "run")
		cmd := exec.Command(runnerBinary, "--recording", root, "--output", output, "--driver", driverBinary)
		b, e := cmd.CombinedOutput()
		require.Error(t, e, string(b))
		require.NoDirExists(t, filepath.Join(output, "preserve"))
		require.FileExists(t, filepath.Join(output, "summary.json"))
	})
}

func TestCancellationAlwaysFinishesPreparedRun(t *testing.T) {
	source := controlledCapture(t)
	output := filepath.Join(t.TempDir(), "run")
	host := filepath.Join(t.TempDir(), "idle-host")
	require.NoError(t, os.WriteFile(host, []byte("#!/bin/sh\nsleep 60\n"), 0700))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, runnerBinary, "--recording", source, "--output", output, "--driver", host)
	require.NoError(t, cmd.Start())
	require.Eventually(t, func() bool { _, e := os.Stat(filepath.Join(output, "preserve/run.json")); return e == nil }, 10*time.Second, 10*time.Millisecond)
	require.NoError(t, cmd.Process.Signal(os.Interrupt))
	require.Error(t, cmd.Wait())
	require.NoError(t, ctx.Err())
	var d descriptor
	require.NoError(t, readJSON(filepath.Join(output, "preserve/run.json"), &d))
	require.NoDirExists(t, d.ResourceRoot)
	var f struct {
		Status          string
		CleanupComplete bool
	}
	require.NoError(t, readJSON(filepath.Join(output, "preserve/finish.json"), &f))
	require.Equal(t, "failed", f.Status)
	require.True(t, f.CleanupComplete)
	require.FileExists(t, filepath.Join(output, "summary.json"))
}
