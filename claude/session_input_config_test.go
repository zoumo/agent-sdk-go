// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude/types"
)

func TestSessionStructuredInputPreservesNativeWire(t *testing.T) {
	s, path := openInputConfigSession(t)
	var user types.UserInput
	raw := `{"message":{"role":"user","content":[{"type":"text","text":"hello","future":null},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="},"future":42},{"type":"document","source":{"type":"text","media_type":"text/plain","data":"doc"},"title":"title"},{"type":"tool_result","tool_use_id":"tool","content":[{"type":"text","text":"answer","extra":false}],"is_error":false}],"future_message":null},"uuid":"u","priority":"now","shouldQuery":false,"origin":{"kind":"user","extra":42},"timestamp":"now","tool_use_result":{"x":null},"future_envelope":[1,null]}`
	require.NoError(t, json.Unmarshal([]byte(raw), &user))
	receipt, err := s.Submit(context.Background(), Input{User: &user})
	require.NoError(t, err)
	assert.True(t, receipt.Written)
	require.NoError(t, s.Interrupt(context.Background(), InterruptOptions{}))
	require.NoError(t, s.Close())
	frames := permissionTranscript(t, path)
	require.Len(t, frames, 2)
	var want map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &want))
	want["type"], want["session_id"], want["parent_tool_use_id"] = "user", "", nil
	expected, err := json.Marshal(want)
	require.NoError(t, err)
	assert.JSONEq(t, string(expected), string(frames[0]))
}

// Synthetic peer validates transport facts only, never model acceptance.
func runInputConfigFixture(reader *bufio.Reader) {
	f, err := os.Create(os.Getenv("CLAUDE_GO_TRANSCRIPT"))
	if err != nil {
		os.Exit(12)
	}
	defer f.Close()
	fixtureEmit(map[string]any{"type": "system", "subtype": "init", "session_id": "main", "claude_code_version": "2.1.258"})
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		if _, err = f.Write(line); err != nil {
			os.Exit(12)
		}
		var wire map[string]any
		if json.Unmarshal(line, &wire) != nil {
			os.Exit(12)
		}
		if wire["type"] == "control_request" {
			request := wire["request"].(map[string]any)
			fixtureEmit(map[string]any{"type": "assistant", "session_id": "main", "message": map[string]any{"content": "control seen"}, "wire": wire})
			if request["model"] == "hang" {
				continue
			}
			if request["model"] == "reject" {
				sessionReply(wire["request_id"].(string), "native model rejected: exact reason")
				continue
			}
			fixtureEmit(map[string]any{"type": "control_response", "future_envelope": nil, "response": map[string]any{"subtype": "success", "request_id": wire["request_id"], "response": map[string]any{"future_ack": 42}, "future_response": false}})
		} else if wire["type"] == "user" {
			fixtureEmit(map[string]any{"type": "user", "session_id": "main", "message": wire["message"]})
			if wire["message"].(map[string]any)["content"] == "reject input" {
				fixtureEmit(map[string]any{"type": "result", "session_id": "main", "subtype": "error_during_execution", "is_error": true, "errors": []string{"native input rejected"}})
			}
		}
	}
}

func TestSessionNativeConfigurationWireAndEvidence(t *testing.T) {
	s, path := openInputConfigSession(t)
	model := "native-model"
	r, err := s.SetModel(context.Background(), ModelChange{Model: &model})
	require.NoError(t, err)
	assert.True(t, r.Accepted)
	assert.NotEmpty(t, r.Response)
	assert.Contains(t, string(r.Response), `"future_envelope":null`)
	assert.Contains(t, string(r.Response), `"future_response":false`)
	assert.Contains(t, string(r.Response), `"future_ack":42`)
	assert.Empty(t, r.Observed)
	assert.JSONEq(t, `{"subtype":"set_model","model":"native-model"}`, string(r.Requested))
	_, err = s.SetModel(context.Background(), ModelChange{})
	require.NoError(t, err)
	zero := 0
	display := "omitted"
	_, err = s.SetThinking(context.Background(), ThinkingChange{MaxThinkingTokens: &zero, ThinkingDisplay: &display})
	require.NoError(t, err)
	_, err = s.SetThinking(context.Background(), ThinkingChange{ClearDisplay: true})
	require.NoError(t, err)
	require.NoError(t, s.Close())
	frames := permissionTranscript(t, path)
	require.Len(t, frames, 4)
	wants := []string{`{"subtype":"set_model","model":"native-model"}`, `{"subtype":"set_model"}`, `{"subtype":"set_max_thinking_tokens","max_thinking_tokens":0,"thinking_display":"omitted"}`, `{"subtype":"set_max_thinking_tokens","max_thinking_tokens":null,"thinking_display":null}`}
	for i, frame := range frames {
		var w struct{ Request json.RawMessage }
		require.NoError(t, json.Unmarshal(frame, &w))
		assert.JSONEq(t, wants[i], string(w.Request))
	}
}

func TestSessionCreationThinkingIsSeparateFromDynamicControl(t *testing.T) {
	zero := 0
	display := "summarized"
	for _, tc := range []struct {
		name     string
		thinking *types.ThinkingConfig
		args     []string
	}{
		{"adaptive", &types.ThinkingConfig{Type: "adaptive", Display: &display}, []string{"--thinking=adaptive", "--thinking-display=summarized"}},
		{"enabled", &types.ThinkingConfig{Type: "enabled", BudgetTokens: &zero}, []string{"--max-thinking-tokens=0"}},
		{"enabled-adaptive", &types.ThinkingConfig{Type: "enabled"}, []string{"--thinking=adaptive"}},
		{"disabled", &types.ThinkingConfig{Type: "disabled"}, []string{"--thinking=disabled"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := SessionOptions(fixtureOptions(t, "input_config"))
			capture := filepath.Join(t.TempDir(), "capture.json")
			o.Env = append(o.Env, "CLAUDE_GO_CAPTURE="+capture, "CLAUDE_GO_TRANSCRIPT="+filepath.Join(t.TempDir(), "wire.jsonl"))
			o.Model = "native-create"
			o.Thinking = tc.thinking
			config := Options(o).Types()
			assert.Equal(t, tc.thinking, OptionsFromTypes(config).Thinking)
			s, err := Open(context.Background(), o)
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Abort() })
			data, err := os.ReadFile(capture)
			require.NoError(t, err)
			var got struct{ Argv []string }
			require.NoError(t, json.Unmarshal(data, &got))
			assert.Contains(t, got.Argv, "--model=native-create")
			for _, arg := range tc.args {
				assert.Contains(t, got.Argv, arg)
			}
			require.Len(t, got.Argv, 9+len(tc.args))
		})
	}
}

func openInputConfigSession(t *testing.T) (*Session, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wire.jsonl")
	o := SessionOptions(fixtureOptions(t, "input_config"))
	o.Env = append(o.Env, "CLAUDE_GO_TRANSCRIPT="+path)
	s, err := Open(context.Background(), o)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Abort() })
	return s, path
}

func openInputConfigPeer(t *testing.T) (*Session, string) {
	t.Helper()
	s, path := openInputConfigSession(t)
	require.Equal(t, "init", sessionNative(t, s).Event.Subtype)
	return s, path
}

func TestSessionInputAndConfigLocalRejectionsRemainUsable(t *testing.T) {
	s, path := openInputConfigPeer(t)
	for _, raw := range []string{
		`{"message":{"role":"user","content":[{"type":"thinking","thinking":"output only"}]}}`,
		`{"message":{"role":"user","content":[{"type":"future_block","x":null}]}}`,
		`{"message":{"role":"user","content":[{"type":"image","source":{"type":"future","data":42}}]}}`,
		`{"session_id":"child","message":{"role":"user","content":"x"}}`,
		`{"parent_tool_use_id":"child-tool","message":{"role":"user","content":"x"}}`,
		`{"message":{"role":"assistant","content":"x"}}`,
		`{"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":[{"type":"tool_use","id":"x"}]}]}}`,
	} {
		var u types.UserInput
		require.NoError(t, json.Unmarshal([]byte(raw), &u))
		_, err := s.Submit(context.Background(), Input{User: &u})
		var d *DispatchError
		require.ErrorAs(t, err, &d)
		assert.Equal(t, NotSent, d.State)
	}
	empty := ""
	invalid := "adaptive"
	negative := -1
	_, err := s.SetModel(context.Background(), ModelChange{Model: &empty})
	require.ErrorIs(t, err, ErrInvalidInput)
	_, err = s.SetThinking(context.Background(), ThinkingChange{ThinkingDisplay: &invalid})
	require.ErrorIs(t, err, ErrUnsupported)
	_, err = s.SetThinking(context.Background(), ThinkingChange{MaxThinkingTokens: &negative})
	require.ErrorIs(t, err, ErrInvalidInput)
	_, err = s.SetThinking(context.Background(), ThinkingChange{ThinkingDisplay: &invalid, ClearDisplay: true})
	require.ErrorIs(t, err, ErrInvalidInput)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.SetModel(ctx, ModelChange{})
	var d *DispatchError
	require.ErrorAs(t, err, &d)
	assert.Equal(t, NotSent, d.State)
	_, err = s.Submit(nil, Input{})
	require.Error(t, err)
	require.NoError(t, s.Err())
	submitFrame(t, s, "still usable")
	require.NoError(t, s.Close())
	require.Len(t, permissionTranscript(t, path), 1, "local rejection must not send or add operations")
}

func TestSessionInputDuringExecutionKeepsNativeAcceptanceSeparate(t *testing.T) {
	s, path := openInputConfigPeer(t)
	// No result/idle was received before either input or configuration.
	submitFrame(t, s, "first active input")
	assert.Equal(t, "first active input", *sessionNative(t, s).Event.User.Content.Text)
	r, err := s.SetModel(context.Background(), ModelChange{})
	require.NoError(t, err)
	assert.True(t, r.Accepted)
	assert.Empty(t, r.Observed)
	assert.Equal(t, "control seen", *sessionNative(t, s).Event.Assistant.Content.Text)
	submitFrame(t, s, "reject input")
	assert.Equal(t, "reject input", *sessionNative(t, s).Event.User.Content.Text)
	rejected := sessionNative(t, s)
	assert.True(t, rejected.Event.Result.IsError)
	require.NoError(t, s.Err())
	require.NoError(t, s.Close())
	require.Len(t, permissionTranscript(t, path), 3, "no interrupt, rebuild, child update or scheduler")
}

func TestSessionConfigurationNativeRejectionPreservesResponse(t *testing.T) {
	s, _ := openInputConfigPeer(t)
	model := "reject"
	r, err := s.SetModel(context.Background(), ModelChange{Model: &model})
	var native *ControlError
	require.ErrorAs(t, err, &native)
	assert.Equal(t, "native model rejected: exact reason", native.Message)
	assert.Equal(t, "set_model", native.Subtype)
	assert.Equal(t, native.Raw, r.Response)
	assert.Contains(t, string(r.Response), native.RequestID)
	assert.False(t, r.Accepted)
	assert.Empty(t, r.Observed)
	require.NoError(t, s.Err())
	_, err = s.SetModel(context.Background(), ModelChange{})
	require.NoError(t, err)
}

func TestSessionConfigurationCanceledAfterWriteIsUnknownWithoutRetry(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "timeout"}[timeout], func(t *testing.T) {
			s, path := openInputConfigPeer(t)
			var ctx context.Context
			var cancel context.CancelFunc
			if timeout {
				ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			model := "hang"
			result := make(chan error, 1)
			go func() { _, err := s.SetModel(ctx, ModelChange{Model: &model}); result <- err }()
			assert.Equal(t, "control seen", *sessionNative(t, s).Event.Assistant.Content.Text)
			if !timeout {
				cancel()
			}
			err := awaitPermissionError(t, result)
			var d *DispatchError
			require.ErrorAs(t, err, &d)
			assert.Equal(t, OutcomeUnknown, d.State)
			require.ErrorIs(t, err, ErrUnknownOutcome)
			require.NoError(t, s.Err())
			_, err = s.SetThinking(context.Background(), ThinkingChange{})
			require.NoError(t, err)
			require.NoError(t, s.Close())
			require.Len(t, permissionTranscript(t, path), 2)
		})
	}
}

func TestSessionRejectsInvalidUTF8BeforeEncoding(t *testing.T) {
	s, path := openInputConfigPeer(t)
	bad := string([]byte{0xff})
	text := types.UserInput{Message: types.InputMessage{Role: "user", Content: types.Content{Text: &bad}}}
	uuid := types.UserInput{UUID: &bad, Message: types.InputMessage{Role: "user", Content: types.Content{Text: new(string)}}}
	raw := types.UserInput{Raw: json.RawMessage(append([]byte(`{"unknown":"`), append([]byte{0xff}, []byte(`"}`)...)...)), Message: types.InputMessage{Role: "user", Content: types.Content{Text: new(string)}}}
	for _, input := range []Input{{Text: bad}, {User: &text}, {User: &uuid}, {User: &raw}} {
		_, err := s.Submit(context.Background(), input)
		var d *DispatchError
		require.ErrorAs(t, err, &d)
		assert.Equal(t, NotSent, d.State)
		require.ErrorIs(t, err, ErrInvalidInput)
	}
	_, err := s.SetModel(context.Background(), ModelChange{Model: &bad})
	var d *DispatchError
	require.ErrorAs(t, err, &d)
	assert.Equal(t, NotSent, d.State)
	require.ErrorIs(t, err, ErrInvalidInput)
	require.NoError(t, s.Err())
	submitFrame(t, s, "valid afterward")
	require.NoError(t, s.Close())
	require.Len(t, permissionTranscript(t, path), 1)
}

func TestSessionCreationInvalidThinkingDoesNotLaunch(t *testing.T) {
	budget := 1
	display := "omitted"
	bad := "future"
	for _, thinking := range []*types.ThinkingConfig{
		{Type: "future"}, {Type: "adaptive", BudgetTokens: &budget}, {Type: "disabled", Display: &display}, {Type: "enabled", Display: &bad},
	} {
		o := SessionOptions(fixtureOptions(t, "input_config"))
		capture := filepath.Join(t.TempDir(), "capture.json")
		o.Env = append(o.Env, "CLAUDE_GO_CAPTURE="+capture)
		o.Thinking = thinking
		s, err := Open(context.Background(), o)
		require.Nil(t, s)
		var d *DispatchError
		require.ErrorAs(t, err, &d)
		assert.Equal(t, NotSent, d.State)
		require.ErrorIs(t, err, ErrUnsupported)
		_, err = os.Stat(capture)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}
