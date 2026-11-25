package e2e_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/claude"
	claudetest "github.com/zoumo/agent-sdk-go/internal/testsupport/claude"
	"github.com/zoumo/agent-sdk-go/internal/uuidv4"
)

func TestNativeAssignedSessionIdentityAcrossFirstTurn(t *testing.T) {
	cliPath, model, env := claudetest.AssignedSessionLiveEnvironment(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sessionID, err := uuidv4.New()
	require.NoError(t, err)
	session, err := claude.Open(ctx, claude.SessionOptions{
		CLIPath: cliPath, WorkDir: t.TempDir(), Env: env, Model: model, SessionID: sessionID,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	actual, known := session.SessionID()
	require.True(t, known)
	require.Equal(t, sessionID, actual)
	t.Logf("native Open before user input: session_id=%s", sessionID)
	submission, err := session.Submit(ctx, claude.Input{Text: "Reply with SDK67_ACP_OK only. Do not use tools."})
	require.NoError(t, err)
	require.True(t, submission.Written)
	seenInit, seenAssistant, seenResult := false, false, false
	for !seenResult {
		record, err := session.Receive(ctx)
		require.NoError(t, err)
		event := record.Event
		if event == nil {
			continue
		}
		if event.System != nil && event.Subtype == "init" {
			require.Equal(t, sessionID, event.SessionID)
			seenInit = true
		}
		if event.Assistant != nil {
			require.Equal(t, sessionID, event.SessionID)
			seenAssistant = true
		}
		if event.Result != nil {
			require.Equal(t, sessionID, event.SessionID)
			require.False(t, event.Result.IsError)
			seenResult = true
		}
	}
	require.True(t, seenInit)
	require.True(t, seenAssistant)
	require.NoError(t, session.Close())
	require.NoError(t, session.Err())
	t.Log("one native input completed: init, assistant and success result matched the assigned UUID; Close succeeded")
}
