package codex_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestSubscriptionNativeDeliveryAndConvenienceData(t *testing.T) {
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := startFake(t, "tree-success", codex.Limits{})
			s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			treeStart(t, f.c)
			created, deltas, states := 0, 0, 0
			for {
				select {
				case e, ok := <-s.Events():
					if !ok {
						t.Fatalf("early close: %v", s.Err())
					}
					switch d := e.Data.(type) {
					case *types.SubagentCreated:
						created++
						if d.ParentThreadID != "native-thread-42" || d.Evidence == nil || d.Evidence.Method != "item/completed" {
							t.Fatalf("creation evidence: %+v", d)
						}
					case *types.ObservationState:
						states++
					case *types.AgentMessageDeltaNotification:
						if e.DecodeError == nil && d.ThreadID == "child-1" {
							deltas++
							if created != 2 || e.Method != "item/agentMessage/delta" || string(e.Params) != treeChildParams || string(e.Raw) != treeChildRaw {
								t.Fatalf("native child changed: %+v", e)
							}
						}
					}
					if e.Raw == nil {
						if e.Method != "" || e.Params != nil || e.DecodeError != nil {
							t.Fatalf("fabricated native fields: %+v", e)
						}
						switch e.Data.(type) {
						case *types.SubagentCreated, *types.SubagentAssociated, *types.ObservationState:
						default:
							t.Fatalf("untyped convenience data: %T", e.Data)
						}
					} else {
						var envelope struct {
							Method string          `json:"method"`
							Params json.RawMessage `json:"params"`
						}
						if err := json.Unmarshal(e.Raw, &envelope); err != nil || envelope.Method != e.Method || string(envelope.Params) != string(e.Params) {
							t.Fatalf("native envelope changed: %+v", e)
						}
					}
					if e.Method == "future/done" {
						if created != 2 || deltas != 2 || states < 1 {
							t.Fatalf("coverage created=%d deltas=%d states=%d", created, deltas, states)
						}
						return
					}
				case <-time.After(5 * time.Second):
					t.Fatal("notification timeout")
				}
			}
		})
	}
}
