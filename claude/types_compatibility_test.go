// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude/types"
)

// Function signatures require identical types, not merely convertible structs.
var (
	_ func(context.Context) (ReceivedEvent, error)  = (*Client)(nil).Receive
	_ func(context.Context, PermissionAnswer) error = (*PermissionRequest)(nil).Answer
	_ types.Event                                   = Event{
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
	for _, tc := range []struct {
		name     string
		decorate bool
	}{{"plain", false}, {"callback and observation options", true}} {
		t.Run(tc.name, func(t *testing.T) {
			o := OptionsFromTypes(config)
			require.Nil(t, o.PermissionHandler)
			assert.Equal(t, config, o.Types(), "configuration changed")
			if tc.decorate {
				o.PermissionHandler = func(*PermissionRequest) {}
				o.Subagents = SubagentOptions{MaxNodes: 7, History: []SubagentHistory{{SessionID: "s", TaskID: "a"}}}
			}
			assert.Equal(t, config, o.Types(), "callback affected pure configuration")
		})
	}
	for _, tc := range []struct {
		name string
		env  []string
	}{{"nil", nil}, {"empty", []string{}}} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.env, OptionsFromTypes(types.Options{Env: tc.env}).Env, "nil versus empty environment lost")
		})
	}
}

func (s *clientProtocolSuite) TestOptionsAdapterRunsExistingSubprocessProtocol() {
	t := s.T()
	// Exercise the existing isolated initialize/send/receive protocol via the adapter.
	o := OptionsFromTypes(fixtureOptions(t, "echo").Types())
	c, err := NewClient(context.Background(), o)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Abort() })
	send(t, c, "adapter")
	var event types.Event = receive(t, c)
	var echo struct {
		Wire struct {
			Type    string
			Message struct{ Role, Content string }
		}
	}
	{
		err := json.Unmarshal(event.Raw, &echo)
		require.NoError(t, err)
	}
	require.Falsef(t, echo.Wire.Type != "user" || echo.Wire.Message.Role != "user" || echo.Wire.Message.Content != "adapter", "adapter send frame = %s", event.Raw)
}

// Both public entry points own the same codec, including constructed frames.
func TestMessageCodecAliases(t *testing.T) {
	for _, tc := range []struct{ name, frame string }{{"result model usage", `{"type":"result","subtype":"success","is_error":true,"modelUsage":{"fixture":{"inputTokens":0}},"api_error_status":429}`}} {
		t.Run(tc.name, func(t *testing.T) {
			frame := []byte(tc.frame)
			root, err := ParseEvent(frame)
			require.NoError(t, err)
			data, err := types.ParseEvent(frame)
			require.NoError(t, err)
			var same Event = data
			same.Result.ModelUsage["fixture"] = ModelUsage{InputTokens: 3, Fields: map[string]FieldState{"inputTokens": Present}}
			b, err := json.Marshal(same)
			require.NoError(t, err)
			var decoded types.Event
			{
				err = json.Unmarshal(b, &decoded)
				require.NoError(t, err)
			}
			assert.True(t, root.Result.IsError, "root/type codecs differ")
			assert.EqualValues(t, 3, decoded.Result.ModelUsage["fixture"].InputTokens, "root/type codecs differ")
		})
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
		t.Run(reflect.TypeOf(pair[0]).Name(), func(t *testing.T) {
			assert.Equal(t, reflect.TypeOf(pair[1]), reflect.TypeOf(pair[0]), "alias identity changed")
		})
	}
	frame := []byte(`{"type":"control_response","response":{"subtype":"success","request_id":"r","response":{"behavior":"deny","message":"no"}}}`)
	root, err := ParseEvent(frame)
	require.NoError(t, err)
	data, err := types.ParseEvent(frame)
	require.NoError(t, err)
	assert.Equal(t, root, data, "entry point mismatch")
	assert.NotNil(t, root.Response)
}
