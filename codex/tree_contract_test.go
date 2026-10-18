package codex_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

const treeChildParams = `{ "threadId" : "child-1", "turnId":"child-turn", "itemId":"child-item", "delta":"child text", "future":[null,9007199254740993] }`
const treeChildRaw = `{"method":"item/agentMessage/delta","params":` + treeChildParams + `,"futureEnvelope":null}`

func treeSpawnParams(tool, status string) string {
	return `{"threadId":"native-thread-42","turnId":"root-turn","completedAtMs":8,"item":{"type":"collabAgentToolCall","id":"spawn-1","agentsStates":{"child-1":{"status":"running","message":null}},"receiverThreadIds":["child-1","child-1","child-2"],"senderThreadId":"native-thread-42","status":"` + status + `","tool":"` + tool + `","prompt":null,"model":null,"reasoningEffort":null,"future":null}}`
}

// Independently authored synthetic collab/auto-attach fixture (rechecked at 0.162.1). All child
// events arrive on the original connection before the root start RPC response.
// No model, real CLI, credentials, or extra socket is used by the SDK.
func handleTreePeer(mode string, m fakeEnvelope, send func(json.RawMessage, string)) bool {
	if handleLifecycleTreePeer(mode, m, send) || handleNestedTreePeer(mode, m, send) {
		return true
	}
	if !strings.HasPrefix(mode, "tree-") {
		return false
	}
	emit := func(method, params string) { fmt.Fprintf(os.Stdout, "{\"method\":%q,\"params\":%s}\n", method, params) }
	switch m.Method {
	case "turn/start":
		emit("turn/started", `{"threadId":"native-thread-42","turn":{"id":"root-turn","items":[],"status":"inProgress"}}`)
		emit("future/other", `{"threadId":"unrelated-root","secret":null}`)
		emit("thread/started", `{"thread":{"id":"unrelated-root","future":null}}`)
		tool, status := "spawnAgent", "completed"
		if mode == "tree-failed" {
			status = "failed"
		}
		if mode == "tree-resume" {
			tool = "resumeAgent"
		}
		emit("item/completed", treeSpawnParams(tool, status))
		// Repeated relations must not produce a second creation or duplicate delivery.
		emit("item/completed", treeSpawnParams(tool, status))
		fmt.Fprintln(os.Stdout, treeChildRaw)
		fmt.Fprintln(os.Stdout, treeChildRaw)
		emit("future/child", `{"threadId":"child-2","opaque":null}`)
		emit("item/agentMessage/delta", `{"threadId":"child-1","turnId":"child-turn","itemId":"broken"}`)
		emit("future/null", `null`)
		fmt.Fprintln(os.Stdout, `{"method":"future/absent"}`)
		emit("future/done", `{"threadId":"native-thread-42"}`)
		send(m.ID, `{"turn":`+nativeTurn+`}`)
	case "tree-emit":
		fmt.Fprintln(os.Stdout, treeChildRaw)
		send(m.ID, `{}`)
	default:
		return false
	}
	return true
}
func nextTree(t *testing.T, s *codex.Subscription) subscriptionRecord {
	t.Helper()
	select {
	case e, ok := <-s.Events():
		if !ok {
			t.Fatalf("tree closed: %v", s.Err())
		}
		return recordView(e)
	case <-time.After(5 * time.Second):
		t.Fatal("tree receive timeout")
		return subscriptionRecord{}
	}
}
func treeUntilDone(t *testing.T, s *codex.Subscription) []subscriptionRecord {
	t.Helper()
	var events []subscriptionRecord
	for {
		e := nextTree(t, s)
		events = append(events, e)
		if e.Native != nil && e.Native.Method == "future/done" {
			return events
		}
	}
}
func treeStart(t *testing.T, c *codex.Client) {
	t.Helper()
	_, err := c.StartTurn(context.Background(), types.TurnStartParams{ThreadID: "native-thread-42", Input: []types.UserInput{}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTreeLiveChildStream(t *testing.T) {
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := startFake(t, "tree-success", codex.Limits{})
			raw, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			a, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			// Reader emits to registered observers even before this RPC returns.
			treeStart(t, f.c)
			events := treeUntilDone(t, a)
			others := treeUntilDone(t, b)
			if len(events) != len(others) {
				t.Fatal("observers differ")
			}
			if events[0].Kind != stateRecord || events[0].State != "incomplete" {
				t.Fatal("missing honest observation limits")
			}
			created := 0
			childDeltas := 0
			child2 := false
			bad := false
			sawNull := false
			sawAbsent := false
			for i, e := range events {
				if e.Kind == createdRecord {
					created++
					if !e.CreationConfirmed || e.ParentThreadID != "native-thread-42" || e.Native != nil || e.Raw != nil || e.Evidence.Method != "item/completed" || e.Evidence.Source.ItemID != "spawn-1" || e.Evidence.Source.TurnID != "root-turn" || len(e.Evidence.ReceiverThreadIDs) != 3 {
						t.Fatalf("creation evidence: %+v", e)
					}
				}
				if e.Native == nil {
					continue
				}
				if e.Source.ThreadID == "unrelated-root" {
					t.Fatal("unrelated root leaked")
				}
				if e.Native.Method == "item/agentMessage/delta" && e.Native.DecodeError == nil {
					childDeltas++
					if created != 2 || e.Source.ThreadID != "child-1" || e.Source.TurnID != "child-turn" || e.Source.ItemID != "child-item" || string(e.Raw) != treeChildRaw || string(e.Native.Params) != treeChildParams {
						t.Fatalf("child native: %+v", e)
					}
					data := e.Native.Data.(*types.AgentMessageDeltaNotification)
					if string(data.UnknownFields["future"]) != "[null,9007199254740993]" {
						t.Fatal("lost unknown fields")
					}
					// Mutating one observer cannot corrupt another, including typed storage.
					e.Raw[0] = '!'
					e.Native.Params[0] = '!'
					data.Delta = "changed"
					other := others[i]
					if string(other.Raw) != treeChildRaw || string(other.Native.Params) != treeChildParams || other.Native.Data.(*types.AgentMessageDeltaNotification).Delta != "child text" {
						t.Fatal("tree buffers shared")
					}
				}
				if e.Native.Method == "future/child" {
					child2 = e.Source.ThreadID == "child-2" && e.Native.Data == nil
				}
				if e.Native.DecodeError != nil {
					bad = e.Source.ThreadID == "child-1" && len(e.Raw) > 0
				}
				if e.Native.Method == "future/null" {
					sawNull = string(e.Native.Params) == "null"
				}
				if e.Native.Method == "future/absent" {
					sawAbsent = e.Native.Params == nil
				}
			}
			if created != 2 || childDeltas != 2 || !child2 || !bad || !sawNull || !sawAbsent {
				t.Fatalf("coverage: created=%d deltas=%d child2=%v bad=%v null=%v absent=%v", created, childDeltas, child2, bad, sawNull, sawAbsent)
			}
			rawCount := 0
			rawOther := 0
			for {
				e := nextEvent(t, raw)
				rawCount++
				if e.Method == "future/other" || e.Method == "thread/started" {
					rawOther++
				}
				if e.Method == "item/agentMessage/delta" && e.DecodeError == nil && string(e.Params) != treeChildParams {
					t.Fatal("native subscriber corrupted")
				}
				if e.Method == "future/done" {
					break
				}
			}
			if rawCount != 9 || rawOther != 0 {
				t.Fatalf("managed native routing changed: count=%d other=%d", rawCount, rawOther)
			}
			calls := trace(t, f.trace)
			if len(calls) != 3 || calls[0].Method != "initialize" || calls[1].Method != "initialized" || calls[2].Method != "turn/start" {
				t.Fatalf("unexpected child/subscribe RPCs: %+v", calls)
			}
		})
	}
}

func TestTreeFailedSpawnAndResumeAreNotCreated(t *testing.T) {
	for _, mode := range []string{"tree-failed", "tree-resume"} {
		t.Run(mode, func(t *testing.T) {
			f := startFake(t, mode, codex.Limits{})
			s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			treeStart(t, f.c)
			associated := 0
			child := 0
			nativeSpawn := 0
			for _, e := range treeUntilDone(t, s) {
				if e.Kind == createdRecord {
					t.Fatal("false creation")
				}
				if e.Kind == associatedRecord {
					associated++
					if e.CreationConfirmed {
						t.Fatal("false confirmation")
					}
				}
				if (e.Source.ThreadID == "child-1" || e.Source.ThreadID == "child-2") && e.Native != nil {
					child++
				}
				if e.Native != nil && e.Native.Method == "item/completed" {
					nativeSpawn++
				}
			}
			if nativeSpawn != 1 {
				t.Fatal("lost native collab")
			}
			if mode == "tree-failed" && (associated != 0 || child != 0) {
				t.Fatal("failed spawn established child")
			}
			if mode == "tree-resume" && (associated != 2 || child != 4) {
				t.Fatalf("association coverage: %d %d", associated, child)
			}
		})
	}
}

func TestTreeBoundsAndLocalClose(t *testing.T) {
	f := startFake(t, "tree-success", codex.Limits{})
	slow, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{EventQueue: 1})
	if err != nil {
		t.Fatal(err)
	}
	nextTree(t, slow) // consume initial diagnostic
	healthy, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	treeStart(t, f.c)
	treeUntilDone(t, healthy)
	select {
	case <-slow.Done():
	case <-time.After(time.Second):
		t.Fatal("overflow not signaled")
	}
	var overflow *codex.SubscriptionOverflowError
	if !errors.As(slow.Err(), &overflow) {
		t.Fatalf("overflow: %v", slow.Err())
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = healthy.Close(); _ = healthy.Err() }()
	}
	wg.Wait()
	if healthy.Err() != nil {
		t.Fatal(healthy.Err())
	}
	if err = f.c.Call(context.Background(), "echo", true, nil); err != nil {
		t.Fatal(err)
	}
	for _, m := range trace(t, f.trace) {
		if m.Method == "thread/resume" || m.Method == "thread/unsubscribe" || m.Method == "turn/interrupt" {
			t.Fatalf("local close sent RPC: %s", m.Method)
		}
	}
}

func TestTreeAdmissionAndNodeBound(t *testing.T) {
	f := startFake(t, "tree-success", codex.Limits{Subscriptions: 1})
	for _, tc := range []struct {
		ctx     context.Context
		root    string
		options types.SubscriptionOptions
	}{
		{nil, "root", types.SubscriptionOptions{}},
		{context.Background(), "", types.SubscriptionOptions{}},
		{context.Background(), "root", types.SubscriptionOptions{EventQueue: -1}},
		{context.Background(), "root", types.SubscriptionOptions{MaxNodes: -1}},
		{context.Background(), "root", types.SubscriptionOptions{MaxDepth: -1}},
		{context.Background(), "root", types.SubscriptionOptions{PendingEvents: -1}},
		{context.Background(), "root", types.SubscriptionOptions{PendingBytes: -1}},
		{context.Background(), "root", types.SubscriptionOptions{PendingAge: -1}},
		{context.Background(), "root", types.SubscriptionOptions{DedupEntries: -1}},
	} {
		if _, err := f.c.Subscribe(tc.ctx, tc.root, tc.options); err == nil {
			t.Fatal("invalid admission accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.c.Subscribe(ctx, "root", types.SubscriptionOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{MaxNodes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{}); !errors.Is(err, codex.ErrLimit) {
		t.Fatalf("shared admission limit: %v", err)
	}
	treeStart(t, f.c)
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("node limit not signaled")
	}
	if !errors.Is(s.Err(), codex.ErrLimit) {
		t.Fatal(s.Err())
	}
	raw, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
	if err != nil {
		t.Fatal("tree did not release slot:", err)
	}
	defer raw.Close()
}
