package agentclaude

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/zoumo/agent-sdk-go/agent"
)

const maxClaudeLiveState = 4096

type claudeLane struct {
	responseID string
}

type claudeBlockKey struct {
	parent   string
	response string
	index    int
}

type claudeBlock struct {
	kind          string
	stopped       bool
	completed     bool
	completedText string
	toolUseID     string
	toolName      string
}

type claudeCall struct {
	identity claudeToolIdentity
}

func (s *session) lane(parent string) (*claudeLane, error) {
	if s.lanes == nil {
		s.lanes = make(map[string]*claudeLane)
	}
	lane := s.lanes[parent]
	if lane == nil {
		if len(s.lanes) >= maxClaudeLiveState {
			return nil, liveStateExhausted(ErrorReasonLiveBlockLaneCapacity)
		}
		lane = &claudeLane{}
		s.lanes[parent] = lane
	}
	return lane, nil
}

func (s *session) response(parent string) string {
	lane := s.lanes[parent]
	if lane == nil {
		return ""
	}
	return lane.responseID
}

func (s *session) beginResponse(parent, response string) error {
	lane, err := s.lane(parent)
	if err != nil {
		return err
	}
	if lane.responseID != "" && lane.responseID != response {
		s.forgetResponseBlocks(parent, lane.responseID)
	}
	lane.responseID = response
	return nil
}

func (s *session) forgetResponseBlocks(parent, response string) {
	for key := range s.blocks {
		if key.parent == parent && key.response == response {
			delete(s.blocks, key)
		}
	}
}

func (s *session) trackedBlock(key claudeBlockKey, kind string) (*claudeBlock, error) {
	if s.blocks == nil {
		s.blocks = make(map[claudeBlockKey]*claudeBlock)
	}
	block := s.blocks[key]
	if block == nil {
		if len(s.blocks) >= maxClaudeLiveState {
			return nil, liveStateExhausted(ErrorReasonLiveBlockCapacity)
		}
		block = &claudeBlock{}
		s.blocks[key] = block
	}
	if block.kind == "" {
		block.kind = kind
	}
	return block, nil
}

func (s *session) noteBlockStart(
	parent string,
	index int,
	kind string,
	toolUseID string,
	toolName string,
) error {
	response := s.response(parent)
	if response == "" {
		return nil
	}
	block, err := s.trackedBlock(claudeBlockKey{
		parent:   parent,
		response: response,
		index:    index,
	}, kind)
	if err != nil {
		return err
	}
	if kind == "tool_use" {
		if block.toolUseID != "" && block.toolUseID != toolUseID {
			return claudeProtocolError(ErrorReasonConflictingToolBlockIdentity)
		}
		block.toolUseID = toolUseID
		block.toolName = toolName
	}
	return nil
}

func (s *session) noteBlockStop(parent string, index int) {
	response := s.response(parent)
	block := s.blocks[claudeBlockKey{
		parent:   parent,
		response: response,
		index:    index,
	}]
	if block != nil && !block.completed {
		block.stopped = true
	}
}

func (s *session) deltaBlock(
	key claudeBlockKey,
	kind string,
) (*claudeBlock, bool, error) {
	block, err := s.trackedBlock(key, kind)
	if err != nil {
		return nil, false, err
	}
	return block, block.kind == kind, nil
}

func (s *session) uniqueBlock(parent, response, kind string) (int, bool) {
	active := make([]int, 0, 1)
	stopped := make([]int, 0, 1)
	completed := make([]int, 0, 1)
	for key, block := range s.blocks {
		if key.parent != parent || key.response != response ||
			block.kind != kind {
			continue
		}
		if block.completed {
			completed = append(completed, key.index)
		} else if block.stopped {
			stopped = append(stopped, key.index)
		} else {
			active = append(active, key.index)
		}
	}
	if len(active) == 1 {
		return active[0], true
	}
	if len(active) != 0 || len(stopped) != 1 {
		if len(active) == 0 && len(stopped) == 0 && len(completed) == 1 {
			return completed[0], true
		}
		return 0, false
	}
	return stopped[0], true
}

func (s *session) observeToolCall(id string, identity claudeToolIdentity) error {
	if s.calls == nil {
		s.calls = make(map[string]claudeCall)
	}
	if _, exists := s.calls[id]; exists {
		return nil
	}
	if len(s.calls) >= maxClaudeLiveState {
		return liveStateExhausted(ErrorReasonToolCallCapacity)
	}
	s.calls[id] = claudeCall{identity: identity}
	return nil
}

func (s *session) toolCall(id string) (claudeCall, bool) {
	call, ok := s.calls[id]
	return call, ok
}

func (s *session) forgetToolCall(id string) {
	delete(s.calls, id)
}

func (s *session) blockIndexForTool(parent, response, toolID string) (int, bool) {
	for key, block := range s.blocks {
		if key.parent == parent && key.response == response &&
			block.toolUseID == toolID {
			return key.index, true
		}
	}
	return 0, false
}

func blockItemID(response string, index int) string {
	return response + ":" + strconv.Itoa(index)
}

func (key claudeBlockKey) itemID() string {
	if key.parent == "" {
		return blockItemID(key.response, key.index)
	}
	return key.parent + ":" + blockItemID(key.response, key.index)
}

func (key claudeBlockKey) meta() map[string]any {
	return map[string]any{
		"responseId": key.response,
		"blockIndex": key.index,
	}
}

func liveStateExhausted(reason string) error {
	return adapterError(
		agent.ErrorResourceExhausted,
		reason,
		"Claude live block state exceeded its limit",
		nil,
	)
}

func claudeProtocolError(reason string) error {
	return adapterError(
		agent.ErrorUnavailable,
		reason,
		"Claude protocol identity is inconsistent",
		nil,
	)
}

func decodeJSON(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}
