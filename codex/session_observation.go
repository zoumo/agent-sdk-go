package codex

import (
	"encoding/json"
	"errors"

	"github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/lineage"
)

func (s *Session) member(id string) bool { _, ok := s.members[id]; return ok }
func (s *Session) acceptLocked(value Received) {
	if s.closed {
		return
	}
	if err := s.queue.Push(value, int64(len(value.Raw))); err != nil {
		s.finishLocked(err)
	}
}
func (s *Session) observeLocked(o Observation) {
	s.acceptLocked(Received{Kind: ReceivedObservation, Observation: &o, Origin: "live"})
}
func (s *Session) addMemberLocked(id, method string) bool {
	if id == "" || s.closed {
		return false
	}
	if s.member(id) {
		return true
	}
	if len(s.members) >= s.maxMembers {
		s.finishLocked(ErrLimit)
		return false
	}
	if !s.retainAttachmentLocked(id) {
		s.observeLocked(Observation{ThreadID: id, Method: method, Reason: "attachment-owned-or-release-unconfirmed"})
		return false
	}
	s.members[id] = struct{}{}
	s.observeLocked(Observation{ThreadID: id, Method: method, Reason: "member-confirmed"})
	return true
}
func (s *Session) parentLocked(child, parent, method string) {
	if child == "" || parent == "" || s.closed {
		return
	}
	previous, known := s.parents.Parent(child)
	err := s.parents.Confirm(child, parent)
	if err != nil {
		if errors.Is(err, lineage.ErrCycle) || errors.Is(err, lineage.ErrConflict) {
			s.observeLocked(Observation{ThreadID: child, ParentThreadID: parent, Method: method, Reason: err.Error()})
		} else {
			s.observeLocked(Observation{ThreadID: child, ParentThreadID: parent, Method: method, Reason: errors.Join(ErrLimit, err).Error()})
		}
		return
	}
	if !known || previous != parent {
		s.observeLocked(Observation{ThreadID: child, ParentThreadID: parent, Method: method, Reason: "parent-confirmed"})
	}
}

// Thread metadata is evidence of a parent, independent of the membership set.
func nativeThreadParents(method string, params json.RawMessage) (child string, parents []string) {
	if method != "thread/started" {
		return
	}
	fields, err := objectFields(params)
	if err != nil {
		return
	}
	thread, err := objectFields(fields["thread"])
	if err != nil {
		return
	}
	child, _ = nativeIdentity(thread["id"])
	direct, _ := nativeIdentity(thread["parentThreadId"])
	if direct != "" {
		parents = append(parents, direct)
	}
	source, _ := objectFields(thread["source"])
	sub, _ := objectFields(source["subAgent"])
	spawn, _ := objectFields(sub["thread_spawn"])
	parent, _ := nativeIdentity(spawn["parent_thread_id"])
	if parent != "" && parent != direct {
		parents = append(parents, parent)
	}
	return
}
func (c *Client) publishSessionsLocked(method string, params json.RawMessage, raw []byte) {
	identity := treeIdentity(params)
	child, parents := nativeThreadParents(method, params)
	accepted := false
	for _, s := range c.sessions {
		if s.closed {
			continue
		}
		// Parent evidence may establish new membership. Never retain or replay an
		// earlier quarantined event to wait for this evidence.
		for _, parent := range parents {
			if s.member(parent) {
				s.addMemberLocked(child, method)
			}
		}
		if s.closed || !s.member(identity.ThreadID) {
			continue
		}
		accepted = true
		event := decodeEvent(method, params)
		event.Raw = append(json.RawMessage(nil), raw...)
		event.DecodeError = c.safeError(event.DecodeError)
		s.acceptLocked(Received{Kind: ReceivedEvent, Event: &event, Raw: event.Raw, Params: event.Params, Source: identity, Origin: "live"})
		for _, parent := range parents {
			s.parentLocked(child, parent, method)
		}
		if s.closed {
			continue
		}
		n, ok := event.Data.(*types.ItemCompletedNotification)
		if !ok || event.DecodeError != nil {
			continue
		}
		if tool := n.Item.CollabAgentToolCall; tool != nil && tool.Status == types.CollabAgentToolCallStatusCompleted && tool.SenderThreadID == identity.ThreadID {
			for _, receiver := range tool.ReceiverThreadIDs {
				if tool.Tool == types.CollabAgentToolSpawnAgent {
					if s.addMemberLocked(receiver, method) {
						s.parentLocked(receiver, identity.ThreadID, method)
					}
				} else if !s.member(receiver) {
					s.observeLocked(Observation{ThreadID: receiver, Method: method, Reason: "communication-target-outside-confirmed-scope"})
				}
			}
		}
		if activity := n.Item.SubAgentActivity; activity != nil && activity.Kind == types.SubAgentActivityKindStarted && identity.TurnID != "" && identity.ItemID != "" && identity.ItemID == activity.ID {
			if s.addMemberLocked(activity.AgentThreadID, method) {
				s.parentLocked(activity.AgentThreadID, identity.ThreadID, method)
			}
		}
	}
	if !accepted {
		c.quarantineLocked(raw)
	}
}
