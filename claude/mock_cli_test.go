// SDK integration against the Go mock CLI; all scenarios are synthetic.
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
			client, _ := openMockSession(t, ctx, peerSession)
			require.NoError(t, submitMockCLIInput(ctx, client, "hello"))
			if mode != "permission-and-interrupt" {
				for {
					_, err := client.Receive(ctx)
					if err != nil {
						require.NotErrorIs(t, err, context.DeadlineExceeded)
						require.NoError(t, ctx.Err())
						closeErr := client.Close()
						if mode == "disconnect" {
							require.ErrorIs(t, closeErr, mocktest.ErrDisconnected)
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
	client, _ := openMockSession(t, ctx, peerSession)
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
	require.ErrorIs(t, client.Close(), mocktest.ErrDisconnected)
}

func openMockSession(t *testing.T, ctx context.Context, peerSession *mocktest.Session) (*claude.Session, *mocktest.Run) {
	t.Helper()
	run, err := peerSession.NewRun(ctx, mocktest.RunOptions{})
	require.NoError(t, err)
	client, err := claude.OpenStreams(ctx, claude.SessionOptions{
		CLIPath:           "must-not-start",
		WorkDir:           run.Start.Workspace,
		Env:               run.Environment(),
		InitializeTimeout: 2 * time.Second,
		ShutdownTimeout:   time.Second,
	}, claude.StreamTransport{
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
