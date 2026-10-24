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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
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
		resolved := false
		for {
			if _, err := os.Stat(path + ".resolve"); err == nil && !resolved {
				fmt.Fprintln(os.Stdout, `{"method":"serverRequest/resolved","params":{"threadId":"native-thread-42","requestId":"queued"}}`)
				resolved = true
			}
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
	{
		err := c.Call(context.Background(), "request-frames", frames, nil)
		require.NoError(t, err)
	}
}
func nextRequest(t *testing.T, c *codex.Client) codex.Request {
	t.Helper()
	select {
	case request, ok := <-c.Requests():
		require.True(t, ok, "requests closed: %v", c.Err())
		return request
	case <-time.After(3 * time.Second):
		require.FailNow(t, "server request not delivered")
	}
	return codex.Request{}
}
func awaitAnswer(t *testing.T, h codex.AnswerHandle, cause error) {
	t.Helper()
	select {
	case <-h.Done():
	case <-time.After(3 * time.Second):
		require.FailNow(t, "answer handle did not terminate")
	}
	require.ErrorIs(t, h.Err(), cause, "handle error = %v, want %v", h.Err(), cause)
}
func answerError(t *testing.T, err error, r codex.Request, dispatch codex.DispatchState, cause error) {
	t.Helper()
	var ae *codex.AnswerError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, dispatch, ae.Dispatch)
	assert.Equal(t, r.Method(), ae.Method)
	assert.Equal(t, r.ID(), ae.ID)
	if cause != nil {
		require.ErrorIs(t, err, cause)
	}
}
func answerFrames(t *testing.T, path string) []map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	scan := bufio.NewScanner(bytes.NewReader(b))
	scan.Buffer(make([]byte, 4096), 32<<20)
	var out []map[string]json.RawMessage
	for scan.Scan() {
		var frame map[string]json.RawMessage
		{
			err := json.Unmarshal(scan.Bytes(), &frame)
			require.NoError(t, err)
		}
		if _, method := frame["method"]; !method {
			out = append(out, frame)
		}
	}
	{
		err := scan.Err()
		require.NoError(t, err)
	}
	return out
}
func responseBarrier(t *testing.T, f fixture) {
	t.Helper()
	var echo string
	{
		err := f.c.Call(context.Background(), "echo", "response-barrier", &echo)
		require.NoError(t, err)
		require.False(t, echo != "response-barrier", "barrier: %q %v", echo, err)
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
			require.FailNow(t, "peer did not receive response byte")
		case <-tick.C:
		}
	}
}

func runServerRequestIDsAndNativeReplyEnvelopes(t *testing.T, f fixture) {
	require.Equal(t, f.c.Requests(), f.c.Requests(), "Requests must return one shared pull queue")
	// ID 2 deliberately equals the client request-frames call's ID. The string
	// and integer variants must remain different in both protocol directions.
	emitRequests(t, f.c,
		requestFrame(`2`, "future/approval", `{"native":9007199254740993}`),
		requestFrame(`"2"`, "future/approval", `null`),
		requestFrame(`9007199254740993`, "future/input", ``))
	requests := []codex.Request{nextRequest(t, f.c), nextRequest(t, f.c), nextRequest(t, f.c)}
	wantIDs := []codex.ID{codex.IntegerID(2), codex.StringID("2"), codex.IntegerID(9007199254740993)}
	for i, r := range requests {
		require.False(t, r.ID() != wantIDs[i] || r.Handle().Err() != nil, "request %d identity or state changed: %v %v", i, r.ID(), r.Handle().Err())
		{
			data, err := r.Data()
			require.False(t, data != nil || err != nil, "unknown method acquired a guessed payload: %#v %v", data, err)
		}
	}
	{
		err := requests[0].Handle().Answer(context.Background(), json.RawMessage(`{"decision":"caller-selected","large":9007199254740993,"nullable":null}`))
		require.NoError(t, err)
	}
	rpc := &codex.RPCError{Code: -32042, Message: "caller refusal", Data: json.RawMessage(`{"large":9007199254740993,"nullable":null}`)}
	{
		err := requests[1].Handle().Reject(context.Background(), rpc)
		require.NoError(t, err)
	}
	{
		err := requests[2].Handle().Answer(context.Background(), nil)
		require.NoError(t, err)
	}
	for _, r := range requests {
		awaitAnswer(t, r.Handle(), codex.ErrRequestAnswered)
		answerError(t, r.Handle().Answer(context.Background(), "again"), r, codex.NotSent, codex.ErrRequestAnswered)
		answerError(t, r.Handle().Reject(context.Background(), rpc), r, codex.NotSent, codex.ErrRequestAnswered)
	}
	responseBarrier(t, f)
	frames := answerFrames(t, f.trace)
	require.Len(t, frames, 3, "sent %d responses, want exactly 3", len(frames))
	for i, frame := range frames {
		require.False(t, len(frame) != 2 || string(frame["id"]) != wantIDs[i].String(), "response %d not a native response envelope: %v", i, frame)
	}
	require.False(t, string(frames[0]["result"]) != `{"decision":"caller-selected","large":9007199254740993,"nullable":null}` || string(frames[2]["result"]) != "null", "native result changed")
	var rejected codex.RPCError
	{
		err := json.Unmarshal(frames[1]["error"], &rejected)
		require.NoError(t, err)
		require.False(t, rejected.Code != rpc.Code || rejected.Message != rpc.Message || string(rejected.Data) != string(rpc.Data), "native error changed: %+v %v", rejected, err)
	}
}

func runServerRequestRawAndTypedCopies(t *testing.T, f fixture) {
	const raw = `{ "n" : 9007199254740993, "nested" : [null, true] }`
	emitRequests(t, f.c,
		requestFrame(`"known"`, "item/commandExecution/requestApproval", approvalParams),
		requestFrame(`"bad"`, "item/commandExecution/requestApproval", `{"threadId":false}`),
		requestFrame(`"unknown"`, "future/raw", raw),
		requestFrame(`"null"`, "future/raw", `null`),
		requestFrame(`"absent"`, "future/raw", ``))
	r := nextRequest(t, f.c)
	require.False(t, r.Method() != "item/commandExecution/requestApproval" || string(r.Params()) != approvalParams, "native method or payload changed")
	data, err := r.Data()
	p, ok := data.(*types.CommandExecutionRequestApprovalParams)
	require.NoError(t, err)
	require.False(t, !ok || p.ThreadID != "native-thread-42" || p.ItemID != "native-item-9" || p.StartedAtMs != 9007199254740993 || !p.ApprovalID.IsNull() || p.Command.ValueOr("missing") != "" || string(p.UnknownFields["future"]) != `{"large":9007199254740993}`, "typed request lost fields: %#v %v", data, err)
	p.ThreadID = "mutated"
	p.UnknownFields["future"][0] = '['
	bytes := r.Params()
	bytes[0] = '['
	fresh, err := r.Data()
	require.NoError(t, err)
	require.False(t, fresh.(*types.CommandExecutionRequestApprovalParams).ThreadID != "native-thread-42" || string(fresh.(*types.CommandExecutionRequestApprovalParams).UnknownFields["future"]) != `{"large":9007199254740993}` || string(r.Params()) != approvalParams, "caller mutation reached immutable request")
	bad := nextRequest(t, f.c)
	if data, err := bad.Data(); data != nil || err == nil {
		require.FailNowf(t, "protocol scenario failed", "malformed known payload: %#v %v", data, err)
	} else {
		var de *codex.DecodeError
		require.False(t, !errors.As(err, &de) || de.Method != bad.Method() || errors.Unwrap(de) == nil, "decode error lost method or cause: %v", err)
	}
	for _, want := range []string{raw, "null", ""} {
		r := nextRequest(t, f.c)
		require.Equal(t, want, string(r.Params()), "raw payload = %s, want %s", r.Params(), want)
		{
			data, err := r.Data()
			require.False(t, data != nil || err != nil, "unknown payload must remain raw: %#v %v", data, err)
		}
	}
	require.False(t, f.c.Err() != nil || bad.Handle().Err() != nil, "payload decode failure terminated connection or handle")
	{
		err := bad.Handle().Reject(context.Background(), &codex.RPCError{Code: -32602, Message: "invalid parameters"})
		require.NoError(t, err)
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
	require.False(t, e.Method != "future/duringApproval" || string(e.Params) != `{"large":9007199254740993}`, "event stopped behind approval UI: %#v", e)
	require.Len(t, answerFrames(t, f.trace), 0, "SDK automatically answered a server request")
	for _, id := range []string{"pending-1", "pending-2"} {
		{
			r := nextRequest(t, f.c)
			require.Equal(t, codex.StringID(id), r.ID(), "pull queue lost request order")
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
	require.NoError(t, h.Err(), "preflight consumed handle: %v", h.Err())
	select {
	case <-h.Done():
		require.FailNow(t, "preflight closed handle")
	default:
	}
	responseBarrier(t, f)
	require.Len(t, answerFrames(t, f.trace), 0, "preflight failure emitted response bytes")
	{
		err := h.Answer(context.Background(), "valid retry")
		require.NoError(t, err)
	}
	responseBarrier(t, f)
	{
		frames := answerFrames(t, f.trace)
		require.False(t, len(frames) != 1 || string(frames[0]["result"]) != `"valid retry"`, "valid answer after preflight did not arrive once")
	}
}

func TestServerRequestLocalCancellation(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{ActiveRequests: 1, RequestQueue: 1})
	emitRequests(t, f.c, requestFrame(`"old"`, "future/approval", `{}`))
	r := nextRequest(t, f.c)
	require.NoError(t, r.Handle().Cancel())
	awaitAnswer(t, r.Handle(), codex.ErrRequestCanceled)
	answerError(t, r.Handle().Answer(context.Background(), "late"), r, codex.NotSent, codex.ErrRequestCanceled)
	answerError(t, r.Handle().Reject(context.Background(), &codex.RPCError{Code: -1, Message: "late"}), r, codex.NotSent, codex.ErrRequestCanceled)
	responseBarrier(t, f)
	require.Empty(t, answerFrames(t, f.trace), "local abandonment invented a wire decision")
	emitRequests(t, f.c, requestFrame(`"new"`, "future/approval", `{}`))
	fresh := nextRequest(t, f.c)
	require.NoError(t, fresh.Handle().Answer(context.Background(), "caller decision"))
}

func runResolvedServerRequestUsesExactIDAndKeepsEvent(t *testing.T, f fixture) {
	s := subscribe(t, f.c, context.Background())
	emitRequests(t, f.c,
		requestFrame(`7`, "item/commandExecution/requestApproval", approvalParams),
		requestFrame(`"7"`, "item/commandExecution/requestApproval", approvalParams))
	numeric, text := nextRequest(t, f.c), nextRequest(t, f.c)
	emitRequests(t, f.c, `{"method":"serverRequest/resolved","params":{"threadId":"native-thread-42","requestId":7}}`)
	awaitAnswer(t, numeric.Handle(), codex.ErrRequestResolved)
	require.NoError(t, text.Handle().Err(), "numeric resolution invalidated textual ID")
	answerError(t, numeric.Handle().Answer(context.Background(), "late"), numeric, codex.NotSent, codex.ErrRequestResolved)
	e := nextEvent(t, s)
	require.False(t, e.Method != "serverRequest/resolved" || string(e.Params) != `{"threadId":"native-thread-42","requestId":7}` || e.DecodeError != nil, "native resolution event lost: %#v", e)
	{
		err := text.Handle().Answer(context.Background(), "still pending")
		require.NoError(t, err)
	}
	responseBarrier(t, f)
	{
		frames := answerFrames(t, f.trace)
		require.False(t, len(frames) != 1 || string(frames[0]["id"]) != `"7"`, "resolution fabricated or miscorrelated a response")
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
			require.False(t, !errors.Is(err, codex.ErrLimit) || !errors.Is(f.c.Err(), codex.ErrRequestOverflow), "overflow omitted resource-limit evidence: %v", err)
			if capacity == "active" {
				awaitAnswer(t, first.Handle(), codex.ErrRequestOverflow)
				answerError(t, first.Handle().Answer(context.Background(), "late"), first, codex.NotSent, codex.ErrRequestOverflow)
			}
			_ = f.c.Close()
			require.Len(t, answerFrames(t, f.trace), 0, "overflow guessed an approval or rejection")
		})
	}
}

func runServerRequestCloseInvalidatesAndUnblocks(t *testing.T, f fixture) {
	emitRequests(t, f.c, requestFrame(`"old-connection"`, "future/approval", `{}`))
	old := nextRequest(t, f.c)
	consumer := make(chan bool, 1)
	go func() { _, ok := <-f.c.Requests(); consumer <- ok }()
	{
		err := f.c.Close()
		require.NoError(t, err)
	}
	awaitAnswer(t, old.Handle(), codex.ErrClosed)
	select {
	case ok := <-consumer:
		require.False(t, ok, "closed queue manufactured request")
	case <-time.After(time.Second):
		require.FailNow(t, "Close did not unblock request consumer")
	}
	fresh := startFake(t, "request-normal", codex.Limits{})
	emitRequests(t, fresh.c, requestFrame(`"old-connection"`, "future/approval", `{}`))
	current := nextRequest(t, fresh.c)
	answerError(t, old.Handle().Answer(context.Background(), "stale"), old, codex.NotSent, codex.ErrClosed)
	{
		err := current.Handle().Answer(context.Background(), "fresh")
		require.NoError(t, err)
	}
	responseBarrier(t, fresh)
	require.False(t, len(answerFrames(t, f.trace)) != 0 || len(answerFrames(t, fresh.trace)) != 1, "old-client handle escaped its connection")
}

func runConcurrentServerRequestAnswerIsOneShot(t *testing.T, f fixture) {
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
	require.EqualValues(t, 1, success, "successful answers = %d, want 1", success)
	awaitAnswer(t, r.Handle(), codex.ErrRequestAnswered)
	responseBarrier(t, f)
	require.Len(t, answerFrames(t, f.trace), 1, "concurrent callers emitted duplicate response")
}

func runServerRequestDuplicateActiveIDTerminates(t *testing.T, f fixture) {
	emitRequests(t, f.c, requestFrame(`"duplicate"`, "future/approval", `{}`))
	r := nextRequest(t, f.c)
	err := f.c.Call(context.Background(), "request-frames", []string{requestFrame(`"duplicate"`, "future/other", `{}`)}, nil)
	callError(t, err, codex.OutcomeUnknown, codex.ErrProtocol)
	awaitAnswer(t, r.Handle(), codex.ErrProtocol)
	_ = f.c.Close()
	require.Len(t, answerFrames(t, f.trace), 0, "duplicate correlation produced a guessed response")
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
			{
				err := f.c.Call(context.Background(), "request-block", nil, nil)
				require.NoError(t, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- r.Handle().Answer(ctx, strings.Repeat("x", 2<<20)) }()
			waitResponsePrefix(t, f)
			// A local Cancel must lose after Answer has claimed the request. It
			// cannot erase the in-progress response or grant another answer.
			{
				err := r.Handle().Cancel()
				require.ErrorIs(t, err, codex.ErrRequestAnswered, "Cancel stole a claimed response: %v", err)
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
				require.FailNow(t, "partial answer did not terminate")
			}
			select {
			case <-f.c.Done():
			case <-time.After(time.Second):
				require.FailNow(t, "partial write left connection usable")
			}
			if err := r.Handle().Answer(context.Background(), "retry"); err == nil {
				require.FailNow(t, "partially sent answer replayed")
			} else {
				answerError(t, err, r, codex.NotSent, nil)
			}
			_ = f.c.Close()
			assertReaped(t, f.pid)
			require.Len(t, answerFrames(t, f.trace), 0, "peer unexpectedly received a complete or replayed answer")
		})
	}
}

func TestServerRequestLimitDefaultsAndValidation(t *testing.T) {
	defaults := codex.DefaultLimits()
	require.False(t, defaults.RequestIdentities != 4096 || defaults.ActiveRequests != 128 || defaults.RequestQueue != 32 || defaults.RequestTimeout != 0, "unexpected server request bounds: %+v", defaults)
	for _, limits := range []codex.Limits{{ActiveRequests: -1}, {RequestIdentities: -1}, {RequestQueue: -1}, {RequestTimeout: -time.Second}} {
		{
			c, err := codex.NewStdio(context.Background(), codex.StdioOptions{Executable: "unused", Limits: limits})
			require.False(t, err == nil || c != nil, "negative request limit accepted: %+v %v", limits, err)
		}
	}
}

func TestEverySupportedServerRequestTypedNativeRoundTrip(t *testing.T) {
	cases := []struct {
		method, params, result   string
		paramsType, responseType any
	}{
		{"item/commandExecution/requestApproval", approvalParams, `{"decision":"decline","future":null}`, new(types.CommandExecutionRequestApprovalParams), new(types.CommandExecutionRequestApprovalResponse)},
		{"item/fileChange/requestApproval", `{"threadId":"native-thread-42","turnId":"t","itemId":"f","startedAtMs":9007199254740993,"reason":null,"grantRoot":"","future":null}`, `{"decision":"cancel","future":null}`, new(types.FileChangeRequestApprovalParams), new(types.FileChangeRequestApprovalResponse)},
		{"item/permissions/requestApproval", `{"threadId":"native-thread-42","turnId":"t","itemId":"p","cwd":"/synthetic","startedAtMs":9007199254740993,"permissions":{},"reason":null,"environmentId":null,"future":null}`, `{"permissions":{},"scope":"turn","strictAutoReview":false,"future":null}`, new(types.PermissionsRequestApprovalParams), new(types.PermissionsRequestApprovalResponse)},
		{"item/tool/requestUserInput", `{"threadId":"native-thread-42","turnId":"t","itemId":"i","isBlocking":false,"questions":[{"id":"q","header":"H","question":"Choose","isOther":false,"isSecret":false,"options":[]}],"autoResolutionMs":null,"future":null}`, `{"answers":{"q":{"answers":["caller choice"],"future":null}},"future":null}`, new(types.ToolRequestUserInputParams), new(types.ToolRequestUserInputResponse)},
		{"applyPatchApproval", `{"callId":"patch","conversationId":"native-conversation","fileChanges":{"synthetic":{"type":"add","content":"caller text"}},"reason":null,"grantRoot":"","future":null}`, `{"decision":"denied","future":null}`, new(types.ApplyPatchApprovalParams), new(types.ApplyPatchApprovalResponse)},
		{"execCommandApproval", `{"callId":"exec","conversationId":"native-conversation","command":[],"cwd":"/synthetic","parsedCmd":[],"approvalId":null,"reason":"","future":null}`, `{"decision":"abort","future":null}`, new(types.ExecCommandApprovalParams), new(types.ExecCommandApprovalResponse)},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			f := startFake(t, "request-normal", codex.Limits{})
			emitRequests(t, f.c, requestFrame(`"native-request"`, tc.method, tc.params))
			r := nextRequest(t, f.c)
			data, err := r.Data()
			require.NoError(t, err)
			require.False(t, reflect.TypeOf(data) != reflect.TypeOf(tc.paramsType), "typed payload = %T, want %T: %v", data, tc.paramsType, err)
			raw, err := json.Marshal(data)
			require.NoError(t, err)
			require.True(t, reflect.DeepEqual(decodeExactJSON(t, raw), decodeExactJSON(t, []byte(tc.params))), "native payload changed on typed round trip: %s", raw)
			{
				err := json.Unmarshal([]byte(tc.result), tc.responseType)
				require.NoError(t, err)
			}
			{
				err := r.Handle().Answer(context.Background(), tc.responseType)
				require.NoError(t, err)
			}
			responseBarrier(t, f)
			frames := answerFrames(t, f.trace)
			require.False(t, len(frames) != 1 || !reflect.DeepEqual(decodeExactJSON(t, frames[0]["result"]), decodeExactJSON(t, []byte(tc.result))), "typed native response changed: %v", frames)
		})
	}
}

func decodeExactJSON(t *testing.T, raw []byte) any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	{
		err := d.Decode(&value)
		require.NoError(t, err)
	}
	return value
}

func runMalformedOrUnrelatedResolutionKeepsHandle(t *testing.T, f fixture) {
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
		require.NoError(t, r.Handle().Err(), "unrelated resolution invalidated handle: %s", p)
		{
			e := nextEvent(t, s)
			require.False(t, e.Method != "serverRequest/resolved" || string(e.Params) != p, "malformed resolution lost raw observability: %#v", e)
		}
	}
	{
		err := r.Handle().Answer(context.Background(), "caller choice")
		require.NoError(t, err)
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
	{
		err := f.c.Call(context.Background(), "request-gate", nil, nil)
		require.NoError(t, err)
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
			require.NoError(t, result.r.Handle().Err(), "queue-full error consumed answer handle")
			rejected = append(rejected, result.r)
		case <-time.After(2 * time.Second):
			require.FailNow(t, "full queue blocked answer admission")
		}
	}
	{
		err := os.WriteFile(f.trace+".release", []byte("resume"), 0600)
		require.NoError(t, err)
	}
	select {
	case err := <-writer:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		require.FailNow(t, "cooperative peer failed to resume writer")
	}
	select {
	case result := <-results:
		require.NoError(t, result.err)
	case <-time.After(3 * time.Second):
		require.FailNow(t, "admitted response failed to resume")
	}
	for _, r := range rejected {
		{
			err := r.Handle().Answer(context.Background(), "explicit retry after NotSent")
			require.NoError(t, err)
		}
	}
	responseBarrier(t, f)
	{
		frames := answerFrames(t, f.trace)
		require.Len(t, frames, len(requests), "queue recovery sent %d replies, want %d", len(frames), len(requests))
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
				require.FailNow(t, "caller marshaler not entered")
			}
			// Caller code is frozen, but neither the reader nor the client mutex
			// may be held while it runs.
			responseBarrier(t, f)
			cause := codex.ErrRequestCanceled
			if stop == "cancel" {
				{
					err := r.Handle().Cancel()
					require.NoError(t, err)
				}
			} else {
				cause = codex.ErrClosed
				closed := make(chan error, 1)
				go func() { closed <- f.c.Close() }()
				select {
				case err := <-closed:
					require.NoError(t, err)
				case <-time.After(time.Second):
					require.FailNow(t, "Close waited on noncooperative caller code")
				}
			}
			awaitAnswer(t, r.Handle(), cause)
			once.Do(func() { close(release) })
			select {
			case err := <-answer:
				answerError(t, err, r, codex.NotSent, cause)
			case <-time.After(time.Second):
				require.FailNow(t, "stale marshal return did not finish")
			}
			if stop == "cancel" {
				responseBarrier(t, f)
			}
			require.Len(t, answerFrames(t, f.trace), 0, "stale marshal emitted response after terminal state")
		})
	}
}

func TestZeroAnswerHandleFailsLocally(t *testing.T) {
	var r codex.Request
	require.False(t, r.ID().Valid() || r.Method() != "" || r.Params() != nil, "zero request invented identity")
	{
		data, err := r.Data()
		require.False(t, data != nil || !errors.Is(err, codex.ErrInvalidAnswerHandle), "zero request Data = %#v %v", data, err)
	}
	h := r.Handle()
	awaitAnswer(t, h, codex.ErrInvalidAnswerHandle)
	answerError(t, h.Answer(context.Background(), nil), r, codex.NotSent, codex.ErrInvalidAnswerHandle)
	answerError(t, h.Reject(context.Background(), &codex.RPCError{Code: -1, Message: "invalid"}), r, codex.NotSent, codex.ErrInvalidAnswerHandle)
	require.ErrorIs(t, h.Cancel(), codex.ErrInvalidAnswerHandle, "zero handle cancellation succeeded")
}

func TestSessionQueuedAnswerCancellationIsNotSentAndRetainsHandle(t *testing.T) {
	for _, stop := range []string{"caller-cancel", "native-resolved"} {
		t.Run(stop, func(t *testing.T) {
			cause := error(context.Canceled)
			if stop == "native-resolved" {
				cause = codex.ErrRequestResolved
			}

			f := startFake(t, "request-cooperative-gate", codex.Limits{OutboundQueue: 1, WriteTimeout: 5 * time.Second, ShutdownTimeout: 200 * time.Millisecond})
			s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			emitRequests(t, f.c, requestFrame(`"queued"`, "future/approval", `{"threadId":"native-thread-42"}`))
			r := sessionRequest(t, s).Request
			{
				err := f.c.Call(context.Background(), "request-gate", nil, nil)
				require.NoError(t, err)
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
				require.FailNow(t, "one-shot answer admission did not resolve losing caller")
			}
			{
				err := r.Handle().Cancel()
				require.ErrorIs(t, err, codex.ErrRequestAnswered, "Cancel stole admitted queued answer: %v", err)
			}
			if stop == "caller-cancel" {
				cancel()
			} else {
				require.NoError(t, os.WriteFile(f.trace+".resolve", nil, 0600))
			}
			select {
			case err := <-answers:
				answerError(t, err, r, codex.NotSent, cause)
			case <-time.After(time.Second):
				require.FailNow(t, "queued answer context did not cancel promptly")
			}
			if stop == "caller-cancel" {
				require.NoError(t, r.Handle().Err(), "confirmed NotSent must keep native permission")
			} else {
				require.ErrorIs(t, r.Handle().Err(), cause)
			}
			{
				err := os.WriteFile(f.trace+".release", []byte("resume"), 0600)
				require.NoError(t, err)
			}
			select {
			case err := <-writer:
				require.NoError(t, err)
			case <-time.After(3 * time.Second):
				require.FailNow(t, "unrelated writer failed to resume")
			}
			responseBarrier(t, f)
			require.Len(t, answerFrames(t, f.trace), 0, "canceled queued answer was written later")
			if stop == "caller-cancel" {
				require.NoError(t, r.Handle().Answer(context.Background(), "explicit retry"))
				responseBarrier(t, f)
				require.Len(t, answerFrames(t, f.trace), 1)
			} else {
				require.ErrorIs(t, r.Handle().Answer(context.Background(), "late"), cause)
			}
			require.NoError(t, f.c.Err(), "cancellation before write unnecessarily terminated connection")
		})
	}
}

func runConcurrentAnswerAndCancelHaveOneWinner(t *testing.T, f fixture) {
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
			} else {
				require.True(t, errors.Is(result.err, codex.ErrRequestAnswered) || errors.Is(result.err, codex.ErrRequestCanceled), "unexpected race loss: %v", result.err)
			}
		}
		require.EqualValues(t, 1, winners, "Answer/Cancel race had %d successful operations", winners)
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
		require.Equal(t, want, n, "race emitted %d responses, want %d", n, want)
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
			require.Len(t, answerFrames(t, f.trace), 0, "malformed envelope received a guessed answer")
		})
	}
}

func runConcurrentAnswerAndNativeResolutionHaveOneOutcome(t *testing.T, f fixture) {
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
		{
			err := <-resolved
			require.NoError(t, err)
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
		require.Equal(t, want, n, "Answer/resolution race emitted %d responses, want %d", n, want)
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
				{
					err := old.Handle().Cancel()
					require.NoError(t, err)
				}
			case "resolved":
				cause = codex.ErrRequestResolved
				emitRequests(t, f.c, `{"method":"serverRequest/resolved","params":{"threadId":"native-thread-42","requestId":"reused"}}`)
			case "answered":
				cause = codex.ErrRequestAnswered
				{
					err := old.Handle().Answer(context.Background(), "first answer")
					require.NoError(t, err)
				}
			}
			awaitAnswer(t, old.Handle(), cause)
			emitRequests(t, f.c, requestFrame(`"reused"`, "future/newRequest", `{"different":true}`))
			fresh := nextRequest(t, f.c)
			answerError(t, old.Handle().Answer(context.Background(), "stale"), old, codex.NotSent, cause)
			require.ErrorIs(t, old.Handle().Cancel(), cause, "old handle changed after ID reuse")
			require.False(t, fresh.Handle().Err() != nil || fresh.Method() != "future/newRequest" || string(fresh.Params()) != `{"different":true}`, "old handle affected new request with same native ID")
			{
				err := fresh.Handle().Answer(context.Background(), "second answer")
				require.NoError(t, err)
			}
			responseBarrier(t, f)
			frames := answerFrames(t, f.trace)
			want := 1
			if firstOutcome == "answered" {
				want = 2
			}
			require.False(t, len(frames) != want || string(frames[len(frames)-1]["result"]) != `"second answer"`, "stale answer affected reused ID: %v", frames)
		})
	}
}

func TestBufferedRequestsRemainInspectableAfterClose(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{RequestQueue: 3, RequestTimeout: 30 * time.Millisecond})
	emitRequests(t, f.c,
		requestFrame(`"buffered-one"`, "future/approval", `{}`),
		requestFrame(`"buffered-two"`, "future/approval", `{}`))
	// Leave the queue unattended beyond the deprecated explicit timeout.
	// No policy decision or discard is allowed.
	time.Sleep(150 * time.Millisecond)
	{
		err := f.c.Close()
		require.NoError(t, err)
	}
	var count int
	for r := range f.c.Requests() {
		count++
		awaitAnswer(t, r.Handle(), codex.ErrClosed)
		require.False(t, string(r.Params()) != `{}` || r.Method() != "future/approval", "terminal queued request lost immutable data")
		answerError(t, r.Handle().Answer(context.Background(), "late"), r, codex.NotSent, codex.ErrClosed)
	}
	require.False(t, count != 2 || len(answerFrames(t, f.trace)) != 0, "Close drained %d queued requests or emitted policy answers", count)
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
			require.NoError(t, r.Handle().Err(), "wrong legacy conversation resolved request")
			emitRequests(t, f.c, `{"method":"serverRequest/resolved","params":{"threadId":"native-legacy","requestId":"legacy"}}`)
			awaitAnswer(t, r.Handle(), codex.ErrRequestResolved)
			answerError(t, r.Handle().Answer(context.Background(), "late"), r, codex.NotSent, codex.ErrRequestResolved)
			responseBarrier(t, f)
			require.Len(t, answerFrames(t, f.trace), 0, "legacy resolution fabricated wire answer")
		})
	}
}

func TestServerRequestsArrivingDuringInitializeAreRetained(t *testing.T) {
	f := startFake(t, "request-during-initialize", codex.Limits{})
	r := nextRequest(t, f.c)
	require.False(t, r.ID() != codex.IntegerID(1) || r.Method() != "future/earlyApproval" || string(r.Params()) != `{"beforeInitialized":true}`, "early reverse request lost identity during initialization")
	{
		err := r.Handle().Answer(context.Background(), "caller choice after handshake")
		require.NoError(t, err)
	}
	responseBarrier(t, f)
	frames := trace(t, f.trace)
	require.False(t, len(frames) != 4 || frames[0].Method != "initialize" || frames[1].Method != "initialized" || frames[2].Method != "" || string(frames[2].ID) != "1", "early answer escaped completed handshake: %+v", frames)
}

func TestServerRequestOverflowDuringInitializeCleansUp(t *testing.T) {
	f, err := openFake(t, "request-overflow-initialize", codex.Limits{RequestQueue: 1, ShutdownTimeout: 200 * time.Millisecond})
	require.False(t, err == nil || f.c != nil || !errors.Is(err, codex.ErrRequestOverflow) || !errors.Is(err, codex.ErrLimit), "startup overflow exposed usable client: %v %v", f.c, err)
	assertReaped(t, f.pid)
	require.Len(t, answerFrames(t, f.trace), 0, "startup overflow invented a policy answer")
}
