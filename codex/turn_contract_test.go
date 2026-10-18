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
			emit("future/ordered", fmt.Sprintf(`{"sequence":%d}`, i))
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

func cleanupSubscription(t *testing.T, s *codex.Subscription) {
	t.Helper()
	if s != nil {
		t.Cleanup(func() { _ = s.Abort() })
	}
}

func subscribe(t *testing.T, c *codex.Client, ctx context.Context) *codex.Subscription {
	t.Helper()
	s, err := c.Subscribe(ctx, "native-thread-42", types.SubscriptionOptions{PendingAge: 10 * time.Millisecond})
	cleanupSubscription(t, s)
	require.NoError(t, err, err)
	{
		e := nextTree(t, s)
		require.Equal(t, stateRecord, e.Kind, "missing initial observation state")
	}
	return s
}
func nextEvent(t *testing.T, s *codex.Subscription) types.Event {
	t.Helper()
	for {
		select {
		case e, ok := <-s.Events():
			require.True(t, ok, "events closed: %v", s.Err())
			if e.Raw != nil {
				return e
			}
		case <-time.After(3 * time.Second):
			require.FailNow(t, "test invariant", "event not delivered")
		}
	}
}
func awaitSubscription(t *testing.T, s *codex.Subscription, cause error) {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(3 * time.Second):
		require.FailNow(t, "test invariant", "subscription did not terminate")
	}
	require.ErrorIs(t, s.Err(), cause, "terminal error %v, want %v", s.Err(), cause)
}
func startParams() types.TurnStartParams {
	return types.TurnStartParams{ThreadID: "native-thread-42", Input: []types.UserInput{{Text: types.Ptr(types.TextUserInput{Text: "synthetic input"})}}, ServiceTier: types.Null[string](), DisabledPluginIDs: types.Some([]string{}), OutputSchema: types.Some(json.RawMessage(`{"type":"string"}`))}
}

func (obs *observationSuite) TestTurnEarlyEventsKeepResponseIdentity() {
	t := obs.T()
	f := obs.fixture(t, "turn-early", codex.Limits{EventQueue: 16})
	s := subscribe(t, f.c, context.Background())
	type outcome struct {
		r   types.TurnStartResponse
		err error
	}
	result := make(chan outcome, 1)
	params := startParams()
	params.ParentTurnID = types.Some("causing-turn")
	params.RootTurnID = types.Some("chain-root-turn")
	go func() { r, err := f.c.StartTurn(context.Background(), params); result <- outcome{r, err} }()
	var events []types.Event
	other := false
	for i := 0; i < 8; i++ {
		e := nextEvent(t, s)
		require.False(t, e.DecodeError != nil || e.Data == nil, "event %d: %#v", i, e)
		if v, ok := e.Data.(*types.TurnStartedNotification); ok && v.ThreadID == "other-thread" {
			other = true
			continue
		}
		events = append(events, e)
	}
	require.False(t, events[0].Data.(*types.TurnCompletedNotification).Turn.ID != "stale-turn" || !other || events[2].Data.(*types.AgentMessageDeltaNotification).TurnID != "other-turn", "interleaved native identities changed")
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
	s := subscribe(t, f.c, context.Background())
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
	for i := 0; i < len(expected)+2; i++ {
		e := nextEvent(t, s)
		if e.Method == "future/absent" {
			seen["absent"] = e
		} else if e.Method == "item/started" {
			seen["item-absent"] = e
		} else if e.Method == "turn/started" {
			seen["turn-null"] = e
		} else {
			seen[string(e.Params)] = e
		}
	}
	for i, raw := range expected {
		key := raw
		if i == 2 {
			key = "absent"
		}
		if i == 5 {
			key = "turn-null"
		}
		if i == 6 {
			key = "item-absent"
		}
		e, ok := seen[key]
		require.False(t, !ok || string(e.Params) != raw || e.Data != nil, "raw event %d: %#v", i, e)
		var de *codex.DecodeError
		if i < 3 {
			require.NoError(t, e.DecodeError, "unknown method treated as malformed")
		} else {
			require.False(t, !errors.As(e.DecodeError, &de) || de.Method != e.Method || errors.Unwrap(de) == nil, "missing decode cause: %#v", e)
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

func (obs *observationSuite) TestSubscriptionOverflowIsolatedAndInspectable() {
	t := obs.T()
	f := obs.fixture(t, "normal", codex.Limits{EventQueue: 1, Subscriptions: 2})
	slow := subscribe(t, f.c, context.Background())
	healthy := subscribe(t, f.c, context.Background())
	{
		_, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
		require.ErrorIs(t, err, codex.ErrLimit, "subscriber limit: %v", err)
	}
	for i := 0; i < 4; i++ {
		{
			err := f.c.Call(context.Background(), "turn-emit", nil, nil)
			require.NoError(t, err, err)
		}
		require.Equal(t, "item/agentMessage/delta", nextEvent(t, healthy).Method, "healthy stream reordered")
	}
	// Both readers retain every accepted event regardless of the deprecated capacity.
	require.NoError(t, slow.Err())
	require.NoError(t, slow.Close())
	awaitSubscription(t, slow, nil)
	for i := 0; i < 4; i++ {
		require.Equal(t, "item/agentMessage/delta", nextEvent(t, slow).Method)
	}
	_, ok := <-slow.Events()
	require.False(t, ok)
	require.NoError(t, healthy.Close())
	require.NoError(t, healthy.Err(), healthy.Err())
	replacement := subscribe(t, f.c, context.Background())
	{
		err := replacement.Close()
		require.NoError(t, err, err)
	}
	awaitSubscription(t, replacement, nil)
}

func (obs *observationSuite) TestSlowSubscriberNeverBlocksResponse() {
	t := obs.T()
	f := obs.fixture(t, "normal", codex.Limits{EventQueue: 1})
	s := subscribe(t, f.c, context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var answer string
	{
		err := f.c.Call(ctx, "turn-flood", nil, &answer)
		require.False(t, err != nil || answer != "reader-still-routing", "reader blocked: %s %v", answer, err)
	}
	require.NoError(t, s.Err())
	require.NoError(t, s.Close())
	awaitSubscription(t, s, nil)
	for i := 0; i < 4096; i++ {
		e := nextEvent(t, s)
		var params struct {
			Sequence int `json:"sequence"`
		}
		require.NoError(t, json.Unmarshal(e.Params, &params))
		require.Equal(t, i, params.Sequence)
	}
	_, ok := <-s.Events()
	require.False(t, ok)
	require.Equal(t, uint64(4096), f.c.Diagnostics().Notifications, "reader failed to consume complete flood")
}

func (obs *observationSuite) TestSubscriptionOwnsPayloadAndNoReplay() {
	t := obs.T()
	f := obs.fixture(t, "normal", codex.Limits{})
	{
		err := f.c.Call(context.Background(), "turn-emit", nil, nil)
		require.NoError(t, err, err)
	}
	first := subscribe(t, f.c, context.Background())
	second := subscribe(t, f.c, context.Background())
	select {
	case <-first.Events():
		require.FailNow(t, "test invariant", "replayed old notification")
	default:
	}
	{
		err := f.c.Call(context.Background(), "turn-emit", nil, nil)
		require.NoError(t, err, err)
	}
	a, b := nextEvent(t, first), nextEvent(t, second)
	a.Params[0] = '!'
	a.Data.(*types.AgentMessageDeltaNotification).UnknownFields["future"][0] = '!'
	a.Data.(*types.AgentMessageDeltaNotification).ThreadID = "mutated"
	require.False(t, string(b.Params) != turnDelta || b.Data.(*types.AgentMessageDeltaNotification).ThreadID != "native-thread-42" || string(b.Data.(*types.AgentMessageDeltaNotification).UnknownFields["future"]) != `{"x":9007199254740993}`, "subscriber buffers alias")
	{
		err := f.c.Call(context.Background(), "turn-item", nil, nil)
		require.NoError(t, err, err)
	}
	a, b = nextEvent(t, first), nextEvent(t, second)
	a.Data.(*types.ItemCompletedNotification).Item.Unknown[0] = '!'
	require.Equal(t, unknownItem, string(b.Data.(*types.ItemCompletedNotification).Item.Unknown), "unknown union aliases")

}

func (obs *observationSuite) TestCancellationDoesNotInterruptTurn() {
	t := obs.T()
	f := obs.fixture(t, "turn-wait", codex.Limits{})
	subctx, subcancel := context.WithCancel(context.Background())
	s := subscribe(t, f.c, subctx)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := f.c.StartTurn(ctx, startParams()); result <- err }()
	waitTrace(t, f.trace, "turn/start")
	// Establish a completed write, rather than only peer receipt, before
	// testing cancellation of local waiting on a still-usable connection.
	responseBarrier(t, f)
	cancel()
	subcancel()
	callError(t, <-result, codex.OutcomeUnknown, context.Canceled)
	awaitSubscription(t, s, context.Canceled)
	// This response is late for the canceled start. A barrier establishes that
	// all writes/notifications before the explicit interrupt have been handled.
	{
		err := f.c.Call(context.Background(), "release-start", nil, nil)
		require.NoError(t, err, err)
	}
	for _, m := range trace(t, f.trace) {
		require.False(t, m.Method == "turn/interrupt" || strings.Contains(m.Method, "cancel"), "implicit cancellation sent: %s", m.Method)
	}
	live := subscribe(t, f.c, context.Background())
	ack, err := f.c.InterruptTurn(context.Background(), types.TurnInterruptParams{ThreadID: "native-thread-42", TurnID: "native-turn-response"})
	require.False(t, err != nil || string(ack.UnknownFields["futureAck"]) != "true", "interrupt ack: %+v %v", ack, err)
	select {
	case e := <-live.Events():
		require.FailNowf(t, "test invariant", "ack fabricated execution event: %#v", e)
	default:
	}
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
			s := subscribe(t, f.c, context.Background())
			_, err := f.c.StartTurn(context.Background(), startParams())
			state := codex.Responded
			if mode == "turn-exit" {
				state = codex.OutcomeUnknown
				awaitSubscription(t, s, io.EOF)
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

func (obs *observationSuite) TestSubscriptionTerminalStatus() {
	t := obs.T()
	for _, method := range []string{"turn-malformed-envelope", "turn-oversized-envelope"} {
		t.Run(method, func(t *testing.T) {
			f := obs.fixture(t, "normal", codex.Limits{MaxFrameBytes: 4096})
			s := subscribe(t, f.c, context.Background())
			callError(t, f.c.Call(context.Background(), method, nil, nil), codex.OutcomeUnknown, codex.ErrProtocol)
			awaitSubscription(t, s, codex.ErrProtocol)
			require.False(t, method == "turn-oversized-envelope" && !errors.Is(s.Err(), codex.ErrFrameTooLarge), s.Err())
			{
				e := nextTree(t, s)
				require.False(t, e.Kind != stateRecord || e.Reason != "permanent-gap", "missing gap: %+v", e)
			}
			{
				_, ok := <-s.Events()
				require.False(t, ok, "malformed envelope became event")
			}
			_ = s.Close()
			require.ErrorIs(t, s.Err(), codex.ErrProtocol, "close replaced terminal cause")
		})
	}
	t.Run("client-close", func(t *testing.T) {
		f := obs.fixture(t, "normal", codex.Limits{EventQueue: 1})
		s := subscribe(t, f.c, context.Background())
		{
			err := f.c.Call(context.Background(), "turn-emit", nil, nil)
			require.NoError(t, err, err)
		}
		{
			err := f.c.Close()
			require.NoError(t, err, err)
		}
		awaitSubscription(t, s, nil)
		nextEvent(t, s)
		{
			_, ok := <-s.Events()
			require.False(t, ok, "events still open")
		}
		{
			_, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			require.ErrorIs(t, err, codex.ErrClosed, err)
		}
	})
	t.Run("cancel-full", func(t *testing.T) {
		f := obs.fixture(t, "normal", codex.Limits{EventQueue: 1})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s := subscribe(t, f.c, ctx)
		{
			err := f.c.Call(context.Background(), "turn-emit", nil, nil)
			require.NoError(t, err, err)
		}
		cancel()
		awaitSubscription(t, s, context.Canceled)
		nextEvent(t, s)
		{
			_, ok := <-s.Events()
			require.False(t, ok, "canceled events still open")
		}
	})
	t.Run("deadline", func(t *testing.T) {
		f := obs.fixture(t, "normal", codex.Limits{})
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		s := subscribe(t, f.c, ctx)
		awaitSubscription(t, s, context.DeadlineExceeded)
		{
			_, ok := <-s.Events()
			require.False(t, ok, "canceled events open")
		}
	})
}

func TestSubscriptionConcurrentLifecycle(t *testing.T) {
	f := startFake(t, "normal", codex.Limits{EventQueue: 2, Subscriptions: 128})
	var wg sync.WaitGroup
	failures := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Go(func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s, err := f.c.Subscribe(ctx, "native-thread-42", types.SubscriptionOptions{})
			cleanupSubscription(t, s)
			if err != nil {
				if !errors.Is(err, codex.ErrClosed) {
					failures <- err
				}
				return
			}
			wg.Go(func() { cancel(); _ = s.Close() })
			for range s.Events() {
			}
			<-s.Done()
			_ = s.Err()
			_ = s.Close()
		})
	}
	for i := 0; i < 4; i++ {
		{
			err := f.c.Call(context.Background(), "turn-emit", nil, nil)
			require.NoError(t, err, err)
		}
	}
	{
		err := f.c.Close()
		require.NoError(t, err, err)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		assert.NoError(t, err)
	}
}

func (obs *observationSuite) TestSubscriptionAdmissionValidation() {
	t := obs.T()
	for _, limits := range []codex.Limits{{Subscriptions: -1}, {EventQueue: -1}} {
		{
			_, err := codex.NewStdio(context.Background(), codex.StdioOptions{Executable: "not-executed", Limits: limits})
			require.False(t, err == nil, "negative subscription limit accepted")
		}
	}
	f := obs.fixture(t, "normal", codex.Limits{Subscriptions: 1})
	{
		_, err := f.c.Subscribe(nil, "native-thread-42", types.SubscriptionOptions{})
		require.False(t, err == nil, "nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	{
		_, err := f.c.Subscribe(ctx, "native-thread-42", types.SubscriptionOptions{})
		require.ErrorIs(t, err, context.Canceled, err)
	}
	s := subscribe(t, f.c, context.Background())
	_ = s.Close()
	subscribe(t, f.c, context.Background())
}
