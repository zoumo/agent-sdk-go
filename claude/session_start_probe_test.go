// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/claude/types"
)

func TestSessionStartProbePreservesCallerHooks(t *testing.T) {
	hooks := json.RawMessage(`{
		"PreToolUse": [{"matcher": "Read", "hookCallbackIds": ["caller-tool"], "timeout": 12, "future": {"keep": [1, true]}}],
		"SessionStart": [{"matcher": "startup", "hookCallbackIds": ["caller-start"], "future": "keep"}],
		"Stop": [{"matcher": null, "hookCallbackIds": ["caller-stop"]}],
		"FutureEvent": [{"hookCallbackIds": []}]
	}`)
	probe := SessionStartProbe{Source: "resume", CallbackID: "agent-sdk-go:test-probe"}
	request := captureProbeInitialization(t, &types.InitializeOptions{Hooks: hooks}, probe)

	require.JSONEq(t, `{
		"PreToolUse": [{"matcher": "Read", "hookCallbackIds": ["caller-tool"], "timeout": 12, "future": {"keep": [1, true]}}],
		"SessionStart": [
			{"matcher": "startup", "hookCallbackIds": ["caller-start"], "future": "keep"},
			{"matcher": "resume", "hookCallbackIds": ["agent-sdk-go:test-probe"]}
		],
		"Stop": [{"matcher": null, "hookCallbackIds": ["caller-stop"]}],
		"FutureEvent": [{"hookCallbackIds": []}]
	}`, string(request["hooks"]))
	require.NotContains(t, request, "forwardSubagentText")
}

func TestNilSessionStartProbeLeavesHooksUnchanged(t *testing.T) {
	hooks := json.RawMessage(`{
		"SessionStart": [{"matcher": "startup", "hookCallbackIds": ["caller-start"], "timeout": 3}],
		"FutureEvent": [{"hookCallbackIds": []}]
	}`)
	opts := fixtureOptions(t, "echo")
	opts.Initialize = &types.InitializeOptions{Hooks: hooks}
	capture := filepath.Join(opts.WorkDir, "capture.json")
	opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
	session, err := Open(context.Background(), SessionOptions(opts))
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Abort() })

	data, err := os.ReadFile(capture)
	require.NoError(t, err)
	var captured struct {
		Init struct {
			Request struct {
				Hooks json.RawMessage `json:"hooks"`
			} `json:"request"`
		} `json:"init"`
	}
	require.NoError(t, json.Unmarshal(data, &captured))
	require.JSONEq(t, string(hooks), string(captured.Init.Request.Hooks))
}

func TestSessionStartProbeHandlesNilAndNullHooks(t *testing.T) {
	probe := SessionStartProbe{Source: "resume", CallbackID: "agent-sdk-go:test-probe"}
	for _, tc := range []struct {
		name       string
		initialize *types.InitializeOptions
		forward    bool
	}{
		{name: "nil initialize", forward: true},
		{name: "nil hooks", initialize: &types.InitializeOptions{}},
		{name: "null hooks", initialize: &types.InitializeOptions{Hooks: json.RawMessage(`null`)}},
		{name: "empty hooks", initialize: &types.InitializeOptions{Hooks: json.RawMessage(`{}`)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := captureProbeInitialization(t, tc.initialize, probe)
			require.JSONEq(t, `{
				"SessionStart": [{"matcher": "resume", "hookCallbackIds": ["agent-sdk-go:test-probe"]}]
			}`, string(request["hooks"]))
			if tc.forward {
				require.JSONEq(t, `true`, string(request["forwardSubagentText"]))
			} else {
				require.NotContains(t, request, "forwardSubagentText")
			}
		})
	}
}

func TestSessionStartProbeRejectsInvalidHooksBeforeStart(t *testing.T) {
	for _, tc := range []struct {
		name  string
		hooks string
	}{
		{name: "top-level array", hooks: `[]`},
		{name: "top-level string", hooks: `"hooks"`},
		{name: "event object", hooks: `{"Stop":{}}`},
		{name: "null event", hooks: `{"Stop":null}`},
		{name: "null matcher entry", hooks: `{"Stop":[null]}`},
		{name: "missing callback IDs", hooks: `{"Stop":[{}]}`},
		{name: "null callback IDs", hooks: `{"Stop":[{"hookCallbackIds":null}]}`},
		{name: "non-array callback IDs", hooks: `{"Stop":[{"hookCallbackIds":"caller"}]}`},
		{name: "non-string callback ID", hooks: `{"Stop":[{"hookCallbackIds":[7]}]}`},
		{name: "non-string matcher", hooks: `{"Stop":[{"matcher":7,"hookCallbackIds":[]}]}`},
		{name: "non-numeric timeout", hooks: `{"Stop":[{"hookCallbackIds":[],"timeout":"slow"}]}`},
		{name: "reserved callback ID", hooks: `{"Stop":[{"hookCallbackIds":["agent-sdk-go:caller"]}]}`},
		{name: "duplicate member", hooks: `{"Stop":[{"hookCallbackIds":[],"hookCallbackIds":["caller"]}]}`},
		{name: "malformed JSON", hooks: `{"Stop":[`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertProbeRejectedBeforeStart(t,
				&types.InitializeOptions{Hooks: json.RawMessage(tc.hooks)},
				SessionStartProbe{Source: "resume", CallbackID: "agent-sdk-go:test-probe"})
		})
	}
}

func TestSessionStartProbeRejectsInvalidOptionBeforeStart(t *testing.T) {
	for _, tc := range []struct {
		name  string
		probe SessionStartProbe
	}{
		{name: "empty source", probe: SessionStartProbe{CallbackID: "agent-sdk-go:test-probe"}},
		{name: "invalid source UTF-8", probe: SessionStartProbe{Source: string([]byte{0xff}), CallbackID: "agent-sdk-go:test-probe"}},
		{name: "empty callback ID", probe: SessionStartProbe{Source: "resume"}},
		{name: "unreserved callback ID", probe: SessionStartProbe{Source: "resume", CallbackID: "caller"}},
		{name: "invalid callback ID UTF-8", probe: SessionStartProbe{Source: "resume", CallbackID: "agent-sdk-go:" + string([]byte{0xff})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertProbeRejectedBeforeStart(t, nil, tc.probe)
		})
	}
}

func TestSessionStartProbeAnswersMatchingCallback(t *testing.T) {
	probe := SessionStartProbe{Source: "resume", CallbackID: "agent-sdk-go:test-probe"}
	opts := fixtureOptions(t, "session_start_probe")
	opts.Initialize = &types.InitializeOptions{Hooks: json.RawMessage(`{"Stop":[{"hookCallbackIds":["caller-stop"]}]}`)}
	opts.SessionStartProbe = &probe
	session, err := Open(context.Background(), SessionOptions(opts))
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Abort() })

	for _, subtype := range []string{"hook_started", "hook_progress", "hook_response"} {
		received := sessionNative(t, session)
		require.Nil(t, received.Request)
		require.NotNil(t, received.Event.System)
		require.Equal(t, subtype, received.Event.Subtype)
		var lifecycle struct {
			HookName string `json:"hook_name"`
		}
		require.NoError(t, json.Unmarshal(received.Event.Raw, &lifecycle))
		require.Equal(t, "SessionStart:resume", lifecycle.HookName)
	}

	received := sessionNative(t, session)
	require.Equal(t, ReceivedNative, received.Kind)
	require.Nil(t, received.Request)
	require.Nil(t, received.Params)
	require.NotNil(t, received.Event.Control)
	require.Equal(t, "hook_callback", received.Event.Control.Subtype)
	var callback struct {
		CallbackID string          `json:"callback_id"`
		Input      json.RawMessage `json:"input"`
	}
	require.NoError(t, json.Unmarshal(received.Event.Control.Request, &callback))
	require.Equal(t, probe.CallbackID, callback.CallbackID)
	require.JSONEq(t, `{
		"hook_event_name": "SessionStart",
		"source": "resume",
		"session_id": "native-resume",
		"cwd": "/synthetic/project",
		"transcript_path": "/synthetic/transcript.jsonl",
		"future": {"preserve": true}
	}`, string(callback.Input))

	response := sessionNative(t, session)
	require.NotNil(t, response.Event)
	require.Equal(t, "reply_seen", eventKind(*response.Event))
	var echoed struct {
		Wire map[string]any `json:"wire"`
	}
	require.NoError(t, json.Unmarshal(response.Event.Raw, &echoed))
	require.Equal(t, map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": "probe-callback",
			"response":   map[string]any{},
		},
	}, echoed.Wire)
}

func TestSessionStartProbeLeavesDifferentCallbackIDUnsupported(t *testing.T) {
	probe := SessionStartProbe{Source: "resume", CallbackID: "agent-sdk-go:test-probe"}
	c := openFixture(t, "session_start_probe_wrong", func(opts *Options) {
		opts.SessionStartProbe = &probe
	})

	event := receive(t, c)
	require.NotNil(t, event.Control)
	require.Equal(t, "hook_callback", event.Control.Subtype)
	reply := receive(t, c)
	require.Equal(t, "reply_seen", eventKind(reply))
	var echoed struct {
		Wire map[string]any `json:"wire"`
	}
	require.NoError(t, json.Unmarshal(reply.Raw, &echoed))
	require.Equal(t, map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "error",
			"request_id": "wrong-callback",
			"error":      "Unsupported control request subtype: hook_callback",
		},
	}, echoed.Wire)
}

func TestSessionStartProbeAnswersMatchingCallbackAtMostOnce(t *testing.T) {
	probe := SessionStartProbe{Source: "resume", CallbackID: "agent-sdk-go:test-probe"}
	opts := fixtureOptions(t, "session_start_probe_duplicate")
	opts.SessionStartProbe = &probe
	session, err := Open(context.Background(), SessionOptions(opts))
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Abort() })

	first := sessionNative(t, session)
	require.NotNil(t, first.Event.Control)
	require.Equal(t, "hook_callback", first.Event.Control.Subtype)
	reply := sessionNative(t, session)
	require.Equal(t, "reply_seen", eventKind(*reply.Event))

	second := sessionNative(t, session)
	require.NotNil(t, second.Event.Control)
	require.Equal(t, "hook_callback", second.Event.Control.Subtype)
	marker := sessionNative(t, session)
	require.Equal(t, "probe_response_count", eventKind(*marker.Event))
	var counted struct {
		Responses int `json:"responses"`
	}
	require.NoError(t, json.Unmarshal(marker.Event.Raw, &counted))
	require.Equal(t, 1, counted.Responses)
}

func captureProbeInitialization(t *testing.T, initialize *types.InitializeOptions, probe SessionStartProbe) map[string]json.RawMessage {
	t.Helper()
	opts := fixtureOptions(t, "echo")
	opts.Initialize = initialize
	opts.SessionStartProbe = &probe
	capture := filepath.Join(opts.WorkDir, "capture.json")
	opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
	session, err := Open(context.Background(), SessionOptions(opts))
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Abort() })

	data, err := os.ReadFile(capture)
	require.NoError(t, err)
	var captured struct {
		Init struct {
			Request map[string]json.RawMessage `json:"request"`
		} `json:"init"`
	}
	require.NoError(t, json.Unmarshal(data, &captured))
	require.JSONEq(t, `"initialize"`, string(captured.Init.Request["subtype"]))
	return captured.Init.Request
}

func assertProbeRejectedBeforeStart(t *testing.T, initialize *types.InitializeOptions, probe SessionStartProbe) {
	t.Helper()
	opts := fixtureOptions(t, "echo")
	opts.Initialize = initialize
	opts.SessionStartProbe = &probe
	capture := filepath.Join(opts.WorkDir, "started.json")
	opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
	session, err := Open(context.Background(), SessionOptions(opts))
	if session != nil {
		_ = session.Abort()
	}
	require.ErrorIs(t, err, ErrInvalidInput)
	var dispatch *DispatchError
	require.ErrorAs(t, err, &dispatch)
	require.Equal(t, NotSent, dispatch.State)
	require.NoFileExists(t, capture)
}

func runSessionStartProbeFixture(reader *bufio.Reader, init map[string]any, mode string) {
	callbackID := fixtureProbeCallbackID(init)
	if callbackID == "" {
		os.Exit(30)
	}
	wrongCallback := mode == "session_start_probe_wrong"
	if mode == "session_start_probe" {
		for _, subtype := range []string{"hook_started", "hook_progress", "hook_response"} {
			fixtureEmit(map[string]any{
				"type":       "system",
				"subtype":    subtype,
				"session_id": "native-resume",
				"hook_id":    "probe-hook",
				"hook_name":  "SessionStart:resume",
			})
		}
	}
	requestID, requestCallbackID := "probe-callback", callbackID
	if wrongCallback {
		requestID, requestCallbackID = "wrong-callback", "different"
	}
	emitCallback := func(requestID, callbackID string) {
		fixtureEmit(map[string]any{
			"type":       "control_request",
			"request_id": requestID,
			"request": map[string]any{
				"subtype":     "hook_callback",
				"callback_id": callbackID,
				"input": map[string]any{
					"hook_event_name": "SessionStart",
					"source":          "resume",
					"session_id":      "native-resume",
					"cwd":             "/synthetic/project",
					"transcript_path": "/synthetic/transcript.jsonl",
					"future":          map[string]any{"preserve": true},
				},
			},
		})
	}
	emitCallback(requestID, requestCallbackID)

	type readResult struct {
		line []byte
		err  error
	}
	lines := make(chan readResult, 1)
	go func() {
		for {
			line, err := reader.ReadBytes('\n')
			lines <- readResult{line: line, err: err}
			if err != nil {
				return
			}
		}
	}()
	responses := 0
	handle := func(result readResult) bool {
		if result.err != nil {
			return false
		}
		var wire struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(result.line, &wire) != nil {
			os.Exit(31)
		}
		if wire.Type == "control_response" {
			responses++
			fixtureEmit(map[string]any{"type": "reply_seen", "session_id": "native-resume", "wire": json.RawMessage(result.line)})
		}
		return true
	}

	if mode != "session_start_probe_duplicate" {
		for {
			if !handle(<-lines) {
				return
			}
		}
	}

	firstResponse := time.NewTimer(2 * time.Second)
	defer firstResponse.Stop()
	for responses == 0 {
		select {
		case result := <-lines:
			if !handle(result) {
				return
			}
		case <-firstResponse.C:
			fixtureEmit(map[string]any{
				"type": "system", "subtype": "status", "session_id": "native-resume",
				"fixture_kind": "probe_response_count", "responses": responses,
			})
			return
		}
	}
	emitCallback("probe-callback-2", callbackID)

	secondResponse := time.NewTimer(300 * time.Millisecond)
	defer secondResponse.Stop()
	for {
		select {
		case result := <-lines:
			if !handle(result) {
				return
			}
		case <-secondResponse.C:
			fixtureEmit(map[string]any{
				"type": "system", "subtype": "status", "session_id": "native-resume",
				"fixture_kind": "probe_response_count", "responses": responses,
			})
			return
		}
	}
}

func fixtureProbeCallbackID(init map[string]any) string {
	data, err := json.Marshal(init)
	if err != nil {
		return ""
	}
	var wire struct {
		Request struct {
			Hooks map[string][]struct {
				HookCallbackIDs []string `json:"hookCallbackIds"`
			} `json:"hooks"`
		} `json:"request"`
	}
	if json.Unmarshal(data, &wire) != nil {
		return ""
	}
	for _, matcher := range wire.Request.Hooks["SessionStart"] {
		for _, callbackID := range matcher.HookCallbackIDs {
			if strings.HasPrefix(callbackID, sessionStartProbeReservedPrefix) {
				return callbackID
			}
		}
	}
	return ""
}
