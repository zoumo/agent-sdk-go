package lineage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func newGraph(t *testing.T, limits Limits) *Graph[string] {
	t.Helper()
	g, err := New[string](limits)
	require.NoError(t, err)
	return g
}
func accept(t *testing.T, err error) {
	t.Helper()
	require.NoError(t, err)
}
func limits() Limits { return Limits{MaxNodes: 16, MaxRelations: 15, MaxDepth: 8} }

// GraphSuite gives each stateful relationship scenario its own graph.
type GraphSuite struct {
	suite.Suite
	graph *Graph[string]
}

func (s *GraphSuite) SetupTest() { s.graph = newGraph(s.T(), limits()) }
func TestGraph(t *testing.T)     { suite.Run(t, new(GraphSuite)) }

func (s *GraphSuite) TestUnknownParentAndAncestors() {
	t := s.T()
	g := s.graph
	accept(t, g.Root("root"))
	accept(t, g.Confirm("leaf", "parent"))
	assert.False(t, g.Reaches("leaf", "root"), "unknown ancestry became root membership")
	parent, known := g.Parent("parent")
	assert.False(t, known)
	assert.Empty(t, parent)
	accept(t, g.Confirm("parent", "root"))
	assert.True(t, g.Reaches("leaf", "root"))
	assert.Equal(t, []string{"parent", "root"}, g.Ancestors("leaf"))
	depth, rooted, exists := g.Depth("leaf")
	assert.Equal(t, 2, depth)
	assert.True(t, rooted)
	assert.True(t, exists)
	_, _, exists = g.Depth("absent")
	assert.False(t, exists, "invented identity")
	assert.False(t, g.Reaches("absent", "absent"), "invented identity")
}

// Include vertex membership in snapshots: rejected candidate evidence must not
// consume capacity or change future admission.
func snapshot(g *Graph[string]) any {
	copyMap := func(m map[string]string) map[string]string {
		r := make(map[string]string)
		for k, v := range m {
			r[k] = v
		}
		return r
	}
	nodes, roots := make(map[string]bool), make(map[string]bool)
	for k := range g.nodes {
		nodes[k] = true
	}
	for k := range g.roots {
		roots[k] = true
	}
	return []any{nodes, roots, copyMap(g.parents)}
}
func reject(t *testing.T, g *Graph[string], want error, action func() error) {
	t.Helper()
	before := snapshot(g)
	assert.ErrorIs(t, action(), want)
	assert.Equal(t, before, snapshot(g), "rejection changed graph")
}

func (s *GraphSuite) TestConfirmRejectionsAreAtomic() {
	t := s.T()
	g := s.graph
	accept(t, g.Root("root"))
	accept(t, g.Confirm("a", "root"))
	accept(t, g.Confirm("b", "a"))
	reject(t, g, ErrConflict, func() error { return g.Confirm("a", "new-parent") })
	reject(t, g, ErrConflict, func() error { return g.Confirm("root", "new-parent") })
	reject(t, g, ErrCycle, func() error { return g.Confirm("unseen", "unseen") })
	accept(t, g.Confirm("x", "y"))
	reject(t, g, ErrCycle, func() error { return g.Confirm("y", "x") })
	reject(t, g, ErrConflict, func() error { return g.Root("a") })
	accept(t, g.Confirm("a", "root"))
	nodes, edges := g.Counts()
	assert.Equal(t, 5, nodes)
	assert.Equal(t, 3, edges)
}

func (s *GraphSuite) TestLiteralLimitsAndLateAncestors() {
	t := s.T()
	for _, tc := range []struct {
		name  string
		bound Limits
		want  error
	}{
		{"nodes", Limits{MaxNodes: 1, MaxRelations: 10, MaxDepth: 10}, ErrNodeLimit},
		{"relations", Limits{MaxNodes: 10, MaxRelations: 0, MaxDepth: 10}, ErrRelationLimit},
		{"depth", Limits{MaxNodes: 10, MaxRelations: 10, MaxDepth: 0}, ErrDepthLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGraph(t, tc.bound)
			accept(t, g.Root("root"))
			reject(t, g, tc.want, func() error { return g.Confirm("child", "root") })
		})
	}
	g := newGraph(t, Limits{MaxNodes: 10, MaxRelations: 10, MaxDepth: 2})
	accept(t, g.Confirm("leaf", "parent"))
	accept(t, g.Confirm("parent", "unknown"))
	reject(t, g, ErrDepthLimit, func() error { return g.Confirm("unknown", "new-ancestor") })
	for _, base := range []int{0, 1} {
		g := newGraph(t, Limits{MaxNodes: 10, MaxRelations: 10, MaxDepth: 1, UnknownDepth: base})
		accept(t, g.Ensure("a"))
		if base == 0 {
			accept(t, g.Confirm("b", "a"))
		} else {
			reject(t, g, ErrDepthLimit, func() error { return g.Confirm("b", "a") })
		}
	}

}

func (s *GraphSuite) TestMergeRepointsAllDescendants() {
	t := s.T()
	g := s.graph
	accept(t, g.Root("root"))
	accept(t, g.Ensure("keep"))
	accept(t, g.Confirm("drop", "root"))
	for _, child := range []string{"early", "late", "last"} {
		accept(t, g.Confirm(child, "drop"))
	}
	accept(t, g.Merge("keep", "drop"))
	assert.False(t, g.Has("drop"), "retained contracted identity")
	for _, child := range []string{"early", "late", "last"} {
		parent, known := g.Parent(child)
		assert.True(t, known, child)
		assert.Equal(t, "keep", parent, child)
		assert.True(t, g.Reaches(child, "root"), child)
	}
	nodes, edges := g.Counts()
	assert.Equal(t, 5, nodes)
	assert.Equal(t, 4, edges)
}

func (s *GraphSuite) TestMergeRejectsConflictsCyclesAndLateDepth() {
	t := s.T()
	g := s.graph
	accept(t, g.Confirm("a", "p"))
	accept(t, g.Confirm("b", "q"))
	reject(t, g, ErrConflict, func() error { return g.Merge("a", "b") })
	reject(t, g, ErrCycle, func() error { return g.Merge("a", "p") })
	reject(t, g, ErrUnknown, func() error { return g.Merge("a", "missing") })
	accept(t, g.Root("root"))
	reject(t, g, ErrConflict, func() error { return g.Merge("root", "q") })
	g = newGraph(t, Limits{MaxNodes: 12, MaxRelations: 12, MaxDepth: 2})
	accept(t, g.Root("root"))
	accept(t, g.Confirm("a", "root"))
	accept(t, g.Confirm("drop", "a"))
	accept(t, g.Confirm("leaf", "keep"))
	reject(t, g, ErrDepthLimit, func() error { return g.Merge("keep", "drop") })
}

func (s *GraphSuite) TestObserversOwnIndependentGraphs() {
	t := s.T()
	a, b := newGraph(t, limits()), newGraph(t, limits())
	accept(t, a.Confirm("child", "one"))
	accept(t, b.Confirm("child", "two"))
	for _, tc := range []struct {
		name  string
		graph *Graph[string]
		want  string
	}{{"first", a, "one"}, {"second", b, "two"}} {
		t.Run(tc.name, func(t *testing.T) { parent, _ := tc.graph.Parent("child"); assert.Equal(t, tc.want, parent) })
	}
}

func (s *GraphSuite) TestRootAndEdgeAdmissionRollBackTogether() {
	t := s.T()
	g := newGraph(t, Limits{MaxNodes: 2, MaxRelations: 0, MaxDepth: 2})
	accept(t, g.Ensure("child"))
	reject(t, g, ErrRelationLimit, func() error { return g.ConfirmRoot("child", "new-root") })
	accept(t, g.Ensure("unknown"))
	reject(t, g, ErrRelationLimit, func() error { return g.ConfirmRoot("child", "unknown") })
	assert.False(t, g.IsRoot("unknown"), "rejected root designation retained")
	g = newGraph(t, limits())
	accept(t, g.ConfirmRoot("child", "root"))
	assert.True(t, g.Reaches("child", "root"), "root edge not confirmed")
	assert.True(t, g.IsRoot("root"), "root edge not confirmed")
}

// New is a pure constructor: exercise its rejected inputs as independent cases.
func TestNewRejectsInvalidLimits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits Limits
	}{
		{"zero", Limits{}},
		{"negative relations", Limits{MaxNodes: 1, MaxRelations: -1}},
		{"negative depth", Limits{MaxNodes: 1, MaxDepth: -1}},
		{"invalid unknown depth", Limits{MaxNodes: 1, UnknownDepth: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New[string](tc.limits)
			assert.ErrorIs(t, err, ErrInvalidLimits, "limits=%+v", tc.limits)
		})
	}
}
