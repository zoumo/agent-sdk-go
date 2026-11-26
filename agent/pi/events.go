package agentpi

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/eventenvelope"
	"github.com/zoumo/agent-sdk-go/client/pi"
	"github.com/zoumo/agent-sdk-go/internal/uuidv4"
)

// project runs on the single native reader. Each supported frame yields its
// events in wire order; unmapped frames retain the complete original object.
func (s *session) project(native pi.Event) ([]agent.Event, error) {
	var f struct {
		Aborted bool `json:"aborted"`
		Message struct {
			Role         string          `json:"role"`
			Content      json.RawMessage `json:"content"`
			Timestamp    int64           `json:"timestamp"`
			StopReason   string          `json:"stopReason"`
			ErrorMessage string          `json:"errorMessage"`
		} `json:"message"`
		Update struct {
			Type         string `json:"type"`
			Delta        string `json:"delta"`
			ContentIndex int    `json:"contentIndex"`
		} `json:"assistantMessageEvent"`
		ToolCallID string          `json:"toolCallId"`
		ToolName   string          `json:"toolName"`
		Args       json.RawMessage `json:"args"`
		Result     json.RawMessage `json:"result"`
		IsError    bool            `json:"isError"`
		DurationMs *float64        `json:"durationMs"`
	}
	envelope := eventenvelope.Envelope{SessionID: s.ID()}
	var occurred time.Time
	var item string
	var nativeRequestID string
	var payloads []agent.Payload
	fallback := agent.UnsupportedNativeEventPayload{AgentType: "pi", Raw: native.Raw}
	if native.Type == "extension_ui_request" || json.Unmarshal(native.Raw, &f) == nil {
		switch native.Type {
		case "extension_ui_request":
			var err error
			payloads, item, nativeRequestID, err = s.registerUI(native)
			if err != nil {
				return nil, err
			}
		case "agent_start", "agent_settled":
			if native.Type == "agent_settled" {
				payloads = append(payloads, s.settleInteractions("", agent.ErrorReasonNativeRequestEnded, false, false)...)
			}
			payload, err := s.changeState(native.Type == "agent_start", f.Aborted)
			if err != nil {
				return nil, err
			}
			payloads = append(payloads, payload)
		case "message_start":
			s.messageCount++
			s.messageID = "message-" + strconv.FormatUint(s.messageCount, 10)
			// Pi's start frame can have empty content. Preserve it natively.
		case "message_update":
			item = s.messageID
			content := textContent(f.Update.Delta)
			if item != "" && f.Update.Delta != "" && f.Update.ContentIndex >= 0 {
				item = blockID(item, f.Update.ContentIndex)
				switch f.Update.Type {
				case "text_delta":
					payloads = append(payloads, agent.MessageChunk{MessageID: item, Content: content})
				case "thinking_delta":
					payloads = append(payloads, agent.ThinkingChunk{MessageID: item, Content: content})
				}
			}
		case "message_end":
			item = s.messageID
			if item == "" {
				s.messageCount++
				item = "message-" + strconv.FormatUint(s.messageCount, 10)
			}
			s.messageID = ""
			if f.Message.Timestamp > 0 {
				occurred = time.UnixMilli(f.Message.Timestamp).UTC()
			}
			message, complete := messagePayloads(f.Message.Role, item, f.Message.Content)
			payloads = append(payloads, message...)
			if !complete {
				payloads = append(payloads, fallback)
			}
			if f.Message.Role == "assistant" {
				s.stopReason = f.Message.StopReason
				s.runError = nil
				if s.stopReason == "error" {
					message := f.Message.ErrorMessage
					if message == "" {
						message = "Pi reported an assistant error"
					}
					s.runError = &agent.ObservedError{Message: message}
					payloads = append(payloads, agent.SessionErrorPayload{Message: message, Reason: "assistant_error"})
				}
			}
		case "extension_error":
			if !s.core.config.EnableInject {
				break
			}
			var failure struct {
				Path  string `json:"extensionPath"`
				Event string `json:"event"`
			}
			if json.Unmarshal(native.Raw, &failure) != nil {
				break
			}
			switch {
			case failure.Path == "command:"+sessionInjectCommand:
				payloads = append(payloads, agent.SessionErrorPayload{
					Reason: "pi_inject_extension_failed", Message: "Pi reported an error in the injection command",
				}, fallback)
			case failure.Event == "send_message":
				// Runtime errors omit the command and cannot be tied to an Inject receipt.
				payloads = append(payloads, agent.SessionErrorPayload{
					Reason: "pi_extension_send_message_failed", Message: "Pi failed to append an extension message",
				}, fallback)
			}
		case "tool_execution_start", "tool_execution_end":
			if native.Type == "tool_execution_end" {
				payloads = append(payloads, s.toolInteractionClosure(f.ToolCallID, f.ToolName, f.Result, f.IsError, f.DurationMs)...)
			}
			kind, known := toolKind(f.ToolName)
			provider := agent.ToolProviderAgent
			if isMCPTool(f.ToolName) {
				kind, known = agent.ToolKindOther, true
				provider = agent.ToolProviderMCP
			}
			if !known || f.ToolCallID == "" {
				break
			}
			item = f.ToolCallID
			if native.Type == "tool_execution_start" && f.Args != nil {
				payloads = append(payloads, agent.ToolCallPayload{ID: item, Kind: kind, ToolName: f.ToolName, Provider: provider, RawInput: f.Args, Status: "in_progress"})
			} else if native.Type == "tool_execution_end" && f.Result != nil {
				var raw any = f.Result
				status := "completed"
				if f.IsError {
					status = "failed"
				}
				payloads = append(payloads, agent.ToolResultPayload{ID: item, Kind: kind, ToolName: f.ToolName, Provider: provider, RawOutput: &raw, DurationMs: f.DurationMs, Status: status})
			}
		}
	}
	if len(payloads) == 0 {
		payloads = append(payloads, fallback)
	}
	events := make([]agent.Event, 0, len(payloads))
	for _, payload := range payloads {
		payloadItem := item
		switch message := payload.(type) {
		case agent.AgentMessage:
			payloadItem = message.MessageID
		case agent.AgentThinking:
			payloadItem = message.MessageID
		}
		piMeta := map[string]any{"nativeType": native.Type}
		if nativeRequestID != "" {
			piMeta["nativeRequestId"] = nativeRequestID
		}
		meta := map[string]any{"pi": piMeta}
		event, err := envelope.Event(payloadItem, payload, occurred, meta)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

// Pi uses these names to classify MCP tools when filtering its tool set.
func isMCPTool(name string) bool {
	return strings.HasPrefix(name, "mcp__") || name == "list_mcp_resources" ||
		name == "list_mcp_resource_templates" || name == "read_mcp_resource"
}

func (s *session) changeState(running, aborted bool) (agent.StateUpdatePayload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var prev *agent.SessionState
	if s.state != "" {
		previous := s.state
		prev = &previous
	}
	next := agent.StateIdle
	var reason string
	var observedError *agent.ObservedError
	if running {
		next = agent.StateRunning
		s.stopReason = ""
		s.runError = nil
		if s.active == "" {
			s.active = s.pending
			if s.active == "" {
				id, err := uuidv4.New()
				if err != nil {
					return agent.StateUpdatePayload{}, err
				}
				s.active = id
			}
		}
	} else {
		s.settled++
		s.active = ""
		reason, observedError = s.stopReason, s.runError
		if aborted {
			reason = "aborted"
		}
		s.stopReason = ""
		s.runError = nil
	}
	s.submitUnknown = false
	s.state = next
	return agent.StateUpdatePayload{State: &next, PreviousState: prev, StopReason: reason, Error: observedError}, nil
}

func textContent(text string) []agent.ContentBlock {
	return []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: text}}}
}

func blockID(id string, index int) string { return id + ":" + strconv.Itoa(index) }

func messagePayloads(role, id string, raw json.RawMessage) ([]agent.Payload, bool) {
	if role != "assistant" && role != "user" {
		return nil, false
	}
	var scalar string
	if len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &scalar) == nil && role == "user" {
		return []agent.Payload{agent.UserMessage{MessageID: id, Content: textContent(scalar)}}, true
	}
	var blocks []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
		Data     string `json:"data"`
		MIMEType string `json:"mimeType"`
	}
	if json.Unmarshal(raw, &blocks) != nil || len(blocks) == 0 {
		return nil, false
	}
	complete := true
	var result []agent.Payload
	var userContent []agent.ContentBlock
	for i, b := range blocks {
		switch {
		case role == "assistant" && b.Type == "thinking":
			result = append(result, agent.AgentThinking{MessageID: blockID(id, i), Content: textContent(b.Thinking)})
		case role == "assistant" && b.Type == "text":
			result = append(result, agent.AgentMessage{MessageID: blockID(id, i), Content: textContent(b.Text)})
		case role == "user" && b.Type == "text":
			userContent = append(userContent, textContent(b.Text)...)
		case role == "user" && b.Type == "image":
			block := agent.ContentBlock{Image: &agent.ContentBlockImage{MIMEType: b.MIMEType, Data: b.Data}}
			if block.Validate() != nil {
				complete = false
				continue
			}
			userContent = append(userContent, block)
		default:
			complete = false
		}
	}
	if len(userContent) != 0 {
		result = append(result, agent.UserMessage{MessageID: id, Content: userContent})
	}
	return result, complete
}

// Pi's built-in tool names have known actions. Extension and MCP tools
// require positive provider evidence for unified tool identity.
func toolKind(name string) (agent.ToolKind, bool) {
	switch name {
	case "read", "ls":
		return agent.ToolKindRead, true
	case "bash", "powershell":
		return agent.ToolKindExecute, true
	case "write", "edit":
		return agent.ToolKindEdit, true
	case "find", "grep":
		return agent.ToolKindSearch, true
	case sdkQuestionTool:
		return agent.ToolKindOther, true
	default:
		return "", false
	}
}
