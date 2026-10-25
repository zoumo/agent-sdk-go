// SPDX-License-Identifier: Apache-2.0
package recorder

import "fmt"

// ChildLimits is an offline accounting policy, never live authorization or
// evidence of native interception. There are no default or approved allowances.
// The selected runtime cannot currently enforce these limits before recursive
// creation; Preflight remains closed even after this policy is recorded.
type ChildLimits struct {
	TotalAttempts int `json:"total_attempts"`
	Attempts      int `json:"attempts"`
	Depth         int `json:"depth"`
	Concurrent    int `json:"concurrent"`
}

type childState struct {
	depth   int
	outcome string
}

// ConfigureChildren durably fixes one root's policy before its first turn.
func (l *Ledger) ConfigureChildren(scene string, limits ChildLimits) error {
	return l.appendEntry(ledgerEntry{Scene: scene, Action: "child-policy", Children: &limits})
}

// ChildAttempt records a unique pre-dispatch intent. Empty parent means root;
// nested parents must be active reserved attempts. Saved/workflow paths and
// native alias observations cannot call this as proof of creation. Failed or
// ambiguous dispatch must never refund an intent or reuse its ID.
func (l *Ledger) ChildAttempt(scene, id, parent string) error {
	return l.appendEntry(ledgerEntry{Scene: scene, Action: "child-attempt", Child: id, Parent: parent})
}

// ChildFinish records observed completion/failure without refunding attempts.
// Root results and Close are not child completion evidence. An absent finish
// remains pending, including background work, until bounded observation stops.
func (l *Ledger) ChildFinish(scene, id string, success bool) error {
	outcome := "failed"
	if success {
		outcome = "succeeded"
	}
	return l.appendEntry(ledgerEntry{Scene: scene, Action: "child-finish", Child: id, Outcome: outcome})
}

func applyChildEntry(s *attemptState, e ledgerEntry) error {
	if e.Action == "child-policy" {
		p := e.Children
		if p == nil || s.limits != nil || s.turns != 0 || e.Child != "" || e.Parent != "" || e.Outcome != "" || p.TotalAttempts < 1 || p.TotalAttempts > 1024 || p.Attempts < 1 || p.Attempts > p.TotalAttempts || p.Depth < 1 || p.Depth > p.Attempts || p.Concurrent < 1 || p.Concurrent > p.Attempts {
			return fmt.Errorf("invalid or already fixed child policy")
		}
		s.limits, s.children = p, map[string]childState{}
		return nil
	}
	if s.limits == nil || s.turns == 0 || e.Children != nil || e.Child == "" || len(e.Child) > 255 || len(e.Parent) > 255 {
		return fmt.Errorf("child intent requires fixed policy, root turn and bounded ID")
	}
	child, exists := s.children[e.Child]
	if e.Action == "child-finish" {
		if !exists || child.outcome != "" || e.Parent != "" || (e.Outcome != "failed" && e.Outcome != "succeeded") {
			return fmt.Errorf("invalid child outcome")
		}
		child.outcome = e.Outcome
		s.children[e.Child] = child
		return nil
	}
	if exists || e.Outcome != "" || len(s.children) >= s.limits.Attempts {
		return fmt.Errorf("child attempt reused or cumulative budget exhausted")
	}
	depth, active := 1, 0
	if e.Parent != "" {
		parent, ok := s.children[e.Parent]
		if !ok || parent.outcome != "" {
			return fmt.Errorf("unknown or finished child parent")
		}
		depth = parent.depth + 1
	}
	for _, previous := range s.children {
		if previous.outcome == "" {
			active++
		}
	}
	if depth > s.limits.Depth || active >= s.limits.Concurrent {
		return fmt.Errorf("child depth/concurrency budget exhausted")
	}
	s.children[e.Child] = childState{depth: depth}
	return nil
}
