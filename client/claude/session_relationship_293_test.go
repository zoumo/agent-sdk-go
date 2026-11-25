package claude

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const relationshipInit293 = `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.293"}`

func TestSession293PermissionCheckStatusPreservesNativeFacts(t *testing.T) {
	frames := []string{
		relationshipInit293,
		`{"type":"system","subtype":"permission_check_status","session_id":"s","tool_use_id":"call","status":"checking","future":{"n":9007199254740993}}`,
		`{"type":"system","subtype":"permission_check_status","session_id":"s","tool_use_id":"call","status":"done"}`,
	}
	records, _ := relationshipRecords(t, frames)
	require.Len(t, records, len(frames))
	for index, frame := range frames {
		require.Equal(t, frame, string(records[index].Raw))
		require.Equal(t, "s", records[index].Source.SessionID)
		require.Empty(t, records[index].Source.AssociationID)
		require.Nil(t, records[index].Parent)
	}
}

func relationshipRecords(t *testing.T, frames []string, configure ...func(*SessionOptions)) ([]Received, []string) {
	t.Helper()
	session := openSessionFixture(t, configure...)
	for _, frame := range frames {
		submitFrame(t, session, frame)
	}
	require.NoError(t, session.Interrupt(t.Context(), InterruptOptions{}))
	require.NoError(t, session.Close())
	var records []Received
	var reasons []string
	for {
		record, err := session.Receive(context.Background())
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
		if record.Event != nil {
			records = append(records, record)
		}
		if record.Observation != nil {
			reasons = append(reasons, record.Observation.Reason)
		}
	}
	return records, reasons
}

func TestSession293DeclaredIdentityAndParent(t *testing.T) {
	frames := []string{
		relationshipInit293,
		creationRequest("outer", "Agent", `{}`, ""),
		creationTask("a", "outer", "false"),
		`{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent","parent_task_id":"a","run_id":"z"}`,
		`{"type":"assistant","session_id":"s","agent_id":"b","message":{"content":[{"type":"text","text":"child"}]}}`,
		`{"type":"user","session_id":"s","agent_id":"b","message":{"content":"child input"}}`,
		`{"type":"system","subtype":"task_progress","session_id":"s","task_id":"b","run_id":"a"}`,
	}
	records, reasons := relationshipRecords(t, frames)
	require.Len(t, records, len(frames))
	require.NotContains(t, reasons, "relationship-version-unverified")
	require.NotContains(t, reasons, "identity-extension-unverified")
	require.NotContains(t, reasons, "parent-extension-unverified")
	for index, frame := range frames {
		require.Equal(t, frame, string(records[index].Raw))
		if index < 3 {
			continue
		}
		require.Equal(t, "b", records[index].Source.TaskID)
		require.Equal(t, "task:b", records[index].Source.AssociationID)
		require.NotNil(t, records[index].Parent)
		require.Equal(t, "a", records[index].Parent.TaskID)
	}
	require.Equal(t, "b", records[4].Source.AgentID)
}

func TestSession293UnconfirmedAndConflictingIdentityPreservesFacts(t *testing.T) {
	for _, test := range []struct {
		name, frame, reason string
	}{
		{"early identity", `{"type":"assistant","session_id":"s","agent_id":"unseen","message":{"content":[{"type":"text","text":"early"}]}}`, "identity-member-unconfirmed"},
		{"conflicting identity", `{"type":"assistant","session_id":"s","agent_id":"b","parent_tool_use_id":"outer","message":{"content":[{"type":"text","text":"conflict"}]}}`, "identity-alias-conflict"},
		{"unconfirmed identity with known tool", `{"type":"assistant","session_id":"s","agent_id":"unseen","parent_tool_use_id":"outer","message":{"content":[{"type":"text","text":"conflict"}]}}`, "identity-member-unconfirmed"},
		{"undeclared stream identity", `{"type":"stream_event","session_id":"s","agent_id":"a","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"unknown"}}}`, "identity-extension-unverified"},
	} {
		t.Run(test.name, func(t *testing.T) {
			frames := []string{relationshipInit293, creationTask("a", "outer", ""), creationTask("b", "inner", ""), test.frame}
			records, reasons := relationshipRecords(t, frames, func(options *SessionOptions) { options.ChildDetailPolicy = ChildDetailSuppress })
			require.Len(t, records, len(frames))
			last := records[len(records)-1]
			require.Equal(t, test.frame, string(last.Raw))
			require.Empty(t, last.Source.AssociationID)
			require.Nil(t, last.Parent)
			require.Contains(t, reasons, test.reason)
		})
	}
}

func TestSessionSupportedVersionsShareRelationshipSemantics(t *testing.T) {
	for _, version := range []string{"2.1.293", "2.1.294", "2.2.0", "3.0.0", "2.1.293+build.1"} {
		t.Run(version, func(t *testing.T) {
			frames := []string{
				fmt.Sprintf(`{"type":"system","subtype":"init","session_id":"s","claude_code_version":%q}`, version),
				creationTask("a", "outer", ""),
				`{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent","parent_task_id":"a"}`,
				`{"type":"assistant","session_id":"s","agent_id":"b","message":{"content":"child identity"}}`,
			}
			records, reasons := relationshipRecords(t, frames)
			require.Len(t, records, len(frames))
			require.NotContains(t, reasons, "relationship-version-unverified")
			require.NotContains(t, reasons, "identity-extension-unverified")
			require.NotContains(t, reasons, "parent-extension-unverified")
			last := records[len(records)-1]
			require.Equal(t, "task:b", last.Source.AssociationID)
			require.Equal(t, "b", last.Source.TaskID)
			require.NotNil(t, last.Parent)
			require.Equal(t, "a", last.Parent.TaskID)
			require.Equal(t, frames[len(frames)-1], string(last.Raw))
		})
	}
}

func TestSession293UnknownParentDoesNotCreateMemberOrRoot(t *testing.T) {
	frame := `{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent","parent_task_id":"unknown"}`
	records, reasons := relationshipRecords(t, []string{relationshipInit293, frame})
	require.Len(t, records, 2)
	require.Equal(t, "task:b", records[1].Source.AssociationID)
	require.Nil(t, records[1].Parent)
	require.Contains(t, reasons, "parent-member-unconfirmed")
}

func TestSession293ConflictingTaskAliasDoesNotPoisonDeclaredIdentity(t *testing.T) {
	frames := []string{
		relationshipInit293,
		creationTask("a", "outer", ""),
		creationTask("b", "outer", ""),
		`{"type":"assistant","session_id":"s","agent_id":"b","message":{"content":"independent child identity"}}`,
		`{"type":"assistant","session_id":"s","parent_tool_use_id":"outer","message":{"content":"original alias"}}`,
	}
	records, reasons := relationshipRecords(t, frames)
	require.Len(t, records, len(frames))
	require.Contains(t, reasons, "tool-alias-conflict")
	require.Equal(t, "task:b", records[3].Source.AssociationID)
	require.Empty(t, records[3].Source.ToolUseID)
	require.Equal(t, "task:a", records[4].Source.AssociationID)
	require.Equal(t, "outer", records[4].Source.ToolUseID)
}

func TestSession293ParentConflictsAndCyclesRemainDiagnostic(t *testing.T) {
	frames := []string{
		relationshipInit293,
		creationTask("a", "outer", ""),
		`{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent","parent_task_id":"a"}`,
		`{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","parent_task_id":"b"}`,
		creationRequest("inner", "Agent", `{}`, ""),
		`{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent","tool_use_id":"inner","parent_task_id":"a"}`,
	}
	records, reasons := relationshipRecords(t, frames)
	require.Len(t, records, len(frames))
	require.Contains(t, reasons, "parent-knowledge-rejected: lineage: cycle")
	require.Contains(t, reasons, "parent-knowledge-rejected: lineage: conflicting parent")
	require.Equal(t, "a", records[len(records)-1].Parent.TaskID)
}

func TestSession293ResumedTaskKeepsIdentityAcrossToolReferences(t *testing.T) {
	frames := []string{
		relationshipInit293,
		creationRequest("first", "Agent", `{}`, ""),
		creationTask("a", "first", "false"),
		creationRequest("resumed", "Agent", `{}`, ""),
		creationTask("a", "resumed", "true"),
		`{"type":"assistant","session_id":"s","agent_id":"a","parent_tool_use_id":"resumed","message":{"content":"new run"}}`,
		`{"type":"assistant","session_id":"s","agent_id":"a","parent_tool_use_id":"first","message":{"content":"late old run"}}`,
	}
	records, reasons := relationshipRecords(t, frames)
	require.Len(t, records, len(frames))
	require.NotContains(t, reasons, "tool-reference-conflict")
	for _, record := range records[4:] {
		require.Equal(t, "task:a", record.Source.AssociationID)
		require.Equal(t, "a", record.Source.TaskID)
		require.NotNil(t, record.Parent)
		require.Empty(t, record.Parent.TaskID)
	}
}

func TestSession293IdentityDoesNotBackfillOrCreateSnapshotMembers(t *testing.T) {
	early := `{"type":"assistant","session_id":"s","agent_id":"a","message":{"content":"before registration"}}`
	snapshot := `{"type":"system","subtype":"background_tasks_changed","session_id":"s","tasks":[{"task_id":"a","task_type":"local_agent"}]}`
	frames := []string{relationshipInit293, snapshot, early, creationTask("a", "outer", ""), early}
	records, _ := relationshipRecords(t, frames)
	require.Len(t, records, len(frames))
	require.Empty(t, records[2].Source.AssociationID)
	require.Equal(t, "task:a", records[4].Source.AssociationID)
	require.Equal(t, "outer", records[4].Source.ToolUseID)
	require.Equal(t, early, string(records[2].Raw))
}

func TestSession293RelationshipBoundsRetainNativeFacts(t *testing.T) {
	t.Run("depth", func(t *testing.T) {
		frames := []string{
			relationshipInit293,
			creationRequest("outer", "Agent", `{}`, ""),
			creationTask("a", "outer", ""),
			`{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent","parent_task_id":"a"}`,
			`{"type":"assistant","session_id":"s","agent_id":"b","message":{"content":"member without parent"}}`,
		}
		records, reasons := relationshipRecords(t, frames, func(options *SessionOptions) { options.Subagents.MaxDepth = 1 })
		require.Len(t, records, len(frames))
		require.Contains(t, reasons, "parent-knowledge-rejected: lineage: depth limit")
		require.Equal(t, "task:b", records[4].Source.AssociationID)
		require.Nil(t, records[4].Parent)
	})
	t.Run("tool aliases", func(t *testing.T) {
		frames := []string{relationshipInit293, creationTask("a", "first", ""), creationTask("a", "second", "")}
		records, reasons := relationshipRecords(t, frames, func(options *SessionOptions) { options.Subagents.MaxNodes = 1 })
		require.Len(t, records, len(frames))
		require.Contains(t, reasons, "tool-knowledge-limit")
		require.Equal(t, "task:a", records[2].Source.AssociationID)
	})
}

func TestSession293SuppressionKeepsOriginalPermissionAuthority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wire.jsonl")
	session := openSessionFixture(t, func(options *SessionOptions) {
		options.Env = append(options.Env, "CLAUDE_GO_TRANSCRIPT="+path)
		options.ChildDetailPolicy = ChildDetailSuppress
	})
	for _, frame := range []string{relationshipInit293, creationTask("a", "outer", "")} {
		submitFrame(t, session, frame)
		sessionNative(t, session)
	}
	request := sessionPermission(t, session, "child-request", "Read", `{"file_path":"original"}`)
	require.NoError(t, request.Approve(t.Context(), ApprovalOptions{}))
	require.ErrorIs(t, request.Approve(t.Context(), ApprovalOptions{}), ErrPermissionUsed)
	require.NoError(t, session.Interrupt(t.Context(), InterruptOptions{}))
	require.NoError(t, session.Close())
	responses := permissionResponses(t, permissionTranscript(t, path))
	require.Len(t, responses, 1)
	response, ok := responses[0]["response"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "child-request", response["request_id"])
}

func TestSessionSupportedChildDetailIdentity(t *testing.T) {
	for _, test := range []struct {
		version   string
		supported bool
	}{
		{"2.1.258", false},
		{"2.1.293", true},
		{"2.1.294", true},
		{"2.2.0", true},
		{"3.0.0", true},
		{"2.1.293+build.1", true},
		{"2.1.293-custom", false},
	} {
		for _, policy := range []ChildDetailPolicy{ChildDetailPreserve, ChildDetailSuppress} {
			t.Run(test.version+"/"+string(policy), func(t *testing.T) {
				detail := ` {"type":"assistant","session_id":"s","agent_id":"a","parent_tool_use_id":"outer","message":{"content":[{"type":"text","text":"child"}]}} `
				mixed := strings.Replace(detail, `"agent_id":"a"`, `"agent_id":"a","new_fact":true`, 1)
				frames := []string{
					fmt.Sprintf(`{"type":"system","subtype":"init","session_id":"s","claude_code_version":%q}`, test.version),
					creationRequest("outer", "Agent", `{}`, ""),
					creationTask("a", "outer", ""), detail, mixed,
					`{"type":"system","subtype":"task_notification","session_id":"s","task_id":"a","status":"completed"}`,
				}
				records, _ := relationshipRecords(t, frames, func(options *SessionOptions) { options.ChildDetailPolicy = policy })
				want := frames
				if test.supported && policy == ChildDetailSuppress {
					want = append(append([]string{}, frames[:3]...), frames[4:]...)
				}
				require.Len(t, records, len(want))
				for index, frame := range want {
					require.Equal(t, frame, string(records[index].Raw))
				}
			})
		}
	}
}
