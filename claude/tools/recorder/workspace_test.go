// SPDX-License-Identifier: Apache-2.0
package recorder_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/claude/tools/recorder"
)

func TestIsolatedPersistentWorkspaceAndExplicitQuery(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	w, err := recorder.PrepareWorkspace(filepath.Join(base, "capture"), "r1")
	require.NoError(t, err)
	require.NotEqual(t, os.Getenv("HOME"), w.Home)
	require.Contains(t, w.Environment(), "CLAUDE_CONFIG_DIR="+w.Config)
	args, err := recorder.Arguments("r1", w)
	require.NoError(t, err)
	require.NotContains(t, args, "--no-session-persistence")
	args, err = recorder.Arguments("s1", w)
	require.NoError(t, err)
	require.Contains(t, args, "--no-session-persistence")
	const id = "11111111-1111-4111-8111-111111111111"
	dir := filepath.Join(w.Config, "projects", "-synthetic")
	require.NoError(t, os.MkdirAll(dir, 0700))
	raw := `{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"11111111-1111-4111-8111-111111111111","message":{"content":"Reply with the word Ready."}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(raw), 0600))
	page, err := claude.GetSessionMessages(context.Background(), id, claude.SessionMessagesOptions{Scope: claude.SessionQueryScope{ConfigDir: w.Config}, Limit: 2, MaxBytes: 2 << 20})
	require.NoError(t, err)
	require.Len(t, page.Messages, 1)
	require.False(t, page.Coverage.Atomic)
	_, err = recorder.PrepareWorkspace(w.Root, "r1")
	require.Error(t, err)
	alias := filepath.Join(base, "alias")
	require.NoError(t, os.Symlink(w.Root, alias))
	_, err = recorder.PrepareWorkspace(filepath.Join(alias, "new"), "r1")
	require.Error(t, err)
	require.NoError(t, w.Cleanup())
	_, err = os.Stat(w.Root)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestExecutableS1SelectsExactProxyModel(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	w, err := recorder.PrepareWorkspace(filepath.Join(base, "fresh"), "s1")
	require.NoError(t, err)
	defer w.Cleanup()
	args, err := recorder.Arguments("s1", w)
	require.NoError(t, err)
	found := false
	for i, arg := range args {
		if arg == "--model" {
			require.Less(t, i+1, len(args))
			require.Equal(t, "codex/gpt-6.1-sol", args[i+1])
			found = true
		}
	}
	require.True(t, found, "no implicit Claude alias or provider switch")
}

func TestWorkspaceRevalidationRejectsNewGitAncestry(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	w, err := recorder.PrepareWorkspace(filepath.Join(base, "fresh"), "s1")
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(base, ".git"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(base, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0600))
	_, err = recorder.Arguments("s1", w)
	require.ErrorContains(t, err, "outside Git")
	require.NoError(t, w.Cleanup())
}
