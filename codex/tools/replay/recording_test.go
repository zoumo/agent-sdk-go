package recording

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
func TestIDCorrelationAndEarlyNotification(t *testing.T) {
	f := []Frame{frame(1, "c2s", `{"id":101,"method":"one"}`), frame(2, "c2s", `{"id":102,"method":"two"}`), frame(3, "s2c", `{"method":"early","params":{"itemId":"item-001"}}`), frame(4, "s2c", `{"id":102,"result":{}}`), frame(5, "s2c", `{"id":101,"result":{}}`), frame(6, "s2c", `{"id":"reverse-1","method":"item/tool/call","params":{"callId":"item-001"}}`), frame(7, "c2s", `{"id":"reverse-1","result":{"success":true}}`)}
	in := `{"id":7,"method":"one"}` + "\n" + `{"id":"8","method":"two"}` + "\n" + `{"id":"reverse-1","result":{"success":true}}` + "\n" + `{"id":9,"method":"replay/assert"}` + "\n"
	var out bytes.Buffer
	{
		err := Peer(strings.NewReader(in), &out, f)
		require.NoError(t, err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.False(t, !strings.Contains(lines[1], `"id":"8"`) || !strings.Contains(lines[2], `"id":7`),
		"numeric/string dynamic IDs not preserved")

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

func TestCaptureBounds(t *testing.T) {
	var out bytes.Buffer
	capture := NewCapture(&out, 18)
	for _, tc := range []struct {
		name, direction, wire string
		wantError             bool
	}{
		{"first request", "c2s", `{"id":1}`, false},
		{"response below cutoff", "s2c", `{"id":2}`, false},
		{"byte cutoff", "s2c", `{}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := capture.Frame(tc.direction, []byte(tc.wire))
			if tc.wantError {
				assert.Error(t, err, "no byte cutoff")
			} else {
				require.NoError(t, err)
			}
		})
	}
	assert.Equal(t, 2, strings.Count(out.String(), "\n"), "published over-limit frame")
}

func TestLiveGuard(t *testing.T) {
	for _, tc := range []struct {
		name, enabled, ci, mode string
		wantError               bool
	}{
		{"default disabled", "0", "", "record", true},
		{"CI disabled", "1", "1", "record", true},
		{"wrong mode disabled", "1", "", "replay", true},
		{"explicit record enabled", "1", "", "record", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SDK_REAL_AGENT_E2E", tc.enabled)
			t.Setenv("CI", tc.ci)
			err := Guard(tc.mode)
			if tc.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
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
			b, err := fixtures.ReadFile("synthetic-faults/lifecycle/protocol.jsonl")
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
			c, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: exe, Args: []string{"--session-failure-peer", mode}, Env: []string{"EXPECTED_TURN=" + string(wire)}, Initialize: initialize})
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
		b, _ := fixtures.ReadFile("synthetic-faults/lifecycle/protocol.jsonl")
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
		b, _ := fixtures.ReadFile("synthetic-faults/lifecycle/protocol.jsonl")
		f, e := Decode(bytes.NewReader(b))
		if e == nil {
			e = Peer(os.Stdin, os.Stdout, f)
		}
		if e != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	if len(os.Args) != 3 || os.Args[1] != "--peer" {
		return
	}
	f, err := Load(os.Args[2])
	if err == nil {
		err = Peer(os.Stdin, os.Stdout, f)
	}
	if err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
func (s *ManagedRecordingSuite) TestSyntheticManagedSession() {
	t := s.T()
	b, err := fixtures.ReadFile("synthetic-faults/lifecycle/protocol.jsonl")
	require.NoError(t, err)

	frames, err := Decode(bytes.NewReader(b))
	require.NoError(t, err)

	init, thread, turn, err := Params(frames)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	executable := s.executable
	c, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: executable, Args: []string{"--synthetic-peer"}, Env: []string{}, Initialize: init})
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
	result, err := RunSession(ctx, session, turn, &Handler{Root: root, Story: "s1"})
	require.NoError(t, err)
	require.False(t, !result.Completed || result.Tools != 2,
		"missing lifecycle/tools")
	{

		err = c.Call(ctx, "replay/assert", nil, nil)
		require.NoError(t, err)
	}

}
func (s *ManagedRecordingSuite) TestRecordedManagedIntegration() {
	t := s.T()
	{
		_, err := fixtures.ReadFile("recordings/real-sanitized/s1/protocol.jsonl")
		require.NoError(t, err,
			"required recorded fixture missing")
	}

	// An executable named codex would leave a sentinel if accidentally started.
	poison := t.TempDir()
	sentinel := poison + "/started"
	{
		err := os.WriteFile(poison+"/codex", []byte("#!/bin/sh\nprintf poison > '"+sentinel+"'\nexit 99\n"), 0700)
		require.NoError(t, err)
	}

	t.Setenv("PATH", poison)
	executable := s.executable

	for _, story := range []string{"s1", "s2"} {
		for i := 0; i < 3; i++ {
			s.Run(fmt.Sprintf("%s/repetition-%d", story, i+1), func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				require.NoError(s.T(), Replay(ctx, executable, story, io.Discard))
			})
		}
	}
	{
		_, err := os.Stat(sentinel)
		require.True(t, os.IsNotExist(err),
			"real CLI discovered")
	}

}
func TestProxyChild(t *testing.T) {
	if os.Getenv("RECORD_PROXY_FAKE") != "1" {
		return
	}
	fmtLine := []byte("{\"method\":\"synthetic\",\"params\":{}}\n")
	if os.Getenv("RECORD_PROXY_OVERFLOW") == "1" {
		for i := 0; i < 100000; i++ {
			_, _ = os.Stdout.Write(fmtLine)
		}
	} else {
		_, _ = os.Stdout.Write(fmtLine)
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
	os.Exit(0)
}
func (s *ManagedRecordingSuite) TestProxyOfflineCleanupAndLimit() {
	t := s.T()
	executable := s.executable

	for _, overflow := range []bool{false, true} {
		dir := t.TempDir()
		env := []string{"RECORD_PROXY_FAKE=1"}
		if overflow {
			env = append(env, "RECORD_PROXY_OVERFLOW=1")
		}
		reader, writer := io.Pipe()
		defer reader.Close()
		defer writer.Close()
		if !overflow {
			go writer.Close()
		}
		result, e := Proxy(ProxyConfig{Executable: executable, Args: []string{"-test.run=^TestProxyChild$"}, Env: env, Dir: dir, RawDir: dir, Seconds: 3}, reader, io.Discard)
		require.False(t, !result.Reaped || !result.CleanupComplete,
			"cleanup incomplete")
		require.False(t, overflow && e == nil,
			"overflow succeeded")
		require.False(t, !overflow && e != nil,
			e)

	}
}
func (s *ManagedRecordingSuite) TestProxyOfflineDeadline() {
	t := s.T()
	executable := s.executable
	dir := t.TempDir()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	start := time.Now()
	result, err := Proxy(ProxyConfig{Executable: executable, Args: []string{"-test.run=^TestProxyChild$"}, Env: []string{"RECORD_PROXY_FAKE=1"}, Dir: dir, RawDir: dir, Seconds: 1}, reader, io.Discard)
	require.False(t, err == nil || result.Reason != "deadline" || !result.Reaped || !result.CleanupComplete || time.Since(start) > 5*time.Second,
		"deadline cleanup contract")

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
