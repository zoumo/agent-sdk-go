package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Check the repository entry's routing with synthetic drivers and native peers.
// TestSDKRequestUsesRunnerPeers separately exercises the actual Go SDK driver.
func TestSimulationEntryStagesOutsideCheckout(t *testing.T) {
	o := sample(t, "passed")
	build, outside := t.TempDir(), t.TempDir()
	require.NoError(t, os.Symlink(o.Peer, filepath.Join(build, "agent-sdk-testify")))
	for _, name := range []string{"sdk-driver", "agent-sdk-testify-codex"} {
		wrapper := fmt.Sprintf("#!/bin/sh\nexec %q driver \"$@\"\n", o.Driver)
		require.NoError(t, os.WriteFile(filepath.Join(build, name), []byte(wrapper), 0700))
	}
	t.Setenv("AGENT_SDK_TESTIFY_BUILD_DIR", build)
	t.Setenv("PATH", build+string(os.PathListSeparator)+os.Getenv("PATH"))
	script, err := filepath.Abs("../../scripts/simulation.sh")
	require.NoError(t, err)
	capture := filepath.Join(outside, "record")
	for _, mode := range []string{"record", "replay", "simulate"} {
		materials := capture
		args := []string{script, mode}
		if mode != "simulate" {
			args = append(args, "codex")
		}
		if mode == "record" {
			materials = o.Materials
			args = append(args, "--cli", o.Driver)
		}
		output := filepath.Join(outside, mode)
		args = append(args, "--materials", materials, "--output", output)
		cmd := exec.Command("bash", args...)
		cmd.Dir = outside
		log, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", log)
		var summary struct {
			Mode   string `json:"mode"`
			Result result `json:"result"`
		}
		require.NoError(t, json.Unmarshal(log, &summary))
		require.Equal(t, mode, summary.Mode)
		require.Equal(t, "passed", summary.Result.Status)
		require.FileExists(t, filepath.Join(output, "summary.json"))
	}
	// Later stages must leave the original bytes and source manifest intact.
	require.NoError(t, verifySourcePhase(capture, true))
}
