// SPDX-License-Identifier: Apache-2.0

package types_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/zoumo/agent-sdk-go/claude/types"
)

func jsonValue(t *testing.T, b []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func roundTrip(t *testing.T, frame string) types.Event {
	t.Helper()
	e, err := types.ParseEvent([]byte(frame))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(jsonValue(t, b), jsonValue(t, []byte(frame))) {
		t.Fatalf("roundtrip:\n%s\n%s", frame, b)
	}
	return e
}
func marshalObject(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return jsonValue(t, b).(map[string]any)
}

func TestMessageWireRoundTrips(t *testing.T) {
	for _, frame := range []string{
		`{"type":"assistant","session_id":"s","uuid":"a","parent_tool_use_id":null,"error":"future_error","extra":0,"message":{"id":"m","role":"assistant","model":"fixture","stop_reason":null,"usage":{"input_tokens":0,"future":true},"extra":false,"content":[{"type":"text","text":"","citations":[]},{"type":"thinking","thinking":"x","signature":"s"},{"type":"redacted_thinking","data":"opaque","future":3},{"type":"tool_use","id":"t","name":"Read","input":{}},{"type":"server_tool_use","id":"s","name":"web_search","input":{"query":"x"}},{"type":"web_search_tool_result","tool_use_id":"s","content":{"type":"future","extra":1}},{"type":"future","nested":{"x":2}}]}}`,
		`{"type":"user","parent_tool_use_id":"t","tool_use_result":{"ok":true},"origin":{"kind":"future","channel":"x"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":null,"is_error":false,"future":0}]}}`,
		`{"type":"user","isSynthetic":false,"isReplay":true,"message":{"content":"你好"}}`,
		`{"type":"future","subtype":"future","value":[null,false,0,9007199254740993]}`,
		`{"type":"control_request","request_id":"r","future":false,"request":{"subtype":"can_use_tool","tool_name":"Read","input":{},"future":null}}`,
		`{"type":"control_cancel_request","request_id":"r","future":0}`,
		`{"type":"system","subtype":"future","payload":{"x":1}}`,
		`{"type":"system","subtype":"init","apiKeySource":"none","permissionMode":"default","mcp_servers":[{"name":"x","status":"connected","future":false}],"capabilities":[],"model":"m","tools":[]}`,
		`{"type":"system","subtype":"task_started","task_id":"t","description":"d","tool_use_id":null,"task_type":"local","future":false}`,
		`{"type":"system","subtype":"task_progress","task_id":"t","usage":{"total_tokens":0},"last_tool_name":"Read"}`,
		`{"type":"system","subtype":"task_notification","task_id":"t","status":"completed","output_file":"x","summary":"ok"}`,
		`{"type":"system","subtype":"task_updated","task_id":"t","patch":{"status":"killed","future":0}}`,
		`{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"auto","pre_tokens":0}}`,
		`{"type":"rate_limit_event","session_id":"s","rate_limit_info":{"status":"allowed","resetsAt":0,"utilization":0,"future":false}}`,
		`{"type":"conversation_reset","new_conversation_id":"new","uuid":"r","session_id":"old","future":null}`,
		`{"type":"stream_event","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":0,"delta":{"type":"future","text":"x"},"future":false}}`,
	} {
		t.Run(frame, func(t *testing.T) { roundTrip(t, frame) })
	}
}
func TestResultBranchesAndCamelCase(t *testing.T) {
	for _, subtype := range []string{"success", "error_during_execution", "error_max_turns", "error_max_budget_usd", "error_max_structured_output_retries", "future"} {
		frame := `{"type":"result","subtype":"` + subtype + `","is_error":true,"api_error_status":429,"stop_reason":null,"result":"error text","errors":[],"total_cost_usd":0,"duration_ms":0,"duration_api_ms":0,"num_turns":0,"usage":{"input_tokens":0},"modelUsage":{"fixture":{"inputTokens":0,"outputTokens":2,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"webSearchRequests":0,"costUSD":0,"contextWindow":200000,"maxOutputTokens":8000,"provider":"firstParty","future":false}},"permission_denials":[{"tool_name":"Read","future":1}],"deferred_tool_use":{"id":"d","name":"Read","input":{}},"structured_output":null,"origin":{"kind":"human"},"terminal_reason":"completed","future":0}`
		e := roundTrip(t, frame)
		if !e.Result.IsError || e.Result.APIErrorStatus == nil || e.Result.ModelUsage["fixture"].OutputTokens != 2 {
			t.Fatal("missing result views")
		}
		usage := e.Result.ModelUsage["fixture"]
		usage.InputTokens = 7
		e.Result.ModelUsage["fixture"] = usage
		m := marshalObject(t, e)
		models := m["modelUsage"].(map[string]any)
		u := models["fixture"].(map[string]any)
		if u["inputTokens"] != float64(7) || u["future"] != false {
			t.Fatal(m)
		}
		if _, bad := m["model_usage"]; bad {
			t.Fatal("snake case model_usage emitted")
		}
	}
}
func TestTypedEditsWinOverRaw(t *testing.T) {
	e := roundTrip(t, `{"type":"assistant","parent_tool_use_id":null,"error":null,"future":true,"message":{"content":[{"type":"text","text":"before","future":0}],"usage":null,"extra":42}}`)
	e.Assistant.Content.Blocks[0].Text.Text = "after"
	e.Assistant.Content.Blocks[0].Text.Fields = map[string]types.FieldState{"text": types.Absent}
	m := marshalObject(t, e)
	inner := m["message"].(map[string]any)
	block := inner["content"].([]any)[0].(map[string]any)
	if _, ok := block["text"]; ok {
		t.Fatal("block raw restored omitted typed field")
	}
	if block["future"] != float64(0) || inner["extra"] != float64(42) || m["future"] != true {
		t.Fatal(m)
	}
	e.Assistant.Content.Blocks[0].Text.Fields = nil
	m = marshalObject(t, e)
	block = m["message"].(map[string]any)["content"].([]any)[0].(map[string]any)
	if block["text"] != "after" {
		t.Fatal("cached raw hid typed edit")
	}
	if _, ok := inner["parent_tool_use_id"]; ok {
		t.Fatal("envelope field flattened")
	}
	e.ParentToolUseID = new(string)
	*e.ParentToolUseID = "parent"
	if marshalObject(t, e)["parent_tool_use_id"] != "parent" {
		t.Fatal("envelope edit lost")
	}
	s := roundTrip(t, `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"future":true}}`)
	i := 3
	s.Stream.Delta.Index = &i
	event := marshalObject(t, s)["event"].(map[string]any)
	if event["index"] != float64(3) || event["future"] != true {
		t.Fatal(event)
	}
	r := roundTrip(t, `{"type":"result","subtype":"success","session_id":"old"}`)
	r.Result.Subtype = "error_max_turns"
	r.Result.SessionID = "new"
	if m = marshalObject(t, r); m["subtype"] != "error_max_turns" || m["session_id"] != "new" {
		t.Fatal(m)
	}
}
func TestOptionalPresenceAndConstruction(t *testing.T) {
	for _, frame := range []string{`{"type":"result","subtype":"success"}`, `{"type":"result","subtype":"success","is_error":null,"result":null,"total_cost_usd":null,"num_turns":null}`, `{"type":"result","subtype":"success","is_error":false,"result":"","total_cost_usd":0,"num_turns":0}`} {
		roundTrip(t, frame)
	}
	e := types.Event{Type: "user", User: &types.Message{Role: "user", Content: types.Content{Text: ptr("hello")}}, Fields: map[string]types.FieldState{"parent_tool_use_id": types.Null, "isSynthetic": types.Present}}
	m := marshalObject(t, e)
	if m["isSynthetic"] != nil { // a nil *bool is null; supply a pointer for explicit false
		t.Fatal(m)
	}
	e.IsSynthetic = ptr(false)
	m = marshalObject(t, e)
	if m["isSynthetic"] != false || m["parent_tool_use_id"] != nil || m["message"].(map[string]any)["content"] != "hello" {
		t.Fatal(m)
	}
	r := types.ResultEvent{Subtype: "success", Fields: map[string]types.FieldState{"is_error": types.Present, "num_turns": types.Present, "result": types.Null, "errors": types.Absent}}
	m = marshalObject(t, r)
	if m["is_error"] != false || m["num_turns"] != float64(0) || m["result"] != nil {
		t.Fatal(m)
	}
	if _, exists := m["errors"]; exists {
		t.Fatal(m)
	}
	// Decoded absence can be changed to an explicit zero without a scalar API break.
	decoded := roundTrip(t, `{"type":"result","subtype":"success"}`)
	decoded.Result.Fields = map[string]types.FieldState{"num_turns": types.Present, "is_error": types.Null}
	m = marshalObject(t, decoded)
	if m["num_turns"] != float64(0) || m["is_error"] != nil {
		t.Fatal(m)
	}
	for _, block := range []types.ContentBlock{
		{Type: "text", Text: &types.TextBlock{Text: ""}},
		{Type: "tool_result", ToolResult: &types.ToolResultBlock{ToolUseID: "t", Content: json.RawMessage(`[]`)}},
		{Type: "server_tool_use", ServerToolUse: &types.ServerToolUseBlock{ID: "s", Name: "advisor", Input: json.RawMessage(`{}`)}},
	} {
		b, err := json.Marshal(block)
		if err != nil {
			t.Fatal(err)
		}
		var again types.ContentBlock
		if err = json.Unmarshal(b, &again); err != nil {
			t.Fatal(err)
		}
	}
}
func ptr[T any](v T) *T { return &v }
func TestRepeatedDecodeResetsUnionState(t *testing.T) {
	var c types.Content
	for _, raw := range []string{`"text"`, `[]`, `[{"type":"text","text":"x"}]`, `"last"`} {
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(c)
		if err != nil || !reflect.DeepEqual(jsonValue(t, b), jsonValue(t, []byte(raw))) {
			t.Fatalf("%s %s %v", raw, b, err)
		}
	}
	var block types.ContentBlock
	for _, raw := range []string{`{"type":"text","text":"x"}`, `{"type":"thinking","thinking":"x","signature":"s"}`, `{"type":"future"}`} {
		if err := json.Unmarshal([]byte(raw), &block); err != nil {
			t.Fatal(err)
		}
		if _, err := json.Marshal(block); err != nil {
			t.Fatal(err)
		}
	}
	if block.Text != nil || block.Thinking != nil {
		t.Fatal("stale block branch")
	}
	var e types.Event
	for _, raw := range []string{`{"type":"assistant","message":{"content":"x"}}`, `{"type":"result","subtype":"success"}`, `{"type":"future"}`} {
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			t.Fatal(err)
		}
	}
	if e.Assistant != nil || e.Result != nil {
		t.Fatal("stale event branch")
	}
	if err := json.Unmarshal([]byte(`{"type":"user","message":null}`), &e); err == nil || e.Type != "" {
		t.Fatal("failed decode retained prior event")
	}
	var result types.ResultEvent
	_ = json.Unmarshal([]byte(`{"is_error":true,"modelUsage":{"m":{"inputTokens":1}}}`), &result)
	_ = json.Unmarshal([]byte(`{}`), &result)
	if result.IsError || result.ModelUsage != nil {
		t.Fatal("stale result fields")
	}
}
func TestInvalidUnions(t *testing.T) {
	for _, raw := range []string{`null`, `true`, `{}`, `[null]`, `[{"type":"tool_use","input":[]}]`, `[{"type":"tool_result","content":{}}]`, `[{"type":"tool_result","content":[1]}]`} {
		var c types.Content
		if err := json.Unmarshal([]byte(raw), &c); err == nil {
			t.Fatalf("accepted content %s", raw)
		}
	}
	for _, v := range []any{
		types.Content{Text: ptr("x"), Blocks: []types.ContentBlock{}},
		types.ContentBlock{Type: "text", Text: &types.TextBlock{}, Thinking: &types.ThinkingBlock{}},
		types.ContentBlock{Type: "tool_use", Text: &types.TextBlock{}},
		types.ContentBlock{Type: "text"},
		types.ContentBlock{Type: "text", ServerToolResult: &types.ServerToolResultBlock{}},
		types.Event{Type: "user", Assistant: &types.Message{}},
		types.Event{Type: "user", User: &types.Message{}, Result: &types.ResultEvent{}},
		types.Event{Type: "user"},
		types.ResultEvent{Fields: map[string]types.FieldState{"result": 99}},
	} {
		if _, err := json.Marshal(v); err == nil {
			t.Fatalf("accepted invalid union %#v", v)
		}
	}
}

func TestEnvelopePrecedenceAndExactUnknownNumber(t *testing.T) {
	e := roundTrip(t, `{"type":"result","subtype":"success","session_id":"old","future":9007199254740993}`)
	e.SessionID = "envelope"
	e.Result.SessionID = "view"
	if marshalObject(t, e)["session_id"] != "envelope" {
		t.Fatal("view overrode explicit envelope edit")
	}
	e.Fields = map[string]types.FieldState{"session_id": types.Absent}
	m := marshalObject(t, e)
	if _, ok := m["session_id"]; ok {
		t.Fatal("branch restored explicitly absent envelope field")
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"future":9007199254740993`)) {
		t.Fatal("unknown integer lost precision")
	}
	e.Result.Fields = map[string]types.FieldState{"result": types.Absent}
	e.Result.Result = "typed"
	if _, ok := marshalObject(t, e)["result"]; ok {
		t.Fatal("explicit branch omission lost")
	}
}
