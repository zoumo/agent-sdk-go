package lineage

import (
	"errors"
	"reflect"
	"testing"
)

func newGraph(t *testing.T, limits Limits) *Graph[string] {
	t.Helper()
	g, err := New[string](limits)
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func accept(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func limits() Limits { return Limits{MaxNodes: 16, MaxRelations: 15, MaxDepth: 8} }

func TestUnknownParentAndAncestors(t *testing.T) {
	g := newGraph(t, limits())
	accept(t, g.Root("root"))
	accept(t, g.Confirm("leaf", "parent"))
	if g.Reaches("leaf", "root") {
		t.Fatal("unknown ancestry became root membership")
	}
	if parent, known := g.Parent("parent"); known || parent != "" {
		t.Fatal(parent, known)
	}
	accept(t, g.Confirm("parent", "root"))
	if !g.Reaches("leaf", "root") || !reflect.DeepEqual(g.Ancestors("leaf"), []string{"parent", "root"}) {
		t.Fatal(g.Ancestors("leaf"))
	}
	if depth, rooted, exists := g.Depth("leaf"); depth != 2 || !rooted || !exists {
		t.Fatal(depth, rooted, exists)
	}
	if _, _, exists := g.Depth("absent"); exists || g.Reaches("absent", "absent") {
		t.Fatal("invented identity")
	}
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
	if err := action(); !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
	if !reflect.DeepEqual(before, snapshot(g)) {
		t.Fatal("rejection changed graph")
	}
}

func TestConfirmRejectionsAreAtomic(t *testing.T) {
	g := newGraph(t, limits())
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
	if nodes, edges := g.Counts(); nodes != 5 || edges != 3 {
		t.Fatal(nodes, edges)
	}
}

func TestLiteralLimitsAndLateAncestors(t *testing.T) {
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
	for _, l := range []Limits{{}, {MaxNodes: 1, MaxRelations: -1}, {MaxNodes: 1, MaxDepth: -1}, {MaxNodes: 1, UnknownDepth: 2}} {
		if _, err := New[string](l); !errors.Is(err, ErrInvalidLimits) {
			t.Fatal(l, err)
		}
	}
}

func TestMergeRepointsAllDescendants(t *testing.T) {
	g := newGraph(t, limits())
	accept(t, g.Root("root"))
	accept(t, g.Ensure("keep"))
	accept(t, g.Confirm("drop", "root"))
	for _, child := range []string{"early", "late", "last"} {
		accept(t, g.Confirm(child, "drop"))
	}
	accept(t, g.Merge("keep", "drop"))
	if g.Has("drop") {
		t.Fatal("retained contracted identity")
	}
	for _, child := range []string{"early", "late", "last"} {
		parent, known := g.Parent(child)
		if !known || parent != "keep" || !g.Reaches(child, "root") {
			t.Fatal(child, parent, known)
		}
	}
	if nodes, edges := g.Counts(); nodes != 5 || edges != 4 {
		t.Fatal(nodes, edges)
	}
}

func TestMergeRejectsConflictsCyclesAndLateDepth(t *testing.T) {
	g := newGraph(t, limits())
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

func TestObserversOwnIndependentGraphs(t *testing.T) {
	a, b := newGraph(t, limits()), newGraph(t, limits())
	accept(t, a.Confirm("child", "one"))
	accept(t, b.Confirm("child", "two"))
	if parent, _ := a.Parent("child"); parent != "one" {
		t.Fatal(parent)
	}
	if parent, _ := b.Parent("child"); parent != "two" {
		t.Fatal(parent)
	}
}

func TestRootAndEdgeAdmissionRollBackTogether(t *testing.T) {
	g := newGraph(t, Limits{MaxNodes: 2, MaxRelations: 0, MaxDepth: 2})
	accept(t, g.Ensure("child"))
	reject(t, g, ErrRelationLimit, func() error { return g.ConfirmRoot("child", "new-root") })
	accept(t, g.Ensure("unknown"))
	reject(t, g, ErrRelationLimit, func() error { return g.ConfirmRoot("child", "unknown") })
	if g.IsRoot("unknown") {
		t.Fatal("rejected root designation retained")
	}
	g = newGraph(t, limits())
	accept(t, g.ConfirmRoot("child", "root"))
	if !g.Reaches("child", "root") || !g.IsRoot("root") {
		t.Fatal("root edge not confirmed")
	}
}
