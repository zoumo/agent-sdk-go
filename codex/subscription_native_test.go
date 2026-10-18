package codex_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func (obs *observationSuite) TestSubscriptionNativeDeliveryAndConvenienceData() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := obs.fixture(t, "tree-success", codex.Limits{})
			s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			cleanupSubscription(t, s)
			require.NoError(t, err, err)
			defer s.Close()
			treeStart(t, f.c)
			created, deltas, states := 0, 0, 0
			for {
				select {
				case e, ok := <-s.Events():
					require.True(t, ok, "early close: %v", s.Err())
					switch d := e.Data.(type) {
					case *types.SubagentCreated:
						created++
						require.False(t, d.ParentThreadID != "native-thread-42" || d.Evidence == nil || d.Evidence.Method != "item/completed", "creation evidence: %+v", d)
					case *types.ObservationState:
						states++
					case *types.AgentMessageDeltaNotification:
						if e.DecodeError == nil && d.ThreadID == "child-1" {
							deltas++
							require.False(t, created != 2 || e.Method != "item/agentMessage/delta" || string(e.Params) != treeChildParams || string(e.Raw) != treeChildRaw, "native child changed: %+v", e)
						}
					}
					if e.Raw == nil {
						require.False(t, e.Method != "" || e.Params != nil || e.DecodeError != nil, "fabricated native fields: %+v", e)
						switch e.Data.(type) {
						case *types.SubagentCreated, *types.SubagentAssociated, *types.ObservationState:
						default:
							require.FailNowf(t, "test invariant", "untyped convenience data: %T", e.Data)
						}
					} else {
						var envelope struct {
							Method string          `json:"method"`
							Params json.RawMessage `json:"params"`
						}
						{
							err := json.Unmarshal(e.Raw, &envelope)
							require.False(t, err != nil || envelope.Method != e.Method || string(envelope.Params) != string(e.Params), "native envelope changed: %+v", e)
						}
					}
					if e.Method == "future/done" {
						require.False(t, created != 2 || deltas != 2 || states < 1, "coverage created=%d deltas=%d states=%d", created, deltas, states)
						return
					}
				case <-time.After(5 * time.Second):
					require.FailNow(t, "test invariant", "notification timeout")
				}
			}
		})
	}
}
