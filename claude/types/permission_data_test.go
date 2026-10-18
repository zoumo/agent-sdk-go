// SPDX-License-Identifier: Apache-2.0

package types_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/zoumo/agent-sdk-go/claude/types"
)

func TestDetachedPermissionMetadata(t *testing.T) {
	frame := []byte(`{"type":"control_request","request_id":"r","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"t","agent_id":"a","input":{"questions":[],"future":{"integer":9007199254740993}},"permission_suggestions":[{"type":"future","nested":{"x":false}}],"blocked_path":null,"title":"Review","display_name":"Question","description":"Choose","decision_reason":"ask","future":[null]}}`)
	event, err := types.ParseEvent(frame)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := event.Control.PermissionMetadata()
	if err != nil {
		t.Fatal(err)
	}
	original := bytes.Clone(metadata.Raw)
	frame[0] = '!'
	event.Control.Request[0] = '!'
	if !bytes.Equal(metadata.Raw, original) || metadata.ToolUseID != "t" || metadata.AgentID != "a" {
		t.Fatal("metadata not detached")
	}
	metadata.Title = "Edited"
	metadata.Fields = map[string]types.FieldState{"description": types.Absent, "display_name": types.Null}
	m := marshalObject(t, metadata)
	if m["title"] != "Edited" || m["blocked_path"] != nil || m["display_name"] != nil {
		t.Fatal(m)
	}
	if _, ok := m["description"]; ok {
		t.Fatal("omission lost")
	}
	b, err := json.Marshal(metadata)
	if err != nil || !bytes.Contains(b, []byte("9007199254740993")) {
		t.Fatalf("large unknown number lost: %s %v", b, err)
	}
	if err := json.Unmarshal([]byte(`{"subtype":"can_use_tool","tool_name":"Read","input":{}}`), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.AgentID != "" || metadata.Fields != nil {
		t.Fatal("decode failed to reset")
	}
	if err := json.Unmarshal([]byte(`{"subtype":"interrupt"}`), &metadata); err == nil || metadata.Raw != nil || metadata.ToolName != "" {
		t.Fatal("invalid decode must reset")
	}
}
func TestPermissionResponseWire(t *testing.T) {
	for _, frame := range []string{
		`{"type":"control_response","extra":false,"response":{"subtype":"success","request_id":"r","response":{"behavior":"allow","updatedInput":{"unknown":{"n":9007199254740993}}},"future":null}}`,
		`{"type":"control_response","response":{"subtype":"success","request_id":"r"}}`,
		`{"type":"control_response","response":{"subtype":"success","request_id":"r","response":null}}`,
		`{"type":"control_response","response":{"subtype":"error","request_id":"r","error":"拒绝","future":[]}}`,
	} {
		roundTrip(t, frame)
	}
	var result types.PermissionResult
	for _, data := range []string{`{"behavior":"allow"}`, `{"behavior":"allow","updatedInput":{},"future":null}`, `{"behavior":"deny","message":"","future":{"n":9007199254740993}}`} {
		if err := json.Unmarshal([]byte(data), &result); err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(result)
		if err != nil || !reflect.DeepEqual(jsonValue(t, b), jsonValue(t, []byte(data))) {
			t.Fatalf("result roundtrip %s %v", b, err)
		}
	}
	deny := marshalObject(t, types.PermissionResult{Behavior: types.PermissionDeny})
	if deny["message"] != "" {
		t.Fatal(deny)
	}
	payload, err := json.Marshal(types.PermissionResult{Behavior: types.PermissionAllow, UpdatedInput: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	event := types.Event{Type: "control_response", Response: &types.ControlResponse{Type: "control_response", Response: types.ControlResponseData{Subtype: "success", RequestID: "exact/id", Response: payload}}}
	b, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"control_response","response":{"subtype":"success","request_id":"exact/id","response":{"behavior":"allow","updatedInput":{}}}}`
	if !reflect.DeepEqual(jsonValue(t, b), jsonValue(t, []byte(want))) {
		t.Fatalf("wrong nesting: %s", b)
	}
	decoded := roundTrip(t, want)
	decoded.Response.Response.Response = json.RawMessage(`{"behavior":"deny","message":"no"}`)
	m := marshalObject(t, decoded)
	if m["response"].(map[string]any)["response"].(map[string]any)["behavior"] != "deny" {
		t.Fatal("typed response masked by Raw")
	}
}

const questionInput = `{"questions":[{"question":"How should I format the output?","header":"Format","options":[{"label":"Summary","description":"Short","preview":"# Summary","future":{"integer":9007199254740993}},{"label":"Detailed","description":"Long"}],"multiSelect":false,"future":{"ui":true}},{"question":"Which sections should I include?","header":"Sections","options":[{"label":"Introduction"},{"label":"Conclusion"}],"multiSelect":true}],"context":{"unknown":[1,false,null]}}`

func TestQuestionAnswersPreserveInput(t *testing.T) {
	var input types.AskUserQuestionInput
	if err := json.Unmarshal([]byte(questionInput), &input); err != nil {
		t.Fatal(err)
	}
	text := "Custom 自由文本"
	answers := map[string]types.QuestionAnswer{
		input.Questions[0].Question: {Text: &text},
		input.Questions[1].Question: {Labels: []string{"Introduction", "Conclusion"}},
	}
	b, err := input.WithAnswers(answers)
	if err != nil {
		t.Fatal(err)
	}
	before := jsonValue(t, []byte(questionInput)).(map[string]any)
	after := jsonValue(t, b).(map[string]any)
	if !reflect.DeepEqual(before["questions"], after["questions"]) || !reflect.DeepEqual(before["context"], after["context"]) {
		t.Fatal("original input lost")
	}
	if !bytes.Contains(b, []byte("9007199254740993")) {
		t.Fatal("unknown nested precision lost")
	}
	if input.Answers != nil || input.Fields != nil {
		t.Fatal("helper mutated receiver")
	}
	if err := json.Unmarshal(b, &input); err != nil {
		t.Fatal(err)
	}
	input.Questions[0].Options[0].Label = "Edited"
	input.Questions[0].Fields = map[string]types.FieldState{"multiSelect": types.Present}
	m := marshalObject(t, input)
	q := m["questions"].([]any)[0].(map[string]any)
	if q["multiSelect"] != false || q["options"].([]any)[0].(map[string]any)["label"] != "Edited" {
		t.Fatal("nested mutation masked")
	}
	input.Fields = map[string]types.FieldState{"response": types.Null, "answers": types.Absent}
	m = marshalObject(t, input)
	if _, ok := m["answers"]; ok {
		t.Fatal("answers omission failed")
	}
	if v, ok := m["response"]; !ok || v != nil {
		t.Fatal("response null lost")
	}
	if err := json.Unmarshal([]byte(`{"questions":[]}`), &input); err != nil || input.Answers != nil || input.Fields != nil {
		t.Fatal("reset failed", err)
	}
	if err := json.Unmarshal([]byte(`{"questions":null}`), &input); err == nil || input.Raw != nil || input.Questions != nil {
		t.Fatal("failed decode must reset")
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
				if err := json.Unmarshal([]byte(data), tt.value()); err == nil {
					t.Errorf("accepted %s", data)
				}
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
		if b, err := json.Marshal(v); err == nil {
			t.Errorf("accepted constructed invalid value: %s", b)
		}
	}
	var input types.AskUserQuestionInput
	if err := json.Unmarshal([]byte(questionInput), &input); err != nil {
		t.Fatal(err)
	}
	text := "Summary"
	for _, answers := range []map[string]types.QuestionAnswer{
		nil, {"q_0": {Text: &text}}, {"Format": {Text: &text}}, {input.Questions[0].Question: {Labels: []string{"Summary"}}},
	} {
		if b, err := input.WithAnswers(answers); err == nil {
			t.Errorf("accepted invalid answers: %s", b)
		}
	}
	for _, data := range []string{`""`, `"Other 自定义"`, `[]`, `["Introduction","Conclusion"]`} {
		var answer types.QuestionAnswer
		if err := json.Unmarshal([]byte(data), &answer); err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(answer)
		if err != nil || string(b) != data {
			t.Fatalf("answer roundtrip: %s %v", b, err)
		}
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
			if err := json.Unmarshal([]byte(tc.first), tc.value); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.second), tc.value); err != nil {
				t.Fatal(err)
			}
			fresh := reflect.New(reflect.TypeOf(tc.value).Elem()).Interface()
			if err := json.Unmarshal([]byte(tc.second), fresh); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(tc.value, fresh) {
				t.Fatal("stale receiver fields")
			}
			if err := json.Unmarshal([]byte(`null`), tc.value); err == nil {
				t.Fatal("accepted null")
			}
			zero := reflect.New(reflect.TypeOf(tc.value).Elem()).Interface()
			if !reflect.DeepEqual(tc.value, zero) {
				t.Fatal("failed decode did not zero receiver")
			}
		})
	}
	var metadata types.PermissionMetadata
	if err := json.Unmarshal([]byte(`{"subtype":"can_use_tool","tool_name":"Read","input":{},"title":null}`), &metadata); err != nil {
		t.Fatal(err)
	}
	metadata.Fields = map[string]types.FieldState{"title": types.Present, "agent_id": types.Null, "description": types.Absent}
	m := marshalObject(t, metadata)
	if m["title"] != "" || m["agent_id"] != nil {
		t.Fatal(m)
	}
	metadata.Fields["title"] = types.FieldState(255)
	if _, err := json.Marshal(metadata); err == nil {
		t.Fatal("invalid field state accepted")
	}
	var input types.AskUserQuestionInput
	if err := json.Unmarshal([]byte(questionInput), &input); err != nil {
		t.Fatal(err)
	}
	joined := "Introduction, Conclusion"
	for _, answers := range []map[string]types.QuestionAnswer{{}, {input.Questions[1].Question: {Text: &joined}}} {
		if _, err := input.WithAnswers(answers); err != nil {
			t.Fatal("valid partial/joined answer rejected", err)
		}
	}
	invalid := "\xff"
	if _, err := json.Marshal(types.QuestionAnswer{Text: &invalid}); err == nil {
		t.Fatal("invalid UTF-8 answer accepted")
	}
	var answer types.QuestionAnswer
	if err := json.Unmarshal([]byte{'"', 255, '"'}, &answer); err == nil {
		t.Fatal("invalid UTF-8 wire accepted")
	}
}
