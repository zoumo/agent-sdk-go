package codex_test

import (
	"context"
	"testing"

	codex "github.com/zoumo/codex-sdk-go"
	"github.com/zoumo/codex-sdk-go/types"
)

// Synthetic 0.162.1 native fields through public stdio/WS/WSS APIs. Turn-chain
// identity and activity metadata do not create a thread or synthesize an answer.
func TestTreeCurrentVersionMetadata(t *testing.T) {
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := startFake(t, "request-normal", codex.Limits{})
			s, err := f.c.SubscribeTree(context.Background(), "native-thread-42", codex.TreeOptions{})
			if err != nil {
				t.Fatal(err)
			}
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
				if e.Kind != codex.TreeNative || e.Native == nil || e.Native.DecodeError != nil || string(e.Raw) != want {
					t.Fatalf("current wire changed: %+v", e)
				}
				switch i {
				case 0:
					n := e.Native.Data.(*types.TurnStartedNotification)
					if got, ok := n.Turn.RootTurnID.Get(); !ok || got != "chain-root-turn" || e.Source.TurnID != "child-turn" || e.RootThreadID != "native-thread-42" {
						t.Fatal("turn chain conflated with thread membership")
					}
				case 1:
					n := e.Native.Data.(*types.ItemStartedNotification).Item.SubAgentActivity
					model, ok := n.Model.Get()
					effort, known := n.ReasoningEffort.Get()
					if !ok || model != "current-model" || !known || effort != "high" || string(n.UnknownFields["future"]) != "null" {
						t.Fatal("current activity metadata lost")
					}
				case 2:
					n := e.Native.Data.(*types.ItemCompletedNotification).Item.AgentMessage
					if phase, ok := n.Phase.Get(); !ok || phase != types.MessagePhasePartialAnswer || n.Text != "partial parent answer" {
						t.Fatal("partial answer reinterpreted")
					}
				case 4:
					n := e.Native.Data.(*types.TurnCompletedNotification)
					if !n.Turn.RootTurnID.IsNull() {
						t.Fatal("explicit null lost")
					}
				case 5:
					if e.Source.ThreadID != "child-1" || e.Scope != "child" {
						t.Fatal("parent completion closed child observation")
					}
				}
			}
			select {
			case <-s.Done():
				t.Fatal("activity/partial answer ended observation")
			default:
			}
		})
	}
}
