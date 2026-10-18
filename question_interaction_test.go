// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/zoumo/claude-agent-sdk-go/types"
)

func TestQuestionContractThroughOriginalPermissionHandle(t *testing.T) {
	requests := make(chan *PermissionRequest, 1)
	c := openFixture(t, "permission_controls", func(opts *Options) {
		opts.PermissionHandler = func(p *PermissionRequest) { requests <- p }
	})
	input := json.RawMessage(`{"questions":[{"question":"Which output format should I use?","header":"Format","options":[{"label":"Summary","description":"Short","future":{"x":true}},{"label":"Detailed","description":"Long"}],"multiSelect":false,"future":[null]},{"question":"Which sections should I include?","header":"Sections","options":[{"label":"Intro"},{"label":"End"}],"multiSelect":true}],"unknown":{"nested":[false,1]}}`)
	// An assistant tool_use can be displayed/decoded, but cannot grant authority.
	emitPermissionFrame(t, c, map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "tool/question", "name": "AskUserQuestion", "input": input}}}})
	assistant := receive(t, c)
	var display types.AskUserQuestionInput
	if err := json.Unmarshal(assistant.Assistant.Content.Blocks[0].ToolUse.Input, &display); err != nil {
		t.Fatal(err)
	}
	if got := permissionResponses(t, permissionSnapshot(t, c)); len(got) != 0 {
		t.Fatal("assistant event answered", got)
	}
	select {
	case <-requests:
		t.Fatal("assistant event minted handle")
	default:
	}
	emitPermissionFrame(t, c, map[string]any{"type": "control_request", "request_id": "request/original", "future": true, "request": map[string]any{
		"subtype": "can_use_tool", "tool_name": "AskUserQuestion", "tool_use_id": "tool/question", "agent_id": "agent", "title": "Choose", "permission_suggestions": nil, "input": input, "future": map[string]any{"nested": false},
	}})
	event := receive(t, c)
	p := awaitPermission(t, requests)
	metadata, err := p.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	observed, err := event.Control.PermissionMetadata()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(metadata, observed) || metadata.AgentID != "agent" || metadata.ToolName != "AskUserQuestion" {
		t.Fatal("metadata differs", metadata)
	}
	metadata.ToolUseID = "tampered"
	metadata.Input[0] = '!'
	observed.Raw[0] = '!'
	fresh, err := p.Metadata()
	if err != nil || fresh.ToolUseID != "tool/question" || fresh.Input[0] != '{' || p.RequestID() != "request/original" {
		t.Fatal("metadata changed authority", err)
	}
	var questions AskUserQuestionInput
	if err := json.Unmarshal(p.Input(), &questions); err != nil {
		t.Fatal(err)
	}
	custom := "自由文本 / custom format"
	updated, err := questions.WithAnswers(map[string]QuestionAnswer{
		questions.Questions[0].Question: {Text: &custom},
		questions.Questions[1].Question: {Labels: []string{"Intro", "End"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Answer(context.Background(), types.PermissionAnswer{Behavior: types.PermissionAllow, UpdatedInput: updated}); err != nil {
		t.Fatal(err)
	}
	echo := receive(t, c)
	var envelope struct {
		Wire types.ControlResponse `json:"wire"`
	}
	if err := json.Unmarshal(echo.Raw, &envelope); err != nil {
		t.Fatal(err)
	}
	wire := envelope.Wire
	if wire.Type != "control_response" || wire.Response.RequestID != "request/original" || wire.Response.Subtype != "success" {
		t.Fatal("wrong response envelope", wire)
	}
	var decision types.PermissionResult
	if err := json.Unmarshal(wire.Response.Response, &decision); err != nil {
		t.Fatal(err)
	}
	var answered types.AskUserQuestionInput
	if err := json.Unmarshal(decision.UpdatedInput, &answered); err != nil {
		t.Fatal(err)
	}
	if decision.Behavior != types.PermissionAllow || *answered.Answers[questions.Questions[0].Question].Text != custom || !reflect.DeepEqual(answered.Questions, questions.Questions) {
		t.Fatal("questions or answers changed")
	}
	var got, want any
	if err := json.Unmarshal(decision.UpdatedInput, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(updated, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("unknown input lost", got, want)
	}
	copyHandle := *p
	if err := copyHandle.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny}); !errors.Is(err, ErrPermissionUsed) {
		t.Fatal(err)
	}
	if got := permissionResponses(t, permissionSnapshot(t, c)); len(got) != 1 {
		t.Fatal("response count", got)
	}
}

func TestQuestionPreparedAnswerCannotBypassCancellation(t *testing.T) {
	requests := make(chan *PermissionRequest, 1)
	c := openFixture(t, "permission_controls", func(opts *Options) {
		opts.PermissionHandler = func(p *PermissionRequest) { requests <- p }
	})
	emitPermission(t, c, "cancel/question", "AskUserQuestion", json.RawMessage(`{"questions":[{"question":"Proceed?","options":[]}]}`))
	p := awaitPermission(t, requests)
	metadata, err := p.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	var input types.AskUserQuestionInput
	if err := json.Unmarshal(metadata.Input, &input); err != nil {
		t.Fatal(err)
	}
	text := "No custom action"
	updated, err := input.WithAnswers(map[string]types.QuestionAnswer{"Proceed?": {Text: &text}})
	if err != nil {
		t.Fatal(err)
	}
	emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": "cancel/question"})
	if event := receive(t, c); event.Cancel == nil {
		t.Fatal("missing cancellation")
	}
	if err := p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow, UpdatedInput: updated}); !errors.Is(err, ErrPermissionCancelled) {
		t.Fatal(err)
	}
	if got := permissionResponses(t, permissionSnapshot(t, c)); len(got) != 0 {
		t.Fatal("late answer written", got)
	}
	emitPermission(t, c, "deny/question", "AskUserQuestion", json.RawMessage(`{"questions":[]}`))
	deny := awaitPermission(t, requests)
	if err := deny.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny, Message: "User declined"}); err != nil {
		t.Fatal(err)
	}
	assertPermissionReply(t, c, "deny/question", map[string]any{"behavior": "deny", "message": "User declined"})
	for _, zero := range []*PermissionRequest{nil, new(PermissionRequest)} {
		if _, err := zero.Metadata(); !errors.Is(err, ErrInvalidPermissionRequest) {
			t.Fatal(err)
		}
	}
}
