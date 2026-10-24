// SPDX-License-Identifier: Apache-2.0

package claude

// SubagentIdentity preserves distinct native identity namespaces. Empty fields mean
// unknown, never an inferred session/agent/tool identity. ToolUseID can identify
// an unresolved parent tool reference without identifying its owning agent.
type SubagentIdentity struct {
	// AssociationID is a session/epoch-scoped SDK key, stable through enrichment.
	AssociationID string `json:"association_id,omitempty"`
	RunID         string `json:"run_id,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	AgentID       string `json:"agent_id,omitempty"`
	TaskID        string `json:"task_id,omitempty"`
	ToolUseID     string `json:"tool_use_id,omitempty"`
}

// SubagentHistory asserts a previously verified local-agent task in a native session.
// It discovers identity only; it does not execute/resume work or replay requests.
type SubagentHistory struct {
	SessionID    string
	TaskID       string
	ParentTaskID string
}
