// SPDX-License-Identifier: Apache-2.0

package types_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/zoumo/agent-sdk-go/claude/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetachedPermissionMetadata(t *testing.T) {
	frame := []byte(`{"type":"control_request","request_id":"r","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"t","agent_id":"a","input":{"questions":[],"future":{"integer":9007199254740993}},"permission_suggestions":[{"type":"future","nested":{"x":false}}],"blocked_path":null,"title":"Review","display_name":"Question","description":"Choose","decision_reason":"ask","future":[null]}}`)
	event, err := types.ParseEvent(frame)
	require.NoError(t, err)
	metadata, err := event.Control.PermissionMetadata()
	require.NoError(t, err)
	original := bytes.Clone(metadata.Raw)
	frame[0] = '!'
	event.Control.Request[0] = '!'
	require.True(t, bytes.Equal(metadata.Raw, original), "metadata not detached")
	require.EqualValues(t, "t", metadata.ToolUseID, "metadata not detached")
	require.EqualValues(t, "a", metadata.AgentID, "metadata not detached")
	metadata.Title = "Edited"
	metadata.Fields = map[string]types.FieldState{"description": types.Absent, "display_name": types.Null}
	m := marshalObject(t, metadata)
	require.EqualValues(t, "Edited", m["title"], m)
	require.Nil(t, m["blocked_path"], m)
	require.Nil(t, m["display_name"], m)
	{
		_, ok := m["description"]
		require.False(t, ok, "omission lost")
	}
	b, err := json.Marshal(metadata)
	require.NoErrorf(t, err, "large unknown number lost: %s %v", b, err)
	require.Truef(t, bytes.Contains(b, []byte("9007199254740993")), "large unknown number lost: %s %v", b, err)
	{
		err := json.Unmarshal([]byte(`{"subtype":"can_use_tool","tool_name":"Read","input":{}}`), &metadata)
		require.NoError(t, err)
	}
	require.EqualValues(t, "", metadata.AgentID, "decode failed to reset")
	require.Nil(t, metadata.Fields, "decode failed to reset")
	{
		err := json.Unmarshal([]byte(`{"subtype":"interrupt"}`), &metadata)
		require.Error(t, err, "invalid decode must reset")
		require.Nil(t, metadata.Raw, "invalid decode must reset")
		require.EqualValues(t, "", metadata.ToolName, "invalid decode must reset")
	}
}
func TestPermissionResponseWire(t *testing.T) {
	for _, frame := range []string{
		`{"type":"control_response","extra":false,"response":{"subtype":"success","request_id":"r","response":{"behavior":"allow","updatedInput":{"unknown":{"n":9007199254740993}}},"future":null}}`,
		`{"type":"control_response","response":{"subtype":"success","request_id":"r"}}`,
		`{"type":"control_response","response":{"subtype":"success","request_id":"r","response":null}}`,
		`{"type":"control_response","response":{"subtype":"error","request_id":"r","error":"拒绝","future":[]}}`,
	} {
		t.Run(fmt.Sprintf("%v", frame), func(t *testing.T) {
			roundTrip(t, frame)
		})
	}
	var result types.PermissionResult
	for _, data := range []string{`{"behavior":"allow"}`, `{"behavior":"allow","updatedInput":{},"future":null}`, `{"behavior":"deny","message":"","future":{"n":9007199254740993}}`} {
		t.Run(fmt.Sprintf("%v", data), func(t *testing.T) {
			{
				err := json.Unmarshal([]byte(data), &result)
				require.NoError(t, err)
			}
			b, err := json.Marshal(result)
			require.NoErrorf(t, err, "result roundtrip %s %v", b, err)
			require.Equalf(t, jsonValue(t, b), jsonValue(t, []byte(data)), "result roundtrip %s %v", b, err)
		})
	}
	deny := marshalObject(t, types.PermissionResult{Behavior: types.PermissionDeny})
	require.EqualValues(t, "", deny["message"], deny)
	payload, err := json.Marshal(types.PermissionResult{Behavior: types.PermissionAllow, UpdatedInput: json.RawMessage(`{}`)})
	require.NoError(t, err)
	event := types.Event{Type: "control_response", Response: &types.ControlResponse{Type: "control_response", Response: types.ControlResponseData{Subtype: "success", RequestID: "exact/id", Response: payload}}}
	b, err := json.Marshal(event)
	require.NoError(t, err)
	want := `{"type":"control_response","response":{"subtype":"success","request_id":"exact/id","response":{"behavior":"allow","updatedInput":{}}}}`
	require.Equalf(t, jsonValue(t, b), jsonValue(t, []byte(want)), "wrong nesting: %s", b)
	decoded := roundTrip(t, want)
	decoded.Response.Response.Response = json.RawMessage(`{"behavior":"deny","message":"no"}`)
	m := marshalObject(t, decoded)
	require.EqualValues(t, "deny", m["response"].(map[string]any)["response"].(map[string]any)["behavior"], "typed response masked by Raw")
}

const questionInput = `{"questions":[{"question":"How should I format the output?","header":"Format","options":[{"label":"Summary","description":"Short","preview":"# Summary","future":{"integer":9007199254740993}},{"label":"Detailed","description":"Long"}],"multiSelect":false,"future":{"ui":true}},{"question":"Which sections should I include?","header":"Sections","options":[{"label":"Introduction"},{"label":"Conclusion"}],"multiSelect":true}],"context":{"unknown":[1,false,null]}}`

func TestQuestionAnswersPreserveInput(t *testing.T) {
	var input types.AskUserQuestionInput
	{
		err := json.Unmarshal([]byte(questionInput), &input)
		require.NoError(t, err)
	}
	text := "Custom 自由文本"
	answers := map[string]types.QuestionAnswer{
		input.Questions[0].Question: {Text: &text},
		input.Questions[1].Question: {Labels: []string{"Introduction", "Conclusion"}},
	}
	b, err := input.WithAnswers(answers)
	require.NoError(t, err)
	before := jsonValue(t, []byte(questionInput)).(map[string]any)
	after := jsonValue(t, b).(map[string]any)
	require.Equal(t, before["questions"], after["questions"], "original input lost")
	require.Equal(t, before["context"], after["context"], "original input lost")
	require.True(t, bytes.Contains(b, []byte("9007199254740993")), "unknown nested precision lost")
	require.Nil(t, input.Answers, "helper mutated receiver")
	require.Nil(t, input.Fields, "helper mutated receiver")
	{
		err := json.Unmarshal(b, &input)
		require.NoError(t, err)
	}
	input.Questions[0].Options[0].Label = "Edited"
	input.Questions[0].Fields = map[string]types.FieldState{"multiSelect": types.Present}
	m := marshalObject(t, input)
	q := m["questions"].([]any)[0].(map[string]any)
	require.EqualValues(t, false, q["multiSelect"], "nested mutation masked")
	require.EqualValues(t, "Edited", q["options"].([]any)[0].(map[string]any)["label"], "nested mutation masked")
	input.Fields = map[string]types.FieldState{"response": types.Null, "answers": types.Absent}
	m = marshalObject(t, input)
	{
		_, ok := m["answers"]
		require.False(t, ok, "answers omission failed")
	}
	{
		v, ok := m["response"]
		require.True(t, ok, "response null lost")
		require.Nil(t, v, "response null lost")
	}
	{
		err := json.Unmarshal([]byte(`{"questions":[]}`), &input)
		require.NoError(t, err, "reset failed", err)
		require.Nil(t, input.Answers, "reset failed", err)
		require.Nil(t, input.Fields, "reset failed", err)
	}
	{
		err := json.Unmarshal([]byte(`{"questions":null}`), &input)
		require.Error(t, err, "failed decode must reset")
		require.Nil(t, input.Raw, "failed decode must reset")
		require.Nil(t, input.Questions, "failed decode must reset")
	}
}
func TestPermissionAndQuestionInvalidShapes(t *testing.T) {
	tests := []struct {
		name   string
		value  func() any
		inputs []string
	}{
		{"metadata", func() any { return new(types.PermissionMetadata) }, []string{`null`, `[]`, `{}`, `{"subtype":"can_use_tool","tool_name":"Read","input":null}`, `{"subtype":"can_use_tool","tool_name":"Read","input":[]}`, `{"subtype":"can_use_tool","tool_name":"Read","input":{},"permission_suggestions":[null]}`}},
		{"result", func() any { return new(types.PermissionResult) }, []string{`null`, `{"behavior":"ask"}`, `{"behavior":"allow","updatedInput":null}`, `{"behavior":"allow","updatedInput":[]}`, `{"behavior":"allow","message":""}`, `{"behavior":"deny"}`, `{"behavior":"deny","message":null}`, `{"behavior":"deny","message":"no","updatedInput":{}}`}},
		{"response", func() any { return new(types.ControlResponse) }, []string{`{"type":"control_response","subtype":"success","request_id":"r"}`, `{"type":"control_response","response":{"subtype":"success","request_id":""}}`, `{"type":"control_response","response":{"subtype":"error","request_id":"r"}}`, `{"type":"control_response","response":{"subtype":"success","request_id":"r","error":""}}`, `{"type":"control_response","response":{"subtype":"error","request_id":"r","error":"no","response":{}}}`}},
		{"questions", func() any { return new(types.AskUserQuestionInput) }, []string{`{}`, `{"questions":null}`, `{"questions":[null]}`, `{"questions":[{"question":"Q","options":null}]}`, `{"questions":[{"question":"Q","options":[{}]}]}`, `{"questions":[],"answers":{"q_0":"x"}}`, `{"questions":[],"answers":null}`, `{"questions":[{"question":"Q","options":[]},{"question":"Q","options":[]}]}`}},
		{"answer", func() any { return new(types.QuestionAnswer) }, []string{`null`, `true`, `1`, `{}`, `[null]`, `[1]`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, data := range tt.inputs {
				t.Run(fmt.Sprintf("%v", data), func(t *testing.T) {
					{
						err := json.Unmarshal([]byte(data), tt.value())
						assert.Errorf(t, err, "accepted %s", data)
					}
				})
			}
		})
	}
	for _, v := range []any{
		types.PermissionMetadata{Subtype: "can_use_tool", ToolName: "Read", Input: json.RawMessage(`{}`), Fields: map[string]types.FieldState{"tool_name": types.Absent}},
		types.PermissionResult{Behavior: types.PermissionAllow, Fields: map[string]types.FieldState{"behavior": types.Null}},
		types.ControlResponse{Type: "control_response", Response: types.ControlResponseData{Subtype: "success", RequestID: "r"}, Fields: map[string]types.FieldState{"response": types.Absent}},
		types.QuestionAnswer{}, types.QuestionAnswer{Text: new(string), Labels: []string{}},
		types.Question{Question: "Q", Options: []types.QuestionOption{}, Fields: map[string]types.FieldState{"options": types.Null}},
	} {
		t.Run(fmt.Sprintf("%T/%v", v, v), func(t *testing.T) {
			{
				b, err := json.Marshal(v)
				assert.Errorf(t, err, "accepted constructed invalid value: %s", b)
			}
		})
	}
	var input types.AskUserQuestionInput
	{
		err := json.Unmarshal([]byte(questionInput), &input)
		require.NoError(t, err)
	}
	text := "Summary"
	for _, answers := range []map[string]types.QuestionAnswer{
		nil, {"q_0": {Text: &text}}, {"Format": {Text: &text}}, {input.Questions[0].Question: {Labels: []string{"Summary"}}},
	} {
		t.Run(fmt.Sprintf("%T/%v", answers, answers), func(t *testing.T) {
			{
				b, err := input.WithAnswers(answers)
				assert.Errorf(t, err, "accepted invalid answers: %s", b)
			}
		})
	}
	for _, data := range []string{`""`, `"Other 自定义"`, `[]`, `["Introduction","Conclusion"]`} {
		t.Run(fmt.Sprintf("%v", data), func(t *testing.T) {
			var answer types.QuestionAnswer
			{
				err := json.Unmarshal([]byte(data), &answer)
				require.NoError(t, err)
			}
			b, err := json.Marshal(answer)
			require.NoErrorf(t, err, "answer roundtrip: %s %v", b, err)
			require.EqualValuesf(t, data, string(b), "answer roundtrip: %s %v", b, err)
		})
	}
}

func TestPermissionDataRepeatedDecodeAndPresence(t *testing.T) {
	cases := []struct {
		value         any
		first, second string
	}{
		{new(types.PermissionMetadata), `{"subtype":"can_use_tool","tool_name":"Read","input":{},"agent_id":"a","title":null}`, `{"subtype":"can_use_tool","tool_name":"Write","input":{}}`},
		{new(types.PermissionResult), `{"behavior":"allow","updatedInput":{"x":1}}`, `{"behavior":"deny","message":""}`},
		{new(types.ControlResponse), `{"type":"control_response","response":{"subtype":"success","request_id":"r","response":{}}}`, `{"type":"control_response","response":{"subtype":"error","request_id":"other","error":"no"}}`},
		{new(types.ControlResponseData), `{"subtype":"success","request_id":"r","response":{}}`, `{"subtype":"error","request_id":"r","error":"no"}`},
		{new(types.Question), `{"question":"A?","header":"A","multiSelect":true,"options":[{"label":"X"}]}`, `{"question":"B?","options":[]}`},
		{new(types.QuestionOption), `{"label":"A","description":"long","preview":"preview"}`, `{"label":"B"}`},
		{new(types.QuestionAnswer), `["A","B"]`, `"custom"`},
	}
	for _, tc := range cases {
		t.Run(reflect.TypeOf(tc.value).Elem().Name(), func(t *testing.T) {
			{
				err := json.Unmarshal([]byte(tc.first), tc.value)
				require.NoError(t, err)
			}
			{
				err := json.Unmarshal([]byte(tc.second), tc.value)
				require.NoError(t, err)
			}
			fresh := reflect.New(reflect.TypeOf(tc.value).Elem()).Interface()
			{
				err := json.Unmarshal([]byte(tc.second), fresh)
				require.NoError(t, err)
			}
			require.Equal(t, tc.value, fresh, "stale receiver fields")
			{
				err := json.Unmarshal([]byte(`null`), tc.value)
				require.Error(t, err, "accepted null")
			}
			zero := reflect.New(reflect.TypeOf(tc.value).Elem()).Interface()
			require.Equal(t, tc.value, zero, "failed decode did not zero receiver")
		})
	}
	var metadata types.PermissionMetadata
	{
		err := json.Unmarshal([]byte(`{"subtype":"can_use_tool","tool_name":"Read","input":{},"title":null}`), &metadata)
		require.NoError(t, err)
	}
	metadata.Fields = map[string]types.FieldState{"title": types.Present, "agent_id": types.Null, "description": types.Absent}
	m := marshalObject(t, metadata)
	require.EqualValues(t, "", m["title"], m)
	require.Nil(t, m["agent_id"], m)
	metadata.Fields["title"] = types.FieldState(255)
	{
		_, err := json.Marshal(metadata)
		require.Error(t, err, "invalid field state accepted")
	}
	var input types.AskUserQuestionInput
	{
		err := json.Unmarshal([]byte(questionInput), &input)
		require.NoError(t, err)
	}
	joined := "Introduction, Conclusion"
	for _, answers := range []map[string]types.QuestionAnswer{{}, {input.Questions[1].Question: {Text: &joined}}} {
		t.Run(fmt.Sprintf("%T/%v", answers, answers), func(t *testing.T) {
			{
				_, err := input.WithAnswers(answers)
				require.NoError(t, err, "valid partial/joined answer rejected", err)
			}
		})
	}
	invalid := "\xff"
	{
		_, err := json.Marshal(types.QuestionAnswer{Text: &invalid})
		require.Error(t, err, "invalid UTF-8 answer accepted")
	}
	var answer types.QuestionAnswer
	{
		err := json.Unmarshal([]byte{'"', 255, '"'}, &answer)
		require.Error(t, err, "invalid UTF-8 wire accepted")
	}
}
