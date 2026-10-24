// SPDX-License-Identifier: Apache-2.0
package mock_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/claude/tools/mock"
	replay "github.com/zoumo/agent-sdk-go/claude/tools/replay"
)

func TestMain(m *testing.M) {
	if path := os.Getenv(mock.ScenarioEnvironment); path != "" {
		os.Exit(mock.CLI(context.Background(), os.Args[1:], path, os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

type writeBuffer struct{ bytes.Buffer }

func (*writeBuffer) Close() error { return nil }

func frame(direction, channel, data string) mock.Frame {
	return mock.Frame{Direction: direction, Channel: channel, Data: json.RawMessage(data)}
}
func scenario(frames ...mock.Frame) mock.Scenario {
	for i := range frames {
		frames[i].Ordinal = i + 1
	}
	return mock.Scenario{Version: mock.Version, Provenance: "synthetic", Frames: frames}
}
func initialization() []mock.Frame {
	return []mock.Frame{
		frame("client", "control", `{"type":"control_request","request_id":"init","request":{"subtype":"initialize","hooks":null,"forwardSubagentText":true}}`),
		frame("server", "control", `{"type":"control_response","response":{"subtype":"success","request_id":"init","response":{}}}`),
	}
}

func TestRunStrictAndVersioned(t *testing.T) {
	expected := frame("client", "protocol", `{"type":"user","message":{"content":"hello"}}`)
	emitted := frame("server", "protocol", `{"type":"result","subtype":"success","result":"synthetic"}`)
	for _, tc := range []struct {
		name, input string
		script      mock.Scenario
		want        error
		fail        bool
	}{
		{name: "complete", input: string(expected.Data) + "\n", script: scenario(expected, emitted)},
		{name: "mismatch", input: `{"type":"user","message":{"content":"wrong"}}` + "\n", script: scenario(expected, emitted), fail: true},
		{name: "unknown field", input: `{"type":"user","message":{"content":"hello"},"unexpected":true}` + "\n", script: scenario(expected), fail: true},
		{name: "early eof", script: scenario(expected), fail: true},
		{name: "extra frame", input: string(expected.Data) + "\n" + string(expected.Data) + "\n", script: scenario(expected), fail: true},
		{name: "trailing json", input: string(expected.Data) + ` {}` + "\n", script: scenario(expected), fail: true},
		{name: "oversize input", input: strings.Repeat("x", mock.MaxFrameBytes+1), script: scenario(expected), fail: true},
		{name: "unsupported version", script: mock.Scenario{Version: 2, Provenance: "synthetic", Frames: []mock.Frame{expected}}, fail: true},
		{name: "disconnect", script: scenario(mock.Frame{Direction: "disconnect", Channel: "protocol"}), want: replay.ErrDisconnected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output writeBuffer
			err := mock.Run(context.Background(), io.NopCloser(strings.NewReader(tc.input)), &output, tc.script)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
			} else if tc.fail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Contains(t, output.String(), `"synthetic"`)
			}
		})
	}
}

func TestLoadAndArguments(t *testing.T) {
	for _, tc := range []struct{ name, data string }{
		{"unknown wrapper", `{"version":1,"provenance":"synthetic","frames":[],"unknown":true}`},
		{"unknown frame", `{"version":1,"provenance":"synthetic","frames":[{"ordinal":1,"direction":"server","channel":"protocol","data":{},"typo":1}]}`},
		{"missing provenance", `{"version":1,"frames":[]}`},
		{"oversize", strings.Repeat(" ", mock.MaxScenarioBytes+1)},
		{"trailing", `{} {}`},
	} {
		t.Run(tc.name, func(t *testing.T) { _, err := mock.Load(strings.NewReader(tc.data)); require.Error(t, err) })
	}
	raw, err := json.Marshal(scenario(initialization()...))
	require.NoError(t, err)
	loaded, err := mock.Load(bytes.NewReader(raw))
	require.NoError(t, err)
	assert.Equal(t, "synthetic", loaded.Provenance)
	for _, tc := range []struct {
		name  string
		args  []string
		valid bool
	}{
		{"sdk", []string{"--output-format", "stream-json", "--verbose", "--input-format", "stream-json", "--permission-prompt-tool", "stdio", "--include-partial-messages", "--model=x", "--resume=y"}, true},
		{"shell", nil, true},
		{"unknown", []string{"--dangerously-skip-permissions"}, false},
		{"duplicate", []string{"--verbose", "--verbose"}, false},
		{"wrong format", []string{"--input-format", "json"}, false},
		{"missing", []string{"--output-format"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := mock.ValidateArgs(tc.args)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestRunCancellationBoundsStreams(t *testing.T) {
	for _, mode := range []string{"read", "write", "delay", "eof"} {
		t.Run(mode, func(t *testing.T) {
			inputR, inputW := io.Pipe()
			defer inputW.Close()
			outputR, outputW := io.Pipe()
			defer outputR.Close()
			f := frame("server", "protocol", `{"type":"notice"}`)
			switch mode {
			case "read":
				f.Direction = "client"
			case "delay":
				f.DelayMillis = 1000
			case "eof":
				inputW.Close()
			}
			if mode == "eof" {
				go io.Copy(io.Discard, outputR)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			err := mock.Run(ctx, inputR, outputW, scenario(f))
			if mode == "eof" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}
		})
	}
}

type sdkSuite struct {
	suite.Suite
	ctx    context.Context
	cancel context.CancelFunc
	client *claude.Session
}

func TestSDKPeer(t *testing.T) { suite.Run(t, new(sdkSuite)) }
func (s *sdkSuite) SetupTest() {
	s.ctx, s.cancel = context.WithTimeout(context.Background(), 5*time.Second)
}
func (s *sdkSuite) TearDownTest() {
	if s.client != nil {
		s.client.Abort()
		s.client = nil
	}
	s.cancel()
}
func (s *sdkSuite) open(script mock.Scenario) {
	dir := s.T().TempDir()
	path := filepath.Join(dir, "synthetic.json")
	data, err := json.Marshal(script)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(path, data, 0600))
	exe, err := os.Executable()
	s.Require().NoError(err)
	s.client, err = claude.Open(s.ctx, claude.SessionOptions{CLIPath: exe, WorkDir: dir, Env: []string{mock.ScenarioEnvironment + "=" + path, "HOME=" + dir, "GORACE=atexit_sleep_ms=0"}, ShutdownTimeout: time.Second})
	s.Require().NoError(err)
}
func (s *sdkSuite) nextNative(typ string) claude.Event {
	for {
		record, err := s.client.Receive(s.ctx)
		s.Require().NoError(err)
		if e := record.Event; e != nil && e.Type == typ {
			return *e
		}
	}
}
func (s *sdkSuite) TestPermissionChildAndInterrupt() {
	frames := initialization()
	frames = append(frames,
		frame("client", "protocol", `{"type":"user","message":{"role":"user","content":"synthetic task"},"parent_tool_use_id":null,"session_id":""}`),
		frame("server", "control", `{"type":"control_request","request_id":"permission","request":{"subtype":"can_use_tool","tool_name":"Read","tool_use_id":"read-1","input":{"path":"synthetic.txt"}}}`),
		frame("client", "control", `{"type":"control_response","response":{"subtype":"success","request_id":"permission","response":{"behavior":"allow","updatedInput":{"path":"synthetic.txt"}}}}`),
		frame("server", "protocol", `{"type":"system","subtype":"init","session_id":"root"}`),
		frame("server", "protocol", `{"type":"assistant","session_id":"root","message":{"content":[{"type":"tool_use","id":"spawn-child","name":"Agent","input":{"prompt":"synthetic child"}}]}}`),
		frame("server", "protocol", `{"type":"assistant","session_id":"root","agent_id":"child","parent_tool_use_id":"spawn-child","message":{"content":[{"type":"text","text":"synthetic child"}]}}`),
		frame("client", "control", `{"type":"control_request","request_id":"interrupt","request":{"subtype":"interrupt"}}`),
		frame("server", "control", `{"type":"control_response","response":{"subtype":"success","request_id":"interrupt","response":{}}}`),
		frame("server", "protocol", `{"type":"result","subtype":"success","session_id":"root","result":"synthetic complete"}`),
	)
	s.open(scenario(frames...))
	s.Require().NoError(submitText(s.client, s.ctx, "synthetic task"))
	p := s.nextRequest().Handle()
	s.Require().Equal("permission", p.RequestID())
	s.Require().NoError(p.Answer(s.ctx, claude.PermissionAnswer{Behavior: claude.PermissionAllow}))
	s.nextNative("assistant")
	e := s.nextNative("assistant")
	s.Contains(string(e.Raw), "synthetic child")
	s.Require().NoError(s.client.Interrupt(s.ctx, claude.InterruptOptions{}))
	e = s.nextNative("result")
	s.Contains(string(e.Raw), "synthetic complete")
	s.Require().NoError(s.client.Close())
	s.client = nil
}
func (s *sdkSuite) TestReverseCancellationAndDisconnect() {
	frames := initialization()
	frames = append(frames,
		frame("server", "control", `{"type":"control_request","request_id":"permission","request":{"subtype":"can_use_tool","tool_name":"Read","input":{}}}`),
		mock.Frame{Direction: "server", Channel: "control", Data: json.RawMessage(`{"type":"control_cancel_request","request_id":"permission"}`), DelayMillis: 50},
		mock.Frame{Direction: "disconnect", Channel: "protocol", DelayMillis: 50},
	)
	s.open(scenario(frames...))
	p := s.nextRequest().Handle()
	select {
	case <-p.Context().Done():
	case <-s.ctx.Done():
		s.Require().FailNow("cancel timeout")
	}
	s.Require().ErrorIs(p.Answer(s.ctx, claude.PermissionAnswer{Behavior: claude.PermissionAllow}), claude.ErrPermissionCancelled)
	select {
	case <-s.client.Done():
	case <-s.ctx.Done():
		s.Require().FailNow("disconnect timeout")
	}
	s.Require().Error(s.client.Close())
	s.client = nil
}

func TestCLIExitClassification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script mock.Scenario
		input  string
		code   int
	}{
		{"complete", scenario(frame("server", "protocol", `{"type":"notice"}`)), "", 0},
		{"incomplete", scenario(frame("client", "protocol", `{"type":"user"}`)), "", 1},
		{"invalid", mock.Scenario{}, "", 2},
		{"disconnect", scenario(mock.Frame{Direction: "disconnect", Channel: "protocol"}), "", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.script)
			require.NoError(t, err)
			path := filepath.Join(t.TempDir(), "scenario.json")
			require.NoError(t, os.WriteFile(path, data, 0600))
			var output writeBuffer
			var stderr bytes.Buffer
			code := mock.CLI(context.Background(), nil, path, io.NopCloser(strings.NewReader(tc.input)), &output, &stderr)
			assert.Equal(t, tc.code, code, stderr.String())
			if tc.code != 0 {
				assert.NotEmpty(t, stderr.String())
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output writeBuffer
	assert.True(t, errors.Is(mock.Run(ctx, io.NopCloser(strings.NewReader("")), &output, scenario(frame("client", "protocol", `{}`))), context.Canceled))
}

func TestScenarioValidation(t *testing.T) {
	base := func() mock.Scenario { return scenario(frame("server", "protocol", `{"type":"notice"}`)) }
	for _, tc := range []struct {
		name   string
		change func(*mock.Scenario)
	}{
		{"version", func(s *mock.Scenario) { s.Version = 2 }},
		{"provenance", func(s *mock.Scenario) { s.Provenance = "real" }},
		{"recording without source", func(s *mock.Scenario) { s.Provenance = "recording" }},
		{"recording with synthetic delay", func(s *mock.Scenario) {
			s.Provenance = "recording"
			s.Source = "capture.jsonl"
			s.Frames[0].DelayMillis = 1
		}},
		{"recording with synthetic disconnect", func(s *mock.Scenario) {
			s.Provenance = "recording"
			s.Source = "capture.jsonl"
			s.Frames[0].Direction = "disconnect"
			s.Frames[0].Data = nil
		}},
		{"empty", func(s *mock.Scenario) { s.Frames = nil }},
		{"too many frames", func(s *mock.Scenario) { s.Frames = make([]mock.Frame, mock.MaxFrames+1) }},
		{"ordinal", func(s *mock.Scenario) { s.Frames[0].Ordinal = 2 }},
		{"channel", func(s *mock.Scenario) { s.Frames[0].Channel = "stdout" }},
		{"direction", func(s *mock.Scenario) { s.Frames[0].Direction = "ignored" }},
		{"delay negative", func(s *mock.Scenario) { s.Frames[0].DelayMillis = -1 }},
		{"delay excessive", func(s *mock.Scenario) { s.Frames[0].DelayMillis = 30001 }},
		{"native scalar", func(s *mock.Scenario) { s.Frames[0].Data = json.RawMessage(`null`) }},
		{"native trailing", func(s *mock.Scenario) { s.Frames[0].Data = json.RawMessage(`{} {}`) }},
		{"oversize frame", func(s *mock.Scenario) {
			s.Frames[0].Data = json.RawMessage(`{"padding":"` + strings.Repeat("x", mock.MaxFrameBytes) + `"}`)
		}},
		{"disconnect with data", func(s *mock.Scenario) { s.Frames[0].Direction = "disconnect" }},
		{"disconnect before end", func(s *mock.Scenario) {
			s.Frames = append([]mock.Frame{{Ordinal: 1, Direction: "disconnect", Channel: "protocol"}}, mock.Frame{Ordinal: 2, Direction: "server", Channel: "protocol", Data: json.RawMessage(`{}`)})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) { s := base(); tc.change(&s); require.Error(t, s.Validate()) })
	}
	s := base()
	s.Provenance = "recording"
	s.Source = "original/manifest.json"
	require.NoError(t, s.Validate())
	var out writeBuffer
	require.Error(t, mock.Run(nil, io.NopCloser(strings.NewReader("")), &out, base()))
}

func TestUnresolvedControlsAreNotSuccessful(t *testing.T) {
	for _, tc := range []struct {
		name   string
		frames []mock.Frame
		input  string
	}{
		{"client pending", []mock.Frame{frame("client", "control", `{"type":"control_request","request_id":"old","request":{"subtype":"interrupt"}}`)}, `{"type":"control_request","request_id":"dynamic","request":{"subtype":"interrupt"}}` + "\n"},
		{"server pending", []mock.Frame{frame("server", "control", `{"type":"control_request","request_id":"permission","request":{"subtype":"can_use_tool","tool_name":"Read","input":{}}}`)}, ""},
		{"unknown response", []mock.Frame{frame("server", "control", `{"type":"control_response","response":{"request_id":"never-requested"}}`)}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out writeBuffer
			require.Error(t, mock.Run(context.Background(), io.NopCloser(strings.NewReader(tc.input)), &out, scenario(tc.frames...)))
		})
	}
}

func (s *sdkSuite) TestNativeUserQuestion() {
	frames := initialization()
	frames = append(frames,
		frame("server", "control", `{"type":"control_request","request_id":"question","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"question-tool","input":{"questions":[{"question":"Which?","header":"Choice","options":[{"label":"A"}]}]}}}`),
		frame("client", "control", `{"type":"control_response","response":{"subtype":"success","request_id":"question","response":{"behavior":"allow","updatedInput":{"questions":[{"question":"Which?","header":"Choice","options":[{"label":"A"}]}],"answers":{"Which?":"A"}}}}}`),
		frame("server", "protocol", `{"type":"result","subtype":"success","session_id":"synthetic","result":"synthetic answer accepted"}`),
	)
	s.open(scenario(frames...))
	p := s.nextRequest().Handle()
	s.Equal("AskUserQuestion", p.ToolName())
	var input claude.AskUserQuestionInput
	s.Require().NoError(json.Unmarshal(p.Input(), &input))
	answer := "A"
	updated, err := input.WithAnswers(map[string]claude.QuestionAnswer{"Which?": {Text: &answer}})
	s.Require().NoError(err)
	s.Require().NoError(p.Answer(s.ctx, claude.PermissionAnswer{Behavior: claude.PermissionAllow, UpdatedInput: updated}))
	event := s.nextNative("result")
	s.Contains(string(event.Raw), "synthetic answer accepted")
	s.Require().NoError(s.client.Close())
	s.client = nil
}

func submitText(session *claude.Session, ctx context.Context, text string) error {
	_, err := session.Submit(ctx, claude.Input{Text: text})
	return err
}
func (s *sdkSuite) nextRequest() *claude.IncomingRequest {
	for {
		r, err := s.client.Receive(s.ctx)
		s.Require().NoError(err)
		if r.Request != nil {
			return r.Request
		}
	}
}
