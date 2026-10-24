package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadInitializeDoesNotConfirmResume(t *testing.T) {
	o := historyTranscript(t, map[string]any{"type": "user", "uuid": "u", "parentUuid": nil, "message": map[string]any{"content": "never replay"}})
	s, err := Load(context.Background(), loadOptions(t, o))
	require.NoError(t, err)
	defer s.Close()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("load did not terminate")
	}
	require.Equal(t, LoadFailed, s.LoadState().Status)
	require.Equal(t, o.SessionID, s.LoadState().RequestedID)
	require.Empty(t, s.LoadState().ObservedID)
	require.ErrorIs(t, s.LoadState().Err, ErrResumeUnconfirmed)
	require.ErrorIs(t, s.Err(), ErrResumeUnconfirmed)
	boundaries := 0
	for {
		r, e := s.Receive(context.Background())
		if e != nil {
			require.ErrorIs(t, e, ErrResumeUnconfirmed)
			break
		}
		require.NotEqual(t, "history", r.Origin)
		if r.Kind == ReceivedLoad {
			boundaries++
			require.Equal(t, LoadFailed, r.Load.Status)
			require.ErrorIs(t, r.Load.Err, ErrResumeUnconfirmed)
		}
	}
	require.Equal(t, 1, boundaries)
	_, err = s.Submit(context.Background(), Input{Text: "forbidden"})
	require.ErrorIs(t, err, ErrLoadFailed)
}

func TestLoadWrongIdentityPreservesActualFrame(t *testing.T) {
	for _, mode := range []string{"session_load_bad_id", "session_load_bad_id_empty_parent"} {
		t.Run(mode, func(t *testing.T) {
			o := historyTranscript(t, map[string]any{"type": "user", "uuid": "u", "parentUuid": nil, "message": map[string]any{"content": "stored"}})
			opts := loadOptions(t, o)
			opts.Options.Env = append(opts.Options.Env, "CLAUDE_GO_FIXTURE="+mode)
			s, err := Load(context.Background(), opts)
			require.NoError(t, err)
			defer s.Close()
			select {
			case <-s.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("not terminated")
			}
			require.ErrorIs(t, s.Err(), ErrIdentityMismatch)
			state := s.LoadState()
			require.Equal(t, "wrong-native-id", state.ObservedID)
			var mismatch *IdentityMismatchError
			require.ErrorAs(t, state.Err, &mismatch)
			require.Equal(t, o.SessionID, mismatch.RequestedID)
			require.Contains(t, string(mismatch.Raw), "wrong-native-id")
		})
	}
}

// This peer tests transport/control mechanics before initialize, not native
// recovery success. It never claims a resume receipt or starts a query.
func runLoadPendingFixture(reader *bufio.Reader, initializeID any) {
	trace, err := os.Create(os.Getenv("CLAUDE_GO_TRANSCRIPT"))
	if err != nil {
		os.Exit(21)
	}
	defer trace.Close()
	fixtureEmit(map[string]any{"type": "system", "subtype": "init", "session_id": os.Getenv("CLAUDE_GO_SESSION_ID"), "claude_code_version": "2.1.258"})
	fixtureEmit(map[string]any{"type": "result", "session_id": os.Getenv("CLAUDE_GO_SESSION_ID"), "result": "live fact"})
	for _, id := range []string{"answer", "pending"} {
		fixtureEmit(map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Read", "input": map[string]any{}}})
	}
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		if _, err = trace.Write(line); err != nil {
			os.Exit(22)
		}
		var w struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
			Request   struct {
				Subtype string `json:"subtype"`
			} `json:"request"`
		}
		if json.Unmarshal(line, &w) != nil {
			os.Exit(23)
		}
		if w.Type == "control_request" && w.Request.Subtype == "interrupt" {
			sessionReply(w.RequestID, "")
			sessionReply(initializeID.(string), "")
		}
	}
}

func TestLoadPendingControlsAndCleanup(t *testing.T) {
	for _, mode := range []string{"interrupt", "cancel", "close"} {
		t.Run(mode, func(t *testing.T) {
			o := historyTranscript(t, map[string]any{"type": "user", "uuid": "u", "parentUuid": nil, "message": map[string]any{"content": "no history permissions"}})
			opts := loadOptions(t, o)
			trace := filepath.Join(t.TempDir(), "wire.jsonl")
			opts.Options.Env = append(opts.Options.Env, "CLAUDE_GO_FIXTURE=session_load_pending", "CLAUDE_GO_TRANSCRIPT="+trace)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s, err := Load(ctx, opts)
			require.NoError(t, err)
			defer s.Close()
			receiveCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			var requests []*IncomingRequest
			var native []string
			for len(requests) < 2 {
				r, err := s.Receive(receiveCtx)
				require.NoError(t, err)
				require.NotEqual(t, "history", r.Origin)
				if r.Event != nil {
					native = append(native, r.Event.Type)
				}
				if r.Request != nil {
					requests = append(requests, r.Request)
				}
			}
			require.Equal(t, []string{"system", "result"}, native)
			require.Equal(t, o.SessionID, s.LoadState().ObservedID)
			require.Empty(t, s.LoadState().Evidence)
			require.Equal(t, LoadPending, s.LoadState().Status)
			require.NoError(t, s.Err())
			_, err = s.Submit(receiveCtx, Input{Text: "must never send"})
			require.ErrorIs(t, err, ErrLoadPending)
			_, err = s.SetModel(receiveCtx, ModelChange{})
			require.ErrorIs(t, err, ErrLoadPending)
			_, err = s.SetThinking(receiveCtx, ThinkingChange{})
			require.ErrorIs(t, err, ErrLoadPending)
			require.NoError(t, requests[0].Approve(receiveCtx, ApprovalOptions{}))
			switch mode {
			case "interrupt":
				require.NoError(t, s.Interrupt(receiveCtx, InterruptOptions{}))
			case "cancel":
				cancel()
			case "close":
				require.NoError(t, s.Close())
			}
			select {
			case <-s.Done():
			case <-receiveCtx.Done():
				t.Fatal("cleanup waited for consumption")
			}
			cause := s.Err()
			state := s.LoadState()
			require.Equal(t, LoadFailed, state.Status)
			switch mode {
			case "interrupt":
				require.ErrorIs(t, state.Err, ErrResumeUnconfirmed)
			case "cancel":
				require.ErrorIs(t, state.Err, context.Canceled)
			case "close":
				require.ErrorIs(t, state.Err, ErrClosed)
			}
			_ = s.Close()
			require.Equal(t, cause, s.Err(), "cleanup replaced first error")
			require.Error(t, requests[1].Context().Err())
			require.Error(t, requests[1].Approve(receiveCtx, ApprovalOptions{}))
			data, err := os.ReadFile(trace)
			require.NoError(t, err)
			require.NotContains(t, string(data), "must never send")
			require.NotContains(t, string(data), "set_model")
			require.NotContains(t, string(data), "set_max_thinking_tokens")
			var frames []map[string]any
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var w map[string]any
				require.NoError(t, json.Unmarshal([]byte(line), &w))
				frames = append(frames, w)
			}
			require.Equal(t, "control_response", frames[0]["type"])
			if mode == "interrupt" {
				require.Len(t, frames, 2)
			} else {
				require.Len(t, frames, 1)
			}
			boundaries := 0
			for {
				r, err := s.Receive(receiveCtx)
				if err != nil {
					if cause == nil {
						require.ErrorIs(t, err, io.EOF)
					} else {
						require.ErrorIs(t, err, cause)
					}
					break
				}
				require.Equal(t, ReceivedLoad, r.Kind)
				require.Equal(t, LoadFailed, r.Load.Status)
				boundaries++
			}
			require.Equal(t, 1, boundaries)
		})
	}
}

func TestLoadStartupFailureClasses(t *testing.T) {
	for _, mode := range []string{"start-failure", "init_error", "init_disconnect", "init_hang", "config-conflict"} {
		t.Run(mode, func(t *testing.T) {
			o := historyTranscript(t, map[string]any{"type": "user", "uuid": "u", "parentUuid": nil, "message": map[string]any{"content": "stored"}})
			opts := loadOptions(t, o)
			opts.Options.Env = append(opts.Options.Env, "CLAUDE_GO_FIXTURE="+mode)
			switch mode {
			case "start-failure":
				opts.Options.CLIPath = filepath.Join(t.TempDir(), "missing")
			case "init_hang":
				opts.Options.InitializeTimeout = 100 * time.Millisecond
			case "config-conflict":
				opts.Options.ResumeID = "other"
			}
			s, err := Load(context.Background(), opts)
			if mode == "config-conflict" {
				require.Error(t, err)
				require.Nil(t, s)
				return
			}
			require.NoError(t, err)
			defer s.Close()
			select {
			case <-s.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("startup did not fail")
			}
			require.Equal(t, LoadFailed, s.LoadState().Status)
			require.Error(t, s.Err())
			require.NotErrorIs(t, s.Err(), ErrResumeUnconfirmed)
			if mode == "init_error" {
				var native *ControlError
				require.ErrorAs(t, s.Err(), &native)
				require.Contains(t, native.Message, "refused")
			}
			if mode == "init_hang" {
				require.ErrorIs(t, s.Err(), context.DeadlineExceeded)
			}
			if mode == "init_disconnect" {
				require.ErrorIs(t, s.Err(), ErrUnexpectedEOF)
			}
			for {
				r, err := s.Receive(context.Background())
				if err != nil {
					break
				}
				require.NotEqual(t, "history", r.Origin)
			}
		})
	}
}

func TestLoadImmediateCloseIsIndependentOfStartupAndDrain(t *testing.T) {
	o := historyTranscript(t, map[string]any{"type": "user", "uuid": "u", "parentUuid": nil, "message": map[string]any{"content": "stored"}})
	opts := loadOptions(t, o)
	opts.Options.Env = append(opts.Options.Env, "CLAUDE_GO_FIXTURE=init_hang")
	s, err := Load(context.Background(), opts)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	require.NoError(t, s.Err())
	require.Equal(t, LoadFailed, s.LoadState().Status)
	require.ErrorIs(t, s.LoadState().Err, ErrClosed)
	r, err := s.Receive(context.Background())
	require.NoError(t, err)
	require.Equal(t, ReceivedObservation, r.Kind)
	r, err = s.Receive(context.Background())
	require.NoError(t, err)
	require.Equal(t, ReceivedLoad, r.Kind)
	require.ErrorIs(t, r.Load.Err, ErrClosed)
	_, err = s.Receive(context.Background())
	require.ErrorIs(t, err, io.EOF)
}

func TestLoadBoundarySnapshotCannotMutateIdentityFailure(t *testing.T) {
	o := historyTranscript(t, map[string]any{"type": "user", "uuid": "u", "parentUuid": nil, "message": map[string]any{"content": "stored"}})
	opts := loadOptions(t, o)
	opts.Options.Env = append(opts.Options.Env, "CLAUDE_GO_FIXTURE=session_load_bad_id")
	s, err := Load(context.Background(), opts)
	require.NoError(t, err)
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var boundary *LoadState
	for boundary == nil {
		r, err := s.Receive(ctx)
		require.NoError(t, err)
		boundary = r.Load
	}
	require.Equal(t, LoadFailed, boundary.Status)
	var first *IdentityMismatchError
	require.ErrorAs(t, boundary.Err, &first)
	raw := append([]byte(nil), first.Raw...)
	first.Raw[0] = '['
	first.ObservedID = "mutated"
	boundary.ObservedID = "mutated"
	var snapshot *IdentityMismatchError
	require.ErrorAs(t, s.LoadState().Err, &snapshot)
	require.Equal(t, raw, []byte(snapshot.Raw))
	require.Equal(t, "wrong-native-id", snapshot.ObservedID)
	snapshot.Raw[0] = '['
	var next *IdentityMismatchError
	require.ErrorAs(t, s.LoadState().Err, &next)
	require.Equal(t, raw, []byte(next.Raw))
	_, err = s.Receive(ctx)
	require.ErrorIs(t, err, ErrIdentityMismatch)
}

func TestLoadProtocolErrorSnapshotsRetainDetachedRaw(t *testing.T) {
	o := historyTranscript(t, map[string]any{"type": "user", "uuid": "u", "parentUuid": nil, "message": map[string]any{"content": "stored"}})
	opts := loadOptions(t, o)
	opts.Options.Env = append(opts.Options.Env, "CLAUDE_GO_FIXTURE=session_load_malformed")
	s, err := Load(context.Background(), opts)
	require.NoError(t, err)
	defer s.Close()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("load did not fail")
	}
	var first *Error
	require.ErrorAs(t, s.LoadState().Err, &first)
	raw := append([]byte(nil), first.Raw...)
	require.NotEmpty(t, raw)
	first.Raw[0] = '!'
	var next *Error
	require.ErrorAs(t, s.LoadState().Err, &next)
	require.Equal(t, raw, []byte(next.Raw))
	var boundary *LoadState
	for boundary == nil {
		r, e := s.Receive(context.Background())
		require.NoError(t, e)
		boundary = r.Load
	}
	var failure *Error
	require.ErrorAs(t, boundary.Err, &failure)
	require.Equal(t, raw, []byte(failure.Raw))
	failure.Raw[0] = '!'
	require.ErrorAs(t, s.LoadState().Err, &next)
	require.Equal(t, raw, []byte(next.Raw))
}
