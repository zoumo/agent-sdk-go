// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude/types"
)

func (s *treeObservationSuite) TestTreeRunTerminalsAndBackgroundLevel() {
	t := s.T()
	c := s.client
	treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"001","is_backgrounded":false}`)
	_, newer := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"002"}`)
	for _, status := range []string{"pending", "running", "paused", "future"} {
		ds, r := treeFrame(t, c, fmt.Sprintf(`{"type":"system","subtype":"task_updated","session_id":"s","task_id":"a","run_id":"002","patch":{"status":%q}}`, status))
		require.False(t, r.RunStatus != status || r.RunTerminal || treeHas(ds, "ObservationState", "native-task-terminal"), ds, r)
	}
	ds, old := treeFrame(t, c, `{"type":"system","subtype":"task_updated","session_id":"s","task_id":"a","run_id":"001","patch":{"status":"killed"}}`)
	require.False(t, !old.Stale || !old.RunTerminal || old.RunStatus != "killed" || old.CurrentRunTerminal || old.CurrentRunStatus != "future" || !treeHas(ds, "ObservationState", "native-task-terminal"), ds, old)
	for _, frame := range []string{
		`{"type":"system","subtype":"background_tasks_changed","session_id":"s","tasks":[]}`,
		`{"type":"system","subtype":"background_tasks_changed","session_id":"s","tasks":[{"task_id":"a","run_id":"001","task_type":"local_agent","ambient":true}]}`,
		`{"type":"system","subtype":"session_state_changed","session_id":"s","state":"idle"}`,
		`{"type":"result","session_id":"s","subtype":"success","result":"parent only"}`,
	} {
		ds, _ := treeFrame(t, c, frame)
		require.EqualValues(t, 0, len(ds), ds)
	}
	_, progress := treeFrame(t, c, `{"type":"system","subtype":"task_progress","session_id":"s","task_id":"a","run_id":"002"}`)
	require.False(t, progress.RunTerminal || progress.RunStatus != "future" || progress.Source.AssociationID != newer.Source.AssociationID, progress)
	ds, killed := treeFrame(t, c, `{"type":"system","subtype":"task_updated","session_id":"s","task_id":"a","run_id":"002","patch":{"status":"killed","is_backgrounded":true,"future":null}}`)
	require.False(t, !killed.RunTerminal || !killed.CurrentRunTerminal || killed.RunStatus != "killed" || killed.RunBackgrounded == nil || !*killed.RunBackgrounded || !treeHas(ds, "ObservationState", "native-task-terminal"), ds, killed)
	for _, frame := range []string{
		`{"type":"system","subtype":"task_updated","session_id":"s","task_id":"a","run_id":"002","patch":{"status":"killed"}}`,
		`{"type":"system","subtype":"task_notification","session_id":"s","task_id":"a","run_id":"002","status":"stopped"}`,
		`{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"002"}`,
	} {
		ds, r := treeFrame(t, c, frame)
		require.False(t, r.RunStatus != "killed" || !r.RunTerminal || treeHas(ds, "ObservationState", "native-task-terminal"), ds, r)
	}
	_, fresh := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"003"}`)
	require.False(t, fresh.RunTerminal || fresh.RunStatus != "running" || fresh.Source.AssociationID != newer.Source.AssociationID, fresh)
	_, legacy := treeFrame(t, c, `{"type":"system","subtype":"task_notification","session_id":"s","task_id":"a","status":"completed"}`)
	require.False(t, !legacy.RunTerminal || legacy.RunScope != "legacy-unknown" || legacy.CurrentRunTerminal || legacy.CurrentRunStatus != "running", legacy)
}

func (s *treeObservationSuite) TestTreeTerminalKindsAndReceiveCancellation() {
	t := s.T()
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
				require.False(t, !r.RunTerminal || r.RunStatus != status || !treeHas(ds, "ObservationState", "native-task-terminal") || treeHas(ds, "SubagentAssociated", "first-observed"), ds, r)
			})
		}
	}
	c := openTreeFixture(t)
	treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"1"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	{
		_, err := c.tree.receive(ctx)
		require.ErrorIs(t, err, context.DeadlineExceeded, err)
	}
	select {
	case <-c.Done():
		require.FailNow(t, "verification failed", "receive timeout stopped client")
	default:
	}
	_, r := treeFrame(t, c, `{"type":"system","subtype":"task_progress","session_id":"s","task_id":"a","run_id":"1"}`)
	require.False(t, r.RunTerminal || r.RunStatus != "running", r)
	treeEmit(t, c, "disconnect")
	end := treeReceive(t, c)
	require.EqualValues(t, "incomplete", end.State, end)
	{
		_, err := c.tree.receive(context.Background())
		require.False(t, err == nil || errors.Is(err, io.EOF), err)
	}
}

func (s *treeObservationSuite) TestTreeHistorySeedAndFreshEpoch() {
	t := s.T()
	opts := fixtureOptions(t, "tree")
	opts.ResumeID = "s"
	c, err := openWithTree(context.Background(), opts, SubagentOptions{MaxUnresolvedWait: time.Nanosecond, History: []SubagentHistory{{SessionID: "s", TaskID: "a"}, {SessionID: "s", TaskID: "b", ParentTaskID: "a"}}})
	require.NoError(t, err)
	defer c.Abort()
	startup := treeReceive(t, c)
	for _, task := range []string{"a", "b"} {
		r := treeReceive(t, c)
		require.False(t, r.Kind != "SubagentAssociated" || r.Reason != "history" || r.Provenance != "history" || r.Source.TaskID != task, r)
	}
	{
		r := treeReceive(t, c)
		require.False(t, r.Reason != "history-gap" || r.State != "incomplete", r)
	}
	ds, r := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"2"}`)
	require.False(t, treeHas(ds, "SubagentAssociated", "first-observed") || r.Source.TaskID != "a", ds, r)
	fresh := openTreeFixture(t)
	_, newRecord := treeFrame(t, fresh, `{"type":"assistant","session_id":"s","agent_id":"a","message":{"content":"fresh"}}`)
	require.NotEqualValues(t, startup.Epoch, newRecord.Epoch, "epoch reused")
}

func (s *treeObservationSuite) TestTreeRunLedgerBound() {
	t := s.T()
	c, err := openWithTree(context.Background(), fixtureOptions(t, "tree"), SubagentOptions{MaxUnresolvedWait: time.Nanosecond, MaxRuns: 1})
	require.NoError(t, err)
	defer c.Abort()
	treeReceive(t, c)
	treeFrame(t, c, `{"type":"system","subtype":"task_progress","session_id":"s","task_id":"a","run_id":"1"}`)
	treeEmit(t, c, `{"type":"system","subtype":"task_progress","session_id":"s","task_id":"a","run_id":"2"}`)
	r := treeReceive(t, c)
	require.EqualValues(t, "incomplete", r.State, r)
	treeReceive(t, c) // accepted native frame survives overflow
	for {
		_, err = c.tree.receive(context.Background())
		if err != nil {
			break
		}
	}
	require.ErrorIs(t, err, ErrResourceLimit, err)
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
		{
			err := json.Unmarshal([]byte(raw), &event)
			require.NoError(t, err)
		}
		var wire struct {
			State *string           `json:"state"`
			Tasks []json.RawMessage `json:"tasks"`
		}
		json.Unmarshal([]byte(raw), &wire)
		require.False(t, !reflect.DeepEqual(event.State, wire.State) || !reflect.DeepEqual(event.Tasks, wire.Tasks), event)
		encoded, err := json.Marshal(event)
		require.NoError(t, err)
		var a, b any
		json.Unmarshal([]byte(raw), &a)
		json.Unmarshal(encoded, &b)
		require.True(t, reflect.DeepEqual(a, b), raw, string(encoded))
	}
	var e types.SystemEvent
	json.Unmarshal([]byte(`{"subtype":"session_state_changed","state":"idle","future":null}`), &e)
	state := "running"
	e.State = &state
	encoded, _ := json.Marshal(e)
	var fields map[string]json.RawMessage
	json.Unmarshal(encoded, &fields)
	require.False(t, string(fields["state"]) != `"running"` || string(fields["future"]) != "null", string(encoded))
}

func (s *treeObservationSuite) TestTreeUnclassifiedRunCannotRegressAgentMetadata() {
	t := s.T()
	c := s.client
	treeFrame(t, c, `{"type":"system","subtype":"task_progress","session_id":"s","task_id":"a","run_id":"003"}`)
	treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"001","tool_use_id":"original"}`)
	ds, r := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"002","tool_use_id":"late"}`)
	require.False(t, !r.Stale || r.CurrentRunID != "003" || r.Source.ToolUseID != "original" || treeHas(ds, "SubagentAssociated", "identity-enriched"), ds, r)
}
