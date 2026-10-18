package codex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/zoumo/agent-sdk-go/codex/types"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	codex "github.com/zoumo/agent-sdk-go/codex"
)

// Synthetic pinned wire shapes, through the same public transport fixture as 01.
func handleNestedTreePeer(mode string, m fakeEnvelope, send func(json.RawMessage, string)) bool {
	if mode != "tree-nested" && mode != "tree-pending" {
		return false
	}
	emit := func(method, params string) { fmt.Fprintf(os.Stdout, "{\"method\":%q,\"params\":%s}\n", method, params) }
	relation := func(child, parent string) {
		emit("thread/started", `{"thread":{"id":"`+child+`","parentThreadId":"`+parent+`","future":null}}`)
	}
	spawn := func(parent, child, turn string) {
		p := strings.ReplaceAll(treeSpawnParams("spawnAgent", "completed"), "native-thread-42", parent)
		p = strings.ReplaceAll(p, `"child-1","child-1","child-2"`, `"`+child+`"`)
		p = strings.ReplaceAll(p, "root-turn", turn)
		emit("item/completed", p)
	}
	switch m.Method {
	case "turn/start":
		if mode == "tree-pending" {
			fmt.Fprintln(os.Stdout, treeChildRaw)
			emit("future/late", `{"threadId":"late","opaque":null}`)
		} else {
			// Grandchild events, then its parent relation, arrive before the root link.
			emit("future/grand", `{"threadId":"grand","ordinal":1,"future":null}`)
			emit("future/grand", `{"threadId":"grand","ordinal":2}`)
			relation("grand", "child-1")
			fmt.Fprintln(os.Stdout, treeChildRaw)
			// Native source gives the same parent independently of collab evidence.
			emit("thread/started", `{"thread":{"id":"child-1","source":{"subAgent":{"thread_spawn":{"parent_thread_id":"native-thread-42","depth":1}}}}}`)
			emit("item/started", treeSpawnParams("spawnAgent", "completed"))
			spawn("native-thread-42", "child-1", "root-turn")
			spawn("native-thread-42", "child-1", "root-turn")  // reliable terminal replay
			spawn("native-thread-42", "child-1", "second-run") // distinct native turn
			spawn("native-thread-42", "child-1", "root-turn")  // cache-eviction probe
			spawn("child-1", "grand", "child-turn")            // confirms previously associated grandchild
			relation("grand", "native-thread-42")              // retain original parent
			relation("child-1", "grand")                       // cycle
			for i := 0; i < 2; i++ {
				emit("item/completed", `{"threadId":"grand","turnId":"g-turn","item":{"type":"futureItem","id":"unknown-occurrence","payload":null}}`)
			}
			emit("item/started", `{"threadId":"grand","turnId":"g-turn","item":{"type":"subAgentActivity","id":"activity","agentThreadId":"other","agentPath":"/guessed/child","kind":"future-kind"}}`)
			fmt.Fprintln(os.Stdout, treeChildRaw) // same delta, no occurrence ID
			// An early terminal event flushed from pending must dedup a later replay.
			spawn("orphan", "leaf", "o-turn")
			relation("orphan", "grand")
			spawn("orphan", "leaf", "o-turn")
		}
		emit("future/done", `{"threadId":"native-thread-42"}`)
		send(m.ID, `{"turn":`+nativeTurn+`}`)
	case "tree-link":
		relation("child-1", "native-thread-42")
		relation("late", "native-thread-42")
		emit("future/done", `{"threadId":"native-thread-42"}`)
		send(m.ID, `{}`)
	default:
		return false
	}
	return true
}

func (obs *observationSuite) TestTreeNestedOrderingAndIdentity() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := obs.fixture(t, "tree-nested", codex.Limits{})
			a, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			cleanupSubscription(t, a)
			require.NoError(t, err, err)
			defer a.Close()
			b, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			cleanupSubscription(t, b)
			require.NoError(t, err, err)
			defer b.Close()
			treeStart(t, f.c)
			events := treeUntilDone(t, a)
			others := treeUntilDone(t, b)
			require.Equal(t, len(others), len(events), "independent consumers differ")
			associated, created := map[string]int{}, map[string]int{}
			parents := map[string]string{"child-1": "native-thread-42", "grand": "child-1", "orphan": "grand", "leaf": "orphan"}
			grand, delta, terminal, conflicts, cycles, unknown, partial, unknownTerminal := 0, 0, 0, 0, 0, 0, 0, 0
			for _, e := range events {
				if e.Kind == associatedRecord || e.Kind == createdRecord {
					require.Equal(t, parents[e.Source.ThreadID], e.ParentThreadID, "parent %+v", e)
					require.False(t, e.ParentThreadID != "native-thread-42" && associated[e.ParentThreadID]+created[e.ParentThreadID] == 0, "ancestor announced too late")
					if e.Kind == associatedRecord {
						associated[e.Source.ThreadID]++
					} else {
						created[e.Source.ThreadID]++
						require.False(t, e.Evidence.Source.ThreadID != e.ParentThreadID || e.Evidence.Source.TurnID == "", "creation source lost: %+v", e)
					}
				}
				if e.Reason == "relation-conflict" {
					conflicts++
					require.False(t, e.Evidence == nil || e.PreviousEvidence == nil || e.PreviousEvidence.ParentThreadID != "child-1", "conflict evidence lost")
				}
				if e.Reason == "relation-cycle" {
					cycles++
				}
				if e.Native == nil {
					continue
				}
				if e.Source.ThreadID != "native-thread-42" && e.Source.ThreadID != "" {
					require.False(t, associated[e.Source.ThreadID]+created[e.Source.ThreadID] == 0, "native before relation: %+v", e)
				}
				switch e.Native.Method {
				case "future/grand":
					grand++
					require.True(t, strings.Contains(string(e.Raw), fmt.Sprint(`"ordinal":`, grand)), "pending source order or Raw lost")
				case "item/agentMessage/delta":
					delta++
					require.Equal(t, treeChildRaw, string(e.Raw), "delta changed")
				case "item/completed":
					if strings.Contains(string(e.Raw), "futureItem") {
						unknownTerminal++
						break
					}
					terminal++
				case "item/started":
					if e.Source.ThreadID == "native-thread-42" {
						partial++
						break
					}
					unknown++
					require.True(t, strings.Contains(string(e.Raw), "future-kind"), "unknown activity lost")
				}
			}
			require.False(t, associated["child-1"] != 1 || associated["grand"] != 1 || created["child-1"] != 1 || created["grand"] != 1 || created["leaf"] != 1 || grand != 2 || delta != 2 || terminal != 4 || conflicts != 1 || cycles != 1 || unknown != 1 || partial != 1 || unknownTerminal != 2, "counts associated=%v created=%v grand=%d delta=%d terminal=%d conflicts=%d cycles=%d unknown=%d", associated, created, grand, delta, terminal, conflicts, cycles, unknown)
			require.Equal(t, 3, len(trace(t, f.trace)), "extra child RPC")
		})
	}
}

func (obs *observationSuite) TestTreePendingLimitsAndNoReplay() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			for _, options := range []types.SubscriptionOptions{
				{PendingEvents: 1, PendingAge: 20 * time.Millisecond},
				{PendingBytes: 1, PendingAge: 20 * time.Millisecond},
				{PendingAge: 20 * time.Millisecond},
			} {
				f := obs.fixture(t, "tree-pending", codex.Limits{})
				s, err := f.c.Subscribe(context.Background(), "native-thread-42", options)
				cleanupSubscription(t, s)
				require.NoError(t, err, err)
				treeStart(t, f.c)
				count := 0
				for count < 2 {
					e := nextTree(t, s)
					if e.Native != nil && (e.Native.Method == "item/agentMessage/delta" || e.Native.Method == "future/late") {
						count++
						require.False(t, len(e.Raw) == 0, "unresolved Raw lost")
						require.False(t, e.Source.ThreadID == "child-1" && string(e.Raw) != treeChildRaw, "changed unresolved Raw")
					}
				}
				{
					err = f.c.Call(context.Background(), "tree-link", nil, nil)
					require.NoError(t, err, err)
				}
				for _, e := range treeUntilDone(t, s) {
					require.False(t, e.Native != nil && (e.Native.Method == "item/agentMessage/delta" || e.Native.Method == "future/late"), "already delivered unresolved event replayed")
				}
				s.Close()
			}
		})
	}
}

func (obs *observationSuite) TestTreeDepthAndDedupBounds() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := obs.fixture(t, "tree-nested", codex.Limits{})
			s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{MaxDepth: 1})
			cleanupSubscription(t, s)
			require.NoError(t, err, err)
			treeStart(t, f.c)
			select {
			case <-s.Done():
			case <-time.After(time.Second):
				require.FailNow(t, "test invariant", "depth not bounded")
			}
			require.ErrorIs(t, s.Err(), codex.ErrLimit, s.Err())
			// A tiny cache permits older terminal identities to be delivered again; it
			// never trades bounded memory for a cross-epoch exactly-once claim.
			s, err = f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{DedupEntries: 1})
			cleanupSubscription(t, s)
			require.NoError(t, err, err)
			defer s.Close()
			treeStart(t, f.c)
			terminals := 0
			for _, e := range treeUntilDone(t, s) {
				if e.Native != nil && e.Native.Method == "item/completed" && !strings.Contains(string(e.Raw), "futureItem") {
					terminals++
				}
			}
			require.Equal(t, 5, terminals, "bounded identity eviction: %d", terminals)
			{
				err = f.c.Call(context.Background(), "echo", true, nil)
				require.NoError(t, err, err)
			}
		})
	}
}
