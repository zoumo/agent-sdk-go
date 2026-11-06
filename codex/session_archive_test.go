package codex_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestSessionArchiveAndUnarchive(t *testing.T) {
	f := startFake(t, "normal", codex.Limits{})
	session, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	archived, err := session.Archive(context.Background())
	require.NoError(t, err)
	require.JSONEq(t, `{"archive":true}`, string(archived.UnknownFields["future"]))
	require.Equal(t, "native-thread-42", session.ThreadID())

	restored, err := session.Unarchive(context.Background())
	require.NoError(t, err)
	require.Equal(t, "native-thread-42", restored.Thread.ID)
	require.JSONEq(t, `{"large":9007199254740993}`, string(restored.Thread.UnknownFields["future"]))
	require.JSONEq(t, `{"unarchive":true}`, string(restored.UnknownFields["future"]))

	var calls []fakeEnvelope
	for _, frame := range trace(t, f.trace) {
		if frame.Method == "thread/archive" || frame.Method == "thread/unarchive" {
			calls = append(calls, frame)
		}
	}
	require.Len(t, calls, 2)
	require.Equal(t, "thread/archive", calls[0].Method)
	require.JSONEq(t, `{"threadId":"native-thread-42"}`, string(calls[0].Params))
	require.Equal(t, "thread/unarchive", calls[1].Method)
	require.JSONEq(t, `{"threadId":"native-thread-42"}`, string(calls[1].Params))
}

func TestSessionArchiveNativeErrorAndClosedSession(t *testing.T) {
	f := startFake(t, "archive-reject", codex.Limits{})
	session, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	_, err = session.Archive(context.Background())
	callError(t, err, codex.Responded, nil)
	var rpc *codex.RPCError
	require.ErrorAs(t, err, &rpc)
	require.EqualValues(t, -32010, rpc.Code)
	require.Equal(t, "archive refused", rpc.Message)
	require.JSONEq(t, `{"reason":"synthetic"}`, string(rpc.Data))

	restored, err := session.Unarchive(context.Background())
	require.NoError(t, err, "archive refusal must not damage the Session")
	require.Equal(t, "native-thread-42", restored.Thread.ID)

	_, err = session.Archive(nil) //nolint:staticcheck // verifies nil-context rejection.
	callError(t, err, codex.NotSent, nil)
	_, err = session.Unarchive(nil) //nolint:staticcheck // verifies nil-context rejection.
	callError(t, err, codex.NotSent, nil)

	require.NoError(t, session.Close())
	_, err = session.Archive(context.Background())
	callError(t, err, codex.NotSent, codex.ErrClosed)
	_, err = session.Unarchive(context.Background())
	callError(t, err, codex.NotSent, codex.ErrClosed)
}

func TestSessionUnarchiveRejectsDifferentNativeThread(t *testing.T) {
	f := startFake(t, "unarchive-mismatch", codex.Limits{})
	session, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	_, err = session.Unarchive(context.Background())
	callError(t, err, codex.Responded, codex.ErrProtocol)
	require.Equal(t, "native-thread-42", session.ThreadID())
}

func TestSessionArchiveUsesResumedMainThread(t *testing.T) {
	f := startFake(t, "resume-session", codex.Limits{})
	session, err := codex.Open(context.Background(), codex.SessionOptions{
		Client: f.c,
		Resume: &types.ThreadResumeParams{ThreadID: resumedNativeID},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	_, err = session.Archive(context.Background())
	require.NoError(t, err)

	var params types.ThreadArchiveParams
	for _, frame := range trace(t, f.trace) {
		if frame.Method == "thread/archive" {
			require.NoError(t, json.Unmarshal(frame.Params, &params))
		}
	}
	require.Equal(t, resumedNativeID, params.ThreadID)
}
