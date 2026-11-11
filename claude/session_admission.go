// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bytes"
	"sync"
	"time"

	"github.com/zoumo/agent-sdk-go/internal/lineage"
)

// liveAdmission owns Claude observation knowledge and diagnostic retention. It
// has no FIFO or answer registry. Session serializes its operations and the
// resulting FIFO commit with the same mutex; only expiry callbacks lock it here.
// Returned records are ordered for a single frame, never retained as a backlog.
type liveAdmission struct {
	mu               *sync.Mutex
	root             string
	nativeVersion    string
	members          map[string]SubagentIdentity
	parents          *lineage.Graph[string]
	tools            map[string]string
	childTools       map[string]string
	seeds            []SubagentHistory
	detailPolicy     ChildDetailPolicy
	limits           SubagentOptions
	quarantine       []UnattributedRecord
	quarantineBytes  int
	expiryTimer      *time.Timer
	dropped, expired uint64
}

func newLiveAdmission(o Options, mu *sync.Mutex) (*liveAdmission, error) {
	limits, err := normalizedSubagentOptions(o)
	if err != nil {
		return nil, err
	}
	graph, err := lineage.New[string](lineage.Limits{MaxNodes: limits.MaxNodes + 1, MaxRelations: limits.MaxNodes, MaxDepth: limits.MaxDepth})
	if err != nil {
		return nil, err
	}
	return &liveAdmission{
		mu: mu, detailPolicy: o.ChildDetailPolicy, limits: limits,
		members: make(map[string]SubagentIdentity), parents: graph,
		tools: make(map[string]string), childTools: make(map[string]string),
		seeds: append([]SubagentHistory(nil), o.Subagents.History...),
	}, nil
}

func (a *liveAdmission) sessionID() string { return a.root }

// release drops live relationship knowledge. Identity and bounded diagnostics
// remain readable after termination, and retained diagnostics still expire.
func (a *liveAdmission) release() {
	a.members = nil
	a.parents = nil
	a.tools = nil
	a.childTools = nil
	a.seeds = nil
}

// request preserves authority from the sole owned interaction, independent of
// relationship aliases and unknown identity extensions in the native envelope.
func (a *liveAdmission) request(e Event, p *PermissionRequest) Received {
	r := &IncomingRequest{permission: p, method: e.Control.Subtype, raw: bytes.Clone(e.Raw)}
	return Received{Kind: ReceivedRequest, Request: r, Raw: r.Raw(), Params: r.Params(), Source: SubagentIdentity{SessionID: a.root}, Origin: "live"}
}

// event interprets one frame after native control processing. A same-session
// reviewed envelope is enough observation evidence; neither a complete parent
// path nor an inferred task identity is required. Unknown fields never establish
// membership. Records put native delivery before derived observations. A terminal
// error may accompany records that must be committed before termination; rejected
// relationship knowledge alone does not terminate admission.
func (a *liveAdmission) event(e Event) ([]Received, error) {
	var observations []Received
	knownEnvelope := isTreeRootEvent(e) || e.RateLimit != nil || e.Reset != nil
	rootFrame := knownEnvelope && (e.AgentID == nil || *e.AgentID == "") && (e.ParentToolUseID == nil || *e.ParentToolUseID == "")
	if a.root == "" && rootFrame && e.SessionID != "" {
		a.root = e.SessionID
		_ = a.parents.Root("")
		for _, seed := range a.seeds {
			if seed.SessionID != a.root {
				continue
			}
			key := "task:" + seed.TaskID
			a.members[key] = SubagentIdentity{SessionID: a.root, TaskID: seed.TaskID, AssociationID: key}
		}
		for _, seed := range a.seeds {
			if seed.SessionID != a.root || seed.ParentTaskID == "" {
				continue
			}
			key := "task:" + seed.TaskID
			r := a.confirmParent(key, "task:"+seed.ParentTaskID, a.members[key])
			r.Observation.Evidence = "caller-verified-history"
			observations = append(observations, r)
		}
		a.seeds = nil
	}
	if e.SessionID != "" && a.root != "" && e.SessionID != a.root {
		a.isolate(e, "different-session")
		return nil, nil
	}
	owned := knownEnvelope && a.root != "" && e.SessionID == a.root
	// These records use the sole owned process connection, not an arbitrary
	// agent_id target. T05 retains native request authority in this same path.
	if e.Control != nil || e.Cancel != nil {
		owned = true
	}
	if !owned {
		a.isolate(e, "membership-unconfirmed")
		return nil, nil
	}
	if e.System != nil && e.Subtype == "init" && rootFrame && e.SessionID == a.root {
		a.nativeVersion = e.System.ClaudeCodeVersion
		if a.nativeVersion != "2.1.258" {
			observations = append(observations, liveObservation("relationship-version-unverified", "supported-native-version:2.1.258", SubagentIdentity{SessionID: a.root}, nil))
		}
	}
	source := SubagentIdentity{SessionID: e.SessionID}
	if e.AgentID != nil {
		source.AgentID = *e.AgentID
	} // unverified extension, never task equality
	key := ""
	// The fixed SDK 0.3.258 task_started local_agent + task_id + tool_use_id
	// contract is separate from newer/unknown message agent_id and parent_task_id.
	if isTreeTaskEvent(e) && e.System.TaskID != "" {
		source.TaskID = e.System.TaskID
		candidate := "task:" + source.TaskID
		if e.Subtype == "task_started" && e.System.TaskType != nil && *e.System.TaskType == "local_agent" && a.nativeVersion == "2.1.258" {
			key = candidate
			source.AssociationID = key
			if e.System.ToolUseID != nil {
				source.ToolUseID = *e.System.ToolUseID
			}
			if _, ok := a.members[key]; !ok && len(a.members) >= a.limits.MaxNodes {
				return []Received{{Kind: ReceivedNative, Event: &e, Raw: e.Raw, Source: source, Origin: "live"}},
					&ResourceLimitError{Resource: "session members", Limit: a.limits.MaxNodes}
			}
			if old, ok := a.members[key]; ok && old.ToolUseID != "" && source.ToolUseID != "" && old.ToolUseID != source.ToolUseID {
				observations = append(observations, liveObservation("tool-reference-conflict", "native-task", source, a.parent(key)))
				source.ToolUseID = old.ToolUseID
			}
			if source.ToolUseID == "" {
				source.ToolUseID = a.members[key].ToolUseID
			}
			member := source
			member.AgentID = "" // unknown extensions never become authoritative aliases
			a.members[key] = member
			observations = append(observations, liveObservation("member-confirmed", "cli-2.1.258-local-agent-task", source, a.parent(key)))
			if source.ToolUseID != "" {
				if alias, ok := a.childTools[source.ToolUseID]; ok && alias != key {
					observations = append(observations, liveObservation("tool-alias-conflict", "native-task-tool-mapping", source, a.parent(key)))
				} else {
					a.childTools[source.ToolUseID] = key
				}
			}
		} else if member, ok := a.members[candidate]; ok {
			key = candidate
			source = member
		}
	}
	// Forwarded stream/message/progress parent_tool_use_id can use an already confirmed
	// native task/tool alias. Matching an unknown agent_id string cannot do so.
	if (e.Assistant != nil || e.User != nil || e.Stream != nil || e.Type == "tool_progress") && e.ParentToolUseID != nil && *e.ParentToolUseID != "" {
		source.ToolUseID = *e.ParentToolUseID
		if alias, ok := a.childTools[source.ToolUseID]; ok {
			key = alias
			source.TaskID = a.members[key].TaskID
			source.AssociationID = key
		}
	}
	if key != "" && source.ToolUseID != "" {
		if owner, ok := a.tools[source.ToolUseID]; ok && a.childTools[source.ToolUseID] == key {
			observations = append(observations, a.confirmParent(key, owner, source))
		}
	}
	if e.AgentID != nil && *e.AgentID != "" {
		observations = append(observations, liveObservation("identity-extension-unverified", "cli-2.1.258-does-not-declare-message-agent-id", source, a.parent(key)))
	}
	if e.System != nil && e.System.ParentTaskID != nil {
		observations = append(observations, liveObservation("parent-extension-unverified", "cli-2.1.258-does-not-declare-parent-task-id", source, a.parent(key)))
	}
	if e.Assistant != nil && a.nativeVersion == "2.1.258" {
		// Root tool ownership needs the reviewed root envelope, or a verified task
		// alias. An unknown agent extension cannot silently become a root owner.
		ownerKnown := key != "" || rootFrame
		for _, b := range e.Assistant.Content.Blocks {
			if b.ToolUse == nil || b.ToolUse.ID == "" {
				continue
			}
			if !ownerKnown {
				observations = append(observations, liveObservation("tool-owner-unconfirmed", "native-tool-use", source, nil))
				continue
			}
			id := b.ToolUse.ID
			if owner, ok := a.tools[id]; ok {
				if owner != key {
					observations = append(observations, liveObservation("tool-owner-conflict", "native-tool-use", source, nil))
				}
			} else if len(a.tools) >= a.limits.MaxNodes {
				observations = append(observations, liveObservation("tool-knowledge-limit", "native-tool-use", source, nil))
			} else {
				a.tools[id] = key
				if child, ok := a.childTools[id]; ok {
					observations = append(observations, a.confirmParent(child, key, a.members[child]))
				}
			}
		}
	}
	// Native first; the following bounded per-frame knowledge is not a second FIFO.
	var records []Received
	if a.detailPolicy != ChildDetailSuppress || key == "" || a.nativeVersion != "2.1.258" || !pureChildDetail(e) {
		records = append(records, Received{Kind: ReceivedNative, Event: &e, Raw: e.Raw, Source: source, Parent: a.parent(key), Origin: "live"})
	}
	return append(records, observations...), nil
}

func (a *liveAdmission) confirmParent(child, parent string, source SubagentIdentity) Received {
	var err error
	if parent == "" {
		err = a.parents.ConfirmRoot(child, "")
	} else {
		err = a.parents.Confirm(child, parent)
	}
	reason := "parent-confirmed"
	if err != nil {
		reason = "parent-knowledge-rejected: " + err.Error()
	}
	return liveObservation(reason, "cli-2.1.258-task-tool-owner", source, a.parent(child))
}

func (a *liveAdmission) parent(key string) *SubagentIdentity {
	if key == "" {
		return nil
	}
	p, ok := a.parents.Parent(key)
	if !ok {
		return nil
	}
	if p == "" {
		return &SubagentIdentity{SessionID: a.root}
	}
	id, ok := a.members[p]
	if !ok {
		return &SubagentIdentity{SessionID: a.root, AssociationID: p}
	}
	return &id
}

func liveObservation(reason, evidence string, source SubagentIdentity, parent *SubagentIdentity) Received {
	return Received{Kind: ReceivedObservation, Observation: &Observation{Reason: reason, Evidence: evidence, Source: source, Parent: parent}, Origin: "live"}
}

func (a *liveAdmission) expire(now time.Time) {
	for len(a.quarantine) > 0 && now.Sub(a.quarantine[0].ReceivedAt) >= a.limits.MaxUnresolvedWait {
		a.quarantineBytes -= len(a.quarantine[0].Event.Raw)
		a.quarantine[0] = UnattributedRecord{}
		a.quarantine = a.quarantine[1:]
		a.expired++
	}
}

func (a *liveAdmission) isolate(e Event, reason string) {
	now := time.Now()
	a.expire(now)
	if len(e.Raw) > a.limits.MaxUnresolvedBytes {
		a.dropped++
		return
	}
	for len(a.quarantine) >= a.limits.MaxUnresolvedEvents || a.quarantineBytes+len(e.Raw) > a.limits.MaxUnresolvedBytes {
		a.quarantineBytes -= len(a.quarantine[0].Event.Raw)
		a.quarantine[0] = UnattributedRecord{}
		a.quarantine = a.quarantine[1:]
		a.dropped++
	}
	a.quarantine = append(a.quarantine, UnattributedRecord{Event: e, ReceivedAt: now, Reason: reason})
	a.quarantineBytes += len(e.Raw)
	a.scheduleExpiry()
}

func (a *liveAdmission) snapshot() UnattributedSnapshot {
	records := append([]UnattributedRecord(nil), a.quarantine...)
	// Parse a private copy so diagnostics cannot mutate retained native evidence.
	for i := range records {
		records[i].Event, _ = parseEvent(records[i].Event.Raw)
	}
	return UnattributedSnapshot{Records: records, Bytes: a.quarantineBytes, Dropped: a.dropped, Expired: a.expired, MaxRecords: a.limits.MaxUnresolvedEvents, MaxBytes: a.limits.MaxUnresolvedBytes, Retention: a.limits.MaxUnresolvedWait, CoverageGap: len(records) > 0 || a.dropped+a.expired > 0}
}

// Retention is active even when the consumer never reads diagnostics again.
// At most one timer belongs to this bounded quarantine, not one per record.
func (a *liveAdmission) scheduleExpiry() {
	if a.expiryTimer != nil {
		a.expiryTimer.Stop()
		a.expiryTimer = nil
	}
	if len(a.quarantine) == 0 {
		return
	}
	delay := time.Until(a.quarantine[0].ReceivedAt.Add(a.limits.MaxUnresolvedWait))
	a.expiryTimer = time.AfterFunc(max(delay, 0), func() { a.mu.Lock(); defer a.mu.Unlock(); a.expire(time.Now()); a.scheduleExpiry() })
}
