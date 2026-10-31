package recording

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

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

func frame(n int, d, w string) Frame {
	return Frame{Ordinal: n, Direction: d, Epoch: 1, Channel: "protocol", Wire: json.RawMessage(w)}
}

func TestGoMockAdapterPreservesNativeLexemes(t *testing.T) {
	wire := `{"method" : "notice", "params" : {"html":"<tag>","number":0.123456789012345678901234567890} }`
	var out bytes.Buffer
	require.NoError(t, mockPeer(context.Background(), strings.NewReader(""), &out, []Frame{frame(1, "s2c", wire)}, ""))
	require.Equal(t, wire+"\n", out.String())
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
		err = mockPeer(context.Background(), os.Stdin, os.Stdout, frames, "")
		if err != nil && err != mocktest.ErrDisconnected {
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
