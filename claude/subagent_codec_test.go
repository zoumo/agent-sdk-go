// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude/types"
)

func TestSubagentEnvelopeNullAbsentAndTypedEdits(t *testing.T) {
	for _, frame := range []string{`{"type":"assistant","message":{"content":[]}}`, `{"type":"assistant","agent_id":null,"subagent_type":null,"message":{"content":[]}}`, `{"type":"assistant","agent_id":"child","subagent_type":"Explore","message":{"content":[]},"unknown":42}`} {
		e, err := ParseEvent([]byte(frame))
		require.NoError(t, err)
		encoded, err := json.Marshal(e)
		require.NoError(t, err)
		var a, b any
		json.Unmarshal([]byte(frame), &a)
		json.Unmarshal(encoded, &b)
		require.Truef(t, reflect.DeepEqual(a, b), "round trip %s => %s", frame, encoded)
		value := "edited"
		e.AgentID = &value
		e.Fields = map[string]FieldState{"subagent_type": Present}
		encoded, err = json.Marshal(e)
		require.NoError(t, err)
		var obj map[string]json.RawMessage
		json.Unmarshal(encoded, &obj)
		require.EqualValues(t, `"edited"`, string(obj["agent_id"]), string(encoded))
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
