package claude_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/claude"
	"github.com/zoumo/agent-sdk-go/internal/testsupport"
	claudetest "github.com/zoumo/agent-sdk-go/internal/testsupport/claude"
	transcript "github.com/zoumo/agent-sdk-go/internal/testsupport/claude/replay"
	"github.com/zoumo/agent-sdk-go/internal/testsupport/mocktest"
	"github.com/zoumo/agent-sdk-go/internal/testsupport/replaycheck"
)

// Independently authored task-failure frames exercise drain and error semantics.
// The withheld consumer and barrier make application timing deterministic.
func TestTaskFailureDrain(t *testing.T) {
	fixture, err := transcript.Prepare(t.TempDir(), bytes.NewReader(transcript.TaskFailureWire))
	require.NoError(t, err)
	session, run := openEmbeddedClaude(t, fixture)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Abort() })
	prompt := transcript.TaskFailurePrompt
	_, err = session.Submit(testsupport.Deadline(t), claude.Input{Text: prompt})
	require.NoError(t, err)
	// Embedded replay reports full consumption before its final client-EOF
	// check. Withhold Receive until all synthetic server frames are emitted.
	testsupport.Wait(t, testsupport.Deadline(t), run.FullConsumption())
	var got []json.RawMessage
	var failed, idle bool
	for {
		r, err := session.Receive(testsupport.Deadline(t))
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
		require.Nil(t, r.Request, "task failure cannot mint answer authority")
		if r.Event == nil {
			continue
		}
		got = append(got, r.Raw)
		if r.Event.Result != nil {
			require.Equal(t, "success", r.Event.Subtype)
			failed = r.Event.Result.IsError
		}
		if r.Event.System != nil && r.Event.System.State != nil && *r.Event.System.State == "idle" {
			idle = failed
			require.NoError(t, session.Close())
			testsupport.Wait(t, testsupport.Deadline(t), session.Done())
		}
	}
	var expected []json.RawMessage
	for line := range bytes.SplitSeq(bytes.TrimSpace(transcript.TaskFailureWire), []byte{'\n'}) {
		var f transcript.Frame
		require.NoError(t, json.Unmarshal(line, &f))
		if f.Direction == "server" && f.Channel == "protocol" {
			expected = append(expected, f.Data)
		}
	}
	replaycheck.RequireSuccess(t, run)
	require.Len(t, got, len(expected), "every synthetic native frame require.NoError be consumed")
	for i := range expected {
		assert.Equal(t, string(expected[i]), string(got[i]), "original frame %d", i)
	}
	assert.True(t, failed)
	assert.True(t, idle)
	assert.NoError(t, session.Err(), "task failure is distinct from transport failure")
	require.NoError(t, session.Close())
	_, err = session.Submit(context.Background(), claude.Input{Text: "no retry"})
	require.Error(t, err)
}

func TestFaultClosePendingRequest(t *testing.T) {
	ctx := testsupport.Deadline(t)
	var old *claude.IncomingRequest
	for epoch := range 2 {
		o, path := claudeTargetOptions(t, "session-canceled-approval")
		session, err := claude.Open(ctx, o)
		require.NoError(t, err)
		t.Cleanup(func() { _ = session.Abort() })
		_, err = session.Submit(ctx, claude.Input{Text: "pending"})
		require.NoError(t, err)
		pending := claudetest.Requests(t, session, 2)[0]
		if epoch == 0 {
			old = pending
		} else {
			require.Equal(t, old.Handle().RequestID(), pending.Handle().RequestID(), "synthetic peer deliberately reuses the native ID in a fresh connection")
			require.Error(t, old.Approve(ctx, claude.ApprovalOptions{}), "fresh connection cannot restore old answer authority")
			require.NoError(t, pending.Deny(ctx, claude.DenialOptions{}))
			// The interrupt reply is a peer input/answer settlement barrier before Close.
			require.NoError(t, session.Interrupt(ctx, claude.InterruptOptions{}))
			claudeUntil(t, session, "fixture_interrupt")
		}
		require.NoError(t, session.Close())
		testsupport.Wait(t, ctx, pending.Context().Done())
		require.Error(t, pending.Deny(ctx, claude.DenialOptions{}))
		require.Error(t, pending.Deny(ctx, claude.DenialOptions{}))
		assert.Equal(t, epoch, testsupport.CountAnswers(t, path, "claude"))
	}
}

func openEmbeddedClaude(t *testing.T, fixture *mocktest.Session) (*claude.Session, *mocktest.Run) {
	t.Helper()
	options := claude.SessionOptions{CLIPath: "require.NoError-not-launch", Env: []string{}}
	args, err := claude.NativeArgs(options)
	require.NoError(t, err)
	run := replaycheck.Start(t, fixture, args)
	options.WorkDir, options.Env = run.Start.Workspace, run.Environment()
	actualArgs, err := claude.NativeArgs(options)
	require.NoError(t, err)
	require.Equal(t, run.Start.NativeArgs, actualArgs)
	require.Equal(t, run.Start.Config, run.Start.Env["CLAUDE_CONFIG_DIR"])
	session, err := claude.OpenStreams(testsupport.Deadline(t), options, claude.StreamTransport{
		Reader: run.Reader, Writer: run.Writer, Stderr: run.Stderr, Wait: run.WaitError,
	})
	require.NoError(t, err)
	done := run.CloseInputWhenComplete(session.CloseInput)
	t.Cleanup(func() {
		_ = session.Close()
		select {
		case err := <-done:
			if err != nil {
				assert.ErrorIs(t, err, claude.ErrInputClosed)
			}
		case <-time.After(5 * time.Second):
			t.Error("input completion watcher did not finish")
		}
	})
	return session, run
}
