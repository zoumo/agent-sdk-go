// SDK integration against the in-process library mock; all scenarios are synthetic.
package claude_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	agenttestify "github.com/zoumo/agent-sdk-testify"

	"github.com/zoumo/agent-sdk-go/claude"
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
		frame["direction"] = map[string]string{">": "client", "<": "server", "!": "disconnect"}[direction]
		if direction != "!" {
			frame["data"] = json.RawMessage(data)
		}
		frames = append(frames, frame)
	}
	b, err := json.Marshal(map[string]any{"version": 1, "provenance": "synthetic", "frames": frames})
	require.NoError(t, err)
	return b
}

var mockClaudeInit = []string{
	`>{"type":"control_request","request_id":"init","request":{"subtype":"initialize","hooks":null,"forwardSubagentText":true}}`,
	`<{"type":"control_response","response":{"subtype":"success","request_id":"init","response":{}}}`,
}

func (s *mockCLISuite) TestInteractions() {
	for _, mode := range []string{"permission-and-interrupt", "disconnect", "mismatch"} {
		s.Run(mode, func() {
			t := s.T()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			frames := append([]string(nil), mockClaudeInit...)
			frames = append(frames, `>{"type":"user","message":{"role":"user","content":"hello"},"parent_tool_use_id":null,"session_id":""}`)
			switch mode {
			case "permission-and-interrupt":
				frames = append(frames,
					`<{"type":"control_request","request_id":"permission","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","input":{"questions":[{"question":"Which format?","options":[{"label":"json"}]}]},"tool_use_id":"tool"}}`,
					`>{"type":"control_response","response":{"subtype":"success","request_id":"permission","response":{"behavior":"allow","updatedInput":{"questions":[{"question":"Which format?","options":[{"label":"json"}]}],"answers":{"Which format?":"json"}}}}}`,
					`<{"type":"result","subtype":"success","session_id":"synthetic","is_error":false,"result":"answered"}`,
					`>{"type":"control_request","request_id":"interrupt","request":{"subtype":"interrupt"}}`,
					`<{"type":"control_response","response":{"subtype":"success","request_id":"interrupt","response":{}}}`)
			case "disconnect":
				frames = append(frames, `!`)
			default:
				frames[len(frames)-1] = `>{"type":"user","message":{"role":"user","content":"different"},"parent_tool_use_id":null,"session_id":""}`
			}
			peerSession, err := mocktest.Prepare(t.TempDir(), "claude", mockCLIScenario(t, frames...), mocktest.Options{})
			require.NoError(t, err)
			wantStatus := "passed"
			if mode != "permission-and-interrupt" {
				wantStatus = "failed"
			}
			t.Cleanup(func() { verifyMockSession(t, peerSession, wantStatus) })
			client, run := openMockSession(t, ctx, peerSession)
			require.NoError(t, submitMockCLIInput(ctx, client, "hello"))
			if mode != "permission-and-interrupt" {
				for {
					_, err := client.Receive(ctx)
					if err != nil {
						require.NotErrorIs(t, err, context.DeadlineExceeded)
						require.NoError(t, ctx.Err())
						closeErr := client.Close()
						if mode == "disconnect" {
							require.Error(t, closeErr)
							requireStrictReplayFailure(t, ctx, run)
						} else {
							require.Error(t, closeErr)
						}
						break
					}
				}
				return
			}
			permission := receiveMockCLIRequest(t, ctx, client)
			require.NoError(t, permission.Answer(ctx, claude.QuestionAnswers{Answers: map[string]string{"Which format?": "json"}}))

			for {
				event, err := client.Receive(ctx)
				require.NoError(t, err)
				if event := event.Event; event != nil && event.Result != nil {
					assert.Equal(t, "answered", event.Result.Result)
					break
				}
			}
			canceled, stop := context.WithCancel(ctx)
			stop()
			require.ErrorIs(t, client.Interrupt(canceled, claude.InterruptOptions{}), context.Canceled)
			require.NoError(t, client.Interrupt(ctx, claude.InterruptOptions{}))
			require.NoError(t, client.Close())
		})
	}
}

// Completion must retain the final server result and wait for authoritative
// library evidence, even when full consumption races the last SDK write.
func (s *mockCLISuite) TestNormalCompletion() {
	t := s.T()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`>{"type":"user","message":{"role":"user","content":"hello"},"parent_tool_use_id":null,"session_id":""}`,
		`<{"type":"system","subtype":"init","session_id":"synthetic"}`,
		`<{"type":"result","subtype":"success","session_id":"synthetic","is_error":false,"result":"final frame"}`,
	)
	fixture, err := mocktest.Prepare(t.TempDir(), "claude", mockCLIScenario(t, frames...), mocktest.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { verifyMockSession(t, fixture, "passed") })
	client, run := openMockSession(t, ctx, fixture)
	require.NoError(t, submitMockCLIInput(ctx, client, "hello"))
	result := receiveStreamEvent(t, client, func(event *claude.Event) bool { return event.Result != nil })
	require.Equal(t, "final frame", result.Event.Result.Result)
	select {
	case <-client.Done():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, client.Err())
	require.NoError(t, client.Close())
	require.NoError(t, client.Close())
	evidence, err := run.Wait(ctx)
	require.NoError(t, err)
	require.True(t, evidence.Execution.Complete)
	require.Len(t, evidence.Execution.Connections, 1)
	connection := evidence.Execution.Connections[0]
	require.Equal(t, "in-process", string(connection.Transport))
	require.True(t, connection.FullConsumption)
	require.True(t, connection.ClientEOF)
	require.True(t, connection.CleanupComplete)
	require.False(t, connection.NativeLaunched)
	require.Nil(t, connection.ExitCode)
	require.Empty(t, connection.ProcessReceiptPath)
}

// Controls and answers must remain available during an unread event burst.
// After disconnection, every queued event must drain in order.
func (s *mockCLISuite) TestSlowConsumerBurst() {
	const count = 2048
	t := s.T()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames, `>{"type":"user","message":{"role":"user","content":"hello"},"parent_tool_use_id":null,"session_id":""}`, `<{"type":"system","subtype":"init","session_id":"synthetic","claude_code_version":"2.1.258"}`, `<{"type":"control_request","request_id":"permission","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","input":{"questions":[]},"tool_use_id":"tool"}}`)
	for i := range count {
		frames = append(frames, fmt.Sprintf(`<{"type":"assistant","session_id":"synthetic","message":{"content":[]},"index":%d}`, i))
	}
	frames = append(frames, `>{"type":"control_request","request_id":"interrupt","request":{"subtype":"interrupt"}}`, `<{"type":"control_response","response":{"subtype":"success","request_id":"interrupt","response":{}}}`, `>{"type":"control_response","response":{"subtype":"success","request_id":"permission","response":{"behavior":"allow","updatedInput":{"questions":[]}}}}`, `>{"type":"user","message":{"role":"user","content":"release"},"parent_tool_use_id":null,"session_id":""}`, `!`)
	peerSession, err := mocktest.Prepare(t.TempDir(), "claude", mockCLIScenario(t, frames...), mocktest.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { verifyMockSession(t, peerSession, "failed") })
	client, run := openMockSession(t, ctx, peerSession)
	first, err := client.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "forwarding-not-confirmed", first.Observation.Reason)
	require.NoError(t, submitMockCLIInput(ctx, client, "hello"))
	root, err := client.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "init", root.Event.Subtype)
	permission := receiveMockCLIRequest(t, ctx, client)
	require.NoError(t, client.Interrupt(ctx, claude.InterruptOptions{}))
	require.NoError(t, permission.Approve(ctx, claude.ApprovalOptions{}))
	err = submitMockCLIInput(ctx, client, "release")
	require.NotErrorIs(t, err, context.DeadlineExceeded)
	select {
	case <-client.Done():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.Error(t, client.Err())
	canceled, stop := context.WithCancel(ctx)
	stop()
	_, err = client.Receive(canceled)
	require.ErrorIs(t, err, context.Canceled)
	seen := 0
	for {
		event, err := client.Receive(ctx)
		if err != nil {
			require.NotErrorIs(t, err, context.DeadlineExceeded)
			require.Equal(t, count, seen)
			break
		}
		require.NotNil(t, event.Event)
		require.Equal(t, "assistant", event.Event.Type)
		var payload struct{ Index int }
		require.NoError(t, json.Unmarshal(event.Raw, &payload))
		require.Equal(t, seen, payload.Index)
		seen++
	}
	require.Error(t, client.Close())
	requireStrictReplayFailure(t, ctx, run)
}

func openMockSession(t *testing.T, ctx context.Context, peerSession *mocktest.Session) (*claude.Session, *mocktest.Run) {
	t.Helper()
	options := claude.SessionOptions{
		CLIPath:           "must-not-start",
		WorkDir:           "",
		Env:               []string{},
		InitializeTimeout: 2 * time.Second,
		ShutdownTimeout:   time.Second,
	}
	args, err := claude.NativeArgs(options)
	require.NoError(t, err)
	run, err := peerSession.NewRun(ctx, mocktest.RunOptions{NativeArgs: args})
	require.NoError(t, err)
	options.WorkDir, options.Env = run.Start.Workspace, run.Environment()
	actualArgs, err := claude.NativeArgs(options)
	require.NoError(t, err)
	require.Equal(t, run.Start.NativeArgs, actualArgs)
	require.Equal(t, run.Start.Config, run.Start.Env["CLAUDE_CONFIG_DIR"])
	client, err := claude.OpenStreams(ctx, options, claude.StreamTransport{
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

func submitMockCLIInput(ctx context.Context, session *claude.Session, text string) error {
	_, err := session.Submit(ctx, claude.Input{Text: text})
	return err
}

func receiveMockCLIRequest(t *testing.T, ctx context.Context, session *claude.Session) *claude.IncomingRequest {
	t.Helper()
	for {
		record, err := session.Receive(ctx)
		require.NoError(t, err)
		if record.Request != nil {
			return record.Request
		}
	}
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
