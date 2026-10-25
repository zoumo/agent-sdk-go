// SPDX-License-Identifier: Apache-2.0
package recorder_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/claude/tools/mock"
	"github.com/zoumo/agent-sdk-go/claude/tools/recorder"
)

func TestMain(m *testing.M) {
	if path := os.Getenv(mock.ScenarioEnvironment); path != "" {
		os.Exit(mock.CLI(context.Background(), os.Args[1:], path, os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}
func native(direction, channel, raw string) mock.Frame {
	return mock.Frame{Direction: direction, Channel: channel, Data: json.RawMessage(raw)}
}
func initialize() []mock.Frame {
	return []mock.Frame{
		native("client", "control", `{"type":"control_request","request_id":"init","request":{"subtype":"initialize","hooks":null,"forwardSubagentText":true}}`),
		native("server", "control", `{"type":"control_response","response":{"subtype":"success","request_id":"init","response":{}}}`),
		native("server", "protocol", `{"type":"system","subtype":"init","session_id":"root","claude_code_version":"2.1.258"}`),
	}
}
func user(text string) mock.Frame {
	b, _ := json.Marshal(map[string]any{"type": "user", "session_id": "", "parent_tool_use_id": nil, "message": map[string]string{"role": "user", "content": text}})
	return native("client", "protocol", string(b))
}
func terminal(isError bool) []mock.Frame {
	raw := `{"type":"result","subtype":"success","session_id":"root","is_error":false,"result":"synthetic"}`
	if isError {
		raw = `{"type":"result","subtype":"success","session_id":"root","is_error":true,"result":"synthetic auth failure"}`
	}
	return []mock.Frame{native("server", "protocol", raw), native("server", "protocol", `{"type":"system","subtype":"session_state_changed","session_id":"root","state":"idle"}`)}
}
func openSynthetic(t *testing.T, ctx context.Context, w *recorder.Workspace, frames []mock.Frame) *claude.Session {
	t.Helper()
	for i := range frames {
		frames[i].Ordinal = i + 1
	}
	data, err := json.Marshal(mock.Scenario{Version: 1, Provenance: "synthetic", Frames: frames})
	require.NoError(t, err)
	path := filepath.Join(w.Root, "peer.json")
	require.NoError(t, os.WriteFile(path, data, 0600))
	exe, err := os.Executable()
	require.NoError(t, err)
	s, err := claude.Open(ctx, claude.SessionOptions{CLIPath: exe, WorkDir: w.Project, Env: []string{mock.ScenarioEnvironment + "=" + path, "HOME=" + w.Home, "GORACE=atexit_sleep_ms=0"}, InitializeTimeout: time.Second, ShutdownTimeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { s.Abort() })
	return s
}
func TestRootContinuationConfigurationUsesSameSession(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	w, err := recorder.PrepareWorkspace(filepath.Join(base, "capture"), "r1")
	require.NoError(t, err)
	l, err := recorder.OpenLedger(filepath.Join(base, "ledger"))
	require.NoError(t, err)
	require.NoError(t, l.Reserve("r1"))
	frames := append(initialize(), user("Reply with the word Ready."))
	frames = append(frames, terminal(false)...)
	frames = append(frames,
		native("client", "control", `{"type":"control_request","request_id":"model","request":{"subtype":"set_model"}}`),
		native("server", "control", `{"type":"control_response","response":{"subtype":"success","request_id":"model","response":{}}}`),
		native("client", "control", `{"type":"control_request","request_id":"thinking","request":{"subtype":"set_max_thinking_tokens","max_thinking_tokens":null}}`),
		native("server", "control", `{"type":"control_response","response":{"subtype":"success","request_id":"thinking","response":{}}}`), user("Reply with the word Done."))
	frames = append(frames, terminal(false)...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := openSynthetic(t, ctx, w, frames)
	var observed bytes.Buffer
	require.NoError(t, recorder.Drive(ctx, s, "r1", w, l, &observed))
	require.Contains(t, observed.String(), "set_max_thinking_tokens")
	require.Error(t, l.Reserve("r1"))
}

func TestRootNativeErrorsAndUntriggeredControlDoNotAddTurns(t *testing.T) {
	for _, tc := range []struct {
		name, scene string
		nativeError bool
	}{
		{"success subtype auth error", "r1", true},
		{"no execution barrier", "r3", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			w, err := recorder.PrepareWorkspace(filepath.Join(base, "capture"), tc.scene)
			require.NoError(t, err)
			l, err := recorder.OpenLedger(filepath.Join(base, "ledger"))
			require.NoError(t, err)
			require.NoError(t, l.Reserve(tc.scene))
			plan, err := recorder.Plan(tc.scene)
			require.NoError(t, err)
			frames := append(initialize(), user(plan.Prompts[0]))
			frames = append(frames, terminal(tc.nativeError)...)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s := openSynthetic(t, ctx, w, frames)
			var observed bytes.Buffer
			err = recorder.Drive(ctx, s, tc.scene, w, l, &observed)
			if tc.nativeError {
				require.ErrorContains(t, err, "native error result")
			} else {
				require.ErrorIs(t, err, recorder.ErrUntriggered)
			}
			require.Contains(t, observed.String(), "idle", "drain through idle after result")
			data, err := os.ReadFile(filepath.Join(base, "ledger", "attempts.jsonl"))
			require.NoError(t, err)
			require.Equal(t, 1, bytes.Count(data, []byte(`"action":"turn"`)))
			require.Contains(t, string(data), `"outcome":"failed"`)
			require.Error(t, l.Reserve("r2"))
			select {
			case <-s.Done():
			case <-ctx.Done():
				t.Fatal("owned session cleanup incomplete")
			}
		})
	}
}
func TestRootExecutionBarrierInputAndInterrupt(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	w, err := recorder.PrepareWorkspace(filepath.Join(base, "capture"), "r3")
	require.NoError(t, err)
	l, err := recorder.OpenLedger(filepath.Join(base, "ledger"))
	require.NoError(t, err)
	require.NoError(t, l.Reserve("r3"))
	frames := append(initialize(), user("Produce a short numbered list from one to ten."))
	frames = append(frames,
		native("server", "protocol", `{"type":"stream_event","session_id":"root","event":{"type":"message_start","message":{"id":"m1","role":"assistant","content":[]}}}`),
		user("Also include the word sample."),
		native("client", "control", `{"type":"control_request","request_id":"interrupt","request":{"subtype":"interrupt"}}`),
		native("server", "control", `{"type":"control_response","response":{"subtype":"success","request_id":"interrupt","response":{}}}`))
	frames = append(frames, terminal(false)...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := openSynthetic(t, ctx, w, frames)
	var observed bytes.Buffer
	require.NoError(t, recorder.Drive(ctx, s, "r3", w, l, &observed))
}

func TestRootReadDenyAndOriginalQuestionPolicy(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	w, err := recorder.PrepareWorkspace(filepath.Join(base, "capture"), "r2")
	require.NoError(t, err)
	l, err := recorder.OpenLedger(filepath.Join(base, "ledger"))
	require.NoError(t, err)
	require.NoError(t, l.Reserve("r2"))
	frames := append(initialize(), user("Read numbers.txt, then write summary.txt with total=18."))
	frames = append(frames,
		native("server", "control", `{"type":"control_request","request_id":"read","request":{"subtype":"can_use_tool","tool_name":"Read","tool_use_id":"read-tool","input":{"file_path":"numbers.txt"}}}`),
		native("client", "control", `{"type":"control_request","request_id":"thinking","request":{"subtype":"set_max_thinking_tokens","max_thinking_tokens":null}}`),
		native("server", "protocol", `{"type":"system","subtype":"session_state_changed","session_id":"root","state":"busy"}`),
		native("server", "control", `{"type":"control_response","response":{"subtype":"success","request_id":"thinking","response":{}}}`),
		native("client", "control", `{"type":"control_response","response":{"subtype":"success","request_id":"read","response":{"behavior":"allow","updatedInput":{"file_path":"numbers.txt"}}}}`),
		native("server", "control", `{"type":"control_request","request_id":"write","request":{"subtype":"can_use_tool","tool_name":"Write","tool_use_id":"write-tool","input":{"file_path":"summary.txt","content":"total=18\n"}}}`),
		native("client", "control", `{"type":"control_response","response":{"subtype":"success","request_id":"write","response":{"behavior":"deny","message":"outside exact synthetic story policy"}}}`))
	frames = append(frames, terminal(false)...)
	frames = append(frames, user("Ask two questions once: choose Blue or Green; enter a short label."),
		native("server", "control", `{"type":"control_request","request_id":"question","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"question-tool","input":{"questions":[{"question":"Which synthetic color?","options":[{"label":"Blue"},{"label":"Green"}]},{"question":"Enter a short label.","options":[{"label":"sample"},{"label":"other"}]}]}}}`),
		native("client", "control", `{"type":"control_response","response":{"subtype":"success","request_id":"question","response":{"behavior":"allow","updatedInput":{"questions":[{"question":"Which synthetic color?","options":[{"label":"Blue"},{"label":"Green"}]},{"question":"Enter a short label.","options":[{"label":"sample"},{"label":"other"}]}],"answers":{"Which synthetic color?":"Blue","Enter a short label.":"sample"}}}}}`))
	frames = append(frames, terminal(false)...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := openSynthetic(t, ctx, w, frames)
	var observed bytes.Buffer
	require.NoError(t, recorder.Drive(ctx, s, "r2", w, l, &observed))
	_, err = os.Stat(filepath.Join(w.Project, "summary.txt"))
	require.ErrorIs(t, err, os.ErrNotExist, "denial is not write success")
}
