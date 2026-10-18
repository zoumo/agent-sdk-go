package codex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func v2Activity(parent, child, item, kind string) string {
	return fmt.Sprintf(`{"threadId":%q,"turnId":"parent-turn","completedAtMs":3,"item":{"type":"subAgentActivity","id":%q,"kind":%q,"agentThreadId":%q,"agentPath":"/display/only","model":"selected-model","reasoningEffort":"high","future":[null,9007199254740993]}}`, parent, item, kind, child)
}

func TestTreeV2CreatedWithoutThreadStarted(t *testing.T) {
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := startFake(t, "request-normal", codex.Limits{})
			s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			raw, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			if err != nil {
				t.Fatal(err)
			}
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
					if !e.CreationConfirmed || e.Source.ThreadID != "child-1" || e.ParentThreadID != "native-thread-42" || e.Evidence == nil || e.Evidence.Method != "item/completed" || e.Evidence.Source != (types.EventIdentity{ThreadID: "native-thread-42", TurnID: "parent-turn", ItemID: "v2-spawn"}) || e.Evidence.Tool != "" || len(e.Evidence.ReceiverThreadIDs) != 0 {
						t.Fatalf("V2 creation evidence: %+v", e)
					}
				}
				if e.Kind == createdRecord {
					a := e.Evidence.Activity
					if a == nil || a.ID != "v2-spawn" || a.Kind != types.SubAgentActivityKindStarted || a.AgentThreadID != "child-1" || a.AgentPath != "/display/only" || string(a.UnknownFields["future"]) != "[null,9007199254740993]" {
						t.Fatalf("missing actual activity: %+v", a)
					}
				}
				if e.Native == nil {
					continue
				}
				if e.Native.Method == "item/agentMessage/delta" {
					children++
					if created != 1 || string(e.Raw) != treeChildRaw || string(e.Native.Params) != treeChildParams {
						t.Fatalf("child not routed after creation: %+v", e)
					}
				}
				if e.Native.Method == "item/completed" {
					spawns++
					a := e.Native.Data.(*types.ItemCompletedNotification).Item.SubAgentActivity
					if string(e.Raw) != spawn || a.AgentThreadID != "child-1" || a.Kind != types.SubAgentActivityKindStarted {
						t.Fatal("activity raw changed")
					}
				}
			}
			if created != 1 || children != 2 || spawns != 1 {
				t.Fatalf("created=%d children=%d spawns=%d", created, children, spawns)
			}
			others := treeUntilDone(t, raw)
			if len(others) != len(events) {
				t.Fatal("independent observers differ")
			}
			for i, e := range others {
				if string(e.Raw) != string(events[i].Raw) || e.Kind != events[i].Kind {
					t.Fatal("independent native envelope changed")
				}
				if e.Native != nil && string(e.Native.Params) != string(events[i].Native.Params) {
					t.Fatal("independent params changed")
				}
			}
			calls := trace(t, f.trace)
			if len(calls) != 3 || calls[0].Method != "initialize" || calls[1].Method != "initialized" || calls[2].Method != "request-frames" {
				t.Fatalf("extra RPCs: %+v", calls)
			}
		})
	}
}

func TestTreeV2HistorySuppressesCreation(t *testing.T) {
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := startFake(t, "request-normal", codex.Limits{})
			var n struct {
				Item json.RawMessage `json:"item"`
			}
			if err := json.Unmarshal([]byte(v2Activity("native-thread-42", "child-1", "history-spawn", "started")), &n); err != nil {
				t.Fatal(err)
			}
			seed := json.RawMessage(`{"id":"native-thread-42","turns":[{"id":"old-turn","items":[` + string(n.Item) + `]}]}`)
			s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{History: []json.RawMessage{seed}})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			emitRequests(t, f.c, treeFrame("item/completed", v2Activity("native-thread-42", "child-1", "history-spawn", "started")), treeChildRaw, treeFrame("future/done", `{"threadId":"native-thread-42"}`))
			associated, children := 0, 0
			for _, e := range treeUntilDone(t, s) {
				if e.Kind == createdRecord {
					t.Fatal("historical activity became creation")
				}
				if e.Kind == associatedRecord {
					associated++
					if e.Provenance != "history" || e.CreationConfirmed || e.Evidence.Method != "" || e.Evidence.Activity == nil || e.Evidence.Source.TurnID != "old-turn" {
						t.Fatalf("history evidence: %+v", e)
					}
				}
				if e.Native != nil && e.Native.Method == "item/agentMessage/delta" {
					children++
				}
			}
			if associated != 1 || children != 1 {
				t.Fatalf("associated=%d children=%d", associated, children)
			}
		})
	}
}

func TestTreeV2GraphOrderingAndConfirmation(t *testing.T) {
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := startFake(t, "request-normal", codex.Limits{})
			s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			if err != nil {
				t.Fatal(err)
			}
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
					if e.Source.ThreadID != "child-1" || created != 0 {
						t.Fatalf("ancestor association ordering: %+v", e)
					}
				case createdRecord:
					created++
					if associated != 1 {
						t.Fatal("descendant preceded ancestor")
					}
					if e.Source.ThreadID == "child-1" && (e.Evidence.Activity.ID != "confirm" || e.Evidence.Activity.AgentThreadID != "child-1") {
						t.Fatal("confirmation identity changed")
					}
				}
				if e.Reason == "relation-conflict" {
					conflicts++
					if e.Evidence.Activity.ID != "conflict" || e.PreviousEvidence.Activity.ID != "grand-spawn" || e.PreviousEvidence.ParentThreadID != "child-1" {
						t.Fatal("conflict evidence lost")
					}
					// Mutating delivered evidence cannot corrupt the retained relation.
					e.PreviousEvidence.Activity.ID = "caller-mutated"
					e.PreviousEvidence.Activity.UnknownFields["future"][0] = '!'
				}
				if e.Reason == "relation-cycle" {
					cycles++
				}
				if e.Native != nil && e.Native.Method == "future/grand" {
					grand++
					if associated != 1 || created < 1 || e.Source.TurnID != "grand-turn" {
						t.Fatalf("nested pending routing: %+v", e)
					}
				}
			}
			if associated != 1 || created != 2 || conflicts != 1 || cycles != 1 || grand != 1 {
				t.Fatalf("associated=%d created=%d conflicts=%d cycles=%d grand=%d", associated, created, conflicts, cycles, grand)
			}
			// A second conflict must retain the original evidence, including unknown bytes.
			emitRequests(t, f.c, treeFrame("item/completed", v2Activity("native-thread-42", "grand", "conflict-2", "started")), treeFrame("future/done", `{"threadId":"native-thread-42"}`))
			for _, e := range treeUntilDone(t, s) {
				if e.Reason == "relation-conflict" && (e.PreviousEvidence.Activity.ID != "grand-spawn" || string(e.PreviousEvidence.Activity.UnknownFields["future"]) != "[null,9007199254740993]") {
					t.Fatal("evidence aliases retained graph")
				}
			}
		})
	}
}

func TestTreeV2NonCreationEvidence(t *testing.T) {
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
					f := startFake(t, "request-normal", codex.Limits{})
					s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{PendingEvents: 1})
					if err != nil {
						t.Fatal(err)
					}
					defer s.Close()
					frame := treeFrame(tc.method, tc.params)
					emitRequests(t, f.c, frame, treeChildRaw, treeFrame("future/other", `{"threadId":"other"}`), treeFrame("future/done", `{"threadId":"native-thread-42"}`))
					unresolved, forwarded, gap := false, false, false
					for _, e := range treeUntilDone(t, s) {
						if e.Kind == associatedRecord || e.Kind == createdRecord {
							t.Fatalf("non-creation established relation: %+v", e)
						}
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
					if !unresolved || !forwarded || !gap {
						t.Fatalf("unresolved=%v forwarded=%v", unresolved, forwarded)
					}
				})
			}
		})
	}
}
