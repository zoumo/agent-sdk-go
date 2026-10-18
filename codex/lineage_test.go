package codex

import (
	"errors"
	"testing"

	"github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/lineage"
)

func lineageSubscription(t *testing.T, maxNodes int) *Subscription {
	t.Helper()
	g, err := lineage.New[string](lineage.Limits{MaxNodes: maxNodes, MaxRelations: maxNodes - 1, MaxDepth: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Root("root"); err != nil {
		t.Fatal(err)
	}
	return &Subscription{client: &Client{}, root: "root", lineage: g, children: make(map[string]*treeRelation), events: make(chan types.Event, 32), done: make(chan struct{}), options: types.SubscriptionOptions{EventQueue: 31, MaxDepth: 4}}
}

func TestSubscriptionSharedLineageResolvesLateAncestors(t *testing.T) {
	s := lineageSubscription(t, 8)
	s.relationLocked("grand", "child", types.SubagentEvidence{}, false)
	if s.member("grand") {
		t.Fatal("unconfirmed ancestor proved membership")
	}
	s.relationLocked("child", "root", types.SubagentEvidence{}, false)
	s.announceLocked()
	if !s.member("grand") {
		t.Fatal("confirmed ancestry did not route descendant")
	}
	for _, id := range []string{"child", "grand"} {
		e := <-s.events
		a, ok := e.Data.(*types.SubagentAssociated)
		if !ok || a.ThreadID != id {
			t.Fatalf("ancestor order: %#v", e.Data)
		}
	}
	// A rejected candidate cannot overwrite the routing authority or reserve IDs.
	s.relationLocked("grand", "foreign", types.SubagentEvidence{}, false)
	if parent, _ := s.lineage.Parent("grand"); parent != "child" || s.lineage.Has("foreign") {
		t.Fatal(parent)
	}
	if state, ok := (<-s.events).Data.(*types.ObservationState); !ok || state.Reason != "relation-conflict" {
		t.Fatal(state)
	}
}

func TestSubscriptionCountsUnknownParentOnlyVertices(t *testing.T) {
	s := lineageSubscription(t, 2)
	g := s.lineage
	s.relationLocked("leaf", "unknown", types.SubagentEvidence{}, false)
	if !s.closed || !errors.Is(s.cause, ErrLimit) {
		t.Fatal(s.closed, s.cause)
	}
	if nodes, edges := g.Counts(); nodes != 1 || edges != 0 {
		t.Fatal("rejected evidence retained", nodes, edges)
	}
}
