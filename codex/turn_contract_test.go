package codex_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

const nativeTurn = `{"id":"native-turn-response","items":[],"status":"inProgress","future":9007199254740993}`
const turnDelta = `{"threadId":"native-thread-42","turnId":"native-turn-response","itemId":"native-item-7","delta":"hello","future":{"x":9007199254740993}}`
const knownItem = `{"type":"agentMessage","id":"native-item-7","text":"hello","phase":"final_answer"}`
const unknownItem = `{"type":"futureItem","id":"native-item-future","unknown":[null,9007199254740993]}`

// Fake-peer modes below are independently written. The peer deliberately
// withholds the start response until a second RPC, proving early delivery without
// relying on how quickly the Go scheduler consumes the pipe.
type turnPeer struct{ pending json.RawMessage }

func (p *turnPeer) handle(mode string, m fakeEnvelope, send func(json.RawMessage, string)) bool {
	emit := func(method, params string) { fmt.Fprintf(os.Stdout, "{\"method\":%q,\"params\":%s}\n", method, params) }
	switch m.Method {
	case "turn/start":
		switch mode {
		case "turn-error":
			fmt.Fprintf(os.Stdout, "{\"id\":%s,\"error\":{\"code\":-32603,\"message\":\"internal error after possible execution\"}}\n", m.ID)
		case "turn-exit":
			os.Exit(9)
		case "turn-invalid-response":
			send(m.ID, `{"turn":{"id":7}}`)
		case "turn-wait":
			p.pending = append([]byte(nil), m.ID...)
		default:
			p.pending = append([]byte(nil), m.ID...)
			emit("turn/completed", `{"threadId":"native-thread-42","turn":{"id":"stale-turn","items":[],"status":"completed"}}`)
			emit("turn/started", `{"threadId":"other-thread","turn":{"id":"other-thread-turn","items":[],"status":"inProgress"}}`)
			emit("turn/started", `{"threadId":"native-thread-42","turn":`+nativeTurn+`}`)
			emit("item/agentMessage/delta", `{"threadId":"native-thread-42","turnId":"other-turn","itemId":"other-item","delta":"stale"}`)
			emit("item/agentMessage/delta", turnDelta)
			emit("item/started", `{"threadId":"native-thread-42","turnId":"native-turn-response","startedAtMs":7,"item":`+knownItem+`}`)
			emit("item/completed", `{"threadId":"native-thread-42","turnId":"native-turn-response","completedAtMs":8,"item":`+unknownItem+`}`)
			emit("turn/completed", `{"threadId":"native-thread-42","turn":{"id":"native-turn-response","items":[],"status":"completed"}}`)
		}
	case "release-start":
		send(p.pending, `{"turn":`+nativeTurn+`}`)
		send(m.ID, `{}`)
	case "turn/interrupt":
		send(m.ID, `{"futureAck":true}`)
	case "turn-emit":
		emit("item/agentMessage/delta", turnDelta)
		send(m.ID, `{}`)
	case "turn-item":
		emit("item/completed", `{"threadId":"native-thread-42","turnId":"native-turn-response","completedAtMs":8,"item":`+unknownItem+`}`)
		send(m.ID, `{}`)
	case "turn-flood":
		for i := 0; i < 4096; i++ {
			emit("future/ordered", fmt.Sprintf(`{"threadId":"native-thread-42","sequence":%d}`, i))
		}
		send(m.ID, `"reader-still-routing"`)
	case "turn-mixed":
		emit("future/raw", `{ "n" : 9007199254740993, "nested": [null, true] }`)
		emit("future/raw", `null`)
		fmt.Fprintln(os.Stdout, `{"method":"future/absent"}`)
		emit("item/agentMessage/delta", `{"threadId":false,"turnId":"x","itemId":"x","delta":"bad"}`)
		emit("turn/completed", `{"threadId":"x"}`)
		emit("turn/started", `null`)
		fmt.Fprintln(os.Stdout, `{"method":"item/started"}`)
		emit("item/completed", `{"threadId":"x","turnId":"x","completedAtMs":1,"item":{"type":"agentMessage","id":"missing-text"}}`)
		emit("item/agentMessage/delta", `{"threadId":"x","threadId":"y","turnId":"x","itemId":"x","delta":"duplicate"}`)
		emit("error", `{"threadId":"native-thread-42","turnId":"native-turn-response","willRetry":false,"error":{"message":"synthetic error","codexErrorInfo":{"futureError":{"n":9007199254740993}}}}`)
		emit("item/agentMessage/delta", turnDelta)
		send(m.ID, `{}`)
	case "turn-malformed-envelope":
		fmt.Fprintln(os.Stdout, `{"method":false,"params":{}}`)
	case "turn-oversized-envelope":
		emit("future/raw", `"`+strings.Repeat("x", 10000)+`"`)
	default:
		return false
	}
	return true
}

func openTurnSession(t *testing.T, c *codex.Client, ctx context.Context) *codex.Session {
	t.Helper()
	s, err := codex.Open(ctx, codex.SessionOptions{Client: c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Abort() })
	return s
}
func nextTurnEvent(t *testing.T, s *codex.Session) types.Event {
	t.Helper()
	return *nextSessionNative(t, s).Event
}
func awaitTurnSession(t *testing.T, s *codex.Session, cause error) {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Session did not terminate")
	}
	require.ErrorIs(t, s.Err(), cause)
}
func turnInput(p types.TurnStartParams) codex.Input {
	return codex.Input{Input: p.Input, ServiceTier: p.ServiceTier, DisabledPluginIDs: p.DisabledPluginIDs, OutputSchema: p.OutputSchema, ParentTurnID: p.ParentTurnID, RootTurnID: p.RootTurnID}
}
func startParams() types.TurnStartParams {
	return types.TurnStartParams{ThreadID: "native-thread-42", Input: []types.UserInput{{Text: types.Ptr(types.TextUserInput{Text: "synthetic input"})}}, ServiceTier: types.Null[string](), DisabledPluginIDs: types.Some([]string{}), OutputSchema: types.Some(json.RawMessage(`{"type":"string"}`))}
}

func (obs *observationSuite) TestTurnEarlyEventsKeepResponseIdentity() {
	t := obs.T()
	f := obs.fixture(t, "turn-early", codex.Limits{EventQueue: 16})
	s := openTurnSession(t, f.c, context.Background())
	type outcome struct {
		r   types.TurnStartResponse
		err error
	}
	result := make(chan outcome, 1)
	params := startParams()
	params.ParentTurnID = types.Some("causing-turn")
	params.RootTurnID = types.Some("chain-root-turn")
	go func() {
		r, err := s.Submit(context.Background(), turnInput(params))
		result <- outcome{r.Response, err}
	}()
	var events []types.Event

	for i := 0; i < 7; i++ {
		e := nextTurnEvent(t, s)
		require.False(t, e.DecodeError != nil || e.Data == nil, "event %d: %#v", i, e)

		events = append(events, e)
	}
	require.False(t, events[0].Data.(*types.TurnCompletedNotification).Turn.ID != "stale-turn" || events[2].Data.(*types.AgentMessageDeltaNotification).TurnID != "other-turn", "interleaved native identities changed")
	require.Contains(t, string(f.c.Unattributed().Records[0].Raw), "other-thread-turn")
	require.Equal(t, "native-item-7", events[4].Data.(*types.ItemStartedNotification).Item.AgentMessage.ID, "native item identity changed")
	require.Equal(t, unknownItem, string(events[5].Data.(*types.ItemCompletedNotification).Item.Unknown), "unknown native variant lost")
	require.Equal(t, "native-turn-response", events[6].Data.(*types.TurnCompletedNotification).Turn.ID, "early completion lost")
	select {
	case <-result:
		require.FailNow(t, "test invariant", "an event fabricated a start response")
	default:
	}
	{
		err := f.c.Call(context.Background(), "release-start", nil, nil)
		require.NoError(t, err, err)
	}
	got := <-result
	require.False(t, got.err != nil || got.r.Turn.ID != "native-turn-response" || got.r.Turn.Status != types.TurnStatusInProgress, "response replaced by event: %+v %v", got.r, got.err)
	var starts int
	for _, m := range trace(t, f.trace) {
		if m.Method == "turn/start" {
			starts++
			var p map[string]json.RawMessage
			_ = json.Unmarshal(m.Params, &p)
			require.False(t, string(p["threadId"]) != `"native-thread-42"` || string(p["serviceTier"]) != "null" || string(p["disabledPluginIds"]) != "[]" || string(p["outputSchema"]) != `{"type":"string"}` || string(p["parentTurnId"]) != `"causing-turn"` || string(p["rootTurnId"]) != `"chain-root-turn"`, "native params changed: %s", m.Params)
		}
	}
	require.Equal(t, 1, starts, "sent turn/start %d times", starts)
}

func (obs *observationSuite) TestNotificationRawAndDecodeErrors() {
	t := obs.T()
	f := obs.fixture(t, "normal", codex.Limits{EventQueue: 16})
	s := openTurnSession(t, f.c, context.Background())
	{
		err := f.c.Call(context.Background(), "turn-mixed", nil, nil)
		require.NoError(t, err, err)
	}
	expected := []string{
		`{ "n" : 9007199254740993, "nested": [null, true] }`, `null`, ``,
		`{"threadId":false,"turnId":"x","itemId":"x","delta":"bad"}`, `{"threadId":"x"}`, `null`, ``,
		`{"threadId":"x","turnId":"x","completedAtMs":1,"item":{"type":"agentMessage","id":"missing-text"}}`,
		`{"threadId":"x","threadId":"y","turnId":"x","itemId":"x","delta":"duplicate"}`,
	}
	seen := make(map[string]types.Event)
	records := f.c.Unattributed().Records
	require.Len(t, records, len(expected))
	for i, raw := range expected {
		var envelope map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(records[i].Raw, &envelope))
		require.Equal(t, raw, string(envelope["params"]))
	}
	for range 2 {
		e := nextTurnEvent(t, s)
		seen[string(e.Params)] = e
	}

	for i, raw := range expected {
		var envelope struct {
			Method string `json:"method"`
		}
		require.NoError(t, json.Unmarshal(records[i].Raw, &envelope))
		if i < 3 {
			continue
		}
		switch envelope.Method {
		case "item/agentMessage/delta":
			var v types.AgentMessageDeltaNotification
			require.Error(t, json.Unmarshal([]byte(raw), &v))
		case "turn/completed":
			var v types.TurnCompletedNotification
			require.Error(t, json.Unmarshal([]byte(raw), &v))
		case "turn/started":
			var v types.TurnStartedNotification
			require.Error(t, json.Unmarshal([]byte(raw), &v))
		case "item/started":
			var v types.ItemStartedNotification
			require.Error(t, json.Unmarshal([]byte(raw), &v))
		case "item/completed":
			var v types.ItemCompletedNotification
			require.Error(t, json.Unmarshal([]byte(raw), &v))
		}
	}

	e := seen[`{"threadId":"native-thread-42","turnId":"native-turn-response","willRetry":false,"error":{"message":"synthetic error","codexErrorInfo":{"futureError":{"n":9007199254740993}}}}`]
	native := e.Data.(*types.ErrorNotification)
	info, ok := native.Error.CodexErrorInfo.Get()
	require.False(t, !ok || native.WillRetry || string(info.Unknown) != `{"futureError":{"n":9007199254740993}}`, "error data lost: %#v", e)
	e = seen[turnDelta]
	d := e.Data.(*types.AgentMessageDeltaNotification)
	require.False(t, d.ItemID != "native-item-7" || string(d.UnknownFields["future"]) != `{"x":9007199254740993}`, "valid event lost: %#v", e)
	require.False(t, s.Err() != nil || f.c.Err() != nil, "bad payload terminated connection or stream")
}

func (obs *observationSuite) TestSessionOverflowIsolatedAndInspectable() {
	t := obs.T()
	f := obs.fixture(t, "session-multiple", codex.Limits{EventQueue: 1, Subscriptions: 2})
	slow := openTurnSession(t, f.c, context.Background())
	healthy := openTurnSession(t, f.c, context.Background())
	nextTurnEvent(t, slow)
	nextTurnEvent(t, healthy)
	_, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.ErrorIs(t, err, codex.ErrLimit)
	for range 4 {
		emitRequests(t, f.c, treeFrame("future/ordered", `{"threadId":"main-1"}`), treeFrame("future/ordered", `{"threadId":"main-2"}`))
		require.Equal(t, "future/ordered", nextTurnEvent(t, healthy).Method)
	}
	require.NoError(t, slow.Close())
	awaitTurnSession(t, slow, nil)
	for range 4 {
		require.Equal(t, "future/ordered", nextTurnEvent(t, slow).Method)
	}
	_, err = slow.Receive(context.Background())
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, healthy.Close())
	replacement := openTurnSession(t, f.c, context.Background())
	require.NoError(t, replacement.Close())
}

func (obs *observationSuite) TestSessionSlowConsumerNeverBlocksResponse() {
	t := obs.T()
	f := obs.fixture(t, "normal", codex.Limits{EventQueue: 1})
	s := openTurnSession(t, f.c, context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var answer string
	{
		err := f.c.Call(ctx, "turn-flood", nil, &answer)
		require.False(t, err != nil || answer != "reader-still-routing", "reader blocked: %s %v", answer, err)
	}
	require.NoError(t, s.Err())
	require.NoError(t, s.Close())
	awaitTurnSession(t, s, nil)
	for i := 0; i < 4096; i++ {
		e := nextTurnEvent(t, s)
		var params struct {
			Sequence int `json:"sequence"`
		}
		require.NoError(t, json.Unmarshal(e.Params, &params))
		require.Equal(t, i, params.Sequence)
	}
	_, end := s.Receive(context.Background())
	require.ErrorIs(t, end, io.EOF)
	require.Equal(t, uint64(4096), f.c.Diagnostics().Notifications, "reader failed to consume complete flood")
}

func (obs *observationSuite) TestSessionOwnsPayloadAndNoReplay() {
	t := obs.T()
	f := obs.fixture(t, "history-overlap", codex.Limits{})
	require.NoError(t, f.c.Call(context.Background(), "turn-emit", nil, nil))
	first := openTurnSession(t, f.c, context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	_, err := first.Receive(ctx)
	cancel()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	emitRequests(t, f.c, treeFrame("thread/started", `{"thread":{"id":"child","parentThreadId":"native-thread-42"}}`))
	nextTurnEvent(t, first)
	second, err := codex.Load(context.Background(), codex.LoadOptions{Client: f.c, HistoryOptions: codex.HistoryOptions{ThreadID: "child", CWD: "/synthetic", CodexHome: "/synthetic-home", PageSize: 1}})
	require.NoError(t, err)
	defer second.Close()
	for {
		r, err := second.Receive(context.Background())
		require.NoError(t, err)
		if r.Load != nil {
			break
		}
	}
	delta := strings.ReplaceAll(turnDelta, "native-thread-42", "child")
	emitRequests(t, f.c, treeFrame("item/agentMessage/delta", delta))
	a, b := nextTurnEvent(t, first), nextTurnEvent(t, second)
	a.Params[0] = '!'
	a.Raw[0] = '!'
	a.Data.(*types.AgentMessageDeltaNotification).UnknownFields["future"][0] = '!'
	a.Data.(*types.AgentMessageDeltaNotification).ThreadID = "mutated"
	require.Equal(t, delta, string(b.Params))
	require.Equal(t, "child", b.Data.(*types.AgentMessageDeltaNotification).ThreadID)
	require.Equal(t, `{"x":9007199254740993}`, string(b.Data.(*types.AgentMessageDeltaNotification).UnknownFields["future"]))
	item := treeFrame("item/completed", `{"threadId":"child","turnId":"t","completedAtMs":8,"item":`+unknownItem+`}`)
	emitRequests(t, f.c, item)
	a, b = nextTurnEvent(t, first), nextTurnEvent(t, second)
	a.Data.(*types.ItemCompletedNotification).Item.Unknown[0] = '!'
	require.Equal(t, unknownItem, string(b.Data.(*types.ItemCompletedNotification).Item.Unknown))
}

func (obs *observationSuite) TestCancellationDoesNotInterruptTurn() {
	t := obs.T()
	f := obs.fixture(t, "turn-wait", codex.Limits{})
	subctx, subcancel := context.WithCancel(context.Background())
	s := openTurnSession(t, f.c, subctx)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := s.Submit(ctx, turnInput(startParams())); result <- err }()
	waitTrace(t, f.trace, "turn/start")
	// Establish a completed write, rather than only peer receipt, before
	// testing cancellation of local waiting on a still-usable connection.
	responseBarrier(t, f)
	cancel()
	subcancel()
	callError(t, <-result, codex.OutcomeUnknown, context.Canceled)
	_, waitErr := s.Receive(subctx)
	require.ErrorIs(t, waitErr, context.Canceled)
	require.NoError(t, s.Err())
	// This response is late for the canceled start. A barrier establishes that
	// all writes/notifications before the explicit interrupt have been handled.
	{
		err := f.c.Call(context.Background(), "release-start", nil, nil)
		require.NoError(t, err, err)
	}
	for _, m := range trace(t, f.trace) {
		require.False(t, m.Method == "turn/interrupt" || strings.Contains(m.Method, "cancel"), "implicit cancellation sent: %s", m.Method)
	}
	live := s
	ack, err := f.c.InterruptTurn(context.Background(), types.TurnInterruptParams{ThreadID: "native-thread-42", TurnID: "native-turn-response"})
	require.False(t, err != nil || string(ack.UnknownFields["futureAck"]) != "true", "interrupt ack: %+v %v", ack, err)
	waitCtx, stopWait := context.WithTimeout(context.Background(), 10*time.Millisecond)
	_, waitErr = live.Receive(waitCtx)
	stopWait()
	require.ErrorIs(t, waitErr, context.DeadlineExceeded, "ack fabricated execution event")

	count := 0
	for _, m := range trace(t, f.trace) {
		if m.Method == "turn/interrupt" {
			count++
			require.Equal(t, `{"threadId":"native-thread-42","turnId":"native-turn-response"}`, string(m.Params), "interrupt params: %s", m.Params)
		}
	}
	require.Equal(t, 1, count, "interrupt count: %d", count)
}

func (obs *observationSuite) TestTurnFailureNeverReplays() {
	t := obs.T()
	for _, mode := range []string{"turn-error", "turn-exit", "turn-invalid-response"} {
		t.Run(mode, func(t *testing.T) {
			f := obs.fixture(t, mode, codex.Limits{})
			s := openTurnSession(t, f.c, context.Background())
			_, err := s.Submit(context.Background(), turnInput(startParams()))
			state := codex.Responded
			if mode == "turn-exit" {
				state = codex.OutcomeUnknown
				awaitTurnSession(t, s, io.EOF)
			}
			callError(t, err, state, nil)
			if mode == "turn-error" {
				var rpc *codex.RPCError
				require.False(t, !errors.As(err, &rpc) || rpc.Code != -32603, err)
			}
			count := 0
			for _, m := range trace(t, f.trace) {
				if m.Method == "turn/start" {
					count++
				}
			}
			require.Equal(t, 1, count, "turn/start replayed: %d", count)
		})
	}
}

func (obs *observationSuite) TestSessionTerminalStatus() {
	t := obs.T()
	for _, method := range []string{"turn-malformed-envelope", "turn-oversized-envelope"} {
		t.Run(method, func(t *testing.T) {
			f := obs.fixture(t, "normal", codex.Limits{MaxFrameBytes: 4096})
			s := openTurnSession(t, f.c, context.Background())
			require.NoError(t, f.c.Call(context.Background(), "turn-emit", nil, nil))
			callError(t, f.c.Call(context.Background(), method, nil, nil), codex.OutcomeUnknown, codex.ErrProtocol)
			awaitTurnSession(t, s, codex.ErrProtocol)
			if method == "turn-oversized-envelope" {
				require.ErrorIs(t, s.Err(), codex.ErrFrameTooLarge)
			}
			nextTurnEvent(t, s)
			_, err := s.Receive(context.Background())
			require.ErrorIs(t, err, codex.ErrProtocol)
			_ = s.Close()
			require.ErrorIs(t, s.Err(), codex.ErrProtocol)
		})
	}
	t.Run("client-close", func(t *testing.T) {
		f := obs.fixture(t, "normal", codex.Limits{EventQueue: 1})
		s := openTurnSession(t, f.c, context.Background())
		require.NoError(t, f.c.Call(context.Background(), "turn-emit", nil, nil))
		require.NoError(t, f.c.Close())
		awaitTurnSession(t, s, nil)
		nextTurnEvent(t, s)
		_, err := s.Receive(context.Background())
		require.ErrorIs(t, err, io.EOF)
		_, err = codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
		require.ErrorIs(t, err, codex.ErrClosed)
	})
	for _, stop := range []string{"cancel-full", "deadline"} {
		t.Run(stop, func(t *testing.T) {
			f := obs.fixture(t, "normal", codex.Limits{EventQueue: 1})
			s := openTurnSession(t, f.c, context.Background())
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			if stop == "cancel-full" {
				require.NoError(t, f.c.Call(context.Background(), "turn-emit", nil, nil))
				cancel()
			}
			_, err := s.Receive(ctx)
			want := error(context.DeadlineExceeded)
			if stop == "cancel-full" {
				want = context.Canceled
			}
			require.ErrorIs(t, err, want)
			require.NoError(t, s.Err())
			if stop == "cancel-full" {
				nextTurnEvent(t, s)
			}
			require.NoError(t, s.Close())
			awaitTurnSession(t, s, nil)
		})
	}
}

func TestSessionConcurrentLifecycle(t *testing.T) {
	f := startFake(t, "session-multiple", codex.Limits{EventQueue: 2, Subscriptions: 128, PendingCalls: 128, OutboundQueue: 128})
	sessions := make([]*codex.Session, 64)
	for i := range sessions {
		sessions[i] = openTurnSession(t, f.c, context.Background())
		nextTurnEvent(t, sessions[i])
	}
	var wg sync.WaitGroup
	failures := make(chan error, 128)
	for _, s := range sessions {
		wg.Go(func() {
			wg.Go(func() { failures <- s.Close() })
			for {
				_, err := s.Receive(context.Background())
				if err != nil {
					break
				}
			}
			<-s.Done()
			_ = s.Err()
			failures <- s.Abort()
		})
	}
	for range 4 {
		var frames []string
		for _, s := range sessions {
			frames = append(frames, treeFrame("future/race", fmt.Sprintf(`{"threadId":%q}`, s.ThreadID())))
		}
		emitRequests(t, f.c, frames...)
	}
	require.NoError(t, f.c.Close())
	wg.Wait()
	close(failures)
	for err := range failures {
		assert.NoError(t, err)
	}
}

func (obs *observationSuite) TestSessionAdmissionValidation() {
	t := obs.T()
	for _, limits := range []codex.Limits{{Subscriptions: -1}, {EventQueue: -1}} {
		_, err := codex.NewStdio(context.Background(), codex.StdioOptions{Executable: "not-executed", Limits: limits})
		require.Error(t, err)
	}
	f := obs.fixture(t, "normal", codex.Limits{Subscriptions: 1})
	_, err := codex.Open(nil, codex.SessionOptions{Client: f.c})
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = codex.Open(ctx, codex.SessionOptions{Client: f.c})
	require.ErrorIs(t, err, context.Canceled)
	for _, options := range []codex.SessionOptions{{Client: f.c, MaxMembers: -1}, {Client: f.c, MaxParentDepth: -1}} {
		_, err = codex.Open(context.Background(), options)
		require.Error(t, err)
	}
	s := openTurnSession(t, f.c, context.Background())
	require.NoError(t, s.Close())
	openTurnSession(t, f.c, context.Background())
}
