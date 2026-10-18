package codex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func v2Activity(parent, child, item, kind string) string {
	return fmt.Sprintf(`{"threadId":%q,"turnId":"parent-turn","completedAtMs":3,"item":{"type":"subAgentActivity","id":%q,"kind":%q,"agentThreadId":%q,"agentPath":"/display/only","model":"selected-model","reasoningEffort":"high","future":[null,9007199254740993]}}`, parent, item, kind, child)
}

func (obs *observationSuite) TestTreeV2CreatedWithoutThreadStarted() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := obs.fixture(t, "request-normal", codex.Limits{})
			s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			require.NoError(t, err, err)
			defer s.Close()
			raw, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			require.NoError(t, err, err)
			defer raw.Close()
			spawn := treeFrame("item/completed", v2Activity("native-thread-42", "child-1", "v2-spawn", "started"))
			completed := treeFrame("turn/completed", `{"threadId":"native-thread-42","turn":{"id":"parent-turn","rootTurnId":"chain-only","items":[],"status":"completed"}}`)
			frames := []string{treeChildRaw, spawn, spawn, completed, treeChildRaw, treeFrame("future/done", `{"threadId":"native-thread-42"}`)}
			emitRequests(t, f.c, frames...)
			events := treeUntilDone(t, s)
			created, children, spawns := 0, 0, 0
			for _, e := range events {
				if e.Kind == createdRecord {
					created++
					require.False(t, !e.CreationConfirmed || e.Source.ThreadID != "child-1" || e.ParentThreadID != "native-thread-42" || e.Evidence == nil || e.Evidence.Method != "item/completed" || e.Evidence.Source != (types.EventIdentity{ThreadID: "native-thread-42", TurnID: "parent-turn", ItemID: "v2-spawn"}) || e.Evidence.Tool != "" || len(e.Evidence.ReceiverThreadIDs) != 0, "V2 creation evidence: %+v", e)
				}
				if e.Kind == createdRecord {
					a := e.Evidence.Activity
					require.False(t, a == nil || a.ID != "v2-spawn" || a.Kind != types.SubAgentActivityKindStarted || a.AgentThreadID != "child-1" || a.AgentPath != "/display/only" || string(a.UnknownFields["future"]) != "[null,9007199254740993]", "missing actual activity: %+v", a)
				}
				if e.Native == nil {
					continue
				}
				if e.Native.Method == "item/agentMessage/delta" {
					children++
					require.False(t, created != 1 || string(e.Raw) != treeChildRaw || string(e.Native.Params) != treeChildParams, "child not routed after creation: %+v", e)
				}
				if e.Native.Method == "item/completed" {
					spawns++
					a := e.Native.Data.(*types.ItemCompletedNotification).Item.SubAgentActivity
					require.False(t, string(e.Raw) != spawn || a.AgentThreadID != "child-1" || a.Kind != types.SubAgentActivityKindStarted, "activity raw changed")
				}
			}
			require.False(t, created != 1 || children != 2 || spawns != 1, "created=%d children=%d spawns=%d", created, children, spawns)
			others := treeUntilDone(t, raw)
			require.Equal(t, len(events), len(others), "independent observers differ")
			for i, e := range others {
				require.False(t, string(e.Raw) != string(events[i].Raw) || e.Kind != events[i].Kind, "independent native envelope changed")
				require.False(t, e.Native != nil && string(e.Native.Params) != string(events[i].Native.Params), "independent params changed")
			}
			calls := trace(t, f.trace)
			require.False(t, len(calls) != 3 || calls[0].Method != "initialize" || calls[1].Method != "initialized" || calls[2].Method != "request-frames", "extra RPCs: %+v", calls)
		})
	}
}

func (obs *observationSuite) TestTreeV2HistorySuppressesCreation() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := obs.fixture(t, "request-normal", codex.Limits{})
			var n struct {
				Item json.RawMessage `json:"item"`
			}
			{
				err := json.Unmarshal([]byte(v2Activity("native-thread-42", "child-1", "history-spawn", "started")), &n)
				require.NoError(t, err, err)
			}
			seed := json.RawMessage(`{"id":"native-thread-42","turns":[{"id":"old-turn","items":[` + string(n.Item) + `]}]}`)
			s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{History: []json.RawMessage{seed}})
			require.NoError(t, err, err)
			defer s.Close()
			emitRequests(t, f.c, treeFrame("item/completed", v2Activity("native-thread-42", "child-1", "history-spawn", "started")), treeChildRaw, treeFrame("future/done", `{"threadId":"native-thread-42"}`))
			associated, children := 0, 0
			for _, e := range treeUntilDone(t, s) {
				require.False(t, e.Kind == createdRecord, "historical activity became creation")
				if e.Kind == associatedRecord {
					associated++
					require.False(t, e.Provenance != "history" || e.CreationConfirmed || e.Evidence.Method != "" || e.Evidence.Activity == nil || e.Evidence.Source.TurnID != "old-turn", "history evidence: %+v", e)
				}
				if e.Native != nil && e.Native.Method == "item/agentMessage/delta" {
					children++
				}
			}
			require.False(t, associated != 1 || children != 1, "associated=%d children=%d", associated, children)
		})
	}
}

func (obs *observationSuite) TestTreeV2GraphOrderingAndConfirmation() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := obs.fixture(t, "request-normal", codex.Limits{})
			s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			require.NoError(t, err, err)
			defer s.Close()
			// A grandchild and its creation evidence arrive before its parent's edge.
			emitRequests(t, f.c,
				treeFrame("future/grand", `{"threadId":"grand","turnId":"grand-turn","rootTurnId":"never-a-thread"}`),
				treeFrame("item/completed", v2Activity("child-1", "grand", "grand-spawn", "started")),
				treeChildRaw,
				treeFrame("thread/started", `{"thread":{"id":"child-1","parentThreadId":"native-thread-42"}}`),
				treeFrame("item/completed", v2Activity("native-thread-42", "child-1", "confirm", "started")),
				treeFrame("item/completed", v2Activity("native-thread-42", "child-1", "second-confirm", "started")),
				treeFrame("item/completed", v2Activity("native-thread-42", "grand", "conflict", "started")),
				treeFrame("item/completed", v2Activity("grand", "child-1", "cycle", "started")),
				treeFrame("future/done", `{"threadId":"native-thread-42"}`))
			associated, created, conflicts, cycles, grand := 0, 0, 0, 0, 0
			for _, e := range treeUntilDone(t, s) {
				switch e.Kind {
				case associatedRecord:
					associated++
					require.False(t, e.Source.ThreadID != "child-1" || created != 0, "ancestor association ordering: %+v", e)
				case createdRecord:
					created++
					require.Equal(t, 1, associated, "descendant preceded ancestor")
					require.False(t, e.Source.ThreadID == "child-1" && (e.Evidence.Activity.ID != "confirm" || e.Evidence.Activity.AgentThreadID != "child-1"), "confirmation identity changed")
				}
				if e.Reason == "relation-conflict" {
					conflicts++
					require.False(t, e.Evidence.Activity.ID != "conflict" || e.PreviousEvidence.Activity.ID != "grand-spawn" || e.PreviousEvidence.ParentThreadID != "child-1", "conflict evidence lost")
					// Mutating delivered evidence cannot corrupt the retained relation.
					e.PreviousEvidence.Activity.ID = "caller-mutated"
					e.PreviousEvidence.Activity.UnknownFields["future"][0] = '!'
				}
				if e.Reason == "relation-cycle" {
					cycles++
				}
				if e.Native != nil && e.Native.Method == "future/grand" {
					grand++
					require.False(t, associated != 1 || created < 1 || e.Source.TurnID != "grand-turn", "nested pending routing: %+v", e)
				}
			}
			require.False(t, associated != 1 || created != 2 || conflicts != 1 || cycles != 1 || grand != 1, "associated=%d created=%d conflicts=%d cycles=%d grand=%d", associated, created, conflicts, cycles, grand)
			// A second conflict must retain the original evidence, including unknown bytes.
			emitRequests(t, f.c, treeFrame("item/completed", v2Activity("native-thread-42", "grand", "conflict-2", "started")), treeFrame("future/done", `{"threadId":"native-thread-42"}`))
			for _, e := range treeUntilDone(t, s) {
				require.False(t, e.Reason == "relation-conflict" && (e.PreviousEvidence.Activity.ID != "grand-spawn" || string(e.PreviousEvidence.Activity.UnknownFields["future"]) != "[null,9007199254740993]"), "evidence aliases retained graph")
			}
		})
	}
}

func (obs *observationSuite) TestTreeV2NonCreationEvidence() {
	t := obs.T()
	valid := v2Activity("native-thread-42", "child-1", "probe", "started")
	cases := []struct{ name, method, params string }{
		{"item-started", "item/started", strings.Replace(valid, `"completedAtMs":3`, `"startedAtMs":3`, 1)},
		{"interacted", "item/completed", v2Activity("native-thread-42", "child-1", "probe", "interacted")},
		{"interrupted", "item/completed", v2Activity("native-thread-42", "child-1", "probe", "interrupted")},
		{"completed", "item/completed", v2Activity("native-thread-42", "child-1", "probe", "completed")},
		{"future-kind", "item/completed", v2Activity("native-thread-42", "child-1", "probe", "future")},
		{"empty-child", "item/completed", v2Activity("native-thread-42", "", "probe", "started")},
		{"empty-item", "item/completed", v2Activity("native-thread-42", "child-1", "", "started")},
		{"empty-parent", "item/completed", v2Activity("", "child-1", "probe", "started")},
		{"empty-turn", "item/completed", strings.Replace(valid, `"parent-turn"`, `""`, 1)},
		{"missing-child", "item/completed", strings.Replace(valid, `"agentThreadId":"child-1",`, ``, 1)},
		{"null-child", "item/completed", strings.Replace(valid, `"agentThreadId":"child-1"`, `"agentThreadId":null`, 1)},
		{"duplicate-child", "item/completed", strings.Replace(valid, `"agentThreadId":"child-1"`, `"agentThreadId":"child-1","agentThreadId":"other"`, 1)},
		{"conflicting-item", "item/completed", strings.Replace(valid, `"turnId"`, `"itemId":"other","turnId"`, 1)},
		{"bad-model", "item/completed", strings.Replace(valid, `"selected-model"`, `7`, 1)},
	}
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					f := obs.fixture(t, "request-normal", codex.Limits{})
					s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{PendingEvents: 1})
					require.NoError(t, err, err)
					defer s.Close()
					frame := treeFrame(tc.method, tc.params)
					emitRequests(t, f.c, frame, treeChildRaw, treeFrame("future/other", `{"threadId":"other"}`), treeFrame("future/done", `{"threadId":"native-thread-42"}`))
					unresolved, forwarded, gap := false, false, false
					for _, e := range treeUntilDone(t, s) {
						require.False(t, e.Kind == associatedRecord || e.Kind == createdRecord, "non-creation established relation: %+v", e)
						if e.Reason == "unresolved-source" && e.Source.ThreadID == "child-1" {
							gap = true
						}
						if string(e.Raw) == frame {
							forwarded = true
						}
						if string(e.Raw) == treeChildRaw {
							unresolved = true
						}
					}
					require.False(t, !unresolved || !forwarded || !gap, "unresolved=%v forwarded=%v", unresolved, forwarded)
				})
			}
		})
	}
}
