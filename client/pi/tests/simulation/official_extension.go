package pisim

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/zoumo/agent-sdk-go/client/pi"
)

func (f *baseFlow) runOfficialExtension() error {
	commands, err := f.session.Commands(f.ctx)
	if err != nil {
		return err
	}
	hasExtension := func(name string) bool {
		return slices.ContainsFunc(commands, func(command pi.Command) bool {
			return command.Name == name && command.Source == "extension"
		})
	}
	if !hasExtension("agent-sdk-session-ready") {
		return errors.New("pi: recorded session extension is missing")
	}
	if f.code == "013" || f.code == "014" {
		return f.answerOfficialQuestion()
	}
	if !hasExtension("agent-sdk-inject") {
		return errors.New("pi: recorded injection command is missing")
	}
	return f.injectOfficialContext()
}

func (f *baseFlow) answerOfficialQuestion() error {
	expected := f.plan.Scenario.Answers
	if expected.Question == "" || expected.Choice == "" {
		return errors.New("pi: question plan is missing the question or answer")
	}
	method := "select"
	if f.plan.Scenario.Parameters.FreeText {
		method = "input"
	} else if !slices.Contains(expected.Options, expected.Choice) {
		return errors.New("pi: selected answer is absent from the plan choices")
	}
	if err := f.sendOfficialPrompt(f.plan.Prompts[0], "started"); err != nil {
		return err
	}
	var toolID, requestID, reply string
	answered := false
	for {
		event, err := f.session.Receive(f.ctx)
		if err != nil {
			return err
		}
		var wire struct {
			baseEvent
			Args struct {
				Question string
				Choices  []string
			}
			Result struct {
				Content json.RawMessage
				Details struct{ SDKInteractionExpired *bool }
			}
		}
		if err := json.Unmarshal(event.Raw, &wire); err != nil {
			return err
		}
		switch event.Type {
		case "tool_execution_start":
			if toolID != "" || wire.ToolCallID == "" || wire.ToolName != "agent_sdk_ask" ||
				wire.Args.Question != expected.Question || !slices.Equal(wire.Args.Choices, expected.Options) {
				return errors.New("pi: unexpected question tool call")
			}
			toolID = wire.ToolCallID
		case "extension_ui_request":
			request, err := event.UIRequest()
			if err != nil {
				return err
			}
			title, ok := strings.CutPrefix(request.Title, "Agent SDK question: ")
			var detail struct{ ToolCallID, Question string }
			if !ok || json.Unmarshal([]byte(title), &detail) != nil || requestID != "" ||
				toolID == "" || detail.ToolCallID != toolID || detail.Question != expected.Question ||
				request.Method != method || !slices.Equal(request.Options, expected.Options) {
				return errors.New("pi: question request differs from the original tool call")
			}
			if err := f.session.RespondUI(f.ctx, pi.UIResponse{ID: request.ID, Value: &expected.Choice}); err != nil {
				return err
			}
			requestID = request.ID
		case "tool_execution_end":
			if answered || requestID == "" || wire.ToolCallID != toolID || wire.ToolName != "agent_sdk_ask" ||
				wire.IsError == nil || *wire.IsError || wire.Result.Details.SDKInteractionExpired == nil ||
				*wire.Result.Details.SDKInteractionExpired || messageText(wire.Result.Content) != expected.Choice {
				return errors.New("pi: question tool did not receive the chosen answer")
			}
			answered = true
		case "message_end":
			if wire.Message.Role == "assistant" && wire.Message.StopReason == "stop" {
				reply = messageText(wire.Message.Content)
			}
		case "agent_settled":
			if !answered || wire.Aborted || strings.TrimSpace(reply) == "" {
				return errors.New("pi: question turn did not settle after accepting the answer")
			}
			return f.output.Encode(map[string]string{"questionId": requestID, "toolCallId": toolID, "method": method, "answer": expected.Choice})
		}
	}
}

func (f *baseFlow) injectOfficialContext() error {
	fact := f.plan.Scenario.Parameters.ContextValue
	if fact == "" {
		return errors.New("pi: injection plan is missing the context value")
	}
	active := f.code == "038"
	injected := fact
	if !active {
		injected = "The stored vault code is " + fact + "."
	}
	content, err := json.Marshal([]struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: injected}})
	if err != nil {
		return err
	}
	inject := func() error {
		return f.sendOfficialPrompt("/agent-sdk-inject "+string(content), "handled")
	}
	if err := f.sendOfficialPrompt(f.plan.Prompts[0], "started"); err != nil {
		return err
	}
	var turns, starts, appends int
	var turnEnded, sent bool
	var reply string
	for {
		event, err := f.session.Receive(f.ctx)
		if err != nil {
			return err
		}
		var wire baseEvent
		if err := json.Unmarshal(event.Raw, &wire); err != nil {
			return err
		}
		switch event.Type {
		case "agent_start":
			starts++
			if starts != turns+1 || (turns == 1 && appends != 1) {
				return errors.New("pi: injection started unexpected work")
			}
		case "message_update":
			if active && turns == 0 && !sent && wire.AssistantMessageEvent.Type == "text_delta" {
				if err := inject(); err != nil {
					return err
				}
				sent = true
			}
		case "turn_end":
			turnEnded = true
		case "message_end":
			text := messageText(wire.Message.Content)
			if wire.Message.Role == "assistant" && wire.Message.StopReason == "stop" {
				reply = text
			}
			if wire.Message.Role == "custom" && text == injected {
				if !sent || appends != 0 || (active && (!turnEnded || turns != 0)) || (!active && turns != 1) {
					return errors.New("pi: injected context appeared outside its expected turn boundary")
				}
				appends++
				if !active {
					if err := f.sendOfficialPrompt(f.plan.Prompts[1], "started"); err != nil {
						return err
					}
				}
			}
		case "agent_settled":
			if wire.Aborted || strings.TrimSpace(reply) == "" || starts != turns+1 {
				return errors.New("pi: injection turn did not settle with an assistant reply")
			}
			turns++
			if turns == 2 {
				if appends != 1 || strings.TrimSpace(reply) != fact {
					return errors.New("pi: the next turn did not consume the injected context")
				}
				return f.checkOfficialInjectionState(active)
			}
			reply = ""
			if active {
				if appends != 1 {
					return errors.New("pi: active injection was not appended before settlement")
				}
				if err := f.sendOfficialPrompt(f.plan.Prompts[1], "started"); err != nil {
					return err
				}
			} else {
				if err := inject(); err != nil {
					return err
				}
				sent = true
				if _, err := f.checkIdleState(); err != nil {
					return err
				}
			}
		}
	}
}

func (f *baseFlow) sendOfficialPrompt(prompt, disposition string) error {
	got, err := f.session.Prompt(f.ctx, prompt, nil, "")
	if err != nil {
		return err
	}
	if got.Disposition != disposition {
		return fmt.Errorf("pi: expected prompt disposition %q, got %q", disposition, got.Disposition)
	}
	return nil
}

func (f *baseFlow) checkIdleState() (pi.State, error) {
	state, err := f.session.State(f.ctx)
	if err == nil && (state.SessionID != f.session.ID() || state.IsStreaming) {
		err = errors.New("pi: session identity changed or remained active")
	}
	return state, err
}

func (f *baseFlow) checkOfficialInjectionState(active bool) error {
	state, err := f.checkIdleState()
	if err != nil {
		return err
	}
	return f.output.Encode(map[string]any{"sessionId": state.SessionID, "activeInjection": active, "contextConsumed": true})
}
