// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/zoumo/claude-agent-sdk-go/types"
)

// Function signatures require identical types, not merely convertible structs.
var (
	_ func(context.Context) (ReceivedEvent, error)        = (*Client)(nil).Receive
	_ func(context.Context, types.PermissionAnswer) error = (*PermissionRequest)(nil).Answer
	_ types.Event                                         = Event{
		System: &types.SystemEvent{},
		Assistant: &types.Message{Content: types.Content{Blocks: []ContentBlock{{
			Text: &types.TextBlock{}, Thinking: &types.ThinkingBlock{},
			ToolUse: &types.ToolUseBlock{}, ToolResult: &types.ToolResultBlock{},
		}}}},
		User: &Message{}, Result: &types.ResultEvent{},
		Stream:  &types.StreamEvent{Delta: &StreamDelta{}},
		Control: &types.ControlRequest{}, Cancel: &types.ControlCancelRequest{},
	}
	_ types.PermissionBehavior = PermissionAllow
	_ PermissionBehavior       = types.PermissionDeny
	_ Options                  = Options{
		CLIPath: "fixture", WorkDir: ".", Model: "m", ResumeID: "s", Env: []string{},
		InitializeTimeout: time.Second, ShutdownTimeout: time.Second,
		Limits: Limits{
			EventBuffer: 1, FrameBytes: 1024, PendingControls: 1, ControlBuffer: 1,
			StderrBytes: 1, PendingPermissions: 1, PermissionWorkers: 1, PermissionHistory: 1,
		},
		PermissionHandler: func(*PermissionRequest) {},
	}
)

func TestCallbackFreeOptionsRoundTrip(t *testing.T) {
	config := types.Options{
		CLIPath: "literal cli", WorkDir: "literal dir", Model: "-model", ResumeID: "-session",
		Env: []string{"FIXTURE=1"}, InitializeTimeout: 13 * time.Second, ShutdownTimeout: 7 * time.Second,
		Limits: types.Limits{EventBuffer: 2, FrameBytes: 1000, PendingControls: 3, ControlBuffer: 4, StderrBytes: 5, PendingPermissions: 6, PermissionWorkers: 7, PermissionHistory: 8},
	}
	o := OptionsFromTypes(config)
	if o.PermissionHandler != nil || !reflect.DeepEqual(o.Types(), config) {
		t.Fatalf("configuration changed: %#v", o)
	}
	o.PermissionHandler = func(*PermissionRequest) {}
	o.Subagents = SubagentOptions{MaxNodes: 7, History: []SubagentHistory{{SessionID: "s", TaskID: "a"}}}
	if !reflect.DeepEqual(o.Types(), config) {
		t.Fatal("callback affected pure configuration")
	}
	if OptionsFromTypes(types.Options{}).Env != nil || OptionsFromTypes(types.Options{Env: []string{}}).Env == nil {
		t.Fatal("nil versus empty environment lost")
	}
}

func TestOptionsAdapterRunsExistingSubprocessProtocol(t *testing.T) {
	// Exercise the existing isolated initialize/send/receive protocol via the adapter.
	o := OptionsFromTypes(fixtureOptions(t, "echo").Types())
	c, err := Open(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	send(t, c, "adapter")
	var event types.Event = receive(t, c)
	var echo struct {
		Wire struct {
			Type    string
			Message struct{ Role, Content string }
		}
	}
	if err := json.Unmarshal(event.Raw, &echo); err != nil {
		t.Fatal(err)
	}
	if echo.Wire.Type != "user" || echo.Wire.Message.Role != "user" || echo.Wire.Message.Content != "adapter" {
		t.Fatalf("adapter send frame = %s", event.Raw)
	}
}

// Both public entry points own the same codec, including constructed frames.
func TestMessageCodecAliases(t *testing.T) {
	frame := []byte(`{"type":"result","subtype":"success","is_error":true,"modelUsage":{"fixture":{"inputTokens":0}},"api_error_status":429}`)
	root, err := ParseEvent(frame)
	if err != nil {
		t.Fatal(err)
	}
	data, err := types.ParseEvent(frame)
	if err != nil {
		t.Fatal(err)
	}
	var same Event = data
	same.Result.ModelUsage["fixture"] = ModelUsage{InputTokens: 3, Fields: map[string]FieldState{"inputTokens": Present}}
	b, err := json.Marshal(same)
	if err != nil {
		t.Fatal(err)
	}
	var decoded types.Event
	if err = json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if !root.Result.IsError || decoded.Result.ModelUsage["fixture"].InputTokens != 3 {
		t.Fatal("root/type codecs differ")
	}
}

func TestPermissionDataAliases(t *testing.T) {
	var _ func() (types.PermissionMetadata, error) = (*PermissionRequest)(nil).Metadata
	values := [][2]any{
		{PermissionMetadata{}, types.PermissionMetadata{}},
		{PermissionResult{}, types.PermissionResult{}},
		{ControlResponse{}, types.ControlResponse{}},
		{ControlResponseData{}, types.ControlResponseData{}},
		{AskUserQuestionInput{}, types.AskUserQuestionInput{}},
		{Question{}, types.Question{}},
		{QuestionOption{}, types.QuestionOption{}},
		{QuestionAnswer{}, types.QuestionAnswer{}},
	}
	for _, pair := range values {
		if reflect.TypeOf(pair[0]) != reflect.TypeOf(pair[1]) {
			t.Fatal("alias identity changed")
		}
	}
	frame := []byte(`{"type":"control_response","response":{"subtype":"success","request_id":"r","response":{"behavior":"deny","message":"no"}}}`)
	root, err := ParseEvent(frame)
	if err != nil {
		t.Fatal(err)
	}
	data, err := types.ParseEvent(frame)
	if err != nil || !reflect.DeepEqual(root, data) || root.Response == nil {
		t.Fatal("entry point mismatch", err)
	}
}
