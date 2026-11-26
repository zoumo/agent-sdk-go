package codex

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/codex/types"
	"github.com/zoumo/agent-sdk-go/client/internal/lineage"
)

func TestCompletionRoutingSurvivesReceivedEventMutation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c := newClient(DefaultLimits())
	graph, err := lineage.New[string](lineage.Limits{MaxNodes: 4, MaxRelations: 4, MaxDepth: 4})
	require.NoError(t, err)
	s := &Session{client: c, cancel: cancel, parents: graph, members: make(map[string]struct{}), maxMembers: 4}
	require.NoError(t, c.ownership.bindLocked(s, "root"))
	t.Cleanup(func() { require.NoError(t, s.queue.Abort(nil)) })

	const method = "item/completed"
	params := []byte(`{"threadId":"root","turnId":"t","completedAtMs":1,"item":{"type":"collabAgentToolCall","id":"spawn","tool":"spawnAgent","status":"completed","senderThreadId":"root","receiverThreadIds":["child"],"agentsStates":{}}}`)
	event := decodeEvent(method, params)
	require.NoError(t, event.DecodeError)
	source := treeIdentity(params)
	routing := snapshotCompletionRouting(event, source)
	s.acceptLocked(Received{Kind: ReceivedEvent, Event: &event, Source: source})

	// Force the consumer edit before applying the completion's routing evidence.
	got, err := s.Receive(ctx)
	require.NoError(t, err)
	n := got.Event.Data.(*types.ItemCompletedNotification)
	n.Item.CollabAgentToolCall.ReceiverThreadIDs[0] = "consumer-edit"
	n.Item.CollabAgentToolCall.Tool = types.CollabAgentToolSendInput
	got.Event.Data = nil
	s.applyCompletionRoutingLocked(routing, source.ThreadID, method)

	for _, reason := range []string{"member-confirmed", "parent-confirmed"} {
		got, err = s.Receive(ctx)
		require.NoError(t, err)
		require.Equal(t, ReceivedObservation, got.Kind)
		require.Equal(t, reason, got.Observation.Reason)
		require.Equal(t, "child", got.Observation.ThreadID)
	}
	require.NoError(t, c.routeFrame([]byte(`{"method":"future/child","params":{"threadId":"child"}}`)))
	got, err = s.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, ReceivedEvent, got.Kind)
	require.Equal(t, "child", got.Source.ThreadID)
	require.Equal(t, "future/child", got.Event.Method)
}
