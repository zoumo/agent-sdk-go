// SDK integration against the in-process library mock; all scenarios are synthetic.
package codex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	agenttestify "github.com/zoumo/agent-sdk-testify"

	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

type mockCLISuite struct {
	suite.Suite
}

func TestMockCLI(t *testing.T) { suite.Run(t, new(mockCLISuite)) }

func mockCLIScenario(t *testing.T, wires ...string) []byte {
	t.Helper()
	frames := make([]map[string]any, 0, len(wires))
	for i, wire := range wires {
		direction, data := wire[:1], wire[1:]
		frame := map[string]any{"ordinal": i + 1, "channel": "protocol"}
		frame["epoch"] = 1
		frame["direction"] = "s2c"
		if direction == ">" {
			frame["direction"] = "c2s"
		}
		if direction == "!" {
			frame["disconnect"] = true
		} else {
			frame["wire"] = json.RawMessage(data)
		}
		frames = append(frames, frame)
	}
	b, err := json.Marshal(map[string]any{"version": 1, "provenance": "synthetic", "frames": frames})
	require.NoError(t, err)
	return b
}

var mockCodexInit = []string{
	`>{"id":91,"method":"initialize","params":{"clientInfo":{"name":"consumer","version":"1"}}}`,
	`<{"id":91,"result":{"userAgent":"synthetic","codexHome":"/synthetic","platformFamily":"unix","platformOs":"linux"}}`,
	`>{"method":"initialized"}`,
}

var mockCodexOpen = []string{
	`>{"id":100,"method":"thread/start","params":{}}`,
	`<{"id":100,"result":{"thread":{"id":"root","sessionId":"synthetic","cliVersion":"0.162.1","createdAt":1,"updatedAt":1,"cwd":"/synthetic","ephemeral":true,"modelProvider":"synthetic","preview":"","projectId":null,"source":"appServer","status":{"type":"idle"},"turns":[]},"approvalPolicy":"never","approvalsReviewer":"user","cwd":"/synthetic","model":"synthetic","modelProvider":"synthetic","sandbox":{"type":"readOnly"}}}`,
}

func (s *mockCLISuite) TestInteractions() {
	for _, mode := range []string{"question-and-interrupt", "disconnect", "mismatch"} {
		s.Run(mode, func() {
			t := s.T()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			frames := append(append([]string(nil), mockCodexInit...), mockCodexOpen...)
			switch mode {
			case "question-and-interrupt":
				frames = append(frames,
					`>{"id":92,"method":"consumer/ask","params":{}}`,
					`<{"id":"question","method":"item/tool/requestUserInput","params":{"threadId":"root","turnId":"turn","itemId":"item","questions":[{"id":"format","header":"Format","question":"Which format?","options":[{"label":"json","description":"JSON output"}],"isOther":false,"isSecret":false}]}}`,
					`>{"id":"question","result":{"answers":{"format":{"answers":["json"]}}}}`,
					`<{"id":92,"result":{"answered":true}}`,
					`>{"id":93,"method":"turn/interrupt","params":{"threadId":"root","turnId":"turn"}}`,
					`<{"id":93,"result":{}}`)
			case "disconnect":
				frames = append(frames, `>{"id":92,"method":"consumer/fault","params":{}}`, `!`)
			default:
				frames = append(frames, `>{"id":92,"method":"consumer/expected","params":{}}`)
			}
			peerSession, err := mocktest.Prepare(t.TempDir(), "codex", mockCLIScenario(t, frames...), mocktest.Options{})
			require.NoError(t, err)
			wantStatus := "passed"
			if mode != "question-and-interrupt" {
				wantStatus = "failed"
			}
			t.Cleanup(func() { verifyMockSession(t, peerSession, wantStatus) })
			client, run := openMockClient(t, ctx, peerSession)
			session, err := codex.Open(ctx, codex.SessionOptions{Client: client})
			require.NoError(t, err)
			defer func() { _ = client.Close(); _ = session.Close() }()
			if mode != "question-and-interrupt" {
				var result any
				err = client.Call(ctx, "consumer/fault", map[string]any{}, &result)
				require.Error(t, err)
				require.NotErrorIs(t, err, context.DeadlineExceeded)
				require.NoError(t, ctx.Err())
				closeErr := client.Close()
				if mode == "disconnect" {
					require.Error(t, closeErr)
					requireStrictReplayFailure(t, ctx, run)
				} else {
					require.ErrorContains(t, closeErr, "client mismatch")
				}
				return
			}
			result := make(chan error, 1)
			go func() {
				var response struct {
					Answered bool `json:"answered"`
				}
				err := client.Call(ctx, "consumer/ask", map[string]any{}, &response)
				if err == nil && !response.Answered {
					err = context.Canceled
				}
				result <- err
			}()
			received, err := session.Receive(ctx)
			require.NoError(t, err)
			require.NotNil(t, received.Request)
			request := received.Request
			require.Equal(t, "item/tool/requestUserInput", request.Method())
			require.NoError(t, request.Answer(ctx, codextypes.ToolRequestUserInputResponse{Answers: map[string]codextypes.ToolRequestUserInputAnswer{"format": {Answers: []string{"json"}}}}))

			require.NoError(t, <-result)
			canceled, stop := context.WithCancel(ctx)
			stop()
			err = session.Interrupt(canceled, codex.InterruptOptions{TurnID: "turn"})
			require.ErrorIs(t, err, context.Canceled)
			err = session.Interrupt(ctx, codex.InterruptOptions{TurnID: "turn"})
			require.NoError(t, err)
			require.NoError(t, client.Close())
			assert.Zero(t, client.Diagnostics().StderrBytes)
		})
	}
}

// Controls and answers must remain available during an unread event burst.
// After disconnection, every queued event must drain in order.
func (s *mockCLISuite) TestSlowConsumerBurst() {
	const count = 2048
	t := s.T()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	frames := append(append([]string(nil), mockCodexInit...), mockCodexOpen...)
	frames = append(frames, `<{"id":"question","method":"item/tool/requestUserInput","params":{"threadId":"root","turnId":"turn","itemId":"item","questions":[]}}`)
	for i := range count {
		frames = append(frames, fmt.Sprintf(`<{"method":"fixture/burst","params":{"threadId":"root","index":%d}}`, i))
	}
	frames = append(frames, `>{"id":93,"method":"turn/interrupt","params":{"threadId":"root","turnId":"turn"}}`, `<{"id":93,"result":{}}`, `>{"id":"question","result":{"answers":{}}}`, `>{"id":94,"method":"consumer/release","params":{}}`, `!`)
	peerSession, err := mocktest.Prepare(t.TempDir(), "codex", mockCLIScenario(t, frames...), mocktest.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { verifyMockSession(t, peerSession, "failed") })
	client, run := openMockClient(t, ctx, peerSession)
	defer client.Close()
	session, err := codex.Open(ctx, codex.SessionOptions{Client: client})
	require.NoError(t, err)
	defer session.Abort()
	request, err := session.Receive(ctx)
	require.NoError(t, err)
	require.NotNil(t, request.Request)
	require.NoError(t, session.Interrupt(ctx, codex.InterruptOptions{TurnID: "turn"}))
	require.NoError(t, request.Request.Answer(ctx, codextypes.ToolRequestUserInputResponse{Answers: map[string]codextypes.ToolRequestUserInputAnswer{}}))
	require.Error(t, client.Call(ctx, "consumer/release", map[string]any{}, nil))
	select {
	case <-session.Done():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.Error(t, session.Err())
	seen := 0
	for {
		record, err := session.Receive(ctx)
		if err != nil {
			require.NotErrorIs(t, err, context.DeadlineExceeded)
			require.Equal(t, count, seen)
			break
		}
		require.NotNil(t, record.Event)
		require.Equal(t, "fixture/burst", record.Event.Method)
		var payload struct{ Index int }
		require.NoError(t, json.Unmarshal(record.Params, &payload))
		require.Equal(t, seen, payload.Index)
		seen++
	}
	require.Error(t, client.Close())
	requireStrictReplayFailure(t, ctx, run)
}

func openMockClient(t *testing.T, ctx context.Context, peerSession *mocktest.Session) (*codex.Client, *mocktest.Run) {
	t.Helper()
	run, err := peerSession.NewRun(ctx, mocktest.RunOptions{})
	require.NoError(t, err)
	client, err := codex.NewStreams(ctx, codex.StreamOptions{
		Args: run.Start.NativeArgs,
		Env:  run.Environment(),
		Dir:  run.Start.ProtocolCwd,
		Initialize: codextypes.InitializeParams{
			ClientInfo: codextypes.ClientInfo{Name: "consumer", Version: "1"},
		},
	}, codex.StreamTransport{
		Reader: run.Reader,
		Writer: run.Writer,
		Stderr: run.Stderr,
		Wait:   run.WaitError,
	})
	if err != nil {
		_ = run.Abort()
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = run.CloseSDK()
	})
	run.CloseInputWhenComplete(client.CloseInput)
	return client, run
}

func verifyMockSession(t *testing.T, session *mocktest.Session, want string) {
	t.Helper()
	report, err := session.Report()
	require.NotNil(t, report)
	require.NoError(t, err)
	require.Equal(t, want, report.Status)
	require.NoError(t, session.Close())
}

func requireStrictReplayFailure(t *testing.T, ctx context.Context, run *mocktest.Run) {
	t.Helper()
	result, err := run.Wait(ctx)
	require.Error(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Execution)
	require.Equal(t, agenttestify.ExecutionStatusFailed, result.Execution.Status)
	require.Len(t, result.Execution.Connections, 1)
	connection := result.Execution.Connections[0]
	require.Equal(t, agenttestify.FailureClassStrictReplay, connection.Failure)
	require.False(t, connection.NativeLaunched)
	require.Nil(t, connection.ExitCode)
	require.Empty(t, connection.ProcessReceiptPath)
}

// Normal replay completion must preserve queued final frames and close Receive.
func (s *mockCLISuite) TestNormalCompletionDrainsFinalFrame() {
	t := s.T()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	frames := append(append([]string(nil), mockCodexInit...), mockCodexOpen...)
	frames = append(frames,
		`>{"id":92,"method":"consumer/finish"}`,
		`<{"id":92,"result":{"complete":true}}`,
		`<{"method":"fixture/final","params":{"threadId":"root","value":"last"}}`)
	fixture, err := mocktest.Prepare(t.TempDir(), "codex", mockCLIScenario(t, frames...), mocktest.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { verifyMockSession(t, fixture, "passed") })
	client, run := openMockClient(t, ctx, fixture)
	session, err := codex.Open(ctx, codex.SessionOptions{Client: client})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Abort() })
	var response struct{ Complete bool }
	require.NoError(t, client.Call(ctx, "consumer/finish", nil, &response))
	require.True(t, response.Complete)
	select {
	case <-client.Done():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, client.Close())
	require.NoError(t, client.Err())
	closedCtx, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	require.ErrorIs(t, client.Call(closedCtx, "consumer/after-close", nil, nil), codex.ErrInputClosed)
	final, err := session.Receive(ctx)
	require.NoError(t, err)
	require.NotNil(t, final.Event)
	require.Equal(t, "fixture/final", final.Event.Method)
	select {
	case <-session.Done():
	default:
		t.Fatal("normal peer EOF did not finish the Session")
	}
	_, err = session.Receive(ctx)
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, session.Err())
	require.NoError(t, session.Close())
	result, err := run.Wait(ctx)
	require.NoError(t, err)
	require.NotNil(t, result.Execution)
	require.True(t, result.Execution.Complete)
	require.Len(t, result.Execution.Connections, 1)
	connection := result.Execution.Connections[0]
	require.True(t, connection.FullConsumption)
	require.True(t, connection.ClientEOF)
	require.False(t, connection.NativeLaunched)
	require.True(t, connection.CleanupComplete)
	require.Nil(t, connection.ExitCode)
	require.Empty(t, connection.ProcessReceiptPath)
}
