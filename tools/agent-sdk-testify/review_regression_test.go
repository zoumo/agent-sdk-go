package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSelectedCodexProviderAuth(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "config"), 0700))
	source := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(source, "config.toml"), []byte(`model_provider = "selected"
[model_providers.selected]
env_key = "SDK_TEST_SELECTED_TOKEN"
env_http_headers = { "X-Account" = "SDK_TEST_ACCOUNT" }
[model_providers.other]
env_key = "SDK_TEST_OTHER_TOKEN"
`), 0600))
	t.Setenv("SDK_TEST_SELECTED_TOKEN", "synthetic-token")
	t.Setenv("SDK_TEST_ACCOUNT", "synthetic-account")
	t.Setenv("SDK_TEST_OTHER_TOKEN", "other-token")
	self, err := os.Executable()
	require.NoError(t, err)
	plan := object{"provider": "codex", "project": root, "scenario": object{}}
	require.NoError(t, prepareNative(context.Background(), &options{CLI: self, Config: source}, plan, root))
	var env map[string]string
	require.NoError(t, readJSON(filepath.Join(root, "native.env.private.json"), &env))
	require.Equal(t, "synthetic-token", env["SDK_TEST_SELECTED_TOKEN"])
	require.Equal(t, "synthetic-account", env["SDK_TEST_ACCOUNT"])
	require.NotContains(t, env, "SDK_TEST_OTHER_TOKEN")
}

func TestVersionProbeHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	cli := filepath.Join(root, "hanging-cli")
	require.NoError(t, os.WriteFile(cli, []byte("#!/bin/sh\nsleep 60\n"), 0700))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := prepareNative(ctx, &options{CLI: cli}, object{"provider": "codex"}, root)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(started), 3*time.Second)
}

func TestChangedRecordingSourceIsRejected(t *testing.T) {
	o := sample(t, "passed")
	source := filepath.Join(o.Materials, "raw/raw.jsonl")
	data, err := os.ReadFile(source)
	require.NoError(t, err)
	sum := sha256.Sum256(data)
	require.NoError(t, writeJSON(filepath.Join(o.Materials, "manifest.json"), object{"files": object{"raw.jsonl": hex.EncodeToString(sum[:])}}))
	_, err = loadMaterials(o)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(source, append(data, '\n'), 0600))
	_, err = loadMaterials(o)
	require.ErrorContains(t, err, "source hash mismatch")
}

func TestIncompleteSourceCaptureCannotPassReplay(t *testing.T) {
	for _, transport := range []object{{"captureStatus": "failed", "cleanupComplete": true}, {"captureStatus": "complete", "cleanupComplete": false}} {
		t.Run(str(transport["captureStatus"]), func(t *testing.T) {
			o := sample(t, "passed")
			require.NoError(t, writeJSON(filepath.Join(o.Materials, "raw/transport.json"), transport))
			_, err := loadMaterials(o)
			require.ErrorContains(t, err, "incomplete")
		})
	}
}

func TestIncompleteRemoteCaptureCannotPassReplay(t *testing.T) {
	for _, phase := range []string{"probe", "service"} {
		t.Run(phase, func(t *testing.T) {
			o := sample(t, "passed")
			complete := object{"captureStatus": "complete", "cleanupComplete": true}
			require.NoError(t, writeJSON(filepath.Join(o.Materials, "raw/transport.json"), complete))
			require.NoError(t, os.MkdirAll(filepath.Join(o.Materials, "remote-probe/raw"), 0700))
			probe := complete
			if phase == "probe" {
				probe = object{"captureStatus": "failed", "cleanupComplete": true}
			}
			require.NoError(t, writeJSON(filepath.Join(o.Materials, "remote-probe/raw/transport.json"), probe))
			if phase == "service" {
				require.NoError(t, writeJSON(filepath.Join(o.Materials, "remote-service.json"), object{"serverExitCode": 0, "cleanupComplete": false}))
			}
			_, err := loadMaterials(o)
			require.ErrorContains(t, err, "incomplete")
		})
	}
}
