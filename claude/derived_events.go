// SPDX-License-Identifier: Apache-2.0

package claude

import "encoding/json"

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

// ReceivedEvent is a native Event or a SubagentAssociated, SubagentCreated or
// ObservationState value. Use a type switch to consume the stream. Native values
// retain their original schema and JSON codec.
type ReceivedEvent interface {
	MarshalJSON() ([]byte, error)
}

// SubagentInfo describes observed relationships and runs. It is carried only by
// the derived convenience events; it is never added to native Event values.
type SubagentInfo struct {
	Provider   string            `json:"provider"`
	Epoch      uint64            `json:"epoch"`
	Sequence   uint64            `json:"sequence"`
	Provenance string            `json:"provenance"` // live arrival, not proof of live creation
	Root       SubagentIdentity  `json:"root"`
	Source     SubagentIdentity  `json:"source"`
	Parent     *SubagentIdentity `json:"parent,omitempty"`
	Scope      string            `json:"scope"` // root, child, unresolved, unscoped
	Evidence   string            `json:"evidence,omitempty"`
	Reason     string            `json:"reason,omitempty"`
	State      string            `json:"state,omitempty"`
	UsageScope string            `json:"usage_scope"`
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
}

// SubagentAssociated reports observed identity/parent evidence, not creation.
type SubagentAssociated struct{ SubagentInfo }

// SubagentCreated confirms creation under the conservative fixed-version criteria
// documented in docs/subagent-events.md.
type SubagentCreated struct{ SubagentInfo }

// ObservationState reports forwarding and relationship observation health.
type ObservationState struct{ SubagentInfo }

func (e SubagentAssociated) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		SubagentInfo
	}{"subagent_associated", e.SubagentInfo})
}
func (e SubagentCreated) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		SubagentInfo
	}{"subagent_created", e.SubagentInfo})
}
func (e ObservationState) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		SubagentInfo
	}{"observation_state", e.SubagentInfo})
}

// relationshipRecord is private staging state, never a public event envelope.
type relationshipRecord struct {
	SubagentInfo
	Kind   string
	Native *Event
}

func (r relationshipRecord) event() ReceivedEvent {
	switch r.Kind {
	case "Native":
		return *r.Native
	case "SubagentAssociated":
		return SubagentAssociated{r.SubagentInfo}
	case "SubagentCreated":
		return SubagentCreated{r.SubagentInfo}
	default:
		return ObservationState{r.SubagentInfo}
	}
}

// SubagentHistory asserts a previously verified local-agent task in a native session.
// It discovers identity only; it does not execute/resume work or replay requests.
type SubagentHistory struct {
	SessionID    string
	TaskID       string
	ParentTaskID string
}
