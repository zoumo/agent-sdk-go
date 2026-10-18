// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Read through the sent native frame and inspect the public derived records.
func treeFrame(t *testing.T, c *Client, frame string) ([]relationshipRecord, relationshipRecord) {
	t.Helper()
	treeEmit(t, c, frame)
	var derived []relationshipRecord
	for {
		r := treeReceive(t, c)
		require.NotEqualValues(t, "SubagentCreated", r.Kind, "first observation or resume is not creation")
		if r.Kind == "Native" {
			require.Falsef(t, r.Native == nil || string(r.Native.Raw) != frame, "raw/order: %+v", r)
			return derived, r
		}
		derived = append(derived, r)
	}
}
func treeHas(records []relationshipRecord, kind, reason string) bool {
	for _, r := range records {
		if r.Kind == kind && r.Reason == reason {
			return true
		}
	}
	return false
}
func (s *treeObservationSuite) TestTreeModernRecursiveRunsAndLateFrames() {
	t := s.T()
	c := s.client
	treeFrame(t, c, `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"spawn-a","name":"Agent","input":{}}]}}`)
	ds, a := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","tool_use_id":"spawn-a","run_id":"001","is_backgrounded":false,"spawn_depth":1}`)
	require.False(t, a.Scope != "child" || a.Source.TaskID != "a" || a.Source.AgentID != "" || a.Source.RunID != "001" || !treeHas(ds, "ObservationState", "new-run-observed"), ds, a)
	treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"a","message":{"content":[{"type":"tool_use","id":"spawn-b","name":"Agent","input":{}}]}}`)
	_, b := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent","parent_task_id":"a","run_id":"001","spawn_depth":2}`)
	require.False(t, b.Scope != "child" || b.Parent == nil || b.Parent.TaskID != "a" || b.Parent.AgentID != "a", b)
	_, bmsg := treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"b","message":{"content":"grandchild"}}`)
	require.False(t, bmsg.Source.AssociationID != b.Source.AssociationID || bmsg.Parent.TaskID != "a", bmsg)
	ds, newRun := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"002","is_backgrounded":true}`)
	require.False(t, newRun.Source.AssociationID != a.Source.AssociationID || newRun.CurrentRunID != "002" || newRun.Stale || treeHas(ds, "SubagentAssociated", "first-observed") || !treeHas(ds, "ObservationState", "new-run-observed"), ds, newRun)
	for _, frame := range []string{
		`{"type":"system","subtype":"task_notification","session_id":"s","task_id":"a","run_id":"001","status":"completed"}`,
		`{"type":"system","subtype":"task_updated","session_id":"s","task_id":"a","run_id":"001","patch":{"status":"killed","future":null}}`,
	} {
		ds, late := treeFrame(t, c, frame)
		require.False(t, !late.Stale || late.Source.RunID != "001" || late.CurrentRunID != "002" || treeHas(ds, "ObservationState", "new-run-observed"), ds, late)
	}
	_, legacy := treeFrame(t, c, `{"type":"system","subtype":"task_progress","session_id":"s","task_id":"a","description":"legacy progress"}`)
	require.False(t, legacy.Source.RunID != "" || legacy.RunScope != "legacy-unknown" || legacy.CurrentRunID != "002", legacy)
	// Same task ID in another session is a separate entity and run ordering scope.
	_, other := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"other","task_id":"a","task_type":"local_agent","run_id":"000"}`)
	require.False(t, other.Stale || other.CurrentRunID != "000" || other.Scope != "unresolved", other)
}
func (s *treeObservationSuite) TestTreeLegacyRecursiveToolOwnershipAndEnrichment() {
	t := s.T()
	c := s.client
	treeFrame(t, c, `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"outer","name":"Agent","input":{"resume":"existing"}}]}}`)
	_, child := treeFrame(t, c, `{"type":"assistant","session_id":"s","parent_tool_use_id":"outer","message":{"content":[{"type":"tool_use","id":"inner","name":"Agent","input":{}}]}}`)
	require.False(t, child.Scope != "child" || child.Source.AgentID != "" || child.Source.TaskID != "" || child.Source.AssociationID != "tool:outer", child)
	_, grand := treeFrame(t, c, `{"type":"stream_event","session_id":"s","parent_tool_use_id":"inner","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"same"}}}`)
	require.False(t, grand.Scope != "child" || grand.Parent == nil || grand.Parent.AssociationID != child.Source.AssociationID || grand.Source.AgentID != "", grand)
	ds, enriched := treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"a","parent_tool_use_id":"outer","message":{"content":"native identity"}}`)
	require.False(t, enriched.Source.AssociationID != child.Source.AssociationID || enriched.Source.AgentID != "a" || enriched.Source.TaskID != "a" || !treeHas(ds, "SubagentAssociated", "identity-enriched") || treeHas(ds, "SubagentAssociated", "first-observed"), ds, enriched)
	_, grandAgain := treeFrame(t, c, `{"type":"assistant","session_id":"s","parent_tool_use_id":"inner","message":{"content":"after enrichment"}}`)
	require.False(t, grandAgain.Parent.TaskID != "a" || grandAgain.Parent.AgentID != "a" || grandAgain.Source.AssociationID != grand.Source.AssociationID, grandAgain)
}
func (s *treeObservationSuite) TestTreeChildBeforeRelationUnknownParentAndNoDepthGuess() {
	t := s.T()
	c := s.client
	_, early := treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"child","parent_tool_use_id":"spawn","message":{"content":"early"}}`)
	require.EqualValues(t, "unresolved", early.Scope, early)
	_, ref := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"child","task_type":"local_agent","parent_task_id":"parent","spawn_depth":2}`)
	require.False(t, ref.Scope != "unresolved" || ref.Parent == nil || ref.Parent.TaskID != "parent", ref)
	ds, _ := treeFrame(t, c, `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"parent-tool","name":"Agent","input":{}}]}}`)
	require.False(t, treeHas(ds, "SubagentAssociated", "identity-enriched"), "unrelated root tool guessed parent", ds)
	ds, _ = treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"parent","task_type":"local_agent","tool_use_id":"parent-tool"}`)
	var ordered []string
	for _, r := range ds {
		if r.Kind == "SubagentAssociated" {
			ordered = append(ordered, r.Source.TaskID)
			require.EqualValues(t, "child", r.Scope, r)
		}
	}
	require.True(t, reflect.DeepEqual(ordered, []string{"parent", "child"}), "ancestor must precede descendant enrichment", ordered)
	_, after := treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"child","message":{"content":"after relation"}}`)
	require.False(t, after.Scope != "child" || after.Parent.TaskID != "parent" || after.Source.AssociationID != early.Source.AssociationID, after)
}
func (s *treeObservationSuite) TestTreeNonAgentsHooksAndFailedSpawnNeverCreated() {
	t := s.T()
	c := s.client
	for _, kind := range []string{"local_bash", "local_workflow", "monitor", "mcp_task"} {
		ds, r := treeFrame(t, c, fmt.Sprintf(`{"type":"system","subtype":"task_started","session_id":"s","task_id":%q,"task_type":%q,"parent_task_id":"a","run_id":"001"}`, kind, kind))
		require.False(t, len(ds) != 0 || r.Source.AgentID != "" || r.Source.TaskID != kind, ds, r)
		ds, _ = treeFrame(t, c, fmt.Sprintf(`{"type":"system","subtype":"task_updated","session_id":"s","task_id":%q,"run_id":"001","patch":{"status":"completed"}}`, kind))
		require.False(t, treeHas(ds, "SubagentAssociated", "first-observed"), ds)
	}
	for _, frame := range []string{
		`{"type":"system","subtype":"hook_started","session_id":"s","hook_event":"SubagentStart","hook_id":"start"}`,
		`{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"failed","name":"Agent","input":{}}]}}`,
		`{"type":"user","session_id":"s","message":{"content":[{"type":"tool_result","tool_use_id":"failed","is_error":true,"content":"spawn failed"}]}}`,
	} {
		ds, _ := treeFrame(t, c, frame)
		require.False(t, treeHas(ds, "SubagentAssociated", "first-observed"), ds)
	}
}
func (s *treeObservationSuite) TestTreeUUIDDedupFullScopeAndNoIDPreservation() {
	t := s.T()
	c := s.client
	base := `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"001","uuid":"same"}`
	treeFrame(t, c, base)
	treeEmit(t, c, base)
	// Duplicate is suppressed; this next distinct event is observable immediately.
	treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"002","uuid":"same"}`)
	for _, frame := range []string{
		`{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent","run_id":"002","uuid":"same"}`,
		`{"type":"system","subtype":"task_started","session_id":"other","task_id":"a","task_type":"local_agent","run_id":"002","uuid":"same"}`,
		`{"type":"system","subtype":"task_progress","session_id":"s","task_id":"a","run_id":"002","uuid":"same"}`,
		`{"type":"assistant","session_id":"s","agent_id":"a","uuid":"same","message":{"content":"full"}}`,
		`{"type":"stream_event","session_id":"s","parent_tool_use_id":"legacy","uuid":"same","event":{"type":"content_block_delta","delta":{"text":"partial"}}}`,
	} {
		treeFrame(t, c, frame)
	}
	for i := 0; i < 2; i++ {
		treeFrame(t, c, `{"type":"stream_event","session_id":"s","parent_tool_use_id":"legacy","event":{"type":"content_block_delta","delta":{"text":"same"}}}`)
	}
	// Unknown contracts do not gain a dedup promise merely by containing uuid.
	for i := 0; i < 2; i++ {
		treeFrame(t, c, `{"type":"future_event","session_id":"s","agent_id":"a","uuid":"same","unknown":null}`)
	}
}
func (s *treeObservationSuite) TestTreeLineageConflictsDoNotRebind() {
	t := s.T()
	c := s.client
	treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","parent_task_id":"b"}`)
	ds, _ := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent","parent_task_id":"a"}`)
	require.True(t, treeHas(ds, "ObservationState", "lineage-cycle"), ds)
	ds, r := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","parent_task_id":"other"}`)
	require.False(t, !treeHas(ds, "ObservationState", "parent-conflict") || r.Parent == nil || r.Parent.TaskID != "b", ds, r)
}
func TestTreeAdditionalBounds(t *testing.T) {
	for _, opts := range []SubagentOptions{{MaxDepth: -1}, {MaxDepth: 257}, {MaxDedupEntries: -1}, {MaxDedupEntries: 131073}} {
		if c, err := openWithTree(context.Background(), fixtureOptions(t, "tree"), opts); err == nil {
			c.Close()
			require.FailNow(t, "verification failed", "invalid bounds", opts)
		}
	}
	for _, mode := range []string{"depth", "dedup"} {
		t.Run(mode, func(t *testing.T) {
			opts := SubagentOptions{MaxUnresolvedWait: time.Nanosecond, MaxDepth: 1}
			if mode == "dedup" {
				opts = SubagentOptions{MaxUnresolvedWait: time.Nanosecond, MaxDedupEntries: 1}
			}
			c, err := openWithTree(context.Background(), fixtureOptions(t, "tree"), opts)
			require.NoError(t, err)
			defer c.Abort()
			treeReceive(t, c)
			treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","uuid":"a"}`)
			ds, _ := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent","parent_task_id":"a","uuid":"b"}`)
			reason := "depth-limit"
			if mode == "dedup" {
				reason = "dedup-limit"
			}
			require.True(t, treeHas(ds, "ObservationState", reason), ds)
			for {
				_, err = c.tree.receive(context.Background())
				if err != nil {
					break
				}
			}
			require.ErrorIs(t, err, ErrResourceLimit, err)
		})
	}
}
func TestTaskLineageFieldsLosslessNullAbsentEdits(t *testing.T) {
	for _, extra := range []string{``, `,"run_id":null,"parent_task_id":null,"spawn_depth":null,"is_backgrounded":null`, `,"run_id":"001","parent_task_id":"p","spawn_depth":2,"is_backgrounded":false`} {
		frame := `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","patch":{"future":null,"status":"running"},"unknown":42` + extra + `}`
		e, err := ParseEvent([]byte(frame))
		require.NoError(t, err)
		encoded, err := json.Marshal(e)
		require.NoError(t, err)
		var a, b any
		json.Unmarshal([]byte(frame), &a)
		json.Unmarshal(encoded, &b)
		require.Truef(t, reflect.DeepEqual(a, b), "%s => %s", frame, encoded)
		run, parent, depth, bg := "002", "q", 3, true
		e.System.RunID = &run
		e.System.ParentTaskID = &parent
		e.System.SpawnDepth = &depth
		e.System.IsBackgrounded = &bg
		encoded, err = json.Marshal(e)
		require.NoError(t, err)
		var obj map[string]json.RawMessage
		json.Unmarshal(encoded, &obj)
		require.False(t, string(obj["run_id"]) != `"002"` || string(obj["parent_task_id"]) != `"q"` || string(obj["spawn_depth"]) != "3" || string(obj["is_backgrounded"]) != "true" || string(obj["unknown"]) != "42" || string(obj["patch"]) != `{"future":null,"status":"running"}`, string(encoded))
	}
}

func (s *treeObservationSuite) TestTreeIndependentAliasesReconcileWithBridgeEvidence() {
	t := s.T()
	c := s.client
	// Agent identity on an unknown event is not yet proof that it equals task_id.
	_, agent := treeFrame(t, c, `{"type":"future_event","session_id":"s","agent_id":"a"}`)
	_, task := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"001"}`)
	require.NotEqualValues(t, task.Source.AssociationID, agent.Source.AssociationID, "equal strings are not bridge evidence")
	ds, merged := treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"a","message":{"content":"bridge"}}`)
	require.False(t, !treeHas(ds, "SubagentAssociated", "identity-merged") || merged.Source.AssociationID != agent.Source.AssociationID || merged.Source.TaskID != "a", ds, merged)
	found := false
	for _, r := range ds {
		if r.PreviousAssociationID == task.Source.AssociationID {
			found = true
		}
	}
	require.True(t, found, "caller cannot reconcile the previous association", ds)
	ds, again := treeFrame(t, c, `{"type":"system","subtype":"task_progress","session_id":"s","task_id":"a","run_id":"001"}`)
	require.False(t, !treeOnlyUnresolved(ds) || again.Source.AssociationID != merged.Source.AssociationID, ds, again)
}
func (s *treeObservationSuite) TestTreeStaleMetadataCannotPoisonCurrentRun() {
	t := s.T()
	c := s.client
	treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"parent","task_type":"local_agent"}`)
	_, current := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","parent_task_id":"parent","run_id":"002"}`)
	ds, stale := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_bash","parent_task_id":"wrong","tool_use_id":"wrong-tool","run_id":"001"}`)
	require.False(t, !treeOnlyUnresolved(ds) || !stale.Stale || stale.Parent == nil || stale.Parent.TaskID != "parent" || stale.CurrentRunID != "002", ds, stale)
	_, later := treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"a","message":{"content":"new work"}}`)
	require.False(t, later.Source.AssociationID != current.Source.AssociationID || later.Parent.TaskID != "parent", later)
}
func (s *treeObservationSuite) TestTreeKnownNonAgentToolDoesNotCreateLegacyAgent() {
	t := s.T()
	c := s.client
	treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"bash","task_type":"local_bash","tool_use_id":"bash-tool"}`)
	ds, r := treeFrame(t, c, `{"type":"assistant","session_id":"s","parent_tool_use_id":"bash-tool","message":{"content":"ambiguous"}}`)
	require.False(t, !treeOnlyUnresolved(ds) || r.Scope != "unresolved" || r.Source.AssociationID != "" || r.Reason != "unresolved-source", ds, r)
}

func (s *treeObservationSuite) TestTreeConflictingToolAliasDoesNotPoisonTaskKey() {
	t := s.T()
	c := s.client
	_, a := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","tool_use_id":"same-tool"}`)
	ds, bad := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent","tool_use_id":"same-tool"}`)
	require.False(t, !treeHas(ds, "ObservationState", "identity-conflict") || bad.Scope != "unresolved", ds, bad)
	ds, b := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent"}`)
	require.False(t, b.Source.TaskID != "b" || b.Source.AssociationID == a.Source.AssociationID || !treeHas(ds, "SubagentAssociated", "first-observed"), ds, b)
}
func (s *treeObservationSuite) TestTreeResumeNewToolAndDuplicateRelationships() {
	t := s.T()
	c := s.client
	treeFrame(t, c, `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"one","name":"Agent","input":{}},{"type":"tool_use","id":"two","name":"Agent","input":{"resume":"a"}}]}}`)
	_, a := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","tool_use_id":"one","run_id":"001"}`)
	ds, resumed := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","tool_use_id":"two","run_id":"002"}`)
	require.False(t, resumed.Source.AssociationID != a.Source.AssociationID || resumed.Source.ToolUseID != "two" || resumed.CurrentRunID != "002" || treeHas(ds, "SubagentAssociated", "first-observed"), ds, resumed)
	ds, _ = treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","tool_use_id":"two","run_id":"002"}`)
	require.True(t, treeOnlyUnresolved(ds), "duplicate relationship/run produced derived records", ds)
	// A partial and complete frame sharing a UUID occupy different event kinds.
	treeFrame(t, c, `{"type":"stream_event","session_id":"s","parent_tool_use_id":"two","uuid":"shared","event":{"type":"message_stop"}}`)
	treeFrame(t, c, `{"type":"assistant","session_id":"s","parent_tool_use_id":"two","uuid":"shared","message":{"content":"complete"}}`)
}

func (s *treeObservationSuite) TestTreeNoRunUUIDNotSuppressedAcrossObservedRunBoundary() {
	t := s.T()
	c := s.client
	treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"001"}`)
	frame := `{"type":"assistant","session_id":"s","agent_id":"a","uuid":"replayed","message":{"content":"work"}}`
	treeFrame(t, c, frame)
	treeEmit(t, c, frame)
	treeFrame(t, c, `{"type":"result","session_id":"s","result":"dedup barrier"}`)
	treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"002"}`)
	_, r := treeFrame(t, c, frame)
	require.False(t, r.Source.RunID != "" || r.RunScope != "legacy-unknown" || r.CurrentRunID != "002", r)
}

// Old lineage cases may now include the explicit association-wait diagnostic.
func treeOnlyUnresolved(records []relationshipRecord) bool {
	for _, r := range records {
		if r.Kind != "ObservationState" || r.Reason != "unresolved-source" {
			return false
		}
	}
	return true
}
