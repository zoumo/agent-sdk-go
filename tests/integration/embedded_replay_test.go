package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/codex"
	ct "github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

// These fixtures exercise embedded testify replay through native SDK streams.
// The self-reexecuting peers elsewhere exercise SDK-owned synthetic native
// process behavior; they never host or launch testify.
func embeddedRun(t *testing.T, fixture *mocktest.Session, args []string) *mocktest.Run {
	t.Helper()
	t.Cleanup(func() { assert.NoError(t, fixture.Close()) })
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
	run, err := fixture.NewRun(ctx, mocktest.RunOptions{NativeArgs: args})
	if err != nil {
		cancel()
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		_ = run.CloseSDK()
		waitCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = run.Wait(waitCtx)
		cancel()
	})
	return run
}

func openEmbeddedCodex(t *testing.T, fixture *mocktest.Session, init ct.InitializeParams) (*codex.Client, *mocktest.Run) {
	t.Helper()
	run := embeddedRun(t, fixture, nil)
	client, err := codex.NewStreams(deadline(t), codex.StreamOptions{
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

func openEmbeddedClaude(t *testing.T, fixture *mocktest.Session) (*claude.Session, *mocktest.Run) {
	t.Helper()
	options := claude.SessionOptions{CLIPath: "must-not-launch", Env: []string{}}
	args, err := claude.NativeArgs(options)
	require.NoError(t, err)
	run := embeddedRun(t, fixture, args)
	options.WorkDir, options.Env = run.Start.Workspace, run.Environment()
	actualArgs, err := claude.NativeArgs(options)
	require.NoError(t, err)
	require.Equal(t, run.Start.NativeArgs, actualArgs)
	require.Equal(t, run.Start.Config, run.Start.Env["CLAUDE_CONFIG_DIR"])
	session, err := claude.OpenStreams(deadline(t), options, claude.StreamTransport{
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
