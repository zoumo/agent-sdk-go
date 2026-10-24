package e2e_test

import (
	"context"
	"github.com/stretchr/testify/require"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"testing"
)

// Explicit Load confirms the same persisted identity without ordinary input.
// All historical/live/request facts use the sole Session FIFO.
func loadNativeSession(t *testing.T, ctx context.Context, c *codex.Client, id, cwd string) *codex.Session {
	t.Helper()
	session, err := codex.Load(ctx, codex.LoadOptions{Client: c, HistoryOptions: codex.HistoryOptions{ThreadID: id, CWD: cwd, CodexHome: string(c.ServerInfo().CodexHome)}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	for {
		received, err := session.Receive(ctx)
		require.NoError(t, err)
		require.Nil(t, received.Request, "recovery without ordinary input must not fabricate permission authority")
		if received.Load != nil {
			require.NoError(t, received.Load.Err)
			require.Equal(t, codex.LoadSucceeded, received.Load.Status)
			return session
		}
	}
}
