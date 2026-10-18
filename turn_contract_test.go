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

	codex "github.com/zoumo/codex-sdk-go"
	"github.com/zoumo/codex-sdk-go/types"
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

func subscribe(t *testing.T, c *codex.Client, ctx context.Context) *codex.Subscription {
	t.Helper()
	s, err := c.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func nextEvent(t *testing.T, s *codex.Subscription) codex.Event {
	t.Helper()
	select {
	case e, ok := <-s.Events():
		if !ok {
			t.Fatalf("events closed: %v", s.Err())
		}
		return e
	case <-time.After(3 * time.Second):
		t.Fatal("event not delivered")
	}
	return codex.Event{}
}
func awaitSubscription(t *testing.T, s *codex.Subscription, cause error) {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("subscription did not terminate")
	}
	if !errors.Is(s.Err(), cause) {
		t.Fatalf("terminal error %v, want %v", s.Err(), cause)
	}
}
func startParams() types.TurnStartParams {
	return types.TurnStartParams{ThreadID: "native-thread-42", Input: []types.UserInput{{Text: types.Ptr(types.TextUserInput{Text: "synthetic input"})}}, ServiceTier: types.Null[string](), DisabledPluginIDs: types.Some([]string{}), OutputSchema: types.Some(json.RawMessage(`{"type":"string"}`))}
}

func TestTurnEarlyEventsKeepResponseIdentity(t *testing.T) {
	f := startFake(t, "turn-early", codex.Limits{EventQueue: 16})
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
	var events []codex.Event
	for i := 0; i < 8; i++ {
		e := nextEvent(t, s)
		if e.DecodeError != nil || e.Data == nil {
			t.Fatalf("event %d: %#v", i, e)
		}
		events = append(events, e)
	}
	if events[0].Data.(*types.TurnCompletedNotification).Turn.ID != "stale-turn" || events[1].Data.(*types.TurnStartedNotification).ThreadID != "other-thread" || events[3].Data.(*types.AgentMessageDeltaNotification).TurnID != "other-turn" {
		t.Fatal("interleaved native identities changed")
	}
	if events[5].Data.(*types.ItemStartedNotification).Item.AgentMessage.ID != "native-item-7" {
		t.Fatal("native item identity changed")
	}
	if string(events[6].Data.(*types.ItemCompletedNotification).Item.Unknown) != unknownItem {
		t.Fatal("unknown native variant lost")
	}
	if events[7].Data.(*types.TurnCompletedNotification).Turn.ID != "native-turn-response" {
		t.Fatal("early completion lost")
	}
	select {
	case <-result:
		t.Fatal("an event fabricated a start response")
	default:
	}
	if err := f.c.Call(context.Background(), "release-start", nil, nil); err != nil {
		t.Fatal(err)
	}
	got := <-result
	if got.err != nil || got.r.Turn.ID != "native-turn-response" || got.r.Turn.Status != types.TurnStatusInProgress {
		t.Fatalf("response replaced by event: %+v %v", got.r, got.err)
	}
	var starts int
	for _, m := range trace(t, f.trace) {
		if m.Method == "turn/start" {
			starts++
			var p map[string]json.RawMessage
			_ = json.Unmarshal(m.Params, &p)
			if string(p["threadId"]) != `"native-thread-42"` || string(p["serviceTier"]) != "null" || string(p["disabledPluginIds"]) != "[]" || string(p["outputSchema"]) != `{"type":"string"}` || string(p["parentTurnId"]) != `"causing-turn"` || string(p["rootTurnId"]) != `"chain-root-turn"` {
				t.Fatalf("native params changed: %s", m.Params)
			}
		}
	}
	if starts != 1 {
		t.Fatalf("sent turn/start %d times", starts)
	}
}

func TestNotificationRawAndDecodeErrors(t *testing.T) {
	f := startFake(t, "normal", codex.Limits{EventQueue: 16})
	s := subscribe(t, f.c, context.Background())
	if err := f.c.Call(context.Background(), "turn-mixed", nil, nil); err != nil {
		t.Fatal(err)
	}
	expected := []string{
		`{ "n" : 9007199254740993, "nested": [null, true] }`, `null`, ``,
		`{"threadId":false,"turnId":"x","itemId":"x","delta":"bad"}`, `{"threadId":"x"}`, `null`, ``,
		`{"threadId":"x","turnId":"x","completedAtMs":1,"item":{"type":"agentMessage","id":"missing-text"}}`,
		`{"threadId":"x","threadId":"y","turnId":"x","itemId":"x","delta":"duplicate"}`,
	}
	for i, raw := range expected {
		e := nextEvent(t, s)
		if string(e.Params) != raw || e.Data != nil {
			t.Fatalf("raw event %d: %#v", i, e)
		}
		var de *codex.DecodeError
		if i < 3 {
			if e.DecodeError != nil {
				t.Fatal("unknown method treated as malformed")
			}
		} else if !errors.As(e.DecodeError, &de) || de.Method != e.Method || errors.Unwrap(de) == nil {
			t.Fatalf("missing decode cause: %#v", e)
		}
	}
	e := nextEvent(t, s)
	native := e.Data.(*types.ErrorNotification)
	info, ok := native.Error.CodexErrorInfo.Get()
	if !ok || e.DecodeError != nil || native.WillRetry || string(info.Unknown) != `{"futureError":{"n":9007199254740993}}` {
		t.Fatalf("error data lost: %#v", e)
	}
	e = nextEvent(t, s)
	d := e.Data.(*types.AgentMessageDeltaNotification)
	if e.DecodeError != nil || d.ItemID != "native-item-7" || string(d.UnknownFields["future"]) != `{"x":9007199254740993}` || string(e.Params) != turnDelta {
		t.Fatalf("valid event lost: %#v", e)
	}
	if s.Err() != nil || f.c.Err() != nil {
		t.Fatal("bad payload terminated connection or stream")
	}
}

func TestSubscriptionOverflowIsolatedAndInspectable(t *testing.T) {
	f := startFake(t, "normal", codex.Limits{EventQueue: 1, Subscriptions: 2})
	slow := subscribe(t, f.c, context.Background())
	healthy := subscribe(t, f.c, context.Background())
	if _, err := f.c.Subscribe(context.Background()); !errors.Is(err, codex.ErrLimit) {
		t.Fatalf("subscriber limit: %v", err)
	}
	for i := 0; i < 4; i++ {
		if err := f.c.Call(context.Background(), "turn-emit", nil, nil); err != nil {
			t.Fatal(err)
		}
		if nextEvent(t, healthy).Method != "item/agentMessage/delta" {
			t.Fatal("healthy stream reordered")
		}
	}
	// The terminal state must be observable before reading the still-full queue.
	awaitSubscription(t, slow, codex.ErrSubscriptionOverflow)
	var overflow *codex.SubscriptionOverflowError
	if !errors.As(slow.Err(), &overflow) || overflow.Capacity != 1 {
		t.Fatalf("overflow not inspectable: %v", slow.Err())
	}
	if len(slow.Events()) != 1 {
		t.Fatal("accepted event discarded")
	}
	nextEvent(t, slow)
	if _, ok := <-slow.Events(); ok {
		t.Fatal("overflow stream continued after loss")
	}
	if healthy.Err() != nil {
		t.Fatal(healthy.Err())
	}
	replacement := subscribe(t, f.c, context.Background())
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	awaitSubscription(t, replacement, nil)
}

func TestSlowSubscriberNeverBlocksResponse(t *testing.T) {
	f := startFake(t, "normal", codex.Limits{EventQueue: 1})
	s := subscribe(t, f.c, context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var answer string
	if err := f.c.Call(ctx, "turn-flood", nil, &answer); err != nil || answer != "reader-still-routing" {
		t.Fatalf("reader blocked: %s %v", answer, err)
	}
	awaitSubscription(t, s, codex.ErrSubscriptionOverflow)
	if f.c.Diagnostics().Notifications != 4096 {
		t.Fatal("reader failed to consume complete flood")
	}
}

func TestSubscriptionOwnsPayloadAndNoReplay(t *testing.T) {
	f := startFake(t, "normal", codex.Limits{})
	if err := f.c.Call(context.Background(), "turn-emit", nil, nil); err != nil {
		t.Fatal(err)
	}
	first := subscribe(t, f.c, context.Background())
	second := subscribe(t, f.c, context.Background())
	select {
	case <-first.Events():
		t.Fatal("replayed old notification")
	default:
	}
	if err := f.c.Call(context.Background(), "turn-emit", nil, nil); err != nil {
		t.Fatal(err)
	}
	a, b := nextEvent(t, first), nextEvent(t, second)
	a.Params[0] = '!'
	a.Data.(*types.AgentMessageDeltaNotification).UnknownFields["future"][0] = '!'
	a.Data.(*types.AgentMessageDeltaNotification).ThreadID = "mutated"
	if string(b.Params) != turnDelta || b.Data.(*types.AgentMessageDeltaNotification).ThreadID != "native-thread-42" || string(b.Data.(*types.AgentMessageDeltaNotification).UnknownFields["future"]) != `{"x":9007199254740993}` {
		t.Fatal("subscriber buffers alias")
	}
	if err := f.c.Call(context.Background(), "turn-item", nil, nil); err != nil {
		t.Fatal(err)
	}
	a, b = nextEvent(t, first), nextEvent(t, second)
	a.Data.(*types.ItemCompletedNotification).Item.Unknown[0] = '!'
	if string(b.Data.(*types.ItemCompletedNotification).Item.Unknown) != unknownItem {
		t.Fatal("unknown union aliases")
	}

}

func TestCancellationDoesNotInterruptTurn(t *testing.T) {
	f := startFake(t, "turn-wait", codex.Limits{})
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
	if err := f.c.Call(context.Background(), "release-start", nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, m := range trace(t, f.trace) {
		if m.Method == "turn/interrupt" || strings.Contains(m.Method, "cancel") {
			t.Fatalf("implicit cancellation sent: %s", m.Method)
		}
	}
	live := subscribe(t, f.c, context.Background())
	ack, err := f.c.InterruptTurn(context.Background(), types.TurnInterruptParams{ThreadID: "native-thread-42", TurnID: "native-turn-response"})
	if err != nil || string(ack.UnknownFields["futureAck"]) != "true" {
		t.Fatalf("interrupt ack: %+v %v", ack, err)
	}
	select {
	case e := <-live.Events():
		t.Fatalf("ack fabricated execution event: %#v", e)
	default:
	}
	count := 0
	for _, m := range trace(t, f.trace) {
		if m.Method == "turn/interrupt" {
			count++
			if string(m.Params) != `{"threadId":"native-thread-42","turnId":"native-turn-response"}` {
				t.Fatalf("interrupt params: %s", m.Params)
			}
		}
	}
	if count != 1 {
		t.Fatalf("interrupt count: %d", count)
	}
}

func TestTurnFailureNeverReplays(t *testing.T) {
	for _, mode := range []string{"turn-error", "turn-exit", "turn-invalid-response"} {
		t.Run(mode, func(t *testing.T) {
			f := startFake(t, mode, codex.Limits{})
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
				if !errors.As(err, &rpc) || rpc.Code != -32603 {
					t.Fatal(err)
				}
			}
			count := 0
			for _, m := range trace(t, f.trace) {
				if m.Method == "turn/start" {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("turn/start replayed: %d", count)
			}
		})
	}
}

func TestSubscriptionTerminalStatus(t *testing.T) {
	for _, method := range []string{"turn-malformed-envelope", "turn-oversized-envelope"} {
		t.Run(method, func(t *testing.T) {
			f := startFake(t, "normal", codex.Limits{MaxFrameBytes: 4096})
			s := subscribe(t, f.c, context.Background())
			callError(t, f.c.Call(context.Background(), method, nil, nil), codex.OutcomeUnknown, codex.ErrProtocol)
			awaitSubscription(t, s, codex.ErrProtocol)
			if method == "turn-oversized-envelope" && !errors.Is(s.Err(), codex.ErrFrameTooLarge) {
				t.Fatal(s.Err())
			}
			if _, ok := <-s.Events(); ok {
				t.Fatal("malformed envelope became event")
			}
			_ = s.Close()
			if !errors.Is(s.Err(), codex.ErrProtocol) {
				t.Fatal("close replaced terminal cause")
			}
		})
	}
	t.Run("client-close", func(t *testing.T) {
		f := startFake(t, "normal", codex.Limits{EventQueue: 1})
		s := subscribe(t, f.c, context.Background())
		if err := f.c.Call(context.Background(), "turn-emit", nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := f.c.Close(); err != nil {
			t.Fatal(err)
		}
		awaitSubscription(t, s, nil)
		nextEvent(t, s)
		if _, ok := <-s.Events(); ok {
			t.Fatal("events still open")
		}
		if _, err := f.c.Subscribe(context.Background()); !errors.Is(err, codex.ErrClosed) {
			t.Fatal(err)
		}
	})
	t.Run("cancel-full", func(t *testing.T) {
		f := startFake(t, "normal", codex.Limits{EventQueue: 1})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s := subscribe(t, f.c, ctx)
		if err := f.c.Call(context.Background(), "turn-emit", nil, nil); err != nil {
			t.Fatal(err)
		}
		cancel()
		awaitSubscription(t, s, context.Canceled)
		if len(s.Events()) != 1 {
			t.Fatal("cancel discarded queued event")
		}
		nextEvent(t, s)
		if _, ok := <-s.Events(); ok {
			t.Fatal("canceled events still open")
		}
	})
	t.Run("deadline", func(t *testing.T) {
		f := startFake(t, "normal", codex.Limits{})
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		s := subscribe(t, f.c, ctx)
		awaitSubscription(t, s, context.DeadlineExceeded)
		if _, ok := <-s.Events(); ok {
			t.Fatal("canceled events open")
		}
	})
}

func TestSubscriptionConcurrentLifecycle(t *testing.T) {
	f := startFake(t, "normal", codex.Limits{EventQueue: 2, Subscriptions: 128})
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Go(func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s, err := f.c.Subscribe(ctx)
			if err != nil {
				if !errors.Is(err, codex.ErrClosed) {
					t.Error(err)
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
		if err := f.c.Call(context.Background(), "turn-emit", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.c.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
}

func TestSubscriptionAdmissionValidation(t *testing.T) {
	for _, limits := range []codex.Limits{{Subscriptions: -1}, {EventQueue: -1}} {
		if _, err := codex.NewStdio(context.Background(), codex.StdioOptions{Executable: "not-executed", Limits: limits}); err == nil {
			t.Fatal("negative subscription limit accepted")
		}
	}
	f := startFake(t, "normal", codex.Limits{Subscriptions: 1})
	if _, err := f.c.Subscribe(nil); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.c.Subscribe(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s := subscribe(t, f.c, context.Background())
	_ = s.Close()
	subscribe(t, f.c, context.Background())
}
