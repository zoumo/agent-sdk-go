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
	// heights caches the maximum distance from each node to a descendant.
	heights map[K]int
}

func New[K comparable](limits Limits) (*Graph[K], error) {
	if limits.MaxNodes < 1 || limits.MaxRelations < 0 || limits.MaxDepth < 0 || limits.UnknownDepth < 0 || limits.UnknownDepth > 1 {
		return nil, ErrInvalidLimits
	}
	return &Graph[K]{
		limits:  limits,
		nodes:   make(map[K]struct{}),
		roots:   make(map[K]struct{}),
		parents: make(map[K]K),
		heights: make(map[K]int),
	}, nil
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
	g.heights[id] = 0
	return nil
}

// Root marks a caller-confirmed root. It cannot erase a confirmed parent.
func (g *Graph[K]) Root(id K) error {
	if _, ok := g.parents[id]; ok {
		return ErrConflict
	}
	if !g.Has(id) {
		if len(g.nodes) >= g.limits.MaxNodes {
			return ErrNodeLimit
		}
		g.nodes[id] = struct{}{}
		g.heights[id] = 0
	}
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
			delete(g.heights, root)
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
	previous, hasParent := g.parents[child]
	if hasParent && previous == parent {
		return nil
	}
	if g.Reaches(parent, child) {
		return ErrCycle
	}
	if g.IsRoot(child) {
		return ErrConflict
	}
	if hasParent {
		return ErrConflict
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
	parentDepth, _ := g.depth(parent, g.Parent)
	childHeight := g.heights[child]
	if parentDepth+1+childHeight > g.limits.MaxDepth {
		return ErrDepthLimit
	}
	g.nodes[child], g.nodes[parent] = struct{}{}, struct{}{}
	if _, ok := g.heights[child]; !ok {
		g.heights[child] = 0
	}
	if _, ok := g.heights[parent]; !ok {
		g.heights[parent] = 0
	}
	g.parents[child] = parent
	g.raiseHeight(parent, childHeight+1)
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

// raiseHeight updates the maximum descendant distance after an edge makes id
// the parent of a taller subtree. Unchanged ancestors need not be revisited.
func (g *Graph[K]) raiseHeight(id K, height int) {
	for {
		if g.heights[id] >= height {
			return
		}
		g.heights[id] = height
		parent, ok := g.parents[id]
		if !ok {
			return
		}
		id, height = parent, height+1
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
	g.heights = buildHeights(g.nodes, parents, drop)
	delete(g.nodes, drop)
	delete(g.roots, drop)
	return nil
}

// buildHeights computes each node's maximum descendant distance in a forest.
func buildHeights[K comparable](nodes map[K]struct{}, parents map[K]K, excluded K) map[K]int {
	remaining := make(map[K]int, len(nodes))
	heights := make(map[K]int, len(nodes))
	for id := range nodes {
		if id != excluded {
			heights[id] = 0
		}
	}
	for _, parent := range parents {
		remaining[parent]++
	}
	queue := make([]K, 0, len(heights))
	for id := range heights {
		if remaining[id] == 0 {
			queue = append(queue, id)
		}
	}
	for i := 0; i < len(queue); i++ {
		id := queue[i]
		parent, ok := parents[id]
		if !ok {
			continue
		}
		if height := heights[id] + 1; height > heights[parent] {
			heights[parent] = height
		}
		remaining[parent]--
		if remaining[parent] == 0 {
			queue = append(queue, parent)
		}
	}
	return heights
}
