package codex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
		require.True(t, ok, "tree closed: %v", s.Err())
		return recordView(e)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "test invariant", "tree receive timeout")
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
	require.NoError(t, err, err)
}

func (obs *observationSuite) TestTreeLiveChildStream() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := obs.fixture(t, "tree-success", codex.Limits{})
			raw, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			cleanupSubscription(t, raw)
			require.NoError(t, err, err)
			defer raw.Close()
			a, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			cleanupSubscription(t, a)
			require.NoError(t, err, err)
			defer a.Close()
			b, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			cleanupSubscription(t, b)
			require.NoError(t, err, err)
			defer b.Close()
			// Reader emits to registered observers even before this RPC returns.
			treeStart(t, f.c)
			events := treeUntilDone(t, a)
			others := treeUntilDone(t, b)
			require.Equal(t, len(others), len(events), "observers differ")
			require.False(t, events[0].Kind != stateRecord || events[0].State != "incomplete", "missing honest observation limits")
			created := 0
			childDeltas := 0
			child2 := false
			bad := false
			sawNull := false
			sawAbsent := false
			for i, e := range events {
				if e.Kind == createdRecord {
					created++
					require.False(t, !e.CreationConfirmed || e.ParentThreadID != "native-thread-42" || e.Native != nil || e.Raw != nil || e.Evidence.Method != "item/completed" || e.Evidence.Source.ItemID != "spawn-1" || e.Evidence.Source.TurnID != "root-turn" || len(e.Evidence.ReceiverThreadIDs) != 3, "creation evidence: %+v", e)
				}
				if e.Native == nil {
					continue
				}
				require.False(t, e.Source.ThreadID == "unrelated-root", "unrelated root leaked")
				if e.Native.Method == "item/agentMessage/delta" && e.Native.DecodeError == nil {
					childDeltas++
					require.False(t, created != 2 || e.Source.ThreadID != "child-1" || e.Source.TurnID != "child-turn" || e.Source.ItemID != "child-item" || string(e.Raw) != treeChildRaw || string(e.Native.Params) != treeChildParams, "child native: %+v", e)
					data := e.Native.Data.(*types.AgentMessageDeltaNotification)
					require.Equal(t, "[null,9007199254740993]", string(data.UnknownFields["future"]), "lost unknown fields")
					// Mutating one observer cannot corrupt another, including typed storage.
					e.Raw[0] = '!'
					e.Native.Params[0] = '!'
					data.Delta = "changed"
					other := others[i]
					require.False(t, string(other.Raw) != treeChildRaw || string(other.Native.Params) != treeChildParams || other.Native.Data.(*types.AgentMessageDeltaNotification).Delta != "child text", "tree buffers shared")
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
			require.False(t, created != 2 || childDeltas != 2 || !child2 || !bad || !sawNull || !sawAbsent, "coverage: created=%d deltas=%d child2=%v bad=%v null=%v absent=%v", created, childDeltas, child2, bad, sawNull, sawAbsent)
			rawCount := 0
			rawOther := 0
			for {
				e := nextEvent(t, raw)
				rawCount++
				if e.Method == "future/other" || e.Method == "thread/started" {
					rawOther++
				}
				require.False(t, e.Method == "item/agentMessage/delta" && e.DecodeError == nil && string(e.Params) != treeChildParams, "native subscriber corrupted")
				if e.Method == "future/done" {
					break
				}
			}
			require.False(t, rawCount != 9 || rawOther != 0, "managed native routing changed: count=%d other=%d", rawCount, rawOther)
			calls := trace(t, f.trace)
			require.False(t, len(calls) != 3 || calls[0].Method != "initialize" || calls[1].Method != "initialized" || calls[2].Method != "turn/start", "unexpected child/subscribe RPCs: %+v", calls)
		})
	}
}

func (obs *observationSuite) TestTreeFailedSpawnAndResumeAreNotCreated() {
	t := obs.T()
	for _, mode := range []string{"tree-failed", "tree-resume"} {
		t.Run(mode, func(t *testing.T) {
			f := obs.fixture(t, mode, codex.Limits{})
			s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			cleanupSubscription(t, s)
			require.NoError(t, err, err)
			defer s.Close()
			treeStart(t, f.c)
			associated := 0
			child := 0
			nativeSpawn := 0
			for _, e := range treeUntilDone(t, s) {
				require.False(t, e.Kind == createdRecord, "false creation")
				if e.Kind == associatedRecord {
					associated++
					require.False(t, e.CreationConfirmed, "false confirmation")
				}
				if (e.Source.ThreadID == "child-1" || e.Source.ThreadID == "child-2") && e.Native != nil {
					child++
				}
				if e.Native != nil && e.Native.Method == "item/completed" {
					nativeSpawn++
				}
			}
			require.Equal(t, 1, nativeSpawn, "lost native collab")
			require.False(t, mode == "tree-failed" && (associated != 0 || child != 0), "failed spawn established child")
			require.False(t, mode == "tree-resume" && (associated != 2 || child != 4), "association coverage: %d %d", associated, child)
		})
	}
}

func (obs *observationSuite) TestTreeBoundsAndLocalClose() {
	t := obs.T()
	f := obs.fixture(t, "tree-success", codex.Limits{})
	slow, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{EventQueue: 1})
	cleanupSubscription(t, slow)
	require.NoError(t, err, err)
	nextTree(t, slow) // consume initial diagnostic
	healthy, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
	cleanupSubscription(t, healthy)
	require.NoError(t, err, err)
	treeStart(t, f.c)
	treeUntilDone(t, healthy)
	require.NoError(t, slow.Err())
	slowRecords := treeUntilDone(t, slow)
	require.NotEmpty(t, slowRecords)
	require.NoError(t, slow.Close())
	awaitSubscription(t, slow, nil)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = healthy.Close(); _ = healthy.Err() }()
	}
	wg.Wait()
	require.NoError(t, healthy.Err(), healthy.Err())
	{
		err = f.c.Call(context.Background(), "echo", true, nil)
		require.NoError(t, err, err)
	}
	for _, m := range trace(t, f.trace) {
		require.False(t, m.Method == "thread/resume" || m.Method == "thread/unsubscribe" || m.Method == "turn/interrupt", "local close sent RPC: %s", m.Method)
	}
}

func (obs *observationSuite) TestTreeAdmissionAndNodeBound() {
	t := obs.T()
	f := obs.fixture(t, "tree-success", codex.Limits{Subscriptions: 1})
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
		{
			_, err := f.c.Subscribe(tc.ctx, tc.root, tc.options)
			require.False(t, err == nil, "invalid admission accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	{
		_, err := f.c.Subscribe(ctx, "root", types.SubscriptionOptions{})
		require.ErrorIs(t, err, context.Canceled, err)
	}
	s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{MaxNodes: 1})
	cleanupSubscription(t, s)
	require.NoError(t, err, err)
	{
		_, err = f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
		require.ErrorIs(t, err, codex.ErrLimit, "shared admission limit: %v", err)
	}
	treeStart(t, f.c)
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		require.FailNow(t, "test invariant", "node limit not signaled")
	}
	require.ErrorIs(t, s.Err(), codex.ErrLimit, s.Err())
	raw, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
	cleanupSubscription(t, raw)
	require.NoError(t, err, "tree did not release slot:", err)
	defer raw.Close()
}
