package pisim

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/zoumo/agent-sdk-go/client/pi"
)

type baseTurn struct {
	Reply, StopReason, Model, Provider string
	Aborted, Progress, SteerConsumed   bool
	Disposition                        string
}

type baseEvent struct {
	AssistantMessageEvent struct{ Type string }
	Message               struct {
		Role, StopReason, Model, Provider string
		Content                           json.RawMessage
	}
	Aborted    bool
	IsError    *bool
	ToolCallID string
	ToolName   string
	Args       struct{ Path, Content string }
}

func (f *baseFlow) runTurn(prompt string) (baseTurn, error) {
	var turn baseTurn
	var images []pi.Image
	if f.code == "002" {
		images = []pi.Image{{Type: "image", Data: bluePNG, MIMEType: "image/png"}}
	}
	result, err := f.session.Prompt(f.ctx, prompt, images, "")
	if err != nil {
		return turn, err
	}
	if result.Disposition != "started" {
		return turn, fmt.Errorf("pi: expected a started prompt, got %q", result.Disposition)
	}
	var started []string
	completed := map[string]bool{}
	for {
		event, err := f.session.Receive(f.ctx)
		if err != nil {
			return turn, err
		}
		var wire baseEvent
		if err := json.Unmarshal(event.Raw, &wire); err != nil {
			return turn, err
		}
		switch event.Type {
		case "message_update":
			if !turn.Progress && wire.AssistantMessageEvent.Type == "text_delta" {
				turn.Progress = true
				if err := f.actOnProgress(&turn); err != nil {
					return turn, err
				}
			}
		case "message_end":
			if wire.Message.Role == "assistant" {
				turn.Reply = messageText(wire.Message.Content)
				turn.StopReason, turn.Model, turn.Provider = wire.Message.StopReason, wire.Message.Model, wire.Message.Provider
			}
			if f.code == "005" && wire.Message.Role == "user" && messageText(wire.Message.Content) == f.plan.Prompts[1] {
				turn.SteerConsumed = true
			}
		case "tool_execution_start":
			if f.code == "006" {
				expected := map[string]string{"read": "numbers.txt", "write": "summary.txt"}
				if path, ok := expected[wire.ToolName]; !ok || wire.Args.Path != path ||
					(wire.ToolName == "write" && wire.Args.Content != "total=18\n") || wire.ToolCallID == "" {
					return turn, errors.New("pi: unexpected file tool execution arguments")
				}
				started = append(started, wire.ToolName)
				if _, exists := completed[wire.ToolCallID]; exists {
					return turn, errors.New("pi: duplicate file tool call")
				}
				completed[wire.ToolCallID] = false
			}
		case "tool_execution_end":
			if _, exists := completed[wire.ToolCallID]; exists && wire.IsError != nil && !*wire.IsError {
				completed[wire.ToolCallID] = true
			}
		case "agent_settled":
			turn.Aborted = wire.Aborted
			return turn, f.checkTurn(turn, started, completed)
		}
	}
}

func messageText(content json.RawMessage) string {
	var plain string
	if json.Unmarshal(content, &plain) == nil {
		return plain
	}
	var blocks []struct{ Type, Text string }
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	var text strings.Builder
	for _, block := range blocks {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}

func (f *baseFlow) actOnProgress(turn *baseTurn) error {
	switch f.code {
	case "005":
		got, err := f.session.Steer(f.ctx, f.plan.Prompts[1], nil)
		var rejected *pi.CommandError
		if errors.As(err, &rejected) {
			turn.Disposition = "rejected"
			return nil
		}
		turn.Disposition = got.Disposition
		return err
	case "024":
		return f.changeModel()
	case "030":
		return f.session.Abort(f.ctx)
	default:
		return nil
	}
}

func (f *baseFlow) checkTurn(turn baseTurn, started []string, completed map[string]bool) error {
	if f.code == "030" {
		if !turn.Progress || !turn.Aborted || turn.StopReason != "aborted" {
			return errors.New("pi: native progress did not lead to an aborted execution")
		}
		return nil
	}
	if turn.Aborted || turn.StopReason != "stop" || strings.TrimSpace(turn.Reply) == "" {
		return errors.New("pi: execution did not settle with assistant text")
	}
	switch f.code {
	case "002":
		if !strings.EqualFold(strings.TrimSpace(turn.Reply), "blue") {
			return errors.New("pi: assistant did not identify the picture")
		}
	case "005":
		if !turn.Progress || (turn.Disposition != "rejected" && (turn.Disposition != "queued" || !turn.SteerConsumed)) {
			return errors.New("pi: no active steering outcome followed by terminal result")
		}
	case "006":
		if !slices.Equal(started, []string{"read", "write"}) || len(completed) != 2 {
			return errors.New("pi: expected read and write tool execution")
		}
		for _, done := range completed {
			if !done {
				return errors.New("pi: expected a successful tool result")
			}
		}
	case "024":
		if !turn.Progress {
			return errors.New("pi: model change lacked native execution progress")
		}
	}
	return nil
}
