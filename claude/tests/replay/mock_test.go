package transcript_test

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/suite"
	"github.com/zoumo/agent-sdk-go/claude"
	replay "github.com/zoumo/agent-sdk-go/claude/tests/replay"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type mockScenarioDefinition struct {
	Version    int            `json:"version"`
	Provenance string         `json:"provenance"`
	Frames     []replay.Frame `json:"frames"`
}

func mockFrame(direction, channel, data string) replay.Frame {
	return replay.Frame{Direction: direction, Channel: channel, Data: json.RawMessage(data)}
}
func mockScenario(fs ...replay.Frame) mockScenarioDefinition {
	for i := range fs {
		fs[i].Ordinal = i + 1
	}
	return mockScenarioDefinition{1, "synthetic", fs}
}
func mockInitialization() []replay.Frame {
	return []replay.Frame{
		mockFrame("client", "control", `{"type":"control_request","request_id":"init","request":{"subtype":"initialize","hooks":null,"forwardSubagentText":true}}`),
		mockFrame("server", "control", `{"type":"control_response","response":{"subtype":"success","request_id":"init","response":{}}}`),
	}
}

type mockSDKSuite struct {
	suite.Suite
	ctx    context.Context
	cancel context.CancelFunc
	client *claude.Session
}

func TestGoMockSDK(t *testing.T) { suite.Run(t, new(mockSDKSuite)) }
func (s *mockSDKSuite) SetupTest() {
	s.ctx, s.cancel = context.WithTimeout(context.Background(), 5*time.Second)
}
func (s *mockSDKSuite) TearDownTest() {
	if s.client != nil {
		s.client.Abort()
		s.client = nil
	}
	s.cancel()
}
func (s *mockSDKSuite) open(script mockScenarioDefinition) {
	dir := s.T().TempDir()
	path := filepath.Join(dir, "synthetic.json")
	data, err := json.Marshal(script)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(path, data, 0600))
	exe, err := mocktest.Launcher(dir, "claude", path, "")
	s.Require().NoError(err)
	s.client, err = claude.Open(s.ctx, claude.SessionOptions{CLIPath: exe, WorkDir: dir, Env: []string{"PATH=", "HOME=" + dir, "GORACE=atexit_sleep_ms=0"}, ShutdownTimeout: time.Second})
	s.Require().NoError(err)
}
func (s *mockSDKSuite) nextNative(typ string) claude.Event {
	for {
		record, err := s.client.Receive(s.ctx)
		s.Require().NoError(err)
		if e := record.Event; e != nil && e.Type == typ {
			return *e
		}
	}
}
func (s *mockSDKSuite) TestPermissionChildAndInterrupt() {
	frames := mockInitialization()
	frames = append(frames,
		mockFrame("client", "protocol", `{"type":"user","message":{"role":"user","content":"synthetic task"},"parent_tool_use_id":null,"session_id":""}`),
		mockFrame("server", "control", `{"type":"control_request","request_id":"permission","request":{"subtype":"can_use_tool","tool_name":"Read","tool_use_id":"read-1","input":{"path":"synthetic.txt"}}}`),
		mockFrame("client", "control", `{"type":"control_response","response":{"subtype":"success","request_id":"permission","response":{"behavior":"allow","updatedInput":{"path":"synthetic.txt"}}}}`),
		mockFrame("server", "protocol", `{"type":"system","subtype":"init","session_id":"root"}`),
		mockFrame("server", "protocol", `{"type":"assistant","session_id":"root","message":{"content":[{"type":"tool_use","id":"spawn-child","name":"Agent","input":{"prompt":"synthetic child"}}]}}`),
		mockFrame("server", "protocol", `{"type":"assistant","session_id":"root","agent_id":"child","parent_tool_use_id":"spawn-child","message":{"content":[{"type":"text","text":"synthetic child"}]}}`),
		mockFrame("client", "control", `{"type":"control_request","request_id":"interrupt","request":{"subtype":"interrupt"}}`),
		mockFrame("server", "control", `{"type":"control_response","response":{"subtype":"success","request_id":"interrupt","response":{}}}`),
		mockFrame("server", "protocol", `{"type":"result","subtype":"success","session_id":"root","result":"synthetic complete"}`),
	)
	s.open(mockScenario(frames...))
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
func (s *mockSDKSuite) TestReverseCancellationAndDisconnect() {
	frames := mockInitialization()
	frames = append(frames,
		mockFrame("server", "control", `{"type":"control_request","request_id":"permission","request":{"subtype":"can_use_tool","tool_name":"Read","input":{}}}`),
		replay.Frame{Direction: "server", Channel: "control", Data: json.RawMessage(`{"type":"control_cancel_request","request_id":"permission"}`), DelayMillis: 50},
		replay.Frame{Direction: "disconnect", Channel: "protocol", DelayMillis: 50},
	)
	s.open(mockScenario(frames...))
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

func (s *mockSDKSuite) TestNativeUserQuestion() {
	frames := mockInitialization()
	frames = append(frames,
		mockFrame("server", "control", `{"type":"control_request","request_id":"question","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"question-tool","input":{"questions":[{"question":"Which?","header":"Choice","options":[{"label":"A"}]}]}}}`),
		mockFrame("client", "control", `{"type":"control_response","response":{"subtype":"success","request_id":"question","response":{"behavior":"allow","updatedInput":{"questions":[{"question":"Which?","header":"Choice","options":[{"label":"A"}]}],"answers":{"Which?":"A"}}}}}`),
		mockFrame("server", "protocol", `{"type":"result","subtype":"success","session_id":"synthetic","result":"synthetic answer accepted"}`),
	)
	s.open(mockScenario(frames...))
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
func (s *mockSDKSuite) nextRequest() *claude.IncomingRequest {
	for {
		r, err := s.client.Receive(s.ctx)
		s.Require().NoError(err)
		if r.Request != nil {
			return r.Request
		}
	}
}
