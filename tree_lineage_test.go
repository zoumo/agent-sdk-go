// SPDX-License-Identifier: Apache-2.0

package claude_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	claude "github.com/zoumo/claude-agent-sdk-go"
)

// Read through the sent native frame and inspect the public derived records.
func treeFrame(t *testing.T, c *claude.TreeClient, frame string) ([]claude.TreeRecord, claude.TreeRecord) {
	t.Helper()
	treeEmit(t, c, frame)
	var derived []claude.TreeRecord
	for {
		r := treeReceive(t, c)
		if r.Kind == "SubagentCreated" {
			t.Fatal("first observation or resume is not creation")
		}
		if r.Kind == "Native" {
			if r.Native == nil || string(r.Native.Raw) != frame {
				t.Fatalf("raw/order: %+v", r)
			}
			return derived, r
		}
		derived = append(derived, r)
	}
}
func treeHas(records []claude.TreeRecord, kind, reason string) bool {
	for _, r := range records {
		if r.Kind == kind && r.Reason == reason {
			return true
		}
	}
	return false
}
func TestTreeModernRecursiveRunsAndLateFrames(t *testing.T) {
	c := openTreeFixture(t)
	treeFrame(t, c, `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"spawn-a","name":"Agent","input":{}}]}}`)
	ds, a := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","tool_use_id":"spawn-a","run_id":"001","is_backgrounded":false,"spawn_depth":1}`)
	if a.Scope != "child" || a.Source.TaskID != "a" || a.Source.AgentID != "" || a.Source.RunID != "001" || !treeHas(ds, "ObservationState", "new-run-observed") {
		t.Fatal(ds, a)
	}
	treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"a","message":{"content":[{"type":"tool_use","id":"spawn-b","name":"Agent","input":{}}]}}`)
	_, b := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent","parent_task_id":"a","run_id":"001","spawn_depth":2}`)
	if b.Scope != "child" || b.Parent == nil || b.Parent.TaskID != "a" || b.Parent.AgentID != "a" {
		t.Fatal(b)
	}
	_, bmsg := treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"b","message":{"content":"grandchild"}}`)
	if bmsg.Source.AssociationID != b.Source.AssociationID || bmsg.Parent.TaskID != "a" {
		t.Fatal(bmsg)
	}
	ds, newRun := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"002","is_backgrounded":true}`)
	if newRun.Source.AssociationID != a.Source.AssociationID || newRun.CurrentRunID != "002" || newRun.Stale || treeHas(ds, "SubagentAssociated", "first-observed") || !treeHas(ds, "ObservationState", "new-run-observed") {
		t.Fatal(ds, newRun)
	}
	for _, frame := range []string{
		`{"type":"system","subtype":"task_notification","session_id":"s","task_id":"a","run_id":"001","status":"completed"}`,
		`{"type":"system","subtype":"task_updated","session_id":"s","task_id":"a","run_id":"001","patch":{"status":"killed","future":null}}`,
	} {
		ds, late := treeFrame(t, c, frame)
		if !late.Stale || late.Source.RunID != "001" || late.CurrentRunID != "002" || treeHas(ds, "ObservationState", "new-run-observed") {
			t.Fatal(ds, late)
		}
	}
	_, legacy := treeFrame(t, c, `{"type":"system","subtype":"task_progress","session_id":"s","task_id":"a","description":"legacy progress"}`)
	if legacy.Source.RunID != "" || legacy.RunScope != "legacy-unknown" || legacy.CurrentRunID != "002" {
		t.Fatal(legacy)
	}
	// Same task ID in another session is a separate entity and run ordering scope.
	_, other := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"other","task_id":"a","task_type":"local_agent","run_id":"000"}`)
	if other.Stale || other.CurrentRunID != "000" || other.Scope != "unresolved" {
		t.Fatal(other)
	}
}
func TestTreeLegacyRecursiveToolOwnershipAndEnrichment(t *testing.T) {
	c := openTreeFixture(t)
	treeFrame(t, c, `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"outer","name":"Agent","input":{"resume":"existing"}}]}}`)
	_, child := treeFrame(t, c, `{"type":"assistant","session_id":"s","parent_tool_use_id":"outer","message":{"content":[{"type":"tool_use","id":"inner","name":"Agent","input":{}}]}}`)
	if child.Scope != "child" || child.Source.AgentID != "" || child.Source.TaskID != "" || child.Source.AssociationID != "tool:outer" {
		t.Fatal(child)
	}
	_, grand := treeFrame(t, c, `{"type":"stream_event","session_id":"s","parent_tool_use_id":"inner","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"same"}}}`)
	if grand.Scope != "child" || grand.Parent == nil || grand.Parent.AssociationID != child.Source.AssociationID || grand.Source.AgentID != "" {
		t.Fatal(grand)
	}
	ds, enriched := treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"a","parent_tool_use_id":"outer","message":{"content":"native identity"}}`)
	if enriched.Source.AssociationID != child.Source.AssociationID || enriched.Source.AgentID != "a" || enriched.Source.TaskID != "a" || !treeHas(ds, "SubagentAssociated", "identity-enriched") || treeHas(ds, "SubagentAssociated", "first-observed") {
		t.Fatal(ds, enriched)
	}
	_, grandAgain := treeFrame(t, c, `{"type":"assistant","session_id":"s","parent_tool_use_id":"inner","message":{"content":"after enrichment"}}`)
	if grandAgain.Parent.TaskID != "a" || grandAgain.Parent.AgentID != "a" || grandAgain.Source.AssociationID != grand.Source.AssociationID {
		t.Fatal(grandAgain)
	}
}
func TestTreeChildBeforeRelationUnknownParentAndNoDepthGuess(t *testing.T) {
	c := openTreeFixture(t)
	_, early := treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"child","parent_tool_use_id":"spawn","message":{"content":"early"}}`)
	if early.Scope != "unresolved" {
		t.Fatal(early)
	}
	_, ref := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"child","task_type":"local_agent","parent_task_id":"parent","spawn_depth":2}`)
	if ref.Scope != "unresolved" || ref.Parent == nil || ref.Parent.TaskID != "parent" {
		t.Fatal(ref)
	}
	ds, _ := treeFrame(t, c, `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"parent-tool","name":"Agent","input":{}}]}}`)
	if treeHas(ds, "SubagentAssociated", "identity-enriched") {
		t.Fatal("unrelated root tool guessed parent", ds)
	}
	ds, _ = treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"parent","task_type":"local_agent","tool_use_id":"parent-tool"}`)
	var ordered []string
	for _, r := range ds {
		if r.Kind == "SubagentAssociated" {
			ordered = append(ordered, r.Source.TaskID)
			if r.Scope != "child" {
				t.Fatal(r)
			}
		}
	}
	if !reflect.DeepEqual(ordered, []string{"parent", "child"}) {
		t.Fatal("ancestor must precede descendant enrichment", ordered)
	}
	_, after := treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"child","message":{"content":"after relation"}}`)
	if after.Scope != "child" || after.Parent.TaskID != "parent" || after.Source.AssociationID != early.Source.AssociationID {
		t.Fatal(after)
	}
}
func TestTreeNonAgentsHooksAndFailedSpawnNeverCreated(t *testing.T) {
	c := openTreeFixture(t)
	for _, kind := range []string{"local_bash", "local_workflow", "monitor", "mcp_task"} {
		ds, r := treeFrame(t, c, fmt.Sprintf(`{"type":"system","subtype":"task_started","session_id":"s","task_id":%q,"task_type":%q,"parent_task_id":"a","run_id":"001"}`, kind, kind))
		if len(ds) != 0 || r.Source.AgentID != "" || r.Source.TaskID != kind {
			t.Fatal(ds, r)
		}
		ds, _ = treeFrame(t, c, fmt.Sprintf(`{"type":"system","subtype":"task_updated","session_id":"s","task_id":%q,"run_id":"001","patch":{"status":"completed"}}`, kind))
		if treeHas(ds, "SubagentAssociated", "first-observed") {
			t.Fatal(ds)
		}
	}
	for _, frame := range []string{
		`{"type":"system","subtype":"hook_started","session_id":"s","hook_event":"SubagentStart","hook_id":"start"}`,
		`{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"failed","name":"Agent","input":{}}]}}`,
		`{"type":"user","session_id":"s","message":{"content":[{"type":"tool_result","tool_use_id":"failed","is_error":true,"content":"spawn failed"}]}}`,
	} {
		ds, _ := treeFrame(t, c, frame)
		if treeHas(ds, "SubagentAssociated", "first-observed") {
			t.Fatal(ds)
		}
	}
}
func TestTreeUUIDDedupFullScopeAndNoIDPreservation(t *testing.T) {
	c := openTreeFixture(t)
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
func TestTreeLineageConflictsDoNotRebind(t *testing.T) {
	c := openTreeFixture(t)
	treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","parent_task_id":"b"}`)
	ds, _ := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent","parent_task_id":"a"}`)
	if !treeHas(ds, "ObservationState", "lineage-cycle") {
		t.Fatal(ds)
	}
	ds, r := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","parent_task_id":"other"}`)
	if !treeHas(ds, "ObservationState", "parent-conflict") || r.Parent == nil || r.Parent.TaskID != "b" {
		t.Fatal(ds, r)
	}
}
func TestTreeAdditionalBounds(t *testing.T) {
	for _, opts := range []claude.TreeOptions{{MaxDepth: -1}, {MaxDepth: 257}, {MaxDedupEntries: -1}, {MaxDedupEntries: 131073}} {
		if c, err := claude.OpenTree(context.Background(), fixtureOptions(t, "tree"), opts); err == nil {
			c.Close()
			t.Fatal("invalid bounds", opts)
		}
	}
	for _, mode := range []string{"depth", "dedup"} {
		t.Run(mode, func(t *testing.T) {
			opts := claude.TreeOptions{MaxDepth: 1}
			if mode == "dedup" {
				opts = claude.TreeOptions{MaxDedupEntries: 1}
			}
			c, err := claude.OpenTree(context.Background(), fixtureOptions(t, "tree"), opts)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			treeReceive(t, c)
			treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","uuid":"a"}`)
			ds, _ := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent","parent_task_id":"a","uuid":"b"}`)
			reason := "depth-limit"
			if mode == "dedup" {
				reason = "dedup-limit"
			}
			if !treeHas(ds, "ObservationState", reason) {
				t.Fatal(ds)
			}
			for {
				_, err = c.Receive(context.Background())
				if err != nil {
					break
				}
			}
			if !errors.Is(err, claude.ErrResourceLimit) {
				t.Fatal(err)
			}
		})
	}
}
func TestTaskLineageFieldsLosslessNullAbsentEdits(t *testing.T) {
	for _, extra := range []string{``, `,"run_id":null,"parent_task_id":null,"spawn_depth":null,"is_backgrounded":null`, `,"run_id":"001","parent_task_id":"p","spawn_depth":2,"is_backgrounded":false`} {
		frame := `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","patch":{"future":null,"status":"running"},"unknown":42` + extra + `}`
		e, err := claude.ParseEvent([]byte(frame))
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		var a, b any
		json.Unmarshal([]byte(frame), &a)
		json.Unmarshal(encoded, &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%s => %s", frame, encoded)
		}
		run, parent, depth, bg := "002", "q", 3, true
		e.System.RunID = &run
		e.System.ParentTaskID = &parent
		e.System.SpawnDepth = &depth
		e.System.IsBackgrounded = &bg
		encoded, err = json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		var obj map[string]json.RawMessage
		json.Unmarshal(encoded, &obj)
		if string(obj["run_id"]) != `"002"` || string(obj["parent_task_id"]) != `"q"` || string(obj["spawn_depth"]) != "3" || string(obj["is_backgrounded"]) != "true" || string(obj["unknown"]) != "42" || string(obj["patch"]) != `{"future":null,"status":"running"}` {
			t.Fatal(string(encoded))
		}
	}
}

func TestTreeIndependentAliasesReconcileWithBridgeEvidence(t *testing.T) {
	c := openTreeFixture(t)
	// Agent identity on an unknown event is not yet proof that it equals task_id.
	_, agent := treeFrame(t, c, `{"type":"future_event","session_id":"s","agent_id":"a"}`)
	_, task := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"001"}`)
	if agent.Source.AssociationID == task.Source.AssociationID {
		t.Fatal("equal strings are not bridge evidence")
	}
	ds, merged := treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"a","message":{"content":"bridge"}}`)
	if !treeHas(ds, "SubagentAssociated", "identity-merged") || merged.Source.AssociationID != agent.Source.AssociationID || merged.Source.TaskID != "a" {
		t.Fatal(ds, merged)
	}
	found := false
	for _, r := range ds {
		if r.PreviousAssociationID == task.Source.AssociationID {
			found = true
		}
	}
	if !found {
		t.Fatal("caller cannot reconcile the previous association", ds)
	}
	ds, again := treeFrame(t, c, `{"type":"system","subtype":"task_progress","session_id":"s","task_id":"a","run_id":"001"}`)
	if len(ds) != 0 || again.Source.AssociationID != merged.Source.AssociationID {
		t.Fatal(ds, again)
	}
}
func TestTreeStaleMetadataCannotPoisonCurrentRun(t *testing.T) {
	c := openTreeFixture(t)
	treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"parent","task_type":"local_agent"}`)
	_, current := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","parent_task_id":"parent","run_id":"002"}`)
	ds, stale := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_bash","parent_task_id":"wrong","tool_use_id":"wrong-tool","run_id":"001"}`)
	if len(ds) != 0 || !stale.Stale || stale.Parent == nil || stale.Parent.TaskID != "parent" || stale.CurrentRunID != "002" {
		t.Fatal(ds, stale)
	}
	_, later := treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"a","message":{"content":"new work"}}`)
	if later.Source.AssociationID != current.Source.AssociationID || later.Parent.TaskID != "parent" {
		t.Fatal(later)
	}
}
func TestTreeKnownNonAgentToolDoesNotCreateLegacyAgent(t *testing.T) {
	c := openTreeFixture(t)
	treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"bash","task_type":"local_bash","tool_use_id":"bash-tool"}`)
	ds, r := treeFrame(t, c, `{"type":"assistant","session_id":"s","parent_tool_use_id":"bash-tool","message":{"content":"ambiguous"}}`)
	if len(ds) != 0 || r.Scope != "unresolved" || r.Source.AssociationID != "" || r.Reason != "non-agent-tool-reference" {
		t.Fatal(ds, r)
	}
}

func TestTreeConflictingToolAliasDoesNotPoisonTaskKey(t *testing.T) {
	c := openTreeFixture(t)
	_, a := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","tool_use_id":"same-tool"}`)
	ds, bad := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent","tool_use_id":"same-tool"}`)
	if !treeHas(ds, "ObservationState", "identity-conflict") || bad.Scope != "unresolved" {
		t.Fatal(ds, bad)
	}
	ds, b := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent"}`)
	if b.Source.TaskID != "b" || b.Source.AssociationID == a.Source.AssociationID || !treeHas(ds, "SubagentAssociated", "first-observed") {
		t.Fatal(ds, b)
	}
}
func TestTreeResumeNewToolAndDuplicateRelationships(t *testing.T) {
	c := openTreeFixture(t)
	treeFrame(t, c, `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"one","name":"Agent","input":{}},{"type":"tool_use","id":"two","name":"Agent","input":{"resume":"a"}}]}}`)
	_, a := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","tool_use_id":"one","run_id":"001"}`)
	ds, resumed := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","tool_use_id":"two","run_id":"002"}`)
	if resumed.Source.AssociationID != a.Source.AssociationID || resumed.Source.ToolUseID != "two" || resumed.CurrentRunID != "002" || treeHas(ds, "SubagentAssociated", "first-observed") {
		t.Fatal(ds, resumed)
	}
	ds, _ = treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","tool_use_id":"two","run_id":"002"}`)
	if len(ds) != 0 {
		t.Fatal("duplicate relationship/run produced derived records", ds)
	}
	// A partial and complete frame sharing a UUID occupy different event kinds.
	treeFrame(t, c, `{"type":"stream_event","session_id":"s","parent_tool_use_id":"two","uuid":"shared","event":{"type":"message_stop"}}`)
	treeFrame(t, c, `{"type":"assistant","session_id":"s","parent_tool_use_id":"two","uuid":"shared","message":{"content":"complete"}}`)
}

func TestTreeNoRunUUIDNotSuppressedAcrossObservedRunBoundary(t *testing.T) {
	c := openTreeFixture(t)
	treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"001"}`)
	frame := `{"type":"assistant","session_id":"s","agent_id":"a","uuid":"replayed","message":{"content":"work"}}`
	treeFrame(t, c, frame)
	treeEmit(t, c, frame)
	treeFrame(t, c, `{"type":"result","session_id":"s","result":"dedup barrier"}`)
	treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"002"}`)
	_, r := treeFrame(t, c, frame)
	if r.Source.RunID != "" || r.RunScope != "legacy-unknown" || r.CurrentRunID != "002" {
		t.Fatal(r)
	}
}
