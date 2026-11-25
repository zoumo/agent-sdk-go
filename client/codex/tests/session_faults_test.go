package codex_test

import (
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/codex"
	ct "github.com/zoumo/agent-sdk-go/client/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/testsupport"
	codextest "github.com/zoumo/agent-sdk-go/internal/testsupport/codex"
	recording "github.com/zoumo/agent-sdk-go/internal/testsupport/codex/replay"
	"github.com/zoumo/agent-sdk-go/internal/testsupport/mocktest"
	"github.com/zoumo/agent-sdk-go/internal/testsupport/replaycheck"
)

func TestQuestionDrain(t *testing.T) {
	frames, err := recording.QuestionFrames()
	require.NoError(t, err)
	init, thread, turn, err := recording.Params(frames)
	require.NoError(t, err)
	data, err := json.Marshal(map[string]any{"version": 1, "provenance": "synthetic", "frames": frames})
	require.NoError(t, err)
	fixture, err := mocktest.Prepare(t.TempDir(), "codex", data, mocktest.Options{Handshakes: "answer-and-assert"})
	require.NoError(t, err)
	c, run := openEmbeddedCodex(t, fixture, init)
	session, err := codex.Open(testsupport.Deadline(t), codex.SessionOptions{Client: c, Thread: thread})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Abort() })
	_, err = session.Submit(testsupport.Deadline(t), codex.Input{Input: turn.Input, Environments: turn.Environments})
	require.NoError(t, err)
	var got []json.RawMessage
	var request *codex.IncomingRequest
	for request == nil {
		r, err := session.Receive(testsupport.Deadline(t))
		require.NoError(t, err)
		if r.Event != nil {
			got = append(got, r.Raw)
		}
		request = r.Request
	}
	require.Equal(t, "item/tool/requestUserInput", request.Method())
	var q ct.ToolRequestUserInputParams
	require.NoError(t, json.Unmarshal(request.Params(), &q))
	require.Len(t, q.Questions, 1)
	answer := ct.ToolRequestUserInputResponse{Answers: map[string]ct.ToolRequestUserInputAnswer{q.Questions[0].ID: {Answers: []string{"Amber"}}}}
	require.NoError(t, request.Answer(testsupport.Deadline(t), answer))
	require.Error(t, request.Answer(testsupport.Deadline(t), answer), "original handle is single-use")
	require.NoError(t, c.Call(testsupport.Deadline(t), "replay/answer", nil, nil))
	// No further Receive until every native frame has been emitted and the
	// strict matcher has checked the original single correlated answer.
	require.NoError(t, c.Call(testsupport.Deadline(t), "replay/assert", nil, nil))
	for {
		r, err := session.Receive(testsupport.Deadline(t))
		require.NoError(t, err)
		if r.Event != nil {
			got = append(got, r.Raw)
			if r.Event.Method == "turn/completed" {
				break
			}
		}
	}
	require.NoError(t, c.Close()) // closes peer input; strict EOF rejects any extra frame
	testsupport.Wait(t, testsupport.Deadline(t), session.Done())
	for {
		r, err := session.Receive(testsupport.Deadline(t))
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			assert.NoError(t, session.Err())
			break
		}
		if r.Event != nil {
			got = append(got, r.Raw)
		}
	}
	var expected []json.RawMessage
	for _, f := range frames {
		var wire struct {
			Method string
			ID     json.RawMessage
		}
		require.NoError(t, json.Unmarshal(f.Wire, &wire))
		if f.Direction == "s2c" && wire.Method != "" && len(wire.ID) == 0 {
			expected = append(expected, f.Wire)
		}
	}
	replaycheck.RequireSuccess(t, run)
	require.Len(t, got, len(expected))
	for i := range expected {
		assert.JSONEq(t, string(expected[i]), string(got[i]), "original ordinal sequence %d", i)
	}
	require.Error(t, request.Answer(testsupport.Deadline(t), answer), "Close cannot restore old authority")
}

func TestFaultClosePendingRequest(t *testing.T) {
	ctx := testsupport.Deadline(t)
	var old *codex.IncomingRequest
	for epoch := range 2 {
		o, path := codexTargetOptions(t, "session-canceled-approval")
		c, err := codex.NewStdio(ctx, o)
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Close() })
		session, err := codex.Open(ctx, codex.SessionOptions{Client: c})
		require.NoError(t, err)
		t.Cleanup(func() { _ = session.Close() })
		_, err = session.Submit(ctx, codex.Input{})
		require.NoError(t, err)
		pending := codextest.Requests(t, session, 2)[0]
		if epoch == 0 {
			old = pending
		} else {
			require.Equal(t, old.ID(), pending.ID(), "synthetic peer deliberately reuses the native ID in a fresh connection")
			require.Error(t, old.Approve(ctx, codex.ApprovalOptions{}), "fresh connection cannot restore old answer authority")
			require.NoError(t, pending.Deny(ctx, codex.ApprovalOptions{}))
		}
		require.NoError(t, session.Close())
		testsupport.Wait(t, ctx, pending.Context().Done())
		require.Error(t, pending.Deny(ctx, codex.ApprovalOptions{}))
		require.Error(t, pending.Deny(ctx, codex.ApprovalOptions{}))
		require.NoError(t, c.Close())
		assert.Equal(t, epoch, testsupport.CountAnswers(t, path, "codex"))
	}
}

func openEmbeddedCodex(t *testing.T, fixture *mocktest.Session, init ct.InitializeParams) (*codex.Client, *mocktest.Run) {
	t.Helper()
	run := replaycheck.Start(t, fixture, nil)
	client, err := codex.NewStreams(testsupport.Deadline(t), codex.StreamOptions{
		Args: run.Start.NativeArgs, Env: run.Environment(), Dir: run.Start.ProtocolCwd, Initialize: init,
	}, codex.StreamTransport{Reader: run.Reader, Writer: run.Writer, Stderr: run.Stderr, Wait: run.WaitError})
	require.NoError(t, err)
	done := run.CloseInputWhenComplete(client.CloseInput)
	t.Cleanup(func() {
		_ = client.Close()
		select {
		case err := <-done:
			if err != nil {
				assert.ErrorIs(t, err, codex.ErrInputClosed)
			}
		case <-time.After(5 * time.Second):
			t.Error("input completion watcher did not finish")
		}
	})
	return client, run
}
