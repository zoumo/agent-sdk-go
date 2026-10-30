package codex_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// Synthetic scheduling check at the public Session seam. This does not stand in
// for a native recording or prove that a specific CLI emits this event on demand.
func TestSessionHeldApprovalAllowsControlAndEventBeforeOriginalAnswer(t *testing.T) {
	f := startFake(t, "session-config-held-approval", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	opened, err := s.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "future/opened", opened.Event.Method)

	emitRequests(t, f.c, requestFrame(`"held-approval"`, "item/commandExecution/requestApproval",
		`{"threadId":"main-1","turnId":"held-turn","itemId":"read-item","command":"cat numbers.txt"}`))
	pending, err := s.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, codex.ReceivedRequest, pending.Kind)
	require.NotNil(t, pending.Request)
	require.NoError(t, pending.Request.Context().Err())
	require.Empty(t, answerFrames(t, f.trace))

	receipt, err := s.SetModel(ctx, "synthetic-model")
	require.NoError(t, err)
	require.True(t, receipt.NativeAccepted)
	require.Equal(t, s.ThreadID(), receipt.Requested.ThreadID)
	require.Empty(t, answerFrames(t, f.trace), "control acknowledgement must not answer the approval")

	const event = `{"method":"thread/settings/updated","params":{"threadId":"main-1","threadSettings":{"cwd":"/synthetic","approvalPolicy":"on-request","approvalsReviewer":"user","sandboxPolicy":{"type":"readOnly"},"model":"synthetic-model","modelProvider":"mock","collaborationMode":{"mode":"default","settings":{"model":"synthetic-model"}}}}}`
	emitRequests(t, f.c, event)
	got, err := s.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, codex.ReceivedEvent, got.Kind)
	require.Equal(t, event, string(got.Raw))
	require.NoError(t, got.Event.DecodeError)
	settings, ok := got.Event.Data.(*types.ThreadSettingsUpdatedNotification)
	require.True(t, ok)
	require.Equal(t, s.ThreadID(), settings.ThreadID)
	require.Equal(t, "synthetic-model", settings.ThreadSettings.Model)
	require.NoError(t, pending.Request.Context().Err())
	require.Empty(t, answerFrames(t, f.trace), "event delivery must leave the original approval pending")

	require.NoError(t, pending.Request.Approve(ctx, codex.ApprovalOptions{}))
	require.ErrorIs(t, pending.Request.Approve(ctx, codex.ApprovalOptions{}), codex.ErrRequestAnswered)
	responseBarrier(t, f)
	answers := answerFrames(t, f.trace)
	require.Len(t, answers, 1)
	require.JSONEq(t, `"held-approval"`, string(answers[0]["id"]))
	require.JSONEq(t, `{"decision":"accept"}`, string(answers[0]["result"]))
}
