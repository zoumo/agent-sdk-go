package codex_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// Deliberately opaque, including whitespace and Unicode. Only the fake peer
// creates this identity; the SDK must neither parse nor normalize native IDs.
const resumedNativeID = " native/线程:\"42\" "

type resumePeer struct{ pending json.RawMessage }

func resumeThreadJSON(id string) string {
	raw, _ := json.Marshal(id)
	return strings.Replace(strings.Replace(threadJSON, `"native-thread-42"`, string(raw), 1), `"ephemeral":true`, `"ephemeral":false`, 1)
}
func resumeResponseJSON(id string) string {
	return `{"thread":` + resumeThreadJSON(id) + `,"approvalPolicy":"never","approvalsReviewer":"user","cwd":"/synthetic","model":"fake","modelProvider":"mock","sandbox":{"type":"readOnly"},"serviceTier":null,"disabledPluginIds":[],"initialTurnsPage":{"data":[],"nextCursor":null},"turnsBackwardsCursor":"opaque:next","future":9007199254740993}`
}
func (p *resumePeer) handle(mode string, m fakeEnvelope, send func(json.RawMessage, string)) bool {
	if !strings.HasPrefix(mode, "resume-") {
		return false
	}
	switch m.Method {
	case "thread/start":
		send(m.ID, strings.Replace(startResult, threadJSON, resumeThreadJSON(resumedNativeID), 1))
	case "thread/read":
		send(m.ID, `{"thread":`+resumeThreadJSON(resumedNativeID)+`}`)
	case "thread/resume", "thread/unsubscribe":
		switch mode {
		case "resume-exit":
			os.Exit(9)
		case "resume-error", "resume-missing":
			code, message := -32603, "internal failure after possible work"
			if mode == "resume-missing" {
				code, message = -32600, "thread is missing or expired"
			}
			fmt.Fprintf(os.Stdout, `{"id":%s,"error":{"code":%d,"message":%q,"data":{"native":9007199254740993}}}`+"\n", m.ID, code, message)
		case "resume-wait":
			p.pending = append([]byte(nil), m.ID...)
		case "resume-invalid":
			send(m.ID, `{}`)
		case "resume-bad-envelope":
			fmt.Fprintf(os.Stdout, `{"id":%s,"result":{},"error":{"code":-32603,"message":"bad"}}`+"\n", m.ID)
		default:
			if m.Method == "thread/unsubscribe" {
				status := strings.TrimPrefix(mode, "resume-status-")
				if status == mode {
					status = "unsubscribed"
				}
				send(m.ID, `{"status":`+fmt.Sprintf("%q", status)+`,"future":null}`)
			} else {
				var params types.ThreadResumeParams
				if json.Unmarshal(m.Params, &params) != nil {
					os.Exit(87)
				}
				id := params.ThreadID
				if mode == "resume-mismatch" {
					id = "different-native-id"
				}
				send(m.ID, resumeResponseJSON(id))
			}
		}
	case "resume-release":
		send(p.pending, resumeResponseJSON("late-native-id"))
		send(m.ID, `"fresh"`)
	case "resume-event":
		fmt.Fprintln(os.Stdout, `{"method":"future/afterUnsubscribe","params":{"threadId":"another-native-thread"}}`)
		send(m.ID, `{}`)
	default:
		return false
	}
	return true
}

func assertMethods(t *testing.T, f fixture, want ...string) []fakeEnvelope {
	t.Helper()
	frames := trace(t, f.trace)
	got := make([]string, len(frames))
	for i, frame := range frames {
		got[i] = frame.Method
	}
	require.True(t, reflect.DeepEqual(got, want), "outgoing methods = %q, want %q", got, want)
	return frames
}
func nativeOperation(c *codex.Client, method string, ctx context.Context) error {
	if method == "thread/resume" {
		_, err := c.ResumeThread(ctx, types.ThreadResumeParams{ThreadID: resumedNativeID})
		return err
	}
	_, err := c.UnsubscribeThread(ctx, types.ThreadUnsubscribeParams{ThreadID: resumedNativeID})
	return err
}

func (obs *observationSuite) TestResumeAcrossIndependentConnections() {
	t := obs.T()
	first := obs.fixture(t, "resume-normal", codex.Limits{})
	started, err := first.c.StartThread(context.Background(), types.ThreadStartParams{Ephemeral: types.Some(false)})
	require.NoError(t, err, err)
	nativeID := started.Thread.ID
	require.Equal(t, resumedNativeID, nativeID, "peer identity changed")
	{
		err := first.c.Close()
		require.NoError(t, err, err)
	}
	assertMethods(t, first, "initialize", "initialized", "thread/start")

	// A new client performs its own explicit handshake. Only the native string is
	// carried across; no SDK state, journal, persisted fixture, or replay is used.
	second := obs.fixture(t, "resume-normal", codex.Limits{})
	resumed, err := second.c.ResumeThread(context.Background(), types.ThreadResumeParams{
		ThreadID: nativeID, ServiceTier: types.Null[string](), ExcludeTurns: types.Some(false),
		Config: types.Some(map[string]json.RawMessage{}), Path: types.Some(""),
		InitialTurnsPage: types.Some(types.ThreadResumeInitialTurnsPageParams{Limit: types.Some(uint32(0))}),
	})
	require.NoError(t, err, err)
	require.False(t, resumed.Thread.ID != nativeID || resumed.Thread.SessionID != started.Thread.SessionID || resumed.Thread.CreatedAt != 9007199254740993, "native result changed")
	require.False(t, !resumed.ServiceTier.IsNull() || resumed.CollaborationMode.IsSet() || string(resumed.UnknownFields["future"]) != "9007199254740993" || resumed.TurnsBackwardsCursor.ValueOr("") != "opaque:next", "resume result fields lost")
	page, ok := resumed.InitialTurnsPage.Get()
	require.False(t, !ok || len(page.Data) != 0 || !page.NextCursor.IsNull(), "native page lost")
	read, err := second.c.ReadThread(context.Background(), types.ThreadReadParams{ThreadID: nativeID, IncludeTurns: types.Some(true)})
	require.False(t, err != nil || read.Thread.ID != nativeID, "read resumed thread: %v, %q", err, read.Thread.ID)
	{
		err := second.c.Close()
		require.NoError(t, err, err)
	}
	frames := assertMethods(t, second, "initialize", "initialized", "thread/resume", "thread/read")
	var params map[string]json.RawMessage
	_ = json.Unmarshal(frames[2].Params, &params)
	wantID, _ := json.Marshal(nativeID)
	for key, want := range map[string]string{"threadId": string(wantID), "serviceTier": "null", "excludeTurns": "false", "config": "{}", "path": `""`, "initialTurnsPage": `{"limit":0}`} {
		require.Equal(t, want, string(params[key]), "%s = %s, want %s", key, params[key], want)
	}
	require.Equal(t, 6, len(params), "unexpected fields: %s", frames[2].Params)
}

func (obs *observationSuite) TestResumeMismatchKeepsCorrelationAndNoFallback() {
	t := obs.T()
	for _, alternate := range []bool{false, true} {
		t.Run(fmt.Sprint(alternate), func(t *testing.T) {
			f := obs.fixture(t, "resume-mismatch", codex.Limits{})
			params := types.ThreadResumeParams{ThreadID: resumedNativeID}
			if alternate {
				params.Path = types.Some("/synthetic/alternate-rollout")
				params.History = types.Some([]types.ResponseItem{})
			}
			result, err := f.c.ResumeThread(context.Background(), params)
			callError(t, err, codex.Responded, codex.ErrProtocol)
			require.Equal(t, "", result.Thread.ID, "mismatched thread exposed as result")
			var ce *codex.CallError
			_ = errors.As(err, &ce)
			require.Equal(t, "thread/resume", ce.Method, "error method lost")
			var echoed string
			{
				err := f.c.Call(context.Background(), "echo", "still-ready", &echoed)
				require.False(t, err != nil || echoed != "still-ready", "mismatched result poisoned connection", err)
			}
			_ = f.c.Close()
			frames := assertMethods(t, f, "initialize", "initialized", "thread/resume", "echo")
			expected, _ := json.Marshal(ce.ID)
			require.True(t, reflect.DeepEqual([]byte(frames[2].ID), expected), "matching RPC ID lost")
			raw, _ := json.Marshal(params)
			require.Equal(t, string(raw), string(frames[2].Params), "native request was rewritten")
		})
	}
}

func (obs *observationSuite) TestResumeAndUnsubscribeFailureNeverReplay() {
	t := obs.T()
	for _, method := range []string{"thread/resume", "thread/unsubscribe"} {
		for _, mode := range []string{"resume-error", "resume-missing", "resume-invalid", "resume-bad-envelope", "resume-exit"} {
			t.Run(method+"/"+mode, func(t *testing.T) {
				f := obs.fixture(t, mode, codex.Limits{})
				err := nativeOperation(f.c, method, context.Background())
				switch mode {
				case "resume-error", "resume-missing":
					callError(t, err, codex.Responded, nil)
					var rpc *codex.RPCError
					code := int64(-32603)
					if mode == "resume-missing" {
						code = -32600
					}
					require.False(t, !errors.As(err, &rpc) || rpc.Code != code || string(rpc.Data) != `{"native":9007199254740993}`, "native RPC error lost: %v", err)
				case "resume-invalid":
					callError(t, err, codex.Responded, codex.ErrProtocol)
				case "resume-bad-envelope":
					callError(t, err, codex.OutcomeUnknown, codex.ErrProtocol)
				case "resume-exit":
					callError(t, err, codex.OutcomeUnknown, io.EOF)
					// A dead connection fails locally; it never initializes another child.
					callError(t, nativeOperation(f.c, method, context.Background()), codex.NotSent, io.EOF)
				}
				_ = f.c.Close()
				frames := assertMethods(t, f, "initialize", "initialized", method)
				var params map[string]string
				_ = json.Unmarshal(frames[2].Params, &params)
				require.False(t, params["threadId"] != resumedNativeID || len(params) != 1, "request identity changed")
			})
		}
	}
}

func (obs *observationSuite) TestResumeAndUnsubscribeCancellationEvidence() {
	t := obs.T()
	for _, method := range []string{"thread/resume", "thread/unsubscribe"} {
		t.Run(method, func(t *testing.T) {
			f := obs.fixture(t, "resume-wait", codex.Limits{})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			callError(t, nativeOperation(f.c, method, ctx), codex.NotSent, context.Canceled)
			if err := nativeOperation(f.c, method, nil); err == nil {
				require.FailNow(t, "test invariant", "nil context accepted")
			} else {
				callError(t, err, codex.NotSent, nil)
			}
			ctx, cancel = context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() { result <- nativeOperation(f.c, method, ctx) }()
			waitTrace(t, f.trace, method)
			// Receipt alone can race the sender returning from Write. This
			// later response proves the serialized first write has settled.
			responseBarrier(t, f)
			cancel()
			callError(t, <-result, codex.OutcomeUnknown, context.Canceled)
			var fresh string
			{
				err := f.c.Call(context.Background(), "resume-release", nil, &fresh)
				require.False(t, err != nil || fresh != "fresh", "late result affected fresh call", err)
			}
			require.Equal(t, uint64(1), f.c.Diagnostics().UnmatchedResponses, "late response not counted")
			_ = f.c.Close()
			assertMethods(t, f, "initialize", "initialized", method, "echo", "resume-release")
		})
	}
}

func (obs *observationSuite) TestResumeAndUnsubscribeTimeoutAndClose() {
	t := obs.T()
	for _, method := range []string{"thread/resume", "thread/unsubscribe"} {
		for _, terminate := range []string{"deadline", "close"} {
			t.Run(method+"/"+terminate, func(t *testing.T) {
				f := obs.fixture(t, "resume-wait", codex.Limits{})
				ctx := context.Background()
				var cancel context.CancelFunc
				if terminate == "deadline" {
					ctx, cancel = context.WithTimeout(ctx, time.Second)
					defer cancel()
				}
				result := make(chan error, 1)
				go func() { result <- nativeOperation(f.c, method, ctx) }()
				waitTrace(t, f.trace, method)
				cause := error(context.DeadlineExceeded)
				if terminate == "close" {
					cause = codex.ErrClosed
					{
						err := f.c.Close()
						require.NoError(t, err, err)
					}
				}
				callError(t, <-result, codex.OutcomeUnknown, cause)
				_ = f.c.Close()
				assertMethods(t, f, "initialize", "initialized", method)
			})
		}
	}
}

func (obs *observationSuite) TestUnsubscribeNativeStatusAndLocalIndependence() {
	t := obs.T()
	for _, status := range []types.ThreadUnsubscribeStatus{types.ThreadUnsubscribeStatusNotLoaded, types.ThreadUnsubscribeStatusNotSubscribed, types.ThreadUnsubscribeStatusUnsubscribed, "futureStatus"} {
		t.Run(string(status), func(t *testing.T) {
			f := obs.fixture(t, "resume-status-"+string(status), codex.Limits{})
			s := subscribe(t, f.c, context.Background())
			result, err := f.c.UnsubscribeThread(context.Background(), types.ThreadUnsubscribeParams{ThreadID: resumedNativeID})
			require.False(t, err != nil || result.Status != status || string(result.UnknownFields["future"]) != "null", "native status lost: %+v %v", result, err)
			select {
			case <-s.Done():
				require.FailNow(t, "test invariant", "server unsubscribe closed local subscription")
			default:
			}
			{
				err := f.c.Call(context.Background(), "resume-event", nil, nil)
				require.NoError(t, err, err)
			}
			{
				e := nextEvent(t, s)
				require.Equal(t, "future/afterUnsubscribe", e.Method, "unrelated event was lost")
			}
			_ = s.Close()
			awaitSubscription(t, s, nil)
			_ = f.c.Close()
			assertMethods(t, f, "initialize", "initialized", "thread/unsubscribe", "resume-event")
		})
	}
}

func TestResumeAndUnsubscribeConcurrentClose(t *testing.T) {
	f := startFake(t, "resume-normal", codex.Limits{})
	var wg sync.WaitGroup
	failures := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			method := "thread/resume"
			if i%2 == 0 {
				method = "thread/unsubscribe"
			}
			if err := nativeOperation(f.c, method, context.Background()); err != nil && !errors.Is(err, codex.ErrClosed) {
				failures <- err
			}
		}(i)
	}
	_ = f.c.Close()
	wg.Wait()
	close(failures)
	for err := range failures {
		assert.NoError(t, err, "unexpected concurrent error")
	}
	frames := trace(t, f.trace)
	count := 0
	for _, m := range frames {
		switch m.Method {
		case "initialize", "initialized":
		case "thread/resume", "thread/unsubscribe":
			count++
		default:
			require.FailNowf(t, "test invariant", "unexpected method: %s", m.Method)
		}
	}
	require.False(t, count > 24, "requests replayed")
	callError(t, nativeOperation(f.c, "thread/resume", context.Background()), codex.NotSent, codex.ErrClosed)
}

func (obs *observationSuite) TestResumeAndUnsubscribeNotSentLimits() {
	t := obs.T()
	f := obs.fixture(t, "resume-normal", codex.Limits{MaxFrameBytes: 2048})
	_, err := f.c.ResumeThread(context.Background(), types.ThreadResumeParams{ThreadID: resumedNativeID, ExcludeTurns: types.Null[bool]()})
	callError(t, err, codex.NotSent, nil)
	tooLarge := strings.Repeat("x", 4096)
	_, err = f.c.ResumeThread(context.Background(), types.ThreadResumeParams{ThreadID: tooLarge})
	callError(t, err, codex.NotSent, codex.ErrFrameTooLarge)
	_, err = f.c.UnsubscribeThread(context.Background(), types.ThreadUnsubscribeParams{ThreadID: tooLarge})
	callError(t, err, codex.NotSent, codex.ErrFrameTooLarge)
	// An independent successful call proves validation/admission errors did not
	// poison the connection; the transcript proves the failed methods sent nothing.
	{
		err := f.c.Call(context.Background(), "echo", true, nil)
		require.NoError(t, err, err)
	}
	_ = f.c.Close()
	assertMethods(t, f, "initialize", "initialized", "echo")
}

func (obs *observationSuite) TestResumeAndUnsubscribePendingAdmission() {
	t := obs.T()
	f := obs.fixture(t, "resume-wait", codex.Limits{PendingCalls: 1})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- nativeOperation(f.c, "thread/resume", ctx) }()
	waitTrace(t, f.trace, "thread/resume")
	for _, method := range []string{"thread/resume", "thread/unsubscribe"} {
		callError(t, nativeOperation(f.c, method, context.Background()), codex.NotSent, codex.ErrLimit)
	}
	cancel()
	callError(t, <-result, codex.OutcomeUnknown, context.Canceled)
	_ = f.c.Close()
	assertMethods(t, f, "initialize", "initialized", "thread/resume")
}
