package codex_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// Synthetic 0.162.1 native fields through public stdio/WS/WSS APIs. Turn-chain
// identity and activity metadata do not create a thread or synthesize an answer.
func (obs *observationSuite) TestTreeCurrentVersionMetadata() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := obs.fixture(t, "request-normal", codex.Limits{})
			s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			cleanupSubscription(t, s)
			require.NoError(t, err, err)
			defer s.Close()
			emitRequests(t, f.c, treeFrame("thread/started", boundsRelation))
			for i := 0; i < 3; i++ {
				nextTree(t, s)
			}
			frames := []string{
				treeFrame("turn/started", `{"threadId":"child-1","turn":{"id":"child-turn","rootTurnId":"chain-root-turn","items":[],"status":"inProgress","future":null}}`),
				treeFrame("item/started", `{"threadId":"native-thread-42","turnId":"root-turn","startedAtMs":1,"item":{"type":"subAgentActivity","id":"activity","kind":"started","agentThreadId":"child-1","agentPath":"display-only","model":"current-model","reasoningEffort":"high","future":null}}`),
				treeFrame("item/completed", `{"threadId":"native-thread-42","turnId":"root-turn","completedAtMs":2,"item":{"type":"agentMessage","id":"partial","text":"partial parent answer","phase":"partial_answer"}}`),
				treeFrame("error", `{"threadId":"child-1","turnId":"child-turn","willRetry":false,"error":{"message":"opaque provider error","codexErrorInfo":{}}}`),
				treeFrame("turn/completed", `{"threadId":"native-thread-42","turn":{"id":"root-turn","rootTurnId":null,"items":[],"status":"completed"}}`),
				treeChildRaw,
			}
			emitRequests(t, f.c, frames...)
			for i, want := range frames {
				e := nextTree(t, s)
				require.False(t, e.Kind != nativeRecord || e.Native == nil || e.Native.DecodeError != nil || string(e.Raw) != want, "current wire changed: %+v", e)
				switch i {
				case 0:
					n := e.Native.Data.(*types.TurnStartedNotification)
					{
						got, ok := n.Turn.RootTurnID.Get()
						require.False(t, !ok || got != "chain-root-turn" || e.Source.TurnID != "child-turn", "turn chain conflated with thread membership")
					}
				case 1:
					n := e.Native.Data.(*types.ItemStartedNotification).Item.SubAgentActivity
					model, ok := n.Model.Get()
					effort, known := n.ReasoningEffort.Get()
					require.False(t, !ok || model != "current-model" || !known || effort != "high" || string(n.UnknownFields["future"]) != "null", "current activity metadata lost")
				case 2:
					n := e.Native.Data.(*types.ItemCompletedNotification).Item.AgentMessage
					{
						phase, ok := n.Phase.Get()
						require.False(t, !ok || phase != types.MessagePhasePartialAnswer || n.Text != "partial parent answer", "partial answer reinterpreted")
					}
				case 4:
					n := e.Native.Data.(*types.TurnCompletedNotification)
					require.True(t, n.Turn.RootTurnID.IsNull(), "explicit null lost")
				case 5:
					require.Equal(t, "child-1", e.Source.ThreadID, "parent completion closed child observation")
				}
			}
			select {
			case <-s.Done():
				require.FailNow(t, "test invariant", "activity/partial answer ended observation")
			default:
			}
		})
	}
}
