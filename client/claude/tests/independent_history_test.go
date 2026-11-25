package claude_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/claude"
	"github.com/zoumo/agent-sdk-go/internal/testsupport"
	claudetest "github.com/zoumo/agent-sdk-go/internal/testsupport/claude"
)

func TestIndependentHistoryBrowser(t *testing.T) {
	t.Run("history-query/claude", claudeIndependentHistory)
}

const querySession = "11111111-1111-4111-8111-111111111111"

func claudeQueryFixture(t *testing.T, source string) (claude.SessionQueryScope, string) {
	t.Helper()
	config := t.TempDir()
	path := filepath.Join(config, "projects", "-synthetic", querySession+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(source), 0o400))
	return claude.SessionQueryScope{ConfigDir: config}, path
}

func queryLine(id, parent, kind, extra, message string) string {
	p := "null"
	if parent != "" {
		p = fmt.Sprintf("%q", parent)
	}
	return fmt.Sprintf(`{"type":%q,"uuid":%q,"parentUuid":%s,"sessionId":%q,"timestamp":"2026-01-01T00:00:00Z",%s"message":%s}`+"\n", kind, id, p, querySession, extra, message)
}

func TestClaudeIndependentMessages(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no runnable Claude runtime
	scope, path := claudeQueryFixture(t, queryLine("u", "", "user", "", `{"content":"hello","future":9007199254740993}`)+queryLine("a", "u", "assistant", "", `{"content":[{"type":"future","n":9007199254740993}]}`))
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	page, err := claude.GetSessionMessages(context.Background(), querySession, claude.SessionMessagesOptions{Scope: scope})
	require.NoError(t, err)
	require.Equal(t, "complete", page.Coverage.Status)
	require.Len(t, page.Messages, 2)
	require.Equal(t, "u", page.Messages[0].UUID)
	require.Equal(t, querySession, page.Messages[0].SessionID)
	require.Contains(t, string(page.Messages[1].Message), "9007199254740993")
	require.Contains(t, string(page.Messages[0].Raw), `"parentUuid":null`)
	page.Messages[0].Raw[0] = 'x'
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, sha256.Sum256(before), sha256.Sum256(after))
	stat, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o400), stat.Mode().Perm())
}

// These cases enter through the native adapter, with expected message order
// taken from the pinned official chain/recovery algorithm, not live Events.
func TestClaudeMessageSelection(t *testing.T) {
	cases := []struct {
		name, source string
		want         []string
	}{
		{"branch", queryLine("u", "", "user", "", `{"content":"root"}`) + queryLine("old", "u", "assistant", "", `{"content":"abandoned"}`) + queryLine("new", "u", "assistant", "", `{"content":"chosen"}`), []string{"u", "new"}},
		{"compaction", queryLine("old", "", "user", "", `{"content":"before"}`) + `{"type":"system","uuid":"boundary","parentUuid":null,"sessionId":"` + querySession + `","subtype":"compact_boundary","logicalParentUuid":"old"}` + "\n" + queryLine("summary", "boundary", "user", `"isCompactSummary":true,`, `{"content":"summary"}`) + queryLine("a", "summary", "assistant", "", `{"content":"after"}`), []string{"summary", "a"}},
		{"filtered", queryLine("u", "", "user", "", `{"content":"root"}`) + queryLine("meta", "u", "user", `"isMeta":true,`, `{"content":"meta"}`) + queryLine("a", "meta", "assistant", "", `{"content":"answer"}`) + queryLine("side", "u", "assistant", `"isSidechain":true,`, `{"content":"side"}`) + queryLine("team", "u", "assistant", `"teamName":"x",`, `{"content":"team"}`), []string{"u", "a"}},
		{"parallel", queryLine("u", "", "user", "", `{"content":"root"}`) + queryLine("a1", "u", "assistant", "", `{"id":"response","content":[{"type":"tool_use","id":"t1"}]}`) + queryLine("a2", "a1", "assistant", "", `{"id":"response","content":[{"type":"tool_use","id":"t2"}]}`) + queryLine("r1", "a1", "user", "", `{"content":[{"type":"tool_result","tool_use_id":"t1"}]}`) + queryLine("r2", "a2", "user", "", `{"content":[{"type":"tool_result","tool_use_id":"t2"}]}`) + queryLine("end", "r1", "assistant", "", `{"id":"end","content":"done"}`), []string{"u", "a1", "a2", "r2", "r1", "end"}},
		{"empty", "", []string{}},
		{"side-terminal-main-ancestor", queryLine("u", "", "user", "", `{"content":"main"}`) + queryLine("side", "u", "assistant", `"isSidechain":true,`, `{"content":"side"}`), []string{"u"}},
		{"only-side", queryLine("side", "", "user", `"isSidechain":true,`, `{"content":"side"}`), []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scope, _ := claudeQueryFixture(t, tc.source)
			page, err := claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
			require.NoError(t, err)
			got := []string{}
			for _, m := range page.Messages {
				got = append(got, m.UUID)
			}
			require.Equal(t, tc.want, got)
			page, err = claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope, Offset: 1, Limit: 1})
			require.NoError(t, err)
			if len(tc.want) > 1 {
				require.Len(t, page.Messages, 1)
				require.Equal(t, tc.want[1], page.Messages[0].UUID)
			} else {
				require.Empty(t, page.Messages)
			}
			page, err = claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope, Offset: 1000})
			require.NoError(t, err)
			require.Empty(t, page.Messages)
		})
	}
}

func TestClaudeMessageQueryFailures(t *testing.T) {
	valid := queryLine("u", "", "user", "", `{"content":"hello"}`)
	cases := []struct {
		name, source, kind string
		change             func(*claude.SessionMessagesOptions)
	}{
		{"malformed", "{\n", "corrupt", nil},
		{"duplicate-key", `{"type":"user","type":"assistant"}` + "\n", "corrupt", nil},
		{"duplicate-uuid", valid + valid, "corrupt", nil},
		{"cycle", queryLine("u", "a", "user", "", `{"content":"hello"}`) + queryLine("a", "u", "assistant", "", `{"content":"cycle"}`), "corrupt", nil},
		{"missing-parent", queryLine("u", "missing", "user", "", `{"content":"hello"}`), "corrupt", nil},
		{"unsupported", `{"type":"future"}` + "\n", "unsupported", nil},
		{"truncated", valid[:len(valid)-1], "corrupt", nil},
		{"metadata-identity", `{"type":"custom-title","sessionId":"another"}` + "\n", "corrupt", nil},
		{"negative-limit", valid, "invalid-input", func(o *claude.SessionMessagesOptions) { o.Limit = -1 }},
		{"negative-offset", valid, "invalid-input", func(o *claude.SessionMessagesOptions) { o.Offset = -1 }},
		{"bytes", valid, "resource-limit", func(o *claude.SessionMessagesOptions) { o.MaxBytes = 1 }},
		{"lines", valid + `{"type":"summary"}` + "\n", "resource-limit", func(o *claude.SessionMessagesOptions) { o.MaxLines = 1 }},
		{"index", valid + queryLine("a", "u", "assistant", "", `{"content":"answer"}`), "resource-limit", func(o *claude.SessionMessagesOptions) { o.MaxRecords = 1 }},
		{"line-size", valid, "resource-limit", func(o *claude.SessionMessagesOptions) { o.MaxLineBytes = 10 }},
		{"return-limit", valid, "resource-limit", func(o *claude.SessionMessagesOptions) { o.Limit = 2; o.MaxMessages = 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scope, _ := claudeQueryFixture(t, tc.source)
			o := claude.SessionMessagesOptions{Scope: scope}
			if tc.change != nil {
				tc.change(&o)
			}
			page, err := claude.GetSessionMessages(testsupport.Deadline(t), querySession, o)
			var qe *claude.SessionQueryError
			require.ErrorAs(t, err, &qe)
			require.Equal(t, tc.kind, qe.Kind)
			require.Empty(t, page.Messages)
			require.NotEqual(t, "complete", page.Coverage.Status)
		})
	}
}

func TestClaudeQueryScope(t *testing.T) {
	valid := queryLine("u", "", "user", "", `{"content":"hello"}`)
	t.Run("inaccessible", func(t *testing.T) {
		scope, path := claudeQueryFixture(t, valid)
		scope.ConfigDir = path
		p, e := claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
		requireQueryKind(t, p, e, "inaccessible")
	})
	t.Run("missing", func(t *testing.T) {
		scope, path := claudeQueryFixture(t, valid)
		require.NoError(t, os.Remove(path))
		p, e := claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
		requireQueryKind(t, p, e, "missing")
	})
	t.Run("ambiguous", func(t *testing.T) {
		scope, path := claudeQueryFixture(t, valid)
		other := filepath.Join(scope.ConfigDir, "projects", "-other", filepath.Base(path))
		require.NoError(t, os.MkdirAll(filepath.Dir(other), 0o700))
		require.NoError(t, os.WriteFile(other, []byte(valid), 0o400))
		p, e := claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
		requireQueryKind(t, p, e, "ambiguous")
	})
	t.Run("entry-limit", func(t *testing.T) {
		scope, _ := claudeQueryFixture(t, valid)
		require.NoError(t, os.Mkdir(filepath.Join(scope.ConfigDir, "projects", "-other"), 0o700))
		scope.MaxEntries = 1
		p, e := claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
		requireQueryKind(t, p, e, "resource-limit")
	})
	t.Run("symlink-file", func(t *testing.T) {
		scope, path := claudeQueryFixture(t, valid)
		outside := filepath.Join(t.TempDir(), "private")
		require.NoError(t, os.WriteFile(outside, []byte(valid), 0o400))
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Symlink(outside, path))
		p, e := claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
		requireQueryKind(t, p, e, "unsupported")
	})
	t.Run("symlink-project", func(t *testing.T) {
		scope, path := claudeQueryFixture(t, valid)
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Remove(filepath.Dir(path)))
		require.NoError(t, os.Symlink(t.TempDir(), filepath.Dir(path)))
		p, e := claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
		requireQueryKind(t, p, e, "unsupported")
	})
	t.Run("invalid-input", func(t *testing.T) {
		for _, id := range []string{"../session", "", "not-a-uuid"} {
			p, e := claude.GetSessionMessages(testsupport.Deadline(t), id, claude.SessionMessagesOptions{Scope: claude.SessionQueryScope{ConfigDir: t.TempDir()}})
			requireQueryKind(t, p, e, "invalid-input")
		}
		p, e := claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: claude.SessionQueryScope{ConfigDir: "relative"}})
		requireQueryKind(t, p, e, "invalid-input")
	})
	t.Run("canceled", func(t *testing.T) {
		scope, _ := claudeQueryFixture(t, valid)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		p, e := claude.GetSessionMessages(ctx, querySession, claude.SessionMessagesOptions{Scope: scope})
		require.ErrorIs(t, e, context.Canceled)
		require.Empty(t, p.Messages)
	})
	t.Run("explicit-project", func(t *testing.T) {
		scope, path := claudeQueryFixture(t, valid)
		target := t.TempDir()
		project := filepath.Join(t.TempDir(), "project-alias")
		require.NoError(t, os.Symlink(target, project))
		scope.ProjectDir = project
		// ProjectDir is canonicalized by contract. Keep the alias as input,
		// but place synthetic storage under the actual project's native key.
		canonical, err := filepath.EvalSymlinks(project)
		require.NoError(t, err)
		key := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(canonical, "-")
		dest := filepath.Join(scope.ConfigDir, "projects", key)
		require.NoError(t, os.Rename(filepath.Dir(path), dest))
		p, e := claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
		require.NoError(t, e)
		require.Len(t, p.Messages, 1)
		require.Equal(t, "u", p.Messages[0].UUID)
	})
}

func requireQueryKind(t *testing.T, p claude.SessionMessagesPage, e error, kind string) {
	t.Helper()
	var qe *claude.SessionQueryError
	require.ErrorAs(t, e, &qe)
	require.Equal(t, kind, qe.Kind)
	require.Empty(t, p.Messages)
}

func TestClaudeParallelStrays(t *testing.T) {
	source := queryLine("u", "", "user", "", `{"content":"root"}`) +
		queryLine("a1", "u", "assistant", "", `{"id":"R","content":[{"type":"tool_use","id":"t1"}]}`) +
		queryLine("a2", "a1", "assistant", "", `{"id":"R","content":[{"type":"tool_use","id":"t2"}]}`) +
		queryLine("r2", "a2", "user", "", `{"content":[{"type":"tool_result","tool_use_id":"t2"}]}`) +
		queryLine("stray", "u", "user", `"sourceToolAssistantUUID":"a1",`, `{"content":[{"type":"tool_result","tool_use_id":"t1"}]}`) +
		queryLine("already-answered", "u", "user", "", `{"content":[{"type":"tool_result","tool_use_id":"t2"}]}`) +
		queryLine("another-writer", "u", "user", `"agentId":"child",`, `{"content":[{"type":"tool_result","tool_use_id":"t1"}]}`) +
		queryLine("empty-writer", "u", "user", `"agentId":"",`, `{"content":[{"type":"tool_result","tool_use_id":"t1"}]}`) +
		queryLine("end", "r2", "assistant", "", `{"id":"end","content":"done"}`)
	scope, _ := claudeQueryFixture(t, source)
	p, e := claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
	require.NoError(t, e)
	ids := []string{}
	for _, m := range p.Messages {
		ids = append(ids, m.UUID)
	}
	require.Equal(t, []string{"u", "a1", "a2", "stray", "r2", "end"}, ids)
}

func TestClaudeQueryDefaultReturnBound(t *testing.T) {
	var source strings.Builder
	parent := ""
	for i := range 1001 {
		id := strconv.Itoa(i)
		source.WriteString(queryLine(id, parent, "user", "", `{"content":"saved"}`))
		parent = id
	}
	scope, _ := claudeQueryFixture(t, source.String())
	p, e := claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
	require.NoError(t, e)
	require.Len(t, p.Messages, 1000)
	require.Equal(t, "999", p.Messages[999].UUID)
	p.Messages[0].Message[0] = 'x'
	fresh, e := claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope, Offset: 1000, Limit: 1})
	require.NoError(t, e)
	require.Len(t, fresh.Messages, 1)
	require.Equal(t, "1000", fresh.Messages[0].UUID)
}

func TestClaudeQueryAlongsideSession(t *testing.T) {
	o, trace := claudeTargetOptions(t, "session-approval")
	session, e := claude.Open(testsupport.Deadline(t), o)
	require.NoError(t, e)
	t.Cleanup(func() { _ = session.Close() })
	claudeUntil(t, session, "init")
	_, e = session.Submit(testsupport.Deadline(t), claude.Input{Text: "synthetic"})
	require.NoError(t, e)
	pending := claudetest.Requests(t, session, 2)
	require.NoError(t, session.Interrupt(testsupport.Deadline(t), claude.InterruptOptions{}))
	claudeUntil(t, session, "fixture_interrupt")
	before, e := os.ReadFile(trace)
	require.NoError(t, e)
	scope, _ := claudeQueryFixture(t, queryLine("saved", "", "user", "", `{"content":"stored"}`))
	p, e := claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
	require.NoError(t, e)
	require.Len(t, p.Messages, 1)
	listed, e := claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: scope})
	require.NoError(t, e)
	require.Len(t, listed.Sessions, 1)
	metadata, e := claude.GetSessionInfo(testsupport.Deadline(t), querySession, claude.SessionInfoOptions{Scope: scope})
	require.NoError(t, e)
	require.Equal(t, "stored", metadata.Info.Summary)
	after, e := os.ReadFile(trace)
	require.NoError(t, e)
	require.Equal(t, before, after)
	require.Zero(t, testsupport.CountAnswers(t, trace, "claude"))
	require.NoError(t, pending[0].Deny(testsupport.Deadline(t), claude.DenialOptions{Message: "fixture decision"}))
	require.NoError(t, pending[1].Approve(testsupport.Deadline(t), claude.ApprovalOptions{}))
	require.NoError(t, session.Interrupt(testsupport.Deadline(t), claude.InterruptOptions{}))
	live := claudeUntil(t, session, "fixture_interrupt")
	for _, r := range live {
		if r.Event != nil {
			require.NotContains(t, string(r.Raw), `"uuid":"saved"`)
		}
	}
	require.Equal(t, 2, testsupport.CountAnswers(t, trace, "claude"))
}

func TestClaudeNullToolIdentity(t *testing.T) {
	source := queryLine("u", "", "user", "", `{"content":"root"}`) +
		queryLine("a", "u", "assistant", "", `{"id":"response","content":[{"type":"tool_use","id":null}]}`) +
		queryLine("unrelated", "u", "user", "", `{"content":[{"type":"tool_result","tool_use_id":null}]}`) +
		queryLine("end", "a", "assistant", "", `{"content":"done"}`)
	scope, _ := claudeQueryFixture(t, source)
	p, e := claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
	require.NoError(t, e)
	ids := []string{}
	for _, m := range p.Messages {
		ids = append(ids, m.UUID)
	}
	require.Equal(t, []string{"u", "a", "end"}, ids)
}

func TestClaudeRecoveredTimestampOrder(t *testing.T) {
	line := func(id, parent, kind, time, content string) string {
		return strings.Replace(queryLine(id, parent, kind, "", content), "2026-01-01T00:00:00Z", time, 1)
	}
	source := queryLine("u", "", "user", "", `{"content":"root"}`) +
		line("c0", "u", "assistant", "2026-01-01T00:00:00Z", `{"id":"R","content":[{"type":"tool_use","id":"t0"}]}`) +
		line("c2", "c1", "assistant", "2026-01-01T00:00:02Z", `{"id":"R","content":[{"type":"tool_use","id":"t2"}]}`) +
		line("c1", "c0", "assistant", "2026-01-01T00:00:01Z", `{"id":"R","content":[{"type":"tool_use","id":"t1"}]}`) +
		line("r1", "c1", "user", "2026-01-01T00:00:04Z", `{"content":[{"type":"tool_result","tool_use_id":"t1"}]}`) +
		line("r2", "c2", "user", "2026-01-01T00:00:03Z", `{"content":[{"type":"tool_result","tool_use_id":"t2"}]}`) +
		line("r0", "c0", "user", "2026-01-01T00:00:05Z", `{"content":[{"type":"tool_result","tool_use_id":"t0"}]}`) +
		queryLine("end", "r0", "assistant", "", `{"content":"end"}`)
	scope, _ := claudeQueryFixture(t, source)
	p, e := claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
	require.NoError(t, e)
	ids := []string{}
	for _, m := range p.Messages {
		ids = append(ids, m.UUID)
	}
	require.Equal(t, []string{"u", "c0", "c1", "c2", "r2", "r1", "r0", "end"}, ids)
}

func TestClaudeSessionMetadata(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	source := queryLine("u", "", "user", `"future":9007199254740993,`, `{"content":"first saved prompt"}`) +
		queryLine("a", "u", "assistant", "", `{"content":"answer"}`) +
		`{"type":"custom-title","customTitle":"Chosen title","future":null}` + "\n"
	scope, path := claudeQueryFixture(t, source)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	page, err := claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: scope})
	require.NoError(t, err)
	require.Equal(t, "complete", page.Coverage.Status)
	require.Len(t, page.Sessions, 1)
	info := page.Sessions[0]
	require.Equal(t, querySession, info.SessionID)
	require.Equal(t, "Chosen title", info.Summary)
	require.Equal(t, "first saved prompt", *info.FirstPrompt)
	require.Equal(t, "unknown", info.Running)
	require.False(t, info.Sampled)
	require.Contains(t, string(info.Head), "9007199254740993")
	single, err := claude.GetSessionInfo(testsupport.Deadline(t), info.SessionID, claude.SessionInfoOptions{Scope: scope})
	require.NoError(t, err)
	require.NotNil(t, single.Info)
	require.Equal(t, info, *single.Info)
	messages, err := claude.GetSessionMessages(testsupport.Deadline(t), info.SessionID, claude.SessionMessagesOptions{Scope: scope})
	require.NoError(t, err)
	require.Len(t, messages.Messages, 2)
	page.Sessions[0].Head[0] = 'x'
	again, err := claude.GetSessionInfo(testsupport.Deadline(t), info.SessionID, claude.SessionInfoOptions{Scope: scope})
	require.NoError(t, err)
	require.Equal(t, byte('{'), again.Info.Head[0])
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, sha256.Sum256(before), sha256.Sum256(after))
	stat, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o400), stat.Mode().Perm())
}

func TestClaudeMetadataScopeAndPages(t *testing.T) {
	scope, path := claudeQueryFixture(t, queryLine("u", "", "user", "", `{"content":"one"}`))
	const other = "22222222-2222-4222-8222-222222222222"
	p2 := filepath.Join(filepath.Dir(path), other+".jsonl")
	require.NoError(t, os.WriteFile(p2, []byte(strings.ReplaceAll(queryLine("u", "", "user", "", `{"content":"two"}`), querySession, other)), 0o400))
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	require.NoError(t, os.Chtimes(path, older, older))
	require.NoError(t, os.Chtimes(p2, newer, newer))
	page, e := claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: scope, Limit: 1})
	require.NoError(t, e)
	require.Len(t, page.Sessions, 1)
	require.Equal(t, other, page.Sessions[0].SessionID)
	page, e = claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: scope, Offset: 1, Limit: 1})
	require.NoError(t, e)
	require.Len(t, page.Sessions, 1)
	require.Equal(t, querySession, page.Sessions[0].SessionID)
	page, e = claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: scope, Offset: 99})
	require.NoError(t, e)
	require.Empty(t, page.Sessions)
	require.Equal(t, "complete", page.Coverage.Status)
	require.NoError(t, os.Chtimes(path, newer, newer))
	page, e = claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: scope})
	require.NoError(t, e)
	require.Len(t, page.Sessions, 2)
	require.Equal(t, querySession, page.Sessions[0].SessionID)
	// Project scope require.NoError be reused by list, info and messages.
	project := t.TempDir()
	canonical, e := filepath.EvalSymlinks(project)
	require.NoError(t, e)
	key := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(canonical, "-")
	dir := filepath.Join(scope.ConfigDir, "projects", key)
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, other+".jsonl"), []byte(""), 0o400))
	scoped := scope
	scoped.ProjectDir = project
	page, e = claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: scoped})
	require.NoError(t, e)
	require.Len(t, page.Sessions, 1)
	require.Equal(t, other, page.Sessions[0].SessionID)
	require.Empty(t, page.Sessions[0].Summary)
	_, e = claude.GetSessionInfo(testsupport.Deadline(t), querySession, claude.SessionInfoOptions{Scope: scoped})
	requireMetadataKind(t, e, "missing")
	// Discovery retains duplicate identity, never picks a newest winner.
	page, e = claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: scope})
	requireMetadataKind(t, e, "ambiguous")
	require.Equal(t, "partial", page.Coverage.Status)
	require.Len(t, page.Sessions, 3)
	_, e = claude.GetSessionInfo(testsupport.Deadline(t), other, claude.SessionInfoOptions{Scope: scope})
	requireMetadataKind(t, e, "ambiguous")
	// Known nested subagent layout is outside scope. A main file marked sidechain
	// is excluded, but metadata-only or unknown native objects remain discoverable.
	require.NoError(t, os.Mkdir(filepath.Join(dir, other), 0o700))
	side := strings.ReplaceAll(queryLine("side", "", "user", `"isSidechain":true,`, `{"content":"hidden"}`), querySession, "33333333-3333-4333-8333-333333333333")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "33333333-3333-4333-8333-333333333333.jsonl"), []byte(side), 0o400))
	page, e = claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: scoped})
	require.NoError(t, e)
	require.Len(t, page.Sessions, 1)
}

func requireMetadataKind(t *testing.T, err error, kind string) {
	t.Helper()
	var q *claude.SessionQueryError
	require.ErrorAs(t, err, &q)
	require.Equal(t, kind, q.Kind)
}

func TestClaudeMetadataSamples(t *testing.T) {
	cases := []struct{ name, source, summary, prompt string }{
		{"native-precedence", queryLine("u", "", "user", "", `{"content":"prompt"}`) + `{"type":"custom-title","aiTitle":"AI"}` + "\n" + `{"type":"last-prompt","lastPrompt":"recent"}` + "\n" + `{"type":"summary","summary":"older"}` + "\n", "AI", "prompt"},
		{"last-prompt-precedence", queryLine("u", "", "user", "", `{"content":"prompt"}`) + `{"type":"last-prompt","lastPrompt":"recent"}` + "\n" + `{"type":"summary","summary":"older"}` + "\n", "recent", "prompt"},
		{"skip-hook", queryLine("hook", "", "user", "", `{"content":"<session-start-hook>generated</session-start-hook>"}`) + queryLine("u", "hook", "user", "", `{"content":[{"type":"text","text":"actual prompt"}]}`), "actual prompt", "actual prompt"},
		{"command-fallback", queryLine("u", "", "user", "", `{"content":"<command-name>/help</command-name>"}`), "/help", "/help"},
		{"unknown", `{"type":"future-native","extension":{"n":9007199254740993}}` + "\n", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scope, _ := claudeQueryFixture(t, tc.source)
			result, e := claude.GetSessionInfo(testsupport.Deadline(t), querySession, claude.SessionInfoOptions{Scope: scope})
			require.NoError(t, e)
			require.NotNil(t, result.Info)
			require.Equal(t, tc.summary, result.Info.Summary)
			if tc.prompt != "" {
				require.NotNil(t, result.Info.FirstPrompt)
				require.Equal(t, tc.prompt, *result.Info.FirstPrompt)
			}
		})
	}
	t.Run("bounded-middle", func(t *testing.T) {
		head := queryLine("u", "", "user", "", `{"content":"sampled prompt"}`)
		// A corrupt unread middle proves metadata does not force message parsing.
		source := head + strings.Repeat("unread not JSON", 1000) + "\n" + `{"type":"custom-title","customTitle":"tail title","future":9007199254740993}` + "\n"
		scope, _ := claudeQueryFixture(t, source)
		result, e := claude.GetSessionInfo(testsupport.Deadline(t), querySession, claude.SessionInfoOptions{Scope: scope, SampleBytes: 256})
		require.NoError(t, e)
		require.NotNil(t, result.Info)
		require.True(t, result.Info.Sampled)
		require.Equal(t, "tail title", result.Info.Summary)
		require.Equal(t, int64(513), result.Coverage.Bytes) // two 256-byte windows plus the tail boundary probe
		require.Equal(t, "complete", result.Coverage.Status)
		require.Contains(t, string(result.Info.Tail), "9007199254740993")
		_, e = claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
		requireMetadataKind(t, e, "corrupt")
	})
}

func TestClaudeMetadataFailures(t *testing.T) {
	cases := []struct {
		name    string
		options func(claude.SessionQueryScope) claude.ListSessionsOptions
		kind    string
	}{
		{"negative-offset", func(q claude.SessionQueryScope) claude.ListSessionsOptions {
			return claude.ListSessionsOptions{Scope: q, Offset: -1}
		}, "invalid-input"},
		{"negative-limit", func(q claude.SessionQueryScope) claude.ListSessionsOptions {
			return claude.ListSessionsOptions{Scope: q, Limit: -1}
		}, "invalid-input"},
		{"negative-sample", func(q claude.SessionQueryScope) claude.ListSessionsOptions {
			return claude.ListSessionsOptions{Scope: q, SampleBytes: -1}
		}, "invalid-input"},
		{"entries", func(q claude.SessionQueryScope) claude.ListSessionsOptions {
			q.MaxEntries = 1
			return claude.ListSessionsOptions{Scope: q}
		}, "resource-limit"},
		{"bytes", func(q claude.SessionQueryScope) claude.ListSessionsOptions {
			return claude.ListSessionsOptions{Scope: q, MaxBytes: 1}
		}, "resource-limit"},
		{"lines", func(q claude.SessionQueryScope) claude.ListSessionsOptions {
			return claude.ListSessionsOptions{Scope: q, MaxLines: 1}
		}, "resource-limit"},
		{"page", func(q claude.SessionQueryScope) claude.ListSessionsOptions {
			return claude.ListSessionsOptions{Scope: q, Limit: 1001}
		}, "resource-limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, _ := claudeQueryFixture(t, queryLine("u", "", "user", "", `{"content":"prompt"}`)+queryLine("a", "u", "assistant", "", `{"content":"answer"}`))
			p, e := claude.ListSessions(testsupport.Deadline(t), tc.options(q))
			requireMetadataKind(t, e, tc.kind)
			require.NotEqual(t, "complete", p.Coverage.Status)
		})
	}
	for _, tc := range []struct{ name, source, kind string }{
		{"invalid-json", "{bad}\n", "corrupt"},
		{"incomplete", "{\"type\":\"future\"}", "corrupt"},
		{"duplicate-key", "{\"x\":1,\"x\":2}\n", "corrupt"},
		{"identity", "{\"sessionId\":\"other\"}\n", "corrupt"},
		{"title-type", "{\"customTitle\":5}\n", "corrupt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, _ := claudeQueryFixture(t, tc.source)
			p, e := claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: q})
			requireMetadataKind(t, e, tc.kind)
			require.Equal(t, "partial", p.Coverage.Status)
			require.Len(t, p.Sessions, 1)
			r, e := claude.GetSessionInfo(testsupport.Deadline(t), querySession, claude.SessionInfoOptions{Scope: q})
			requireMetadataKind(t, e, tc.kind)
			require.NotNil(t, r.Info)
		})
	}
	t.Run("access-cancel-isolation", func(t *testing.T) {
		q, path := claudeQueryFixture(t, `{"future":null}`+"\n")
		require.NoError(t, os.Chmod(path, 0o000))
		t.Cleanup(func() { _ = os.Chmod(path, 0o400) })
		p, e := claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: q})
		requireMetadataKind(t, e, "inaccessible")
		require.ErrorIs(t, e, os.ErrPermission)
		require.Equal(t, "partial", p.Coverage.Status)
		require.Len(t, p.Sessions, 1)
		require.NoError(t, os.Chmod(path, 0o400))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		p, e = claude.ListSessions(ctx, claude.ListSessionsOptions{Scope: q})
		require.ErrorIs(t, e, context.Canceled)
		require.Equal(t, "unknown", p.Coverage.Status)
		_, e = claude.GetSessionInfo(ctx, querySession, claude.SessionInfoOptions{Scope: q})
		require.ErrorIs(t, e, context.Canceled)
		_, e = claude.ListSessions(nil, claude.ListSessionsOptions{Scope: q}) //nolint:staticcheck // verifies nil-context rejection.
		requireMetadataKind(t, e, "invalid-input")
		_, e = claude.GetSessionInfo(testsupport.Deadline(t), "../escape", claude.SessionInfoOptions{Scope: q})
		requireMetadataKind(t, e, "invalid-input")
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "secret"), path))
		_, e = claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: q})
		requireMetadataKind(t, e, "unsupported")
		_, e = claude.GetSessionInfo(testsupport.Deadline(t), querySession, claude.SessionInfoOptions{Scope: q})
		requireMetadataKind(t, e, "unsupported")
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Remove(filepath.Dir(path)))
		require.NoError(t, os.Symlink(t.TempDir(), filepath.Dir(path)))
		_, e = claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: q})
		requireMetadataKind(t, e, "unsupported")
	})
	t.Run("empty-missing-partial", func(t *testing.T) {
		q, path := claudeQueryFixture(t, "")
		r, e := claude.GetSessionInfo(testsupport.Deadline(t), querySession, claude.SessionInfoOptions{Scope: q})
		require.NoError(t, e)
		require.NotNil(t, r.Info)
		require.Equal(t, "complete", r.Coverage.Status)
		require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(path), "legacy.jsonl"), []byte("private require.NoError not read"), 0o000))
		p, e := claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: q})
		requireMetadataKind(t, e, "unsupported")
		require.Len(t, p.Sessions, 1)
		require.Equal(t, "partial", p.Coverage.Status)
		require.NoError(t, os.Remove(filepath.Join(filepath.Dir(path), "legacy.jsonl")))
		require.NoError(t, os.Remove(path))
		p, e = claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: q})
		require.NoError(t, e)
		require.Empty(t, p.Sessions)
		require.Equal(t, "complete", p.Coverage.Status)
		_, e = claude.GetSessionInfo(testsupport.Deadline(t), querySession, claude.SessionInfoOptions{Scope: q})
		requireMetadataKind(t, e, "missing")
		_, e = claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: claude.SessionQueryScope{ConfigDir: t.TempDir()}})
		requireMetadataKind(t, e, "missing")
	})
}

func TestClaudeMetadataDefaultPage(t *testing.T) {
	q, path := claudeQueryFixture(t, "")
	for i := 2; i <= 102; i++ {
		id := fmt.Sprintf("%08x-1111-4111-8111-111111111111", i)
		require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(path), id+".jsonl"), nil, 0o400))
	}
	p, e := claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: q})
	require.NoError(t, e)
	require.Len(t, p.Sessions, 100)
	require.Equal(t, "complete", p.Coverage.Status)
	p, e = claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: q, Offset: 100})
	require.NoError(t, e)
	require.Len(t, p.Sessions, 2)
	p, e = claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: q, MaxSessions: 1})
	requireMetadataKind(t, e, "resource-limit")
	require.Len(t, p.Sessions, 1)
	require.Equal(t, "partial", p.Coverage.Status)
}

func TestClaudeMetadataSampleEdges(t *testing.T) {
	t.Run("head-is-not-tail", func(t *testing.T) {
		source := strings.Repeat("\n", 256) + strings.Repeat("middle", 1000) + "\n" + queryLine("u", "", "user", "", `{"content":"tail only"}`)
		q, _ := claudeQueryFixture(t, source)
		r, e := claude.GetSessionInfo(testsupport.Deadline(t), querySession, claude.SessionInfoOptions{Scope: q, SampleBytes: 256})
		require.NoError(t, e)
		require.True(t, r.Info.Sampled)
		require.Nil(t, r.Info.FirstPrompt)
		require.Nil(t, r.Info.CreatedAt)
	})
	t.Run("exact-entries", func(t *testing.T) {
		q, _ := claudeQueryFixture(t, "")
		q.MaxEntries = 2
		p, e := claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: q})
		require.NoError(t, e)
		require.Len(t, p.Sessions, 1)
	})
	t.Run("invalid-visibility-flag", func(t *testing.T) {
		q, _ := claudeQueryFixture(t, queryLine("u", "", "user", `"isSidechain":"true",`, `{"content":"uncertain"}`))
		_, e := claude.GetSessionInfo(testsupport.Deadline(t), querySession, claude.SessionInfoOptions{Scope: q})
		requireMetadataKind(t, e, "corrupt")
	})
}

func TestClaudeMetadataIncompleteTail(t *testing.T) {
	q, _ := claudeQueryFixture(t, strings.Repeat("x", 1000))
	_, e := claude.GetSessionInfo(testsupport.Deadline(t), querySession, claude.SessionInfoOptions{Scope: q, SampleBytes: 16})
	requireMetadataKind(t, e, "corrupt")
}

func TestClaudeMetadataNestedSampleProvenance(t *testing.T) {
	for _, extra := range []string{"", `"isSidechain":true,`} {
		t.Run("nested-"+extra, func(t *testing.T) {
			tail := queryLine("tail", "", "user", extra, `{"content":"tail evidence"}`)
			source := `{"type":"future-native","nested":` + strings.TrimSpace(tail) + `,"padding":"` + strings.Repeat("x", 2000) + `"}` + "\n" + tail
			q, _ := claudeQueryFixture(t, source)
			r, e := claude.GetSessionInfo(testsupport.Deadline(t), querySession, claude.SessionInfoOptions{Scope: q, SampleBytes: 300})
			require.NoError(t, e)
			require.NotNil(t, r.Info)
			require.True(t, r.Info.Sampled)
			require.Nil(t, r.Info.FirstPrompt)
			require.Nil(t, r.Info.CreatedAt)
		})
	}
}

// Browse metadata first, then page the selected content after compaction and
// parallel recovery. Expected order follows the pinned native algorithm, rather
// than JSONL line offsets or the preview's pre-compaction prompt.
func claudeIndependentHistory(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	source := queryLine("old", "", "user", "", `{"content":"pre-compaction preview"}`) +
		`{"type":"system","uuid":"boundary","parentUuid":null,"sessionId":"` + querySession + `","subtype":"compact_boundary","logicalParentUuid":"old"}` + "\n" +
		queryLine("summary", "boundary", "user", `"isCompactSummary":true,`, `{"content":"retained summary"}`) +
		queryLine("a1", "summary", "assistant", "", `{"id":"response","content":[{"type":"tool_use","id":"t1"}]}`) +
		queryLine("a2", "a1", "assistant", "", `{"id":"response","content":[{"type":"tool_use","id":"t2"}]}`) +
		queryLine("r1", "a1", "user", "", `{"content":[{"type":"tool_result","tool_use_id":"t1"}]}`) +
		queryLine("r2", "a2", "user", "", `{"content":[{"type":"tool_result","tool_use_id":"t2"}]}`) +
		queryLine("end", "r1", "assistant", `"future":null,`, `{"content":[{"type":"future","n":9007199254740993}]}`) +
		queryLine("side", "summary", "assistant", `"isSidechain":true,`, `{"content":"hidden"}`)
	scope, path := claudeQueryFixture(t, source)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	ctx := testsupport.Deadline(t)
	listed, err := claude.ListSessions(ctx, claude.ListSessionsOptions{Scope: scope, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, "complete", listed.Coverage.Status)
	require.Len(t, listed.Sessions, 1)
	id := listed.Sessions[0].SessionID
	info, err := claude.GetSessionInfo(ctx, id, claude.SessionInfoOptions{Scope: scope})
	require.NoError(t, err)
	require.NotNil(t, info.Info)
	assert.Equal(t, "pre-compaction preview", info.Info.Summary)
	assert.Equal(t, "unknown", info.Info.Running)
	var ids []string
	for offset := 0; offset <= 6; offset += 2 {
		page, err := claude.GetSessionMessages(ctx, id, claude.SessionMessagesOptions{Scope: scope, Offset: offset, Limit: 2})
		require.NoError(t, err)
		require.Equal(t, "complete", page.Coverage.Status)
		assert.False(t, page.Coverage.Atomic)
		if offset == 6 {
			require.Empty(t, page.Messages)
		} else {
			require.Len(t, page.Messages, 2)
		}
		for _, message := range page.Messages {
			ids = append(ids, message.UUID)
			assert.Equal(t, id, message.SessionID)
			if message.UUID == "end" {
				assert.JSONEq(t, `{"content":[{"type":"future","n":9007199254740993}]}`, string(message.Message))
				assert.Contains(t, string(message.Raw), `"future":null`)
			}
		}
	}
	assert.Equal(t, []string{"summary", "a1", "a2", "r2", "r1", "end"}, ids)
	exhausted, err := claude.ListSessions(ctx, claude.ListSessionsOptions{Scope: scope, Offset: 1, Limit: 1})
	require.NoError(t, err)
	assert.Empty(t, exhausted.Sessions)
	_, err = claude.GetSessionInfo(ctx, "22222222-2222-4222-8222-222222222222", claude.SessionInfoOptions{Scope: scope})
	requireMetadataKind(t, err, "missing")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, sha256.Sum256(before), sha256.Sum256(after))
}

func TestClaudeMetadataTailLineBoundary(t *testing.T) {
	head := `{"type":"user","message":{"content":"hello"}}` + "\n"
	tail := `{"type":"custom-title","customTitle":"tail title"}` + "\n"
	source := head + `{"type":"summary","summary":"` + strings.Repeat("x", 1000) + `"}` + "\n" + tail
	scope, _ := claudeQueryFixture(t, source)
	sample := int64(len(tail))
	info, err := claude.GetSessionInfo(testsupport.Deadline(t), querySession, claude.SessionInfoOptions{Scope: scope, SampleBytes: sample})
	require.NoError(t, err)
	require.NotNil(t, info.Info)
	assert.Equal(t, "tail title", info.Info.Summary)
	require.Len(t, info.Info.Records, 2)
	assert.Equal(t, tail, string(info.Info.Tail))
	assert.Equal(t, "hello", *info.Info.FirstPrompt)
	assert.True(t, info.Info.Sampled)
	// Both windows plus one preceding byte establish that the tail starts a line.
	assert.Equal(t, 2*sample+1, info.Coverage.Bytes)
	listed, err := claude.ListSessions(testsupport.Deadline(t), claude.ListSessionsOptions{Scope: scope, SampleBytes: sample, MaxBytes: 2*sample + 1})
	require.NoError(t, err)
	require.Len(t, listed.Sessions, 1)
	assert.Equal(t, "tail title", listed.Sessions[0].Summary)
	limited, err := claude.GetSessionInfo(testsupport.Deadline(t), querySession, claude.SessionInfoOptions{Scope: scope, SampleBytes: sample, MaxBytes: 2 * sample})
	requireMetadataKind(t, err, "resource-limit")
	assert.Equal(t, "partial", limited.Coverage.Status)
	assert.Zero(t, limited.Coverage.Bytes, "reject before exceeding the byte budget")
}

func TestClaudeNonMessageRequestMetadata(t *testing.T) {
	for _, kind := range []string{"api-request-shape", "api-request-blob", "api-request", "cost-state"} {
		t.Run(kind, func(t *testing.T) {
			// Artificial non-message record: no uuid/parentUuid and no real request data.
			metadata := fmt.Sprintf(`{"type":%q,"synthetic":{"future":9007199254740993,"nullable":null}}`, kind)
			source := queryLine("u", "", "user", "", `{"content":"question"}`) + metadata + "\n" + queryLine("a", "u", "assistant", "", `{"content":"answer"}`)
			scope, _ := claudeQueryFixture(t, source)
			page, err := claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope, MaxBytes: int64(len(source)), MaxLines: 3, MaxRecords: 2})
			require.NoError(t, err)
			require.Len(t, page.Messages, 2)
			assert.Equal(t, "u", page.Messages[0].UUID)
			assert.Equal(t, "a", page.Messages[1].UUID)
			assert.Equal(t, "complete", page.Coverage.Status)
			assert.Equal(t, 3, page.Coverage.Lines)
			assert.Equal(t, 2, page.Coverage.Records, "non-message metadata is not a chain entry")
			next, err := claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope, Offset: 1, Limit: 1})
			require.NoError(t, err)
			require.Len(t, next.Messages, 1)
			assert.Equal(t, "a", next.Messages[0].UUID)
			end, err := claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope, Offset: 2, Limit: 1})
			require.NoError(t, err)
			assert.Empty(t, end.Messages)
			info, err := claude.GetSessionInfo(testsupport.Deadline(t), querySession, claude.SessionInfoOptions{Scope: scope})
			require.NoError(t, err)
			require.NotNil(t, info.Info)
			require.Len(t, info.Info.Records, 3)
			assert.JSONEq(t, metadata, string(info.Info.Records[1]))
		})
	}
}

func TestClaudeRequestMetadataGuards(t *testing.T) {
	for _, kind := range []string{"api-request-shape", "api-request-blob", "api-request", "cost-state"} {
		t.Run(kind, func(t *testing.T) {
			for _, tc := range []struct {
				name, extra, want string
				configure         func(*claude.SessionMessagesOptions)
			}{
				{name: "foreign-camel-identity", extra: `,"sessionId":"22222222-2222-4222-8222-222222222222"`, want: "corrupt"},
				{name: "foreign-snake-identity", extra: `,"session_id":"22222222-2222-4222-8222-222222222222"`, want: "corrupt"},
				{name: "typed-identity", extra: `,"sessionId":12`, want: "corrupt"},
				{name: "duplicate-identity", extra: `,"sessionId":null,"sessionId":null`, want: "corrupt"},
				{name: "invalid-unicode", extra: `,"synthetic":"\ud800"`, want: "corrupt"},
				{name: "lines", want: "resource-limit", configure: func(o *claude.SessionMessagesOptions) { o.MaxLines = 2 }},
				{name: "bytes", want: "resource-limit"},
				{name: "index", want: "resource-limit", configure: func(o *claude.SessionMessagesOptions) { o.MaxRecords = 1 }},
				{name: "large-metadata-line", extra: `,"synthetic":"` + strings.Repeat("x", 2048) + `"`, want: "resource-limit", configure: func(o *claude.SessionMessagesOptions) { o.MaxLineBytes = 1024 }},
			} {
				t.Run(tc.name, func(t *testing.T) {
					metadata := fmt.Sprintf(`{"type":%q%s}`+"\n", kind, tc.extra)
					source := queryLine("u", "", "user", "", `{"content":"question"}`) + metadata + queryLine("a", "u", "assistant", "", `{"content":"answer"}`)
					scope, _ := claudeQueryFixture(t, source)
					options := claude.SessionMessagesOptions{Scope: scope}
					if tc.configure != nil {
						tc.configure(&options)
					}
					if tc.name == "bytes" {
						options.MaxBytes = int64(len(source) - 1)
					}
					page, err := claude.GetSessionMessages(testsupport.Deadline(t), querySession, options)
					requireQueryKind(t, page, err, tc.want)
				})
			}
		})
	}
	t.Run("unknown-remains-unsupported", func(t *testing.T) {
		scope, _ := claudeQueryFixture(t, `{"type":"api-request-future"}`+"\n")
		page, err := claude.GetSessionMessages(testsupport.Deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
		requireQueryKind(t, page, err, "unsupported")
	})
}
