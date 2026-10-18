package codex

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/lineage"
)

func lineageSubscription(t *testing.T, maxNodes int) *Subscription {
	t.Helper()
	g, err := lineage.New[string](lineage.Limits{MaxNodes: maxNodes, MaxRelations: maxNodes - 1, MaxDepth: 4})
	require.NoError(t, err)
	require.NoError(t, g.Root("root"))
	return &Subscription{client: &Client{}, root: "root", lineage: g, children: make(map[string]*treeRelation), events: make(chan types.Event, 32), done: make(chan struct{}), options: types.SubscriptionOptions{EventQueue: 31, MaxDepth: 4}}
}

func TestSubscriptionSharedLineageResolvesLateAncestors(t *testing.T) {
	s := lineageSubscription(t, 8)
	s.relationLocked("grand", "child", types.SubagentEvidence{}, false)
	require.False(t, s.member("grand"), "unconfirmed ancestor proved membership")
	s.relationLocked("child", "root", types.SubagentEvidence{}, false)
	s.announceLocked()
	require.True(t, s.member("grand"), "confirmed ancestry did not route descendant")
	for _, id := range []string{"child", "grand"} {
		e := <-s.events
		a, ok := e.Data.(*types.SubagentAssociated)
		require.True(t, ok, "ancestor order: %#v", e.Data)
		assert.Equal(t, id, a.ThreadID)
	}
	// A rejected candidate cannot overwrite the routing authority or reserve IDs.
	s.relationLocked("grand", "foreign", types.SubagentEvidence{}, false)
	parent, _ := s.lineage.Parent("grand")
	assert.Equal(t, "child", parent)
	assert.False(t, s.lineage.Has("foreign"))
	state, ok := (<-s.events).Data.(*types.ObservationState)
	require.True(t, ok)
	assert.Equal(t, "relation-conflict", state.Reason)
}

func TestSubscriptionCountsUnknownParentOnlyVertices(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		maxNodes, nodes, edges int
		rejected               bool
	}{
		{name: "unknown parent exceeds capacity", maxNodes: 2, nodes: 1, edges: 0, rejected: true},
		{name: "unknown parent fits capacity", maxNodes: 3, nodes: 3, edges: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := lineageSubscription(t, tc.maxNodes)
			g := s.lineage
			s.relationLocked("leaf", "unknown", types.SubagentEvidence{}, false)
			assert.Equal(t, tc.rejected, s.closed)
			if tc.rejected {
				assert.ErrorIs(t, s.cause, ErrLimit)
			} else {
				assert.NoError(t, s.cause)
			}
			nodes, edges := g.Counts()
			assert.Equal(t, tc.nodes, nodes, "rejected evidence must not be retained")
			assert.Equal(t, tc.edges, edges, "rejected evidence must not be retained")
		})
	}
}
