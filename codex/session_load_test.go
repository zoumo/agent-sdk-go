package codex_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestSessionLoadSingleEntryAndIndependentLifetime(t *testing.T) {
	f := startFake(t, "history", codex.Limits{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := codex.Load(ctx, codex.LoadOptions{Client: f.c, HistoryOptions: codex.HistoryOptions{ThreadID: "native-thread-42", CWD: "/synthetic", CodexHome: "/synthetic-home", PageSize: 1}})
	require.NoError(t, err)
	defer s.Close()
	var turns []string
	receiveCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	for {
		got, err := s.Receive(receiveCtx)
		require.NoError(t, err)
		if got.Load != nil {
			require.Equal(t, codex.LoadSucceeded, got.Load.Status)
			break
		}
		require.Equal(t, "history", got.Origin)
		require.NotNil(t, got.Event.History)
		require.Empty(t, got.Raw)
		if got.Event.Method == "turn/completed" {
			turns = append(turns, got.Event.History.TurnID)
		}
	}
	require.Equal(t, []string{"t1", "t2"}, turns)
	require.Equal(t, codex.LoadSucceeded, s.LoadState().Status)
	cancel()
	_, err = s.Submit(context.Background(), codex.Input{})
	require.NoError(t, err)
	select {
	case <-s.Done():
		t.Fatal("successful Load terminated")
	default:
	}
}

func TestSessionLoadPendingControlsCancelAndDrain(t *testing.T) {
	f := startFake(t, "history-blocked", codex.Limits{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := codex.Load(ctx, codex.LoadOptions{Client: f.c, HistoryOptions: codex.HistoryOptions{ThreadID: "native-thread-42", CWD: "/synthetic", CodexHome: "/synthetic-home", PageSize: 1}})
	require.NoError(t, err)
	defer s.Close()
	receiveCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	first, err := s.Receive(receiveCtx)
	require.NoError(t, err)
	require.Equal(t, "t1", first.Event.History.TurnID)
	_, err = s.Submit(context.Background(), codex.Input{})
	require.ErrorIs(t, err, codex.ErrLoadPending)
	_, err = s.SetModel(context.Background(), "native-model")
	require.ErrorIs(t, err, codex.ErrLoadPending)
	_, err = s.SetThinking(context.Background(), codex.ThinkingChange{})
	require.ErrorIs(t, err, codex.ErrLoadPending)
	require.NoError(t, s.Interrupt(context.Background(), codex.InterruptOptions{TurnID: "actual-turn"}))
	cancel()
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("cancel blocked")
	}
	require.Equal(t, codex.LoadFailed, s.LoadState().Status)
	require.ErrorIs(t, s.LoadState().Err, context.Canceled)
	var history int
	for {
		got, e := s.Receive(receiveCtx)
		if e != nil {
			require.ErrorIs(t, e, context.Canceled)
			break
		}
		if got.Event != nil {
			history++
		}
	}
	require.Equal(t, 1, history)
	require.NoError(t, s.Close())
	require.NoError(t, f.c.Call(context.Background(), "echo", "alive", nil))
}

func TestSessionLoadFailureEvidenceAndDrain(t *testing.T) {
	for _, mode := range []string{"resume-malformed", "resume-error", "mutated", "resume-mismatch", "resume-cwd", "late", "cursor", "duplicate", "item-duplicate", "summary", "active", "missing", "release-error", "future-status", "rich"} {
		t.Run(mode, func(t *testing.T) {
			f := startFake(t, "history-"+mode, codex.Limits{})
			size := uint32(1)
			if mode == "rich" {
				size = 5
			}
			s, err := codex.Load(context.Background(), codex.LoadOptions{Client: f.c, HistoryOptions: codex.HistoryOptions{ThreadID: "native-thread-42", CWD: "/synthetic", CodexHome: "/synthetic-home", PageSize: size}})
			require.NoError(t, err)
			defer s.Close()
			ctx, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			var events int
			for {
				got, e := s.Receive(ctx)
				if e != nil {
					require.NotErrorIs(t, e, context.DeadlineExceeded)
					break
				}
				if got.Load != nil {
					break
				}
				events++
				require.NotEmpty(t, got.Event.History.Raw)
				if mode == "future-status" {
					require.Empty(t, got.Event.Method)
					require.Contains(t, string(got.Event.History.Raw), "futureStatus")
				}
			}
			state := s.LoadState()
			if mode == "future-status" || mode == "rich" || mode == "release-error" {
				require.Equal(t, codex.LoadSucceeded, state.Status)
			} else {
				require.Equal(t, codex.LoadFailed, state.Status)
				require.Error(t, state.Err)
			}
			if mode == "resume-malformed" || mode == "resume-error" || mode == "resume-mismatch" || mode == "resume-cwd" {
				require.Zero(t, events)
			}
			if mode == "resume-mismatch" || mode == "resume-cwd" {
				require.ErrorIs(t, state.Err, codex.ErrIdentityMismatch)
				require.Empty(t, state.Confirmation)
				require.NotEmpty(t, state.ResumeRaw)
				if mode == "resume-mismatch" {
					require.Equal(t, "different", state.ObservedIdentity)
					require.Contains(t, string(state.ResumeRaw), `"id":"different"`)
				}
			}
			if mode == "resume-malformed" {
				require.ErrorIs(t, state.Err, codex.ErrResumeUnconfirmed)
				require.JSONEq(t, `{}`, string(state.ResumeRaw))
			}

			if mode == "late" {
				require.Equal(t, 2, events)
			}
			if mode == "release-error" {
				require.ErrorContains(t, s.Close(), "unknown unsubscribe")
				_, err = codex.Load(context.Background(), codex.LoadOptions{Client: f.c, HistoryOptions: codex.HistoryOptions{ThreadID: "native-thread-42", CWD: "/synthetic", CodexHome: "/synthetic-home"}})
				require.ErrorIs(t, err, codex.ErrOwnershipConflict)
			}
			for _, m := range trace(t, f.trace) {
				require.NotContains(t, []string{"thread/start", "turn/start"}, m.Method)
			}
		})
	}
}

func TestSessionLoadObservedDescendantKeepsAncestorCoverage(t *testing.T) {
	f := startFake(t, "history-overlap", codex.Limits{})
	a, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	defer a.Close()
	emitRequests(t, f.c, `{"method":"thread/started","params":{"thread":{"id":"child","parentThreadId":"native-thread-42"}}}`)
	// A real request observed before explicit child Load retains its one authority
	// even when native resume replays the exact same pending request.
	frame := `{"id":"original","method":"future/approval","params":{"threadId":"child"}}`
	emitRequests(t, f.c, frame)
	child, err := codex.Load(context.Background(), codex.LoadOptions{Client: f.c, HistoryOptions: codex.HistoryOptions{ThreadID: "child", CWD: "/synthetic", CodexHome: "/synthetic-home", PageSize: 1}})
	require.NoError(t, err)
	defer child.Close()
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	for {
		got, e := child.Receive(ctx)
		require.NoError(t, e)
		if got.Load != nil {
			break
		}
	}
	emitRequests(t, f.c, frame, `{"method":"future/both","params":{"threadId":"child"}}`, `{"id":"direct","method":"future/approval","params":{"threadId":"child"}}`)
	// Close child first: it must neither detach A's coverage nor revoke A's handle.
	require.NoError(t, child.Close())
	for _, m := range trace(t, f.trace) {
		if m.Method == "thread/unsubscribe" {
			t.Fatal("child close unsubscribed ancestor coverage")
		}
	}
	emitRequests(t, f.c, `{"method":"future/after-child-close","params":{"threadId":"child"}}`)
	var methods []string
	var handles []*codex.IncomingRequest
	for {
		got, e := a.Receive(ctx)

		require.NoError(t, e)
		if got.Event != nil {
			methods = append(methods, got.Event.Method)
			if got.Event.Method == "future/after-child-close" {
				break
			}
		}
		if got.Request != nil {
			handles = append(handles, got.Request)
		}
	}
	require.Contains(t, methods, "future/both")
	require.Contains(t, methods, "future/after-child-close")
	require.Len(t, handles, 2) // Same original capability re-delivered by native replay.
	require.NoError(t, handles[0].Context().Err())
	require.NoError(t, handles[0].Handle().Answer(context.Background(), json.RawMessage(`{"answer":"once"}`)))
	require.Error(t, handles[1].Handle().Answer(context.Background(), json.RawMessage(`{"answer":"twice"}`)))
	require.NoError(t, a.Close())
	var direct int
	for {
		got, e := child.Receive(ctx)
		if e != nil {
			require.ErrorIs(t, e, io.EOF)
			break
		}
		if got.Request != nil {
			direct++
			require.Equal(t, codex.StringID("direct"), got.Request.ID())
			require.ErrorIs(t, got.Request.Context().Err(), context.Canceled)
		}
	}
	require.Equal(t, 1, direct)
	var detached []string
	for _, m := range trace(t, f.trace) {
		if m.Method == "thread/unsubscribe" {
			var p types.ThreadUnsubscribeParams
			require.NoError(t, json.Unmarshal(m.Params, &p))
			detached = append(detached, p.ThreadID)
		}
	}
	require.ElementsMatch(t, []string{"native-thread-42", "child"}, detached)
}

func TestSessionLoadInterleavesActualQuestionWithoutConsumerControlDependency(t *testing.T) {
	f := startFake(t, "history-interleave", codex.Limits{})
	s, err := codex.Load(context.Background(), codex.LoadOptions{Client: f.c, HistoryOptions: codex.HistoryOptions{ThreadID: "native-thread-42", CWD: "/synthetic", CodexHome: "/synthetic-home", PageSize: 1}})
	require.NoError(t, err)
	defer s.Close()
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	var facts []string
	for {
		got, e := s.Receive(ctx)
		require.NoError(t, e)
		if got.Event != nil {
			facts = append(facts, got.Origin+":"+got.Event.Method)
			if got.Origin == "live" {
				require.Contains(t, string(got.Raw), `"unknown":true`)
			}
		}
		if got.Request != nil {
			require.Equal(t, codex.LoadPending, s.LoadState().Status)
			require.NoError(t, got.Request.Answer(context.Background(), types.ToolRequestUserInputResponse{Answers: map[string]types.ToolRequestUserInputAnswer{"q1": {Answers: []string{"chosen"}}}}))
			break
		}
	}
	require.Equal(t, []string{"history:item/completed", "history:turn/completed", "live:future/during-load"}, facts)
	require.NoError(t, s.Interrupt(context.Background(), codex.InterruptOptions{TurnID: "active"}))
	// Release page 2, then wait on state without consuming its records/boundary.
	require.NoError(t, f.c.Call(context.Background(), "echo", "release", nil))
	require.Eventually(t, func() bool { return s.LoadState().Status == codex.LoadSucceeded }, time.Second, time.Millisecond)
	require.NoError(t, s.Close())
	var tail []string
	for {
		got, e := s.Receive(ctx)
		if e != nil {
			require.ErrorIs(t, e, io.EOF)
			break
		}
		if got.Event != nil {
			tail = append(tail, got.Event.History.TurnID)
		} else {
			require.Equal(t, codex.LoadSucceeded, got.Load.Status)
		}
	}
	require.Equal(t, []string{"t2"}, tail)
}

func TestSessionLoadCloseUnconfirmedResumeRetainsFenceAndAcceptedLive(t *testing.T) {
	f := startFake(t, "history-resume-blocked", codex.Limits{})
	o := codex.LoadOptions{Client: f.c, HistoryOptions: codex.HistoryOptions{ThreadID: "native-thread-42", CWD: "/synthetic", CodexHome: "/synthetic-home", PageSize: 1}}
	s, err := codex.Load(context.Background(), o)
	require.NoError(t, err)
	require.Eventually(t, func() bool { _, err := os.Stat(f.trace + ".resume-blocked"); return err == nil }, time.Second, time.Millisecond)
	_, err = codex.Load(context.Background(), o)
	require.ErrorIs(t, err, codex.ErrOwnershipConflict)
	_, err = f.c.ResumeThread(context.Background(), types.ThreadResumeParams{ThreadID: o.ThreadID})
	require.ErrorIs(t, err, codex.ErrOwnershipConflict)
	emitRequests(t, f.c, `{"method":"future/before-confirmation","params":{"threadId":"native-thread-42"}}`)
	require.ErrorContains(t, s.Close(), "unconfirmed")
	require.Equal(t, codex.LoadFailed, s.LoadState().Status)
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	got, err := s.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "live", got.Origin)
	require.Equal(t, "future/before-confirmation", got.Event.Method)
	boundary, err := s.Receive(ctx)
	require.NoError(t, err)
	require.NotNil(t, boundary.Load)
	require.Equal(t, codex.LoadFailed, boundary.Load.Status)
	_, err = s.Receive(ctx)
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, f.c.Call(context.Background(), "echo", "release late response", nil))
	_, err = codex.Load(context.Background(), o)
	require.ErrorIs(t, err, codex.ErrOwnershipConflict)
}

func TestSessionLoadTypedDetachCannotBypassObservers(t *testing.T) {
	f := startFake(t, "history-overlap", codex.Limits{})
	a, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	defer a.Close()
	emitRequests(t, f.c, `{"method":"thread/started","params":{"thread":{"id":"child","parentThreadId":"native-thread-42"}}}`)
	for _, id := range []string{"native-thread-42", "child"} {
		_, err = f.c.UnsubscribeThread(context.Background(), types.ThreadUnsubscribeParams{ThreadID: id})
		require.ErrorIs(t, err, codex.ErrOwnershipConflict)
		_, err = f.c.ResumeThread(context.Background(), types.ThreadResumeParams{ThreadID: id})
		require.ErrorIs(t, err, codex.ErrOwnershipConflict)
	}
	require.NoError(t, a.Close())
	for _, m := range trace(t, f.trace) {
		require.NotEqual(t, "thread/resume", m.Method)
		if m.Method == "thread/unsubscribe" {
			var p types.ThreadUnsubscribeParams
			require.NoError(t, json.Unmarshal(m.Params, &p))
			require.Equal(t, "native-thread-42", p.ThreadID)
		}
	}
}

// Reversing discovery/admission order still shares the physical attachment.
func TestSessionLoadOwnerPrecedesAncestorObservation(t *testing.T) {
	f := startFake(t, "history-overlap", codex.Limits{})
	a, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	defer a.Close()
	child, err := codex.Load(context.Background(), codex.LoadOptions{Client: f.c, HistoryOptions: codex.HistoryOptions{ThreadID: "child", CWD: "/synthetic", CodexHome: "/synthetic-home", PageSize: 1}})
	require.NoError(t, err)
	defer child.Close()
	for range historyEvents(t, child) {
	}
	require.Equal(t, codex.LoadSucceeded, child.LoadState().Status)
	emitRequests(t, f.c, `{"method":"thread/started","params":{"thread":{"id":"child","parentThreadId":"native-thread-42"}}}`, `{"method":"future/both","params":{"threadId":"child"}}`)
	require.NoError(t, child.Close())
	emitRequests(t, f.c, `{"method":"future/after-child-close","params":{"threadId":"child"}}`)
	require.NoError(t, a.Close())
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	var methods []string
	for {
		record, err := a.Receive(ctx)
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
		if record.Event != nil {
			methods = append(methods, record.Event.Method)
		}
	}
	require.Equal(t, []string{"thread/started", "future/both", "future/after-child-close"}, methods)
	var childReleases int
	for _, frame := range trace(t, f.trace) {
		if frame.Method == "thread/unsubscribe" {
			var p types.ThreadUnsubscribeParams
			require.NoError(t, json.Unmarshal(frame.Params, &p))
			if p.ThreadID == "child" {
				childReleases++
			}
		}
	}
	require.Equal(t, 1, childReleases, "only the last observer releases the shared native attachment")
}
