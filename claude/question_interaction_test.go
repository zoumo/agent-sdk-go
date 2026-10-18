// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"encoding/json"

	"github.com/zoumo/agent-sdk-go/claude/types"

	"github.com/stretchr/testify/require"
)

func (s *controlInteractionSuite) TestQuestionContractThroughOriginalPermissionHandle() {
	t := s.T()
	requests := make(chan *PermissionRequest, 1)
	c := s.openFixture(t, "permission_controls", func(opts *Options) {
		opts.PermissionHandler = func(p *PermissionRequest) { requests <- p }
	})
	input := json.RawMessage(`{"questions":[{"question":"Which output format should I use?","header":"Format","options":[{"label":"Summary","description":"Short","future":{"x":true}},{"label":"Detailed","description":"Long"}],"multiSelect":false,"future":[null]},{"question":"Which sections should I include?","header":"Sections","options":[{"label":"Intro"},{"label":"End"}],"multiSelect":true}],"unknown":{"nested":[false,1]}}`)
	// An assistant tool_use can be displayed/decoded, but cannot grant authority.
	emitPermissionFrame(t, c, map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "tool/question", "name": "AskUserQuestion", "input": input}}}})
	assistant := receive(t, c)
	var display types.AskUserQuestionInput
	{
		err := json.Unmarshal(assistant.Assistant.Content.Blocks[0].ToolUse.Input, &display)
		require.NoError(t, err)
	}
	{
		got := permissionResponses(t, permissionSnapshot(t, c))
		require.EqualValues(t, 0, len(got), "assistant event answered", got)
	}
	select {
	case <-requests:
		require.FailNow(t, "assistant event minted handle")
	default:
	}
	emitPermissionFrame(t, c, map[string]any{"type": "control_request", "request_id": "request/original", "future": true, "request": map[string]any{
		"subtype": "can_use_tool", "tool_name": "AskUserQuestion", "tool_use_id": "tool/question", "agent_id": "agent", "title": "Choose", "permission_suggestions": nil, "input": input, "future": map[string]any{"nested": false},
	}})
	event := receive(t, c)
	p := awaitPermission(t, requests)
	metadata, err := p.Metadata()
	require.NoError(t, err)
	observed, err := event.Control.PermissionMetadata()
	require.NoError(t, err)
	require.Equal(t, metadata, observed, "metadata differs", metadata)
	require.EqualValues(t, "agent", metadata.AgentID, "metadata differs", metadata)
	require.EqualValues(t, "AskUserQuestion", metadata.ToolName, "metadata differs", metadata)
	metadata.ToolUseID = "tampered"
	metadata.Input[0] = '!'
	observed.Raw[0] = '!'
	fresh, err := p.Metadata()
	require.NoError(t, err, "metadata changed authority", err)
	require.EqualValues(t, "tool/question", fresh.ToolUseID, "metadata changed authority", err)
	require.EqualValues(t, '{', fresh.Input[0], "metadata changed authority", err)
	require.EqualValues(t, "request/original", p.RequestID(), "metadata changed authority", err)
	var questions AskUserQuestionInput
	{
		err := json.Unmarshal(p.Input(), &questions)
		require.NoError(t, err)
	}
	custom := "自由文本 / custom format"
	updated, err := questions.WithAnswers(map[string]QuestionAnswer{
		questions.Questions[0].Question: {Text: &custom},
		questions.Questions[1].Question: {Labels: []string{"Intro", "End"}},
	})
	require.NoError(t, err)
	{
		err := p.Answer(context.Background(), types.PermissionAnswer{Behavior: types.PermissionAllow, UpdatedInput: updated})
		require.NoError(t, err)
	}
	echo := receive(t, c)
	var envelope struct {
		Wire types.ControlResponse `json:"wire"`
	}
	{
		err := json.Unmarshal(echo.Raw, &envelope)
		require.NoError(t, err)
	}
	wire := envelope.Wire
	require.EqualValues(t, "control_response", wire.Type, "wrong response envelope", wire)
	require.EqualValues(t, "request/original", wire.Response.RequestID, "wrong response envelope", wire)
	require.EqualValues(t, "success", wire.Response.Subtype, "wrong response envelope", wire)
	var decision types.PermissionResult
	{
		err := json.Unmarshal(wire.Response.Response, &decision)
		require.NoError(t, err)
	}
	var answered types.AskUserQuestionInput
	{
		err := json.Unmarshal(decision.UpdatedInput, &answered)
		require.NoError(t, err)
	}
	require.EqualValues(t, types.PermissionAllow, decision.Behavior, "questions or answers changed")
	require.EqualValues(t, custom, *answered.Answers[questions.Questions[0].Question].Text, "questions or answers changed")
	require.Equal(t, answered.Questions, questions.Questions, "questions or answers changed")
	var got, want any
	{
		err := json.Unmarshal(decision.UpdatedInput, &got)
		require.NoError(t, err)
	}
	{
		err := json.Unmarshal(updated, &want)
		require.NoError(t, err)
	}
	require.Equal(t, got, want, "unknown input lost", got, want)
	copyHandle := *p
	{
		err := copyHandle.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny})
		require.ErrorIs(t, err, ErrPermissionUsed, err)
	}
	{
		got := permissionResponses(t, permissionSnapshot(t, c))
		require.EqualValues(t, 1, len(got), "response count", got)
	}
}

func (s *controlInteractionSuite) TestQuestionPreparedAnswerCannotBypassCancellation() {
	t := s.T()
	requests := make(chan *PermissionRequest, 1)
	c := s.openFixture(t, "permission_controls", func(opts *Options) {
		opts.PermissionHandler = func(p *PermissionRequest) { requests <- p }
	})
	emitPermission(t, c, "cancel/question", "AskUserQuestion", json.RawMessage(`{"questions":[{"question":"Proceed?","options":[]}]}`))
	p := awaitPermission(t, requests)
	metadata, err := p.Metadata()
	require.NoError(t, err)
	var input types.AskUserQuestionInput
	{
		err := json.Unmarshal(metadata.Input, &input)
		require.NoError(t, err)
	}
	text := "No custom action"
	updated, err := input.WithAnswers(map[string]types.QuestionAnswer{"Proceed?": {Text: &text}})
	require.NoError(t, err)
	emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": "cancel/question"})
	{
		event := receive(t, c)
		require.NotNil(t, event.Cancel, "missing cancellation")
	}
	{
		err := p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow, UpdatedInput: updated})
		require.ErrorIs(t, err, ErrPermissionCancelled, err)
	}
	{
		got := permissionResponses(t, permissionSnapshot(t, c))
		require.EqualValues(t, 0, len(got), "late answer written", got)
	}
	emitPermission(t, c, "deny/question", "AskUserQuestion", json.RawMessage(`{"questions":[]}`))
	deny := awaitPermission(t, requests)
	{
		err := deny.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny, Message: "User declined"})
		require.NoError(t, err)
	}
	assertPermissionReply(t, c, "deny/question", map[string]any{"behavior": "deny", "message": "User declined"})
	for _, zero := range []*PermissionRequest{nil, new(PermissionRequest)} {
		{
			_, err := zero.Metadata()
			require.ErrorIs(t, err, ErrInvalidPermissionRequest, err)
		}
	}
}
