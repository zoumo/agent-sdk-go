// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"context"
	"encoding/json"
	"time"

	"github.com/stretchr/testify/require"
)

func (s *controlInteractionSuite) TestReceiveConcreteEventsAndNativeJSON() {
	t := s.T()
	c := s.openFixture(t, "tree")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	first, err := c.Receive(ctx)
	require.NoError(t, err)
	state, ok := first.(ObservationState)
	require.Truef(t, ok, "first=%#v", first)
	require.EqualValuesf(t, "unverified", state.State, "first=%#v", first)
	associated, created := false, false
	frames := []string{creationInit, creationRequest("call", "Agent", creationInput, ""), creationTask("child", "call", "false")}
	for _, origin := range []string{"", `,"origin":null`, `,"origin":{"kind":"future","extra":7}`} {
		frames = append(frames, ` {"type":"result","subtype":"success","session_id":"s","future":true`+origin+`} `)
	}
	for _, frame := range frames {
		treeEmit(t, c, frame)
		for {
			received, err := c.Receive(ctx)
			require.NoError(t, err)
			encoded, err := json.Marshal(received)
			require.NoError(t, err)
			switch event := received.(type) {
			case Event:
				require.EqualValuesf(t, frame, string(event.Raw), "Raw changed: %q", event.Raw)
				var before, after any
				require.Nilf(t, json.Unmarshal([]byte(frame), &before), "native JSON changed: %s", encoded)
				require.Nilf(t, json.Unmarshal(encoded, &after), "native JSON changed: %s", encoded)
				require.Equalf(t, before, after, "native JSON changed: %s", encoded)
				require.False(t, event.Subtype == "task_started" && (!associated || !created), "child preceded relationship notifications")
				goto nextFrame
			case SubagentAssociated:
				associated = true
				require.EqualValues(t, "child", event.Source.TaskID, event)
			case SubagentCreated:
				require.True(t, associated, "creation preceded association")
				created = true
			case ObservationState:
			default:
				require.FailNowf(t, "unexpected test outcome", "unexpected public event %T", received)
			}
			var fields map[string]json.RawMessage
			require.Nilf(t, json.Unmarshal(encoded, &fields), "derived envelope classification survived: %s", encoded)
			require.Nilf(t, fields["origin"], "derived envelope classification survived: %s", encoded)
			require.Nilf(t, fields["native"], "derived envelope classification survived: %s", encoded)
			require.Nilf(t, fields["kind"], "derived envelope classification survived: %s", encoded)
		}
	nextFrame:
	}
	require.True(t, associated, "missing derived convenience events")
	require.True(t, created, "missing derived convenience events")
}
