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
	agenttestify "github.com/zoumo/agent-sdk-testify"

	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

// ManagedRecordingSuite exercises synthetic fixtures through in-process streams.
type ManagedRecordingSuite struct{ suite.Suite }

func frame(n int, d, w string) Frame {
	return Frame{Ordinal: n, Direction: d, Epoch: 1, Channel: "protocol", Wire: json.RawMessage(w)}
}

func TestGoMockAdapterPreservesNativeLexemes(t *testing.T) {
	wire := `{"method" : "notice", "params" : {"html":"<tag>","number":0.123456789012345678901234567890} }`

	data, err := syntheticScenario([]Frame{frame(1, "s2c", wire)})
	require.NoError(t, err)
	fixture, err := mocktest.Prepare(t.TempDir(), "codex", data, mocktest.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fixture.Close()) })
	run, err := fixture.NewRun(t.Context(), mocktest.RunOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = run.CloseSDK() })
	closed := run.CloseInputWhenComplete(run.Writer.Close)
	out, err := io.ReadAll(run.Reader)
	require.NoError(t, err)
	require.Equal(t, wire+"\n", string(out))
	require.NoError(t, <-closed)
	require.NoError(t, run.WaitError(t.Context()))
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
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			frames = frames[:5]
			if mode != "unsupported-extension" {
				frames = append(frames, frame(6, "c2s", `{"id":103,"method":"turn/start","params":`+string(wire)+`}`))
				if mode == "disconnect" {
					frames = append(frames, Frame{Ordinal: 7, Direction: "s2c", Epoch: 1, Channel: "protocol", Disconnect: true})
				} else {
					frames = append(frames, frame(7, "s2c", `{"id":103,"error":{"code":-32042,"message":"synthetic rejected input"}}`))
				}
			}
			wantStatus := "passed"
			if mode == "disconnect" {
				wantStatus = "failed"
			}
			c := replayClient(t, ctx, frames, "", initialize, wantStatus)
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
			switch mode {
			case "unsupported-extension":
				require.ErrorIs(t, err, codex.ErrInvalidInput)
			case "submit-rejected":
				var rpc *codex.RPCError
				require.ErrorAs(t, err, &rpc)
				require.EqualValues(t, -32042, rpc.Code)
			default:
				require.ErrorIs(t, err, codex.ErrUnexpectedEOF)
				require.Error(t, c.Close())
			}
		})
	}
}

func (s *ManagedRecordingSuite) TestSyntheticManagedSession() {
	t := s.T()
	// The explicit synthetic peer must work even when PATH offers a poisoned CLI.
	poison := t.TempDir()
	sentinel := filepath.Join(poison, "started")
	require.NoError(t, os.WriteFile(filepath.Join(poison, "codex"), []byte("#!/bin/sh\nprintf poison > '"+sentinel+"'\nexit 99\n"), 0o700))
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
	c := replayClient(t, ctx, frames, "answer-and-assert", init, "passed")

	defer c.Close()
	root := t.TempDir()
	err = os.WriteFile(root+"/numbers.txt", []byte("17\n25\n"), 0o600)
	require.NoError(t, err)

	session, err := codex.Open(ctx, codex.SessionOptions{Client: c, Thread: thread})
	require.NoError(t, err)
	defer func() { _ = c.Close(); _ = session.Close() }()
	result, err := RunSession(ctx, session, turn, &Handler{Root: root, Scenario: "dynamic-tools", replayClient: c})
	require.NoError(t, err)
	require.False(t, !result.Completed || result.Tools != 2,
		"missing lifecycle/tools")
	err = c.Call(ctx, "replay/assert", nil, nil)
	require.NoError(t, err)
}

func TestManagedRecording(t *testing.T) { suite.Run(t, new(ManagedRecordingSuite)) }

func replayClient(t *testing.T, ctx context.Context, frames []Frame, handshakes string, initialize types.InitializeParams, wantStatus string) *codex.Client {
	t.Helper()
	data, err := syntheticScenario(frames)
	require.NoError(t, err)
	fixture, err := mocktest.Prepare(t.TempDir(), "codex", data, mocktest.Options{Handshakes: handshakes})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fixture.Close()) })
	run, err := fixture.NewRun(ctx, mocktest.RunOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = run.CloseSDK() })
	client, err := codex.NewStreams(ctx, codex.StreamOptions{
		Args: run.Start.NativeArgs, Env: run.Environment(), Dir: run.Start.ProtocolCwd, Initialize: initialize,
	}, codex.StreamTransport{Reader: run.Reader, Writer: run.Writer, Stderr: run.Stderr, Wait: run.WaitError})
	if err != nil {
		_ = run.Abort()
	}
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.Close()
		waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result, waitErr := run.Wait(waitCtx)
		if wantStatus == "passed" {
			require.NoError(t, waitErr)
		} else {
			require.Error(t, waitErr)
		}
		require.NotNil(t, result)
		require.NotNil(t, result.Execution)
		require.Equal(t, wantStatus, string(result.Execution.Status))
		if wantStatus == "failed" {
			require.Len(t, result.Execution.Connections, 1)
			connection := result.Execution.Connections[0]
			require.Equal(t, agenttestify.FailureClassStrictReplay, connection.Failure)
			require.False(t, connection.NativeLaunched)
			require.Nil(t, connection.ExitCode)
			require.Empty(t, connection.ProcessReceiptPath)
		}
	})
	run.CloseInputWhenComplete(client.CloseInput)
	return client
}

type (
	readerOnly struct{ io.Reader }
	writerOnly struct{ io.Writer }
)

func TestSyntheticPeerRejectsUninterruptibleStreams(t *testing.T) {
	for _, direction := range []string{"input", "output"} {
		t.Run(direction, func(t *testing.T) {
			reader, writer := io.Pipe()
			t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
			var input io.Reader = strings.NewReader("")
			var output io.Writer = io.Discard
			if direction == "input" {
				input = readerOnly{reader}
			} else {
				output = writerOnly{writer}
			}
			done := make(chan error, 1)
			go func() { done <- Peer(input, output, []Frame{frame(1, "s2c", `{"method":"notice"}`)}) }()
			select {
			case err := <-done:
				require.ErrorContains(t, err, "closeable "+direction)
			case <-time.After(time.Second):
				_ = reader.Close()
				_ = writer.Close()
				<-done
				t.Fatal("synthetic peer accepted an uninterruptible stream")
			}
		})
	}
}
