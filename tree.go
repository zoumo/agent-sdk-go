// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// TreeOptions bounds graph entries, lineage depth and native identity history.
// Zero selects defaults (4096 entries, 32 levels, 8192 dedup identities/runs).
// Exhaustion fails visibly; identities are never evicted and mistaken as new.
type TreeOptions struct {
	MaxNodes        int
	MaxDepth        int
	MaxDedupEntries int
	MaxRuns         int
	// History contains caller-verified local-agent associations, not replay frames.
	History []TreeSeed
}

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

// TreeRecord is an SDK envelope, never a native protocol event. Kind is Native,
// SubagentAssociated or ObservationState. SubagentCreated is reserved for future
// evidence that excludes resume; this version never emits it. Native retains
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

type treeRunKey struct{ session, task, run string }
type treeRun struct {
	status     string
	terminal   bool
	background *bool
}

type treeKey struct{ session, id string }
type treeNode struct {
	identity        TreeIdentity
	parent          *treeNode
	rootParent      bool
	parentTask      string
	tool            string
	evidence        string
	currentRun      string
	announced       bool
	published       TreeIdentity
	publishedParent *TreeIdentity
	publishedScope  string
}
type treeTool struct{ owner *treeNode }
type treeEventKey struct{ session, agent, task, tool, run, boundary, kind, subtype, uuid string }

// TreeClient owns a private Client and its sole Receive consumer. Like Client,
// methods are concurrency-safe except Receive, which requires one caller.
// It adds no reader goroutine, child process, subscription, resume or turn.
// PermissionHandler receives the original sole answerable request handle.
type TreeClient struct {
	client        *Client
	options       TreeOptions
	nodes         map[treeKey]*treeNode
	order         []*treeNode
	tools         map[treeKey]treeTool
	nonAgents     map[treeKey]struct{}
	nonAgentTools map[treeKey]struct{}
	seen          map[treeEventKey]struct{}
	runs          map[treeRunKey]treeRun
	currentRuns   map[treeKey]string
	pending       []TreeRecord // bounded graph fanout plus the accepted native frame
	sequence      uint64
	startup       bool
	end           error
}

// OpenTree enables native forwarding before initialize. A rejected initialize
// fails OpenTree; an accepted initialize still cannot verify that the installed
// CLI honors forwarding. The first record reports unverified, never complete.
func OpenTree(ctx context.Context, options Options, treeOptions TreeOptions) (*TreeClient, error) {
	for _, bound := range []struct {
		name              string
		value             *int
		fallback, ceiling int
	}{
		{"MaxNodes", &treeOptions.MaxNodes, 4096, 65536},
		{"MaxDepth", &treeOptions.MaxDepth, 32, 256},
		{"MaxDedupEntries", &treeOptions.MaxDedupEntries, 8192, 131072},
		{"MaxRuns", &treeOptions.MaxRuns, 8192, 131072},
	} {
		if *bound.value < 0 || *bound.value > bound.ceiling {
			return nil, fmt.Errorf("claude: %s must be between 0 and %d", bound.name, bound.ceiling)
		}
		if *bound.value == 0 {
			*bound.value = bound.fallback
		}
	}
	if len(treeOptions.History) > treeOptions.MaxNodes {
		return nil, fmt.Errorf("claude: history exceeds MaxNodes")
	}
	for _, seed := range treeOptions.History {
		if seed.SessionID == "" || seed.TaskID == "" {
			return nil, fmt.Errorf("claude: history requires session and task IDs")
		}
	}
	options.ForwardSubagentText = true
	c, err := Open(ctx, options)
	if err != nil {
		return nil, err
	}
	t := &TreeClient{client: c, options: treeOptions, nodes: make(map[treeKey]*treeNode), tools: make(map[treeKey]treeTool), nonAgents: make(map[treeKey]struct{}), nonAgentTools: make(map[treeKey]struct{}), seen: make(map[treeEventKey]struct{}), startup: true, runs: make(map[treeRunKey]treeRun), currentRuns: make(map[treeKey]string)}
	if err := t.seed(treeOptions.History); err != nil {
		_ = c.Close()
		return nil, err
	}
	if options.ResumeID != "" || len(treeOptions.History) > 0 {
		t.diagnostic("", "history-gap")
	}
	return t, nil
}

func (t *TreeClient) Send(ctx context.Context, text string) error { return t.client.Send(ctx, text) }
func (t *TreeClient) Interrupt(ctx context.Context) error         { return t.client.Interrupt(ctx) }
func (t *TreeClient) SessionID() (string, bool)                   { return t.client.SessionID() }
func (t *TreeClient) RequestedResumeID() string                   { return t.client.RequestedResumeID() }
func (t *TreeClient) Close() error                                { return t.client.Close() }
func (t *TreeClient) Done() <-chan struct{}                       { return t.client.Done() }
func (t *TreeClient) Err() error                                  { return t.client.Err() }

func (t *TreeClient) record(kind string, root TreeIdentity) TreeRecord {
	return TreeRecord{Kind: kind, Origin: "sdk-derived", Provider: "claude", Epoch: t.client.epoch, Provenance: "live", Root: root, Scope: "unscoped", UsageScope: "unknown"}
}
func (t *TreeClient) deliver(r TreeRecord) (TreeRecord, error) {
	t.sequence++
	r.Sequence = t.sequence
	return r, nil
}

// Receive emits an association before its first child Native. Root answers are
// never combined with child text. Parent tools without known ownership and
// missing session identities stay unresolved. Later evidence enriches relationships
// without replaying delivered Native records. Only scoped native UUIDs deduplicate;
// identical no-ID deltas are retained. Timed unresolved buffering is not provided.
// Canceling a receive wait leaves the process and permission service running.
func (t *TreeClient) Receive(ctx context.Context) (TreeRecord, error) {
	if t.startup {
		t.startup = false
		session, _ := t.SessionID()
		r := t.record("ObservationState", TreeIdentity{SessionID: session})
		r.State, r.Reason = "unverified", "forwarding-not-confirmed"
		return t.deliver(r)
	}
	if len(t.pending) > 0 {
		r := t.pending[0]
		t.pending[0] = TreeRecord{}
		t.pending = t.pending[1:]
		return t.deliver(r)
	}
	if t.end != nil {
		return TreeRecord{}, t.end
	}
nextEvent:
	e, err := t.client.Receive(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return TreeRecord{}, err
		}
		t.end = err
		session, _ := t.SessionID()
		r := t.record("ObservationState", TreeIdentity{SessionID: session})
		r.State, r.Reason = "incomplete", "stream-ended"
		if errors.Is(err, io.EOF) {
			r.State, r.Reason = "closed", "client-closed"
		}
		return t.deliver(r)
	}
	duplicate, identityErr := t.duplicate(e)
	if duplicate {
		goto nextEvent
	}
	root := TreeIdentity{SessionID: e.SessionID}
	r := t.record("Native", root)
	r.Origin, r.Native, r.Source, r.Scope = "native", &e, root, "root"
	if e.SessionID == "" {
		r.Scope = "unscoped"
	}
	if identityErr != nil {
		return t.failRecord(r, identityErr)
	}
	if err := t.route(&r, e); err != nil {
		return t.failRecord(r, err)
	}
	if err := t.trackRun(&r, e); err != nil {
		return t.failRecord(r, err)
	}
	t.pending = append(t.pending, r)
	next := t.pending[0]
	t.pending[0] = TreeRecord{}
	t.pending = t.pending[1:]
	return t.deliver(next)
}

// UUID identity is taken from the native frame, never from an enriched node:
// enrichment cannot make a retransmission look new. Unknown event contracts and
// sessionless frames are not deduplicated. Epoch is implicit in this owned map.
func (t *TreeClient) duplicate(e Event) (bool, error) {
	if e.SessionID == "" || e.UUID == "" {
		return false, nil
	}
	switch e.Type {
	case "assistant", "user", "system", "result", "stream_event":
	default:
		return false, nil
	}
	k := treeEventKey{session: e.SessionID, kind: e.Type, subtype: e.Subtype, uuid: e.UUID}
	if e.AgentID != nil {
		k.agent = *e.AgentID
	}
	if e.ParentToolUseID != nil {
		k.tool = *e.ParentToolUseID
	}
	if e.System != nil {
		k.task = e.System.TaskID
		if e.System.RunID != nil {
			k.run = *e.System.RunID
		}
	}
	// A known new run creates a conservative dedup boundary even for frames
	// without run_id. This does not assign that run to the frame; it prevents an
	// unproven cross-run UUID match from dropping resumed work.
	if k.run == "" {
		var n *treeNode
		if k.agent != "" {
			n = t.nodes[treeKey{e.SessionID, "task:" + k.agent}]
			if n == nil {
				n = t.nodes[treeKey{e.SessionID, "agent:" + k.agent}]
			}
		}
		if n == nil && k.tool != "" {
			n = t.nodes[treeKey{e.SessionID, "tool:" + k.tool}]
		}
		if n != nil {
			k.boundary = n.currentRun
		}
	}
	if _, exists := t.seen[k]; exists {
		return true, nil
	}
	if len(t.seen) >= t.options.MaxDedupEntries {
		return false, &ResourceLimitError{Resource: "tree dedup identities", Limit: t.options.MaxDedupEntries}
	}
	t.seen[k] = struct{}{}
	return false, nil
}

func (t *TreeClient) entries() int {
	return len(t.nodes) + len(t.tools) + len(t.nonAgents) + len(t.nonAgentTools)
}
func (t *TreeClient) reserve() error {
	if t.entries() >= t.options.MaxNodes {
		return &ResourceLimitError{Resource: "tree association entries", Limit: t.options.MaxNodes}
	}
	return nil
}
func (t *TreeClient) diagnostic(session, reason string) {
	d := t.record("ObservationState", TreeIdentity{SessionID: session})
	d.State, d.Reason = "incomplete", reason
	t.pending = append(t.pending, d)
}
func isTreeTaskEvent(e Event) bool {
	if e.System == nil {
		return false
	}
	switch e.Subtype {
	case "task_started", "task_updated", "task_progress", "task_notification":
		return true
	}
	return false
}
func (t *TreeClient) route(r *TreeRecord, e Event) error {
	// Delayed native runs may be observed, but must not rewrite entity metadata,
	// parent relations, tool aliases or the current run of newer work.
	if sys := e.System; isTreeTaskEvent(e) && sys.RunID != nil && *sys.RunID != "" {
		n := t.nodes[treeKey{e.SessionID, "task:" + sys.TaskID}]
		if n != nil && t.currentRuns[treeKey{e.SessionID, sys.TaskID}] > n.currentRun {
			n.currentRun = t.currentRuns[treeKey{e.SessionID, sys.TaskID}]
		}
		if n != nil && n.currentRun != "" && *sys.RunID < n.currentRun {
			r.Source = n.identity
			r.Source.RunID = *sys.RunID
			r.CurrentRunID = n.currentRun
			r.RunScope = "native"
			r.Stale = true
			r.Parent, r.Scope = t.parent(n)
			r.Evidence = "known-agent-task"
			return nil
		}
	}
	identity := TreeIdentity{SessionID: e.SessionID}
	task, agent, tool, run, evidence := "", "", "", "", ""
	if e.Control != nil {
		if m, err := e.Control.PermissionMetadata(); err == nil {
			agent = m.AgentID
			evidence = "permission-metadata-agent-id"
		}
	}
	if e.AgentID != nil && *e.AgentID != "" {
		agent = *e.AgentID
		evidence = "native-agent-id"
		if e.Type == "assistant" || e.Type == "user" {
			task = agent
			evidence = "message-agent-id-is-task-id"
		}
	}
	if e.ParentToolUseID != nil {
		tool = *e.ParentToolUseID
	}
	if s := e.System; isTreeTaskEvent(e) && s.TaskID != "" {
		if s.RunID != nil {
			run = *s.RunID
		}
		if e.Subtype == "task_started" && s.TaskType != nil && *s.TaskType != "local_agent" && *s.TaskType != "" {
			k := treeKey{e.SessionID, s.TaskID}
			if _, exists := t.nonAgents[k]; !exists && e.SessionID != "" {
				if err := t.reserve(); err != nil {
					return err
				}
				t.nonAgents[k] = struct{}{}
			}
			if t.nodes[treeKey{e.SessionID, "task:" + s.TaskID}] != nil {
				t.diagnostic(e.SessionID, "task-type-conflict")
			}
			if s.ToolUseID != nil && *s.ToolUseID != "" && e.SessionID != "" {
				k := treeKey{e.SessionID, *s.ToolUseID}
				if _, ok := t.nonAgentTools[k]; !ok {
					if err := t.reserve(); err != nil {
						return err
					}
					t.nonAgentTools[k] = struct{}{}
				}
			}
			// Preserve task identity without promoting bash/workflow/monitor to an agent.
			r.Source.TaskID, r.Source.RunID = s.TaskID, run
			r.RunScope = "legacy-unknown"
			if run != "" {
				r.RunScope = "native"
			}
			return nil
		}
		if e.Subtype == "task_started" && s.TaskType != nil && *s.TaskType == "local_agent" {
			task = s.TaskID
			evidence = "local-agent-task"
		}
		if t.nodes[treeKey{e.SessionID, "task:" + s.TaskID}] != nil {
			task = s.TaskID
			evidence = "known-agent-task"
		}
		if task != "" && s.ToolUseID != nil {
			tool = *s.ToolUseID
		}
		// Unclassified tasks keep their native task/run identity, without association.
		if task == "" && agent == "" {
			r.Source.TaskID, r.Source.RunID = s.TaskID, run
			r.Scope = "unresolved"
			r.RunScope = "legacy-unknown"
			if run != "" {
				r.RunScope = "native"
			}
			return nil
		}
	}
	// Only a previously observed message contract can connect a generic agent ID
	// to a task. Equal strings alone on arbitrary event kinds are insufficient.
	if task == "" && agent != "" {
		if known := t.nodes[treeKey{e.SessionID, "task:" + agent}]; known != nil && known.identity.AgentID == agent {
			task = known.identity.TaskID
		}
	}
	identity.AgentID, identity.TaskID, identity.ToolUseID, identity.RunID = agent, task, tool, run
	if task != "" {
		if _, no := t.nonAgents[treeKey{e.SessionID, task}]; no {
			r.Source = identity
			r.Scope = "unresolved"
			t.diagnostic(e.SessionID, "task-type-conflict")
			return nil
		}
	}
	if agent == "" && task == "" && tool != "" {
		if _, nonAgent := t.nonAgentTools[treeKey{e.SessionID, tool}]; nonAgent {
			r.Source = identity
			r.Scope = "unresolved"
			r.Reason = "non-agent-tool-reference"
			return nil
		}
	}
	if agent == "" && task == "" && tool == "" {
		if e.Type != "assistant" && e.Type != "user" && e.Type != "system" && e.Type != "result" && e.Type != "stream_event" {
			r.Scope = "unscoped"
		}
	} else {
		r.Source, r.Scope, r.Evidence = identity, "unresolved", evidence
		if e.SessionID == "" {
			return nil
		}
		key := "agent:" + agent
		if task != "" {
			key = "task:" + task
		} else if agent == "" {
			key = "tool:" + tool
			evidence = "scoped-parent-tool-use"
		}
		n := t.nodes[treeKey{e.SessionID, key}]
		// Only the message contract can reconcile independent task/agent aliases.
		if task != "" && agent != "" {
			other := t.nodes[treeKey{e.SessionID, "agent:" + agent}]
			if n == nil {
				n = other
			} else if other != nil && other != n {
				var ok bool
				n, ok = t.merge(n, other, e.SessionID)
				if !ok {
					r.Reason = "identity-conflict"
					return nil
				}
			}
		}
		legacy := t.nodes[treeKey{e.SessionID, "tool:" + tool}]
		if tool != "" && legacy != nil && legacy != n {
			if n == nil {
				n = legacy
			} else {
				var ok bool
				n, ok = t.merge(n, legacy, e.SessionID)
				if !ok {
					r.Reason = "identity-conflict"
					return nil
				}
			}
		}
		if n == nil {
			if err := t.reserve(); err != nil {
				return err
			}
			n = &treeNode{identity: TreeIdentity{SessionID: e.SessionID, AssociationID: key}, evidence: evidence}
			t.order = append(t.order, n)
		}
		if (agent != "" && n.identity.AgentID != "" && agent != n.identity.AgentID) || (task != "" && n.identity.TaskID != "" && task != n.identity.TaskID) {
			t.diagnostic(e.SessionID, "identity-conflict")
			return nil
		}
		k := treeKey{e.SessionID, key}
		if t.nodes[k] == nil {
			if err := t.reserve(); err != nil {
				return err
			}
			t.nodes[k] = n
		}
		if evidence != "" {
			n.evidence = evidence
		}
		if agent != "" {
			n.identity.AgentID = agent
		}
		if task != "" {
			n.identity.TaskID = task
		}
		if tool != "" {
			if n.tool != "" && n.tool != tool && (run == "" || n.currentRun != "" && run <= n.currentRun) {
				t.diagnostic(e.SessionID, "tool-reference-conflict")
			} else {
				n.tool = tool
				n.identity.ToolUseID = tool
			}
			// Add a tool alias only with observed task/message mapping, never a tool name.
			k = treeKey{e.SessionID, "tool:" + tool}
			if t.nodes[k] == nil {
				if err := t.reserve(); err != nil {
					return err
				}
				t.nodes[k] = n
			}
		}
		if s := e.System; s != nil && e.Subtype == "task_started" && s.ParentTaskID != nil && *s.ParentTaskID != "" {
			if n.parentTask != "" && n.parentTask != *s.ParentTaskID {
				t.diagnostic(e.SessionID, "parent-conflict")
			} else {
				n.parentTask = *s.ParentTaskID
			}
		}
		// Entity state follows only comparable native run IDs. Missing IDs never
		// overwrite a modern current run or acquire a fabricated run identity.
		if known := t.currentRuns[treeKey{e.SessionID, task}]; known > n.currentRun {
			n.currentRun = known
		}
		newRun := run != "" && (n.currentRun == "" || run > n.currentRun)
		if newRun {
			n.currentRun = run
		}
		r.Source = n.identity
		if tool != "" {
			r.Source.ToolUseID = tool
		}
		r.Source.RunID = run
		r.CurrentRunID = n.currentRun
		r.RunScope = "legacy-unknown"
		if run != "" {
			r.RunScope = "native"
			r.Stale = run < n.currentRun
		}
		if err := t.resolve(e.SessionID); err != nil {
			return err
		}
		t.announce(e.SessionID)
		r.Parent, r.Scope = t.parent(n)
		if newRun {
			d := t.record("ObservationState", r.Root)
			d.Source, d.Parent, d.Scope, d.CurrentRunID, d.RunScope = r.Source, copyTreeParent(r.Parent), r.Scope, n.currentRun, "native"
			d.State, d.Reason = "run-observed", "new-run-observed"
			t.pending = append(t.pending, d)
		}
		// Tool ownership below must refer to this entity, regardless of unresolved root.
		if e.Assistant != nil {
			if err := t.observeTools(e, n); err != nil {
				return err
			}
		}
		return nil
	}
	if e.Assistant != nil && r.Scope == "root" && e.SessionID != "" {
		if err := t.observeTools(e, nil); err != nil {
			return err
		}
	}
	if err := t.resolve(e.SessionID); err != nil {
		return err
	}
	t.announce(e.SessionID)
	return nil
}

// merge requires a verified message/task/tool mapping. Conflicting native
// identities or ancestry are diagnostics, never silent rebindings.
func (t *TreeClient) merge(a, b *treeNode, session string) (*treeNode, bool) {
	conflict := a.identity.AgentID != "" && b.identity.AgentID != "" && a.identity.AgentID != b.identity.AgentID || a.identity.TaskID != "" && b.identity.TaskID != "" && a.identity.TaskID != b.identity.TaskID || a.parentTask != "" && b.parentTask != "" && a.parentTask != b.parentTask
	if (a.parent != nil || a.rootParent) && (b.parent != nil || b.rootParent) && (a.parent != b.parent || a.rootParent != b.rootParent) {
		conflict = true
	}
	for p := a; p != nil; p = p.parent {
		if p == b {
			conflict = true
		}
	}
	for p := b; p != nil; p = p.parent {
		if p == a {
			conflict = true
		}
	}
	if conflict {
		t.diagnostic(session, "identity-conflict")
		return a, false
	}
	// Keep the earliest displayed association stable, and explicitly redirect the
	// other key if both were observed before the bridge evidence arrived.
	for _, n := range t.order {
		if n == a {
			break
		}
		if n == b {
			a, b = b, a
			break
		}
	}
	if a.identity.AgentID == "" {
		a.identity.AgentID = b.identity.AgentID
	}
	if a.identity.TaskID == "" {
		a.identity.TaskID = b.identity.TaskID
	}
	if a.tool == "" {
		a.tool = b.tool
		a.identity.ToolUseID = b.identity.ToolUseID
	}
	if a.parentTask == "" {
		a.parentTask = b.parentTask
	}
	if a.parent == nil && !a.rootParent {
		a.parent, a.rootParent = b.parent, b.rootParent
	}
	if b.currentRun > a.currentRun {
		a.currentRun = b.currentRun
	}
	for k, n := range t.nodes {
		if n == b {
			t.nodes[k] = a
		}
	}
	for k, o := range t.tools {
		if o.owner == b {
			t.tools[k] = treeTool{a}
		}
	}
	for i, n := range t.order {
		if n.parent == b {
			n.parent = a
		}
		if n == b {
			t.order = append(t.order[:i], t.order[i+1:]...)
			break
		}
	}
	// Repoint every descendant, including those after the removed slot.
	for _, n := range t.order {
		if n.parent == b {
			n.parent = a
		}
	}
	if b.announced {
		d := t.record("SubagentAssociated", TreeIdentity{SessionID: session})
		d.Source = a.identity
		d.Parent, d.Scope = t.parent(a)
		d.Reason, d.Evidence = "identity-merged", "verified-message-task-tool-mapping"
		d.PreviousAssociationID = b.identity.AssociationID
		t.pending = append(t.pending, d)
	}
	return a, true
}

func (t *TreeClient) observeTools(e Event, owner *treeNode) error {
	for _, b := range e.Assistant.Content.Blocks {
		if b.ToolUse == nil || b.ToolUse.ID == "" {
			continue
		}
		k := treeKey{e.SessionID, b.ToolUse.ID}
		if old, ok := t.tools[k]; ok {
			if old.owner != owner {
				t.diagnostic(e.SessionID, "tool-owner-conflict")
			}
			continue
		}
		if err := t.reserve(); err != nil {
			return err
		}
		t.tools[k] = treeTool{owner}
	}
	if err := t.resolve(e.SessionID); err != nil {
		return err
	}
	t.announce(e.SessionID)
	return nil
}
func (t *TreeClient) resolve(session string) error {
	for _, n := range t.order {
		if n.identity.SessionID != session {
			continue
		}
		var candidate *treeNode
		root, known := false, false
		if n.parentTask != "" {
			candidate = t.nodes[treeKey{session, "task:" + n.parentTask}]
			known = candidate != nil
		}
		// An explicit unresolved task reference takes precedence; tool ownership
		// cannot silently substitute a different, already-known parent.
		if n.parentTask != "" && !known {
			continue
		}
		if o, ok := t.tools[treeKey{session, n.tool}]; ok && n.tool != "" {
			if known && (o.owner != candidate || o.owner == nil) {
				t.diagnostic(session, "parent-conflict")
				continue
			}
			candidate, root, known = o.owner, o.owner == nil, true
		}
		if !known {
			continue
		}
		if (n.parent != nil || n.rootParent) && (n.parent != candidate || n.rootParent != root) {
			t.diagnostic(session, "parent-conflict")
			continue
		}
		depth := 1
		for p := candidate; p != nil; p = p.parent {
			if p == n {
				t.diagnostic(session, "lineage-cycle")
				known = false
				break
			}
			depth++
			if depth > t.options.MaxDepth {
				return &ResourceLimitError{Resource: "tree lineage depth", Limit: t.options.MaxDepth}
			}
		}
		if known {
			n.parent, n.rootParent = candidate, root
		}
	}
	// A newly resolved ancestor can deepen existing descendants.
	for _, n := range t.order {
		depth := 1
		for p := n.parent; p != nil; p = p.parent {
			depth++
			if depth > t.options.MaxDepth {
				return &ResourceLimitError{Resource: "tree lineage depth", Limit: t.options.MaxDepth}
			}
		}
	}
	return nil
}
func (t *TreeClient) parent(n *treeNode) (*TreeIdentity, string) {
	if n.rootParent {
		return &TreeIdentity{SessionID: n.identity.SessionID}, "child"
	}
	if n.parent != nil {
		p := n.parent.identity
		scope := "unresolved"
		for a := n.parent; a != nil; a = a.parent {
			if a.rootParent {
				scope = "child"
				break
			}
		}
		return &p, scope
	}
	if n.parentTask != "" {
		return &TreeIdentity{SessionID: n.identity.SessionID, TaskID: n.parentTask}, "unresolved"
	}
	return nil, "unresolved"
}
func (t *TreeClient) announce(session string) {
	visited := make(map[*treeNode]bool)
	var emit func(*treeNode)
	emit = func(n *treeNode) {
		if visited[n] {
			return
		}
		visited[n] = true
		if n.parent != nil {
			emit(n.parent)
		}
		parent, scope := t.parent(n)
		if n.announced && n.published == n.identity && equalTreeParent(n.publishedParent, parent) && n.publishedScope == scope {
			return
		}
		a := t.record("SubagentAssociated", TreeIdentity{SessionID: session})
		a.Source, a.Parent, a.Scope, a.Evidence, a.Reason = n.identity, copyTreeParent(parent), scope, n.evidence, "first-observed"
		if n.parentTask != "" {
			a.Evidence += "+parent-task-id"
		}
		if n.parent != nil || n.rootParent {
			if n.tool != "" {
				a.Evidence += "+observed-tool-owner"
			}
		}
		if n.announced {
			a.Reason = "identity-enriched"
		}
		t.pending = append(t.pending, a)
		n.announced, n.published, n.publishedParent, n.publishedScope = true, n.identity, copyTreeParent(parent), scope
	}
	for _, n := range t.order {
		if n.identity.SessionID == session {
			emit(n)
		}
	}
}
func equalTreeParent(a, b *TreeIdentity) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func copyTreeParent(p *TreeIdentity) *TreeIdentity {
	if p == nil {
		return nil
	}
	copy := *p
	return &copy
}

func (t *TreeClient) failRecord(native TreeRecord, err error) (TreeRecord, error) {
	t.client.fail(err)
	t.pending = append(t.pending, native)
	r := t.record("ObservationState", native.Root)
	r.State, r.Reason = "incomplete", "association-limit"
	if limit, ok := err.(*ResourceLimitError); ok {
		if limit.Resource == "tree dedup identities" {
			r.Reason = "dedup-limit"
		}
		if limit.Resource == "tree runs" {
			r.Reason = "run-limit"
		}
		if limit.Resource == "tree lineage depth" {
			r.Reason = "depth-limit"
		}
	}
	return t.deliver(r)
}

// seed uses the existing graph rules but never treats a snapshot as live creation.
func (t *TreeClient) seed(seeds []TreeSeed) error {
	for _, seed := range seeds {
		key := treeKey{seed.SessionID, "task:" + seed.TaskID}
		if n := t.nodes[key]; n != nil {
			if n.parentTask != seed.ParentTaskID {
				t.diagnostic(seed.SessionID, "parent-conflict")
			}
			continue
		}
		if err := t.reserve(); err != nil {
			return err
		}
		n := &treeNode{identity: TreeIdentity{SessionID: seed.SessionID, TaskID: seed.TaskID, AssociationID: key.id}, parentTask: seed.ParentTaskID, evidence: "caller-verified-history"}
		t.nodes[key] = n
		t.order = append(t.order, n)
	}
	sessions := make(map[string]bool)
	for _, seed := range seeds {
		if sessions[seed.SessionID] {
			continue
		}
		sessions[seed.SessionID] = true
		if err := t.resolve(seed.SessionID); err != nil {
			return err
		}
		t.announce(seed.SessionID)
	}
	for i := range t.pending {
		t.pending[i].Provenance = "history"
		if t.pending[i].Kind == "SubagentAssociated" {
			t.pending[i].Reason = "history"
		}
	}
	// Do not retain caller-owned slices after initialization.
	t.options.History = nil
	return nil
}

func (t *TreeClient) trackRun(r *TreeRecord, e Event) error {
	if !isTreeTaskEvent(e) || e.System.TaskID == "" || e.SessionID == "" {
		return nil
	}
	sys := e.System
	run := ""
	if sys.RunID != nil {
		run = *sys.RunID
	}
	key := treeRunKey{e.SessionID, sys.TaskID, run}
	state, exists := t.runs[key]
	if !exists && len(t.runs) >= t.options.MaxRuns {
		return &ResourceLimitError{Resource: "tree runs", Limit: t.options.MaxRuns}
	}
	status := ""
	terminal := false
	var background *bool
	switch e.Subtype {
	case "task_started":
		status, background = "running", sys.IsBackgrounded
	case "task_updated":
		var patch struct {
			Status     string `json:"status"`
			Background *bool  `json:"is_backgrounded"`
		}
		// Future/non-string patch statuses remain Raw and never imply completion.
		var fields map[string]json.RawMessage
		if json.Unmarshal(sys.Patch, &fields) == nil {
			_ = json.Unmarshal(fields["status"], &patch.Status)
			_ = json.Unmarshal(fields["is_backgrounded"], &patch.Background)
		}
		status, background = patch.Status, patch.Background
		terminal = status == "completed" || status == "failed" || status == "killed"
	case "task_notification":
		status = sys.Status
		terminal = status == "completed" || status == "failed" || status == "stopped"
	}
	becameTerminal := terminal && !state.terminal
	// A delayed edge cannot reopen a finished run or rewrite killed to stopped.
	if !state.terminal {
		if status != "" {
			state.status = status
		}
		state.terminal = terminal
	}
	if background != nil {
		b := *background
		state.background = &b
	}
	t.runs[key] = state
	entity := treeKey{e.SessionID, sys.TaskID}
	current := t.currentRuns[entity]
	if run > current {
		current = run
		t.currentRuns[entity] = run
	}
	r.Source.TaskID, r.Source.RunID = sys.TaskID, run
	r.CurrentRunID = current
	r.RunScope = "legacy-unknown"
	if run != "" {
		r.RunScope = "native"
		r.Stale = run < current
	}
	r.RunStatus, r.RunTerminal = state.status, state.terminal
	if state.background != nil {
		b := *state.background
		r.RunBackgrounded = &b
	}
	if current != "" {
		newest := t.runs[treeRunKey{e.SessionID, sys.TaskID, current}]
		r.CurrentRunStatus, r.CurrentRunTerminal = newest.status, newest.terminal
	}
	if becameTerminal {
		d := *r
		d.Kind, d.Origin, d.Native = "ObservationState", "sdk-derived", nil
		d.State, d.Reason = "run-terminal", "native-task-terminal"
		d.Parent = copyTreeParent(r.Parent)
		if r.RunBackgrounded != nil {
			b := *r.RunBackgrounded
			d.RunBackgrounded = &b
		}
		t.pending = append(t.pending, d)
	}
	return nil
}
