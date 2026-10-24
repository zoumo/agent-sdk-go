// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"context"
	"encoding/json"
	"io"
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
	first, err := c.session.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, ReceivedObservation, first.Kind)
	require.Equal(t, "forwarding-not-confirmed", first.Observation.Reason)
	frames := []string{creationInit, creationRequest("call", "Agent", creationInput, ""), creationTask("child", "call", "false")}
	for _, origin := range []string{"", `,"origin":null`, `,"origin":{"kind":"future","extra":7}`} {
		frames = append(frames, ` {"type":"result","subtype":"success","session_id":"s","future":true`+origin+`} `)
	}
	for _, frame := range frames {
		send(t, c, frame)
		record := sessionNative(t, c.session)
		require.Equal(t, frame, string(record.Raw))
		require.NotNil(t, record.Event)
		encoded, err := json.Marshal(*record.Event)
		require.NoError(t, err)
		var before, after any
		require.NoError(t, json.Unmarshal([]byte(frame), &before))
		require.NoError(t, json.Unmarshal(encoded, &after))
		require.Equal(t, before, after)
	}
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
			_, err := c.session.Receive(tc.ctx)
			require.ErrorIs(t, err, tc.want)
		})
	}
	event, err := c.session.Receive(context.Background())
	require.NoError(t, err)
	require.Equal(t, ReceivedObservation, event.Kind)
	require.Equal(t, "forwarding-not-confirmed", event.Observation.Reason)
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
			require.Equal(t, 5, c.session.fifo.Stats().Items)
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
			require.Zero(t, c.session.fifo.Stats().Items)
			require.Zero(t, c.session.fifo.Stats().Bytes)
			require.True(t, c.session.fifo.Stats().Closed)
			_, err = c.session.Receive(context.Background())
			if terminal {
				require.ErrorIs(t, err, ErrProtocol)
			} else {
				require.ErrorIs(t, err, io.EOF)
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = c.session.Receive(canceled)
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
