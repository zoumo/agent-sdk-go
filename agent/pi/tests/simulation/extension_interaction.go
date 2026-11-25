package pisim

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/zoumo/agent-sdk-go/agent"
)

const (
	extensionToolPrompt = "execute controlled tool flow"
	approvalDenied      = "Agent SDK tool approval denied"
	approvedCommand     = "approved controlled command"
	markerPath          = "sdk-test-marker.txt"
)

type extensionInteraction struct {
	tool, detail, content, question, answer, result string
	choices                                         []string
	denied                                          bool
}

var extensionInteractions = map[string]extensionInteraction{
	"008": {tool: "write", detail: markerPath, content: "reject this controlled marker\n", result: approvalDenied, denied: true},
	"009": {tool: "write", detail: markerPath, content: "approved controlled marker\n", result: "Successfully wrote to " + markerPath},
	"010": {tool: "bash", detail: "printf '" + approvedCommand + "'", result: approvedCommand},
	"011": {tool: "bash", detail: "printf 'rejected controlled command'", result: approvalDenied, denied: true},
	"012": {tool: "bash", detail: "printf '" + approvedCommand + "'", result: approvedCommand},
	"013": {tool: "agent_sdk_ask", question: "Choose a color", choices: []string{"Red", "Green"}, answer: "Green", result: "Green"},
	"014": {tool: "agent_sdk_ask", question: "Enter a short phrase", answer: "controlled response", result: "controlled response"},
}

func (f extensionFlow) runInteraction(code string, expected extensionInteraction) error {
	if err := f.prompt(extensionToolPrompt); err != nil {
		return err
	}
	request, toolID, err := f.waitForInteraction(expected)
	if err != nil {
		return err
	}
	if code == "012" {
		if err := f.controlDuringApproval(); err != nil {
			return err
		}
	}
	answer := expected.response()
	if err := f.answer(request, answer); err != nil {
		return err
	}
	return f.finishInteraction(request, toolID, expected)
}

func (f extensionFlow) waitForInteraction(expected extensionInteraction) (agent.InteractionRef, string, error) {
	var callID string
	var running, requiresAction bool
	for {
		event, err := f.next()
		if err != nil {
			return "", "", err
		}
		switch payload := event.Payload.(type) {
		case agent.StateUpdatePayload:
			switch nativeEventType(event) {
			case "agent_start":
				running = inState(payload, agent.StateRunning)
			case "extension_ui_request":
				requiresAction = inState(payload, agent.StateRequiresAction)
			case "agent_settled":
				return "", "", errors.New("pi: settled before requesting an interaction")
			}
		case agent.ToolCallPayload:
			if callID != "" || payload.ID != event.ItemID || !expected.matchesTool(payload) {
				return "", "", fmt.Errorf("unexpected Pi tool call %q", payload.ToolName)
			}
			callID = payload.ID
		case agent.InteractionRequestedPayload:
			metadata, _ := event.Meta["pi"].(map[string]any)
			nativeID, _ := metadata["nativeRequestId"].(string)
			if !running || !requiresAction || callID == "" || event.ItemID != callID || nativeID == "" ||
				!payload.Blocking || payload.Interaction == "" || !expected.matchesRequest(payload.Request, callID) {
				return "", "", errors.New("pi: interaction did not match the live tool call")
			}
			return payload.Interaction, callID, nil
		case agent.ToolResultPayload:
			return "", "", errors.New("pi: tool finished before requesting an interaction")
		}
	}
}

func (expected extensionInteraction) matchesTool(call agent.ToolCallPayload) bool {
	if call.ToolName != expected.tool || call.Provider != agent.ToolProviderAgent || call.Status != "in_progress" {
		return false
	}
	raw, ok := call.RawInput.(json.RawMessage)
	if !ok {
		return false
	}
	var args struct {
		Path, Command, Content, Question string
		Choices                          []string
	}
	if json.Unmarshal(raw, &args) != nil {
		return false
	}
	switch expected.tool {
	case "write":
		return args.Path == expected.detail && args.Content == expected.content
	case "bash":
		return args.Command == expected.detail
	default:
		return args.Question == expected.question && slices.Equal(args.Choices, expected.choices)
	}
}

func (expected extensionInteraction) matchesRequest(request agent.InteractionRequest, callID string) bool {
	if expected.question == "" {
		options := []agent.PermissionOption{
			{OptionID: "allow", Name: "Allow once", Kind: agent.PermissionAllow, Scope: agent.PermissionScopeOnce},
			{OptionID: "deny", Name: "Deny", Kind: agent.PermissionReject, Scope: agent.PermissionScopeOnce},
		}
		return request.Kind == agent.InteractionPermission && request.ToolCallID == callID &&
			request.Title == "Approve Pi "+expected.tool && extensionText(request.Content) == expected.detail && reflect.DeepEqual(request.Options, options)
	}
	value := map[string]any{"type": "string"}
	if len(expected.choices) != 0 {
		value["enum"] = expected.choices
	}
	schema := map[string]any{
		"type": "object", "properties": map[string]any{"value": value},
		"required": []string{"value"}, "additionalProperties": false,
	}
	return request.Kind == agent.InteractionForm && request.Message == expected.question && reflect.DeepEqual(request.RequestedSchema, schema)
}

func (expected extensionInteraction) response() agent.Answer {
	if expected.question != "" {
		return agent.FormAnswer{Outcome: agent.FormAccept, Content: map[string]any{"value": expected.answer}}
	}
	option := "allow"
	if expected.denied {
		option = "deny"
	}
	return agent.PermissionAnswer{Outcome: agent.PermissionSelected, OptionID: option}
}

func (f extensionFlow) controlDuringApproval() error {
	controller, ok := f.session.(agent.ConfigController)
	if !ok {
		return errors.New("unified Pi session has no ConfigController")
	}
	if err := f.changeConfig(controller, agent.ConfigChange{ID: agent.ConfigEffort, Value: "low"}); err != nil {
		return err
	}
	for {
		event, err := f.next()
		if err != nil {
			return err
		}
		if nativeEventType(event) != "thinking_level_changed" {
			if event.Type() == agent.EventInteractionClosed || event.Type() == agent.EventAgentToolResult || nativeEventType(event) == "agent_settled" {
				return errors.New("pi: approval finished before the independent native event")
			}
			continue
		}
		native, ok := event.Payload.(agent.UnsupportedNativeEventPayload)
		var thinking struct{ Type, Level string }
		if !ok || native.AgentType != "pi" || json.Unmarshal(native.Raw, &thinking) != nil ||
			thinking.Type != "thinking_level_changed" || thinking.Level != "low" {
			return errors.New("pi: native thinking-level event is missing or invalid")
		}
		return f.changeConfig(controller, agent.ConfigChange{ID: agent.ConfigModel, Value: "alternate"})
	}
}

func (f extensionFlow) changeConfig(controller agent.ConfigController, change agent.ConfigChange) error {
	receipt, err := controller.SetConfig(f.ctx, change)
	if err != nil {
		return fmt.Errorf("set Pi %s: %w", change.ID, err)
	}
	if receipt.Change != change || !nativeAccepted(receipt.Acceptance) {
		return fmt.Errorf("pi: %s control receipt did not confirm the request", change.ID)
	}
	return f.output.Encode(map[string]any{"type": "config.receipt", "receipt": receipt})
}

func (f extensionFlow) answer(ref agent.InteractionRef, answer agent.Answer) error {
	wrong := ref + "-unexpected"
	invalid, err := f.session.Answer(f.ctx, wrong, answer)
	var rejected *agent.Error
	if !errors.As(err, &rejected) || rejected.Reason != agent.ErrorReasonInvalidInteractionRef ||
		(invalid.Acceptance.Sent != nil && *invalid.Acceptance.Sent) {
		return errors.New("pi: accepted an answer for an unrelated interaction")
	}
	receipt, err := f.session.Answer(f.ctx, ref, answer)
	if err != nil {
		return fmt.Errorf("answer Pi interaction: %w", err)
	}
	if receipt.Interaction != ref || receipt.Acceptance.Sent == nil || !*receipt.Acceptance.Sent || receipt.Acceptance.NativeAccepted != nil {
		return errors.New("pi: answer receipt did not confirm the original interaction write")
	}
	return f.output.Encode(map[string]any{"type": "interaction.answer_receipt", "wrongReferenceRejected": true, "receipt": receipt})
}

func (f extensionFlow) finishInteraction(ref agent.InteractionRef, toolID string, expected extensionInteraction) error {
	var closed, toolResult bool
	var reply strings.Builder
	for {
		event, err := f.next()
		if err != nil {
			return err
		}
		switch payload := event.Payload.(type) {
		case agent.InteractionClosedPayload:
			if closed || payload.Interaction != ref || payload.Outcome != agent.InteractionAnswered {
				return errors.New("pi: settled a different interaction or did not accept the answer")
			}
			closed = true
		case agent.ToolResultPayload:
			if toolResult || payload.ID != toolID || event.ItemID != toolID || !expected.matchesResult(payload) {
				return fmt.Errorf("unexpected Pi tool result for %q", payload.ToolName)
			}
			toolResult = true
		case agent.AgentMessage:
			if toolResult {
				reply.WriteString(extensionText(payload.Content))
			}
		case agent.StateUpdatePayload:
			if nativeEventType(event) != "agent_settled" {
				continue
			}
			if !closed || !toolResult || reply.String() != "Tool handled" || !inState(payload, agent.StateIdle) || payload.StopReason != "stop" || payload.Error != nil {
				return errors.New("pi: did not finish the answered tool flow successfully")
			}
			return nil
		case agent.InteractionRequestedPayload:
			return errors.New("pi: requested another interaction while finishing the tool flow")
		}
	}
}

func (expected extensionInteraction) matchesResult(tool agent.ToolResultPayload) bool {
	status := "completed"
	if expected.denied {
		status = "failed"
	}
	if tool.ToolName != expected.tool || tool.Status != status || tool.RawOutput == nil {
		return false
	}
	raw, ok := (*tool.RawOutput).(json.RawMessage)
	var result struct {
		Content    []struct{ Type, Text string }
		Structured *struct {
			Output   string
			ExitCode *int `json:"exit_code"`
		} `json:"structuredContent"`
	}
	if !ok || json.Unmarshal(raw, &result) != nil || len(result.Content) != 1 ||
		result.Content[0].Type != "text" || result.Content[0].Text != expected.result {
		return false
	}
	if expected.tool == "bash" && !expected.denied {
		return result.Structured != nil && result.Structured.ExitCode != nil && *result.Structured.ExitCode == 0 && result.Structured.Output == expected.result
	}
	return expected.denied == (tool.DurationMs == nil)
}

func inState(payload agent.StateUpdatePayload, state agent.SessionState) bool {
	return payload.State != nil && *payload.State == state
}
