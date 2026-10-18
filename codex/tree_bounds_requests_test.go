package codex_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// Original synthetic frames use the existing public request-frames transport seam.
func treeFrame(method, params string) string {
	return fmt.Sprintf(`{"method":%q,"params":%s}`, method, params)
}

const boundsRelation = `{"thread":{"id":"child-1","parentThreadId":"native-thread-42"}}`
const childQuestion = `{"threadId":"child-1","turnId":"child-turn","itemId":"question-item","isBlocking":false,"questions":[{"id":"q","header":"H","question":"Choose","isOther":false,"isSecret":false,"options":[]}],"autoResolutionMs":null,"future":null}`

func awaitTreeCause(t *testing.T, s *codex.Subscription, cause error) {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(3 * time.Second):
		require.FailNow(t, "test invariant", "tree did not terminate")
	}
	require.ErrorIs(t, s.Err(), cause, "tree cause: %v, want %v", s.Err(), cause)
}

func (obs *observationSuite) TestTreeFanoutAdmissionIsAtomic() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			for _, tc := range []struct {
				name     string
				options  types.SubscriptionOptions
				before   []string
				incoming string
			}{
				{"relation", types.SubscriptionOptions{EventQueue: 2}, nil, treeFrame("thread/started", boundsRelation)},
				{"receivers", types.SubscriptionOptions{EventQueue: 3}, nil, treeFrame("item/completed", treeSpawnParams("spawnAgent", "completed"))},
				{"pending", types.SubscriptionOptions{EventQueue: 3}, []string{treeChildRaw}, treeFrame("thread/started", boundsRelation)},
				{"unresolved", types.SubscriptionOptions{EventQueue: 2, PendingBytes: 1}, nil, treeChildRaw},
			} {
				t.Run(tc.name, func(t *testing.T) {
					f := obs.fixture(t, "request-normal", codex.Limits{})
					s, err := f.c.Subscribe(context.Background(), "native-thread-42", tc.options)
					require.NoError(t, err, err)
					// Keep the initial accepted diagnostic queued: fanout has one slot too few.
					if len(tc.before) > 0 {
						emitRequests(t, f.c, tc.before...)
					}
					emitRequests(t, f.c, tc.incoming)
					awaitTreeCause(t, s, codex.ErrSubscriptionOverflow)
					var overflow *codex.SubscriptionOverflowError
					require.False(t, !errors.As(s.Err(), &overflow) || overflow.Capacity != tc.options.EventQueue, s.Err())
					count := 0
					for event := range s.Events() {
						e := recordView(event)
						count++
						require.Equal(t, "best-effort-auto-attach-unverified", e.Reason, "partial fanout admitted: %+v", e)
					}
					require.Equal(t, 1, count, "accepted queue changed: %d", count)
					responseBarrier(t, f)
				})
			}
		})
	}
}

func (obs *observationSuite) TestTreePendingDeadlineFanoutIsAtomic() {
	t := obs.T()
	f := obs.fixture(t, "request-normal", codex.Limits{})
	s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{EventQueue: 2, PendingAge: 10 * time.Millisecond})
	require.NoError(t, err, err)
	emitRequests(t, f.c, treeChildRaw)
	awaitTreeCause(t, s, codex.ErrSubscriptionOverflow)
	count := 0
	for event := range s.Events() {
		e := recordView(event)
		count++
		require.Equal(t, "best-effort-auto-attach-unverified", e.Reason, "partial expiry: %+v", e)
	}
	require.Equal(t, 1, count, count)
	responseBarrier(t, f)
}

func (obs *observationSuite) TestTreeFullQueuePreservesOriginalRequestAuthority() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := obs.fixture(t, "request-normal", codex.Limits{})
			slow, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{EventQueue: 3})
			require.NoError(t, err, err)
			healthy, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			require.NoError(t, err, err)
			defer healthy.Close()
			raw, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			require.NoError(t, err, err)
			defer raw.Close()
			emitRequests(t, f.c, treeFrame("thread/started", boundsRelation)) // fills slow's three slots
			for _, tc := range []struct{ method, params, result string }{
				{"item/commandExecution/requestApproval", strings.ReplaceAll(approvalParams, "native-thread-42", "child-1"), `{"decision":"decline"}`},
				{"item/tool/requestUserInput", childQuestion, `{"answers":{"q":{"answers":["caller choice"]}}}`},
			} {
				emitRequests(t, f.c, requestFrame(`"child-request"`, tc.method, tc.params))
				r := nextRequest(t, f.c)
				require.False(t, r.Method() != tc.method || string(r.Params()) != tc.params, "request retargeted: %s %s", r.Method(), r.Params())
				data, err := r.Data()
				require.False(t, err != nil || data == nil, "native request decode: %T %v", data, err)
				{
					question, ok := data.(*types.ToolRequestUserInputParams)
					require.False(t, ok && (question.ThreadID != "child-1" || question.TurnID != "child-turn" || question.ItemID != "question-item" || len(question.Questions) != 1), "question source changed: %+v", question)
				}
				before := len(answerFrames(t, f.trace))
				require.False(t, before != 0 && tc.method == "item/commandExecution/requestApproval", "automatic response")
				var successes atomic.Int32
				var wg sync.WaitGroup
				for i := 0; i < 12; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						if r.Handle().Answer(context.Background(), json.RawMessage(tc.result)) == nil {
							successes.Add(1)
						}
					}()
				}
				wg.Wait()
				require.Equal(t, int32(1), successes.Load(), "successful answers: %d", successes.Load())
				answerError(t, r.Handle().Answer(context.Background(), nil), r, codex.NotSent, codex.ErrRequestAnswered)
				responseBarrier(t, f)
				frames := answerFrames(t, f.trace)
				require.False(t, len(frames) != before+1 || string(frames[len(frames)-1]["id"]) != `"child-request"` || string(frames[len(frames)-1]["result"]) != tc.result, "response changed: %v", frames)
				select {
				case <-slow.Done():
					require.FailNowf(t, "test invariant", "requests consumed event capacity: %v", slow.Err())
				default:
				}
			}
			emitRequests(t, f.c, treeChildRaw, treeFrame("future/done", `{"threadId":"native-thread-42"}`))
			awaitTreeCause(t, slow, codex.ErrSubscriptionOverflow)
			queued := 0
			for range slow.Events() {
				queued++
			}
			require.Equal(t, 3, queued, "queue drain=%d", queued)
			events := treeUntilDone(t, healthy)
			native := 0
			for _, e := range events {
				if e.Native != nil {
					native++
					require.False(t, strings.Contains(e.Native.Method, "request"), "second authority in tree")
				}
			}
			require.Equal(t, 3, native, "healthy native count=%d", native)
			for i := 0; i < 3; i++ {
				nextEvent(t, raw)
			}
			responseBarrier(t, f)
		})
	}
}

func (obs *observationSuite) TestTreeObservationCancellationDoesNotCancelRequest() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := obs.fixture(t, "request-normal", codex.Limits{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s, err := f.c.Subscribe(ctx, "native-thread-42", types.SubscriptionOptions{EventQueue: 3})
			require.NoError(t, err, err)
			emitRequests(t, f.c, treeFrame("thread/started", boundsRelation), requestFrame(`17`, "item/tool/requestUserInput", childQuestion))
			r := nextRequest(t, f.c)
			// Race cancellation, publication and repeated Close while the queue is full.
			var wg sync.WaitGroup
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); cancel(); _ = s.Close(); _ = s.Err() }()
			}
			emitRequests(t, f.c, treeChildRaw)
			wg.Wait()
			require.NoError(t, r.Handle().Err(), "observation invalidated request", r.Handle().Err())
			{
				err := r.Handle().Answer(context.Background(), json.RawMessage(`{"answers":{}}`))
				require.NoError(t, err, err)
			}
			responseBarrier(t, f)
			require.Equal(t, 1, len(answerFrames(t, f.trace)), "answer lost or duplicated")
		})
	}
}

func (obs *observationSuite) TestTreeRequestTerminalAndEpochIsolation() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			for _, terminal := range []string{"cancel", "expired", "resolved", "close"} {
				t.Run(terminal, func(t *testing.T) {
					limits := codex.Limits{}
					if terminal == "expired" {
						limits.RequestTimeout = 100 * time.Millisecond
					}
					old := obs.fixture(t, "request-normal", limits)
					s, err := old.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{EventQueue: 1})
					require.NoError(t, err, err)
					emitRequests(t, old.c, requestFrame(`"reused"`, "item/tool/requestUserInput", childQuestion))
					r := nextRequest(t, old.c)
					cause := codex.ErrRequestCanceled
					switch terminal {
					case "cancel":
						{
							err := r.Handle().Cancel()
							require.NoError(t, err, err)
						}
					case "expired":
						cause = codex.ErrRequestExpired
					case "resolved":
						cause = codex.ErrRequestResolved
						emitRequests(t, old.c, treeFrame("serverRequest/resolved", `{"threadId":"child-1","requestId":"reused"}`))
					case "close":
						cause = codex.ErrClosed
						{
							err := old.c.Close()
							require.NoError(t, err, err)
						}
					}
					awaitAnswer(t, r.Handle(), cause)
					next := obs.fixture(t, "request-normal", codex.Limits{})
					other, err := next.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{EventQueue: 1})
					require.NoError(t, err, err)
					defer other.Close()
					nextTree(t, s)
					nextTree(t, other)
					emitRequests(t, next.c, requestFrame(`"reused"`, "item/tool/requestUserInput", childQuestion))
					fresh := nextRequest(t, next.c)
					answerError(t, r.Handle().Answer(context.Background(), nil), r, codex.NotSent, cause)
					require.NoError(t, fresh.Handle().Err(), "old handle invalidated fresh request")
					{
						err := fresh.Handle().Answer(context.Background(), json.RawMessage(`{"answers":{}}`))
						require.NoError(t, err, err)
					}
					responseBarrier(t, next)
					require.False(t, len(answerFrames(t, next.trace)) != 1 || len(answerFrames(t, old.trace)) != 0, "late answer crossed epoch")
					_ = s.Close()
				})
			}
		})
	}
}

func (obs *observationSuite) TestTreeTextThinkingAndUsageStayAtNativeSource() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := obs.fixture(t, "request-normal", codex.Limits{})
			s, err := f.c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
			require.NoError(t, err, err)
			defer s.Close()
			emitRequests(t, f.c, treeFrame("thread/started", boundsRelation))
			// Drain initial diagnostic, association, and its native notification.
			for i := 0; i < 3; i++ {
				nextTree(t, s)
			}
			frames := []string{
				treeChildRaw, treeChildRaw,
				treeFrame("item/reasoning/textDelta", `{"threadId":"child-1","turnId":"child-turn","itemId":"thinking","delta":"private child reasoning","contentIndex":0,"future":null}`),
				treeFrame("thread/tokenUsage/updated", `{"threadId":"child-1","turnId":"child-turn","tokenUsage":{"total":{"totalTokens":5,"inputTokens":3,"cachedInputTokens":0,"cacheWriteInputTokens":0,"outputTokens":2,"reasoningOutputTokens":0},"last":{"totalTokens":2,"inputTokens":1,"cachedInputTokens":0,"cacheWriteInputTokens":0,"outputTokens":1,"reasoningOutputTokens":0},"modelContextWindow":null}}`),
				treeFrame("thread/tokenUsage/updated", `{"threadId":"native-thread-42","turnId":"root-turn","tokenUsage":{"total":{"totalTokens":10,"inputTokens":6,"cachedInputTokens":0,"cacheWriteInputTokens":0,"outputTokens":4,"reasoningOutputTokens":0},"last":{"totalTokens":4,"inputTokens":3,"cachedInputTokens":0,"cacheWriteInputTokens":0,"outputTokens":1,"reasoningOutputTokens":0},"modelContextWindow":null},"future":null}`),
				treeFrame("item/completed", `{"threadId":"native-thread-42","turnId":"root-turn","completedAtMs":8,"item":{"type":"agentMessage","id":"final","text":"parent final only","phase":"final_answer"}}`),
			}
			emitRequests(t, f.c, frames...)
			for i, want := range frames {
				e := nextTree(t, s)
				source := "child-1"
				if i >= 4 {
					source = "native-thread-42"
				}
				require.False(t, e.Kind != nativeRecord || e.Native == nil || string(e.Raw) != want || e.Source.ThreadID != source || e.Native.DecodeError != nil, "source/content changed: %+v", e)
				if i == 5 {
					item := e.Native.Data.(*types.ItemCompletedNotification)
					require.Equal(t, "parent final only", item.Item.AgentMessage.Text, "parent answer merged")
				}
			}
			select {
			case event := <-s.Events():
				e := recordView(event)
				require.FailNowf(t, "test invariant", "synthesized result/usage: %+v", e)
			default:
			}
		})
	}
}

func (obs *observationSuite) TestTreeQueueCapacityOverflowIsRejected() {
	t := obs.T()
	for _, inherited := range []bool{false, true} {
		limits := codex.Limits{}
		options := types.SubscriptionOptions{EventQueue: int(^uint(0) >> 1)}
		if inherited {
			limits.EventQueue = options.EventQueue
			options.EventQueue = 0
		}
		f := obs.fixture(t, "request-normal", limits)
		{
			s, err := f.c.Subscribe(context.Background(), "root", options)
			require.False(t, err == nil || s != nil, "overflowing reserved capacity accepted")
		}
	}
}
