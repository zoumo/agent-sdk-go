// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"errors"

	"github.com/stretchr/testify/require"
)

func (s *treeObservationSuite) TestSharedLineageAliasMergeKeepsDescendantRouting() {
	t := s.T()
	c := s.client
	_, first := treeFrame(t, c, `{"type":"future_event","session_id":"s","agent_id":"a"}`)
	treeFrame(t, c, `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"spawn","name":"Agent","input":{}}]}}`)
	_, task := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","tool_use_id":"spawn"}`)
	_, child := treeFrame(t, c, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"child","task_type":"local_agent","parent_task_id":"a"}`)
	require.False(t, child.Scope != "child" || child.Parent == nil || child.Parent.AssociationID != task.Source.AssociationID, child)
	ds, bridge := treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"a","message":{"content":"bridge"}}`)
	require.False(t, !treeHas(ds, "SubagentAssociated", "identity-merged") || bridge.Source.AssociationID != first.Source.AssociationID || bridge.Scope != "child", ds, bridge)
	_, after := treeFrame(t, c, `{"type":"system","subtype":"task_progress","session_id":"s","task_id":"child"}`)
	require.False(t, after.Scope != "child" || after.Parent == nil || after.Parent.AssociationID != first.Source.AssociationID, after)
	require.False(t, c.tree.lineage.Has(treeKey{"s", task.Source.AssociationID}), "contracted graph identity retained")
	{
		parent, ok := c.tree.lineage.Parent(treeKey{"s", child.Source.AssociationID})
		require.False(t, !ok || parent != (treeKey{"s", first.Source.AssociationID}), parent, ok)
	}
}

func (s *treeObservationSuite) TestSharedLineageMergeDepthUsesNativeResourceError() {
	t := s.T()
	observer, err := newManagedTree(Options{Subagents: SubagentOptions{MaxDepth: 2}})
	require.NoError(t, err)
	nodes := make(map[string]*treeNode)
	for _, id := range []string{"keep", "drop", "ancestor", "leaf"} {
		n := &treeNode{identity: SubagentIdentity{SessionID: "s", AssociationID: id}}
		nodes[id] = n
		observer.nodes[treeNodeKey(n)] = n
		observer.order = append(observer.order, n)
		{
			err := observer.lineage.Ensure(treeNodeKey(n))
			require.NoError(t, err)
		}
	}
	for _, pair := range [][2]string{{"drop", "ancestor"}, {"leaf", "keep"}} {
		{
			err := observer.lineage.Confirm(treeNodeKey(nodes[pair[0]]), treeNodeKey(nodes[pair[1]]))
			require.NoError(t, err)
		}
	}
	{
		err := observer.lineage.ConfirmRoot(treeNodeKey(nodes["ancestor"]), treeKey{"s", ""})
		require.NoError(t, err)
	}
	_, accepted, err := observer.merge(nodes["keep"], nodes["drop"], "s")
	var resource *ResourceLimitError
	require.False(t, accepted || !errors.As(err, &resource) || resource.Resource != "subagent lineage depth", accepted, err)
	require.False(t, !observer.lineage.Has(treeNodeKey(nodes["drop"])) || len(observer.order) != 4, "failed contraction changed native identities")
	{
		parent, _ := observer.lineage.Parent(treeNodeKey(nodes["leaf"]))
		require.EqualValues(t, treeNodeKey(nodes["keep"]), parent, parent)
	}
}
