package agentclaude

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/internal/uuidv4"
)

type claudeUsageState struct {
	model         string
	inputTokens   int64
	outputTokens  int64
	cacheRead     int64
	cacheCreation int64
	seen          bool
}

type claudeStreamFrame struct {
	Type         string          `json:"type"`
	ID           string          `json:"id"`
	Index        *int            `json:"index"`
	ContentBlock json.RawMessage `json:"content_block"`
	Delta        struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	Message struct {
		ID    string          `json:"id"`
		Model string          `json:"model"`
		Usage json.RawMessage `json:"usage"`
	} `json:"message"`
	Usage json.RawMessage `json:"usage"`
}

type claudeUsageValues struct {
	inputTokens   int64
	outputTokens  int64
	cacheRead     int64
	cacheCreation int64
}

type claudeCoordinateState uint8

const (
	coordinateMissing claudeCoordinateState = iota
	coordinateValid
	coordinateInvalid
)

func (s *session) convertReceived(received claude.Received) ([]agent.Event, error) {
	switch received.Kind {
	case claude.ReceivedNative:
		return s.convertEvent(received)
	case claude.ReceivedRequest:
		return s.convertRequest(received)
	case claude.ReceivedObservation:
		return s.observationGap(received)
	default:
		return nil, nil
	}
}

func (s *session) convertEvent(received claude.Received) ([]agent.Event, error) {
	event := received.Event
	if event == nil || event.IsReplay != nil && *event.IsReplay {
		return nil, nil
	}

	var (
		events []agent.Event
		err    error
	)
	switch {
	case event.Type == "system" && event.Subtype == "init":
		return nil, nil
	case event.Stream != nil:
		events, err = s.streamEvent(received)
	case event.Assistant != nil:
		events, err = s.assistantEvent(received)
	case event.Type == "user":
		events, err = s.userEvent(received)
	case event.Type == "system" && event.Subtype == "session_state_changed":
		events, err = s.stateEvent(received)
	case event.Type == "system" && event.Subtype == "commands_changed":
		events, err = s.commandsChangedEvent(received)
	case event.Result != nil:
		events, err = s.resultEvent(received)
	case event.Cancel != nil:
		events, err = s.cancelInteraction(received)
	case protocolControlEvent(event):
		return nil, nil
	default:
		events, err = s.gap(
			received,
			"native_event_unsupported",
			"Claude native event cannot be represented as a unified event",
			map[string]any{"eventType": event.Type, "eventSubtype": event.Subtype},
		)
	}
	if err != nil {
		return nil, err
	}
	if event.Error != nil && *event.Error != "" &&
		(event.Result == nil || !event.Result.IsError) {
		errorEvents, errorErr := s.envelopeErrorEvent(received)
		if errorErr != nil {
			return nil, errorErr
		}
		events = append(events, errorEvents...)
	}
	return events, nil
}

func protocolControlEvent(event *claude.Event) bool {
	return event.Response != nil
}

func (s *session) streamEvent(received claude.Received) ([]agent.Event, error) {
	var frame claudeStreamFrame
	if err := json.Unmarshal(received.Event.Stream.Event, &frame); err != nil {
		return s.gap(
			received,
			"stream_event_invalid",
			"Claude stream event cannot be decoded",
			nil,
		)
	}
	parent := claudeParent(received)
	switch frame.Type {
	case "message_start":
		response := frame.Message.ID
		if response == "" {
			response = frame.ID
		}
		if response == "" {
			return s.gap(
				received,
				"stream_response_identity_missing",
				"Claude message_start has no response identity",
				nil,
			)
		}
		if err := s.beginResponse(parent, response); err != nil {
			return nil, err
		}
		s.observeUsage(frame.Message.Usage, frame.Message.Model)
		return nil, nil
	case "content_block_start":
		if frame.Index == nil || *frame.Index < 0 {
			return s.gap(
				received,
				"stream_block_coordinate_missing",
				"Claude content_block_start has no valid block index",
				nil,
			)
		}
		if s.response(parent) == "" {
			return s.gap(
				received,
				"stream_response_missing",
				"Claude content block started before a response identity was observed",
				nil,
			)
		}
		var block struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if len(frame.ContentBlock) != 0 {
			if err := json.Unmarshal(frame.ContentBlock, &block); err != nil {
				return s.gap(
					received,
					"stream_content_block_invalid",
					"Claude content block start cannot be decoded",
					nil,
				)
			}
		}
		if block.Type == "" {
			return s.gap(
				received,
				"stream_content_block_type_missing",
				"Claude content block start has no block type",
				nil,
			)
		}
		if err := s.noteBlockStart(
			parent,
			*frame.Index,
			block.Type,
			block.ID,
			block.Name,
		); err != nil {
			return nil, err
		}
		return nil, nil
	case "content_block_delta":
		return s.streamDelta(received, parent, &frame)
	case "content_block_stop":
		if frame.Index != nil && *frame.Index >= 0 {
			s.noteBlockStop(parent, *frame.Index)
		}
		return nil, nil
	case "message_delta":
		s.observeUsage(frame.Usage, "")
		return nil, nil
	case "message_stop":
		return nil, nil
	default:
		return s.gap(
			received,
			"stream_event_unsupported",
			"Claude stream event type is not supported by this adapter",
			map[string]any{"streamType": frame.Type},
		)
	}
}

func (s *session) streamDelta(
	received claude.Received,
	parent string,
	frame *claudeStreamFrame,
) ([]agent.Event, error) {
	if frame.Delta.Type == "signature_delta" {
		return nil, nil
	}
	response := s.response(parent)
	if frame.ID != "" && response != "" && frame.ID != response {
		return s.gap(
			received,
			"stream_response_identity_conflict",
			"Claude stream delta response identity conflicts with the active response",
			map[string]any{"eventResponseId": frame.ID, "activeResponseId": response},
		)
	}
	if response == "" {
		return s.gap(
			received,
			"stream_response_missing",
			"Claude content delta arrived before a response identity was observed",
			nil,
		)
	}
	if frame.Index == nil || *frame.Index < 0 {
		return s.gap(
			received,
			"stream_block_coordinate_missing",
			"Claude content delta has no valid block index",
			nil,
		)
	}
	key := claudeBlockKey{parent: parent, response: response, index: *frame.Index}
	var kind string
	switch frame.Delta.Type {
	case "thinking_delta":
		kind = "thinking"
	case "text_delta":
		kind = "text"
	case "input_json_delta":
		kind = "tool_use"
	default:
		return s.gap(
			received,
			"stream_delta_unsupported",
			"Claude content delta type is not supported by this adapter",
			map[string]any{"deltaType": frame.Delta.Type},
		)
	}
	block, ok, err := s.deltaBlock(key, kind)
	if err != nil {
		return nil, err
	}
	if !ok {
		return s.gap(
			received,
			"stream_block_type_conflict",
			"Claude content delta does not match the started block type",
			map[string]any{"deltaType": frame.Delta.Type, "blockType": block.kind},
		)
	}
	if block.completed {
		return nil, nil
	}
	meta := key.meta()
	switch kind {
	case "text":
		event, eventErr := s.newEvent(
			received,
			key.itemID(),
			agent.MessageChunk{
				MessageID: key.itemID(),
				Content:   []agent.ContentBlock{agent.TextBlock{Text: frame.Delta.Text}},
			},
			nativeEventTime(received.Raw),
			meta,
		)
		if eventErr != nil {
			return nil, eventErr
		}
		return []agent.Event{event}, nil
	case "thinking":
		event, eventErr := s.newEvent(
			received,
			key.itemID(),
			agent.ThinkingChunk{
				MessageID: key.itemID(),
				Content:   []agent.ContentBlock{agent.TextBlock{Text: frame.Delta.Thinking}},
			},
			nativeEventTime(received.Raw),
			meta,
		)
		if eventErr != nil {
			return nil, eventErr
		}
		return []agent.Event{event}, nil
	default:
		if block.toolUseID == "" {
			return s.gap(
				received,
				"tool_chunk_identity_missing",
				"Claude tool input delta has no proven tool-use identity",
				meta,
			)
		}
		event, eventErr := s.newEvent(
			received,
			block.toolUseID,
			agent.ToolChunkPayload{
				ID: block.toolUseID,
				Content: []agent.ContentBlock{
					agent.TextBlock{Text: frame.Delta.PartialJSON},
				},
			},
			nativeEventTime(received.Raw),
			meta,
		)
		if eventErr != nil {
			return nil, eventErr
		}
		return []agent.Event{event}, nil
	}
}

func (s *session) assistantEvent(received claude.Received) ([]agent.Event, error) {
	message := received.Event.Assistant
	if message == nil {
		return s.gap(
			received,
			"invalid_assistant_event",
			"Claude assistant event has no message",
			nil,
		)
	}
	parent := claudeParent(received)
	response := message.ID
	if response == "" {
		response = s.response(parent)
	}
	if response != "" {
		if err := s.beginResponse(parent, response); err != nil {
			return nil, err
		}
	}
	if assistantAborted(received.Event.Raw) {
		return s.gap(
			received,
			"assistant_aborted",
			"Claude assistant frame was aborted and is not a complete message",
			map[string]any{"uuid": received.Event.UUID},
		)
	}
	if response == "" {
		return s.gap(
			received,
			"assistant_response_identity_missing",
			"Claude assistant message has no response identity",
			map[string]any{"uuid": received.Event.UUID},
		)
	}
	s.observeUsage(message.Usage, message.Model)

	blocks, err := assistantBlocks(message.Content)
	if err != nil {
		return s.gap(
			received,
			"assistant_content_invalid",
			"Claude assistant content cannot be decoded",
			nil,
		)
	}
	eventIndex, eventIndexState := assistantEventBlockIndex(received.Raw)
	visible := make([]map[string]json.RawMessage, 0, len(blocks))
	for _, block := range blocks {
		switch rawString(block, "type") {
		case "text", "thinking":
			visible = append(visible, block)
		}
	}

	var events []agent.Event
	for _, block := range blocks {
		kind, text, ok := visibleBlockText(block)
		if ok {
			key, coordinateErr := s.assistantBlockKey(
				parent,
				response,
				kind,
				block,
				len(visible),
				eventIndex,
				eventIndexState,
			)
			if coordinateErr != nil {
				gapEvents, gapErr := s.gap(
					received,
					"assistant_block_coordinate_missing",
					"Claude assistant block identity cannot be proven",
					map[string]any{"blockType": kind},
				)
				if gapErr != nil {
					return nil, gapErr
				}
				events = append(events, gapEvents...)
				continue
			}
			state, stateErr := s.trackedBlock(key, kind)
			if stateErr != nil {
				return nil, stateErr
			}
			if state.kind != kind {
				gapEvents, gapErr := s.gap(
					received,
					"assistant_block_type_conflict",
					"Claude assistant block type conflicts with streamed block state",
					map[string]any{"blockType": kind, "streamedType": state.kind},
				)
				if gapErr != nil {
					return nil, gapErr
				}
				events = append(events, gapEvents...)
				continue
			}
			if state.completed {
				if state.completedText != text {
					gapEvents, gapErr := s.gap(
						received,
						"assistant_block_reopened",
						"Claude assistant block changed after its complete content was delivered",
						map[string]any{"blockType": kind},
					)
					if gapErr != nil {
						return nil, gapErr
					}
					events = append(events, gapEvents...)
				}
				continue
			}
			if text == "" {
				state.completed = true
				state.completedText = text
				continue
			}
			var payload agent.Payload = agent.AgentMessage{
				MessageID: key.itemID(),
				Content:   []agent.ContentBlock{agent.TextBlock{Text: text}},
			}
			if kind == "thinking" {
				payload = agent.AgentThinking{
					MessageID: key.itemID(),
					Content:   []agent.ContentBlock{agent.TextBlock{Text: text}},
				}
			}
			event, eventErr := s.newEvent(
				received,
				key.itemID(),
				payload,
				nativeEventTime(received.Raw),
				key.meta(),
			)
			if eventErr != nil {
				return nil, eventErr
			}
			state.completed = true
			state.completedText = text
			events = append(events, event)
			continue
		}

		switch rawString(block, "type") {
		case "tool_use":
			toolEvents, toolErr := s.assistantToolUse(received, parent, response, block)
			if toolErr != nil {
				return nil, toolErr
			}
			events = append(events, toolEvents...)
		case "redacted_thinking":
			index, coordinate := blockCoordinate(block)
			if coordinate == coordinateValid {
				key := claudeBlockKey{parent: parent, response: response, index: index}
				state, stateErr := s.trackedBlock(key, "redacted_thinking")
				if stateErr != nil {
					return nil, stateErr
				}
				state.completed = true
			}
		default:
			gapEvents, gapErr := s.gap(
				received,
				"assistant_block_unsupported",
				"Claude assistant content block is not supported by this adapter",
				map[string]any{"blockType": rawString(block, "type")},
			)
			if gapErr != nil {
				return nil, gapErr
			}
			events = append(events, gapEvents...)
		}
	}
	return events, nil
}

func (s *session) assistantBlockKey(
	parent string,
	response string,
	kind string,
	block map[string]json.RawMessage,
	visibleCount int,
	eventIndex int,
	eventIndexState claudeCoordinateState,
) (claudeBlockKey, error) {
	index, coordinate := blockCoordinate(block)
	switch coordinate {
	case coordinateInvalid:
		return claudeBlockKey{}, claudeProtocolError("assistant_block_coordinate_invalid")
	case coordinateMissing:
		if visibleCount == 1 && eventIndexState == coordinateValid {
			index = eventIndex
			coordinate = coordinateValid
		}
	}
	if coordinate == coordinateMissing {
		if visibleCount != 1 {
			return claudeBlockKey{}, claudeProtocolError("assistant_block_coordinate_missing")
		}
		unique, ok := s.uniqueBlock(parent, response, kind)
		if !ok {
			return claudeBlockKey{}, claudeProtocolError("assistant_block_coordinate_missing")
		}
		index = unique
	}
	return claudeBlockKey{parent: parent, response: response, index: index}, nil
}

func (s *session) assistantToolUse(
	received claude.Received,
	parent string,
	response string,
	block map[string]json.RawMessage,
) ([]agent.Event, error) {
	toolID := rawString(block, "id")
	toolName := rawString(block, "name")
	if toolID == "" || toolName == "" {
		return s.gap(
			received,
			"tool_call_identity_missing",
			"Claude tool_use block has no tool identity",
			nil,
		)
	}
	if _, observed := s.toolCall(toolID); observed {
		return nil, nil
	}
	input, err := decodeJSON(block["input"])
	if err != nil {
		return s.gap(
			received,
			"tool_call_input_invalid",
			"Claude tool_use input cannot be decoded",
			map[string]any{"toolUseId": toolID},
		)
	}
	meta := map[string]any{"responseId": response, "toolUseId": toolID}
	if index, ok := s.blockIndexForTool(parent, response, toolID); ok {
		meta["blockIndex"] = index
	}
	event, eventErr := s.newEvent(
		received,
		toolID,
		agent.ToolCallPayload{
			ID:       toolID,
			Kind:     toolName,
			Name:     toolName,
			RawInput: input,
		},
		nativeEventTime(received.Raw),
		meta,
	)
	if eventErr != nil {
		return nil, eventErr
	}
	if observeErr := s.observeToolCall(toolID, toolName); observeErr != nil {
		return nil, observeErr
	}
	events := []agent.Event{event}
	if toolName == "TodoWrite" || toolName == "TaskCreate" || toolName == "TaskUpdate" {
		gapEvents, gapErr := s.gap(
			received,
			"plan_unsupported",
			"Claude task or todo data cannot be represented as a unified plan without proven priority",
			map[string]any{"toolUseId": toolID, "toolName": toolName},
		)
		if gapErr != nil {
			return nil, gapErr
		}
		events = append(events, gapEvents...)
	}
	return events, nil
}

func (s *session) userEvent(received claude.Received) ([]agent.Event, error) {
	message := received.Event.User
	if message == nil {
		return s.gap(
			received,
			"invalid_user_event",
			"Claude user event has no message",
			nil,
		)
	}
	if message.Content.Text != nil {
		if len(message.Content.Blocks) != 0 {
			return s.gap(
				received,
				"user_message_content_ambiguous",
				"Claude user message contains both text and block content",
				nil,
			)
		}
		return s.userMessageEvent(
			received,
			message,
			[]agent.ContentBlock{agent.TextBlock{Text: *message.Content.Text}},
		)
	}
	if len(message.Content.Blocks) == 0 {
		return s.gap(
			received,
			"user_message_content_missing",
			"Claude user message has no content",
			nil,
		)
	}

	var events []agent.Event
	var textContent []agent.ContentBlock
	appendGap := func(reason, messageText string, extras map[string]any) error {
		gapEvents, gapErr := s.gap(received, reason, messageText, extras)
		if gapErr != nil {
			return gapErr
		}
		events = append(events, gapEvents...)
		return nil
	}

	for index, block := range message.Content.Blocks {
		switch block.Type {
		case "text":
			if block.Text == nil {
				if err := appendGap(
					"user_text_block_missing",
					"Claude user text block cannot be decoded",
					map[string]any{"blockIndex": index},
				); err != nil {
					return nil, err
				}
				continue
			}
			textContent = append(
				textContent,
				agent.TextBlock{Text: block.Text.Text},
			)
		case "tool_result":
			if block.ToolResult == nil {
				if err := appendGap(
					"tool_result_block_missing",
					"Claude tool_result block cannot be decoded",
					map[string]any{"blockIndex": index},
				); err != nil {
					return nil, err
				}
				continue
			}
			resultEvents, err := s.toolResultEvent(received, block.ToolResult)
			if err != nil {
				return nil, err
			}
			events = append(events, resultEvents...)
		default:
			if err := appendGap(
				"user_message_content_unsupported",
				"Claude user message content block is not supported by this adapter",
				map[string]any{"blockType": block.Type, "blockIndex": index},
			); err != nil {
				return nil, err
			}
		}
	}
	if len(textContent) != 0 {
		messageEvents, err := s.userMessageEvent(received, message, textContent)
		if err != nil {
			return nil, err
		}
		events = append(messageEvents, events...)
	}
	if len(events) == 0 {
		return s.gap(
			received,
			"user_message_content_missing",
			"Claude user message has no representable content",
			nil,
		)
	}
	return events, nil
}

func (s *session) userMessageEvent(
	received claude.Received,
	message *claude.Message,
	content []agent.ContentBlock,
) ([]agent.Event, error) {
	messageID := claudeUserMessageID(received.Event, message)
	if messageID == "" {
		return s.gap(
			received,
			"user_message_identity_missing",
			"Claude user message has no stable native identity",
			nil,
		)
	}
	event, err := s.newEvent(
		received,
		messageID,
		agent.UserMessage{
			MessageID: messageID,
			Content:   content,
		},
		nativeEventTime(received.Raw),
		map[string]any{"blockCount": len(content)},
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func claudeUserMessageID(event *claude.Event, message *claude.Message) string {
	if message != nil && message.ID != "" {
		return message.ID
	}
	if event == nil {
		return ""
	}
	if event.UserMessageUUID != nil && *event.UserMessageUUID != "" {
		return *event.UserMessageUUID
	}
	return event.UUID
}

func (s *session) observationGap(
	received claude.Received,
) ([]agent.Event, error) {
	observation := received.Observation
	if observation == nil {
		return s.gap(
			received,
			"native_observation_missing",
			"Claude relationship observation has no evidence",
			nil,
		)
	}
	if confirmedRelationship(observation.Reason) {
		return nil, nil
	}
	extras := map[string]any{
		"observationReason": observation.Reason,
		"evidence":          observation.Evidence,
		"source":            subagentIdentityMeta(observation.Source),
	}
	if observation.Parent != nil {
		extras["parent"] = subagentIdentityMeta(*observation.Parent)
	}
	return s.gap(
		received,
		"native_relationship_gap",
		"Claude relationship evidence cannot be represented as a unified normal event",
		extras,
	)
}

func confirmedRelationship(reason string) bool {
	return reason == "member-confirmed" || reason == "parent-confirmed"
}

func subagentIdentityMeta(identity claude.SubagentIdentity) map[string]any {
	meta := map[string]any{}
	if identity.AssociationID != "" {
		meta["associationId"] = identity.AssociationID
	}
	if identity.RunID != "" {
		meta["runId"] = identity.RunID
	}
	if identity.SessionID != "" {
		meta["sessionId"] = identity.SessionID
	}
	if identity.AgentID != "" {
		meta["agentId"] = identity.AgentID
	}
	if identity.TaskID != "" {
		meta["taskId"] = identity.TaskID
	}
	if identity.ToolUseID != "" {
		meta["toolUseId"] = identity.ToolUseID
	}
	return meta
}

func (s *session) toolResultEvent(
	received claude.Received,
	block *claude.ToolResultBlock,
) ([]agent.Event, error) {
	if block.ToolUseID == "" {
		return s.gap(
			received,
			"tool_result_identity_missing",
			"Claude tool_result block has no tool-use identity",
			nil,
		)
	}
	call, ok := s.toolCall(block.ToolUseID)
	if !ok {
		return s.gap(
			received,
			"tool_result_without_call",
			"Claude tool result has no previously observed tool call",
			map[string]any{"toolUseId": block.ToolUseID},
		)
	}
	content, provided, err := toolResultContent(block.Content)
	if err != nil {
		return s.gap(
			received,
			"tool_result_content_unsupported",
			"Claude tool result content cannot be represented as unified content",
			map[string]any{"toolUseId": block.ToolUseID},
		)
	}
	var rawOutput any
	rawOutputPresent := false
	if len(received.Event.ToolUseResult) != 0 &&
		!bytes.Equal(bytes.TrimSpace(received.Event.ToolUseResult), []byte("null")) {
		rawOutput, err = decodeJSON(received.Event.ToolUseResult)
		if err != nil {
			return s.gap(
				received,
				"tool_result_output_invalid",
				"Claude tool_use_result cannot be decoded",
				map[string]any{"toolUseId": block.ToolUseID},
			)
		}
		rawOutputPresent = true
	} else if len(block.Content) != 0 &&
		!bytes.Equal(bytes.TrimSpace(block.Content), []byte("null")) {
		rawOutput, err = decodeJSON(block.Content)
		if err != nil {
			return s.gap(
				received,
				"tool_result_output_invalid",
				"Claude tool result content cannot be decoded",
				map[string]any{"toolUseId": block.ToolUseID},
			)
		}
		rawOutputPresent = true
	}
	status := "completed"
	if block.IsError {
		status = "failed"
	}
	output := &agent.ToolOutput{
		Availability: "unavailable",
		Completeness: "unknown",
	}
	if provided {
		output = &agent.ToolOutput{
			Availability: "provided",
			Completeness: "retained",
		}
	}
	var pointer *any
	if rawOutputPresent {
		pointer = &rawOutput
	}
	event, eventErr := s.newEvent(
		received,
		block.ToolUseID,
		agent.ToolResultPayload{
			ID:        block.ToolUseID,
			Kind:      call.name,
			Name:      call.name,
			RawOutput: pointer,
			Content:   content,
			Output:    output,
			Status:    status,
		},
		nativeEventTime(received.Raw),
		map[string]any{
			"toolUseId": block.ToolUseID,
			"isError":   block.IsError,
		},
	)
	if eventErr != nil {
		return nil, eventErr
	}
	s.forgetToolCall(block.ToolUseID)
	return []agent.Event{event}, nil
}

func (s *session) stateEvent(received claude.Received) ([]agent.Event, error) {
	event := received.Event
	if event.System == nil || event.System.State == nil || *event.System.State == "" {
		return s.gap(
			received,
			"session_state_missing",
			"Claude session state event has no state",
			nil,
		)
	}

	var state agent.SessionState
	switch *event.System.State {
	case string(agent.StateIdle):
		state = agent.StateIdle
	case string(agent.StateRunning):
		state = agent.StateRunning
	case string(agent.StateRequiresAction):
		state = agent.StateRequiresAction
	default:
		previous := s.markUnknownState()
		return s.gap(
			received,
			"session_state_unsupported",
			"Claude session state is not in the unified state set",
			map[string]any{
				"nativeState":   *event.System.State,
				"previousState": stringOrNil(previous),
			},
		)
	}

	at := nativeEventTime(received.Raw)
	settled := s.settleInteractionsForState(state)
	closed, err := s.closedInteractionEvents(
		settled,
		agent.InteractionExpired,
		"native_state_advanced",
		"native_state_advanced",
		at,
	)
	if err != nil {
		return nil, err
	}
	previous := s.updateState(state)
	next := state
	unified, err := s.newEvent(
		received,
		"",
		agent.StateUpdatePayload{
			PreviousState: previous,
			State:         &next,
		},
		at,
		map[string]any{"nativeState": *event.System.State},
	)
	if err != nil {
		return nil, err
	}
	return append(closed, unified), nil
}

func (s *session) commandsChangedEvent(
	received claude.Received,
) ([]agent.Event, error) {
	var raw struct {
		Commands []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(received.Event.Raw, &raw); err != nil {
		return s.gap(
			received,
			"commands_changed_invalid",
			"Claude commands_changed event cannot be decoded",
			nil,
		)
	}
	commands := make([]agent.AvailableCommand, 0, len(raw.Commands))
	for _, command := range raw.Commands {
		if command.Name == "" {
			return s.gap(
				received,
				"available_command_identity_missing",
				"Claude available command has no name",
				nil,
			)
		}
		commands = append(commands, agent.AvailableCommand{
			Name:        command.Name,
			Description: command.Description,
		})
	}
	event, err := s.newEvent(
		received,
		"",
		agent.AvailableCommandsUpdatePayload{AvailableCommands: commands},
		nativeEventTime(received.Raw),
		map[string]any{"commands": append(json.RawMessage(nil), received.Event.Raw...)},
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) initialCommandsEvent(init claude.Event) ([]agent.Event, error) {
	if init.System == nil {
		return nil, nil
	}
	commands := make([]agent.AvailableCommand, 0, len(init.System.SlashCommands))
	for _, name := range init.System.SlashCommands {
		if name == "" {
			return s.gap(
				claude.Received{Event: &init, Raw: init.Raw},
				"available_command_identity_missing",
				"Claude init available command has no name",
				nil,
			)
		}
		commands = append(commands, agent.AvailableCommand{Name: name})
	}
	event, err := s.newEvent(
		claude.Received{Event: &init, Raw: init.Raw},
		"",
		agent.AvailableCommandsUpdatePayload{AvailableCommands: commands},
		nativeEventTime(init.Raw),
		nil,
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) resultEvent(received claude.Received) ([]agent.Event, error) {
	result := received.Event.Result
	if result == nil {
		return nil, nil
	}
	events, err := s.usageEvents(received)
	if err != nil {
		return nil, err
	}
	if !result.IsError {
		return events, nil
	}
	message := strings.TrimSpace(result.Result)
	if message == "" && len(result.Errors) != 0 {
		message = strings.Join(result.Errors, "; ")
	}
	if message == "" {
		message = "Claude returned an error result"
	}
	unified, err := s.newEvent(
		received,
		"",
		agent.SessionErrorPayload{
			Message: message,
			Reason:  "native_result_error",
		},
		nativeEventTime(received.Raw),
		map[string]any{
			"resultSubtype":  result.Subtype,
			"terminalReason": result.TerminalReason,
		},
	)
	if err != nil {
		return nil, err
	}
	return append(events, unified), nil
}

func (s *session) usageEvents(received claude.Received) ([]agent.Event, error) {
	result := received.Event.Result
	if result == nil || len(result.ModelUsage) == 0 {
		return nil, nil
	}
	var (
		key   string
		usage claude.ModelUsage
	)
	if len(result.ModelUsage) == 1 {
		for model, value := range result.ModelUsage {
			key, usage = model, value
		}
	} else {
		value, ok := result.ModelUsage[s.usage.model]
		if !ok {
			return s.gap(
				received,
				"usage_model_ambiguous",
				"Claude result usage cannot be attributed to the observed model",
				map[string]any{"model": s.usage.model},
			)
		}
		key, usage = s.usage.model, value
	}
	if usage.ContextWindow < 0 {
		return s.gap(
			received,
			"usage_values_invalid",
			"Claude result contains a negative context-window size",
			map[string]any{"model": key},
		)
	}
	used := usage.InputTokens + usage.OutputTokens +
		usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	if used < 0 {
		return s.gap(
			received,
			"usage_values_invalid",
			"Claude result contains negative token usage",
			map[string]any{"model": key},
		)
	}
	payload := agent.UsageUpdatePayload{
		Used: used,
		Size: usage.ContextWindow,
	}
	cost := modelCostNumber(usage.Raw)
	if cost == "" && result.TotalCostUSD != nil {
		cost = strconv.FormatFloat(*result.TotalCostUSD, 'g', -1, 64)
	}
	if cost != "" {
		payload.Cost = &agent.UsageCost{
			Amount:   json.Number(cost),
			Currency: "USD",
		}
	}
	event, err := s.newEvent(
		received,
		"",
		payload,
		nativeEventTime(received.Raw),
		map[string]any{
			"model":               key,
			"contextWindowAbsent": usage.ContextWindow == 0,
		},
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func modelCostNumber(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value struct {
		CostUSD json.RawMessage `json:"costUSD"`
	}
	if json.Unmarshal(raw, &value) != nil || len(value.CostUSD) == 0 {
		return ""
	}
	var number json.Number
	if json.Unmarshal(value.CostUSD, &number) != nil {
		return ""
	}
	return number.String()
}

func (s *session) observeUsage(raw json.RawMessage, model string) {
	if model != "" {
		s.usage.model = model
	}
	values, ok := parseClaudeUsage(raw)
	if !ok {
		return
	}
	s.usage.inputTokens = values.inputTokens
	s.usage.outputTokens = values.outputTokens
	s.usage.cacheRead = values.cacheRead
	s.usage.cacheCreation = values.cacheCreation
	s.usage.seen = true
}

func parseClaudeUsage(raw json.RawMessage) (claudeUsageValues, bool) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return claudeUsageValues{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil {
		return claudeUsageValues{}, false
	}
	read := func(name string) (int64, bool) {
		value, present := values[name]
		if !present {
			return 0, false
		}
		number, ok := value.(json.Number)
		if !ok {
			return 0, false
		}
		parsed, err := number.Int64()
		if err != nil || parsed < 0 {
			return 0, false
		}
		return parsed, true
	}
	var result claudeUsageValues
	var present bool
	if value, ok := read("input_tokens"); ok {
		result.inputTokens, present = value, true
	}
	if value, ok := read("output_tokens"); ok {
		result.outputTokens, present = value, true
	}
	if value, ok := read("cache_read_input_tokens"); ok {
		result.cacheRead, present = value, true
	}
	if value, ok := read("cache_creation_input_tokens"); ok {
		result.cacheCreation, present = value, true
	}
	return result, present
}

func (s *session) envelopeErrorEvent(
	received claude.Received,
) ([]agent.Event, error) {
	unified, err := s.newEvent(
		received,
		"",
		agent.SessionErrorPayload{
			Message: *received.Event.Error,
			Reason:  "native_error",
		},
		nativeEventTime(received.Raw),
		nil,
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{unified}, nil
}

func (s *session) gap(
	received claude.Received,
	reason string,
	message string,
	extras map[string]any,
) ([]agent.Event, error) {
	event, err := s.newEvent(
		received,
		"",
		agent.SessionErrorPayload{Message: message, Reason: reason},
		nativeEventTime(received.Raw),
		extras,
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) newEvent(
	received claude.Received,
	itemID string,
	payload agent.Payload,
	eventTime time.Time,
	extras map[string]any,
) (agent.Event, error) {
	eventID, err := uuidv4.New()
	if err != nil {
		return agent.Event{}, adapterError(
			agent.ErrorUnavailable,
			"event_id_failed",
			"create unified event identity",
			err,
		)
	}
	if eventTime.IsZero() {
		eventTime = time.Now().UTC()
	}

	raw := received.Raw
	eventType := ""
	subtype := ""
	if received.Event != nil {
		raw = received.Event.Raw
		eventType = received.Event.Type
		subtype = received.Event.Subtype
	}
	if received.Request != nil {
		eventType = "control_request"
		subtype = received.Request.Method()
		raw = received.Request.Raw()
	}

	claudeMeta := map[string]any{}
	if len(raw) != 0 {
		claudeMeta["raw"] = append(json.RawMessage(nil), raw...)
	}
	if eventType != "" {
		claudeMeta["type"] = eventType
	}
	if subtype != "" {
		claudeMeta["subtype"] = subtype
	}
	if received.Event != nil {
		if received.Event.UUID != "" {
			claudeMeta["uuid"] = received.Event.UUID
		}
		if received.Event.UserMessageUUID != nil {
			claudeMeta["userMessageUuid"] = *received.Event.UserMessageUUID
		}
		if received.Event.AgentID != nil {
			claudeMeta["agentId"] = *received.Event.AgentID
		}
		if received.Event.ParentToolUseID != nil {
			claudeMeta["parentToolUseId"] = *received.Event.ParentToolUseID
		}
	}
	if source := subagentIdentityMeta(received.Source); len(source) != 0 {
		claudeMeta["source"] = source
	}
	if received.Parent != nil {
		if parent := subagentIdentityMeta(*received.Parent); len(parent) != 0 {
			claudeMeta["parent"] = parent
		}
	}
	for key, value := range extras {
		if _, reserved := claudeMeta[key]; !reserved {
			claudeMeta[key] = value
		}
	}

	event := agent.Event{
		EventID:   eventID,
		SessionID: s.ID(),
		Time:      eventTime,
		ItemID:    itemID,
		Payload:   payload,
		Meta: map[string]any{
			"claude": claudeMeta,
		},
	}
	if err := event.Validate(); err != nil {
		return agent.Event{}, adapterError(
			agent.ErrorUnavailable,
			"event_invalid",
			"convert native Claude event",
			err,
		)
	}
	return event, nil
}

func nativeEventTime(raw json.RawMessage) time.Time {
	var envelope struct {
		Timestamp json.RawMessage `json:"timestamp"`
	}
	var value string
	if json.Unmarshal(raw, &envelope) == nil &&
		json.Unmarshal(envelope.Timestamp, &value) == nil &&
		value != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Now().UTC()
}

func claudeParent(received claude.Received) string {
	if received.Event != nil && received.Event.ParentToolUseID != nil {
		return *received.Event.ParentToolUseID
	}
	return ""
}

func assistantAborted(raw json.RawMessage) bool {
	var envelope struct {
		Aborted bool `json:"aborted"`
	}
	return json.Unmarshal(raw, &envelope) == nil && envelope.Aborted
}

func assistantBlocks(content claude.Content) ([]map[string]json.RawMessage, error) {
	if content.Text != nil {
		raw, err := json.Marshal(map[string]any{
			"type": "text",
			"text": *content.Text,
		})
		if err != nil {
			return nil, err
		}
		var block map[string]json.RawMessage
		if err := json.Unmarshal(raw, &block); err != nil {
			return nil, err
		}
		return []map[string]json.RawMessage{block}, nil
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(content.Raw, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

func visibleBlockText(block map[string]json.RawMessage) (string, string, bool) {
	switch rawString(block, "type") {
	case "text":
		var text *string
		if json.Unmarshal(block["text"], &text) != nil || text == nil {
			return "text", "", true
		}
		return "text", *text, true
	case "thinking":
		var text *string
		if json.Unmarshal(block["thinking"], &text) != nil || text == nil {
			return "thinking", "", true
		}
		return "thinking", *text, true
	default:
		return "", "", false
	}
}

func rawString(block map[string]json.RawMessage, name string) string {
	var value string
	_ = json.Unmarshal(block[name], &value)
	return value
}

func blockCoordinate(block map[string]json.RawMessage) (int, claudeCoordinateState) {
	index := -1
	for _, name := range []string{"apiBlockIndex", "index"} {
		raw, present := block[name]
		if !present {
			continue
		}
		var number *json.Number
		if err := json.Unmarshal(raw, &number); err != nil || number == nil {
			return 0, coordinateInvalid
		}
		parsed, ok := nonNegativeIndex(*number)
		if !ok || index >= 0 && parsed != index {
			return 0, coordinateInvalid
		}
		index = parsed
	}
	if index < 0 {
		return 0, coordinateMissing
	}
	return index, coordinateValid
}

func assistantEventBlockIndex(
	raw json.RawMessage,
) (int, claudeCoordinateState) {
	var envelope struct {
		APIBlockIndex json.RawMessage `json:"apiBlockIndex"`
	}
	if json.Unmarshal(raw, &envelope) != nil || len(envelope.APIBlockIndex) == 0 {
		return 0, coordinateMissing
	}
	var number *json.Number
	if json.Unmarshal(envelope.APIBlockIndex, &number) != nil || number == nil {
		return 0, coordinateInvalid
	}
	index, ok := nonNegativeIndex(*number)
	if !ok {
		return 0, coordinateInvalid
	}
	return index, coordinateValid
}

func nonNegativeIndex(number json.Number) (int, bool) {
	index, err := strconv.Atoi(number.String())
	if err != nil || index < 0 {
		return 0, false
	}
	return index, true
}

func toolResultContent(
	raw json.RawMessage,
) ([]agent.ContentBlock, bool, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	switch trimmed[0] {
	case '"':
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, false, err
		}
		return []agent.ContentBlock{agent.TextBlock{Text: text}}, true, nil
	case '[':
		var blocks []map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &blocks); err != nil {
			return nil, false, err
		}
		content := make([]agent.ContentBlock, 0, len(blocks))
		for _, block := range blocks {
			if rawString(block, "type") != "text" {
				return nil, false, claudeProtocolError("tool_result_content_unsupported")
			}
			var text *string
			if json.Unmarshal(block["text"], &text) != nil || text == nil {
				return nil, false, claudeProtocolError("tool_result_text_invalid")
			}
			content = append(content, agent.TextBlock{Text: *text})
		}
		return content, true, nil
	default:
		return nil, false, claudeProtocolError("tool_result_content_unsupported")
	}
}

func stringOrNil(value *agent.SessionState) any {
	if value == nil {
		return nil
	}
	return string(*value)
}
