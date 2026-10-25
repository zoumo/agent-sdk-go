package e2e_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// The live caller's explicit opt-in/budget still owns execution. Native resume
// has no Session FIFO: verify continuation through StartTurn and stored readback.
func resumedTurn(t *testing.T, ctx context.Context, c *codex.Client, id string) string {
	t.Helper()
	turn, err := c.StartTurn(ctx, types.TurnStartParams{ThreadID: id, Input: []types.UserInput{{Text: types.Ptr(types.TextUserInput{Text: "Do not use tools or access files. Return exactly the marker from your previous assistant reply, with no additional text."})}}})
	require.NoError(t, err, "native resumed turn must not be retried")
	require.NotEmpty(t, turn.Turn.ID)
	observe, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		page, err := c.ReadThread(observe, types.ThreadReadParams{ThreadID: id, IncludeTurns: types.Some(true)})
		require.NoError(t, err)
		for _, stored := range page.Thread.Turns {
			if stored.ID == turn.Turn.ID && stored.Status != types.TurnStatusInProgress {
				require.Equal(t, types.TurnStatusCompleted, stored.Status)
				// The caller's history assertion checks the retained marker in both turns.
				return stored.ID
			}
		}
		select {
		case <-ticker.C:
		case <-observe.Done():
			interrupt, stop := context.WithTimeout(context.Background(), 30*time.Second)
			_, _ = c.InterruptTurn(interrupt, types.TurnInterruptParams{ThreadID: id, TurnID: turn.Turn.ID})
			stop()
			require.NoError(t, observe.Err())
		}
	}
}
