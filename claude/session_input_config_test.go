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
			os.Exit(12) //nolint:gocritic // Fixture exits immediately; deferred cleanup is irrelevant.
		}
		var wire map[string]any
		if json.Unmarshal(line, &wire) != nil {
			os.Exit(12)
		}
		if wire["type"] == "control_request" {
			request := wire["request"].(map[string]any)
			fixtureEmit(map[string]any{"type": "assistant", "session_id": "main", "message": map[string]any{"content": "control seen"}, "wire": wire})
			if request["subtype"] == "get_settings" {
				mode := os.Getenv("CLAUDE_GO_SETTINGS_MODE")
				if mode == "reject_settings" {
					sessionReply(wire["request_id"].(string), "settings unavailable")
					continue
				}
				if mode == "hang_settings" {
					continue
				}
				applied := map[string]any{"model": "main", "effort": "medium", "advisor": nil, "ultracode": true}
				switch mode {
				case "missing_ultracode":
					delete(applied, "ultracode")
				case "invalid_ultracode":
					applied["ultracode"] = "true"
				case "ultracode_false":
					applied["ultracode"] = false
				}
				fixtureEmit(map[string]any{"type": "control_response", "response": map[string]any{
					"subtype":    "success",
					"request_id": wire["request_id"],
					"response": map[string]any{
						"effective": map[string]any{"effortLevel": "medium", "fastMode": true},
						"sources": []any{
							map[string]any{"source": "flagSettings", "settings": map[string]any{"fastMode": true}},
						},
						"applied": applied,
					},
				}})
				continue
			}
			if request["model"] == "exact-keys" {
				fixtureEmit(map[string]any{"type": "control_response", "TYPE": "future", "response": map[string]any{"subtype": "success", "SUBTYPE": "error", "request_id": wire["request_id"], "REQUEST_ID": "wrong", "response": map[string]any{"exact": true}, "RESPONSE": map[string]any{"wrong": true}}, "RESPONSE": map[string]any{"subtype": "error", "request_id": wire["request_id"], "error": "wrong casing"}})
				continue
			}
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

func TestSessionEffortUsesMergedFlagSettingsWithoutBudgetMapping(t *testing.T) {
	s, path := openInputConfigSession(t)
	high := "high"
	receipt, err := s.SetEffort(context.Background(), EffortChange{Level: &high})
	require.NoError(t, err)
	assert.True(t, receipt.Accepted)
	assert.NotEmpty(t, receipt.Response)
	assert.Contains(t, string(receipt.Response), `"future_ack":42`)
	assert.Empty(t, receipt.Observed)
	assert.JSONEq(t, `{"subtype":"apply_flag_settings","settings":{"effortLevel":"high","ultracode":false}}`, string(receipt.Requested))

	cleared, err := s.SetEffort(context.Background(), EffortChange{})
	require.NoError(t, err)
	assert.True(t, cleared.Accepted)
	assert.JSONEq(t, `{"subtype":"apply_flag_settings","settings":{"effortLevel":null,"ultracode":false}}`, string(cleared.Requested))
	require.NoError(t, s.Close())

	frames := permissionTranscript(t, path)
	require.Len(t, frames, 4)
	want := []struct {
		subtype  string
		settings string
	}{
		{subtype: "get_settings"},
		{subtype: "apply_flag_settings", settings: `{"effortLevel":"high","ultracode":false}`},
		{subtype: "get_settings"},
		{subtype: "apply_flag_settings", settings: `{"effortLevel":null,"ultracode":false}`},
	}
	for i, frame := range frames {
		var wire struct {
			Request struct {
				Subtype  string          `json:"subtype"`
				Settings json.RawMessage `json:"settings"`
			} `json:"request"`
		}
		require.NoError(t, json.Unmarshal(frame, &wire))
		assert.Equal(t, want[i].subtype, wire.Request.Subtype)
		if want[i].settings != "" {
			assert.JSONEq(t, want[i].settings, string(wire.Request.Settings))
		}
	}
	for _, frame := range frames {
		assert.NotContains(t, string(frame), "set_max_thinking_tokens")
	}
}

func TestSessionEffortKeepsUltracodeOnlyWhenRequestedModeStillUsesIt(t *testing.T) {
	high, xhigh, ultracode := "high", "xhigh", "ultracode"
	tests := []struct {
		name      string
		mode      string
		level     *string
		wantLevel any
		wantUltra bool
	}{
		{
			name:      "different level closes active ultracode",
			mode:      "ultracode_true",
			level:     &high,
			wantLevel: "high",
			wantUltra: false,
		},
		{
			name:      "clear closes active ultracode",
			mode:      "ultracode_true",
			wantLevel: nil,
			wantUltra: false,
		},
		{
			name:      "xhigh keeps compatible ultracode",
			mode:      "ultracode_true",
			level:     &xhigh,
			wantLevel: "xhigh",
			wantUltra: true,
		},
		{
			name:      "ultracode literal enables workflows",
			mode:      "ultracode_false",
			level:     &ultracode,
			wantLevel: "ultracode",
			wantUltra: true,
		},
		{
			name:      "inactive ultracode stays closed",
			mode:      "ultracode_false",
			level:     &high,
			wantLevel: "high",
			wantUltra: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s, path := openInputConfigSessionWithEnv(
				t,
				"CLAUDE_GO_SETTINGS_MODE="+test.mode,
			)
			_, err := s.SetEffort(context.Background(), EffortChange{Level: test.level})
			require.NoError(t, err)
			require.NoError(t, s.Close())

			frames := permissionTranscript(t, path)
			require.Len(t, frames, 2)
			var apply struct {
				Request struct {
					Subtype  string         `json:"subtype"`
					Settings map[string]any `json:"settings"`
				} `json:"request"`
			}
			require.NoError(t, json.Unmarshal(frames[1], &apply))
			require.Equal(t, "apply_flag_settings", apply.Request.Subtype)
			assert.Equal(t, test.wantLevel, apply.Request.Settings["effortLevel"])
			assert.Equal(t, test.wantUltra, apply.Request.Settings["ultracode"])
		})
	}
}

func TestSessionEffortRequiresConfirmableFlagState(t *testing.T) {
	for _, mode := range []string{
		"missing_ultracode",
		"invalid_ultracode",
		"reject_settings",
	} {
		t.Run(mode, func(t *testing.T) {
			s, path := openInputConfigSessionWithEnv(t, "CLAUDE_GO_SETTINGS_MODE="+mode)
			high := "high"
			receipt, err := s.SetEffort(context.Background(), EffortChange{Level: &high})
			require.ErrorIs(t, err, ErrUnsupported)
			if mode == "reject_settings" {
				var controlErr *ControlError
				require.ErrorAs(t, err, &controlErr)
			}
			var dispatch *DispatchError
			require.ErrorAs(t, err, &dispatch)
			assert.Equal(t, NotSent, dispatch.State)
			assert.False(t, receipt.Accepted)
			assert.Empty(t, receipt.Requested)
			assert.Empty(t, receipt.Response)
			require.NoError(t, s.Close())

			frames := permissionTranscript(t, path)
			require.Len(t, frames, 1)
			var wire struct {
				Request struct {
					Subtype string `json:"subtype"`
				} `json:"request"`
			}
			require.NoError(t, json.Unmarshal(frames[0], &wire))
			assert.Equal(t, "get_settings", wire.Request.Subtype)
		})
	}
}

func TestSessionEffortPreflightTimeoutIsUnsupportedNotSent(t *testing.T) {
	s, path := openInputConfigSessionWithEnv(t, "CLAUDE_GO_SETTINGS_MODE=hang_settings")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	high := "high"
	receipt, err := s.SetEffort(ctx, EffortChange{Level: &high})
	require.ErrorIs(t, err, ErrUnsupported)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NotErrorIs(t, err, ErrUnknownOutcome)
	var nativeErr *Error
	require.ErrorAs(t, err, &nativeErr)
	assert.True(t, nativeErr.OutcomeUnknown)
	var dispatch *DispatchError
	require.ErrorAs(t, err, &dispatch)
	assert.Equal(t, NotSent, dispatch.State)
	assert.False(t, receipt.Accepted)
	assert.Empty(t, receipt.Requested)
	assert.Empty(t, receipt.Response)
	require.NoError(t, s.Close())

	frames := permissionTranscript(t, path)
	require.Len(t, frames, 1)
	var wire struct {
		Request struct {
			Subtype string `json:"subtype"`
		} `json:"request"`
	}
	require.NoError(t, json.Unmarshal(frames[0], &wire))
	assert.Equal(t, "get_settings", wire.Request.Subtype)
}

func TestSessionPermissionModesUseTypedWireValues(t *testing.T) {
	s, path := openInputConfigSession(t)
	modes := []PermissionMode{
		PermissionModeDefault,
		PermissionModeAcceptEdits,
		PermissionModeBypassPermissions,
		PermissionModePlan,
		PermissionModeDontAsk,
		PermissionModeAuto,
	}
	for _, mode := range modes {
		receipt, err := s.SetPermissionMode(context.Background(), mode)
		require.NoError(t, err)
		require.True(t, receipt.Accepted)
		require.JSONEq(t, `{"subtype":"set_permission_mode","mode":"`+string(mode)+`"}`, string(receipt.Requested))
	}
	for _, mode := range []PermissionMode{"", "manual", "future"} {
		_, err := s.SetPermissionMode(context.Background(), mode)
		var dispatch *DispatchError
		require.ErrorAs(t, err, &dispatch)
		require.Equal(t, NotSent, dispatch.State)
		require.ErrorIs(t, err, ErrInvalidInput)
	}
	require.NoError(t, s.Close())
	frames := permissionTranscript(t, path)
	require.Len(t, frames, len(modes))
	for i, mode := range modes {
		var frame struct{ Request json.RawMessage }
		require.NoError(t, json.Unmarshal(frames[i], &frame))
		require.JSONEq(t, `{"subtype":"set_permission_mode","mode":"`+string(mode)+`"}`, string(frame.Request))
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
	return openInputConfigSessionWithEnv(t)
}

func openInputConfigSessionWithEnv(t *testing.T, extraEnv ...string) (*Session, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wire.jsonl")
	o := SessionOptions(fixtureOptions(t, "input_config"))
	o.Env = append(o.Env, "CLAUDE_GO_TRANSCRIPT="+path)
	o.Env = append(o.Env, extraEnv...)
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
	_, err = s.SetEffort(context.Background(), EffortChange{Level: &empty})
	require.ErrorIs(t, err, ErrInvalidInput)
	badEffort := string([]byte{0xff})
	_, err = s.SetEffort(context.Background(), EffortChange{Level: &badEffort})
	require.ErrorIs(t, err, ErrInvalidInput)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.SetModel(ctx, ModelChange{})
	var d *DispatchError
	require.ErrorAs(t, err, &d)
	assert.Equal(t, NotSent, d.State)
	_, err = s.Submit(nil, Input{}) //nolint:staticcheck // verifies nil-context rejection.
	require.Error(t, err)
	require.NoError(t, s.Err())
	submitFrame(t, s, "still usable")
	require.NoError(t, s.Close())
	_, err = s.SetEffort(context.Background(), EffortChange{})
	var closed *DispatchError
	require.ErrorAs(t, err, &closed)
	assert.Equal(t, NotSent, closed.State)
	require.ErrorIs(t, err, ErrClosed)
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
				// Peer reception alone can precede local Write returning. This
				// acknowledged control traverses the same serialized writer and
				// proves its earlier cancellation hook has been stopped.
				require.NoError(t, s.Interrupt(context.Background(), InterruptOptions{}))
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
			want := []string{"set_model", "set_max_thinking_tokens"}
			if !timeout {
				want = []string{"set_model", "interrupt", "set_max_thinking_tokens"}
			}
			frames := permissionTranscript(t, path)
			require.Len(t, frames, len(want))
			for i, raw := range frames {
				var wire struct{ Request struct{ Subtype string } }
				require.NoError(t, json.Unmarshal(raw, &wire))
				require.Equal(t, want[i], wire.Request.Subtype, "unexpected retry or extra operation")
			}
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

func TestSessionStructuredInputExactKeys(t *testing.T) {
	s, path := openInputConfigSession(t)
	raw := `{"message":{"role":"user","ROLE":"assistant","content":"exact","CONTENT":"wrong"},"TYPE":"future","SESSION_ID":"other","shouldQuery":null,"SHOULDQUERY":true}`
	var user types.UserInput
	require.NoError(t, json.Unmarshal([]byte(raw), &user))
	receipt, err := s.Submit(context.Background(), Input{User: &user})
	require.NoError(t, err)
	require.True(t, receipt.Written)
	require.NoError(t, s.Interrupt(context.Background(), InterruptOptions{}))
	require.NoError(t, s.Close())
	frames := permissionTranscript(t, path)
	require.Len(t, frames, 2)
	require.JSONEq(t, `{"message":{"role":"user","ROLE":"assistant","content":"exact","CONTENT":"wrong"},"TYPE":"future","SESSION_ID":"other","shouldQuery":null,"SHOULDQUERY":true,"type":"user","session_id":"","parent_tool_use_id":null}`, string(frames[0]))
}

func TestSessionConfigurationExactResponseKeys(t *testing.T) {
	s, _ := openInputConfigSession(t)
	model := "exact-keys"
	receipt, err := s.SetModel(context.Background(), ModelChange{Model: &model})
	require.NoError(t, err)
	require.True(t, receipt.Accepted)
	require.Contains(t, string(receipt.Response), `"REQUEST_ID":"wrong"`)
	require.Contains(t, string(receipt.Response), `"error":"wrong casing"`)
	require.NoError(t, s.Close())
}

func TestSessionWrongCaseInputCannotSupplyRequiredFields(t *testing.T) {
	s, path := openInputConfigSession(t)
	for _, raw := range []string{
		`{"message":{"ROLE":"user","content":"x"}}`,
		`{"message":{"role":"user","CONTENT":"x"}}`,
		`{"MESSAGE":{"role":"user","content":"x"}}`,
	} {
		var user types.UserInput
		require.NoError(t, json.Unmarshal([]byte(raw), &user))
		_, err := s.Submit(context.Background(), Input{User: &user})
		var dispatch *DispatchError
		require.ErrorAs(t, err, &dispatch)
		require.Equal(t, NotSent, dispatch.State)
		require.ErrorIs(t, err, ErrInvalidInput)
		require.NoError(t, s.Err())
	}
	receipt, err := s.Submit(context.Background(), Input{Text: "still usable"})
	require.NoError(t, err)
	require.True(t, receipt.Written)
	require.NoError(t, s.Interrupt(context.Background(), InterruptOptions{}))
	require.NoError(t, s.Close())
	frames := permissionTranscript(t, path)
	require.Len(t, frames, 2)
	require.Contains(t, string(frames[0]), `"content":"still usable"`)
}
