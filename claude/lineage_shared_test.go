// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"errors"
	"testing"
)

func TestSharedLineageAliasMergeKeepsDescendantRouting(t *testing.T) {
	c := openTreeFixture(t)
	_, first := treeFrame(t, c, `{"type":"future_event","session_id":"s","agent_id":"a"}`)
	treeFrame(t, c, `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"spawn","name":"Agent","input":{}}]}}`)
	_, task := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","tool_use_id":"spawn"}`)
	_, child := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"child","task_type":"local_agent","parent_task_id":"a"}`)
	if child.Scope != "child" || child.Parent == nil || child.Parent.AssociationID != task.Source.AssociationID {
		t.Fatal(child)
	}
	ds, bridge := treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"a","message":{"content":"bridge"}}`)
	if !treeHas(ds, "SubagentAssociated", "identity-merged") || bridge.Source.AssociationID != first.Source.AssociationID || bridge.Scope != "child" {
		t.Fatal(ds, bridge)
	}
	_, after := treeFrame(t, c, `{"type":"system","subtype":"task_progress","session_id":"s","task_id":"child"}`)
	if after.Scope != "child" || after.Parent == nil || after.Parent.AssociationID != first.Source.AssociationID {
		t.Fatal(after)
	}
	if c.tree.lineage.Has(treeKey{"s", task.Source.AssociationID}) {
		t.Fatal("contracted graph identity retained")
	}
	if parent, ok := c.tree.lineage.Parent(treeKey{"s", child.Source.AssociationID}); !ok || parent != (treeKey{"s", first.Source.AssociationID}) {
		t.Fatal(parent, ok)
	}
}

func TestSharedLineageMergeDepthUsesNativeResourceError(t *testing.T) {
	observer, err := newManagedTree(Options{Subagents: SubagentOptions{MaxDepth: 2}})
	if err != nil {
		t.Fatal(err)
	}
	nodes := make(map[string]*treeNode)
	for _, id := range []string{"keep", "drop", "ancestor", "leaf"} {
		n := &treeNode{identity: SubagentIdentity{SessionID: "s", AssociationID: id}}
		nodes[id] = n
		observer.nodes[treeNodeKey(n)] = n
		observer.order = append(observer.order, n)
		if err := observer.lineage.Ensure(treeNodeKey(n)); err != nil {
			t.Fatal(err)
		}
	}
	for _, pair := range [][2]string{{"drop", "ancestor"}, {"leaf", "keep"}} {
		if err := observer.lineage.Confirm(treeNodeKey(nodes[pair[0]]), treeNodeKey(nodes[pair[1]])); err != nil {
			t.Fatal(err)
		}
	}
	if err := observer.lineage.ConfirmRoot(treeNodeKey(nodes["ancestor"]), treeKey{"s", ""}); err != nil {
		t.Fatal(err)
	}
	_, accepted, err := observer.merge(nodes["keep"], nodes["drop"], "s")
	var resource *ResourceLimitError
	if accepted || !errors.As(err, &resource) || resource.Resource != "subagent lineage depth" {
		t.Fatal(accepted, err)
	}
	if !observer.lineage.Has(treeNodeKey(nodes["drop"])) || len(observer.order) != 4 {
		t.Fatal("failed contraction changed native identities")
	}
	if parent, _ := observer.lineage.Parent(treeNodeKey(nodes["leaf"])); parent != treeNodeKey(nodes["keep"]) {
		t.Fatal(parent)
	}
}
