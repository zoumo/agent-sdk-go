// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
)

// Creation evidence is scoped to one owned process and never promoted across
// init/replay boundaries. There is deliberately no >= version comparison.
type treeCreation struct {
	disabled, initialized bool
	session               string
	builtins              map[string]bool
	calls                 map[treeKey]*treeCreationCall
}
type treeCreationCall struct {
	owner                   *treeNode
	resultOwner             *treeNode
	request, result         bool
	rejected                bool
	requestHash, resultHash [32]byte
	task, agent             string
	foreground              bool
}

func (t *managedTree) creationCall(k treeKey) (*treeCreationCall, error) {
	if c := t.creation.calls[k]; c != nil {
		return c, nil
	}
	if len(t.creation.calls) >= t.options.MaxCreationEntries {
		return nil, &ResourceLimitError{Resource: "tree creation evidence", Limit: t.options.MaxCreationEntries}
	}
	c := &treeCreationCall{}
	t.creation.calls[k] = c
	return c, nil
}

func (t *managedTree) observeCreation(r TreeRecord, e Event) error {
	c := &t.creation
	if e.IsReplay != nil && *e.IsReplay {
		c.disabled = true
	}
	if e.Type == "system" && e.Subtype == "init" {
		if c.initialized {
			c.disabled = true
		}
		c.initialized = true
		if e.System == nil || e.System.ClaudeCodeVersion != "2.1.258" || e.SessionID == "" || e.AgentID != nil || e.ParentToolUseID != nil && *e.ParentToolUseID != "" {
			c.disabled = true
		}
		c.session = e.SessionID
		c.builtins = make(map[string]bool)
		if e.System != nil {
			for _, name := range e.System.Tools {
				if name == "Agent" || name == "Task" {
					c.builtins[name] = true
				}
			}
		}
	}
	if c.disabled || !c.initialized {
		return nil
	}
	// Session changes are a new scope, not fresh evidence in this epoch.
	if e.SessionID != "" && e.SessionID != c.session {
		c.disabled = true
		return nil
	}
	if e.SessionID != c.session {
		return nil
	}
	if e.Assistant != nil {
		for _, b := range e.Assistant.Content.Blocks {
			if b.ToolUse == nil || b.ToolUse.ID == "" {
				continue
			}
			call, err := t.creationCall(treeKey{e.SessionID, b.ToolUse.ID})
			if err != nil {
				return err
			}
			hash := sha256.Sum256(b.Raw)
			if call.request {
				if call.requestHash != hash {
					call.rejected = true
				}
				continue
			}
			call.request, call.requestHash = true, hash
			ownership, ok := t.tools[treeKey{e.SessionID, b.ToolUse.ID}]
			call.owner = ownership.owner
			if call.result && call.resultOwner != call.owner {
				call.rejected = true
			}
			var input map[string]json.RawMessage
			valid := json.Unmarshal(b.ToolUse.Input, &input) == nil && input != nil
			_, resume := input["resume"]
			var prompt, description, isolation string
			valid = valid && json.Unmarshal(input["prompt"], &prompt) == nil && prompt != "" && json.Unmarshal(input["description"], &description) == nil && description != ""
			if raw, exists := input["isolation"]; exists {
				valid = valid && !bytes.Equal(raw, []byte("null")) && json.Unmarshal(raw, &isolation) == nil && isolation == "worktree"
			}
			for _, name := range []string{"subagent_type", "model", "name", "team_name", "mode"} {
				if raw, exists := input[name]; exists {
					var value string
					valid = valid && !bytes.Equal(raw, []byte("null")) && json.Unmarshal(raw, &value) == nil
				}
			}
			if raw, exists := input["run_in_background"]; exists {
				var background bool
				valid = valid && !bytes.Equal(raw, []byte("null")) && json.Unmarshal(raw, &background) == nil
			}
			if !ok || !c.builtins[b.ToolUse.Name] || !valid || resume || (r.Scope != "root" && r.Scope != "child") {
				call.rejected = true
			}
		}
	}
	if isTreeTaskEvent(e) && e.Subtype == "task_started" && e.System.ToolUseID != nil && *e.System.ToolUseID != "" {
		s := e.System
		call, err := t.creationCall(treeKey{e.SessionID, *s.ToolUseID})
		if err != nil {
			return err
		}
		if s.TaskType == nil || *s.TaskType != "local_agent" || s.TaskID == "" || (e.AgentID != nil && *e.AgentID != "" && *e.AgentID != s.TaskID) {
			call.rejected = true
		} else if call.task != "" && call.task != s.TaskID {
			// Reusing a spawning tool invocation for another entity/run is ambiguous.
			call.rejected = true
		} else {
			call.task = s.TaskID
			// 2.1.258 has no native run_id contract; creation is entity-scoped.
			// A future/unknown raw run_id is not evidence for this criterion.
			call.foreground = s.IsBackgrounded != nil && !*s.IsBackgrounded
		}
	}
	if e.User != nil {
		// Structured output belongs to the entire user frame. It is not safe to
		// assign it to one of several tool_result blocks, even if IDs repeat.
		var ids []string
		var result *ToolResultBlock
		for _, b := range e.User.Content.Blocks {
			if b.ToolResult != nil {
				ids = append(ids, b.ToolResult.ToolUseID)
				result = b.ToolResult
			}
		}
		for _, id := range ids {
			if id == "" {
				continue
			}
			call, err := t.creationCall(treeKey{e.SessionID, id})
			if err != nil {
				return err
			}
			if len(ids) != 1 || result.IsError || e.Error != nil || (r.Scope != "root" && r.Scope != "child") {
				call.rejected = true
				continue
			}
			var output struct {
				Status string `json:"status"`
				Agent  string `json:"agentId"`
			}
			hash := sha256.Sum256(e.ToolUseResult)
			// Check the result's caller independently of the child task identity.
			var owner *treeNode
			if r.Scope == "child" {
				owner = t.nodes[treeKey{e.SessionID, r.Source.AssociationID}]
			}
			if call.result && (call.resultHash != hash || call.resultOwner != owner) {
				call.rejected = true
			}
			call.result, call.resultHash = true, hash
			if call.request && owner != call.owner {
				call.rejected = true
			}
			call.resultOwner = owner
			if json.Unmarshal(e.ToolUseResult, &output) != nil || output.Agent == "" || (output.Status != "completed" && output.Status != "async_launched") {
				call.rejected = true
			}
			call.agent = output.Agent
		}
	}
	for k, call := range c.calls {
		if call.rejected || !call.request || call.task == "" {
			continue
		}
		n := t.nodes[treeKey{k.session, "task:" + call.task}]
		if n == nil || n.created || n.identity.TaskID != call.task || n.tool != k.id || (n.identity.AgentID != "" && n.identity.AgentID != call.task) {
			continue
		}
		ownership, ok := t.tools[k]
		if !ok || ownership.owner != call.owner {
			continue
		}
		parent, scope := t.parent(n)
		if scope != "child" {
			continue
		}
		proof := ""
		if call.foreground {
			proof = "cli-2.1.258-local-agent-foreground-start"
		}
		if call.result {
			if call.agent != call.task {
				continue
			}
			proof = "cli-2.1.258-agent-success+local-agent-task"
		}
		if proof == "" {
			continue
		}
		n.created = true
		created := t.record("SubagentCreated", TreeIdentity{SessionID: k.session})
		created.Source, created.Parent, created.Scope = n.identity, copyTreeParent(parent), scope
		created.Source.RunID = ""
		created.RunScope = "legacy-unknown"
		created.Reason, created.Evidence = "creation-confirmed", proof
		t.stage(created)
	}
	return nil
}
