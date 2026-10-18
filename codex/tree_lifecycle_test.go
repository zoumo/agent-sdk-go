package codex_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/zoumo/agent-sdk-go/codex/types"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	codex "github.com/zoumo/agent-sdk-go/codex"
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

func (obs *observationSuite) TestTreeParentIdleChildActive() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := obs.fixture(t, "tree-lifecycle", codex.Limits{})
			s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			cleanupSubscription(t, s)
			require.NoError(t, err, err)
			defer s.Close()
			treeStart(t, f.c)
			activities := 0
			rootIdle, rootDone, childActive, delta, interrupted, nextRun := false, false, false, false, false, false
			for _, e := range treeUntilDone(t, s) {
				if e.Native == nil {
					continue
				}
				if e.Native.Method == "item/completed" && strings.Contains(string(e.Raw), "subAgentActivity") {
					require.NoError(t, e.Native.DecodeError, e.Native.DecodeError)
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
					require.False(t, !rootIdle || !childActive, "parent idle lost or child status changed")
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
			require.False(t, !rootDone || !delta || !interrupted || !nextRun || activities != 2, "lifecycle: %v %v %v %v", rootDone, delta, interrupted, nextRun)
			select {
			case <-s.Done():
				require.FailNow(t, "test invariant", "parent ended observation")
			default:
			}
			{
				err = f.c.Call(context.Background(), "tree-step", nil, nil)
				require.NoError(t, err, err)
			}
			{
				e := nextTree(t, s)
				require.False(t, e.Native == nil || string(e.Raw) != treeChildRaw, "child no longer observed")
			}
		})
	}
}

func (obs *observationSuite) TestTreeCloseCancelSharedSource() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := obs.fixture(t, "tree-lifecycle", codex.Limits{})
			a, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{History: []json.RawMessage{json.RawMessage(`{"id":"child-1","parentThreadId":"native-thread-42"}`)}})
			cleanupSubscription(t, a)
			require.NoError(t, err, err)
			b, err := f.c.Subscribe(context.Background(), "child-1", types.SubscriptionOptions{})
			cleanupSubscription(t, b)
			require.NoError(t, err, err)
			defer b.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			canceled, err := f.c.Subscribe(ctx, "child-1", types.SubscriptionOptions{})
			cleanupSubscription(t, canceled)
			require.NoError(t, err, err)
			raw, err := f.c.Subscribe(context.Background(), "child-1", types.SubscriptionOptions{})
			cleanupSubscription(t, raw)
			require.NoError(t, err, err)
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
				{
					err = f.c.Call(context.Background(), "tree-step", nil, nil)
					require.NoError(t, err, err)
				}
			}
			wg.Wait()
			select {
			case <-canceled.Done():
			case <-time.After(time.Second):
				require.FailNow(t, "test invariant", "cancel blocked")
			}
			require.False(t, !errors.Is(canceled.Err(), context.Canceled) || a.Err() != nil, "termination causes", canceled.Err(), a.Err())
			for i := 0; i < 8; i++ {
				e := nextTree(t, b)
				require.Equal(t, treeChildRaw, string(e.Raw), "shared source stopped")
				{
					e := nextEvent(t, raw)
					require.Equal(t, treeChildParams, string(e.Params), "raw stopped")
				}
			}
			{
				err = f.c.Call(context.Background(), "echo", true, nil)
				require.NoError(t, err, err)
			}
			for _, call := range trace(t, f.trace) {
				require.False(t, call.Method != "initialize" && call.Method != "initialized" && call.Method != "tree-step" && call.Method != "echo", "unexpected RPC %s", call.Method)
			}
		})
	}
}

func (obs *observationSuite) TestTreeDisconnectHistoryEpoch() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			// Root history supplies successful historical spawn evidence; grandchild is
			// intentionally before its parent. No native snapshot is replayed as live.
			var spawn struct {
				Item json.RawMessage `json:"item"`
			}
			{
				err := json.Unmarshal([]byte(treeSpawnParams("spawnAgent", "completed")), &spawn)
				require.NoError(t, err, err)
			}
			history := []json.RawMessage{
				json.RawMessage(`{"id":"known-unlinked"}`),
				json.RawMessage(`{"id":"grand","parentThreadId":"child-1"}`),
				json.RawMessage(`{"id":"native-thread-42","turns":[{"id":"root-turn","items":[` + string(spawn.Item) + `]}]}`),
				json.RawMessage(`{"id":"child-1","source":{"subAgent":{"thread_spawn":{"parent_thread_id":"native-thread-42"}}},"status":{"type":"active"}}`),
			}
			for epoch := 0; epoch < 2; epoch++ {
				f := obs.fixture(t, "tree-history", codex.Limits{})
				s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{History: history})
				cleanupSubscription(t, s)
				require.NoError(t, err, err)
				treeStart(t, f.c)
				associated, created, terminals, gap := 0, 0, 0, false
				discovered := 0
				for _, e := range treeUntilDone(t, s) {
					if e.Reason == "history-recovery-gap" {
						gap = e.State == "incomplete"
					}
					if e.Kind == associatedRecord {
						if e.Source.ThreadID == "known-unlinked" {
							discovered++
							require.False(t, e.Provenance != "live" || e.CreationConfirmed, "unlinked historical entity recreated")
							continue
						}
						associated++
						require.False(t, e.Provenance != "history" || e.CreationConfirmed || e.Evidence.Provenance != "history", "false history creation %+v", e)
					}
					if e.Kind == createdRecord {
						created++
						require.False(t, e.Source.ThreadID != "live-child" || e.Provenance != "live", "historical entity recreated")
					}
					if e.Native != nil && e.Native.Method == "item/completed" {
						terminals++
					}
				}
				require.False(t, associated != 3 || created != 1 || terminals != 2 || !gap || discovered != 1, "seed coverage %d %d %d %v", associated, created, terminals, gap)
				// Leave one admitted native event unread, then abruptly lose the peer.
				{
					err = f.c.Call(context.Background(), "tree-step", nil, nil)
					require.NoError(t, err, err)
				}
				_ = f.c.Call(context.Background(), "exit", nil, nil)
				select {
				case <-s.Done():
				case <-time.After(5 * time.Second):
					require.FailNow(t, "test invariant", "disconnect not signaled")
				}
				require.False(t, s.Err() == nil, "EOF treated as success")
				remaining := []subscriptionRecord{}
				for event := range s.Events() {
					e := recordView(event)
					remaining = append(remaining, e)
				}
				require.False(t, len(remaining) != 2 || remaining[0].Native == nil || string(remaining[0].Raw) != treeChildRaw || remaining[1].State != "disconnected" || remaining[1].Reason != "permanent-gap", "lost queued data or disconnect: %+v", remaining)
				{
					err = s.Close()
					require.False(t, err != nil || s.Err() == nil, "close erased failure")
				}
				calls := trace(t, f.trace)
				for _, call := range calls {
					require.False(t, call.Method != "initialize" && call.Method != "initialized" && call.Method != "turn/start" && call.Method != "tree-step" && call.Method != "exit", "automatic recovery RPC %s", call.Method)
				}
			}
		})
	}
}

func (obs *observationSuite) TestTreeDisconnectFullQueueAndSeedBounds() {
	t := obs.T()
	f := obs.fixture(t, "tree-lifecycle", codex.Limits{})
	for _, options := range []types.SubscriptionOptions{
		{History: []json.RawMessage{json.RawMessage(`{"id":"child"}`)}, PendingBytes: 1},
		{History: []json.RawMessage{json.RawMessage(`{"id":"a"}`), json.RawMessage(`{"id":"b"}`)}, MaxNodes: 1},
		{History: []json.RawMessage{json.RawMessage(`{"thread":{"id":"wrong-envelope"}}`)}},
	} {
		{
			_, err := f.c.Subscribe(context.Background(), "root", options)
			require.False(t, err == nil, "invalid/unbounded seed admitted")
		}
	}
	s, err := f.c.Subscribe(context.Background(), "child-1", types.SubscriptionOptions{EventQueue: 1})
	cleanupSubscription(t, s)
	require.NoError(t, err, err)
	nextTree(t, s)
	{
		err = f.c.Call(context.Background(), "tree-step", nil, nil)
		require.NoError(t, err, err)
	}
	_ = f.c.Call(context.Background(), "exit", nil, nil)
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		require.FailNow(t, "test invariant", "full queue blocked termination")
	}
	require.False(t, s.Err() == nil || errors.Is(s.Err(), codex.ErrSubscriptionOverflow), "disconnect cause lost", s.Err())
	e := nextTree(t, s)
	require.False(t, e.Native == nil || string(e.Raw) != treeChildRaw, "accepted native evicted")
	e = nextTree(t, s)
	require.Equal(t, "disconnected", e.State, "missing reserved terminal state")
}
