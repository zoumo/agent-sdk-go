// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bytes"
	"os"
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
	assigned         bool
	cwd              string
	initSeen         bool
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
	admission := &liveAdmission{
		mu: mu, detailPolicy: o.ChildDetailPolicy, limits: limits,
		assigned: o.SessionID != "", cwd: o.WorkDir,
		members: make(map[string]SubagentIdentity), parents: graph,
		tools: make(map[string]string), childTools: make(map[string]string),
		seeds: append([]SubagentHistory(nil), o.Subagents.History...),
	}
	if admission.cwd == "" {
		admission.cwd, _ = os.Getwd()
	}
	return admission, nil
}

func (a *liveAdmission) sessionID() string { return a.root }

// claimRoot fixes a native identity reported by claim_session. The identity is
// known before input even though no caller assigned it.
func (a *liveAdmission) claimRoot(sessionID string) []Received {
	a.assigned = true
	return a.confirmRoot(sessionID)
}

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
	if a.assigned {
		if err := a.checkAssignedIdentity(e, rootFrame); err != nil {
			return nil, err
		}
	}
	if a.root == "" && rootFrame && e.SessionID != "" {
		observations = append(observations, a.confirmRoot(e.SessionID)...)
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
		if !a.supportedVersion() {
			observations = append(observations, liveObservation("relationship-version-unverified", "minimum-native-version:"+MinimumCLIVersion, SubagentIdentity{SessionID: a.root}, nil))
		}
	}
	source := SubagentIdentity{SessionID: e.SessionID}
	if e.AgentID != nil {
		source.AgentID = *e.AgentID
	}
	key := ""
	if isTreeTaskEvent(e) && e.System.TaskID != "" {
		source.TaskID = e.System.TaskID
		candidate := "task:" + source.TaskID
		if e.Subtype == "task_started" && e.System.TaskType != nil && *e.System.TaskType == "local_agent" && a.supportedVersion() {
			key = candidate
			source.AssociationID = key
			if e.System.ToolUseID != nil {
				source.ToolUseID = *e.System.ToolUseID
			}
			if _, ok := a.members[key]; !ok && len(a.members) >= a.limits.MaxNodes {
				return []Received{{Kind: ReceivedNative, Event: &e, Raw: e.Raw, Source: source, Origin: "live"}},
					&ResourceLimitError{Resource: "session members", Limit: a.limits.MaxNodes}
			}
			if source.ToolUseID == "" {
				source.ToolUseID = a.members[key].ToolUseID
			}
			member := source
			member.AgentID = member.TaskID
			member.ToolUseID = a.members[key].ToolUseID
			observations = append(observations, liveObservation("member-confirmed", "cli-"+a.nativeVersion+"-local-agent-task", source, a.parent(key)))
			if source.ToolUseID != "" {
				if alias, ok := a.childTools[source.ToolUseID]; ok && alias != key {
					observations = append(observations, liveObservation("tool-alias-conflict", "native-task-tool-mapping", source, a.parent(key)))
				} else if !ok && len(a.childTools) >= a.limits.MaxNodes {
					observations = append(observations, liveObservation("tool-knowledge-limit", "native-task-tool-mapping", source, a.parent(key)))
				} else {
					a.childTools[source.ToolUseID] = key
					member.ToolUseID = source.ToolUseID
				}
			}
			a.members[key] = member
		} else if member, ok := a.members[candidate]; ok {
			key = candidate
			source = member
			if a.supportedVersion() && e.System.ToolUseID != nil {
				source.ToolUseID = *e.System.ToolUseID
			}
		}
	}
	declaredIdentity := a.supportedVersion() && (e.Assistant != nil || e.User != nil) && e.AgentID != nil && *e.AgentID != ""
	identityUnconfirmed := false
	if declaredIdentity {
		candidate := "task:" + *e.AgentID
		if member, ok := a.members[candidate]; ok {
			key = candidate
			source.TaskID = member.TaskID
			source.AssociationID = key
			source.ToolUseID = member.ToolUseID
		} else {
			identityUnconfirmed = true
			observations = append(observations, liveObservation("identity-member-unconfirmed", "cli-2.1.293-message-agent-id", source, nil))
		}
	}
	if (e.Assistant != nil || e.User != nil || e.Stream != nil || e.Type == "tool_progress") && e.ParentToolUseID != nil && *e.ParentToolUseID != "" {
		source.ToolUseID = *e.ParentToolUseID
		if alias, ok := a.childTools[source.ToolUseID]; ok {
			if declaredIdentity && key != "" && key != alias {
				identityUnconfirmed = true
				observations = append(observations, liveObservation("identity-alias-conflict", "cli-2.1.293-message-agent-id-task-tool", source, nil))
			} else if !identityUnconfirmed {
				key = alias
				source.TaskID = a.members[key].TaskID
				source.AssociationID = key
			}
		}
	}
	if identityUnconfirmed {
		key, source.TaskID, source.AssociationID = "", "", ""
	}
	declaredParent := a.supportedVersion() && e.System != nil && e.Subtype == "task_started" && e.System.ParentTaskID != nil
	if declaredParent && key != "" && *e.System.ParentTaskID != "" {
		parentKey := "task:" + *e.System.ParentTaskID
		if _, ok := a.members[parentKey]; ok {
			observations = append(observations, a.confirmParentWithEvidence(key, parentKey, source, "cli-2.1.293-parent-task-id"))
		} else {
			observations = append(observations, liveObservation("parent-member-unconfirmed", "cli-2.1.293-parent-task-id", source, a.parent(key)))
		}
	}
	if key != "" && source.ToolUseID != "" {
		if owner, ok := a.tools[source.ToolUseID]; ok && a.childTools[source.ToolUseID] == key {
			observations = append(observations, a.confirmParent(key, owner, source))
		}
	}
	if e.AgentID != nil && *e.AgentID != "" && !declaredIdentity {
		observations = append(observations, liveObservation("identity-extension-unverified", "unreviewed-message-agent-id", source, a.parent(key)))
	}
	if e.System != nil && e.System.ParentTaskID != nil && !declaredParent {
		observations = append(observations, liveObservation("parent-extension-unverified", "unreviewed-parent-task-id", source, a.parent(key)))
	}
	if e.Assistant != nil && a.supportedVersion() {
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
	if a.detailPolicy != ChildDetailSuppress || key == "" || !a.supportedVersion() || !pureChildDetail(e) {
		records = append(records, Received{Kind: ReceivedNative, Event: &e, Raw: e.Raw, Source: source, Parent: a.parent(key), Origin: "live"})
	}
	return append(records, observations...), nil
}

func (a *liveAdmission) supportedVersion() bool {
	return ValidateCLIVersion(a.nativeVersion) == nil
}

func (a *liveAdmission) confirmRoot(sessionID string) []Received {
	a.root = sessionID
	_ = a.parents.Root("")
	var observations []Received
	for _, seed := range a.seeds {
		if seed.SessionID == a.root {
			key := "task:" + seed.TaskID
			a.members[key] = SubagentIdentity{SessionID: a.root, TaskID: seed.TaskID, AssociationID: key}
		}
	}
	for _, seed := range a.seeds {
		if seed.SessionID != a.root || seed.ParentTaskID == "" {
			continue
		}
		key := "task:" + seed.TaskID
		record := a.confirmParent(key, "task:"+seed.ParentTaskID, a.members[key])
		record.Observation.Evidence = "caller-verified-history"
		observations = append(observations, record)
	}
	a.seeds = nil
	return observations
}

func (a *liveAdmission) checkAssignedIdentity(event Event, rootFrame bool) error {
	isInit := event.System != nil && event.Subtype == "init"
	if (rootFrame || isInit) && event.SessionID != a.root {
		return &SessionIdentityError{Reason: "session_identity_mismatch"}
	}
	if !isInit {
		return nil
	}
	if event.IsReplay != nil && *event.IsReplay {
		return &SessionIdentityError{Reason: "initialize_replayed"}
	}
	if a.initSeen {
		return &SessionIdentityError{Reason: "initialize_duplicate"}
	}
	left, leftErr := os.Stat(event.System.CWD)
	right, rightErr := os.Stat(a.cwd)
	if leftErr != nil || rightErr != nil || !left.IsDir() || !right.IsDir() || !os.SameFile(left, right) {
		return &SessionIdentityError{Reason: "session_cwd_mismatch"}
	}
	a.initSeen = true
	return nil
}

func (a *liveAdmission) confirmParent(child, parent string, source SubagentIdentity) Received {
	return a.confirmParentWithEvidence(child, parent, source, "cli-"+a.nativeVersion+"-task-tool-owner")
}

func (a *liveAdmission) confirmParentWithEvidence(child, parent string, source SubagentIdentity, evidence string) Received {
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
	return liveObservation(reason, evidence, source, a.parent(child))
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
