package claude_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude"
)

const historySession = "00000000-0000-4000-8000-000000000001"

func transcript(t *testing.T, rows ...map[string]any) claude.HistoryOptions {
	t.Helper()
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	home := t.TempDir()
	key := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, cwd)
	dir := filepath.Join(home, "projects", key)
	require.NoError(t, os.MkdirAll(dir, 0700))
	var data []byte
	for _, row := range rows {
		row["sessionId"] = historySession
		row["cwd"] = cwd
		b, e := json.Marshal(row)
		require.NoError(t, e)
		data = append(data, append(b, '\n')...)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, historySession+".jsonl"), data, 0600))
	return claude.HistoryOptions{SessionID: historySession, CWD: cwd, ConfigDir: home}
}

func TestHistoryBranchAndFailureContracts(t *testing.T) {
	row := func(id, parent string) map[string]any {
		return map[string]any{"type": "user", "uuid": id, "parentUuid": parent, "message": map[string]any{"role": "user", "content": "x"}}
	}
	for _, tc := range []struct {
		name    string
		rows    []map[string]any
		want    []string
		failure string
	}{
		{"latest main branch", []map[string]any{row("root", ""), row("abandoned", "root"), row("chosen", "root"), {"type": "assistant", "uuid": "side", "parentUuid": "root", "isSidechain": true, "message": map[string]any{"content": "side"}}}, []string{"root", "chosen"}, ""},
		{"terminal leaf", []map[string]any{row("root", ""), row("chosen", "root"), row("later", "root"), {"type": "assistant", "uuid": "side", "parentUuid": "later", "isSidechain": true, "message": map[string]any{"content": "side"}}}, []string{"root", "chosen"}, ""},
		{"missing message", []map[string]any{{"type": "user", "uuid": "u", "parentUuid": nil}}, nil, "message"},
		{"missing parent evidence", []map[string]any{{"type": "user", "uuid": "u", "message": map[string]any{"content": "x"}}}, nil, "parentUuid"},
		{"compaction", []map[string]any{row("old", ""), {"type": "system", "subtype": "compact_boundary", "uuid": "boundary", "parentUuid": nil, "logicalParentUuid": "old"}, row("summary", "boundary")}, []string{"boundary", "summary"}, ""},
		{"missing parent", []map[string]any{row("a", "missing")}, nil, "missing parent"},
		{"duplicate", []map[string]any{row("a", ""), row("a", "")}, nil, "duplicate"},
		{"cycle", []map[string]any{row("a", "b"), row("b", "a")}, nil, "cycle"},
		{"unknown entry", []map[string]any{{"type": "future", "uuid": "x"}}, nil, "unsupported"},
		{"unknown block retained", []map[string]any{{"type": "assistant", "uuid": "a", "parentUuid": nil, "message": map[string]any{"content": []any{map[string]any{"type": "future-block", "opaque": true}}}}}, []string{"a"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := transcript(t, tc.rows...)
			h, e := claude.Load(context.Background(), o)
			require.NoError(t, e)
			defer h.Close()
			var ids []string
			for r := range h.Records() {
				ids = append(ids, r.UUID)
				if tc.name == "unknown block retained" {
					require.Contains(t, string(r.Raw), "future-block")
				}
			}
			<-h.Done()
			if tc.failure != "" {
				require.ErrorContains(t, h.Err(), tc.failure)
				var he *claude.HistoryError
				require.ErrorAs(t, h.Err(), &he)
				require.NotEmpty(t, he.Raw)
			} else {
				require.NoError(t, h.Err())
				require.Equal(t, tc.want, ids)
			}
		})
	}
}

func historyPath(t *testing.T, o claude.HistoryOptions) string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(o.ConfigDir, "projects", "*", o.SessionID+".jsonl"))
	require.NoError(t, err)
	require.Len(t, paths, 1)
	return paths[0]
}

func TestHistoryCancellationMutationAndIdentity(t *testing.T) {
	rows := []map[string]any{{"type": "user", "uuid": "u", "parentUuid": nil, "message": map[string]any{"content": "x"}}, {"type": "assistant", "uuid": "a", "parentUuid": "u", "message": map[string]any{"content": "y"}}}
	for _, mode := range []string{"cancel", "close", "mutation", "missing", "cwd", "session", "malformed", "record limit", "line limit", "crlf", "duplicate JSON", "incomplete tail"} {
		t.Run(mode, func(t *testing.T) {
			// Clone maps because transcript adds per-test native identity.
			local := make([]map[string]any, len(rows))
			for i, r := range rows {
				local[i] = map[string]any{}
				for k, v := range r {
					local[i][k] = v
				}
			}
			o := transcript(t, local...)
			path := historyPath(t, o)
			switch mode {
			case "missing":
				o.SessionID = "00000000-0000-4000-8000-000000000002"
			case "cwd", "session":
				b, e := os.ReadFile(path)
				require.NoError(t, e)
				old, new := o.CWD, "/wrong"
				if mode == "session" {
					old, new = o.SessionID, "foreign"
				}
				require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(string(b), old, new)), 0600))
			case "malformed":
				require.NoError(t, os.WriteFile(path, []byte("{bad}\n"), 0600))
			case "duplicate JSON":
				b, e := os.ReadFile(path)
				require.NoError(t, e)
				require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(string(b), `"uuid":"u"`, `"uuid":"erased","uuid":"u"`)), 0600))
			case "incomplete tail":
				b, e := os.ReadFile(path)
				require.NoError(t, e)
				require.NoError(t, os.WriteFile(path, b[:len(b)-1], 0600))
			case "record limit":
				o.MaxRecords = 1
			case "line limit":
				o.MaxRecordBytes = 32
			case "crlf":
				b, e := os.ReadFile(path)
				require.NoError(t, e)
				require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(string(b), "\n", "\r\n")), 0600))
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h, e := claude.Load(ctx, o)
			require.NoError(t, e)
			defer h.Close()
			if mode == "cancel" || mode == "close" || mode == "mutation" {
				select {
				case r := <-h.Records():
					require.Equal(t, "u", r.UUID)
				case <-time.After(time.Second):
					t.Fatal("first record missing")
				}
				if mode == "cancel" {
					cancel()
				} else if mode == "close" {
					require.NoError(t, h.Close())
				} else {
					f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
					require.NoError(t, e)
					_, e = f.WriteString("{}\n")
					require.NoError(t, e)
					require.NoError(t, f.Close())
				}
			}
			var ids []string
			for r := range h.Records() {
				ids = append(ids, r.UUID)
			}
			select {
			case <-h.Done():
			case <-time.After(time.Second):
				t.Fatal("replay not released")
			}
			if mode == "crlf" {
				require.NoError(t, h.Err())
				require.Equal(t, []string{"u", "a"}, ids)
			} else {
				require.Error(t, h.Err())
			}
		})
	}
}

func TestHistorySessionsAreIsolated(t *testing.T) {
	o := transcript(t, map[string]any{"type": "user", "uuid": "u", "parentUuid": nil, "message": map[string]any{"content": "first session"}})
	path := historyPath(t, o)
	b, e := os.ReadFile(path)
	require.NoError(t, e)
	second := o
	second.SessionID = "00000000-0000-4000-8000-000000000002"
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(path), second.SessionID+".jsonl"), []byte(strings.ReplaceAll(strings.ReplaceAll(string(b), o.SessionID, second.SessionID), "first session", "second session")), 0600))
	slow, e := claude.Load(context.Background(), o)
	require.NoError(t, e)
	defer slow.Close()
	fast, e := claude.Load(context.Background(), second)
	require.NoError(t, e)
	defer fast.Close()
	var count int
	for r := range fast.Records() {
		require.Equal(t, second.SessionID, r.SessionID)
		require.Contains(t, string(r.Message), "second session")
		count++
	}
	require.NoError(t, fast.Err())
	require.Equal(t, 1, count)
	require.NoError(t, slow.Close())
	require.Error(t, slow.Err())
}

func TestHistoryLoadsOriginalMainChain(t *testing.T) {
	opts := transcript(t,
		map[string]any{"type": "user", "uuid": "u", "parentUuid": nil, "message": map[string]any{"role": "user", "content": "hello"}},
		map[string]any{"type": "assistant", "uuid": "a", "parentUuid": "u", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "tool-1", "name": "Read", "input": map[string]any{"file_path": "x"}}}}},
		map[string]any{"type": "user", "uuid": "r", "parentUuid": "a", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "tool-1", "content": "done"}}}},
	)
	h, err := claude.Load(context.Background(), opts)
	require.NoError(t, err)
	defer h.Close()
	var ids []string
	for r := range h.Records() {
		ids = append(ids, r.UUID)
		require.True(t, json.Valid(r.Raw))
		if r.UUID == "r" {
			require.Equal(t, "a", r.ParentUUID)
			require.Contains(t, string(r.Message), "tool-1")
		}
	}
	<-h.Done()
	require.NoError(t, h.Err())
	require.Equal(t, []string{"u", "a", "r"}, ids)
}
