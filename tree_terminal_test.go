// SPDX-License-Identifier: Apache-2.0
package claude_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"

	claude "github.com/zoumo/claude-agent-sdk-go"
	"github.com/zoumo/claude-agent-sdk-go/types"
)

func TestTreeRunTerminalsAndBackgroundLevel(t *testing.T) {
	c := openTreeFixture(t)
	treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"001","is_backgrounded":false}`)
	_, newer := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"002"}`)
	for _, status := range []string{"pending", "running", "paused", "future"} {
		ds, r := treeFrame(t, c, fmt.Sprintf(`{"type":"system","subtype":"task_updated","session_id":"s","task_id":"a","run_id":"002","patch":{"status":%q}}`, status))
		if r.RunStatus != status || r.RunTerminal || treeHas(ds, "ObservationState", "native-task-terminal") {
			t.Fatal(ds, r)
		}
	}
	ds, old := treeFrame(t, c, `{"type":"system","subtype":"task_updated","session_id":"s","task_id":"a","run_id":"001","patch":{"status":"killed"}}`)
	if !old.Stale || !old.RunTerminal || old.RunStatus != "killed" || old.CurrentRunTerminal || old.CurrentRunStatus != "future" || !treeHas(ds, "ObservationState", "native-task-terminal") {
		t.Fatal(ds, old)
	}
	for _, frame := range []string{
		`{"type":"system","subtype":"background_tasks_changed","session_id":"s","tasks":[]}`,
		`{"type":"system","subtype":"background_tasks_changed","session_id":"s","tasks":[{"task_id":"a","run_id":"001","task_type":"local_agent","ambient":true}]}`,
		`{"type":"system","subtype":"session_state_changed","session_id":"s","state":"idle"}`,
		`{"type":"result","session_id":"s","subtype":"success","result":"parent only"}`,
	} {
		ds, _ := treeFrame(t, c, frame)
		if len(ds) != 0 {
			t.Fatal(ds)
		}
	}
	_, progress := treeFrame(t, c, `{"type":"system","subtype":"task_progress","session_id":"s","task_id":"a","run_id":"002"}`)
	if progress.RunTerminal || progress.RunStatus != "future" || progress.Source.AssociationID != newer.Source.AssociationID {
		t.Fatal(progress)
	}
	ds, killed := treeFrame(t, c, `{"type":"system","subtype":"task_updated","session_id":"s","task_id":"a","run_id":"002","patch":{"status":"killed","is_backgrounded":true,"future":null}}`)
	if !killed.RunTerminal || !killed.CurrentRunTerminal || killed.RunStatus != "killed" || killed.RunBackgrounded == nil || !*killed.RunBackgrounded || !treeHas(ds, "ObservationState", "native-task-terminal") {
		t.Fatal(ds, killed)
	}
	for _, frame := range []string{
		`{"type":"system","subtype":"task_updated","session_id":"s","task_id":"a","run_id":"002","patch":{"status":"killed"}}`,
		`{"type":"system","subtype":"task_notification","session_id":"s","task_id":"a","run_id":"002","status":"stopped"}`,
		`{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"002"}`,
	} {
		ds, r := treeFrame(t, c, frame)
		if r.RunStatus != "killed" || !r.RunTerminal || treeHas(ds, "ObservationState", "native-task-terminal") {
			t.Fatal(ds, r)
		}
	}
	_, fresh := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"003"}`)
	if fresh.RunTerminal || fresh.RunStatus != "running" || fresh.Source.AssociationID != newer.Source.AssociationID {
		t.Fatal(fresh)
	}
	_, legacy := treeFrame(t, c, `{"type":"system","subtype":"task_notification","session_id":"s","task_id":"a","status":"completed"}`)
	if !legacy.RunTerminal || legacy.RunScope != "legacy-unknown" || legacy.CurrentRunTerminal || legacy.CurrentRunStatus != "running" {
		t.Fatal(legacy)
	}
}

func TestTreeTerminalKindsAndReceiveCancellation(t *testing.T) {
	for _, sub := range []string{"task_updated", "task_notification"} {
		statuses := []string{"completed", "failed", "killed"}
		if sub == "task_notification" {
			statuses = []string{"completed", "failed", "stopped"}
		}
		for _, status := range statuses {
			t.Run(sub+status, func(t *testing.T) {
				c := openTreeFixture(t)
				payload := fmt.Sprintf(`"status":%q`, status)
				if sub == "task_updated" {
					payload = `"patch":{` + payload + `}`
				}
				ds, r := treeFrame(t, c, fmt.Sprintf(`{"type":"system","subtype":%q,"session_id":"s","task_id":"unclassified","run_id":"1",%s}`, sub, payload))
				if !r.RunTerminal || r.RunStatus != status || !treeHas(ds, "ObservationState", "native-task-terminal") || treeHas(ds, "SubagentAssociated", "first-observed") {
					t.Fatal(ds, r)
				}
			})
		}
	}
	c := openTreeFixture(t)
	treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"1"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Receive(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
		t.Fatal("receive timeout stopped client")
	default:
	}
	_, r := treeFrame(t, c, `{"type":"system","subtype":"task_progress","session_id":"s","task_id":"a","run_id":"1"}`)
	if r.RunTerminal || r.RunStatus != "running" {
		t.Fatal(r)
	}
	treeEmit(t, c, "disconnect")
	end := treeReceive(t, c)
	if end.State != "incomplete" {
		t.Fatal(end)
	}
	if _, err := c.Receive(context.Background()); err == nil || errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func TestTreeHistorySeedAndFreshEpoch(t *testing.T) {
	opts := fixtureOptions(t, "tree")
	opts.ResumeID = "s"
	c, err := openWithTree(context.Background(), opts, claude.TreeOptions{MaxUnresolvedWait: time.Nanosecond, History: []claude.TreeSeed{{SessionID: "s", TaskID: "a"}, {SessionID: "s", TaskID: "b", ParentTaskID: "a"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	startup := treeReceive(t, c)
	for _, task := range []string{"a", "b"} {
		r := treeReceive(t, c)
		if r.Kind != "SubagentAssociated" || r.Reason != "history" || r.Provenance != "history" || r.Source.TaskID != task {
			t.Fatal(r)
		}
	}
	if r := treeReceive(t, c); r.Reason != "history-gap" || r.State != "incomplete" {
		t.Fatal(r)
	}
	ds, r := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"2"}`)
	if treeHas(ds, "SubagentAssociated", "first-observed") || r.Source.TaskID != "a" {
		t.Fatal(ds, r)
	}
	fresh := openTreeFixture(t)
	_, newRecord := treeFrame(t, fresh, `{"type":"assistant","session_id":"s","agent_id":"a","message":{"content":"fresh"}}`)
	if newRecord.Epoch == startup.Epoch {
		t.Fatal("epoch reused")
	}
}

func TestTreeRunLedgerBound(t *testing.T) {
	c, err := openWithTree(context.Background(), fixtureOptions(t, "tree"), claude.TreeOptions{MaxUnresolvedWait: time.Nanosecond, MaxRuns: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	treeReceive(t, c)
	treeFrame(t, c, `{"type":"system","subtype":"task_progress","session_id":"s","task_id":"a","run_id":"1"}`)
	treeEmit(t, c, `{"type":"system","subtype":"task_progress","session_id":"s","task_id":"a","run_id":"2"}`)
	r := treeReceive(t, c)
	if r.State != "incomplete" {
		t.Fatal(r)
	}
	treeReceive(t, c) // accepted native frame survives overflow
	for {
		_, err = c.Receive(context.Background())
		if err != nil {
			break
		}
	}
	if !errors.Is(err, claude.ErrResourceLimit) {
		t.Fatal(err)
	}
}

func TestSessionStateAndBackgroundSnapshotCodec(t *testing.T) {
	for _, raw := range []string{
		`{"type":"system","subtype":"session_state_changed","state":"idle","future":null}`,
		`{"type":"system","subtype":"session_state_changed","state":"requires_action"}`,
		`{"type":"system","subtype":"session_state_changed","state":"running"}`,
		`{"type":"system","subtype":"session_state_changed","state":"future"}`,
		`{"type":"system","subtype":"session_state_changed","state":null}`,
		`{"type":"system","subtype":"background_tasks_changed","tasks":[]}`,
		`{"type":"system","subtype":"background_tasks_changed","tasks":null}`,
		`{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"a","run_id":null,"task_type":"local_agent","ambient":false,"future":42}]}`,
	} {
		var event types.SystemEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatal(err)
		}
		var wire struct {
			State *string           `json:"state"`
			Tasks []json.RawMessage `json:"tasks"`
		}
		json.Unmarshal([]byte(raw), &wire)
		if !reflect.DeepEqual(event.State, wire.State) || !reflect.DeepEqual(event.Tasks, wire.Tasks) {
			t.Fatal(event)
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var a, b any
		json.Unmarshal([]byte(raw), &a)
		json.Unmarshal(encoded, &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatal(raw, string(encoded))
		}
	}
	var e types.SystemEvent
	json.Unmarshal([]byte(`{"subtype":"session_state_changed","state":"idle","future":null}`), &e)
	state := "running"
	e.State = &state
	encoded, _ := json.Marshal(e)
	var fields map[string]json.RawMessage
	json.Unmarshal(encoded, &fields)
	if string(fields["state"]) != `"running"` || string(fields["future"]) != "null" {
		t.Fatal(string(encoded))
	}
}

func TestTreeUnclassifiedRunCannotRegressAgentMetadata(t *testing.T) {
	c := openTreeFixture(t)
	treeFrame(t, c, `{"type":"system","subtype":"task_progress","session_id":"s","task_id":"a","run_id":"003"}`)
	treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"001","tool_use_id":"original"}`)
	ds, r := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"002","tool_use_id":"late"}`)
	if !r.Stale || r.CurrentRunID != "003" || r.Source.ToolUseID != "original" || treeHas(ds, "SubagentAssociated", "identity-enriched") {
		t.Fatal(ds, r)
	}
}
