// SPDX-License-Identifier: Apache-2.0
package recorder_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude/tools/recorder"
)

func TestCaptureOnlyNewOwnedSyntheticStorageSeparatesWireAndSidecars(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	w, err := recorder.PrepareWorkspace(filepath.Join(base, "capture"), "r1")
	require.NoError(t, err)
	const parent = "11111111-1111-4111-8111-111111111111"
	project := filepath.Join(w.Config, "projects", regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(w.Project, "-"))
	children := filepath.Join(project, parent, "subagents")
	require.NoError(t, os.MkdirAll(filepath.Join(children, "workflows", "run"), 0700))
	files := map[string]string{
		filepath.Join(project, parent+".jsonl"):                          "{\"type\":\"user\",\"uuid\":\"root\"}\n",
		filepath.Join(children, "agent-a.jsonl"):                         "{\"type\":\"assistant\",\"unknown\":null}\n",
		filepath.Join(children, "agent-a.meta.json"):                     `{"toolUseId":"tool-a","parentAgentId":null}`,
		filepath.Join(children, "workflows", "run", "agent-c.jsonl"):     "{\"type\":\"assistant\"}\n",
		filepath.Join(children, "workflows", "run", "agent-c.meta.json"): `{"parentAgentId":"a","toolUseId":"tool-c"}`,
	}
	for path, raw := range files {
		require.NoError(t, os.WriteFile(path, []byte(raw), 0600))
	}
	limits := recorder.StorageLimits{Bytes: 4096, Files: 5, Entries: 10, Depth: 2}
	capture, err := w.CaptureStorage(context.Background(), parent, limits)
	require.NoError(t, err)
	require.Len(t, capture.Files, 5)
	require.False(t, capture.Atomic)
	require.False(t, capture.Publishable)
	for _, file := range capture.Files {
		require.Equal(t, files[filepath.Join(project, file.Path)], string(file.Raw))
		require.Len(t, file.SHA256, 64)
	}
	limits.Files = 4
	partial, err := w.CaptureStorage(context.Background(), parent, limits)
	require.Error(t, err)
	require.Empty(t, partial.Files)
	limits.Files = 5
	unknown := filepath.Join(children, "new-native-store.bin")
	require.NoError(t, os.WriteFile(unknown, []byte("quarantined"), 0600))
	_, err = w.CaptureStorage(context.Background(), parent, limits)
	require.ErrorIs(t, err, recorder.ErrStorageQuarantine)
	require.NoError(t, os.Remove(unknown))
	require.NoError(t, os.Symlink(project, filepath.Join(children, "escape")))
	_, err = w.CaptureStorage(context.Background(), parent, limits)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = w.CaptureStorage(ctx, parent, limits)
	require.ErrorIs(t, err, context.Canceled)
	_, err = (&recorder.Workspace{Config: w.Config}).CaptureStorage(context.Background(), parent, limits)
	require.Error(t, err, "copied path alone cannot claim workspace ownership")
}
