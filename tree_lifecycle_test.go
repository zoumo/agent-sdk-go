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

	codex "github.com/zoumo/codex-sdk-go"
)

func handleLifecycleTreePeer(mode string, m fakeEnvelope, send func(json.RawMessage, string)) bool {
	if mode != "tree-lifecycle" && mode != "tree-history" {
		return false
	}
	emit := func(method, params string) { fmt.Fprintf(os.Stdout, "{\"method\":%q,\"params\":%s}\n", method, params) }
	switch m.Method {
	case "turn/start":
		emit("item/completed", treeSpawnParams("spawnAgent", "completed"))
		if mode == "tree-history" {
			p := strings.ReplaceAll(treeSpawnParams("spawnAgent", "completed"), `"child-1","child-1","child-2"`, `"live-child","known-unlinked"`)
			p = strings.ReplaceAll(p, "spawn-1", "spawn-live")
			emit("item/completed", p)
		} else {
			// wait_agent keeps the parent active. Parent idle/completed concerns that
			// thread only; the independently active child continues on this connection.
			emit("thread/status/changed", `{"threadId":"native-thread-42","status":{"type":"active","activeFlags":[]}}`)
			emit("thread/status/changed", `{"threadId":"child-1","status":{"type":"active","activeFlags":[]}}`)
			emit("thread/status/changed", `{"threadId":"native-thread-42","status":{"type":"idle"}}`)
			emit("turn/completed", `{"threadId":"native-thread-42","turn":{"id":"root-turn","items":[],"status":"completed","error":null}}`)
			fmt.Fprintln(os.Stdout, treeChildRaw)
			for _, kind := range []string{"completed", "interrupted"} {
				emit("item/completed", `{"threadId":"native-thread-42","turnId":"root-turn","completedAtMs":9,"item":{"type":"subAgentActivity","id":"activity-`+kind+`","agentThreadId":"child-1","agentPath":"/display-only","kind":"`+kind+`"}}`)
			}
			emit("turn/completed", `{"threadId":"child-1","turn":{"id":"child-turn","items":[],"status":"interrupted","error":null}}`)
			emit("turn/started", `{"threadId":"child-1","turn":{"id":"next-child-turn","items":[],"status":"inProgress"}}`)
		}
		emit("future/done", `{"threadId":"native-thread-42"}`)
		send(m.ID, `{"turn":`+nativeTurn+`}`)
	case "tree-step":
		fmt.Fprintln(os.Stdout, treeChildRaw)
		send(m.ID, `{}`)
	default:
		return false
	}
	return true
}

func TestTreeParentIdleChildActive(t *testing.T) {
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := startFake(t, "tree-lifecycle", codex.Limits{})
			s, err := f.c.SubscribeTree(context.Background(), "native-thread-42", codex.TreeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			treeStart(t, f.c)
			activities := 0
			rootIdle, rootDone, childActive, delta, interrupted, nextRun := false, false, false, false, false, false
			for _, e := range treeUntilDone(t, s) {
				if e.Native == nil {
					continue
				}
				if e.Native.Method == "item/completed" && strings.Contains(string(e.Raw), "subAgentActivity") {
					if e.Native.DecodeError != nil {
						t.Fatal(e.Native.DecodeError)
					}
					activities++
				}
				if e.Native.Method == "thread/status/changed" {
					if e.Source.ThreadID == "child-1" {
						childActive = strings.Contains(string(e.Raw), `"active"`)
					}
					if e.Source.ThreadID == "native-thread-42" && strings.Contains(string(e.Raw), `"idle"`) {
						rootIdle = true
					}
				}
				if e.Native.Method == "turn/completed" && e.Source.ThreadID == "native-thread-42" {
					if !rootIdle || !childActive {
						t.Fatal("parent idle lost or child status changed")
					}
					rootDone = true
				}
				if e.Native.Method == "item/agentMessage/delta" {
					delta = rootDone && childActive && string(e.Raw) == treeChildRaw && e.Source.TurnID == "child-turn"
				}
				if e.Native.Method == "turn/completed" && e.Source.ThreadID == "child-1" {
					interrupted = strings.Contains(string(e.Raw), `"interrupted"`)
				}
				if e.Native.Method == "turn/started" && e.Source.ThreadID == "child-1" {
					nextRun = e.Source.TurnID == "next-child-turn"
				}
			}
			if !rootDone || !delta || !interrupted || !nextRun || activities != 2 {
				t.Fatalf("lifecycle: %v %v %v %v", rootDone, delta, interrupted, nextRun)
			}
			select {
			case <-s.Done():
				t.Fatal("parent ended observation")
			default:
			}
			if err = f.c.Call(context.Background(), "tree-step", nil, nil); err != nil {
				t.Fatal(err)
			}
			if e := nextTree(t, s); e.Native == nil || string(e.Raw) != treeChildRaw {
				t.Fatal("child no longer observed")
			}
		})
	}
}

func TestTreeCloseCancelSharedSource(t *testing.T) {
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := startFake(t, "tree-lifecycle", codex.Limits{})
			a, err := f.c.SubscribeTree(context.Background(), "native-thread-42", codex.TreeOptions{History: []json.RawMessage{json.RawMessage(`{"id":"child-1","parentThreadId":"native-thread-42"}`)}})
			if err != nil {
				t.Fatal(err)
			}
			b, err := f.c.SubscribeTree(context.Background(), "child-1", codex.TreeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			canceled, err := f.c.SubscribeTree(ctx, "child-1", codex.TreeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := f.c.Subscribe(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			nextTree(t, a)
			nextTree(t, a) // history gap
			nextTree(t, a) // shared child association
			nextTree(t, b)
			nextTree(t, canceled)
			var wg sync.WaitGroup
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); _ = a.Close(); cancel() }()
			}
			// Delivery races with observation teardown through the real reader.
			for i := 0; i < 8; i++ {
				if err = f.c.Call(context.Background(), "tree-step", nil, nil); err != nil {
					t.Fatal(err)
				}
			}
			wg.Wait()
			select {
			case <-canceled.Done():
			case <-time.After(time.Second):
				t.Fatal("cancel blocked")
			}
			if !errors.Is(canceled.Err(), context.Canceled) || a.Err() != nil {
				t.Fatal("termination causes", canceled.Err(), a.Err())
			}
			for i := 0; i < 8; i++ {
				e := nextTree(t, b)
				if e.Scope != "root" || string(e.Raw) != treeChildRaw {
					t.Fatal("shared source stopped")
				}
				if e := nextEvent(t, raw); string(e.Params) != treeChildParams {
					t.Fatal("raw stopped")
				}
			}
			if err = f.c.Call(context.Background(), "echo", true, nil); err != nil {
				t.Fatal(err)
			}
			for _, call := range trace(t, f.trace) {
				if call.Method != "initialize" && call.Method != "initialized" && call.Method != "tree-step" && call.Method != "echo" {
					t.Fatalf("unexpected RPC %s", call.Method)
				}
			}
		})
	}
}

func TestTreeDisconnectHistoryEpoch(t *testing.T) {
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			var lastEpoch uint64
			// Root history supplies successful historical spawn evidence; grandchild is
			// intentionally before its parent. No native snapshot is replayed as live.
			var spawn struct {
				Item json.RawMessage `json:"item"`
			}
			if err := json.Unmarshal([]byte(treeSpawnParams("spawnAgent", "completed")), &spawn); err != nil {
				t.Fatal(err)
			}
			history := []json.RawMessage{
				json.RawMessage(`{"id":"known-unlinked"}`),
				json.RawMessage(`{"id":"grand","parentThreadId":"child-1"}`),
				json.RawMessage(`{"id":"native-thread-42","turns":[{"id":"root-turn","items":[` + string(spawn.Item) + `]}]}`),
				json.RawMessage(`{"id":"child-1","source":{"subAgent":{"thread_spawn":{"parent_thread_id":"native-thread-42"}}},"status":{"type":"active"}}`),
			}
			for epoch := 0; epoch < 2; epoch++ {
				f := startFake(t, "tree-history", codex.Limits{})
				s, err := f.c.SubscribeTree(context.Background(), "native-thread-42", codex.TreeOptions{History: history})
				if err != nil {
					t.Fatal(err)
				}
				treeStart(t, f.c)
				associated, created, terminals, gap := 0, 0, 0, false
				discovered := 0
				for _, e := range treeUntilDone(t, s) {
					if e.Epoch == 0 || e.Epoch == lastEpoch {
						t.Fatal("epoch reused")
					}
					if e.Reason == "history-recovery-gap" {
						gap = e.State == "incomplete" && e.Provenance == "history"
					}
					if e.Kind == codex.TreeSubagentAssociated {
						if e.Source.ThreadID == "known-unlinked" {
							discovered++
							if e.Provenance != "live" || e.CreationConfirmed || e.Reason != "discovered" {
								t.Fatal("unlinked historical entity recreated")
							}
							continue
						}
						associated++
						if e.Provenance != "history" || e.Reason != "history" || e.CreationConfirmed || e.Evidence.Provenance != "history" {
							t.Fatalf("false history creation %+v", e)
						}
					}
					if e.Kind == codex.TreeSubagentCreated {
						created++
						if e.Source.ThreadID != "live-child" || e.Provenance != "live" {
							t.Fatal("historical entity recreated")
						}
					}
					if e.Native != nil && e.Native.Method == "item/completed" {
						terminals++
					}
				}
				if associated != 3 || created != 1 || terminals != 2 || !gap || discovered != 1 {
					t.Fatalf("seed coverage %d %d %d %v", associated, created, terminals, gap)
				}
				// Leave one admitted native event unread, then abruptly lose the peer.
				if err = f.c.Call(context.Background(), "tree-step", nil, nil); err != nil {
					t.Fatal(err)
				}
				_ = f.c.Call(context.Background(), "exit", nil, nil)
				select {
				case <-s.Done():
				case <-time.After(5 * time.Second):
					t.Fatal("disconnect not signaled")
				}
				if s.Err() == nil {
					t.Fatal("EOF treated as success")
				}
				remaining := []codex.TreeEvent{}
				for e := range s.Events() {
					remaining = append(remaining, e)
				}
				if len(remaining) != 2 || remaining[0].Native == nil || string(remaining[0].Raw) != treeChildRaw || remaining[1].State != "disconnected" || remaining[1].Reason != "permanent-gap" {
					t.Fatalf("lost queued data or disconnect: %+v", remaining)
				}
				lastEpoch = remaining[1].Epoch
				if err = s.Close(); err != nil || s.Err() == nil {
					t.Fatal("close erased failure")
				}
				calls := trace(t, f.trace)
				for _, call := range calls {
					if call.Method != "initialize" && call.Method != "initialized" && call.Method != "turn/start" && call.Method != "tree-step" && call.Method != "exit" {
						t.Fatalf("automatic recovery RPC %s", call.Method)
					}
				}
			}
		})
	}
}

func TestTreeDisconnectFullQueueAndSeedBounds(t *testing.T) {
	f := startFake(t, "tree-lifecycle", codex.Limits{})
	for _, options := range []codex.TreeOptions{
		{History: []json.RawMessage{json.RawMessage(`{"id":"child"}`)}, PendingBytes: 1},
		{History: []json.RawMessage{json.RawMessage(`{"id":"a"}`), json.RawMessage(`{"id":"b"}`)}, MaxNodes: 1},
		{History: []json.RawMessage{json.RawMessage(`{"thread":{"id":"wrong-envelope"}}`)}},
	} {
		if _, err := f.c.SubscribeTree(context.Background(), "root", options); err == nil {
			t.Fatal("invalid/unbounded seed admitted")
		}
	}
	s, err := f.c.SubscribeTree(context.Background(), "child-1", codex.TreeOptions{EventQueue: 1})
	if err != nil {
		t.Fatal(err)
	}
	nextTree(t, s)
	if err = f.c.Call(context.Background(), "tree-step", nil, nil); err != nil {
		t.Fatal(err)
	}
	_ = f.c.Call(context.Background(), "exit", nil, nil)
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("full queue blocked termination")
	}
	if s.Err() == nil || errors.Is(s.Err(), codex.ErrSubscriptionOverflow) {
		t.Fatal("disconnect cause lost", s.Err())
	}
	e := nextTree(t, s)
	if e.Native == nil || string(e.Raw) != treeChildRaw {
		t.Fatal("accepted native evicted")
	}
	e = nextTree(t, s)
	if e.State != "disconnected" {
		t.Fatal("missing reserved terminal state")
	}
}
