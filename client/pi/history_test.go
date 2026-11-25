package pi_test

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/pi"
)

func TestSavedHistoryDiscoveryAndWindows(t *testing.T) {
	scope := pi.HistoryScope{Cwd: t.TempDir(), SessionDir: t.TempDir()}
	head := savedHeader(t, "native-main", scope.Cwd, 3, map[string]any{
		"parentSession": "/outside/ancestor.jsonl", "extra": "retained",
	})
	rawEntries := []string{
		`{"type":"message","id":"root","parentId":null,"message":{"role":"user","content":"root prompt"}}`,
		`{"type":"label","id":"left","parentId":"root","targetId":"root","label":"original branch"}`,
		`{"type":"new_extension_type","id":"right","parentId":"root","details":{"value":42}}`,
	}
	mainPath := filepath.Join(scope.SessionDir, "a-renamed.jsonl")
	writeSavedFile(t, mainPath, head+"\r\n"+rawEntries[0]+"\n\n"+rawEntries[1]+"\n"+rawEntries[2])
	writeSavedFile(t, filepath.Join(scope.SessionDir, "b-legacy.jsonl"),
		savedHeader(t, "native-legacy", scope.Cwd, 0, nil)+"\n"+
			`{"type":"message","message":{"role":"hookMessage","customType":"extension","content":"legacy"}}`+"\n")
	writeSavedFile(t, filepath.Join(scope.SessionDir, "c-v2.jsonl"),
		savedHeader(t, "native-v2", scope.Cwd, 2, nil)+"\n"+
			`{"type":"message","id":"old","parentId":null,"message":{"role":"hookMessage","content":"v2"}}`+"\n")
	foreign := filepath.Join(scope.SessionDir, "foreign.jsonl")
	writeSavedFile(t, foreign, savedHeader(t, "native-main", t.TempDir(), 3, nil))
	writeSavedFile(t, filepath.Join(scope.SessionDir, "invalid.jsonl"), `{"type":"message"}`)
	writeSavedFile(t, filepath.Join(scope.SessionDir, "ignore.txt"), savedHeader(t, "text-file", scope.Cwd, 3, nil))

	sessions, err := pi.ListSessions(t.Context(), scope)
	require.NoError(t, err)
	require.Len(t, sessions, 3)
	assert.Equal(t, "native-main", sessions[0].ID)
	assert.Equal(t, "native-legacy", sessions[1].ID)
	assert.Equal(t, "native-v2", sessions[2].ID)
	info, err := pi.GetSessionInfo(t.Context(), scope, sessions[0].ID)
	require.NoError(t, err)
	assert.Equal(t, sessions[0], info)
	assert.Equal(t, mainPath, info.SessionFile)
	assert.Equal(t, scope.Cwd, info.Cwd)
	assert.Equal(t, "/outside/ancestor.jsonl", info.ParentSession)
	assert.Equal(t, "2026-11-25T02:00:00.000Z", info.Timestamp)
	assert.Equal(t, head, string(info.Raw))

	first, err := pi.ReadSessionHistory(t.Context(), scope, info.ID, pi.HistoryWindow{Limit: 2})
	require.NoError(t, err)
	require.Len(t, first.Entries, 2)
	assert.Equal(t, info, first.Info)
	assert.True(t, first.HasMore)
	assert.Equal(t, 2, first.NextOffset)
	assert.Nil(t, first.Entries[0].ParentID)
	require.NotNil(t, first.Entries[1].ParentID)
	assert.Equal(t, "root", *first.Entries[1].ParentID)
	assert.Equal(t, rawEntries[0], string(first.Entries[0].Raw))
	assert.Equal(t, rawEntries[1], string(first.Entries[1].Raw))
	last, err := pi.ReadSessionHistory(t.Context(), scope, info.ID, pi.HistoryWindow{Offset: first.NextOffset})
	require.NoError(t, err)
	require.Len(t, last.Entries, 1)
	assert.False(t, last.HasMore)
	assert.False(t, last.TrailingPartial)
	assert.Equal(t, 3, last.NextOffset)
	assert.Equal(t, "new_extension_type", last.Entries[0].Type)
	assert.Equal(t, "right", last.Entries[0].ID)
	require.NotNil(t, last.Entries[0].ParentID)
	assert.Equal(t, "root", *last.Entries[0].ParentID)
	assert.Equal(t, rawEntries[2], string(last.Entries[0].Raw))

	legacy, err := pi.ReadSessionHistory(t.Context(), scope, "native-legacy", pi.HistoryWindow{})
	require.NoError(t, err)
	require.Len(t, legacy.Entries, 1)
	assert.Equal(t, 1, legacy.Info.Version)
	assert.Empty(t, legacy.Entries[0].ID)
	assert.Nil(t, legacy.Entries[0].ParentID)
	assert.Contains(t, string(legacy.Entries[0].Raw), `"role":"hookMessage"`)
	v2, err := pi.ReadSessionHistory(t.Context(), scope, "native-v2", pi.HistoryWindow{})
	require.NoError(t, err)
	require.Len(t, v2.Entries, 1)
	assert.Equal(t, 2, v2.Info.Version)
	assert.Equal(t, "old", v2.Entries[0].ID)
	assert.Contains(t, string(v2.Entries[0].Raw), `"role":"hookMessage"`)

	// Queries do not migrate or rewrite saved data.
	content, err := os.ReadFile(mainPath)
	require.NoError(t, err)
	assert.Equal(t, head+"\r\n"+rawEntries[0]+"\n\n"+rawEntries[1]+"\n"+rawEntries[2], string(content))
}

func TestSavedHistoryScopeAndCorruption(t *testing.T) {
	scope := pi.HistoryScope{Cwd: t.TempDir(), SessionDir: t.TempDir()}
	header := savedHeader(t, "native-id", scope.Cwd, 3, nil)
	path := filepath.Join(scope.SessionDir, "one.jsonl")
	entry := `{"type":"message","id":"one","parentId":null,"message":{"role":"user","content":"one"}}`
	writeSavedFile(t, path, header+"\n"+entry+"\n"+`{"type":"message"`)
	history, err := pi.ReadSessionHistory(t.Context(), scope, "native-id", pi.HistoryWindow{})
	require.NoError(t, err)
	assert.True(t, history.TrailingPartial)
	require.Len(t, history.Entries, 1)
	assert.Equal(t, 1, history.NextOffset)

	writeSavedFile(t, path, header+"\n"+entry+"\n"+`{"type":"message"`+"\n")
	_, err = pi.GetSessionInfo(t.Context(), scope, "native-id")
	require.NoError(t, err)
	_, err = pi.ReadSessionHistory(t.Context(), scope, "native-id", pi.HistoryWindow{Limit: 1})
	require.ErrorIs(t, err, pi.ErrInvalidHistory)
	writeSavedFile(t, path, header+"\n"+entry+"\n"+`{"type":37}`)
	_, err = pi.ReadSessionHistory(t.Context(), scope, "native-id", pi.HistoryWindow{})
	require.ErrorIs(t, err, pi.ErrInvalidHistory)

	duplicate := filepath.Join(scope.SessionDir, "duplicate.jsonl")
	writeSavedFile(t, duplicate, header+"\n")
	_, err = pi.ListSessions(t.Context(), scope)
	require.ErrorIs(t, err, pi.ErrAmbiguousSession)
	_, err = pi.GetSessionInfo(t.Context(), scope, "native-id")
	require.ErrorIs(t, err, pi.ErrAmbiguousSession)
	require.NoError(t, os.Remove(duplicate))

	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	writeSavedFile(t, outside, savedHeader(t, "outside", scope.Cwd, 3, nil))
	link := filepath.Join(scope.SessionDir, "link.jsonl")
	if err := os.Symlink(outside, link); err == nil {
		_, err = pi.GetSessionInfo(t.Context(), scope, "outside")
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	subdir := filepath.Join(scope.SessionDir, "nested")
	require.NoError(t, os.Mkdir(subdir, 0o700))
	writeSavedFile(t, filepath.Join(subdir, "nested.jsonl"), savedHeader(t, "nested", scope.Cwd, 3, nil))
	_, err = pi.GetSessionInfo(t.Context(), scope, "nested")
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = pi.GetSessionInfo(t.Context(), scope, "../outside")
	require.ErrorIs(t, err, os.ErrNotExist)
	foreign := pi.HistoryScope{Cwd: t.TempDir(), SessionDir: scope.SessionDir}
	_, err = pi.GetSessionInfo(t.Context(), foreign, "native-id")
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = pi.ListSessions(t.Context(), pi.HistoryScope{Cwd: scope.Cwd, SessionDir: "relative"})
	require.Error(t, err)
	_, err = pi.ReadSessionHistory(t.Context(), scope, "native-id", pi.HistoryWindow{Offset: -1})
	require.Error(t, err)
}

func savedHeader(t *testing.T, id, cwd string, version int, extras map[string]any) string {
	t.Helper()
	fields := map[string]any{"id": id, "cwd": cwd, "type": "session", "timestamp": "2026-11-25T02:00:00.000Z"}
	if version != 0 {
		fields["version"] = version
	}
	maps.Copy(fields, extras)
	header, err := json.Marshal(fields)
	require.NoError(t, err)
	return string(header)
}

func writeSavedFile(t *testing.T, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(name, []byte(content), 0o600))
}
