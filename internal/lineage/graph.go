// Package lineage maintains bounded, confirmed parent relationships. Callers
// own identity resolution, evidence, event parsing and synchronization.
package lineage

import "errors"

var (
	ErrInvalidLimits = errors.New("lineage: invalid limits")
	ErrConflict      = errors.New("lineage: conflicting parent")
	ErrCycle         = errors.New("lineage: cycle")
	ErrNodeLimit     = errors.New("lineage: node limit")
	ErrRelationLimit = errors.New("lineage: relation limit")
	ErrDepthLimit    = errors.New("lineage: depth limit")
	ErrUnknown       = errors.New("lineage: unknown node")
)

// Limits are literal bounds; zero does not select a default. UnknownDepth is
// the depth of a parent-unknown component's top node (zero or one). Roots have
// depth zero. All vertices, including roots and parent-only vertices, count.
type Limits struct {
	MaxNodes, MaxRelations, MaxDepth int
	UnknownDepth                     int
}

// Graph is owned by one observer. It is not safe for concurrent mutation.
// A present vertex without a parent is unknown unless explicitly marked root.
type Graph[K comparable] struct {
	limits  Limits
	nodes   map[K]struct{}
	roots   map[K]struct{}
	parents map[K]K
}

func New[K comparable](limits Limits) (*Graph[K], error) {
	if limits.MaxNodes < 1 || limits.MaxRelations < 0 || limits.MaxDepth < 0 || limits.UnknownDepth < 0 || limits.UnknownDepth > 1 {
		return nil, ErrInvalidLimits
	}
	return &Graph[K]{limits: limits, nodes: make(map[K]struct{}), roots: make(map[K]struct{}), parents: make(map[K]K)}, nil
}

func (g *Graph[K]) Has(id K) bool                  { _, ok := g.nodes[id]; return ok }
func (g *Graph[K]) IsRoot(id K) bool               { _, ok := g.roots[id]; return ok }
func (g *Graph[K]) Parent(id K) (K, bool)          { parent, ok := g.parents[id]; return parent, ok }
func (g *Graph[K]) Counts() (nodes, relations int) { return len(g.nodes), len(g.parents) }

// Ensure records an observed identity with unknown parent, without inferring a
// relationship or creation. Rejections do not retain the candidate identity.
func (g *Graph[K]) Ensure(id K) error {
	if g.Has(id) {
		return nil
	}
	if len(g.nodes) >= g.limits.MaxNodes {
		return ErrNodeLimit
	}
	if g.limits.UnknownDepth > g.limits.MaxDepth {
		return ErrDepthLimit
	}
	g.nodes[id] = struct{}{}
	return nil
}

// Root marks a caller-confirmed root. It cannot erase a confirmed parent.
func (g *Graph[K]) Root(id K) error {
	if _, ok := g.parents[id]; ok {
		return ErrConflict
	}
	if !g.Has(id) && len(g.nodes) >= g.limits.MaxNodes {
		return ErrNodeLimit
	}
	g.nodes[id] = struct{}{}
	g.roots[id] = struct{}{}
	return nil
}

// ConfirmRoot admits a confirmed root and its child edge as one operation. A
// rejected edge also rolls back a newly observed root or root designation.
func (g *Graph[K]) ConfirmRoot(child, root K) error {
	hadNode, hadRoot := g.Has(root), g.IsRoot(root)
	if err := g.Root(root); err != nil {
		return err
	}
	if err := g.Confirm(child, root); err != nil {
		if !hadRoot {
			delete(g.roots, root)
		}
		if !hadNode {
			delete(g.nodes, root)
		}
		return err
	}
	return nil
}

// Confirm admits one confirmed edge atomically. Conflicts, cycles and limits
// leave both relationships and candidate vertex membership unchanged.
func (g *Graph[K]) Confirm(child, parent K) error {
	if child == parent {
		return ErrCycle
	}
	if g.Reaches(parent, child) {
		return ErrCycle
	}
	if g.IsRoot(child) {
		return ErrConflict
	}
	if previous, ok := g.parents[child]; ok {
		if previous != parent {
			return ErrConflict
		}
		return nil
	}
	nodes := len(g.nodes)
	if !g.Has(child) {
		nodes++
	}
	if !g.Has(parent) {
		nodes++
	}
	if nodes > g.limits.MaxNodes {
		return ErrNodeLimit
	}
	if len(g.parents) >= g.limits.MaxRelations {
		return ErrRelationLimit
	}
	// Read the prospective edge without mutating even transiently. An arriving
	// ancestor can deepen any already observed descendant.
	lookup := func(id K) (K, bool) {
		if id == child {
			return parent, true
		}
		return g.Parent(id)
	}
	for id := range g.nodes {
		if depth, _ := g.depth(id, lookup); depth > g.limits.MaxDepth {
			return ErrDepthLimit
		}
	}
	for _, id := range []K{child, parent} {
		if depth, _ := g.depth(id, lookup); depth > g.limits.MaxDepth {
			return ErrDepthLimit
		}
	}
	g.nodes[child], g.nodes[parent] = struct{}{}, struct{}{}
	g.parents[child] = parent
	return nil
}

// Reaches includes identity only for an observed vertex. Missing parents never
// prove membership in a root's tree.
func (g *Graph[K]) Reaches(id, ancestor K) bool {
	if !g.Has(id) || !g.Has(ancestor) {
		return false
	}
	for {
		if id == ancestor {
			return true
		}
		parent, ok := g.parents[id]
		if !ok {
			return false
		}
		id = parent
	}
}

// Ancestors returns nearest parent first and includes a reached root. It never
// invents an ancestor beyond the last confirmed edge.
func (g *Graph[K]) Ancestors(id K) []K {
	var result []K
	for {
		parent, ok := g.parents[id]
		if !ok {
			return result
		}
		result = append(result, parent)
		id = parent
	}
}

// Depth reports the observed chain's depth and whether it reaches a root.
func (g *Graph[K]) Depth(id K) (depth int, rooted, exists bool) {
	if !g.Has(id) {
		return 0, false, false
	}
	depth, rooted = g.depth(id, g.Parent)
	return depth, rooted, true
}

func (g *Graph[K]) depth(id K, parent func(K) (K, bool)) (int, bool) {
	depth := 0
	for {
		if g.IsRoot(id) {
			return depth, true
		}
		next, ok := parent(id)
		if !ok {
			return depth + g.limits.UnknownDepth, false
		}
		depth++
		id = next
	}
}

// Merge contracts two identities only after the caller has established their
// equivalence. The caller chooses the stable keeper. Parent conflicts, ancestor
// contraction and excessive descendant depth reject the entire operation.
func (g *Graph[K]) Merge(keep, drop K) error {
	if !g.Has(keep) || !g.Has(drop) {
		return ErrUnknown
	}
	if keep == drop {
		return nil
	}
	if g.Reaches(keep, drop) || g.Reaches(drop, keep) {
		return ErrCycle
	}
	if g.IsRoot(keep) != g.IsRoot(drop) {
		return ErrConflict
	}
	a, hasA := g.Parent(keep)
	b, hasB := g.Parent(drop)
	if hasA && hasB && a != b {
		return ErrConflict
	}
	parents := make(map[K]K, len(g.parents))
	for child, parent := range g.parents {
		if child == drop {
			child = keep
		}
		if parent == drop {
			parent = keep
		}
		parents[child] = parent
	}
	lookup := func(id K) (K, bool) { p, ok := parents[id]; return p, ok }
	for id := range g.nodes {
		if id == drop {
			continue
		}
		if depth, _ := g.depth(id, lookup); depth > g.limits.MaxDepth {
			return ErrDepthLimit
		}
	}
	g.parents = parents
	delete(g.nodes, drop)
	delete(g.roots, drop)
	return nil
}
