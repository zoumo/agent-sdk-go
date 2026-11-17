package agentcodex_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestStateEventMutationDoesNotChangeNextTransition(t *testing.T) {
	for _, test := range []struct {
		name  string
		after []string
		count int
		state agent.SessionState
	}{
		{name: "turn_started", count: 1, state: agent.StateRunning},
		{
			name: "permission", after: []string{commandApprovalRequest("approval-1")},
			count: 3, state: agent.StateRequiresAction,
		},
		{
			name:  "turn_completed",
			after: []string{`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"completed"}}}`},
			count: 2, state: agent.StateIdle,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			frames := interactionSessionFrames(test.after...)
			if test.name == "permission" {
				frames = append(frames, `>{"id":"approval-1","result":{"decision":"accept"}}`)
			}
			// The request is a barrier: the next state arrives only after the
			// caller has mutated the previously delivered event.
			frames = append(frames,
				`>{"id":94,"method":"test/advance","params":{}}`,
				`<{"id":94,"result":{}}`,
				`<{"method":"thread/status/changed","params":{"threadId":"root-a","status":{"type":"idle"}}}`,
			)
			frames = append(frames, codexCloseFrames(95, "root-a")...)
			provider := newTestProvider(t, prepareCodexPeer(t, frames...))
			t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
			_, err := provider.Start(ctx)
			require.NoError(t, err)
			session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)
			stream, err := session.Events()
			require.NoError(t, err)
			submitInteractionTurn(t, ctx, session)
			var state agent.StateUpdatePayload
			var interaction agent.InteractionRef
			for range test.count {
				event, nextErr := stream.Next(ctx)
				require.NoError(t, nextErr)
				if payload, ok := event.Payload.(agent.StateUpdatePayload); ok {
					state = payload
				}
				if payload, ok := event.Payload.(agent.InteractionRequestedPayload); ok {
					interaction = payload.Interaction
				}
			}
			require.NotNil(t, state.State)
			require.Equal(t, test.state, *state.State)
			*state.State = agent.StateRequiresAction
			if test.state == agent.StateRequiresAction {
				*state.State = agent.StateRunning
			}
			if state.PreviousState != nil {
				*state.PreviousState = agent.StateIdle
			}
			if interaction != "" {
				_, err = session.Answer(ctx, interaction, agent.PermissionAnswer{
					Outcome: agent.PermissionSelected, OptionID: "allow-once",
				})
				require.NoError(t, err)
			}
			client, err := provider.NativeClient()
			require.NoError(t, err)
			require.NoError(t, client.Call(ctx, "test/advance", json.RawMessage(`{}`), nil))
			for {
				event, nextErr := stream.Next(ctx)
				require.NoError(t, nextErr)
				if next, ok := event.Payload.(agent.StateUpdatePayload); ok {
					require.NotNil(t, next.PreviousState)
					assert.Equal(t, test.state, *next.PreviousState)
					assert.Equal(t, agent.StateIdle, *next.State)
					break
				}
			}
			require.NoError(t, session.Close(ctx))
		})
	}
}
