// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// TreeOptions bounds the single-layer association index. Zero selects 4096;
// values above 65536 or below zero are rejected before starting a process.
// Recursive lineage, history seeds and run tracking are not yet supported.
type TreeOptions struct{ MaxNodes int }

// TreeIdentity preserves distinct native identity namespaces. Empty fields mean
// unknown, never an inferred session/agent/tool identity. ToolUseID can identify
// an unresolved parent tool reference without identifying its owning agent.
type TreeIdentity struct {
	SessionID string `json:"session_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
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
	Native     *Event        `json:"native,omitempty"`
}

type treeKey struct{ session, id string }
type treeNode struct {
	identity TreeIdentity
	parent   *TreeIdentity
}

// TreeClient owns a private Client and its sole Receive consumer. Like Client,
// methods are concurrency-safe except Receive, which requires one caller.
// It adds no reader goroutine, child process, subscription, resume or turn.
// PermissionHandler receives the original sole answerable request handle.
type TreeClient struct {
	client    *Client
	options   TreeOptions
	nodes     map[treeKey]treeNode
	rootTools map[treeKey]struct{}
	pending   []TreeRecord // at most association + native
	sequence  uint64
	startup   bool
	end       error
}

// OpenTree enables native forwarding before initialize. A rejected initialize
// fails OpenTree; an accepted initialize still cannot verify that the installed
// CLI honors forwarding. The first record reports unverified, never complete.
func OpenTree(ctx context.Context, options Options, treeOptions TreeOptions) (*TreeClient, error) {
	if treeOptions.MaxNodes < 0 || treeOptions.MaxNodes > 65536 {
		return nil, fmt.Errorf("claude: MaxNodes must be between 0 and 65536")
	}
	if treeOptions.MaxNodes == 0 {
		treeOptions.MaxNodes = 4096
	}
	options.ForwardSubagentText = true
	c, err := Open(ctx, options)
	if err != nil {
		return nil, err
	}
	return &TreeClient{client: c, options: treeOptions, nodes: make(map[treeKey]treeNode), rootTools: make(map[treeKey]struct{}), startup: true}, nil
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
// missing session identities stay unresolved; this ticket does not buffer for
// future recursive lineage or deduplicate provider events. Every accepted native
// frame is delivered once through this entry point, including identical deltas.
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
	// Use only this frame's native session identity. A later SessionID() value
	// could already belong to another frame and must not establish an association.
	root := TreeIdentity{SessionID: e.SessionID}
	r := t.record("Native", root)
	r.Origin, r.Native = "native", &e
	r.Source = root
	r.Scope = "root"
	if e.SessionID == "" {
		r.Scope = "unscoped"
	}
	id, evidence := "", ""
	identity := TreeIdentity{SessionID: e.SessionID}
	if e.Control != nil {
		if metadata, err := e.Control.PermissionMetadata(); err == nil && metadata.AgentID != "" {
			id = "agent:" + metadata.AgentID
			identity.AgentID = metadata.AgentID
			evidence = "permission-metadata-agent-id"
		}
	}
	if e.AgentID != nil && *e.AgentID != "" {
		id = "agent:" + *e.AgentID
		identity.AgentID = *e.AgentID
		evidence = "native-agent-id"
		// The fixed message contract defines agent_id as task_id. Do not apply
		// that mapping to arbitrary future event types.
		if e.Type == "assistant" || e.Type == "user" {
			identity.TaskID = *e.AgentID
			id = "task:" + *e.AgentID
			evidence = "message-agent-id-is-task-id"
		}
	} else if e.System != nil && e.Subtype == "task_started" && e.System.TaskType != nil && *e.System.TaskType == "local_agent" && e.System.TaskID != "" {
		id = "task:" + e.System.TaskID
		identity.TaskID = e.System.TaskID
		evidence = "local-agent-task-started"
	}
	if e.ParentToolUseID != nil {
		identity.ToolUseID = *e.ParentToolUseID
	}
	if e.System != nil && e.System.ToolUseID != nil && identity.TaskID != "" {
		identity.ToolUseID = *e.System.ToolUseID
	}
	if id != "" {
		r.Source = identity
		r.Scope = "unresolved"
		r.Evidence = evidence
		if e.SessionID != "" {
			key := treeKey{e.SessionID, id}
			if identity.AgentID != "" && identity.TaskID == "" {
				verified := treeKey{e.SessionID, "task:" + identity.AgentID}
				if known, ok := t.nodes[verified]; ok && known.identity.AgentID == identity.AgentID {
					key = verified
					identity.TaskID = known.identity.TaskID
				}
			}
			node, exists := t.nodes[key]
			parent := node.parent
			if _, ok := t.rootTools[treeKey{e.SessionID, identity.ToolUseID}]; ok && identity.ToolUseID != "" {
				p := root
				parent = &p
			}
			changed := !exists || (node.identity.AgentID == "" && identity.AgentID != "") || (node.identity.TaskID == "" && identity.TaskID != "") || (node.parent == nil && parent != nil)
			if changed {
				if !exists && len(t.nodes)+len(t.rootTools) >= t.options.MaxNodes {
					return t.limit(r)
				}
				if identity.AgentID == "" {
					identity.AgentID = node.identity.AgentID
				}
				if identity.TaskID == "" {
					identity.TaskID = node.identity.TaskID
				}
				if identity.ToolUseID == "" {
					identity.ToolUseID = node.identity.ToolUseID
				}
				t.nodes[key] = treeNode{identity, copyTreeParent(parent)}
				a := t.record("SubagentAssociated", root)
				a.Source, a.Parent, a.Scope, a.Evidence, a.Reason = identity, copyTreeParent(parent), "unresolved", evidence, "first-observed"
				if exists {
					a.Reason = "identity-enriched"
				}
				if parent != nil {
					a.Scope = "child"
				}
				t.pending = append(t.pending, a)
			}
			r.Source, r.Parent = identity, copyTreeParent(parent)
			if parent != nil {
				r.Scope = "child"
			}
		}
	} else if e.ParentToolUseID != nil && *e.ParentToolUseID != "" {
		r.Source = identity
		r.Scope = "unresolved"
		r.Reason = "tool-only-lineage-unsupported"
	} else if e.Type != "assistant" && e.Type != "user" && e.Type != "system" && e.Type != "result" && e.Type != "stream_event" {
		r.Scope = "unscoped"
	}
	// Only an observed root assistant tool proves that tool's owner is root.
	if r.Scope == "root" && e.SessionID != "" && e.Assistant != nil {
		for _, b := range e.Assistant.Content.Blocks {
			if b.ToolUse == nil || b.ToolUse.ID == "" {
				continue
			}
			key := treeKey{e.SessionID, b.ToolUse.ID}
			if _, ok := t.rootTools[key]; ok {
				continue
			}
			if len(t.nodes)+len(t.rootTools) >= t.options.MaxNodes {
				return t.limit(r)
			}
			t.rootTools[key] = struct{}{}
		}
	}
	t.pending = append(t.pending, r)
	next := t.pending[0]
	t.pending[0] = TreeRecord{}
	t.pending = t.pending[1:]
	return t.deliver(next)
}

func copyTreeParent(p *TreeIdentity) *TreeIdentity {
	if p == nil {
		return nil
	}
	copy := *p
	return &copy
}

func (t *TreeClient) limit(native TreeRecord) (TreeRecord, error) {
	err := &ResourceLimitError{Resource: "tree association entries", Limit: t.options.MaxNodes}
	t.client.fail(err)
	// Keep the already accepted native frame; no silent loss at this bound.
	t.pending = append(t.pending, native)
	r := t.record("ObservationState", native.Root)
	r.State, r.Reason = "incomplete", "association-limit"
	return t.deliver(r)
}
