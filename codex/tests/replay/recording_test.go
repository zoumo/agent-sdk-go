package recording

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func frame(n int, d, w string) Frame {
	return Frame{Ordinal: n, Direction: d, Epoch: 1, Channel: "protocol", Wire: json.RawMessage(w)}
}

func TestGoMockAdapterPreservesNativeLexemes(t *testing.T) {
	wire := `{"method" : "notice", "params" : {"html":"<tag>","number":0.123456789012345678901234567890} }`
	var out bytes.Buffer
	require.NoError(t, RunPeer(context.Background(), io.NopCloser(strings.NewReader("")), nopOutput{&out}, []Frame{frame(1, "s2c", wire)}))
	require.Equal(t, wire+"\n", out.String())
}

type nopOutput struct{ io.Writer }

func (nopOutput) Close() error { return nil }
func TestIDCorrelationAndEarlyNotification(t *testing.T) {
	f := []Frame{frame(1, "c2s", `{"id":101,"method":"one"}`), frame(2, "c2s", `{"id":102,"method":"two"}`), frame(3, "s2c", `{"method":"early","params":{"itemId":"item-001"}}`), frame(4, "s2c", `{"id":102,"result":{}}`), frame(5, "s2c", `{"id":101,"result":{}}`), frame(6, "s2c", `{"id":"reverse-1","method":"item/tool/call","params":{"callId":"item-001"}}`), frame(7, "c2s", `{"id":"reverse-1","result":{"success":true}}`)}
	in := `{"id":7,"method":"one"}` + "\n" + `{"id":"8","method":"two"}` + "\n" + `{"id":"reverse-1","result":{"success":true}}` + "\n" + `{"id":10,"method":"replay/answer"}` + "\n" + `{"id":9,"method":"replay/assert"}` + "\n"
	var out bytes.Buffer
	{
		err := Peer(strings.NewReader(in), &out, f)
		require.NoError(t, err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.False(t, !strings.Contains(lines[1], `"id":"8"`) || !strings.Contains(lines[2], `"id":7`),
		"numeric/string dynamic IDs not preserved")

}

// Private replay synchronization chooses the recorded successful-answer path.
// The original frames and their relative order remain independently asserted.
func TestReplayAnswerSettlementPrecedesNativeResolution(t *testing.T) {
	frames := []Frame{
		frame(1, "s2c", `{"id":"question","method":"future/question","params":{"threadId":"thread"}}`),
		frame(2, "c2s", `{"id":"question","result":{"answer":"literal"}}`),
		frame(3, "s2c", `{"method":"serverRequest/resolved","params":{"threadId":"thread","requestId":"question"}}`),
	}
	input := `{"id":"question","result":{"answer":"literal"}}` + "\n" +
		`{"id":8,"method":"replay/answer"}` + "\n" + `{"id":9,"method":"replay/assert"}` + "\n"
	var output bytes.Buffer
	require.NoError(t, Peer(strings.NewReader(input), &output, frames))
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	require.Len(t, lines, 4)
	assert.JSONEq(t, string(frames[0].Wire), lines[0])
	assert.JSONEq(t, `{"id":8,"result":{}}`, lines[1])
	assert.JSONEq(t, string(frames[2].Wire), lines[2])
	assert.JSONEq(t, `{"id":9,"result":{}}`, lines[3])
	answer := `{"id":"question","result":{"answer":"literal"}}` + "\n"
	for _, tc := range []struct{ name, control string }{
		{"missing settlement", ""},
		{"completion before settlement", `{"id":8,"method":"replay/assert"}`},
		{"wrong control", `{"id":8,"method":"other"}`},
		{"invalid control ID", `{"id":null,"method":"replay/answer"}`},
		{"unexpected parameters", `{"id":8,"method":"replay/answer","params":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			input := answer
			if tc.control != "" {
				input += tc.control + "\n"
			}
			require.Error(t, Peer(strings.NewReader(input), &out, frames))
			assert.NotContains(t, out.String(), `"serverRequest/resolved"`, "failed handshake released native frames")
		})
	}
	t.Run("public RunPeer needs no private handshake", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "wire.jsonl")
		out, err := os.Create(path)
		require.NoError(t, err)
		require.NoError(t, RunPeer(context.Background(), io.NopCloser(strings.NewReader(answer)), out, frames))
		wire, err := os.ReadFile(path)
		require.NoError(t, err)
		lines := strings.Split(strings.TrimSpace(string(wire)), "\n")
		require.Len(t, lines, 2)
		assert.JSONEq(t, string(frames[0].Wire), lines[0])
		assert.JSONEq(t, string(frames[2].Wire), lines[1])
	})
}

func TestReplayFaults(t *testing.T) {
	frames := []Frame{frame(1, "c2s", `{"id":101,"method":"one"}`), frame(2, "s2c", `{"id":101,"result":{}}`)}
	for _, tc := range []struct{ name, input string }{
		{"early EOF", ""},
		{"mismatched request", `{"id":1,"method":"extra"}` + "\n"},
		{"missing replay assertion", `{"id":1,"method":"one"}` + "\n"},
		{"extra request", `{"id":1,"method":"one"}` + "\n" + `{"id":2,"method":"extra"}` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Error(t, Peer(strings.NewReader(tc.input), io.Discard, frames), "accepted early EOF or extra/mismatched request")
		})
	}
	t.Run("wrong response ID", func(t *testing.T) {
		bad := append([]Frame(nil), frames...)
		bad[1].Wire = json.RawMessage(`{"id":999,"result":{}}`)
		assert.Error(t, Peer(strings.NewReader("{\"id\":1,\"method\":\"one\"}\n"), io.Discard, bad), "accepted wrong response ID")
	})
}

func TestSyntheticFileBoundary(t *testing.T) {
	root := t.TempDir()
	{
		err := os.Symlink(filepath.Join(t.TempDir(), "private"), root+"/numbers.txt")
		require.NoError(t, err)
	}
	{

		_, err := readSynthetic(root)
		require.Error(t, err,
			"read symlink")
	}
	{

		err := os.Symlink(filepath.Join(t.TempDir(), "outside"), root+"/summary.txt")
		require.NoError(t, err)
	}
	{

		err := writeSynthetic(root, "sum=42\n")
		require.Error(t, err,
			"write symlink")
	}

}

// The strict peer checks the complete native turn payload, then either disconnects
// before completion or rejects Submit while Receive is waiting.
func TestRunSessionPreservesEarlyFailure(t *testing.T) {
	for _, mode := range []string{"disconnect", "submit-rejected", "unsupported-extension"} {
		t.Run(mode, func(t *testing.T) {
			b, err := fixtures.ReadFile("integration/lifecycle.jsonl")
			require.NoError(t, err)
			frames, err := Decode(bytes.NewReader(b))
			require.NoError(t, err)
			initialize, thread, turn, err := Params(frames)
			require.NoError(t, err)
			// Native optional states include omitted, explicit null, false and nonempty
			// values; the peer compares the native payload, not the adapter's struct.
			turn.Model = types.Some("explicit-model")
			turn.Cwd = types.Some("/fixture/project")
			turn.Effort = types.Some("high")
			turn.ServiceTier = types.Null[string]()
			turn.ParentTurnID = types.Some("parent-turn")
			turn.RootTurnID = types.Some("root-turn")
			turn.ClientUserMessageID = types.Some("message-id")
			turn.ResponsesapiClientMetadata = types.Some(map[string]string{"native": "retained"})
			turn.OutputSchema = types.Some(json.RawMessage(`{"type":"object","additionalProperties":false}`))
			turn.DisabledPluginIDs = types.Some([]string{"plugin-a"})
			turn.RuntimeWorkspaceRoots = types.Some([]types.AbsolutePathBuf{"/fixture/project"})
			turn.Environments = types.Some([]types.TurnEnvironmentParams{})
			wire, err := json.Marshal(turn)
			require.NoError(t, err)
			exe, err := os.Executable()
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: exe, Args: []string{"--session-failure-peer", mode}, Env: []string{"AGENT_SDK_TESTIFY_BIN=" + os.Getenv("AGENT_SDK_TESTIFY_BIN"), "EXPECTED_TURN=" + string(wire)}, Initialize: initialize})
			require.NoError(t, err)
			defer c.Close()
			session, err := codex.Open(ctx, codex.SessionOptions{Client: c, Thread: thread})
			require.NoError(t, err)
			defer func() { _ = c.Close(); _ = session.Close() }()
			if mode == "unsupported-extension" {
				turn.UnknownFields = map[string]json.RawMessage{"futureExecution": json.RawMessage(`{"opaque":true}`)}
			}
			result, err := RunSession(ctx, session, turn, &Handler{})
			require.Error(t, err)
			require.False(t, result.Completed)
			require.NotErrorIs(t, err, context.DeadlineExceeded)
			if mode == "unsupported-extension" {
				require.ErrorIs(t, err, codex.ErrInvalidInput)
			} else if mode == "submit-rejected" {
				var rpc *codex.RPCError
				require.ErrorAs(t, err, &rpc)
				require.EqualValues(t, -32042, rpc.Code)
			} else {
				require.ErrorIs(t, err, io.EOF)
			}
		})
	}
}

func init() {
	if len(os.Args) == 3 && os.Args[1] == "--session-failure-peer" {
		b, _ := fixtures.ReadFile("integration/lifecycle.jsonl")
		frames, err := Decode(bytes.NewReader(b))
		if err != nil {
			os.Exit(31)
		}
		frames = frames[:5]
		if os.Args[2] != "unsupported-extension" {
			frames = append(frames, frame(6, "c2s", `{"id":103,"method":"turn/start","params":`+os.Getenv("EXPECTED_TURN")+`}`))
			if os.Args[2] == "disconnect" {
				frames = append(frames, Frame{Ordinal: 7, Direction: "s2c", Epoch: 1, Channel: "protocol", Disconnect: true})
			} else {
				frames = append(frames, frame(7, "s2c", `{"id":103,"error":{"code":-32042,"message":"synthetic rejected input"}}`))
			}
		}
		err = runPeer(context.Background(), os.Stdin, os.Stdout, frames, false)
		if err != nil && err != ErrDisconnected {
			os.Exit(32)
		}
		os.Exit(0)
	}

	if len(os.Args) == 2 && os.Args[1] == "--synthetic-peer" {
		b, _ := fixtures.ReadFile("integration/lifecycle.jsonl")
		f, e := Decode(bytes.NewReader(b))
		if e == nil {
			e = Peer(os.Stdin, os.Stdout, f)
		}
		if e != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
}

func (s *ManagedRecordingSuite) TestSyntheticManagedSession() {
	t := s.T()
	// The explicit synthetic peer must work even when PATH offers a poisoned CLI.
	poison := t.TempDir()
	sentinel := filepath.Join(poison, "started")
	require.NoError(t, os.WriteFile(filepath.Join(poison, "codex"), []byte("#!/bin/sh\nprintf poison > '"+sentinel+"'\nexit 99\n"), 0700))
	t.Setenv("PATH", poison)
	t.Cleanup(func() {
		_, err := os.Stat(sentinel)
		require.True(t, os.IsNotExist(err), "real CLI discovered")
	})
	b, err := fixtures.ReadFile("integration/lifecycle.jsonl")
	require.NoError(t, err)

	frames, err := Decode(bytes.NewReader(b))
	require.NoError(t, err)

	init, thread, turn, err := Params(frames)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	executable := s.executable
	c, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: executable, Args: []string{"--synthetic-peer"}, Env: []string{"AGENT_SDK_TESTIFY_BIN=" + os.Getenv("AGENT_SDK_TESTIFY_BIN")}, Initialize: init})
	require.NoError(t, err)

	defer c.Close()
	root := t.TempDir()
	{
		err = os.WriteFile(root+"/numbers.txt", []byte("17\n25\n"), 0600)
		require.NoError(t, err)
	}

	session, err := codex.Open(ctx, codex.SessionOptions{Client: c, Thread: thread})
	require.NoError(t, err)
	defer func() { _ = c.Close(); _ = session.Close() }()
	result, err := RunSession(ctx, session, turn, &Handler{Root: root, Scenario: "dynamic-tools", replayClient: c})
	require.NoError(t, err)
	require.False(t, !result.Completed || result.Tools != 2,
		"missing lifecycle/tools")
	{

		err = c.Call(ctx, "replay/assert", nil, nil)
		require.NoError(t, err)
	}

}

// Ensure importing this internal tooling cannot resolve a real CLI implicitly.
var _ = exec.ErrNotFound

// ManagedRecordingSuite uses only this test binary as the protocol peer.
// Every test gets its own executable lookup and keeps bounded lifecycle cleanup.
type ManagedRecordingSuite struct {
	suite.Suite
	executable string
}

func TestManagedRecording(t *testing.T) { suite.Run(t, new(ManagedRecordingSuite)) }

func (s *ManagedRecordingSuite) SetupTest() {
	executable, err := os.Executable()
	require.NoError(s.T(), err)
	s.executable = executable
}

func TestStoryPeerRequiresAnswerSettlementAndOrdinaryEOF(t *testing.T) {
	frames := []Frame{
		frame(1, "c2s", `{"id":1,"method":"echo","params":{}}`),
		frame(2, "s2c", `{"id":1,"result":{}}`),
		frame(3, "s2c", `{"id":"native-1","method":"future/request","params":{}}`),
		frame(4, "c2s", `{"id":"native-1","result":{}}`),
		frame(5, "s2c", `{"method":"serverRequest/resolved","params":{"requestId":"native-1"}}`),
	}
	prefix := "{\"id\":11,\"method\":\"echo\",\"params\":{}}\n{\"id\":\"native-1\",\"result\":{}}\n"
	for _, tc := range []struct {
		name, tail string
		bad        bool
	}{
		{"settled EOF", "{\"id\":12,\"method\":\"replay/answer\"}\n", false},
		{"missing settlement", "", true},
		{"wrong settlement", "{\"id\":12,\"method\":\"different\"}\n", true},
		{"extra request", "{\"id\":12,\"method\":\"replay/answer\"}\n{}\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := RunStoryPeer(context.Background(), strings.NewReader(prefix+tc.tail), &out, frames)
			if tc.bad {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Contains(t, out.String(), `"id":11`)
				assert.Less(t, strings.Index(out.String(), `"id":12`), strings.Index(out.String(), `"method":"serverRequest/resolved"`))
			}
		})
	}
}
