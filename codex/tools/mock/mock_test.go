package mock_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/zoumo/agent-sdk-go/codex/tools/mock"
	recording "github.com/zoumo/agent-sdk-go/codex/tools/replay"
)

type output struct{ bytes.Buffer }

type shortOutput struct{}

func (shortOutput) Write(p []byte) (int, error) { return len(p) - 1, nil }
func (shortOutput) Close() error                { return nil }

func (*output) Close() error { return nil }

func frames(wires ...string) []recording.Frame {
	result := make([]recording.Frame, 0, len(wires))
	for i, wire := range wires {
		result = append(result, recording.Frame{Ordinal: i + 1, Epoch: 1, Direction: wire[:3], Channel: "protocol", Wire: json.RawMessage(wire[4:])})
	}
	return result
}
func scenario(fs []recording.Frame) mock.Scenario {
	return mock.Scenario{Version: 1, Provenance: "synthetic", Frames: fs}
}

type MockSuite struct{ suite.Suite }

func TestMock(t *testing.T) { suite.Run(t, new(MockSuite)) }

func (s *MockSuite) TestTranscript() {
	for _, tc := range []struct {
		name        string
		frames      []recording.Frame
		input, want string
	}{
		{"dynamic IDs and interleaved reverse namespace", frames(
			`c2s {"id":1,"method":"one","params":{}}`,
			`c2s {"id":2,"method":"two"}`,
			`s2c {"id":2,"result":{}}`,
			`s2c {"id":1,"method":"item/tool/requestUserInput","params":{"threadId":"child","questions":[]}}`,
			`c2s {"id":1,"result":{"answers":{}}}`,
			`s2c {"id":1,"result":{}}`),
			"{\"id\":\"live\",\"method\":\"one\",\"params\":{}}\n{\"id\":8,\"method\":\"two\"}\n{\"id\":1,\"result\":{\"answers\":{}}}\n", `"id":"live"`},
		{"approval cancel resolved and interleaved child bursts", frames(
			`c2s {"id":1,"method":"turn/start","params":{"threadId":"root","input":[]}}`,
			`s2c {"id":1,"result":{}}`,
			`s2c {"id":"permission","method":"item/commandExecution/requestApproval","params":{"threadId":"child","turnId":"t","itemId":"i"}}`,
			`c2s {"id":"permission","result":{"decision":"accept"}}`,
			`s2c {"method":"item/agentMessage/delta","params":{"threadId":"child","turnId":"t","itemId":"i","delta":"a"}}`,
			`s2c {"method":"item/agentMessage/delta","params":{"threadId":"root","turnId":"t","itemId":"j","delta":"b"}}`,
			`c2s {"id":2,"method":"turn/interrupt","params":{"threadId":"root","turnId":"t"}}`,
			`s2c {"id":2,"result":{}}`,
			`s2c {"method":"serverRequest/resolved","params":{"threadId":"child","requestId":"permission"}}`,
			`s2c {"method":"item/completed","params":{"threadId":"child","item":{"type":"agentMessage","id":"i","text":"a"}}}`,
			`s2c {"method":"turn/completed","params":{"threadId":"root","turn":{"id":"t","status":"interrupted","items":[]}}}`,
			`c2s {"id":3,"method":"thread/close","params":{"threadId":"root"}}`,
			`s2c {"id":3,"result":{}}`,
			`s2c {"method":"thread/closed","params":{"threadId":"root"}}`),
			"{\"id\":31,\"method\":\"turn/start\",\"params\":{\"threadId\":\"root\",\"input\":[]}}\n{\"id\":\"permission\",\"result\":{\"decision\":\"accept\"}}\n{\"id\":32,\"method\":\"turn/interrupt\",\"params\":{\"threadId\":\"root\",\"turnId\":\"t\"}}\n{\"id\":33,\"method\":\"thread/close\",\"params\":{\"threadId\":\"root\"}}\n", `"method":"thread/closed"`},
		{"RPC failure", frames(`c2s {"id":1,"method":"thread/start"}`, `s2c {"id":1,"error":{"code":-32000,"message":"synthetic failure"}}`), "{\"id\":99,\"method\":\"thread/start\"}\n", `"code":-32000`},
		{"resolved approval without answer", frames(`s2c {"id":"q","method":"item/commandExecution/requestApproval","params":{"threadId":"child"}}`, `s2c {"method":"serverRequest/resolved","params":{"threadId":"child","requestId":"q"}}`), "", `"requestId":"q"`},
	} {
		s.Run(tc.name, func() {
			var out output
			err := mock.Run(context.Background(), io.NopCloser(strings.NewReader(tc.input)), &out, scenario(tc.frames))
			require.NoError(s.T(), err)
			assert.Contains(s.T(), out.String(), tc.want)
		})
	}
}

func (s *MockSuite) TestStrictness() {
	base := frames(`c2s {"id":1,"method":"one","params":{"x":1}}`, `s2c {"id":1,"result":{}}`)
	for _, tc := range []struct {
		name, input string
		fs          []recording.Frame
	}{
		{"early EOF", "", base},
		{"unknown request", "{\"id\":9,\"method\":\"other\"}\n", base},
		{"wrong parameter", "{\"id\":9,\"method\":\"one\",\"params\":{\"x\":2}}\n", base},
		{"extra trailing notification", "{\"id\":9,\"method\":\"one\",\"params\":{\"x\":1}}\n{\"method\":\"unexpected\"}\n", base},
		{"missing ID", "{\"method\":\"one\",\"params\":{\"x\":1}}\n", base},
		{"null ID", "{\"id\":null,\"method\":\"one\",\"params\":{\"x\":1}}\n", base},
		{"duplicate live IDs", "{\"id\":9,\"method\":\"one\"}\n{\"id\":9,\"method\":\"two\"}\n", frames(`c2s {"id":1,"method":"one"}`, `c2s {"id":2,"method":"two"}`)},
		{"unanswered reverse request", "", frames(`s2c {"id":"q","method":"question"}`)},
		{"wrong answer ID", "{\"id\":\"wrong\",\"result\":{}}\n", frames(`s2c {"id":"q","method":"question"}`, `c2s {"id":"q","result":{}}`)},
		{"unmatched server response", "", frames(`s2c {"id":1,"result":{}}`)},
		{"late answer after resolution", "{\"id\":\"q\",\"result\":{}}\n", frames(`s2c {"id":"q","method":"approval","params":{"threadId":"child"}}`, `s2c {"method":"serverRequest/resolved","params":{"threadId":"child","requestId":"q"}}`, `c2s {"id":"q","result":{}}`)},
		{"resolution thread mismatch", "", frames(`s2c {"id":"q","method":"approval","params":{"threadId":"child"}}`, `s2c {"method":"serverRequest/resolved","params":{"threadId":"root","requestId":"q"}}`)},
		{"historical reverse ID reuse", "{\"id\":\"q\",\"result\":{}}\n", frames(`s2c {"id":"q","method":"question"}`, `c2s {"id":"q","result":{}}`, `s2c {"id":"q","method":"question"}`)},
		{"invalid input JSON", "[1,2]\n", base},
		{"frame byte limit", strings.Repeat("x", recording.ProtocolLimit+1) + "\n", base},
	} {
		s.Run(tc.name, func() {
			var out output
			assert.Error(s.T(), mock.Run(context.Background(), io.NopCloser(strings.NewReader(tc.input)), &out, scenario(tc.fs)))
		})
	}
}

func (s *MockSuite) TestBoundsAndDisconnect() {
	s.Run("short writes fail", func() {
		err := mock.Run(context.Background(), io.NopCloser(strings.NewReader("")), shortOutput{}, scenario(frames(`s2c {"method":"event"}`)))
		assert.ErrorIs(s.T(), err, io.ErrShortWrite)
		err = recording.Peer(strings.NewReader("{\"method\":\"one\"}\n{\"id\":1,\"method\":\"replay/assert\"}\n"), shortOutput{}, frames(`c2s {"method":"one"}`))
		assert.ErrorIs(s.T(), err, io.ErrShortWrite)
	})
	s.Run("rebound output frame limit", func() {
		id := strings.Repeat("i", 1<<20)
		payload := strings.Repeat("p", 1<<20)
		sc := scenario(frames(`c2s {"id":1,"method":"one"}`, `s2c {"id":1,"result":"`+payload+`"}`))
		var out output
		err := mock.Run(context.Background(), io.NopCloser(strings.NewReader(`{"id":"`+id+`","method":"one"}`+"\n")), &out, sc)
		assert.ErrorContains(s.T(), err, "output frame byte limit")
		assert.Empty(s.T(), out.String())
	})
	s.Run("blocked input deadline", func() {
		in, writer := io.Pipe()
		defer writer.Close()
		var out output
		sc := scenario(frames(`c2s {"id":1,"method":"missing"}`))
		sc.TimeoutMS = 10
		err := mock.Run(context.Background(), in, &out, sc)
		assert.ErrorIs(s.T(), err, context.DeadlineExceeded)
	})
	s.Run("blocked output deadline", func() {
		reader, out := io.Pipe()
		defer reader.Close()
		sc := scenario(frames(`s2c {"method":"event"}`))
		sc.TimeoutMS = 10
		err := mock.Run(context.Background(), io.NopCloser(strings.NewReader("")), out, sc)
		assert.ErrorIs(s.T(), err, context.DeadlineExceeded)
	})
	s.Run("delayed response", func() {
		sc := scenario(frames(`c2s {"id":1,"method":"one"}`, `s2c {"id":1,"result":{}}`))
		sc.Frames[1].DelayMS = 5
		start := time.Now()
		var out output
		require.NoError(s.T(), mock.Run(context.Background(), io.NopCloser(strings.NewReader("{\"id\":2,\"method\":\"one\"}\n")), &out, sc))
		assert.GreaterOrEqual(s.T(), time.Since(start), 5*time.Millisecond)
	})
	s.Run("intentional disconnect with pending request", func() {
		sc := scenario(frames(`c2s {"id":1,"method":"one"}`))
		sc.Frames = append(sc.Frames, recording.Frame{Ordinal: 2, Epoch: 1, Direction: "s2c", Disconnect: true})
		var out output
		require.ErrorIs(s.T(), mock.Run(context.Background(), io.NopCloser(strings.NewReader("{\"id\":2,\"method\":\"one\"}\n")), &out, sc), mock.ErrDisconnected)
		sc.Frames = append(sc.Frames, frames(`s2c {"method":"unreachable"}`)[0])
		assert.Error(s.T(), sc.Validate())
	})
}

func (s *MockSuite) TestScenarioValidationAndFaultExitCodes() {
	base := scenario(frames(`c2s {"id":1,"method":"one"}`, `s2c {"id":1,"result":{}}`))
	for _, tc := range []struct {
		name   string
		change func(*mock.Scenario)
	}{
		{"unsupported version", func(sc *mock.Scenario) { sc.Version = 2 }},
		{"missing provenance", func(sc *mock.Scenario) { sc.Provenance = "" }},
		{"missing recording source", func(sc *mock.Scenario) { sc.Provenance = "recording" }},
		{"recording cannot imply synthetic delay", func(sc *mock.Scenario) { sc.Provenance = "recording"; sc.Source = "capture"; sc.Frames[1].DelayMS = 1 }},
		{"invalid timeout", func(sc *mock.Scenario) { sc.TimeoutMS = 300001 }},
		{"invalid ordinal", func(sc *mock.Scenario) { sc.Frames[1].Ordinal = 1 }},
		{"invalid epoch", func(sc *mock.Scenario) { sc.Frames[0].Epoch = 2 }},
		{"invalid direction", func(sc *mock.Scenario) { sc.Frames[0].Direction = "invalid" }},
		{"invalid delay", func(sc *mock.Scenario) { sc.Frames[0].DelayMS = -1 }},
		{"nonobject frame", func(sc *mock.Scenario) { sc.Frames[0].Wire = json.RawMessage(`[]`) }},
	} {
		s.Run(tc.name, func() {
			sc := base
			sc.Frames = append([]recording.Frame(nil), base.Frames...)
			tc.change(&sc)
			assert.Error(s.T(), sc.Validate())
		})
	}
	for _, tc := range []struct {
		name string
		sc   mock.Scenario
		code int
	}{
		{"explicit disconnect", scenario([]recording.Frame{{Ordinal: 1, Epoch: 1, Direction: "s2c", Disconnect: true}}), 4},
		{"explicit deadline", mock.Scenario{Version: 1, Provenance: "synthetic", TimeoutMS: 1, Frames: []recording.Frame{{Ordinal: 1, Epoch: 1, Direction: "s2c", DelayMS: 100, Wire: json.RawMessage(`{"method":"event"}`)}}}, 3},
	} {
		s.Run(tc.name, func() {
			b, err := json.Marshal(tc.sc)
			require.NoError(s.T(), err)
			path := filepath.Join(s.T().TempDir(), "synthetic.json")
			require.NoError(s.T(), os.WriteFile(path, b, 0600))
			var out output
			var stderr bytes.Buffer
			assert.Equal(s.T(), tc.code, mock.Main(context.Background(), []string{"--scenario", path}, io.NopCloser(strings.NewReader("")), &out, &stderr))
			assert.Empty(s.T(), out.String())
			assert.NotEmpty(s.T(), stderr.String())
		})
	}
}

func (s *MockSuite) TestLoadAndCLI() {
	sc := scenario(frames(`c2s {"id":1,"method":"one"}`, `s2c {"id":1,"result":{}}`))
	b, err := json.Marshal(sc)
	require.NoError(s.T(), err)
	loaded, err := mock.Load(bytes.NewReader(b))
	require.NoError(s.T(), err)
	assert.Equal(s.T(), sc, loaded)
	for _, bad := range []string{`{}`, string(b) + ` {}`, strings.Replace(string(b), `"version":1`, `"version":2`, 1), strings.Replace(string(b), `"version":1`, `"unknown":1,"version":1`, 1)} {
		_, err := mock.Load(strings.NewReader(bad))
		assert.Error(s.T(), err)
	}
	path := filepath.Join(s.T().TempDir(), "synthetic.json")
	require.NoError(s.T(), os.WriteFile(path, b, 0600))
	s.T().Setenv("CODEX_MOCK_SCENARIO", path)
	for _, tc := range []struct {
		name  string
		args  []string
		input string
		code  int
	}{
		{"native startup", []string{"app-server", "--listen", "stdio://"}, "{\"id\":99,\"method\":\"one\"}\n", 0},
		{"explicit selection", []string{"--scenario", path, "app-server"}, "{\"id\":99,\"method\":\"one\"}\n", 0},
		{"protocol mismatch", nil, "{\"method\":\"bad\"}\n", 1},
		{"incomplete EOF", nil, "", 1},
		{"unsupported transport", []string{"app-server", "--listen", "ws://127.0.0.1:1"}, "", 2},
		{"unknown flag", []string{"--unknown"}, "", 2},
	} {
		s.Run(tc.name, func() {
			var out output
			var stderr bytes.Buffer
			assert.Equal(s.T(), tc.code, mock.Main(context.Background(), tc.args, io.NopCloser(strings.NewReader(tc.input)), &out, &stderr))
			if tc.code != 0 {
				assert.NotEmpty(s.T(), stderr.String())
			}
		})
	}
}
