package recording

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type childOutput struct{ bytes.Buffer }

func (o *childOutput) Close() error { return nil }

func TestChildReplayPreservesParentFirstBackgroundAndOriginalApproval(t *testing.T) {
	frames := []Frame{
		frame(1, "s2c", `{"method":"item/completed","params":{"threadId":"root","turnId":"turn","item":{"type":"collabAgentToolCall","id":"spawn","tool":"spawnAgent","status":"completed","senderThreadId":"root","receiverThreadIds":["child"],"agentsStates":{"child":{"status":"running"}}}}}`),
		frame(2, "s2c", `{"id":"approval-child","method":"item/commandExecution/requestApproval","params":{"threadId":"child","turnId":"child-turn","itemId":"child-tool","command":"synthetic only"}}`),
		frame(3, "c2s", `{"id":"approval-child","result":{"decision":"decline"}}`),
		frame(4, "s2c", `{"method":"turn/completed","params":{"threadId":"root","turn":{"id":"turn","status":"completed","items":[],"error":null}}}`),
		frame(5, "s2c", `{"method":"future/childController","params":{"threadId":"child","unknown":true}}`),
		frame(6, "s2c", `{"method":"turn/completed","params":{"threadId":"child","turn":{"id":"child-turn","status":"completed","items":[],"error":null}}}`),
	}
	var out childOutput
	require.NoError(t, RunPeer(context.Background(), io.NopCloser(strings.NewReader(string(frames[2].Wire)+"\n")), &out, frames))
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 5)
	for i, n := range []int{0, 1, 3, 4, 5} {
		require.JSONEq(t, string(frames[n].Wire), lines[i])
	}
	for _, answer := range []string{`{"id":"approval-root","result":{"decision":"decline"}}`, `{"id":"approval-child","result":{"decision":"accept"}}`} {
		var refused childOutput
		require.Error(t, RunPeer(context.Background(), io.NopCloser(strings.NewReader(answer+"\n")), &refused, frames))
		require.NotContains(t, refused.String(), "future/childController")
	}
}
