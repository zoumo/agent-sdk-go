// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const discoveryMain = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

func discoveryFixture(t *testing.T, beforeObserve ...func(string, string)) (*Session, string, string) {
	t.Helper()
	config, cwd := t.TempDir(), t.TempDir()
	cwd, err := filepath.EvalSymlinks(cwd)
	require.NoError(t, err)
	s := openSessionFixture(t, func(o *SessionOptions) {
		o.WorkDir = cwd
		o.Env = append(o.Env, "CLAUDE_CONFIG_DIR="+config, "CLAUDE_GO_TRANSCRIPT="+filepath.Join(cwd, "wire.jsonl"))
	})
	base := filepath.Join(config, "projects", projectUnsafe.ReplaceAllString(cwd, "-"), discoveryMain)
	discoveryWrite(t, base+".jsonl", `{"type":"user","sessionId":"`+discoveryMain+`","cwd":`+quoteDiscovery(cwd)+`,"uuid":"u","parentUuid":null}`+"\n")
	for _, setup := range beforeObserve {
		setup(base, cwd)
	}
	submitFrame(t, s, `{"type":"system","subtype":"init","session_id":"`+discoveryMain+`","claude_code_version":"2.1.258"}`)
	sessionNative(t, s)
	return s, base, cwd
}
func quoteDiscovery(s string) string { b, _ := json.Marshal(s); return string(b) }
func discoveryWrite(t *testing.T, path, data string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte(data), 0600))
}

func TestSessionDiscoveryExistingNativeRecords(t *testing.T) {
	s, _, _ := discoveryFixture(t, func(base, cwd string) {
		discoveryWrite(t, filepath.Join(base, "subagents", "agent-old.jsonl"), `{"type":"assistant","sessionId":"`+discoveryMain+`","agentId":"old","future":null}`+"\n")
		discoveryWrite(t, filepath.Join(base, "subagents", "workflows", "run", "agent-nested.jsonl"), `{"type":"result","future_run":42}`+"\n")
		discoveryWrite(t, filepath.Join(base, "subagents", "workflows", "run", "agent-nested.meta.json"), `{"parentAgentId":"old","toolUseId":"tool","unknown":null}`)
		discoveryWrite(t, filepath.Join(filepath.Dir(base), "other", "subagents", "agent-foreign.jsonl"), "{}\n")
	})
	page, err := s.ListSubagents(context.Background(), SubagentQuery{})
	require.NoError(t, err)
	require.Len(t, page.Records, 2)
	assert.Equal(t, "complete", page.Coverage.Status)
	assert.Equal(t, discoveryMain, page.Coverage.SessionID)
	assert.Equal(t, "old", page.Records[0].AgentID)
	assert.Nil(t, page.Records[0].ParentAgentID)
	assert.Equal(t, "unknown", page.Records[0].Running)
	assert.Contains(t, string(page.Records[0].Metadata[0]), `"future":null`)
	assert.Equal(t, "old", *page.Records[1].ParentAgentID)
	assert.Equal(t, "tool", page.Records[1].ToolUseID)
	assert.Contains(t, string(page.Records[1].Sidecar), `"unknown":null`)
	require.NoError(t, s.Err()) // No terminal transcript, resume or Load gate.
}

func TestSessionDiscoveryDamagedSidecarRetainsEvidence(t *testing.T) {
	s, base, _ := discoveryFixture(t)
	discoveryWrite(t, filepath.Join(base, "subagents", "agent-a.jsonl"), "{}\n")
	bad := `{"parentAgentId":`
	discoveryWrite(t, filepath.Join(base, "subagents", "agent-a.meta.json"), bad)
	p, err := s.ListSubagents(context.Background(), SubagentQuery{})
	require.ErrorIs(t, err, ErrDiscoveryCoverage)
	require.Len(t, p.Records, 1)
	assert.Nil(t, p.Records[0].ParentAgentID)
	assert.Equal(t, bad, string(p.Records[0].Sidecar))
	assert.Equal(t, "partial", p.Coverage.Status)
}

func TestSessionDiscoveryPartialTranscriptPreservesRecord(t *testing.T) {
	s, base, _ := discoveryFixture(t)
	bad := "{}\n{broken\n"
	discoveryWrite(t, filepath.Join(base, "subagents", "agent-old.jsonl"), bad)
	p, err := s.ListSubagents(context.Background(), SubagentQuery{})
	require.ErrorIs(t, err, ErrDiscoveryCoverage)
	require.Len(t, p.Records, 1)
	require.Len(t, p.Records[0].Metadata, 2)
	assert.Equal(t, "{broken", string(p.Records[0].Metadata[1]))
}

func TestSessionDiscoverySidecarEmptyParentStaysUnknown(t *testing.T) {
	s, base, _ := discoveryFixture(t)
	discoveryWrite(t, filepath.Join(base, "subagents", "agent-a.jsonl"), "{}\n")
	discoveryWrite(t, filepath.Join(base, "subagents", "agent-a.meta.json"), `{"parentAgentId":"","parent_agent_id":"unreviewed-extension","run_id":"opaque"}`)
	p, err := s.ListSubagents(context.Background(), SubagentQuery{})
	require.NoError(t, err)
	require.Len(t, p.Records, 1)
	assert.Nil(t, p.Records[0].ParentAgentID)
	assert.Contains(t, string(p.Records[0].Sidecar), `"parent_agent_id":"unreviewed-extension"`)
}

func TestSessionDiscoveryLimitsAndPartialCoverage(t *testing.T) {
	for _, tc := range []struct {
		name     string
		q        SubagentQuery
		want     int
		resource bool
	}{
		{"agents", SubagentQuery{MaxRecords: 1}, 1, true},
		{"bytes", SubagentQuery{MaxBytes: 1}, 3, true},
		{"lines", SubagentQuery{MaxLines: 1}, 3, true},
		{"entries", SubagentQuery{MaxEntries: 1}, 1, true},
		{"depth", SubagentQuery{MaxDepth: 1}, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, base, _ := discoveryFixture(t)
			for _, name := range []string{"agent-a.jsonl", "agent-b.jsonl", "nested/deep/agent-c.jsonl"} {
				discoveryWrite(t, filepath.Join(base, "subagents", name), "{}\n")
			}
			p, err := s.ListSubagents(context.Background(), tc.q)
			require.ErrorIs(t, err, ErrDiscoveryCoverage)
			require.ErrorIs(t, err, ErrResourceLimit)
			assert.Len(t, p.Records, tc.want)
			assert.NotEqual(t, "complete", p.Coverage.Status)
			assert.NotEmpty(t, p.Coverage.Issues)
			assert.False(t, p.Coverage.Atomic)
		})
	}
}

func TestSessionDiscoveryDuplicateConflictAndUnknownLayout(t *testing.T) {
	s, base, _ := discoveryFixture(t)
	for _, name := range []string{"agent-a.jsonl", "run/agent-a.jsonl"} {
		discoveryWrite(t, filepath.Join(base, "subagents", name), "{}\n")
	}
	discoveryWrite(t, filepath.Join(base, "subagents", "agent-foreign.jsonl"), `{"sessionId":"other"}`+"\n")
	discoveryWrite(t, filepath.Join(base, "subagents", "agent-orphan.meta.json"), "{}")
	discoveryWrite(t, filepath.Join(base, "subagents", "future.data"), "future")
	p, err := s.ListSubagents(context.Background(), SubagentQuery{})
	require.ErrorIs(t, err, ErrDiscoveryCoverage)
	require.Len(t, p.Records, 3)
	assert.Equal(t, p.Records[0].AgentID, p.Records[2].AgentID)
	assert.Contains(t, string(p.Records[1].Metadata[0]), `"sessionId":"other"`)
	var reasons []string
	for _, i := range p.Coverage.Issues {
		reasons = append(reasons, i.Reason)
	}
	assert.Contains(t, reasons, "duplicate-agent-id")
	assert.Contains(t, reasons, "orphan-sidecar")
	assert.Contains(t, reasons, "unsupported-layout")
}

func TestSessionDiscoveryScopeFailureAndMissingLayout(t *testing.T) {
	for _, tc := range []string{"missing-main", "wrong-main", "missing-layout", "wrong-cwd", "malformed-main"} {
		t.Run(tc, func(t *testing.T) {
			s, base, _ := discoveryFixture(t)
			switch tc {
			case "missing-main":
				require.NoError(t, os.Remove(base+".jsonl"))
			case "wrong-main":
				discoveryWrite(t, base+".jsonl", `{"sessionId":"foreign","cwd":"/other"}`)
			case "wrong-cwd":
				discoveryWrite(t, base+".jsonl", `{"sessionId":"`+discoveryMain+`","cwd":"/other"}`)
			case "malformed-main":
				discoveryWrite(t, base+".jsonl", "{bad\n")
			}
			p, err := s.ListSubagents(context.Background(), SubagentQuery{})
			require.ErrorIs(t, err, ErrDiscoveryCoverage)
			assert.Empty(t, p.Records)
			assert.NotEqual(t, "complete", p.Coverage.Status)
		})
	}
}

func TestSessionDiscoverySymlinksNeverMixOtherRecords(t *testing.T) {
	for _, tc := range []string{"directory", "transcript", "sidecar", "ancestor", "main"} {
		t.Run(tc, func(t *testing.T) {
			s, base, _ := discoveryFixture(t)
			outside := t.TempDir()
			discoveryWrite(t, filepath.Join(outside, "agent-foreign.jsonl"), "{}\n")
			discoveryWrite(t, filepath.Join(base, "subagents", "agent-own.jsonl"), "{}\n")
			switch tc {
			case "directory":
				require.NoError(t, os.Symlink(outside, filepath.Join(base, "subagents", "linked")))
			case "transcript":
				require.NoError(t, os.Symlink(filepath.Join(outside, "agent-foreign.jsonl"), filepath.Join(base, "subagents", "agent-linked.jsonl")))
			case "sidecar":
				require.NoError(t, os.Symlink(filepath.Join(outside, "agent-foreign.jsonl"), filepath.Join(base, "subagents", "agent-own.meta.json")))
			case "main":
				require.NoError(t, os.Remove(base+".jsonl"))
				require.NoError(t, os.Symlink(filepath.Join(outside, "agent-foreign.jsonl"), base+".jsonl"))
			case "ancestor":
				require.NoError(t, os.RemoveAll(base))
				require.NoError(t, os.Symlink(outside, base))
			}
			p, err := s.ListSubagents(context.Background(), SubagentQuery{})
			require.ErrorIs(t, err, ErrDiscoveryCoverage)
			for _, r := range p.Records {
				assert.Equal(t, "own", r.AgentID)
				assert.Empty(t, r.Sidecar)
			}
		})
	}
}

func TestSessionDiscoveryFreshReadDoesNotChangeObservationOrWire(t *testing.T) {
	s, base, cwd := discoveryFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(base, "subagents"), 0700))
	empty, err := s.ListSubagents(context.Background(), SubagentQuery{})
	require.NoError(t, err)
	assert.Empty(t, empty.Records)
	assert.Equal(t, "complete", empty.Coverage.Status)
	discoveryWrite(t, filepath.Join(base, "subagents", "agent-old.jsonl"), "{}\n")
	discoveryWrite(t, filepath.Join(base, "subagents", "agent-old.meta.json"), `{"parentAgentId":"unobserved","toolUseId":"old-tool"}`)
	p, err := s.ListSubagents(context.Background(), SubagentQuery{})
	require.NoError(t, err)
	require.Len(t, p.Records, 1)
	require.NoError(t, os.Remove(filepath.Join(base, "subagents", "agent-old.jsonl")))
	require.NoError(t, os.Remove(filepath.Join(base, "subagents", "agent-old.meta.json")))
	p, err = s.ListSubagents(context.Background(), SubagentQuery{})
	require.NoError(t, err)
	assert.Empty(t, p.Records)
	assert.Empty(t, s.Unattributed().Records)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = s.Receive(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	// Old sidecar does not establish a live tool alias or historical parent.
	submitFrame(t, s, `{"type":"assistant","session_id":"`+discoveryMain+`","parent_tool_use_id":"old-tool","message":{"content":"new fact"}}`)
	r := sessionNative(t, s)
	assert.Empty(t, r.Source.TaskID)
	assert.Nil(t, r.Parent)
	require.NoError(t, s.Close())
	frames := permissionTranscript(t, filepath.Join(cwd, "wire.jsonl"))
	require.Len(t, frames, 2, "only the two explicit test Submit calls, no query I/O")
	for {
		_, err = s.Receive(context.Background())
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
	}
}

func TestSessionDiscoveryCancellationCloseAndUnknownIdentity(t *testing.T) {
	s, base, _ := discoveryFixture(t)
	discoveryWrite(t, filepath.Join(base, "subagents", "agent-a.jsonl"), "{}\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.ListSubagents(ctx, SubagentQuery{})
	require.ErrorIs(t, err, context.Canceled)
	_, err = s.ListSubagents(nil, SubagentQuery{})
	require.ErrorIs(t, err, ErrInvalidInput)
	_, err = s.ListSubagents(context.Background(), SubagentQuery{MaxBytes: -1})
	require.ErrorIs(t, err, ErrInvalidInput)
	require.NoError(t, s.Close())
	_, err = s.ListSubagents(context.Background(), SubagentQuery{})
	require.ErrorIs(t, err, ErrClosed)
	// A requested ID alone cannot select native storage.
	unknown := openSessionFixture(t, func(o *SessionOptions) { o.ResumeID = discoveryMain })
	_, err = unknown.ListSubagents(context.Background(), SubagentQuery{})
	require.ErrorIs(t, err, ErrInvalidInput)
	id, ok := unknown.SessionID()
	assert.Empty(t, id)
	assert.False(t, ok)
}

func TestSessionDiscoveryMetadataNeverGatesRecordDiscovery(t *testing.T) {
	for _, tc := range []string{"large-main", "incomplete-main-tail", "large-child", "child-session-id-is-not-main-alias"} {
		t.Run(tc, func(t *testing.T) {
			s, base, cwd := discoveryFixture(t)
			child := filepath.Join(base, "subagents", "agent-old.jsonl")
			discoveryWrite(t, child, "{}\n")
			switch tc {
			case "large-main":
				discoveryWrite(t, base+".jsonl", `{"sessionId":"`+discoveryMain+`","cwd":`+quoteDiscovery(cwd)+`}`+"\n"+`{"message":"`+strings.Repeat("x", 2<<20)+`"}`+"\n")
			case "incomplete-main-tail":
				f, err := os.OpenFile(base+".jsonl", os.O_APPEND|os.O_WRONLY, 0600)
				require.NoError(t, err)
				_, err = f.WriteString(`{"message":"not yet complete`)
				require.NoError(t, err)
				require.NoError(t, f.Close())
			case "large-child":
				discoveryWrite(t, child, `{"message":"`+strings.Repeat("x", 2<<20)+`"}`+"\n")
			case "child-session-id-is-not-main-alias":
				discoveryWrite(t, child, `{"sessionId":"native-child-session","cwd":"/native/child/path","agentId":"opaque-extension"}`+"\n")
			}
			p, err := s.ListSubagents(context.Background(), SubagentQuery{})
			require.Len(t, p.Records, 1)
			assert.Equal(t, "old", p.Records[0].AgentID)
			if tc == "child-session-id-is-not-main-alias" {
				require.NoError(t, err)
				assert.Equal(t, "complete", p.Coverage.Status)
			} else {
				require.ErrorIs(t, err, ErrDiscoveryCoverage)
				assert.Equal(t, "partial", p.Coverage.Status)
			}
			assert.LessOrEqual(t, p.Coverage.Bytes, int64(128<<10))
		})
	}
}

func TestSessionDiscoveryAccessFailuresKeepDiscoveredIdentity(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("requires non-root permission enforcement")
	}
	for _, tc := range []string{"directory", "transcript", "sidecar"} {
		t.Run(tc, func(t *testing.T) {
			s, base, _ := discoveryFixture(t)
			path := filepath.Join(base, "subagents", "agent-a.jsonl")
			discoveryWrite(t, path, "{}\n")
			switch tc {
			case "directory":
				path = filepath.Dir(path)
			case "sidecar":
				path = strings.TrimSuffix(path, ".jsonl") + ".meta.json"
				discoveryWrite(t, path, `{"parentAgentId":"p"}`)
			}
			require.NoError(t, os.Chmod(path, 0))
			t.Cleanup(func() { _ = os.Chmod(path, 0700) })
			p, err := s.ListSubagents(context.Background(), SubagentQuery{})
			require.ErrorIs(t, err, os.ErrPermission)
			require.ErrorIs(t, err, ErrDiscoveryCoverage)
			assert.Equal(t, "partial", p.Coverage.Status)
			if tc != "directory" {
				require.Len(t, p.Records, 1)
				assert.Equal(t, "a", p.Records[0].AgentID)
			}
		})
	}
}

func TestSessionDiscoveryConflictingSidecarDoesNotChooseLastParent(t *testing.T) {
	s, base, _ := discoveryFixture(t)
	discoveryWrite(t, filepath.Join(base, "subagents", "agent-a.jsonl"), "{}\n")
	raw := `{"parentAgentId":"one","parentAgentId":"two","toolUseId":"t"}`
	discoveryWrite(t, filepath.Join(base, "subagents", "agent-a.meta.json"), raw)
	p, err := s.ListSubagents(context.Background(), SubagentQuery{})
	require.ErrorIs(t, err, ErrDiscoveryCoverage)
	require.Len(t, p.Records, 1)
	assert.Nil(t, p.Records[0].ParentAgentID)
	assert.JSONEq(t, raw, string(p.Records[0].Sidecar))
}

func TestSessionDiscoveryAmbiguousMetadataNeverCreatesParent(t *testing.T) {
	for _, raw := range []string{
		"{\"parentAgentId\":\"" + string([]byte{0xff}) + "\"}",
		`{"parentAgentId":"\ud800"}`,
		`{"parentAgentId":"\udfff"}`,
		`{"parentAgentId":"\ud800\u0041"}`,
		`{"toolUseId":"first","toolUseId":"last","parentAgentId":"p"}`,
	} {
		t.Run(quoteDiscovery(raw), func(t *testing.T) {
			s, base, _ := discoveryFixture(t)
			discoveryWrite(t, filepath.Join(base, "subagents", "agent-a.jsonl"), "{}\n")
			discoveryWrite(t, filepath.Join(base, "subagents", "agent-a.meta.json"), raw)
			p, err := s.ListSubagents(context.Background(), SubagentQuery{})
			require.ErrorIs(t, err, ErrDiscoveryCoverage)
			require.Len(t, p.Records, 1)
			assert.Nil(t, p.Records[0].ParentAgentID)
			assert.Empty(t, p.Records[0].ToolUseID)
			assert.Equal(t, raw, string(p.Records[0].Sidecar))
			assert.Equal(t, "partial", p.Coverage.Status)
		})
	}
}

func TestSessionDiscoveryMainAmbiguityIsMetadataGapNotLoadGate(t *testing.T) {
	s, base, _ := discoveryFixture(t)
	raw := `{"sessionId":"wrong","sessionId":"` + discoveryMain + `","cwd":"also ambiguous"}`
	discoveryWrite(t, base+".jsonl", raw+"\n")
	discoveryWrite(t, filepath.Join(base, "subagents", "agent-a.jsonl"), "{}\n")
	p, err := s.ListSubagents(context.Background(), SubagentQuery{})
	require.ErrorIs(t, err, ErrDiscoveryCoverage)
	require.Len(t, p.Records, 1)
	assert.Equal(t, "partial", p.Coverage.Status)
}

func TestSessionDiscoveryExplicitRootAliasesAreResolvedAtOpen(t *testing.T) {
	config, cwd, aliases := t.TempDir(), t.TempDir(), t.TempDir()
	config, err := filepath.EvalSymlinks(config)
	require.NoError(t, err)
	cwd, err = filepath.EvalSymlinks(cwd)
	require.NoError(t, err)
	configAlias, cwdAlias := filepath.Join(aliases, "config"), filepath.Join(aliases, "cwd")
	require.NoError(t, os.Symlink(config, configAlias))
	require.NoError(t, os.Symlink(cwd, cwdAlias))
	s := openSessionFixture(t, func(o *SessionOptions) { o.WorkDir = cwdAlias; o.Env = append(o.Env, "CLAUDE_CONFIG_DIR="+configAlias) })
	base := filepath.Join(config, "projects", projectUnsafe.ReplaceAllString(cwd, "-"), discoveryMain)
	discoveryWrite(t, base+".jsonl", `{"sessionId":"`+discoveryMain+`","cwd":`+quoteDiscovery(cwd)+`}`+"\n")
	discoveryWrite(t, filepath.Join(base, "subagents", "agent-a.jsonl"), "{}\n")
	require.NoError(t, os.Remove(configAlias))
	require.NoError(t, os.Symlink(t.TempDir(), configAlias))
	submitFrame(t, s, `{"type":"system","subtype":"init","session_id":"`+discoveryMain+`","claude_code_version":"2.1.258"}`)
	sessionNative(t, s)
	p, err := s.ListSubagents(context.Background(), SubagentQuery{})
	require.NoError(t, err)
	require.Len(t, p.Records, 1)
	assert.Equal(t, config, p.Coverage.ConfigDir)
	assert.Equal(t, cwd, p.Coverage.CWD)
}

func TestSessionDiscoveryCloseAndCancellationDoNotWaitForReceive(t *testing.T) {
	s, base, _ := discoveryFixture(t)
	for i := 0; i < 100; i++ {
		discoveryWrite(t, filepath.Join(base, "subagents", fmt.Sprintf("agent-%03d.jsonl", i)), "{}\n")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := s.ListSubagents(ctx, SubagentQuery{}); done <- err }()
	cancel()
	require.NoError(t, s.Close())
	select {
	case err := <-done:
		require.Error(t, err)
		assert.True(t, errors.Is(err, context.Canceled) || errors.Is(err, ErrClosed))
	case <-time.After(3 * time.Second):
		t.Fatal("query cleanup waited for consumption")
	}
	select {
	case <-s.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Close waited for consumption")
	}
}

func TestSessionDiscoverySidecarIsJSONObjectNotJSONL(t *testing.T) {
	s, base, _ := discoveryFixture(t)
	discoveryWrite(t, filepath.Join(base, "subagents", "agent-a.jsonl"), "{}\n")
	raw := "{\n  \"parentAgentId\": \"p\",\n  \"toolUseId\": \"t\",\n  \"extra\": null\n}\n"
	discoveryWrite(t, filepath.Join(base, "subagents", "agent-a.meta.json"), raw)
	p, err := s.ListSubagents(context.Background(), SubagentQuery{})
	require.NoError(t, err)
	require.Len(t, p.Records, 1)
	assert.Equal(t, "p", *p.Records[0].ParentAgentID)
	assert.Equal(t, raw, string(p.Records[0].Sidecar))
}

func TestSessionDiscoveryMetadataKeysMatchNativeCasing(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		parent string
	}{
		{`{"PARENTAGENTID":"invented","TOOLUSEID":"invented"}`, ""},
		{`{"parentAgentId":"one","PARENTAGENTID":"two"}`, "one"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			s, base, _ := discoveryFixture(t)
			discoveryWrite(t, base+".jsonl", `{"SESSIONID":"wrong","CWD":"/wrong"}`+"\n")
			discoveryWrite(t, filepath.Join(base, "subagents", "agent-a.jsonl"), "{}\n")
			discoveryWrite(t, filepath.Join(base, "subagents", "agent-a.meta.json"), tc.raw)
			p, err := s.ListSubagents(context.Background(), SubagentQuery{})
			require.NoError(t, err)
			require.Len(t, p.Records, 1)
			if tc.parent == "" {
				assert.Nil(t, p.Records[0].ParentAgentID)
			} else {
				require.NotNil(t, p.Records[0].ParentAgentID)
				assert.Equal(t, tc.parent, *p.Records[0].ParentAgentID)
			}
			assert.Empty(t, p.Records[0].ToolUseID)
			assert.Equal(t, tc.raw, string(p.Records[0].Sidecar))
		})
	}
}

func TestSessionDiscoveryWrongTypeMainIdentityIsExplicitMetadataGap(t *testing.T) {
	s, base, _ := discoveryFixture(t)
	discoveryWrite(t, base+".jsonl", `{"sessionId":123,"cwd":[]}`+"\n")
	discoveryWrite(t, filepath.Join(base, "subagents", "agent-a.jsonl"), "{}\n")
	p, err := s.ListSubagents(context.Background(), SubagentQuery{})
	require.ErrorIs(t, err, ErrDiscoveryCoverage)
	require.Len(t, p.Records, 1)
	assert.Equal(t, "partial", p.Coverage.Status)
	require.Len(t, p.Coverage.MainMetadata, 1)
	assert.Equal(t, `{"sessionId":123,"cwd":[]}`, string(p.Coverage.MainMetadata[0]))
}
