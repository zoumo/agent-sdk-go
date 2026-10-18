// SPDX-License-Identifier: Apache-2.0

package types_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/zoumo/agent-sdk-go/claude/types"
)

// This test package deliberately imports only types, never the runtime client.
func TestParseEventViewsAndRawOwnership(t *testing.T) {
	frame := []byte(`{"type":"assistant","future":true,"message":{"id":"m1","role":"assistant","model":"fixture","content":[{"type":"text","text":"你好"},{"type":"thinking","thinking":"thought","signature":"s"},{"type":"tool_use","id":"t1","name":"Read","input":{"path":"p"}},{"type":"tool_result","tool_use_id":"t1","content":"done","is_error":true},{"type":"future","extra":42}],"usage":{"future_tokens":7},"unknown":"kept"}}`)
	want := bytes.Clone(frame)
	event, err := types.ParseEvent(frame)
	if err != nil {
		t.Fatal(err)
	}
	frame[0] = '!'
	if !bytes.Equal(event.Raw, want) || event.Assistant == nil {
		t.Fatal("frame ownership or assistant view lost")
	}
	m := event.Assistant
	b := m.Content.Blocks
	if len(b) != 5 || b[0].Text.Text != "你好" || b[1].Thinking.Signature != "s" || b[2].ToolUse.ID != "t1" || !b[3].ToolResult.IsError || b[4].Type != "future" {
		t.Fatalf("content views = %#v", b)
	}
	if string(b[4].Raw) != `{"type":"future","extra":42}` || string(m.Usage) != `{"future_tokens":7}` || !bytes.Contains(m.Raw, []byte(`"unknown":"kept"`)) {
		t.Fatal("unknown data lost")
	}
	for _, raw := range []json.RawMessage{event.Raw, m.Raw, m.Content.Raw, b[0].Raw, b[4].Raw} {
		if !json.Valid(raw) {
			t.Fatalf("invalid retained raw JSON: %s", raw)
		}
	}
}

func TestParseOtherEventViews(t *testing.T) {
	tests := []struct {
		frame string
		check func(types.Event) bool
	}{
		{`{"type":"system","subtype":"init","session_id":"s","model":"m","tools":["Read"]}`, func(e types.Event) bool {
			return e.System != nil && e.System.SessionID == "s" && bytes.Equal(e.System.Raw, e.Raw)
		}},
		{`{"type":"user","message":{"role":"user","content":"hello"}}`, func(e types.Event) bool {
			return e.User != nil && e.User.Content.Text != nil && *e.User.Content.Text == "hello"
		}},
		{`{"type":"result","subtype":"success","result":"done","num_turns":2,"total_cost_usd":0,"structured_output":{"ok":true},"future":1}`, func(e types.Event) bool {
			return e.Result != nil && e.Result.NumTurns == 2 && e.Result.TotalCostUSD != nil && *e.Result.TotalCostUSD == 0 && bytes.Equal(e.Result.Raw, e.Raw)
		}},
		{`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}},"parent_tool_use_id":null}`, func(e types.Event) bool {
			return e.Stream != nil && e.Stream.Delta != nil && e.Stream.Delta.Index != nil && *e.Stream.Delta.Index == 0 && bytes.Equal(e.Stream.Raw, e.Raw)
		}},
		{`{"type":"control_request","request_id":"r","request":{"subtype":"can_use_tool","tool_name":"Read","input":{},"future":1}}`, func(e types.Event) bool {
			return e.Control != nil && e.Control.RequestID == "r" && e.Control.Subtype == "can_use_tool"
		}},
		{`{"type":"control_cancel_request","request_id":"r","future":1}`, func(e types.Event) bool {
			return e.Cancel != nil && e.Cancel.RequestID == "r" && bytes.Equal(e.Cancel.Raw, e.Raw)
		}},
		{`{"type":"future","unknown":[1,2]}`, func(e types.Event) bool { return e.Type == "future" && e.Assistant == nil && e.Control == nil }},
	}
	for _, tt := range tests {
		t.Run(tt.frame, func(t *testing.T) {
			e, err := types.ParseEvent([]byte(tt.frame))
			if err != nil || !tt.check(e) || string(e.Raw) != tt.frame {
				t.Fatalf("event = %#v, error = %v", e, err)
			}
		})
	}
}

func TestParseEventRejectsMalformedExistingShapes(t *testing.T) {
	for _, frame := range []string{
		"\xff", `{`, `{}`, `{"type":1}`,
		`{"type":"assistant","message":null}`,
		`{"type":"assistant","message":{}}`,
		`{"type":"user","message":{"content":{}}}`,
		`{"type":"user","message":{"content":[{}]}}`,
		`{"type":"control_request","request_id":"r","request":{}}`,
		`{"type":"control_cancel_request"}`,
	} {
		if _, err := types.ParseEvent([]byte(frame)); err == nil {
			t.Fatalf("accepted malformed frame %q", frame)
		}
	}
}

func TestContentCodecsAvailableWithoutRuntime(t *testing.T) {
	var content types.Content
	if err := json.Unmarshal([]byte(`[{"type":"text","text":"hello"}]`), &content); err != nil || len(content.Blocks) != 1 || content.Blocks[0].Text.Text != "hello" {
		t.Fatalf("content = %#v, error = %v", content, err)
	}
	var block types.ContentBlock
	if err := json.Unmarshal([]byte(`{"type":"tool_use","id":"t","name":"Read","input":{}}`), &block); err != nil || block.ToolUse == nil || block.ToolUse.Name != "Read" {
		t.Fatalf("block = %#v, error = %v", block, err)
	}
}
