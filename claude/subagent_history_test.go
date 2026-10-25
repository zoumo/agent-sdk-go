package claude_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude"
)

const childParent = "550e8400-e29b-41d4-a716-446655440000"

func childStore(t *testing.T) (claude.SessionQueryScope, string) {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "projects", "synthetic")
	require.NoError(t, os.MkdirAll(project, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(project, childParent+".jsonl"), nil, 0600))
	return claude.SessionQueryScope{ConfigDir: root}, filepath.Join(project, childParent, "subagents")
}
func putChild(t *testing.T, root, name, data string) string {
	t.Helper()
	p := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0700))
	require.NoError(t, os.WriteFile(p, []byte(data), 0600))
	return p
}
func TestIndependentSubagentHistory(t *testing.T) {
	t.Run("H1-no-session", func(t *testing.T) {
		scope, root := childStore(t)
		t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "poison"))
		t.Setenv("PATH", t.TempDir())
		putChild(t, root, "workflows/run/agent-Agent-α.jsonl", `{"type":"user","uuid":"u","parentUuid":null,"message":{"content":"question"}}`+"\n"+`{"type":"assistant","uuid":"a","parentUuid":"u","isSidechain":true,"isMeta":true,"teamName":"native","message":{"content":[{"type":"future","n":9007199254740993}]}}`+"\n")
		putChild(t, root, "workflows/run/agent-Agent-α.meta.json", `{"toolUseId":"tool-1","parentAgentId":null,"future":9007199254740993}`)
		page, err := claude.ListSubagents(context.Background(), childParent, claude.ListSubagentsOptions{Scope: scope})
		require.NoError(t, err)
		require.Len(t, page.Records, 1)
		require.Equal(t, "Agent-α", page.Records[0].AgentID)
		require.Nil(t, page.Records[0].ParentAgentID)
		require.Equal(t, "unknown", page.Records[0].Running)
		messages, err := claude.GetSubagentMessages(context.Background(), childParent, page.Records[0].AgentID, claude.SubagentMessagesOptions{Scope: scope, Offset: 1, Limit: 1})
		require.NoError(t, err)
		require.Len(t, messages.Messages, 1)
		require.Equal(t, "a", messages.Messages[0].UUID)
		require.Contains(t, string(messages.Messages[0].Message), "9007199254740993")
		require.Equal(t, "tool-1", *messages.Messages[0].ParentToolUseID)
		require.Equal(t, "complete", messages.Coverage.Status)
		require.False(t, messages.Coverage.Atomic)
	})
}

func childLine(id, parent, kind string) string {
	p := "null"
	if parent != "" {
		p = `"` + parent + `"`
	}
	return `{"type":"` + kind + `","uuid":"` + id + `","parentUuid":` + p + `,"message":{"content":"synthetic"}}` + "\n"
}
func childError(t *testing.T, page claude.SubagentMessagesPage, err error, kind string) {
	t.Helper()
	var qe *claude.SessionQueryError
	require.ErrorAs(t, err, &qe)
	require.Equal(t, kind, qe.Kind)
	require.Empty(t, page.Messages)
	require.Equal(t, claude.SubagentInfo{}, page.Info)
}
func TestChildSelectionAndOwnership(t *testing.T) {
	scope, root := childStore(t)
	source := childLine("u", "", "user") + childLine("stray", "u", "user") + childLine("boundary", "", "system") + childLine("leaf", "boundary", "assistant")
	source = strings.Replace(source, `"uuid":"boundary"`, `"uuid":"boundary","subtype":"compact_boundary","logicalParentUuid":"u"`, 1)
	path := putChild(t, root, "agent-x.jsonl", source)
	side := `{"parentAgentId":"external-parent","toolUseId":"t","future":null}`
	putChild(t, root, "agent-x.meta.json", side)
	page, err := claude.GetSubagentMessages(context.Background(), childParent, "x", claude.SubagentMessagesOptions{Scope: scope})
	require.NoError(t, err)
	require.Len(t, page.Messages, 1)
	require.Equal(t, "leaf", page.Messages[0].UUID)
	require.Equal(t, path, page.Info.Path)
	require.Equal(t, side, string(page.Info.Sidecar))
	*page.Info.ParentAgentID = "mutated"
	*page.Messages[0].ParentToolUseID = "changed"
	page.Info.Sidecar[0] = 'x'
	page.Messages[0].Raw[0] = 'x'
	page.Messages[0].Message[0] = 'x'
	page.Coverage.Directories[0] = "changed"
	fresh, err := claude.GetSubagentMessages(context.Background(), childParent, "x", claude.SubagentMessagesOptions{Scope: scope})
	require.NoError(t, err)
	require.Equal(t, "external-parent", *fresh.Info.ParentAgentID)
	require.Equal(t, "t", *fresh.Messages[0].ParentToolUseID)
	require.Equal(t, side, string(fresh.Info.Sidecar))
	require.Equal(t, byte('{'), fresh.Messages[0].Raw[0])
}
func TestChildHistoryFaults(t *testing.T) {
	valid := childLine("u", "", "user")
	for _, tc := range []struct{ name, source, side, kind string }{
		{"duplicate", valid + valid, "", "corrupt"},
		{"unselected-cycle", childLine("bad", "bad", "system") + valid, "", "corrupt"},
		{"unselected-missing-parent", childLine("bad", "missing", "progress") + valid, "", "corrupt"},
		{"partial", strings.TrimSuffix(valid, "\n"), "", "corrupt"},
		{"duplicate-key", `{"type":"user","type":"system"}` + "\n", "", "corrupt"},
		{"invalid-utf", string([]byte{'{', '"', 'x', '"', ':', '"', 255, '"', '}', '\n'}), "", "corrupt"},
		{"non-object", "[]\n", "", "corrupt"},
		{"unknown", "{\"type\":\"future\"}\n", "", "unsupported"},
		{"identity", strings.Replace(valid, `"uuid"`, `"session_id":"other","uuid"`, 1), "", "corrupt"},
		{"null-identity", strings.Replace(valid, `"uuid"`, `"agentId":null,"uuid"`, 1), "", "corrupt"},
		{"agent-conflict", strings.Replace(valid, `"uuid"`, `"agent_id":"other","uuid"`, 1), "", "corrupt"},
		{"null-uuid", strings.Replace(valid, `"u"`, `null`, 1), "", "corrupt"},
		{"side-array", valid, `[]`, "corrupt"},
		{"side-empty", valid, " ", "corrupt"},
		{"side-self", valid, `{"parentAgentId":"x"}`, "corrupt"},
		{"side-parent-type", valid, `{"parentAgentId":7}`, "corrupt"},
		{"side-tool-empty", valid, `{"toolUseId":""}`, "corrupt"},
		{"side-identity", valid, `{"sessionId":"other"}`, "corrupt"},
		{"side-null-identity", valid, `{"agent_id":null}`, "corrupt"},
		{"side-invalid-parent", valid, `{"parentAgentId":"a/b"}`, "corrupt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope, root := childStore(t)
			putChild(t, root, "agent-x.jsonl", tc.source)
			if tc.side != "" {
				putChild(t, root, "agent-x.meta.json", tc.side)
			}
			page, err := claude.GetSubagentMessages(context.Background(), childParent, "x", claude.SubagentMessagesOptions{Scope: scope})
			childError(t, page, err, tc.kind)
			require.Equal(t, "partial", page.Coverage.Status)
		})
	}
}
func TestChildHistoryBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*claude.SubagentMessagesOptions)
	}{
		{"bytes", func(o *claude.SubagentMessagesOptions) { o.Limits.MaxBytes = 1 }},
		{"lines", func(o *claude.SubagentMessagesOptions) { o.Limits.MaxLines = 1 }},
		{"records", func(o *claude.SubagentMessagesOptions) { o.Limits.MaxRecords = 1 }},
		{"line-bytes", func(o *claude.SubagentMessagesOptions) { o.Limits.MaxLineBytes = 1 }},
		{"files", func(o *claude.SubagentMessagesOptions) { o.Limits.MaxFiles = 1 }},
		{"directories", func(o *claude.SubagentMessagesOptions) { o.Limits.MaxDirectories = 1 }},
		{"entries", func(o *claude.SubagentMessagesOptions) { o.Scope.MaxEntries = 1 }},
		{"sidecar-bytes", func(o *claude.SubagentMessagesOptions) { o.Limits.MaxSidecarBytes = 1 }},
		{"depth", func(o *claude.SubagentMessagesOptions) { o.Limits.MaxDepth = 1 }},
		{"result-request", func(o *claude.SubagentMessagesOptions) { o.Limit = 2; o.MaxMessages = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope, root := childStore(t)
			putChild(t, root, "workflows/run/agent-x.jsonl", childLine("u", "", "user")+childLine("a", "u", "assistant"))
			putChild(t, root, "workflows/run/agent-x.meta.json", `{}`)
			o := claude.SubagentMessagesOptions{Scope: scope}
			tc.change(&o)
			page, err := claude.GetSubagentMessages(context.Background(), childParent, "x", o)
			childError(t, page, err, "resource-limit")
		})
	}
	for _, id := range []string{"", ".", "..", "a/b", `a\b`, "a:b", "a\x00b", "a\nb", strings.Repeat("x", 256), string([]byte{255})} {
		t.Run("id-"+fmt.Sprintf("%x", id), func(t *testing.T) {
			scope, _ := childStore(t)
			p, e := claude.GetSubagentMessages(context.Background(), childParent, id, claude.SubagentMessagesOptions{Scope: scope})
			childError(t, p, e, "invalid-input")
			require.ErrorIs(t, e, claude.ErrInvalidInput)
		})
	}
}
func TestChildLayoutAndEmpty(t *testing.T) {
	t.Run("absent-container", func(t *testing.T) {
		scope, _ := childStore(t)
		p, e := claude.ListSubagents(context.Background(), childParent, claude.ListSubagentsOptions{Scope: scope})
		require.NoError(t, e)
		require.Empty(t, p.Records)
		require.Equal(t, "complete", p.Coverage.Status)
		m, e := claude.GetSubagentMessages(context.Background(), childParent, "x", claude.SubagentMessagesOptions{Scope: scope})
		childError(t, m, e, "missing")
		require.ErrorIs(t, e, os.ErrNotExist)
	})
	for _, source := range []string{"", childLine("s", "", "system"), "\n"} {
		t.Run("no-leaf-"+fmt.Sprint(len(source)), func(t *testing.T) {
			scope, root := childStore(t)
			putChild(t, root, "agent-x.jsonl", source)
			m, e := claude.GetSubagentMessages(context.Background(), childParent, "x", claude.SubagentMessagesOptions{Scope: scope})
			require.NoError(t, e)
			require.Empty(t, m.Messages)
			require.Nil(t, m.Info.ParentAgentID)
			require.Equal(t, "absent", m.Coverage.SidecarStatus)
		})
	}
	for _, tc := range []struct {
		name, kind string
		setup      func(*testing.T, string)
	}{
		{"duplicate-id", "ambiguous", func(t *testing.T, r string) { putChild(t, r, "nested/agent-x.jsonl", "") }},
		{"hardlink", "ambiguous", func(t *testing.T, r string) {
			require.NoError(t, os.Link(filepath.Join(r, "agent-x.jsonl"), filepath.Join(r, "agent-y.jsonl")))
		}},
		{"symlink", "unsupported", func(t *testing.T, r string) { require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(r, "nested"))) }},
		{"orphan", "corrupt", func(t *testing.T, r string) { putChild(t, r, "agent-y.meta.json", `{}`) }},
		{"unsupported-file", "unsupported", func(t *testing.T, r string) { putChild(t, r, "unknown", "") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope, root := childStore(t)
			putChild(t, root, "agent-x.jsonl", "")
			tc.setup(t, root)
			m, e := claude.GetSubagentMessages(context.Background(), childParent, "x", claude.SubagentMessagesOptions{Scope: scope})
			childError(t, m, e, tc.kind)
			p, e := claude.ListSubagents(context.Background(), childParent, claude.ListSubagentsOptions{Scope: scope})
			var qe *claude.SessionQueryError
			require.ErrorAs(t, e, &qe)
			require.Equal(t, tc.kind, qe.Kind)
			require.Empty(t, p.Records)
		})
	}
	t.Run("list-slice-and-syntax", func(t *testing.T) {
		scope, root := childStore(t)
		putChild(t, root, "agent-z.jsonl", "")
		putChild(t, root, "nested/agent-a.jsonl", "")
		p, e := claude.ListSubagents(context.Background(), childParent, claude.ListSubagentsOptions{Scope: scope, Limit: 1, Offset: 1})
		require.NoError(t, e)
		require.Len(t, p.Records, 1)
		require.Equal(t, "a", p.Records[0].AgentID)
		putChild(t, root, "agent-z.jsonl", "broken\n")
		p, e = claude.ListSubagents(context.Background(), childParent, claude.ListSubagentsOptions{Scope: scope, Limit: 1, Offset: 1})
		require.Error(t, e)
		require.Empty(t, p.Records)
	})
}

func TestChildListInventoryBoundBeforeReading(t *testing.T) {
	scope, root := childStore(t)
	putChild(t, root, "agent-a.jsonl", "")
	putChild(t, root, "agent-b.jsonl", "")
	putChild(t, root, "later/agent-c.jsonl", "")
	p, e := claude.ListSubagents(context.Background(), childParent, claude.ListSubagentsOptions{Scope: scope, Limit: 1, MaxAgents: 1})
	var qe *claude.SessionQueryError
	require.ErrorAs(t, e, &qe)
	require.Equal(t, "resource-limit", qe.Kind)
	require.Empty(t, p.Records)
	// Inventory stops before allocating the second candidate: it has not opened
	// that candidate's later nested directory when the bound is exceeded.
	require.NotContains(t, p.Coverage.Directories, filepath.Join(root, "later"))
}

func TestChildQueryScopeAndCancellation(t *testing.T) {
	scope, root := childStore(t)
	putChild(t, root, "agent-x.jsonl", "")
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		parent string
		scope  claude.SessionQueryScope
		kind   string
	}{
		{"nil-context", nil, childParent, scope, "invalid-input"},
		{"invalid-parent", context.Background(), "not-uuid", scope, "invalid-input"},
		{"relative-config", context.Background(), childParent, claude.SessionQueryScope{ConfigDir: "relative"}, "invalid-input"},
		{"relative-project", context.Background(), childParent, claude.SessionQueryScope{ConfigDir: scope.ConfigDir, ProjectDir: "relative"}, "invalid-input"},
		{"missing-main", context.Background(), "11111111-1111-4111-8111-111111111111", scope, "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, e := claude.GetSubagentMessages(tc.ctx, tc.parent, "x", claude.SubagentMessagesOptions{Scope: tc.scope})
			childError(t, p, e, tc.kind)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p, e := claude.GetSubagentMessages(ctx, childParent, "x", claude.SubagentMessagesOptions{Scope: scope})
	require.ErrorIs(t, e, context.Canceled)
	require.Empty(t, p.Messages)
	for _, field := range []string{"MaxBytes", "MaxLines", "MaxRecords", "MaxLineBytes", "MaxFiles", "MaxDirectories", "MaxDepth", "MaxSidecarBytes"} {
		t.Run("negative-"+field, func(t *testing.T) {
			o := claude.SubagentMessagesOptions{Scope: scope}
			reflect.ValueOf(&o.Limits).Elem().FieldByName(field).SetInt(-1)
			p, e := claude.GetSubagentMessages(context.Background(), childParent, "x", o)
			childError(t, p, e, "invalid-input")
		})
	}
	t.Run("ambiguous-parent", func(t *testing.T) {
		putChild(t, filepath.Join(scope.ConfigDir, "projects", "other"), childParent+".jsonl", "")
		p, e := claude.GetSubagentMessages(context.Background(), childParent, "x", claude.SubagentMessagesOptions{Scope: scope})
		childError(t, p, e, "ambiguous")
	})
}
func TestChildGetReadsOnlySelectedContent(t *testing.T) {
	scope, root := childStore(t)
	putChild(t, root, "agent-a.jsonl", childLine("u", "", "user"))
	putChild(t, root, "agent-b.jsonl", "broken\n")
	p, e := claude.GetSubagentMessages(context.Background(), childParent, "a", claude.SubagentMessagesOptions{Scope: scope, Offset: 100})
	require.NoError(t, e)
	require.Empty(t, p.Messages)
	require.Equal(t, 2, p.Coverage.Files)
	listed, e := claude.ListSubagents(context.Background(), childParent, claude.ListSubagentsOptions{Scope: scope})
	require.Error(t, e)
	require.Empty(t, listed.Records)
}
func TestChildQueryInaccessibleSidecar(t *testing.T) {
	scope, root := childStore(t)
	putChild(t, root, "agent-x.jsonl", "")
	side, e := os.OpenFile(filepath.Join(root, "agent-x.meta.json"), os.O_CREATE|os.O_WRONLY, 0000)
	require.NoError(t, e)
	require.NoError(t, side.Close())
	p, e := claude.GetSubagentMessages(context.Background(), childParent, "x", claude.SubagentMessagesOptions{Scope: scope})
	childError(t, p, e, "inaccessible")
	require.ErrorIs(t, e, os.ErrPermission)
}

func TestChildPagingBoundIsReturnedRange(t *testing.T) {
	scope, root := childStore(t)
	putChild(t, root, "agent-x.jsonl", childLine("u", "", "user")+childLine("a", "u", "assistant"))
	p, e := claude.GetSubagentMessages(context.Background(), childParent, "x", claude.SubagentMessagesOptions{Scope: scope, Offset: 1, Limit: 1, MaxMessages: 1})
	require.NoError(t, e)
	require.Len(t, p.Messages, 1)
	require.Equal(t, "a", p.Messages[0].UUID)
	_, e = claude.GetSubagentMessages(context.Background(), childParent, "missing", claude.SubagentMessagesOptions{Scope: scope})
	var qe *claude.SessionQueryError
	require.ErrorAs(t, e, &qe)
	require.Equal(t, root, qe.Path)
}

func TestChildSidecarRequiresTranscriptFile(t *testing.T) {
	scope, root := childStore(t)
	putChild(t, root, "agent-x.meta.json", `{}`)
	require.NoError(t, os.Mkdir(filepath.Join(root, "agent-x.jsonl"), 0700))
	p, e := claude.ListSubagents(context.Background(), childParent, claude.ListSubagentsOptions{Scope: scope})
	var qe *claude.SessionQueryError
	require.ErrorAs(t, e, &qe)
	require.Equal(t, "corrupt", qe.Kind)
	require.Empty(t, p.Records)
}

func TestChildDoesNotRecoverParallelSiblings(t *testing.T) {
	scope, root := childStore(t)
	source := `{"type":"user","uuid":"u","message":{"content":"root"}}` + "\n" +
		`{"type":"assistant","uuid":"a1","parentUuid":"u","timestamp":"2099-01-01","message":{"id":"same-response","content":[{"type":"tool_use","id":"t1"}]}}` + "\n" +
		`{"type":"assistant","uuid":"a2","parentUuid":"a1","message":{"id":"same-response","content":[{"type":"tool_use","id":"t2"}]}}` + "\n" +
		`{"type":"user","uuid":"stray","parentUuid":"u","sourceToolAssistantUUID":"a1","message":{"content":[{"type":"tool_result","tool_use_id":"t1"}]}}` + "\n" +
		`{"type":"user","uuid":"r2","parentUuid":"a2","timestamp":"2000-01-01","message":{"content":[{"type":"tool_result","tool_use_id":"t2"}]}}` + "\n"
	putChild(t, root, "agent-x.jsonl", source)
	p, e := claude.GetSubagentMessages(context.Background(), childParent, "x", claude.SubagentMessagesOptions{Scope: scope})
	require.NoError(t, e)
	ids := []string{}
	for _, m := range p.Messages {
		ids = append(ids, m.UUID)
	}
	require.Equal(t, []string{"u", "a1", "a2", "r2"}, ids)
}
func TestChildDefaultSlices(t *testing.T) {
	scope, root := childStore(t)
	var data strings.Builder
	parent := ""
	for i := 0; i < 1001; i++ {
		id := fmt.Sprint(i)
		data.WriteString(childLine(id, parent, "user"))
		parent = id
	}
	putChild(t, root, "agent-x.jsonl", data.String())
	p, e := claude.GetSubagentMessages(context.Background(), childParent, "x", claude.SubagentMessagesOptions{Scope: scope})
	require.NoError(t, e)
	require.Len(t, p.Messages, 1000)
	require.Equal(t, "999", p.Messages[999].UUID)
	require.Equal(t, int64(64<<20), p.Coverage.Limits.MaxBytes)
	require.Equal(t, 10000, p.Coverage.MaxResults)
	for i := 0; i < 100; i++ {
		putChild(t, root, fmt.Sprintf("agent-%03d.jsonl", i), "")
	}
	listed, e := claude.ListSubagents(context.Background(), childParent, claude.ListSubagentsOptions{Scope: scope})
	require.NoError(t, e)
	require.Len(t, listed.Records, 100)
	require.Equal(t, 100, listed.Coverage.Limit)
}
