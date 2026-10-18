// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/zoumo/agent-sdk-go/internal/lineage"
)

// SubagentOptions bounds graph entries, lineage depth, native identity history and
// buffered records. Zero selects the documented defaults in docs/subagent-events.md.
// Exhaustion fails visibly; identities are never evicted and mistaken as new.
type SubagentOptions struct {
	MaxNodes        int
	MaxDepth        int
	MaxDedupEntries int
	MaxRuns         int
	// MaxCreationEntries bounds retained tool/task creation evidence, without eviction.
	MaxCreationEntries int
	// MaxPendingRecords bounds one atomic association/diagnostic/native fanout.
	MaxPendingRecords int
	// MaxUnresolvedEvents/Bytes bound native frames awaiting source attribution.
	MaxUnresolvedEvents int
	MaxUnresolvedBytes  int
	// MaxUnresolvedWait bounds association waiting, never provider work.
	MaxUnresolvedWait time.Duration
	// History contains caller-verified local-agent associations, not replay frames.
	History []SubagentHistory
}

type treeRunKey struct{ session, task, run string }
type treeRun struct {
	status     string
	terminal   bool
	background *bool
}

type treeKey struct{ session, id string }
type treeNode struct {
	created         bool
	identity        SubagentIdentity
	parentTask      string
	evidence        string
	currentRun      string
	announced       bool
	published       SubagentIdentity
	publishedParent *SubagentIdentity
	publishedScope  string
}
type treeHeld struct {
	record   relationshipRecord
	deadline time.Time
}
type treeEventKey struct{ session, agent, task, tool, run, boundary, kind, subtype, uuid string }

// managedTree is the Client-owned sole native dequeue consumer. It adds no reader
// goroutine, process, resume or turn and never answers permission requests.
type managedTree struct {
	lineage       *lineage.Graph[treeKey]
	creation      treeCreation
	client        *Client
	options       SubagentOptions
	nodes         map[treeKey]*treeNode
	order         []*treeNode
	tools         map[treeKey]*treeNode // present nil = observed root owner; absent = unknown
	nonAgents     map[treeKey]struct{}
	nonAgentTools map[treeKey]struct{}
	seen          map[treeEventKey]struct{}
	runs          map[treeRunKey]treeRun
	currentRuns   map[treeKey]string
	pending       []relationshipRecord // one atomically admitted fanout
	held          []treeHeld
	heldBytes     int
	fanoutErr     error
	halted        bool                // after graph/fanout exhaustion, drain raw frames without mutating state
	fallback      *relationshipRecord // one accepted native outside the bounded fanout
	ready         *relationshipRecord // the Native reserved in the current atomic fanout
	sequence      uint64
	startup       bool
	end           error
}

// newManagedTree validates and seeds state before a process is started.
func newManagedTree(options Options) (*managedTree, error) {
	treeOptions := options.Subagents
	for _, bound := range []struct {
		name              string
		value             *int
		fallback, ceiling int
	}{
		{"MaxNodes", &treeOptions.MaxNodes, 4096, 65536},
		{"MaxDepth", &treeOptions.MaxDepth, 32, 256},
		{"MaxDedupEntries", &treeOptions.MaxDedupEntries, 8192, 131072},
		{"MaxRuns", &treeOptions.MaxRuns, 8192, 131072},
		{"MaxCreationEntries", &treeOptions.MaxCreationEntries, 8192, 131072},
		{"MaxPendingRecords", &treeOptions.MaxPendingRecords, 8192, 131072},
		{"MaxUnresolvedEvents", &treeOptions.MaxUnresolvedEvents, 128, 65536},
		{"MaxUnresolvedBytes", &treeOptions.MaxUnresolvedBytes, 4 << 20, 64 << 20},
	} {
		if *bound.value < 0 || *bound.value > bound.ceiling {
			return nil, fmt.Errorf("claude: %s must be between 0 and %d", bound.name, bound.ceiling)
		}
		if *bound.value == 0 {
			*bound.value = bound.fallback
		}
	}
	if treeOptions.MaxUnresolvedWait < 0 || treeOptions.MaxUnresolvedWait > time.Minute {
		return nil, fmt.Errorf("claude: MaxUnresolvedWait must be between 0 and 1 minute")
	}
	if treeOptions.MaxUnresolvedWait == 0 {
		treeOptions.MaxUnresolvedWait = 5 * time.Second
	}
	if len(treeOptions.History) > treeOptions.MaxNodes {
		return nil, fmt.Errorf("claude: history exceeds MaxNodes")
	}
	for _, seed := range treeOptions.History {
		if seed.SessionID == "" || seed.TaskID == "" {
			return nil, fmt.Errorf("claude: history requires session and task IDs")
		}
	}
	graph, err := lineage.New[treeKey](lineage.Limits{MaxNodes: 2 * treeOptions.MaxNodes, MaxRelations: treeOptions.MaxNodes, MaxDepth: treeOptions.MaxDepth, UnknownDepth: 1})
	if err != nil {
		return nil, err
	}
	t := &managedTree{lineage: graph, options: treeOptions, nodes: make(map[treeKey]*treeNode), tools: make(map[treeKey]*treeNode), nonAgents: make(map[treeKey]struct{}), nonAgentTools: make(map[treeKey]struct{}), seen: make(map[treeEventKey]struct{}), startup: true, runs: make(map[treeRunKey]treeRun), currentRuns: make(map[treeKey]string)}
	t.creation.disabled = options.ResumeID != "" || len(treeOptions.History) > 0
	t.creation.calls = make(map[treeKey]*treeCreationCall)
	if err := t.seed(treeOptions.History); err != nil {
		return nil, err
	}
	if options.ResumeID != "" || len(treeOptions.History) > 0 {
		t.diagnostic("", "history-gap")
	}
	if t.fanoutErr != nil {
		return nil, t.fanoutErr
	}
	return t, nil
}

func (t *managedTree) record(kind string, root SubagentIdentity) relationshipRecord {
	return relationshipRecord{Kind: kind, SubagentInfo: SubagentInfo{Provider: "claude", Provenance: "live", Root: root, Scope: "unscoped", UsageScope: "unknown"}}
}
func (t *managedTree) deliver(r relationshipRecord) (relationshipRecord, error) {
	t.sequence++
	r.Sequence = t.sequence
	r.Epoch = t.client.epoch
	return r, nil
}

// receive emits associations before attributed Native records. Unresolved native
// frames wait within configured bounds, while control requests remain immediately
// observable and their original handles are dispatched independently by Client.
// Canceling this wait leaves both the held frames and permission service intact.
func (t *managedTree) receive(ctx context.Context) (relationshipRecord, error) {
	if t.startup {
		t.startup = false
		session, _ := t.client.SessionID()
		r := t.record("ObservationState", SubagentIdentity{SessionID: session})
		r.State, r.Reason = "unverified", "forwarding-not-confirmed"
		return t.deliver(r)
	}
	for {
		if len(t.pending) > 0 {
			r := t.pending[0]
			t.pending[0] = relationshipRecord{}
			t.pending = t.pending[1:]
			return t.deliver(r)
		}
		if t.fallback != nil {
			r := *t.fallback
			t.fallback = nil
			return t.deliver(r)
		}
		// Only Receive touches the graph and staging queues. No timer goroutine or
		// second Client reader is needed; expiration is checked on every receive.
		if r, ok := t.releaseHeld(); ok {
			return t.deliver(r)
		}
		if t.ready != nil {
			r := *t.ready
			t.ready = nil
			return t.deliver(r)
		}
		if t.end != nil {
			return relationshipRecord{}, t.end
		}
		waitCtx := ctx
		cancel := func() {}
		if len(t.held) > 0 {
			waitCtx, cancel = context.WithDeadline(ctx, t.held[0].deadline)
		}
		e, err := t.client.receiveNative(waitCtx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return relationshipRecord{}, ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			if errors.Is(err, context.Canceled) {
				return relationshipRecord{}, err
			}
			t.end = err
			session, _ := t.client.SessionID()
			r := t.record("ObservationState", SubagentIdentity{SessionID: session})
			r.State, r.Reason = "incomplete", "stream-ended"
			if errors.Is(err, io.EOF) {
				r.State, r.Reason = "closed", "client-closed"
			}
			return t.deliver(r)
		}
		root := SubagentIdentity{SessionID: e.SessionID}
		r := t.record("Native", root)
		r.Native, r.Source, r.Scope = &e, root, "root"
		if e.SessionID == "" {
			r.Scope = "unscoped"
		}
		if t.halted {
			// Drain accepted frames without trusting graph changes from a
			// rejected fanout; original native identities and Raw still survive.
			r.Scope, r.Reason = "unresolved", "subagent-resource-limit"
			r.Source = treeFrameIdentity(e)
			return t.deliver(r)
		}
		// Trust boundaries must be observed even when a Native UUID is a duplicate.
		if e.IsReplay != nil && *e.IsReplay {
			t.creation.disabled = true
		}
		if e.Type == "system" && e.Subtype == "init" && t.creation.initialized {
			t.creation.disabled = true
		}
		duplicate, identityErr := t.duplicate(e)
		if duplicate {
			continue
		}
		if identityErr != nil {
			return t.failRecord(r, identityErr)
		}
		if err := t.route(&r, e); err != nil {
			return t.failRecord(r, err)
		}
		if e.Control != nil {
			if metadata, err := e.Control.PermissionMetadata(); err == nil {
				r.Source.ToolUseID = metadata.ToolUseID
			}
		}
		if err := t.trackRun(&r, e); err != nil {
			return t.failRecord(r, err)
		}
		if err := t.observeCreation(r, e); err != nil {
			return t.failRecord(r, err)
		}
		// Reserve the entire fanout, including its Native, before exposing any of
		// it. A failure discards staged derived records and reports the raw frame
		// as unresolved through a fixed emergency slot, even with capacity one.
		if t.fanoutErr != nil {
			return t.failRecord(r, t.fanoutErr)
		}
		if len(t.pending) >= t.options.MaxPendingRecords {
			return t.failRecord(r, &ResourceLimitError{Resource: "subagent pending records", Limit: t.options.MaxPendingRecords})
		}
		if r.Scope == "unresolved" && e.Control == nil && e.Cancel == nil {
			if len(t.held) >= t.options.MaxUnresolvedEvents {
				return t.failRecord(r, &ResourceLimitError{Resource: "subagent unresolved events", Limit: t.options.MaxUnresolvedEvents})
			}
			if len(e.Raw) > t.options.MaxUnresolvedBytes-t.heldBytes {
				return t.failRecord(r, &ResourceLimitError{Resource: "subagent unresolved bytes", Limit: t.options.MaxUnresolvedBytes})
			}
			t.held = append(t.held, treeHeld{r, time.Now().Add(t.options.MaxUnresolvedWait)})
			t.heldBytes += len(e.Raw)
		} else {
			t.ready = &r
		}
	}
}

// stage never lets one input's derived fanout grow beyond its positive bound.
func (t *managedTree) stage(r relationshipRecord) {
	if len(t.pending) >= t.options.MaxPendingRecords {
		t.fanoutErr = &ResourceLimitError{Resource: "subagent pending records", Limit: t.options.MaxPendingRecords}
		return
	}
	t.pending = append(t.pending, r)
}

// releaseHeld selects the first releasable frame, so an unrelated unresolved
// source cannot make a newer frame overtake a resolved source's held frames.
// Later evidence only enriches undelivered attribution; Native is never replayed.
func (t *managedTree) releaseHeld() (relationshipRecord, bool) {
	for i, h := range t.held {
		r := h.record
		if !t.halted && t.end == nil {
			n := t.nodes[treeKey{r.Source.SessionID, r.Source.AssociationID}]
			if n == nil && r.Source.TaskID != "" {
				n = t.nodes[treeKey{r.Source.SessionID, "task:" + r.Source.TaskID}]
			}
			if n != nil {
				parent, scope := t.parent(n)
				if scope == "child" {
					// Keep the frame's original native run/tool references across enrichment.
					run, tool := r.Source.RunID, r.Source.ToolUseID
					r.Source = n.identity
					r.Source.RunID = run
					if tool != "" {
						r.Source.ToolUseID = tool
					}
					r.Parent, r.Scope = copyTreeParent(parent), scope
				}
			}
		}
		expired := !time.Now().Before(h.deadline)
		if r.Scope != "child" && !expired && !t.halted && t.end == nil {
			continue
		}
		copy(t.held[i:], t.held[i+1:])
		t.held[len(t.held)-1] = treeHeld{}
		t.held = t.held[:len(t.held)-1]
		t.heldBytes -= len(r.Native.Raw)
		if r.Scope == "child" {
			return r, true
		}
		r.Scope, r.Reason = "unresolved", "unresolved-source"
		t.fallback = &r
		d := t.record("ObservationState", r.Root)
		d.Source, d.Parent, d.Scope = r.Source, copyTreeParent(r.Parent), "unresolved"
		d.State, d.Reason = "incomplete", "unresolved-source"
		return d, true
	}
	return relationshipRecord{}, false
}

// UUID identity is taken from the native frame, never from an enriched node:
// enrichment cannot make a retransmission look new. Unknown event contracts and
// sessionless frames are not deduplicated. Epoch is implicit in this owned map.
func (t *managedTree) duplicate(e Event) (bool, error) {
	if e.SessionID == "" || e.UUID == "" {
		return false, nil
	}
	switch e.Type {
	case "assistant", "user", "result", "stream_event":
	case "system":
		if !isTreeRootEvent(e) {
			return false, nil
		}
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
		return false, &ResourceLimitError{Resource: "subagent dedup identities", Limit: t.options.MaxDedupEntries}
	}
	t.seen[k] = struct{}{}
	return false, nil
}

func (t *managedTree) entries() int {
	return len(t.nodes) + len(t.tools) + len(t.nonAgents) + len(t.nonAgentTools)
}
func (t *managedTree) reserve() error {
	if t.entries() >= t.options.MaxNodes {
		return &ResourceLimitError{Resource: "subagent association entries", Limit: t.options.MaxNodes}
	}
	return nil
}
func (t *managedTree) diagnostic(session, reason string) {
	// Conflicting graph evidence cannot support a later creation claim.
	if strings.Contains(reason, "conflict") || reason == "lineage-cycle" {
		t.creation.disabled = true
	}
	d := t.record("ObservationState", SubagentIdentity{SessionID: session})
	d.State, d.Reason = "incomplete", reason
	t.stage(d)
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

// isTreeRootEvent recognizes the pinned native envelope contracts only. A
// session ID locates the root session but cannot assign an unknown notification
// to its root agent. Explicit source evidence is handled separately in route.
func isTreeRootEvent(e Event) bool {
	switch e.Type {
	case "assistant", "user", "result", "stream_event":
		return true
	case "system":
		// Subtypes reviewed in official TS 0.3.302. Unknown future subtypes stay
		// unscoped unless their own agent/task/tool evidence proves attribution.
		switch e.Subtype {
		case "init", "status", "compact_boundary", "api_retry",
			"background_tasks_changed", "session_state_changed", "commands_changed",
			"control_request_progress", "elicitation_complete", "files_persisted",
			"hook_started", "hook_progress", "hook_response", "informational",
			"local_command_output", "memory_recall", "mirror_error",
			"model_refusal_fallback", "model_refusal_no_fallback", "notification",
			"permission_denied", "plugin_install", "thinking_tokens",
			"worker_shutting_down", "task_started", "task_progress", "task_updated",
			"task_notification":
			return true
		}
	}
	return false
}

func (t *managedTree) route(r *relationshipRecord, e Event) error {
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
	identity := SubagentIdentity{SessionID: e.SessionID}
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
		if !isTreeRootEvent(e) {
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
				var mergeErr error
				n, ok, mergeErr = t.merge(n, other, e.SessionID)
				if mergeErr != nil {
					return mergeErr
				}
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
				var mergeErr error
				n, ok, mergeErr = t.merge(n, legacy, e.SessionID)
				if mergeErr != nil {
					return mergeErr
				}
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
			if err := t.lineage.Ensure(treeKey{e.SessionID, key}); err != nil {
				return t.lineageLimit(err)
			}
			n = &treeNode{identity: SubagentIdentity{SessionID: e.SessionID, AssociationID: key}, evidence: evidence}
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
			if n.identity.ToolUseID != "" && n.identity.ToolUseID != tool && (run == "" || n.currentRun != "" && run <= n.currentRun) {
				t.diagnostic(e.SessionID, "tool-reference-conflict")
			} else {
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
			t.stage(d)
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
func (t *managedTree) merge(a, b *treeNode, session string) (*treeNode, bool, error) {
	conflict := a.identity.AgentID != "" && b.identity.AgentID != "" && a.identity.AgentID != b.identity.AgentID || a.identity.TaskID != "" && b.identity.TaskID != "" && a.identity.TaskID != b.identity.TaskID || a.parentTask != "" && b.parentTask != "" && a.parentTask != b.parentTask
	if conflict {
		t.diagnostic(session, "identity-conflict")
		return a, false, nil
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
	if err := t.lineage.Merge(treeNodeKey(a), treeNodeKey(b)); err != nil {
		if errors.Is(err, lineage.ErrDepthLimit) || errors.Is(err, lineage.ErrNodeLimit) || errors.Is(err, lineage.ErrRelationLimit) {
			return a, false, t.lineageLimit(err)
		}
		t.diagnostic(session, "identity-conflict")
		return a, false, nil
	}
	a.created = a.created || b.created
	for _, call := range t.creation.calls {
		if call.owner == b {
			call.owner = a
		}
		if call.resultOwner == b {
			call.resultOwner = a
		}
	}
	if a.identity.AgentID == "" {
		a.identity.AgentID = b.identity.AgentID
	}
	if a.identity.TaskID == "" {
		a.identity.TaskID = b.identity.TaskID
	}
	if a.identity.ToolUseID == "" {
		a.identity.ToolUseID = b.identity.ToolUseID
	}
	if a.parentTask == "" {
		a.parentTask = b.parentTask
	}
	if b.currentRun > a.currentRun {
		a.currentRun = b.currentRun
	}
	for k, n := range t.nodes {
		if n == b {
			t.nodes[k] = a
		}
	}
	for k, owner := range t.tools {
		if owner == b {
			t.tools[k] = a
		}
	}
	for i, n := range t.order {
		if n == b {
			t.order = append(t.order[:i], t.order[i+1:]...)
			break
		}
	}
	if b.announced {
		d := t.record("SubagentAssociated", SubagentIdentity{SessionID: session})
		d.Source = a.identity
		d.Parent, d.Scope = t.parent(a)
		d.Reason, d.Evidence = "identity-merged", "verified-message-task-tool-mapping"
		d.PreviousAssociationID = b.identity.AssociationID
		t.stage(d)
	}
	return a, true, nil
}

func (t *managedTree) observeTools(e Event, owner *treeNode) error {
	for _, b := range e.Assistant.Content.Blocks {
		if b.ToolUse == nil || b.ToolUse.ID == "" {
			continue
		}
		k := treeKey{e.SessionID, b.ToolUse.ID}
		if old, ok := t.tools[k]; ok {
			if old != owner {
				t.diagnostic(e.SessionID, "tool-owner-conflict")
			}
			continue
		}
		if err := t.reserve(); err != nil {
			return err
		}
		t.tools[k] = owner
	}
	if err := t.resolve(e.SessionID); err != nil {
		return err
	}
	t.announce(e.SessionID)
	return nil
}

// treeNodeKey stays stable while native task/agent/tool aliases are enriched.
func treeNodeKey(n *treeNode) treeKey { return treeKey{n.identity.SessionID, n.identity.AssociationID} }

func (t *managedTree) lineageLimit(err error) error {
	if errors.Is(err, lineage.ErrDepthLimit) {
		return &ResourceLimitError{Resource: "subagent lineage depth", Limit: t.options.MaxDepth}
	}
	return &ResourceLimitError{Resource: "subagent association entries", Limit: t.options.MaxNodes}
}

func (t *managedTree) resolve(session string) error {
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
		// Explicit unresolved task evidence cannot be replaced by tool ownership.
		if n.parentTask != "" && !known {
			continue
		}
		if owner, ok := t.tools[treeKey{session, n.identity.ToolUseID}]; ok && n.identity.ToolUseID != "" {
			if known && (owner != candidate || owner == nil) {
				t.diagnostic(session, "parent-conflict")
				continue
			}
			candidate, root, known = owner, owner == nil, true
		}
		if !known {
			continue
		}
		var err error
		if root {
			err = t.lineage.ConfirmRoot(treeNodeKey(n), treeKey{session, ""})
		} else {
			err = t.lineage.Confirm(treeNodeKey(n), treeNodeKey(candidate))
		}
		if err != nil {
			switch {
			case errors.Is(err, lineage.ErrCycle):
				t.diagnostic(session, "lineage-cycle")
			case errors.Is(err, lineage.ErrConflict):
				t.diagnostic(session, "parent-conflict")
			default:
				return t.lineageLimit(err)
			}
		}
	}
	return nil
}
func (t *managedTree) parent(n *treeNode) (*SubagentIdentity, string) {
	if key, known := t.lineage.Parent(treeNodeKey(n)); known {
		if t.lineage.IsRoot(key) {
			return &SubagentIdentity{SessionID: n.identity.SessionID}, "child"
		}
		p := t.nodes[key].identity
		_, rooted, _ := t.lineage.Depth(treeNodeKey(n))
		if rooted {
			return &p, "child"
		}
		return &p, "unresolved"
	}
	if n.parentTask != "" {
		return &SubagentIdentity{SessionID: n.identity.SessionID, TaskID: n.parentTask}, "unresolved"
	}
	return nil, "unresolved"
}
func (t *managedTree) announce(session string) {
	visited := make(map[*treeNode]bool)
	var emit func(*treeNode)
	emit = func(n *treeNode) {
		if visited[n] {
			return
		}
		visited[n] = true
		key, known := t.lineage.Parent(treeNodeKey(n))
		if known && !t.lineage.IsRoot(key) {
			emit(t.nodes[key])
		}
		parent, scope := t.parent(n)
		if n.announced && n.published == n.identity && equalTreeParent(n.publishedParent, parent) && n.publishedScope == scope {
			return
		}
		a := t.record("SubagentAssociated", SubagentIdentity{SessionID: session})
		a.Source, a.Parent, a.Scope, a.Evidence, a.Reason = n.identity, copyTreeParent(parent), scope, n.evidence, "first-observed"
		if n.parentTask != "" {
			a.Evidence += "+parent-task-id"
		}
		if known && n.identity.ToolUseID != "" {
			a.Evidence += "+observed-tool-owner"
		}
		if n.announced {
			a.Reason = "identity-enriched"
		}
		t.stage(a)
		n.announced, n.published, n.publishedParent, n.publishedScope = true, n.identity, copyTreeParent(parent), scope
	}
	for _, n := range t.order {
		if n.identity.SessionID == session {
			emit(n)
		}
	}
}

func equalTreeParent(a, b *SubagentIdentity) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func copyTreeParent(p *SubagentIdentity) *SubagentIdentity {
	if p == nil {
		return nil
	}
	copy := *p
	return &copy
}

func (t *managedTree) failRecord(native relationshipRecord, err error) (relationshipRecord, error) {
	t.client.fail(err)
	t.halted = true
	// No partial association fanout escapes. The connection is terminal, so
	// graph mutations from this rejected batch will never be used again.
	clear(t.pending)
	t.pending = nil
	native.Scope, native.Reason = "unresolved", "subagent-resource-limit"
	native.Source = treeFrameIdentity(*native.Native)
	// Drain earlier held frames before the overflow frame, preserving their
	// native order. This single reserved slot does not need fanout capacity.
	t.ready = &native
	r := t.record("ObservationState", native.Root)
	r.State, r.Reason = "incomplete", "association-limit"
	if limit, ok := err.(*ResourceLimitError); ok {
		switch limit.Resource {
		case "subagent dedup identities":
			r.Reason = "dedup-limit"
		case "subagent runs":
			r.Reason = "run-limit"
		case "subagent lineage depth":
			r.Reason = "depth-limit"
		case "subagent pending records":
			r.Reason = "fanout-limit"
		case "subagent unresolved events", "subagent unresolved bytes":
			r.Reason = "unresolved-source"
		}
	}
	return t.deliver(r)
}

// treeFrameIdentity reads native evidence only, including original request
// metadata. It cannot expose an association whose fanout was rejected.
func treeFrameIdentity(e Event) SubagentIdentity {
	source := SubagentIdentity{SessionID: e.SessionID}
	if e.AgentID != nil {
		source.AgentID = *e.AgentID
		if e.Type == "assistant" || e.Type == "user" {
			source.TaskID = *e.AgentID
		}
	}
	if e.ParentToolUseID != nil {
		source.ToolUseID = *e.ParentToolUseID
	}
	if e.System != nil {
		source.TaskID = e.System.TaskID
		if e.System.ToolUseID != nil {
			source.ToolUseID = *e.System.ToolUseID
		}
		if e.System.RunID != nil {
			source.RunID = *e.System.RunID
		}
	}
	if e.Control != nil {
		if metadata, err := e.Control.PermissionMetadata(); err == nil {
			source.AgentID, source.ToolUseID = metadata.AgentID, metadata.ToolUseID
		}
	}
	return source
}

// seed uses the existing graph rules but never treats a snapshot as live creation.
func (t *managedTree) seed(seeds []SubagentHistory) error {
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
		if err := t.lineage.Ensure(key); err != nil {
			return t.lineageLimit(err)
		}
		n := &treeNode{identity: SubagentIdentity{SessionID: seed.SessionID, TaskID: seed.TaskID, AssociationID: key.id}, parentTask: seed.ParentTaskID, evidence: "caller-verified-history"}
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

func (t *managedTree) trackRun(r *relationshipRecord, e Event) error {
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
		return &ResourceLimitError{Resource: "subagent runs", Limit: t.options.MaxRuns}
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
		d.Kind, d.Native = "ObservationState", nil
		d.State, d.Reason = "run-terminal", "native-task-terminal"
		d.Parent = copyTreeParent(r.Parent)
		if r.RunBackgrounded != nil {
			b := *r.RunBackgrounded
			d.RunBackgrounded = &b
		}
		t.stage(d)
	}
	return nil
}
