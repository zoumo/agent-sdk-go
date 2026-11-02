package recording_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	codex "github.com/zoumo/agent-sdk-go/codex"
	recording "github.com/zoumo/agent-sdk-go/codex/tests/replay"
	"github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

type GoMockSuite struct{ suite.Suite }

func TestGoMockSDK(t *testing.T) { suite.Run(t, new(GoMockSuite)) }

type mockScenarioDefinition struct {
	Version    int               `json:"version"`
	Provenance string            `json:"provenance"`
	Frames     []recording.Frame `json:"frames"`
}

func mockScenario(fs []recording.Frame) mockScenarioDefinition {
	return mockScenarioDefinition{1, "synthetic", fs}
}

func mockFrames(wires ...string) []recording.Frame {
	var fs []recording.Frame
	for i, w := range wires {
		fs = append(fs, recording.Frame{Ordinal: i + 1, Direction: w[:3], Epoch: 1, Channel: "protocol", Wire: json.RawMessage(w[4:])})
	}
	return fs
}

func (s *GoMockSuite) TestRealSDKStdio() {
	for _, tc := range []struct{ name, method, params, answer string }{
		{"question", "item/tool/requestUserInput", `{"threadId":"child","turnId":"turn","itemId":"item","questions":[{"id":"q","header":"Choice","question":"Pick?","isOther":false,"isSecret":false,"options":[]}]}`, `{"answers":{"q":{"answers":["blue"]}}}`},
		{"permission", "item/commandExecution/requestApproval", `{"threadId":"child","turnId":"turn","itemId":"item","command":"synthetic-only"}`, `{"decision":"accept"}`},
	} {
		s.Run(tc.name, func() {
			fs := mockFrames(
				`c2s {"id":101,"method":"initialize","params":{"clientInfo":{"name":"mock-consumer","version":"1"}}}`,
				`s2c {"id":101,"result":{"userAgent":"synthetic","codexHome":"/synthetic","platformFamily":"unix","platformOs":"linux"}}`,
				`c2s {"method":"initialized"}`,
				`c2s {"id":100,"method":"thread/start","params":{}}`,
				`s2c {"id":100,"result":{"thread":{"id":"child","sessionId":"synthetic","cliVersion":"0.162.1","createdAt":1,"updatedAt":1,"cwd":"/synthetic","ephemeral":true,"modelProvider":"synthetic","preview":"","projectId":null,"source":"appServer","status":{"type":"idle"},"turns":[]},"approvalPolicy":"never","approvalsReviewer":"user","cwd":"/synthetic","model":"synthetic","modelProvider":"synthetic","sandbox":{"type":"readOnly"}}}`,
				`c2s {"id":102,"method":"synthetic/begin"}`,
				`s2c {"id":102,"result":{}}`,
				`s2c {"id":"reverse","method":"`+tc.method+`","params":`+tc.params+`}`,
				`c2s {"id":"reverse","result":`+tc.answer+`}`,
				`c2s {"id":103,"method":"synthetic/done"}`,
				`s2c {"id":103,"result":{}}`)
			c, ctx := s.startSDK(mockScenario(fs))
			session, err := codex.Open(ctx, codex.SessionOptions{Client: c})
			s.Require().NoError(err)
			defer func() { _ = c.Close(); _ = session.Close() }()
			s.Require().NoError(c.Call(ctx, "synthetic/begin", nil, nil))
			received, err := session.Receive(ctx)
			s.Require().NoError(err)
			s.Require().NotNil(received.Request)
			request := received.Request
			s.Equal(tc.method, request.Method())
			var result map[string]any
			s.Require().NoError(json.Unmarshal([]byte(tc.answer), &result))
			s.Require().NoError(request.Handle().Answer(ctx, result))

			s.Require().NoError(c.Call(ctx, "synthetic/done", nil, nil))
			s.Require().NoError(c.Close())
		})
	}
}

func (s *GoMockSuite) TestSDKCancellationAndFault() {
	for _, fault := range []string{"late response", "RPC failure", "disconnect"} {
		s.Run(fault, func() {
			fs := mockFrames(
				`c2s {"id":101,"method":"initialize","params":{"clientInfo":{"name":"mock-consumer","version":"1"}}}`,
				`s2c {"id":101,"result":{"userAgent":"synthetic","codexHome":"/synthetic","platformFamily":"unix","platformOs":"linux"}}`,
				`c2s {"method":"initialized"}`,
				`c2s {"id":102,"method":"synthetic/fault"}`)
			switch fault {
			case "late response":
				fs = append(fs[:3], mockFrames(
					`c2s {"id":100,"method":"thread/start","params":{}}`,
					`s2c {"id":100,"result":{"thread":{"id":"child","sessionId":"synthetic","cliVersion":"0.162.1","createdAt":1,"updatedAt":1,"cwd":"/synthetic","ephemeral":true,"modelProvider":"synthetic","preview":"","projectId":null,"source":"appServer","status":{"type":"idle"},"turns":[]},"approvalPolicy":"never","approvalsReviewer":"user","cwd":"/synthetic","model":"synthetic","modelProvider":"synthetic","sandbox":{"type":"readOnly"}}}`,
					`c2s {"id":102,"method":"synthetic/fault"}`,
					`s2c {"method":"synthetic/dispatched","params":{"threadId":"child"}}`,
					`c2s {"id":103,"method":"synthetic/release"}`,
					`s2c {"id":102,"result":{}}`,
					`s2c {"id":103,"result":{}}`,
					`c2s {"id":104,"method":"synthetic/done"}`,
					`s2c {"id":104,"result":{}}`)...)
			case "RPC failure":
				fs = append(fs, mockFrames(`s2c {"id":102,"error":{"code":-32000,"message":"synthetic fault"}}`, `c2s {"id":103,"method":"synthetic/done"}`, `s2c {"id":103,"result":{}}`)...)
			case "disconnect":
				fs = append(fs, mockFrames(`s2c {}`)...)
				fs[4].Disconnect = true
				fs[4].Wire = nil
			}
			for i := range fs {
				fs[i].Ordinal = i + 1
			}
			c, ctx := s.startSDK(mockScenario(fs))
			var err error
			if fault == "late response" {
				session, openErr := codex.Open(ctx, codex.SessionOptions{Client: c})
				s.Require().NoError(openErr)
				defer func() { _ = c.Close(); _ = session.Close() }()
				callCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				result := make(chan error, 1)
				go func() { result <- c.Call(callCtx, "synthetic/fault", nil, nil) }()
				received, receiveErr := session.Receive(ctx)
				s.Require().NoError(receiveErr)
				s.Require().NotNil(received.Event)
				s.Require().Equal("synthetic/dispatched", received.Event.Method)
				cancel()
				err = <-result
				s.Require().ErrorIs(err, context.Canceled)
				var callError *codex.CallError
				s.Require().ErrorAs(err, &callError)
				s.Equal(codex.OutcomeUnknown, callError.Dispatch)
				// The peer cannot release the late response until cancellation
				// returned. A second response proves the stale ID cannot settle
				// a new request or resurrect the canceled call.
				s.Require().NoError(c.Call(ctx, "synthetic/release", nil, nil))
			} else {
				err = c.Call(ctx, "synthetic/fault", nil, nil)
				s.Require().Error(err)
			}
			if fault != "disconnect" {
				s.Require().NoError(c.Call(ctx, "synthetic/done", nil, nil))
			}
			s.Require().NoError(c.Close())
		})
	}
}

func (s *GoMockSuite) startSDK(sc mockScenarioDefinition) (*codex.Client, context.Context) {
	s.T().Helper()
	b, err := json.Marshal(sc)
	s.Require().NoError(err)
	path := filepath.Join(s.T().TempDir(), "synthetic.json")
	s.Require().NoError(os.WriteFile(path, b, 0o600))
	executable, err := mocktest.Executable()
	s.Require().NoError(err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	s.T().Cleanup(cancel)
	args, err := mocktest.Arguments(s.T().TempDir(), "codex", path, "")
	s.Require().NoError(err)
	c, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: executable, Args: args, Env: []string{}, Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "mock-consumer", Version: "1"}}})
	s.Require().NoError(err)
	s.T().Cleanup(func() { s.NoError(c.Close()) })
	return c, ctx
}
