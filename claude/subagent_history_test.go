package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const savedChildParent = "11111111-1111-4111-8111-111111111111"

func childHistoryFixture(t *testing.T) (SessionQueryScope, string) {
	t.Helper()
	config := t.TempDir()
	project := t.TempDir()
	project, err := filepath.EvalSymlinks(project)
	require.NoError(t, err)
	base := filepath.Join(config, "projects", projectUnsafe.ReplaceAllString(project, "-"))
	require.NoError(t, os.MkdirAll(filepath.Join(base, savedChildParent, "subagents"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(base, savedChildParent+".jsonl"), []byte(`{"type":"user","uuid":"main","sessionId":"`+savedChildParent+`","message":{"content":"main"}}`+"\n"), 0600))
	return SessionQueryScope{ConfigDir: config, ProjectDir: project}, filepath.Join(base, savedChildParent, "subagents")
}
func writeChild(t *testing.T, root, id, body string) string {
	t.Helper()
	path := filepath.Join(root, "agent-"+id+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte(body), 0600))
	return path
}
func childConversation() string {
	return `{"type":"user","uuid":"u","parentUuid":null,"sessionId":"` + savedChildParent + `","agentId":"Alpha","isMeta":true,"message":{"content":"original"}}` + "\n" +
		`{"type":"assistant","uuid":"stray","parentUuid":"u","sessionId":"` + savedChildParent + `","agentId":"Alpha","message":{"content":"unselected sibling"}}` + "\n" +
		`{"type":"progress","uuid":"p","parentUuid":"u","sessionId":"` + savedChildParent + `","agentId":"Alpha"}` + "\n" +
		`{"type":"assistant","uuid":"a","parentUuid":"p","sessionId":"` + savedChildParent + `","agentId":"Alpha","isSidechain":true,"teamName":"team","message":{"content":[{"type":"future","n":9007199254740993,"nullable":null}],"id":"same"}}` + "\n"
}
func childQueryKind(t *testing.T, err error, kind string) {
	t.Helper()
	var qe *SessionQueryError
	require.ErrorAs(t, err, &qe)
	require.Equal(t, kind, qe.Kind)
}

func TestIndependentChildHistoryDiscoveryAndContent(t *testing.T) {
	scope, root := childHistoryFixture(t)
	nested := filepath.Join(root, "workflows", "synthetic")
	writeChild(t, nested, "Alpha", childConversation())
	require.NoError(t, os.WriteFile(filepath.Join(nested, "agent-Alpha.meta.json"), []byte(`{"agentId":"Alpha","parentAgentId":"unlisted-parent","toolUseId":"native-tool","future":{"n":9007199254740993,"x":null}}`), 0600))
	writeChild(t, root, "Beta", "")
	page, err := ListSubagents(context.Background(), savedChildParent, ListSubagentsOptions{Scope: scope})
	require.NoError(t, err)
	require.Len(t, page.Records, 2)
	require.Equal(t, "Beta", page.Records[0].AgentID)
	require.Equal(t, "Alpha", page.Records[1].AgentID)
	require.Equal(t, "per-record", page.Coverage.SidecarStatus)
	require.Equal(t, "complete", page.Coverage.Status)
	require.False(t, page.Coverage.Atomic)
	require.Equal(t, "unknown", page.Records[1].Running)
	require.Equal(t, "unlisted-parent", *page.Records[1].ParentAgentID)
	messages, err := GetSubagentMessages(context.Background(), savedChildParent, "Alpha", SubagentMessagesOptions{Scope: scope})
	require.NoError(t, err)
	require.Len(t, messages.Messages, 2)
	require.Equal(t, "u", messages.Messages[0].UUID)
	require.Equal(t, "a", messages.Messages[1].UUID)
	require.Contains(t, string(messages.Messages[1].Raw), `9007199254740993`)
	require.Contains(t, string(messages.Messages[1].Raw), `"nullable":null`)
	require.Equal(t, "native-tool", *messages.Messages[1].ParentToolUseID)
	// Child selection retains native sidechain/meta/team content. It does not use main-chain filters.
	require.Contains(t, string(messages.Messages[0].Raw), `"isMeta":true`)
	messages.Info.Sidecar[0] = 'x'
	*messages.Messages[0].ParentAgentID = "mutated"
	again, err := GetSubagentMessages(context.Background(), savedChildParent, "Alpha", SubagentMessagesOptions{Scope: scope, Offset: 1, Limit: 1})
	require.NoError(t, err)
	require.Len(t, again.Messages, 1)
	require.Equal(t, "a", again.Messages[0].UUID)
	require.Equal(t, "unlisted-parent", *again.Info.ParentAgentID)
	empty, err := GetSubagentMessages(context.Background(), savedChildParent, "Beta", SubagentMessagesOptions{Scope: scope})
	require.NoError(t, err)
	require.Empty(t, empty.Messages)
	require.Equal(t, "absent", empty.Coverage.SidecarStatus)
}

func TestIndependentChildHistoryLayoutAndIdentity(t *testing.T) {
	for _, mode := range []string{"duplicate-id", "hardlink", "orphan-sidecar", "unknown-layout", "bad-sidecar", "identity-conflict", "sidecar-self", "null-identity"} {
		t.Run(mode, func(t *testing.T) {
			scope, root := childHistoryFixture(t)
			path := writeChild(t, root, "Alpha", childConversation())
			kind := "corrupt"
			switch mode {
			case "duplicate-id":
				writeChild(t, filepath.Join(root, "nested"), "Alpha", childConversation())
				kind = "ambiguous"
			case "hardlink":
				require.NoError(t, os.Link(path, filepath.Join(root, "agent-Beta.jsonl")))
				kind = "ambiguous"
			case "orphan-sidecar":
				require.NoError(t, os.WriteFile(filepath.Join(root, "agent-missing.meta.json"), []byte(`{}`), 0600))
			case "unknown-layout":
				require.NoError(t, os.WriteFile(filepath.Join(root, "unknown.txt"), nil, 0600))
				kind = "unsupported"
			case "bad-sidecar":
				require.NoError(t, os.WriteFile(filepath.Join(root, "agent-Alpha.meta.json"), []byte(`{"x":1,"x":2}`), 0600))
			case "identity-conflict":
				require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(childConversation(), savedChildParent, "22222222-2222-4222-8222-222222222222")), 0600))
			case "sidecar-self":
				require.NoError(t, os.WriteFile(filepath.Join(root, "agent-Alpha.meta.json"), []byte(`{"parentAgentId":"Alpha"}`), 0600))
			case "null-identity":
				require.NoError(t, os.WriteFile(filepath.Join(root, "agent-Alpha.meta.json"), []byte(`{"agentId":null}`), 0600))
			}
			page, err := ListSubagents(context.Background(), savedChildParent, ListSubagentsOptions{Scope: scope})
			childQueryKind(t, err, kind)
			require.Empty(t, page.Records)
			content, err := GetSubagentMessages(context.Background(), savedChildParent, "Alpha", SubagentMessagesOptions{Scope: scope})
			childQueryKind(t, err, kind)
			require.Empty(t, content.Messages)
			require.Empty(t, content.Info.AgentID)
		})
	}
}

func TestIndependentChildHistoryCorruptChains(t *testing.T) {
	for _, mode := range []string{"missing-parent", "cycle", "duplicate", "empty-parent", "invalid-json", "partial-line", "unknown-type", "bad-utf8"} {
		t.Run(mode, func(t *testing.T) {
			scope, root := childHistoryFixture(t)
			body := childConversation()
			switch mode {
			case "missing-parent":
				body = strings.ReplaceAll(body, `"parentUuid":"p"`, `"parentUuid":"missing"`)
			case "cycle":
				body = strings.ReplaceAll(body, `"parentUuid":null`, `"parentUuid":"a"`)
			case "duplicate":
				body += strings.Split(body, "\n")[0] + "\n"
			case "empty-parent":
				body = strings.ReplaceAll(body, `"parentUuid":null`, `"parentUuid":""`)
			case "invalid-json":
				body = "{invalid}\n"
			case "partial-line":
				body = strings.TrimSuffix(body, "\n")
			case "unknown-type":
				body = `{"type":"unknown","uuid":"x"}` + "\n"
			case "bad-utf8":
				body = "{\"type\":\"user\",\"text\":\"" + string([]byte{255}) + "\"}\n"
			}
			writeChild(t, root, "Alpha", body)
			page, err := GetSubagentMessages(context.Background(), savedChildParent, "Alpha", SubagentMessagesOptions{Scope: scope})
			kind := "corrupt"
			if mode == "unknown-type" {
				kind = "unsupported"
			}
			childQueryKind(t, err, kind)
			require.Empty(t, page.Messages)
			require.Empty(t, page.Info.AgentID)
		})
	}
}

func TestIndependentChildHistoryContainmentAndLimits(t *testing.T) {
	scope, root := childHistoryFixture(t)
	writeChild(t, root, "Alpha", childConversation())
	for _, id := range []string{"", ".", "..", "../outside", "a/b", `a\b`, "C:drive", "a\x00b", "a\nb", string([]byte{255})} {
		p, err := GetSubagentMessages(context.Background(), savedChildParent, id, SubagentMessagesOptions{Scope: scope})
		childQueryKind(t, err, "invalid-input")
		require.Empty(t, p.Messages)
	}
	for _, limit := range []SubagentHistoryLimits{{MaxBytes: 1}, {MaxLines: 1}, {MaxRecords: 1}, {MaxLineBytes: 5}, {MaxFiles: 1}, {MaxDirectories: 1}} {
		p, err := GetSubagentMessages(context.Background(), savedChildParent, "Alpha", SubagentMessagesOptions{Scope: scope, Limits: limit})
		childQueryKind(t, err, "resource-limit")
		require.Empty(t, p.Messages)
	}
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(root, "escape")))
	p, err := ListSubagents(context.Background(), savedChildParent, ListSubagentsOptions{Scope: scope})
	childQueryKind(t, err, "unsupported")
	require.Empty(t, p.Records)
	require.NoError(t, os.Remove(filepath.Join(root, "escape")))
	require.NoError(t, os.Symlink(filepath.Join(root, "agent-Alpha.jsonl"), filepath.Join(root, "agent-link.jsonl")))
	_, err = GetSubagentMessages(context.Background(), savedChildParent, "Alpha", SubagentMessagesOptions{Scope: scope})
	childQueryKind(t, err, "unsupported")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ListSubagents(ctx, savedChildParent, ListSubagentsOptions{Scope: scope})
	require.ErrorIs(t, err, context.Canceled)
}

func TestIndependentChildHistoryObservedMutation(t *testing.T) {
	for _, mode := range []string{"append", "rewrite", "replacement", "new-agent", "sidecar-appearance", "directory-replacement"} {
		t.Run(mode, func(t *testing.T) {
			scope, root := childHistoryFixture(t)
			path := writeChild(t, root, "Alpha", childConversation())
			coverage := SubagentHistoryCoverage{SessionID: savedChildParent, AgentID: "Alpha"}
			q, err := newChildQuery(context.Background(), savedChildParent, "Alpha", scope, SubagentHistoryLimits{}, 0, 0, 0, 1000, &coverage)
			require.NoError(t, err)
			defer q.close()
			candidates, err := q.inventory()
			require.NoError(t, err)
			require.Len(t, candidates, 1)
			_, _, err = q.load(candidates[0], true)
			require.NoError(t, err)
			switch mode {
			case "append":
				f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
				require.NoError(t, e)
				_, e = f.WriteString("\n")
				require.NoError(t, e)
				require.NoError(t, f.Close())
			case "rewrite":
				data, e := os.ReadFile(path)
				require.NoError(t, e)
				data[0] = '['
				require.NoError(t, os.WriteFile(path, data, 0600))
				require.NoError(t, os.Chtimes(path, q.files[1].info.ModTime(), q.files[1].info.ModTime()))
			case "replacement":
				require.NoError(t, os.Rename(path, path+".old"))
				writeChild(t, root, "Alpha", childConversation())
			case "new-agent":
				writeChild(t, root, "Beta", "")
			case "sidecar-appearance":
				require.NoError(t, os.WriteFile(filepath.Join(root, "agent-Alpha.meta.json"), []byte(`{}`), 0600))
			case "directory-replacement":
				require.NoError(t, os.Rename(root, root+".old"))
				require.NoError(t, os.Mkdir(root, 0700))
			}
			childQueryKind(t, q.verify(), "changed")
		})
	}
}

func TestIndependentChildHistoryErrorsAndIsolation(t *testing.T) {
	scope, root := childHistoryFixture(t)
	page, err := ListSubagents(context.Background(), savedChildParent, ListSubagentsOptions{Scope: scope})
	require.NoError(t, err)
	require.Empty(t, page.Records)
	_, err = GetSubagentMessages(context.Background(), savedChildParent, "missing", SubagentMessagesOptions{Scope: scope})
	childQueryKind(t, err, "missing")
	require.True(t, errors.Is(err, os.ErrNotExist))
	writeChild(t, root, "Alpha", childConversation())
	s := openSessionFixture(t)
	submitFrame(t, s, `{"type":"system","subtype":"init","session_id":"`+savedChildParent+`","claude_code_version":"2.1.258"}`)
	sessionNative(t, s)
	const live = `{"type":"assistant","session_id":"11111111-1111-4111-8111-111111111111","message":{"content":"unconsumed live"}}`
	submitFrame(t, s, live)
	_, err = ListSubagents(context.Background(), savedChildParent, ListSubagentsOptions{Scope: scope})
	require.NoError(t, err)
	_, err = GetSubagentMessages(context.Background(), savedChildParent, "Alpha", SubagentMessagesOptions{Scope: scope})
	require.NoError(t, err)
	got := sessionNative(t, s)
	require.JSONEq(t, live, string(got.Raw))
	require.Nil(t, got.Request)
}

// Explicitly selected saved native data only; this check never launches an agent.
func TestIndependentChildHistoryNativeSource(t *testing.T) {
	capture := os.Getenv("CLAUDE_CHILD_HISTORY_CAPTURE")
	if capture == "" {
		t.Skip("explicit saved native capture required")
	}
	require.True(t, filepath.IsAbs(capture))
	data, err := os.ReadFile(filepath.Join(capture, "report.json"))
	require.NoError(t, err)
	var report struct {
		Receipts []map[string]json.RawMessage `json:"receipts"`
	}
	require.NoError(t, json.Unmarshal(data, &report))
	tested := 0
	for _, receipt := range report.Receipts {
		raw, ok := receipt["list_subagents"]
		if !ok {
			continue
		}
		var native struct {
			SessionID string   `json:"sessionId"`
			AgentIDs  []string `json:"agentIds"`
		}
		require.NoError(t, json.Unmarshal(raw, &native))
		require.NotEmpty(t, native.AgentIDs)
		scope := SessionQueryScope{ConfigDir: filepath.Join(capture, "config"), ProjectDir: filepath.Join(capture, "project")}
		page, err := ListSubagents(context.Background(), native.SessionID, ListSubagentsOptions{Scope: scope})
		require.NoError(t, err)
		ids := []string{}
		for _, record := range page.Records {
			ids = append(ids, record.AgentID)
		}
		require.ElementsMatch(t, native.AgentIDs, ids)
		for _, id := range ids {
			content, err := GetSubagentMessages(context.Background(), native.SessionID, id, SubagentMessagesOptions{Scope: scope})
			require.NoError(t, err)
			require.NotEmpty(t, content.Messages)
			require.Equal(t, "complete", content.Coverage.Status)
			found := false
			for _, message := range content.Messages {
				if strings.Contains(string(message.Raw), "SYNTHETIC_SAVED_CHILD") {
					found = true
				}
			}
			require.True(t, found, "native saved child marker missing")
			tested++
		}
	}
	require.Positive(t, tested)
}
