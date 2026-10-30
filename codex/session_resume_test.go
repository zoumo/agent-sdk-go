package codex_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestSessionResumePreservesIdentityLiveOrderAndSeparateResponse(t *testing.T) {
	f := startFake(t, "resume-session", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c, Resume: &types.ThreadResumeParams{ThreadID: resumedNativeID}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	require.Equal(t, resumedNativeID, s.ThreadID())
	_, ok := s.StartResponse()
	require.False(t, ok)
	r, ok := s.ResumeResponse()
	require.True(t, ok)
	require.Equal(t, resumedNativeID, r.Thread.ID)
	require.Len(t, r.Thread.Turns, 1)
	require.Equal(t, "9007199254740993", string(r.UnknownFields["future"]))
	delete(r.UnknownFields, "future")
	r, _ = s.ResumeResponse()
	require.Equal(t, "9007199254740993", string(r.UnknownFields["future"]))
	require.NoError(t, s.Close())
	for _, method := range []string{"future/beforeResume", "future/afterResume"} {
		got, err := s.Receive(context.Background())
		require.NoError(t, err)
		require.Equal(t, method, got.Event.Method)
		require.Equal(t, "live", got.Origin)
		require.Nil(t, got.Event.History)
		require.Nil(t, got.Request)
	}
	_, err = s.Receive(context.Background())
	require.ErrorIs(t, err, io.EOF)
	assertMethods(t, f, "initialize", "initialized", "thread/resume", "thread/unsubscribe")
}

func TestSessionResumeFailuresReleaseReservationWithoutRetry(t *testing.T) {
	for _, mode := range []string{"resume-session-error", "resume-session-mismatch", "resume-session-wait"} {
		t.Run(mode, func(t *testing.T) {
			f := startFake(t, mode, codex.Limits{Subscriptions: 1})
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			s, err := codex.Open(ctx, codex.SessionOptions{Client: f.c, Resume: &types.ThreadResumeParams{ThreadID: resumedNativeID}})
			require.Nil(t, s)
			require.Error(t, err)
			var call *codex.CallError
			require.ErrorAs(t, err, &call)
			if mode == "resume-session-wait" {
				require.Equal(t, codex.OutcomeUnknown, call.Dispatch)
			} else {
				require.Equal(t, codex.Responded, call.Dispatch)
			}
			if mode == "resume-session-mismatch" {
				require.ErrorIs(t, err, codex.ErrProtocol)
			}
			assertMethods(t, f, "initialize", "initialized", "thread/resume", "thread/unsubscribe")
			// The shared connection and single Session slot remain usable.
			started, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
			require.NoError(t, err)
			require.NoError(t, started.Close())
		})
	}
}

func TestSessionResumeValidationAndOwnershipBeforeDispatch(t *testing.T) {
	f := startFake(t, "resume-normal", codex.Limits{})
	for _, options := range []codex.SessionOptions{
		{Client: f.c, Resume: &types.ThreadResumeParams{}},
		{Client: f.c, Thread: types.ThreadStartParams{Model: types.Some("conflict")}, Resume: &types.ThreadResumeParams{ThreadID: resumedNativeID}},
	} {
		_, err := codex.Open(context.Background(), options)
		require.ErrorIs(t, err, codex.ErrInvalidInput)
	}
	assertMethods(t, f, "initialize", "initialized")
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c, Resume: &types.ThreadResumeParams{ThreadID: resumedNativeID}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	_, err = codex.Open(context.Background(), codex.SessionOptions{Client: f.c, Resume: &types.ThreadResumeParams{ThreadID: resumedNativeID}})
	require.ErrorIs(t, err, codex.ErrOwnershipConflict)
	_, err = f.c.ResumeThread(context.Background(), types.ThreadResumeParams{ThreadID: resumedNativeID})
	require.ErrorIs(t, err, codex.ErrOwnershipConflict)
	require.NoError(t, s.Close())
	assertMethods(t, f, "initialize", "initialized", "thread/resume", "thread/unsubscribe")
}

func TestSessionResumeDiscoveryDoesNotRestoreChildAuthority(t *testing.T) {
	f := startFake(t, "resume-config", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c, Resume: &types.ThreadResumeParams{ThreadID: resumedNativeID}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	children, err := s.ListSubagents(context.Background(), codex.SubagentQuery{})
	require.NoError(t, err)
	require.True(t, children.Coverage.EnumerationComplete)
	require.Equal(t, "old-child", children.Pages[0].Data[0].ID)
	sub, err := s.Submit(context.Background(), codex.Input{Input: []types.UserInput{}})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := s.Receive(ctx)
	require.NoError(t, err)
	require.Nil(t, got.Request)
	require.Equal(t, "turn/completed", got.Event.Method)
	require.Equal(t, sub.Response.Turn.ID, got.Source.TurnID)
	require.NoError(t, s.Close())
	_, err = s.Receive(ctx)
	require.ErrorIs(t, err, io.EOF)
	require.NotEmpty(t, f.c.Unattributed().Records)
	require.NoError(t, f.c.Call(context.Background(), "echo", json.RawMessage(`{}`), nil))
}

func TestSessionResumeOwnedProcessReaped(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	pid := filepath.Join(dir, "pid")
	s, err := codex.Open(context.Background(), codex.SessionOptions{
		Stdio: &codex.StdioOptions{Executable: exe, Args: []string{"-test.run=^TestFakeChild$"},
			Env:        append(os.Environ(), "CODEX_SDK_HELPER=resume-normal", "CODEX_SDK_PID="+pid, "CODEX_SDK_TRACE="+filepath.Join(dir, "trace")),
			Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "resume-test", Version: "1"}}},
		Resume: &types.ThreadResumeParams{ThreadID: resumedNativeID},
	})
	require.NoError(t, err)
	require.NoError(t, s.Close())
	assertReaped(t, pid)
	require.NoError(t, s.Close())
	select {
	case <-s.Done():
	default:
		t.Fatal("owned session not closed")
	}
}

func TestSessionResumeFailedDetachRetainsOwnershipFence(t *testing.T) {
	f := startFake(t, "resume-error", codex.Limits{})
	options := codex.SessionOptions{Client: f.c, Resume: &types.ThreadResumeParams{ThreadID: resumedNativeID}}
	s, err := codex.Open(context.Background(), options)
	require.Nil(t, s)
	require.Error(t, err)
	assertMethods(t, f, "initialize", "initialized", "thread/resume", "thread/unsubscribe")
	_, err = codex.Open(context.Background(), options)
	require.ErrorIs(t, err, codex.ErrOwnershipConflict)
	assertMethods(t, f, "initialize", "initialized", "thread/resume", "thread/unsubscribe")
	require.NoError(t, f.c.Call(context.Background(), "echo", json.RawMessage(`{}`), nil))
}
