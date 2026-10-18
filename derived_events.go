// SPDX-License-Identifier: Apache-2.0

package claude

// TreeIdentity preserves distinct native identity namespaces. Empty fields mean
// unknown, never an inferred session/agent/tool identity. ToolUseID can identify
// an unresolved parent tool reference without identifying its owning agent.
type TreeIdentity struct {
	// AssociationID is a session/epoch-scoped SDK key, stable through enrichment.
	AssociationID string `json:"association_id,omitempty"`
	RunID         string `json:"run_id,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	AgentID       string `json:"agent_id,omitempty"`
	TaskID        string `json:"task_id,omitempty"`
	ToolUseID     string `json:"tool_use_id,omitempty"`
}

// TreeRecord is an SDK envelope, never a native protocol event. Defined separately
// from native types: Origin here is a wrapper string; Native.Origin remains the
// official *MessageOrigin object. Derived records have Native == nil. Kind is Native,
// SubagentAssociated, SubagentCreated or ObservationState. Created confirms
// creation under the fixed contract described in docs/subagent-events.md. Native retains
// the original Event and Raw bytes. Sequence is local delivery order, not a
// replay cursor. UsageScope remains unknown; no aggregate usage is calculated.
type TreeRecord struct {
	Kind       string        `json:"kind"`
	Origin     string        `json:"origin"` // native or sdk-derived
	Provider   string        `json:"provider"`
	Epoch      uint64        `json:"epoch"`
	Sequence   uint64        `json:"sequence"`
	Provenance string        `json:"provenance"` // live arrival, not proof of live creation
	Root       TreeIdentity  `json:"root"`
	Source     TreeIdentity  `json:"source"`
	Parent     *TreeIdentity `json:"parent,omitempty"`
	Scope      string        `json:"scope"` // root, child, unresolved, unscoped
	Evidence   string        `json:"evidence,omitempty"`
	Reason     string        `json:"reason,omitempty"`
	State      string        `json:"state,omitempty"`
	UsageScope string        `json:"usage_scope"`
	// CurrentRunID is the newest observed native run for this entity. A frame
	// without run_id remains legacy-unknown; it never inherits CurrentRunID.
	CurrentRunID string `json:"current_run_id,omitempty"`
	RunScope     string `json:"run_scope,omitempty"`
	// RunStatus is the observed ledger status for Source.RunID (or the explicit
	// legacy-unknown bucket). Terminal is sticky, not a claim of agent deletion.
	RunStatus          string `json:"run_status,omitempty"`
	RunTerminal        bool   `json:"run_terminal,omitempty"`
	RunBackgrounded    *bool  `json:"run_backgrounded,omitempty"`
	CurrentRunStatus   string `json:"current_run_status,omitempty"`
	CurrentRunTerminal bool   `json:"current_run_terminal,omitempty"`
	Stale              bool   `json:"stale,omitempty"`
	// PreviousAssociationID redirects a previously unresolved node to Source.
	PreviousAssociationID string `json:"previous_association_id,omitempty"`
	Native                *Event `json:"native,omitempty"`
}

// TreeSeed asserts a previously verified local-agent task in a native session.
// It discovers identity only; it does not execute/resume work or replay requests.
type TreeSeed struct {
	SessionID    string
	TaskID       string
	ParentTaskID string
}
