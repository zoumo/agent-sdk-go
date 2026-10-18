package codex_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	codex "github.com/zoumo/codex-sdk-go"
	"github.com/zoumo/codex-sdk-go/protocol"
)

// The independent fake peer accepts literal wire frames as strings so it can
// exercise exact parameter bytes, missing members, and malformed payloads.
// It records responses without issuing an acknowledgment: JSON-RPC responses
// have no response of their own.
type requestPeer struct{ transcript *os.File }

func (p *requestPeer) handle(mode string, m fakeEnvelope, send func(json.RawMessage, string)) bool {
	if !strings.HasPrefix(mode, "request-") {
		return false
	}
	switch m.Method {
	case "initialize":
		if mode != "request-during-initialize" && mode != "request-overflow-initialize" {
			return false
		}
		fmt.Fprintln(os.Stdout, `{"id":1,"method":"future/earlyApproval","params":{"beforeInitialized":true}}`)
		if mode == "request-overflow-initialize" {
			fmt.Fprintln(os.Stdout, `{"id":"second","method":"future/earlyApproval","params":{}}`)
		}
		send(m.ID, initResult)
		return true
	case "":
		return true
	case "request-frames":
		var frames []string
		if json.Unmarshal(m.Params, &frames) != nil {
			os.Exit(88)
		}
		for _, frame := range frames {
			fmt.Fprintln(os.Stdout, frame)
		}
		send(m.ID, `{}`)
		return true
	case "request-gate":
		send(m.ID, `{}`)
		one := make([]byte, 1)
		if _, err := os.Stdin.Read(one); err != nil {
			os.Exit(91)
		}
		path := os.Getenv("CODEX_SDK_TRACE")
		_ = os.WriteFile(path+".dispatch", []byte("response byte received"), 0600)
		for {
			if _, err := os.Stat(path + ".release"); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		// Read only to this frame's newline. A new buffered reader could steal
		// bytes belonging to the original scanner after the gate reopens.
		raw := append([]byte(nil), one...)
		for raw[len(raw)-1] != '\n' {
			if _, err := os.Stdin.Read(one); err != nil {
				os.Exit(92)
			}
			raw = append(raw, one[0])
		}
		_, _ = p.transcript.Write(raw)
		_ = p.transcript.Sync()
		var released fakeEnvelope
		if json.Unmarshal(raw, &released) != nil || released.Method != "echo" {
			os.Exit(94)
		}
		send(released.ID, string(released.Params))
		return true
	case "request-block":
		send(m.ID, `{}`)
		one := make([]byte, 1)
		if _, err := os.Stdin.Read(one); err != nil {
			os.Exit(89)
		}
		_ = os.WriteFile(os.Getenv("CODEX_SDK_TRACE")+".dispatch", []byte("response byte received"), 0600)
		time.Sleep(time.Hour)
		os.Exit(90)
	}
	return false
}

const approvalParams = `{"threadId":"native-thread-42","turnId":"native-turn-7","itemId":"native-item-9","startedAtMs":9007199254740993,"approvalId":null,"command":"","future":{"large":9007199254740993}}`

func requestFrame(id, method, params string) string {
	frame := fmt.Sprintf(`{"id":%s,"method":%q`, id, method)
	if params != "" {
		frame += `,"params":` + params
	}
	return frame + `}`
}
func emitRequests(t *testing.T, c *codex.Client, frames ...string) {
	t.Helper()
	if err := c.Call(context.Background(), "request-frames", frames, nil); err != nil {
		t.Fatal(err)
	}
}
func nextRequest(t *testing.T, c *codex.Client) codex.Request {
	t.Helper()
	select {
	case request, ok := <-c.Requests():
		if !ok {
			t.Fatalf("requests closed: %v", c.Err())
		}
		return request
	case <-time.After(3 * time.Second):
		t.Fatal("server request not delivered")
	}
	return codex.Request{}
}
func awaitAnswer(t *testing.T, h codex.AnswerHandle, cause error) {
	t.Helper()
	select {
	case <-h.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("answer handle did not terminate")
	}
	if !errors.Is(h.Err(), cause) {
		t.Fatalf("handle error = %v, want %v", h.Err(), cause)
	}
}
func answerError(t *testing.T, err error, r codex.Request, dispatch codex.DispatchState, cause error) {
	t.Helper()
	var ae *codex.AnswerError
	if !errors.As(err, &ae) || ae.Dispatch != dispatch || ae.Method != r.Method() || ae.ID != r.ID() {
		t.Fatalf("answer error = %#v, want %s for %s %s", err, dispatch, r.Method(), r.ID())
	}
	if cause != nil && !errors.Is(err, cause) {
		t.Fatalf("answer cause = %v, want %v", err, cause)
	}
}
func answerFrames(t *testing.T, path string) []map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	scan := bufio.NewScanner(bytes.NewReader(b))
	scan.Buffer(make([]byte, 4096), 32<<20)
	var out []map[string]json.RawMessage
	for scan.Scan() {
		var frame map[string]json.RawMessage
		if err := json.Unmarshal(scan.Bytes(), &frame); err != nil {
			t.Fatal(err)
		}
		if _, method := frame["method"]; !method {
			out = append(out, frame)
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
func responseBarrier(t *testing.T, f fixture) {
	t.Helper()
	var echo string
	if err := f.c.Call(context.Background(), "echo", "response-barrier", &echo); err != nil || echo != "response-barrier" {
		t.Fatalf("barrier: %q %v", echo, err)
	}
}
func waitResponsePrefix(t *testing.T, f fixture) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(f.trace + ".dispatch"); err == nil {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("peer did not receive response byte")
		case <-tick.C:
		}
	}
}

func TestServerRequestIDsAndNativeReplyEnvelopes(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{})
	if f.c.Requests() != f.c.Requests() {
		t.Fatal("Requests must return one shared pull queue")
	}
	// ID 2 deliberately equals the client request-frames call's ID. The string
	// and integer variants must remain different in both protocol directions.
	emitRequests(t, f.c,
		requestFrame(`2`, "future/approval", `{"native":9007199254740993}`),
		requestFrame(`"2"`, "future/approval", `null`),
		requestFrame(`9007199254740993`, "future/input", ``))
	requests := []codex.Request{nextRequest(t, f.c), nextRequest(t, f.c), nextRequest(t, f.c)}
	wantIDs := []codex.ID{codex.IntegerID(2), codex.StringID("2"), codex.IntegerID(9007199254740993)}
	for i, r := range requests {
		if r.ID() != wantIDs[i] || r.Handle().Err() != nil {
			t.Fatalf("request %d identity or state changed: %v %v", i, r.ID(), r.Handle().Err())
		}
		if data, err := r.Data(); data != nil || err != nil {
			t.Fatalf("unknown method acquired a guessed payload: %#v %v", data, err)
		}
	}
	if err := requests[0].Handle().Answer(context.Background(), json.RawMessage(`{"decision":"caller-selected","large":9007199254740993,"nullable":null}`)); err != nil {
		t.Fatal(err)
	}
	rpc := &codex.RPCError{Code: -32042, Message: "caller refusal", Data: json.RawMessage(`{"large":9007199254740993,"nullable":null}`)}
	if err := requests[1].Handle().Reject(context.Background(), rpc); err != nil {
		t.Fatal(err)
	}
	if err := requests[2].Handle().Answer(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	for _, r := range requests {
		awaitAnswer(t, r.Handle(), codex.ErrRequestAnswered)
		answerError(t, r.Handle().Answer(context.Background(), "again"), r, codex.NotSent, codex.ErrRequestAnswered)
		answerError(t, r.Handle().Reject(context.Background(), rpc), r, codex.NotSent, codex.ErrRequestAnswered)
	}
	responseBarrier(t, f)
	frames := answerFrames(t, f.trace)
	if len(frames) != 3 {
		t.Fatalf("sent %d responses, want exactly 3", len(frames))
	}
	for i, frame := range frames {
		if len(frame) != 2 || string(frame["id"]) != wantIDs[i].String() {
			t.Fatalf("response %d not a native response envelope: %v", i, frame)
		}
	}
	if string(frames[0]["result"]) != `{"decision":"caller-selected","large":9007199254740993,"nullable":null}` || string(frames[2]["result"]) != "null" {
		t.Fatal("native result changed")
	}
	var rejected codex.RPCError
	if err := json.Unmarshal(frames[1]["error"], &rejected); err != nil || rejected.Code != rpc.Code || rejected.Message != rpc.Message || string(rejected.Data) != string(rpc.Data) {
		t.Fatalf("native error changed: %+v %v", rejected, err)
	}
}

func TestServerRequestRawAndTypedCopies(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{})
	const raw = `{ "n" : 9007199254740993, "nested" : [null, true] }`
	emitRequests(t, f.c,
		requestFrame(`"known"`, "item/commandExecution/requestApproval", approvalParams),
		requestFrame(`"bad"`, "item/commandExecution/requestApproval", `{"threadId":false}`),
		requestFrame(`"unknown"`, "future/raw", raw),
		requestFrame(`"null"`, "future/raw", `null`),
		requestFrame(`"absent"`, "future/raw", ``))
	r := nextRequest(t, f.c)
	if r.Method() != "item/commandExecution/requestApproval" || string(r.Params()) != approvalParams {
		t.Fatal("native method or payload changed")
	}
	data, err := r.Data()
	p, ok := data.(*protocol.CommandExecutionRequestApprovalParams)
	if err != nil || !ok || p.ThreadID != "native-thread-42" || p.ItemID != "native-item-9" || p.StartedAtMs != 9007199254740993 || !p.ApprovalID.IsNull() || p.Command.ValueOr("missing") != "" || string(p.UnknownFields["future"]) != `{"large":9007199254740993}` {
		t.Fatalf("typed request lost fields: %#v %v", data, err)
	}
	p.ThreadID = "mutated"
	p.UnknownFields["future"][0] = '['
	bytes := r.Params()
	bytes[0] = '['
	fresh, err := r.Data()
	if err != nil || fresh.(*protocol.CommandExecutionRequestApprovalParams).ThreadID != "native-thread-42" || string(fresh.(*protocol.CommandExecutionRequestApprovalParams).UnknownFields["future"]) != `{"large":9007199254740993}` || string(r.Params()) != approvalParams {
		t.Fatal("caller mutation reached immutable request")
	}
	bad := nextRequest(t, f.c)
	if data, err := bad.Data(); data != nil || err == nil {
		t.Fatalf("malformed known payload: %#v %v", data, err)
	} else {
		var de *codex.DecodeError
		if !errors.As(err, &de) || de.Method != bad.Method() || errors.Unwrap(de) == nil {
			t.Fatalf("decode error lost method or cause: %v", err)
		}
	}
	for _, want := range []string{raw, "null", ""} {
		r := nextRequest(t, f.c)
		if string(r.Params()) != want {
			t.Fatalf("raw payload = %s, want %s", r.Params(), want)
		}
		if data, err := r.Data(); data != nil || err != nil {
			t.Fatalf("unknown payload must remain raw: %#v %v", data, err)
		}
	}
	if f.c.Err() != nil || bad.Handle().Err() != nil {
		t.Fatal("payload decode failure terminated connection or handle")
	}
	if err := bad.Handle().Reject(context.Background(), &codex.RPCError{Code: -32602, Message: "invalid parameters"}); err != nil {
		t.Fatal(err)
	}
}

func TestStalledServerRequestsDoNotBlockCallsOrEvents(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{RequestQueue: 8})
	s := subscribe(t, f.c, context.Background())
	emitRequests(t, f.c,
		requestFrame(`"pending-1"`, "future/approval", `{}`),
		requestFrame(`"pending-2"`, "future/input", `{}`),
		`{"method":"future/duringApproval","params":{"large":9007199254740993}}`)
	responseBarrier(t, f)
	e := nextEvent(t, s)
	if e.Method != "future/duringApproval" || string(e.Params) != `{"large":9007199254740993}` {
		t.Fatalf("event stopped behind approval UI: %#v", e)
	}
	if len(answerFrames(t, f.trace)) != 0 {
		t.Fatal("SDK automatically answered a server request")
	}
	for _, id := range []string{"pending-1", "pending-2"} {
		if r := nextRequest(t, f.c); r.ID() != codex.StringID(id) {
			t.Fatal("pull queue lost request order")
		}
	}
}

func TestAnswerPreflightFailuresLeaveHandleUsable(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{MaxFrameBytes: 4096})
	emitRequests(t, f.c, requestFrame(`"preflight"`, "future/approval", `{}`))
	r := nextRequest(t, f.c)
	h := r.Handle()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	answerError(t, h.Answer(ctx, "canceled"), r, codex.NotSent, context.Canceled)
	answerError(t, h.Answer(nil, nil), r, codex.NotSent, nil)
	answerError(t, h.Answer(context.Background(), make(chan int)), r, codex.NotSent, nil)
	answerError(t, h.Answer(context.Background(), json.RawMessage(`{"bad":`)), r, codex.NotSent, nil)
	answerError(t, h.Answer(context.Background(), strings.Repeat("x", 8192)), r, codex.NotSent, codex.ErrFrameTooLarge)
	answerError(t, h.Reject(context.Background(), nil), r, codex.NotSent, nil)
	answerError(t, h.Reject(context.Background(), &codex.RPCError{Code: -1, Message: "invalid", Data: json.RawMessage(`bad`)}), r, codex.NotSent, nil)
	if h.Err() != nil {
		t.Fatalf("preflight consumed handle: %v", h.Err())
	}
	select {
	case <-h.Done():
		t.Fatal("preflight closed handle")
	default:
	}
	responseBarrier(t, f)
	if len(answerFrames(t, f.trace)) != 0 {
		t.Fatal("preflight failure emitted response bytes")
	}
	if err := h.Answer(context.Background(), "valid retry"); err != nil {
		t.Fatal(err)
	}
	responseBarrier(t, f)
	if frames := answerFrames(t, f.trace); len(frames) != 1 || string(frames[0]["result"]) != `"valid retry"` {
		t.Fatal("valid answer after preflight did not arrive once")
	}
}

func TestServerRequestLocalCancellationAndExpiration(t *testing.T) {
	for _, terminal := range []string{"cancel", "expire"} {
		t.Run(terminal, func(t *testing.T) {
			f := startFake(t, "request-normal", codex.Limits{ActiveRequests: 1, RequestQueue: 1, RequestTimeout: 100 * time.Millisecond})
			emitRequests(t, f.c, requestFrame(`"old"`, "future/approval", `{}`))
			r := nextRequest(t, f.c)
			cause := codex.ErrRequestExpired
			if terminal == "cancel" {
				cause = codex.ErrRequestCanceled
				if err := r.Handle().Cancel(); err != nil {
					t.Fatal(err)
				}
			}
			awaitAnswer(t, r.Handle(), cause)
			answerError(t, r.Handle().Answer(context.Background(), "late"), r, codex.NotSent, cause)
			answerError(t, r.Handle().Reject(context.Background(), &codex.RPCError{Code: -1, Message: "late"}), r, codex.NotSent, cause)
			responseBarrier(t, f)
			if len(answerFrames(t, f.trace)) != 0 {
				t.Fatal("local abandonment invented a wire decision")
			}
			// Removing a terminal active handle must reclaim admission capacity.
			emitRequests(t, f.c, requestFrame(`"new"`, "future/approval", `{}`))
			fresh := nextRequest(t, f.c)
			if err := fresh.Handle().Answer(context.Background(), "caller decision"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResolvedServerRequestUsesExactIDAndKeepsEvent(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{})
	s := subscribe(t, f.c, context.Background())
	emitRequests(t, f.c,
		requestFrame(`7`, "item/commandExecution/requestApproval", approvalParams),
		requestFrame(`"7"`, "item/commandExecution/requestApproval", approvalParams))
	numeric, text := nextRequest(t, f.c), nextRequest(t, f.c)
	emitRequests(t, f.c, `{"method":"serverRequest/resolved","params":{"threadId":"native-thread-42","requestId":7}}`)
	awaitAnswer(t, numeric.Handle(), codex.ErrRequestResolved)
	if text.Handle().Err() != nil {
		t.Fatal("numeric resolution invalidated textual ID")
	}
	answerError(t, numeric.Handle().Answer(context.Background(), "late"), numeric, codex.NotSent, codex.ErrRequestResolved)
	e := nextEvent(t, s)
	if e.Method != "serverRequest/resolved" || string(e.Params) != `{"threadId":"native-thread-42","requestId":7}` || e.DecodeError != nil {
		t.Fatalf("native resolution event lost: %#v", e)
	}
	if err := text.Handle().Answer(context.Background(), "still pending"); err != nil {
		t.Fatal(err)
	}
	responseBarrier(t, f)
	if frames := answerFrames(t, f.trace); len(frames) != 1 || string(frames[0]["id"]) != `"7"` {
		t.Fatal("resolution fabricated or miscorrelated a response")
	}
}

func TestServerRequestCapacityFailureIsExplicit(t *testing.T) {
	for _, capacity := range []string{"queue", "active"} {
		t.Run(capacity, func(t *testing.T) {
			limits := codex.Limits{RequestQueue: 1, ActiveRequests: 8}
			if capacity == "active" {
				limits = codex.Limits{RequestQueue: 8, ActiveRequests: 1}
			}
			f := startFake(t, "request-normal", limits)
			emitRequests(t, f.c, requestFrame(`"first"`, "future/approval", `{}`))
			var first codex.Request
			if capacity == "active" {
				first = nextRequest(t, f.c)
			}
			err := f.c.Call(context.Background(), "request-frames", []string{requestFrame(`"overflow"`, "future/approval", `{}`)}, nil)
			callError(t, err, codex.OutcomeUnknown, codex.ErrRequestOverflow)
			if !errors.Is(err, codex.ErrLimit) || !errors.Is(f.c.Err(), codex.ErrRequestOverflow) {
				t.Fatalf("overflow omitted resource-limit evidence: %v", err)
			}
			if capacity == "active" {
				awaitAnswer(t, first.Handle(), codex.ErrRequestOverflow)
				answerError(t, first.Handle().Answer(context.Background(), "late"), first, codex.NotSent, codex.ErrRequestOverflow)
			}
			_ = f.c.Close()
			if len(answerFrames(t, f.trace)) != 0 {
				t.Fatal("overflow guessed an approval or rejection")
			}
		})
	}
}

func TestServerRequestCloseInvalidatesAndUnblocks(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{})
	emitRequests(t, f.c, requestFrame(`"old-connection"`, "future/approval", `{}`))
	old := nextRequest(t, f.c)
	consumer := make(chan bool, 1)
	go func() { _, ok := <-f.c.Requests(); consumer <- ok }()
	if err := f.c.Close(); err != nil {
		t.Fatal(err)
	}
	awaitAnswer(t, old.Handle(), codex.ErrClosed)
	select {
	case ok := <-consumer:
		if ok {
			t.Fatal("closed queue manufactured request")
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock request consumer")
	}
	fresh := startFake(t, "request-normal", codex.Limits{})
	emitRequests(t, fresh.c, requestFrame(`"old-connection"`, "future/approval", `{}`))
	current := nextRequest(t, fresh.c)
	answerError(t, old.Handle().Answer(context.Background(), "stale"), old, codex.NotSent, codex.ErrClosed)
	if err := current.Handle().Answer(context.Background(), "fresh"); err != nil {
		t.Fatal(err)
	}
	responseBarrier(t, fresh)
	if len(answerFrames(t, f.trace)) != 0 || len(answerFrames(t, fresh.trace)) != 1 {
		t.Fatal("old-client handle escaped its connection")
	}
}

func TestConcurrentServerRequestAnswerIsOneShot(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{})
	emitRequests(t, f.c, requestFrame(`"race"`, "future/approval", approvalParams))
	r := nextRequest(t, f.c)
	const callers = 32
	start := make(chan struct{})
	results := make(chan error, callers)
	var workers sync.WaitGroup
	for i := range callers {
		workers.Go(func() {
			<-start
			if i%2 == 0 {
				results <- r.Handle().Answer(context.Background(), map[string]int{"winner": i})
			} else {
				results <- r.Handle().Reject(context.Background(), &codex.RPCError{Code: int64(-32000 - i), Message: "caller rejection"})
			}
		})
	}
	close(start)
	workers.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else {
			answerError(t, err, r, codex.NotSent, codex.ErrRequestAnswered)
		}
	}
	if success != 1 {
		t.Fatalf("successful answers = %d, want 1", success)
	}
	awaitAnswer(t, r.Handle(), codex.ErrRequestAnswered)
	responseBarrier(t, f)
	if len(answerFrames(t, f.trace)) != 1 {
		t.Fatal("concurrent callers emitted duplicate response")
	}
}

func TestServerRequestDuplicateActiveIDTerminates(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{})
	emitRequests(t, f.c, requestFrame(`"duplicate"`, "future/approval", `{}`))
	r := nextRequest(t, f.c)
	err := f.c.Call(context.Background(), "request-frames", []string{requestFrame(`"duplicate"`, "future/other", `{}`)}, nil)
	callError(t, err, codex.OutcomeUnknown, codex.ErrProtocol)
	awaitAnswer(t, r.Handle(), codex.ErrProtocol)
	_ = f.c.Close()
	if len(answerFrames(t, f.trace)) != 0 {
		t.Fatal("duplicate correlation produced a guessed response")
	}
}

func TestPartialServerRequestAnswerIsUnknownAndNeverReplayed(t *testing.T) {
	for _, stop := range []string{"cancel", "timeout", "close"} {
		t.Run(stop, func(t *testing.T) {
			limits := codex.Limits{WriteTimeout: 5 * time.Second, ShutdownTimeout: 200 * time.Millisecond}
			if stop == "timeout" {
				limits.WriteTimeout = 100 * time.Millisecond
			}
			f := startFake(t, "request-prefix-blocked", limits)
			emitRequests(t, f.c, requestFrame(`"partial-answer"`, "future/approval", `{}`))
			r := nextRequest(t, f.c)
			if err := f.c.Call(context.Background(), "request-block", nil, nil); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- r.Handle().Answer(ctx, strings.Repeat("x", 2<<20)) }()
			waitResponsePrefix(t, f)
			// A local Cancel must lose after Answer has claimed the request. It
			// cannot erase the in-progress response or grant another answer.
			if err := r.Handle().Cancel(); !errors.Is(err, codex.ErrRequestAnswered) {
				t.Fatalf("Cancel stole a claimed response: %v", err)
			}
			cause := codex.ErrWriteTimeout
			if stop == "cancel" {
				cause = context.Canceled
				cancel()
			} else if stop == "close" {
				cause = codex.ErrClosed
				_ = f.c.Close()
			}
			select {
			case err := <-result:
				answerError(t, err, r, codex.OutcomeUnknown, cause)
			case <-time.After(2 * time.Second):
				t.Fatal("partial answer did not terminate")
			}
			select {
			case <-f.c.Done():
			case <-time.After(time.Second):
				t.Fatal("partial write left connection usable")
			}
			if err := r.Handle().Answer(context.Background(), "retry"); err == nil {
				t.Fatal("partially sent answer replayed")
			} else {
				answerError(t, err, r, codex.NotSent, nil)
			}
			_ = f.c.Close()
			assertReaped(t, f.pid)
			if len(answerFrames(t, f.trace)) != 0 {
				t.Fatal("peer unexpectedly received a complete or replayed answer")
			}
		})
	}
}

func TestServerRequestLimitDefaultsAndValidation(t *testing.T) {
	defaults := codex.DefaultLimits()
	if defaults.ActiveRequests != 128 || defaults.RequestQueue != 32 || defaults.RequestTimeout != 5*time.Minute {
		t.Fatalf("unexpected server request bounds: %+v", defaults)
	}
	for _, limits := range []codex.Limits{{ActiveRequests: -1}, {RequestQueue: -1}, {RequestTimeout: -time.Second}} {
		if c, err := codex.NewStdio(context.Background(), codex.StdioOptions{Executable: "unused", Limits: limits}); err == nil || c != nil {
			t.Fatalf("negative request limit accepted: %+v %v", limits, err)
		}
	}
}

func TestEverySupportedServerRequestTypedNativeRoundTrip(t *testing.T) {
	cases := []struct {
		method, params, result   string
		paramsType, responseType any
	}{
		{"item/commandExecution/requestApproval", approvalParams, `{"decision":"decline","future":null}`, new(protocol.CommandExecutionRequestApprovalParams), new(protocol.CommandExecutionRequestApprovalResponse)},
		{"item/fileChange/requestApproval", `{"threadId":"native-thread-42","turnId":"t","itemId":"f","startedAtMs":9007199254740993,"reason":null,"grantRoot":"","future":null}`, `{"decision":"cancel","future":null}`, new(protocol.FileChangeRequestApprovalParams), new(protocol.FileChangeRequestApprovalResponse)},
		{"item/permissions/requestApproval", `{"threadId":"native-thread-42","turnId":"t","itemId":"p","cwd":"/synthetic","startedAtMs":9007199254740993,"permissions":{},"reason":null,"environmentId":null,"future":null}`, `{"permissions":{},"scope":"turn","strictAutoReview":false,"future":null}`, new(protocol.PermissionsRequestApprovalParams), new(protocol.PermissionsRequestApprovalResponse)},
		{"item/tool/requestUserInput", `{"threadId":"native-thread-42","turnId":"t","itemId":"i","isBlocking":false,"questions":[{"id":"q","header":"H","question":"Choose","isOther":false,"isSecret":false,"options":[]}],"autoResolutionMs":null,"future":null}`, `{"answers":{"q":{"answers":["caller choice"],"future":null}},"future":null}`, new(protocol.ToolRequestUserInputParams), new(protocol.ToolRequestUserInputResponse)},
		{"applyPatchApproval", `{"callId":"patch","conversationId":"native-conversation","fileChanges":{"synthetic":{"type":"add","content":"caller text"}},"reason":null,"grantRoot":"","future":null}`, `{"decision":"denied","future":null}`, new(protocol.ApplyPatchApprovalParams), new(protocol.ApplyPatchApprovalResponse)},
		{"execCommandApproval", `{"callId":"exec","conversationId":"native-conversation","command":[],"cwd":"/synthetic","parsedCmd":[],"approvalId":null,"reason":"","future":null}`, `{"decision":"abort","future":null}`, new(protocol.ExecCommandApprovalParams), new(protocol.ExecCommandApprovalResponse)},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			f := startFake(t, "request-normal", codex.Limits{})
			emitRequests(t, f.c, requestFrame(`"native-request"`, tc.method, tc.params))
			r := nextRequest(t, f.c)
			data, err := r.Data()
			if err != nil || reflect.TypeOf(data) != reflect.TypeOf(tc.paramsType) {
				t.Fatalf("typed payload = %T, want %T: %v", data, tc.paramsType, err)
			}
			raw, err := json.Marshal(data)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decodeExactJSON(t, raw), decodeExactJSON(t, []byte(tc.params))) {
				t.Fatalf("native payload changed on typed round trip: %s", raw)
			}
			if err := json.Unmarshal([]byte(tc.result), tc.responseType); err != nil {
				t.Fatal(err)
			}
			if err := r.Handle().Answer(context.Background(), tc.responseType); err != nil {
				t.Fatal(err)
			}
			responseBarrier(t, f)
			frames := answerFrames(t, f.trace)
			if len(frames) != 1 || !reflect.DeepEqual(decodeExactJSON(t, frames[0]["result"]), decodeExactJSON(t, []byte(tc.result))) {
				t.Fatalf("typed native response changed: %v", frames)
			}
		})
	}
}

func decodeExactJSON(t *testing.T, raw []byte) any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestMalformedOrUnrelatedResolutionKeepsHandle(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{})
	s := subscribe(t, f.c, context.Background())
	emitRequests(t, f.c, requestFrame(`"native-id"`, "item/commandExecution/requestApproval", approvalParams))
	r := nextRequest(t, f.c)
	params := []string{
		`{"threadId":"different-thread","requestId":"native-id"}`,
		`{"threadId":"native-thread-42","requestId":"different-id"}`,
		`{"threadId":false,"requestId":"native-id"}`,
		`{"threadId":"native-thread-42","requestId":null}`,
		`{"threadId":"native-thread-42","requestId":1.5}`,
		`{"threadId":"native-thread-42","requestId":"native-id","requestId":"other"}`,
		`null`,
		`{}`,
	}
	for _, p := range params {
		emitRequests(t, f.c, `{"method":"serverRequest/resolved","params":`+p+`}`)
		if r.Handle().Err() != nil {
			t.Fatalf("unrelated resolution invalidated handle: %s", p)
		}
		if e := nextEvent(t, s); e.Method != "serverRequest/resolved" || string(e.Params) != p {
			t.Fatalf("malformed resolution lost raw observability: %#v", e)
		}
	}
	if err := r.Handle().Answer(context.Background(), "caller choice"); err != nil {
		t.Fatal(err)
	}
}

func TestServerRequestQueueAdmissionFailureIsReusable(t *testing.T) {
	f := startFake(t, "request-cooperative-gate", codex.Limits{OutboundQueue: 1, WriteTimeout: 5 * time.Second, ShutdownTimeout: 200 * time.Millisecond})
	var frames []string
	for i := range 4 {
		frames = append(frames, requestFrame(fmt.Sprint(100+i), "future/approval", `{}`))
	}
	emitRequests(t, f.c, frames...)
	var requests []codex.Request
	for range frames {
		requests = append(requests, nextRequest(t, f.c))
	}
	if err := f.c.Call(context.Background(), "request-gate", nil, nil); err != nil {
		t.Fatal(err)
	}
	writer := make(chan error, 1)
	go func() { writer <- f.c.Call(context.Background(), "echo", strings.Repeat("x", 128<<10), nil) }()
	waitResponsePrefix(t, f)
	type answerResult struct {
		r   codex.Request
		err error
	}
	results := make(chan answerResult, len(requests))
	for _, r := range requests {
		go func() { results <- answerResult{r, r.Handle().Answer(context.Background(), "initial choice")} }()
	}
	// One response may enter the sole queue slot. Every other caller must fail
	// admission promptly, without consuming its handle or blocking the reader.
	var rejected []codex.Request
	for range len(requests) - 1 {
		select {
		case result := <-results:
			answerError(t, result.err, result.r, codex.NotSent, codex.ErrLimit)
			if result.r.Handle().Err() != nil {
				t.Fatal("queue-full error consumed answer handle")
			}
			rejected = append(rejected, result.r)
		case <-time.After(2 * time.Second):
			t.Fatal("full queue blocked answer admission")
		}
	}
	if err := os.WriteFile(f.trace+".release", []byte("resume"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-writer:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cooperative peer failed to resume writer")
	}
	select {
	case result := <-results:
		if result.err != nil {
			t.Fatal(result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("admitted response failed to resume")
	}
	for _, r := range rejected {
		if err := r.Handle().Answer(context.Background(), "explicit retry after NotSent"); err != nil {
			t.Fatal(err)
		}
	}
	responseBarrier(t, f)
	if frames := answerFrames(t, f.trace); len(frames) != len(requests) {
		t.Fatalf("queue recovery sent %d replies, want %d", len(frames), len(requests))
	}
}

type heldAnswerMarshaler struct {
	entered chan<- struct{}
	release <-chan struct{}
}

func (h heldAnswerMarshaler) MarshalJSON() ([]byte, error) {
	close(h.entered)
	<-h.release
	return []byte(`{"late":"marshaled"}`), nil
}

func TestNoncooperativeAnswerMarshalerCannotBlockReaderOrClose(t *testing.T) {
	for _, stop := range []string{"cancel", "close"} {
		t.Run(stop, func(t *testing.T) {
			f := startFake(t, "request-normal", codex.Limits{ShutdownTimeout: 200 * time.Millisecond})
			emitRequests(t, f.c, requestFrame(`"held-marshal"`, "future/approval", `{}`))
			r := nextRequest(t, f.c)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			answer := make(chan error, 1)
			go func() { answer <- r.Handle().Answer(context.Background(), heldAnswerMarshaler{entered, release}) }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("caller marshaler not entered")
			}
			// Caller code is frozen, but neither the reader nor the client mutex
			// may be held while it runs.
			responseBarrier(t, f)
			cause := codex.ErrRequestCanceled
			if stop == "cancel" {
				if err := r.Handle().Cancel(); err != nil {
					t.Fatal(err)
				}
			} else {
				cause = codex.ErrClosed
				closed := make(chan error, 1)
				go func() { closed <- f.c.Close() }()
				select {
				case err := <-closed:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("Close waited on noncooperative caller code")
				}
			}
			awaitAnswer(t, r.Handle(), cause)
			once.Do(func() { close(release) })
			select {
			case err := <-answer:
				answerError(t, err, r, codex.NotSent, cause)
			case <-time.After(time.Second):
				t.Fatal("stale marshal return did not finish")
			}
			if stop == "cancel" {
				responseBarrier(t, f)
			}
			if len(answerFrames(t, f.trace)) != 0 {
				t.Fatal("stale marshal emitted response after terminal state")
			}
		})
	}
}

func TestZeroAnswerHandleFailsLocally(t *testing.T) {
	var r codex.Request
	if r.ID().Valid() || r.Method() != "" || r.Params() != nil {
		t.Fatal("zero request invented identity")
	}
	if data, err := r.Data(); data != nil || !errors.Is(err, codex.ErrInvalidAnswerHandle) {
		t.Fatalf("zero request Data = %#v %v", data, err)
	}
	h := r.Handle()
	awaitAnswer(t, h, codex.ErrInvalidAnswerHandle)
	answerError(t, h.Answer(context.Background(), nil), r, codex.NotSent, codex.ErrInvalidAnswerHandle)
	answerError(t, h.Reject(context.Background(), &codex.RPCError{Code: -1, Message: "invalid"}), r, codex.NotSent, codex.ErrInvalidAnswerHandle)
	if !errors.Is(h.Cancel(), codex.ErrInvalidAnswerHandle) {
		t.Fatal("zero handle cancellation succeeded")
	}
}

func TestQueuedAnswerCancellationIsNotSentButConsumesHandle(t *testing.T) {
	f := startFake(t, "request-cooperative-gate", codex.Limits{OutboundQueue: 1, WriteTimeout: 5 * time.Second, ShutdownTimeout: 200 * time.Millisecond})
	emitRequests(t, f.c, requestFrame(`"queued"`, "future/approval", `{}`))
	r := nextRequest(t, f.c)
	if err := f.c.Call(context.Background(), "request-gate", nil, nil); err != nil {
		t.Fatal(err)
	}
	writer := make(chan error, 1)
	go func() { writer <- f.c.Call(context.Background(), "echo", strings.Repeat("x", 128<<10), nil) }()
	waitResponsePrefix(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	answers := make(chan error, 2)
	for range 2 {
		go func() { answers <- r.Handle().Answer(ctx, "queued answer") }()
	}
	// Exactly one racer is admitted. The other returns ErrRequestAnswered,
	// which proves admission without reaching into implementation state.
	select {
	case err := <-answers:
		answerError(t, err, r, codex.NotSent, codex.ErrRequestAnswered)
	case <-time.After(time.Second):
		t.Fatal("one-shot answer admission did not resolve losing caller")
	}
	if err := r.Handle().Cancel(); !errors.Is(err, codex.ErrRequestAnswered) {
		t.Fatalf("Cancel stole admitted queued answer: %v", err)
	}
	cancel()
	select {
	case err := <-answers:
		answerError(t, err, r, codex.NotSent, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("queued answer context did not cancel promptly")
	}
	awaitAnswer(t, r.Handle(), context.Canceled)
	answerError(t, r.Handle().Answer(context.Background(), "retry"), r, codex.NotSent, context.Canceled)
	if err := os.WriteFile(f.trace+".release", []byte("resume"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-writer:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("unrelated writer failed to resume")
	}
	responseBarrier(t, f)
	if len(answerFrames(t, f.trace)) != 0 {
		t.Fatal("canceled queued answer was written later")
	}
	if f.c.Err() != nil {
		t.Fatal("cancellation before write unnecessarily terminated connection")
	}
}

func TestConcurrentAnswerAndCancelHaveOneWinner(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{})
	for iteration := range 8 {
		emitRequests(t, f.c, requestFrame(fmt.Sprint(iteration+1000), "future/approval", `{}`))
		r := nextRequest(t, f.c)
		start := make(chan struct{})
		type result struct {
			cancel bool
			err    error
		}
		results := make(chan result, 16)
		for i := range 16 {
			go func() {
				<-start
				if i%2 == 0 {
					results <- result{false, r.Handle().Answer(context.Background(), "caller choice")}
				} else {
					results <- result{true, r.Handle().Cancel()}
				}
			}()
		}
		close(start)
		winners, canceled := 0, false
		for range 16 {
			result := <-results
			if result.err == nil {
				winners++
				canceled = result.cancel
			} else if !errors.Is(result.err, codex.ErrRequestAnswered) && !errors.Is(result.err, codex.ErrRequestCanceled) {
				t.Fatalf("unexpected race loss: %v", result.err)
			}
		}
		if winners != 1 {
			t.Fatalf("Answer/Cancel race had %d successful operations", winners)
		}
		cause := codex.ErrRequestAnswered
		if canceled {
			cause = codex.ErrRequestCanceled
		}
		awaitAnswer(t, r.Handle(), cause)
		responseBarrier(t, f)
		n := 0
		for _, frame := range answerFrames(t, f.trace) {
			if string(frame["id"]) == r.ID().String() {
				n++
			}
		}
		want := 1
		if canceled {
			want = 0
		}
		if n != want {
			t.Fatalf("race emitted %d responses, want %d", n, want)
		}
	}
}

func TestMalformedServerRequestEnvelopeTerminates(t *testing.T) {
	frames := []string{
		`{"id":null,"method":"future/approval","params":{}}`,
		`{"id":1.5,"method":"future/approval","params":{}}`,
		`{"id":"x","id":"y","method":"future/approval","params":{}}`,
		`{"id":"x","method":"future/approval","params":{},"result":null}`,
		`{"id":"x","method":"future/approval","params":{},"error":{"code":-1,"message":"mixed"}}`,
		`{"id":"x","method":false,"params":{}}`,
		`{"id":"x","method":"","params":{}}`,
		`{"id":"x","method":"future/\ud800","params":{}}`,
	}
	for i, frame := range frames {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			f := startFake(t, "request-normal", codex.Limits{})
			err := f.c.Call(context.Background(), "request-frames", []string{frame}, nil)
			callError(t, err, codex.OutcomeUnknown, codex.ErrProtocol)
			_ = f.c.Close()
			if len(answerFrames(t, f.trace)) != 0 {
				t.Fatal("malformed envelope received a guessed answer")
			}
		})
	}
}

func TestConcurrentAnswerAndNativeResolutionHaveOneOutcome(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{})
	for iteration := range 16 {
		id := fmt.Sprint(iteration + 2000)
		emitRequests(t, f.c, requestFrame(id, "item/commandExecution/requestApproval", approvalParams))
		r := nextRequest(t, f.c)
		start := make(chan struct{})
		answer, resolved := make(chan error, 1), make(chan error, 1)
		go func() {
			<-start
			answer <- r.Handle().Answer(context.Background(), "caller choice")
		}()
		go func() {
			<-start
			resolved <- f.c.Call(context.Background(), "request-frames", []string{`{"method":"serverRequest/resolved","params":{"threadId":"native-thread-42","requestId":` + id + `}}`}, nil)
		}()
		close(start)
		answerErr := <-answer
		if err := <-resolved; err != nil {
			t.Fatal(err)
		}
		cause, want := codex.ErrRequestAnswered, 1
		if answerErr != nil {
			cause, want = codex.ErrRequestResolved, 0
			answerError(t, answerErr, r, codex.NotSent, codex.ErrRequestResolved)
		}
		awaitAnswer(t, r.Handle(), cause)
		responseBarrier(t, f)
		n := 0
		for _, frame := range answerFrames(t, f.trace) {
			if string(frame["id"]) == id {
				n++
			}
		}
		if n != want {
			t.Fatalf("Answer/resolution race emitted %d responses, want %d", n, want)
		}
	}
}

func TestReusedServerRequestIDDoesNotReviveOldHandle(t *testing.T) {
	// Expiry and its ID reuse are exercised with controlled time in
	// TestTerminalRequestIDReuseWithControlledTime. Real transport scheduling must
	// not race an unrelated short lifetime against these explicit outcomes.
	for _, firstOutcome := range []string{"cancel", "resolved", "answered"} {
		t.Run(firstOutcome, func(t *testing.T) {
			f := startFake(t, "request-normal", codex.Limits{ActiveRequests: 1})
			emitRequests(t, f.c, requestFrame(`"reused"`, "item/commandExecution/requestApproval", approvalParams))
			old := nextRequest(t, f.c)
			cause := codex.ErrRequestCanceled
			switch firstOutcome {
			case "cancel":
				if err := old.Handle().Cancel(); err != nil {
					t.Fatal(err)
				}
			case "resolved":
				cause = codex.ErrRequestResolved
				emitRequests(t, f.c, `{"method":"serverRequest/resolved","params":{"threadId":"native-thread-42","requestId":"reused"}}`)
			case "answered":
				cause = codex.ErrRequestAnswered
				if err := old.Handle().Answer(context.Background(), "first answer"); err != nil {
					t.Fatal(err)
				}
			}
			awaitAnswer(t, old.Handle(), cause)
			emitRequests(t, f.c, requestFrame(`"reused"`, "future/newRequest", `{"different":true}`))
			fresh := nextRequest(t, f.c)
			answerError(t, old.Handle().Answer(context.Background(), "stale"), old, codex.NotSent, cause)
			if !errors.Is(old.Handle().Cancel(), cause) {
				t.Fatal("old handle changed after ID reuse")
			}
			if fresh.Handle().Err() != nil || fresh.Method() != "future/newRequest" || string(fresh.Params()) != `{"different":true}` {
				t.Fatal("old handle affected new request with same native ID")
			}
			if err := fresh.Handle().Answer(context.Background(), "second answer"); err != nil {
				t.Fatal(err)
			}
			responseBarrier(t, f)
			frames := answerFrames(t, f.trace)
			want := 1
			if firstOutcome == "answered" {
				want = 2
			}
			if len(frames) != want || string(frames[len(frames)-1]["result"]) != `"second answer"` {
				t.Fatalf("stale answer affected reused ID: %v", frames)
			}
		})
	}
}

func TestExpiredBufferedRequestsRemainInspectableAfterClose(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{RequestQueue: 3, RequestTimeout: 30 * time.Millisecond})
	emitRequests(t, f.c,
		requestFrame(`"buffered-one"`, "future/approval", `{}`),
		requestFrame(`"buffered-two"`, "future/approval", `{}`))
	// Deliberately leave the pull queue unattended beyond its local lifetime.
	// Expiration must not emit a guessed decision or discard the request bytes.
	time.Sleep(150 * time.Millisecond)
	if err := f.c.Close(); err != nil {
		t.Fatal(err)
	}
	var count int
	for r := range f.c.Requests() {
		count++
		awaitAnswer(t, r.Handle(), codex.ErrRequestExpired)
		if string(r.Params()) != `{}` || r.Method() != "future/approval" {
			t.Fatal("terminal queued request lost immutable data")
		}
		answerError(t, r.Handle().Answer(context.Background(), "late"), r, codex.NotSent, codex.ErrRequestExpired)
	}
	if count != 2 || len(answerFrames(t, f.trace)) != 0 {
		t.Fatalf("Close drained %d queued requests or emitted policy answers", count)
	}
}

func TestLegacyRequestResolutionUsesConversationIdentity(t *testing.T) {
	for _, method := range []string{"applyPatchApproval", "execCommandApproval"} {
		t.Run(method, func(t *testing.T) {
			f := startFake(t, "request-normal", codex.Limits{})
			params := `{"callId":"native-call","conversationId":"native-legacy","fileChanges":{}}`
			if method == "execCommandApproval" {
				params = `{"callId":"native-call","conversationId":"native-legacy","command":[],"cwd":"/synthetic","parsedCmd":[]}`
			}
			emitRequests(t, f.c, requestFrame(`"legacy"`, method, params))
			r := nextRequest(t, f.c)
			emitRequests(t, f.c, `{"method":"serverRequest/resolved","params":{"threadId":"wrong-conversation","requestId":"legacy"}}`)
			if r.Handle().Err() != nil {
				t.Fatal("wrong legacy conversation resolved request")
			}
			emitRequests(t, f.c, `{"method":"serverRequest/resolved","params":{"threadId":"native-legacy","requestId":"legacy"}}`)
			awaitAnswer(t, r.Handle(), codex.ErrRequestResolved)
			answerError(t, r.Handle().Answer(context.Background(), "late"), r, codex.NotSent, codex.ErrRequestResolved)
			responseBarrier(t, f)
			if len(answerFrames(t, f.trace)) != 0 {
				t.Fatal("legacy resolution fabricated wire answer")
			}
		})
	}
}

func TestServerRequestsArrivingDuringInitializeAreRetained(t *testing.T) {
	f := startFake(t, "request-during-initialize", codex.Limits{})
	r := nextRequest(t, f.c)
	if r.ID() != codex.IntegerID(1) || r.Method() != "future/earlyApproval" || string(r.Params()) != `{"beforeInitialized":true}` {
		t.Fatal("early reverse request lost identity during initialization")
	}
	if err := r.Handle().Answer(context.Background(), "caller choice after handshake"); err != nil {
		t.Fatal(err)
	}
	responseBarrier(t, f)
	frames := trace(t, f.trace)
	if len(frames) != 4 || frames[0].Method != "initialize" || frames[1].Method != "initialized" || frames[2].Method != "" || string(frames[2].ID) != "1" {
		t.Fatalf("early answer escaped completed handshake: %+v", frames)
	}
}

func TestServerRequestOverflowDuringInitializeCleansUp(t *testing.T) {
	f, err := openFake(t, "request-overflow-initialize", codex.Limits{RequestQueue: 1, ShutdownTimeout: 200 * time.Millisecond})
	if err == nil || f.c != nil || !errors.Is(err, codex.ErrRequestOverflow) || !errors.Is(err, codex.ErrLimit) {
		t.Fatalf("startup overflow exposed usable client: %v %v", f.c, err)
	}
	assertReaped(t, f.pid)
	if len(answerFrames(t, f.trace)) != 0 {
		t.Fatal("startup overflow invented a policy answer")
	}
}
