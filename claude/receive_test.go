// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/internal/queue"
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

func (s *controlInteractionSuite) TestReceiveInvalidWaitDoesNotConsumeStartup() {
	t := s.T()
	c := s.openFixture(t, "tree")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"nil", nil, queue.ErrNilContext},
		{"canceled", canceled, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Receive(tc.ctx)
			require.ErrorIs(t, err, tc.want)
		})
	}
	event, err := c.Receive(context.Background())
	require.NoError(t, err)
	state, ok := event.(ObservationState)
	require.True(t, ok)
	require.Equal(t, "unverified", state.State)
}

func (s *controlInteractionSuite) TestAbortDiscardsNativeBacklogAndRejectsReceive() {
	t := s.T()
	for _, terminal := range []bool{false, true} {
		name := "explicit shutdown"
		if terminal {
			name = "protocol failure"
		}
		t.Run(name, func(t *testing.T) {
			c := s.openFixture(t, "overload_ack_full", func(o *Options) { o.Limits.EventBuffer = 1 })
			send(t, c, "fill")
			send(t, c, "overflow")
			// The control reply follows the entire burst without consuming any events.
			require.NoError(t, c.Interrupt(context.Background()))
			require.Equal(t, 4, c.events.Stats().Items)
			if terminal {
				send(t, c, "malformed")
				awaitDone(t, c)
			}
			err := c.Abort()
			if terminal {
				require.ErrorIs(t, err, ErrProtocol)
			} else {
				require.NoError(t, err)
			}
			awaitDone(t, c)
			require.Zero(t, c.events.Stats().Items)
			require.Zero(t, c.events.Stats().Bytes)
			require.True(t, c.events.Stats().Closed)
			_, err = c.Receive(context.Background())
			if terminal {
				require.ErrorIs(t, err, ErrProtocol)
			} else {
				require.ErrorIs(t, err, ErrClosed)
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = c.Receive(canceled)
			require.ErrorIs(t, err, context.Canceled)
			// Repeated and concurrent shutdown remains harmless.
			if terminal {
				require.ErrorIs(t, c.Abort(), ErrProtocol)
			} else {
				require.NoError(t, c.Abort())
			}
		})
	}
}
