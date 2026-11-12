package e2e_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/claude/types"
)

func TestNativeSubagentRelationship293(t *testing.T) {
	cliPath, model, env := assignedSessionLiveEnvironment(t)
	env = slices.DeleteFunc(env, func(value string) bool { return value == "CLAUDE_CODE_SIMPLE=1" })
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	forward := true
	session, err := claude.Open(ctx, claude.SessionOptions{
		CLIPath: cliPath, Model: model, Env: env, WorkDir: t.TempDir(), ClaimSession: true,
		Tools: []string{"Agent"}, StrictMCPConfig: true,
		Initialize: &types.InitializeOptions{
			ForwardSubagentText: &forward,
			Agents:              json.RawMessage(`{"sdk-relation-check":{"description":"Return a fixed marker for the SDK relationship check","prompt":"Reply SDK_CHILD_RELATION_OK only. Do not use tools.","tools":[]}}`),
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	submission, err := session.Submit(ctx, claude.Input{
		Text: "Use the Task tool (also called Agent) exactly once with subagent_type sdk-relation-check, description SDK relationship check, and prompt Reply SDK_CHILD_RELATION_OK only. Run it in the foreground and wait for its result. Then reply SDK_PARENT_RELATION_OK only.",
	})
	require.NoError(t, err)
	require.True(t, submission.Written)
	var taskID string
	seenChild, seenParent, seenMember, seenResult, seenChildResult := false, false, false, false, false
	for !seenResult {
		record, err := session.Receive(ctx)
		require.NoError(t, err)
		if record.Request != nil {
			require.Contains(t, []string{"Agent", "Task"}, record.Request.Handle().ToolName())
			require.NoError(t, record.Request.Approve(ctx, claude.ApprovalOptions{}))
		}
		if record.Observation != nil {
			t.Logf("observation reason=%s evidence=%s task=%s", record.Observation.Reason, record.Observation.Evidence, record.Observation.Source.TaskID)
			require.Contains(t, []string{"member-confirmed", "parent-confirmed"}, record.Observation.Reason)
			if record.Observation.Reason == "member-confirmed" {
				seenMember = true
			}
			continue
		}
		event := record.Event
		if event == nil {
			continue
		}
		if event.System != nil && event.Subtype == "init" {
			require.Equal(t, "2.1.293", event.System.ClaudeCodeVersion)
			t.Logf("native tools=%v", event.System.Tools)
			require.True(t, slices.Contains(event.System.Tools, "Agent") || slices.Contains(event.System.Tools, "Task"))
		}
		if event.System != nil && event.Subtype == "task_started" {
			taskID = event.System.TaskID
			t.Logf("task_started task=%s tool=%s source=%+v parent_source=%+v", taskID, record.Source.ToolUseID, record.Source, record.Parent)
		}
		if event.Assistant != nil && event.AgentID != nil {
			t.Logf("forwarded assistant agent=%s source=%+v parent=%+v", *event.AgentID, record.Source, record.Parent)
			require.NotEmpty(t, taskID)
			require.Equal(t, taskID, *event.AgentID)
			require.Equal(t, taskID, record.Source.TaskID)
			require.NotEmpty(t, record.Source.AssociationID)
			require.NotNil(t, record.Parent)
			require.Empty(t, record.Parent.TaskID)
			seenChild = seenChild || strings.Contains(string(event.Assistant.Content.Raw), "SDK_CHILD_RELATION_OK")
		}
		if event.Assistant != nil && (event.ParentToolUseID == nil || *event.ParentToolUseID == "") && event.AgentID == nil {
			seenParent = seenParent || strings.Contains(string(event.Assistant.Content.Raw), "SDK_PARENT_RELATION_OK")
		}
		if event.User != nil && len(event.ToolUseResult) > 0 {
			var output struct {
				Status  string `json:"status"`
				AgentID string `json:"agentId"`
			}
			require.NoError(t, json.Unmarshal(event.ToolUseResult, &output))
			if output.Status == "completed" {
				require.Equal(t, taskID, output.AgentID)
				seenChildResult = true
			}
		}
		if event.Result != nil {
			require.False(t, event.Result.IsError)
			seenResult = true
		}
	}
	require.NotEmpty(t, taskID, "the model did not start the required subagent")
	require.True(t, seenMember)
	require.True(t, seenChild, "no forwarded child assistant with agent_id was observed")
	require.True(t, seenParent)
	require.True(t, seenChildResult, "the spawning tool did not report child completion")
	require.NoError(t, session.Close())
	require.NoError(t, session.Err())
}
