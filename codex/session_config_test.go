package codex_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestSessionConfigurationNativeAckAndIndependentObservation(t *testing.T) {
	f := startFake(t, "session-config", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c, Thread: types.ThreadStartParams{Model: types.Some("creation-model")}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	_, err = s.Receive(context.Background())
	require.NoError(t, err)
	receipt, err := s.SetModel(context.Background(), "native-requested-model")
	require.NoError(t, err)
	require.True(t, receipt.NativeAccepted)
	require.Equal(t, "native-requested-model", receipt.Requested.Model.ValueOr(""))
	require.Equal(t, s.ThreadID(), receipt.Requested.ThreadID)
	raw, err := receipt.Response.MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(raw))
	// An independently authored notification may report a different model.
	emitRequests(t, f.c, `{"method":"thread/settings/updated","params":{"threadId":"main-1","threadSettings":{"cwd":"/tmp","approvalPolicy":"never","approvalsReviewer":"user","sandboxPolicy":{"type":"dangerFullAccess"},"model":"native-observed-model","modelProvider":"native-provider","collaborationMode":{"mode":"default","settings":{"model":"native-observed-model"}},"effort":"high","future":true}},"extra":1}`)
	got, err := s.Receive(context.Background())
	require.NoError(t, err)
	require.Equal(t, codex.ReceivedEvent, got.Kind)
	require.NoError(t, got.Event.DecodeError)
	notification, ok := got.Event.Data.(*types.ThreadSettingsUpdatedNotification)
	require.True(t, ok)
	require.Equal(t, "native-observed-model", notification.ThreadSettings.Model)
	require.Contains(t, string(got.Raw), `"extra":1`)
	require.Equal(t, "native-requested-model", receipt.Requested.Model.ValueOr(""))
	for _, frame := range trace(t, f.trace) {
		require.NotEqual(t, "turn/start", frame.Method)
		require.NotEqual(t, "thread/resume", frame.Method)
	}
}

func TestSessionConfigurationClosePreservesSharedStartedWrite(t *testing.T) {
	for _, closeOwner := range []bool{true, false} {
		t.Run(map[bool]string{true: "same-owner", false: "other-owner"}[closeOwner], func(t *testing.T) {
			f := startFake(t, "session-config-block-write", codex.Limits{})
			a, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
			require.NoError(t, err)
			b, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
			require.NoError(t, err)
			t.Cleanup(func() { _ = os.WriteFile(f.pid+".release", nil, 0600); _ = a.Abort(); _ = b.Abort() })
			result := make(chan error, 1)
			go func() { _, err := b.SetModel(context.Background(), strings.Repeat("x", 256<<10)); result <- err }()
			require.Eventually(t, func() bool { _, err := os.Stat(f.pid + ".writing"); return err == nil }, time.Second, time.Millisecond)
			closing := a
			if closeOwner {
				closing = b
			}
			closed := make(chan error, 1)
			go func() { closed <- closing.Close() }()
			select {
			case <-closing.Done():
			case <-time.After(time.Second):
				t.Fatal("owner did not close")
			}
			if closeOwner {
				select {
				case err := <-result:
					var call *codex.CallError
					require.ErrorAs(t, err, &call)
					require.Equal(t, codex.OutcomeUnknown, call.Dispatch)
				case <-time.After(time.Second):
					t.Fatal("configuration wait retained")
				}
			}
			require.NoError(t, os.WriteFile(f.pid+".release", nil, 0600))
			require.NoError(t, <-closed)
			if !closeOwner {
				require.NoError(t, <-result)
			}
			require.NoError(t, f.c.Call(context.Background(), "echo", json.RawMessage(`{}`), nil))
			require.NoError(t, f.c.Err())
		})
	}
}

func TestSessionConfigurationDoesNotSelectInputSchedulingOrTarget(t *testing.T) {
	f := startFake(t, "session-config", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	emitRequests(t, f.c,
		`{"method":"turn/started","params":{"threadId":"main-1","turn":{"id":"running","status":"inProgress","items":[]}}}`,
		`{"method":"thread/started","params":{"thread":{"id":"child","parentThreadId":"main-1"}}}`,
		`{"method":"turn/started","params":{"threadId":"child","turn":{"id":"child-running","status":"inProgress","items":[]}}}`)
	_, err = s.SetModel(context.Background(), "new-native-model")
	require.NoError(t, err)
	_, err = s.Submit(context.Background(), codex.Input{Input: []types.UserInput{}, Model: types.Some("input-native-model"), Effort: types.Some[types.ReasoningEffort]("low")})
	require.NoError(t, err)
	settings, inputs := 0, 0
	for _, frame := range trace(t, f.trace) {
		switch frame.Method {
		case "thread/settings/update":
			settings++
			require.JSONEq(t, `{"threadId":"main-1","model":"new-native-model"}`, string(frame.Params))
		case "turn/start":
			inputs++
			require.JSONEq(t, `{"threadId":"main-1","input":[],"model":"input-native-model","effort":"low"}`, string(frame.Params))
		case "turn/steer", "turn/interrupt", "thread/resume":
			t.Fatalf("unexpected scheduling/control %s", frame.Method)
		}
	}
	require.Equal(t, 1, settings)
	require.Equal(t, 1, inputs)
}

func TestSessionConfigurationNativeRejectionPreservesRequestAndSession(t *testing.T) {
	f := startFake(t, "session-config", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	receipt, err := s.SetModel(context.Background(), "native-reject")
	var rpc *codex.RPCError
	require.ErrorAs(t, err, &rpc)
	require.EqualValues(t, -32602, rpc.Code)
	require.Contains(t, string(rpc.Raw), `"future":1`)
	require.False(t, receipt.NativeAccepted)
	require.Equal(t, "native-reject", receipt.Requested.Model.ValueOr(""))
	var call *codex.CallError
	require.ErrorAs(t, err, &call)
	require.Equal(t, codex.Responded, call.Dispatch)
	_, err = s.SetThinking(context.Background(), codex.ThinkingChange{Effort: types.Some[types.ReasoningEffort]("native-future-effort")})
	require.NoError(t, err) // unknown native restrictions go to the peer, not a guessed enum gate
}

func TestSessionConfigurationUnknownAfterCancelCloseAndTimeout(t *testing.T) {
	for _, mode := range []string{"cancel", "close", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			limits := codex.Limits{}
			if mode == "timeout" {
				limits.CallTimeout = 50 * time.Millisecond
			}
			f := startFake(t, "session-config", limits)
			s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := s.SetModel(ctx, "native-withhold"); result <- err }()
			waitTrace(t, f.trace, "thread/settings/update")
			if mode == "cancel" {
				cancel()
			}
			if mode == "close" {
				require.NoError(t, s.Close())
			}
			select {
			case err := <-result:
				var call *codex.CallError
				require.ErrorAs(t, err, &call)
				require.Equal(t, codex.OutcomeUnknown, call.Dispatch)
			case <-time.After(time.Second):
				t.Fatal("configuration wait did not end")
			}
			require.NoError(t, f.c.Call(context.Background(), "echo", json.RawMessage(`{}`), nil))
			count := 0
			for _, frame := range trace(t, f.trace) {
				if frame.Method == "thread/settings/update" {
					count++
				}
			}
			require.Equal(t, 1, count)
		})
	}
}

func TestSessionConfigurationThinkingPreservesNativeOptionalEffort(t *testing.T) {
	f := startFake(t, "session-config", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	for _, effort := range []types.Optional[types.ReasoningEffort]{types.Some[types.ReasoningEffort]("high"), types.Null[types.ReasoningEffort](), {}} {
		receipt, err := s.SetThinking(context.Background(), codex.ThinkingChange{Effort: effort})
		require.NoError(t, err)
		require.True(t, receipt.NativeAccepted)
		require.Equal(t, effort, receipt.Requested.Effort)
		require.False(t, receipt.Requested.Model.IsSet())
	}
	var requests []string
	for _, frame := range trace(t, f.trace) {
		if frame.Method == "thread/settings/update" {
			requests = append(requests, string(frame.Params))
		}
	}
	require.Len(t, requests, 3)
	require.JSONEq(t, `{"threadId":"main-1","effort":"high"}`, requests[0])
	require.JSONEq(t, `{"threadId":"main-1","effort":null}`, requests[1])
	require.JSONEq(t, `{"threadId":"main-1"}`, requests[2])
}

func TestSessionConfigurationKnownUnsupportedAndInvalidAreNotSent(t *testing.T) {
	for _, mode := range []string{"session-config-unsupported", "session-config"} {
		t.Run(mode, func(t *testing.T) {
			f := startFake(t, mode, codex.Limits{})
			s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			model := "native-model"
			expected := codex.ErrUnsupportedConfiguration
			if mode == "session-config" {
				model = ""
				expected = codex.ErrInvalidInput
			}
			receipt, err := s.SetModel(context.Background(), model)
			require.ErrorIs(t, err, expected)
			var call *codex.CallError
			require.ErrorAs(t, err, &call)
			require.Equal(t, codex.NotSent, call.Dispatch)
			require.False(t, receipt.NativeAccepted)
			for _, frame := range trace(t, f.trace) {
				require.NotEqual(t, "thread/settings/update", frame.Method)
			}
			// A local configuration refusal never consumes/terminates the Session.
			_, err = s.Submit(context.Background(), codex.Input{Input: []types.UserInput{}})
			require.NoError(t, err)
		})
	}
}

func TestSessionConfigurationPreDispatchFailuresKeepSessionUsable(t *testing.T) {
	f := startFake(t, "session-config", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.SetModel(ctx, "native-model")
	require.ErrorIs(t, err, context.Canceled)
	var call *codex.CallError
	require.ErrorAs(t, err, &call)
	require.Equal(t, codex.NotSent, call.Dispatch)
	_, err = s.SetThinking(context.Background(), codex.ThinkingChange{Effort: types.Some[types.ReasoningEffort]("")})
	require.ErrorIs(t, err, codex.ErrInvalidInput)
	require.ErrorAs(t, err, &call)
	require.Equal(t, codex.NotSent, call.Dispatch)
	_, err = s.SetModel(context.Background(), "usable-model")
	require.NoError(t, err)
	require.NoError(t, s.Close())
	_, err = s.SetModel(context.Background(), "closed-model")
	require.ErrorIs(t, err, codex.ErrClosed)
	require.ErrorAs(t, err, &call)
	require.Equal(t, codex.NotSent, call.Dispatch)
	count := 0
	for _, frame := range trace(t, f.trace) {
		if frame.Method == "thread/settings/update" {
			count++
		}
	}
	require.Equal(t, 1, count)
}
