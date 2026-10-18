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

	codex "github.com/zoumo/codex-sdk-go"
	"github.com/zoumo/codex-sdk-go/types"
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
		t.Fatal("tree did not terminate")
	}
	if !errors.Is(s.Err(), cause) {
		t.Fatalf("tree cause: %v, want %v", s.Err(), cause)
	}
}

func TestTreeFanoutAdmissionIsAtomic(t *testing.T) {
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			for _, tc := range []struct {
				name     string
				options  codex.TreeOptions
				before   []string
				incoming string
			}{
				{"relation", codex.TreeOptions{EventQueue: 2}, nil, treeFrame("thread/started", boundsRelation)},
				{"receivers", codex.TreeOptions{EventQueue: 3}, nil, treeFrame("item/completed", treeSpawnParams("spawnAgent", "completed"))},
				{"pending", codex.TreeOptions{EventQueue: 3}, []string{treeChildRaw}, treeFrame("thread/started", boundsRelation)},
				{"unresolved", codex.TreeOptions{EventQueue: 2, PendingBytes: 1}, nil, treeChildRaw},
			} {
				t.Run(tc.name, func(t *testing.T) {
					f := startFake(t, "request-normal", codex.Limits{})
					s, err := f.c.Subscribe(context.Background(), "native-thread-42", tc.options)
					if err != nil {
						t.Fatal(err)
					}
					// Keep the initial accepted diagnostic queued: fanout has one slot too few.
					if len(tc.before) > 0 {
						emitRequests(t, f.c, tc.before...)
					}
					emitRequests(t, f.c, tc.incoming)
					awaitTreeCause(t, s, codex.ErrSubscriptionOverflow)
					var overflow *codex.SubscriptionOverflowError
					if !errors.As(s.Err(), &overflow) || overflow.Capacity != tc.options.EventQueue {
						t.Fatal(s.Err())
					}
					count := 0
					for e := range s.Events() {
						count++
						if e.Sequence != 1 || e.Reason != "best-effort-auto-attach-unverified" {
							t.Fatalf("partial fanout admitted: %+v", e)
						}
					}
					if count != 1 {
						t.Fatalf("accepted queue changed: %d", count)
					}
					responseBarrier(t, f)
				})
			}
		})
	}
}

func TestTreePendingDeadlineFanoutIsAtomic(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{})
	s, err := f.c.Subscribe(context.Background(), "native-thread-42", codex.TreeOptions{EventQueue: 2, PendingAge: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	emitRequests(t, f.c, treeChildRaw)
	awaitTreeCause(t, s, codex.ErrSubscriptionOverflow)
	count := 0
	for e := range s.Events() {
		count++
		if e.Reason != "best-effort-auto-attach-unverified" {
			t.Fatalf("partial expiry: %+v", e)
		}
	}
	if count != 1 {
		t.Fatal(count)
	}
	responseBarrier(t, f)
}

func TestTreeFullQueuePreservesOriginalRequestAuthority(t *testing.T) {
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := startFake(t, "request-normal", codex.Limits{})
			slow, err := f.c.Subscribe(context.Background(), "native-thread-42", codex.TreeOptions{EventQueue: 3})
			if err != nil {
				t.Fatal(err)
			}
			healthy, err := f.c.Subscribe(context.Background(), "native-thread-42", codex.TreeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer healthy.Close()
			raw, err := f.c.Subscribe(context.Background(), "native-thread-42", codex.TreeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			emitRequests(t, f.c, treeFrame("thread/started", boundsRelation)) // fills slow's three slots
			for _, tc := range []struct{ method, params, result string }{
				{"item/commandExecution/requestApproval", strings.ReplaceAll(approvalParams, "native-thread-42", "child-1"), `{"decision":"decline"}`},
				{"item/tool/requestUserInput", childQuestion, `{"answers":{"q":{"answers":["caller choice"]}}}`},
			} {
				emitRequests(t, f.c, requestFrame(`"child-request"`, tc.method, tc.params))
				r := nextRequest(t, f.c)
				if r.Method() != tc.method || string(r.Params()) != tc.params {
					t.Fatalf("request retargeted: %s %s", r.Method(), r.Params())
				}
				data, err := r.Data()
				if err != nil || data == nil {
					t.Fatalf("native request decode: %T %v", data, err)
				}
				if question, ok := data.(*types.ToolRequestUserInputParams); ok && (question.ThreadID != "child-1" || question.TurnID != "child-turn" || question.ItemID != "question-item" || len(question.Questions) != 1) {
					t.Fatalf("question source changed: %+v", question)
				}
				before := len(answerFrames(t, f.trace))
				if before != 0 && tc.method == "item/commandExecution/requestApproval" {
					t.Fatal("automatic response")
				}
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
				if successes.Load() != 1 {
					t.Fatalf("successful answers: %d", successes.Load())
				}
				answerError(t, r.Handle().Answer(context.Background(), nil), r, codex.NotSent, codex.ErrRequestAnswered)
				responseBarrier(t, f)
				frames := answerFrames(t, f.trace)
				if len(frames) != before+1 || string(frames[len(frames)-1]["id"]) != `"child-request"` || string(frames[len(frames)-1]["result"]) != tc.result {
					t.Fatalf("response changed: %v", frames)
				}
				select {
				case <-slow.Done():
					t.Fatalf("requests consumed event capacity: %v", slow.Err())
				default:
				}
			}
			emitRequests(t, f.c, treeChildRaw, treeFrame("future/done", `{"threadId":"native-thread-42"}`))
			awaitTreeCause(t, slow, codex.ErrSubscriptionOverflow)
			queued := 0
			for range slow.Events() {
				queued++
			}
			if queued != 3 {
				t.Fatalf("queue drain=%d", queued)
			}
			events := treeUntilDone(t, healthy)
			native := 0
			for _, e := range events {
				if e.Native != nil {
					native++
					if strings.Contains(e.Native.Method, "request") {
						t.Fatal("second authority in tree")
					}
				}
			}
			if native != 3 {
				t.Fatalf("healthy native count=%d", native)
			}
			for i := 0; i < 3; i++ {
				nextEvent(t, raw)
			}
			responseBarrier(t, f)
		})
	}
}

func TestTreeObservationCancellationDoesNotCancelRequest(t *testing.T) {
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := startFake(t, "request-normal", codex.Limits{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s, err := f.c.Subscribe(ctx, "native-thread-42", codex.TreeOptions{EventQueue: 3})
			if err != nil {
				t.Fatal(err)
			}
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
			if r.Handle().Err() != nil {
				t.Fatal("observation invalidated request", r.Handle().Err())
			}
			if err := r.Handle().Answer(context.Background(), json.RawMessage(`{"answers":{}}`)); err != nil {
				t.Fatal(err)
			}
			responseBarrier(t, f)
			if len(answerFrames(t, f.trace)) != 1 {
				t.Fatal("answer lost or duplicated")
			}
		})
	}
}

func TestTreeRequestTerminalAndEpochIsolation(t *testing.T) {
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			for _, terminal := range []string{"cancel", "expired", "resolved", "close"} {
				t.Run(terminal, func(t *testing.T) {
					limits := codex.Limits{}
					if terminal == "expired" {
						limits.RequestTimeout = 100 * time.Millisecond
					}
					old := startFake(t, "request-normal", limits)
					s, err := old.c.Subscribe(context.Background(), "native-thread-42", codex.TreeOptions{EventQueue: 1})
					if err != nil {
						t.Fatal(err)
					}
					emitRequests(t, old.c, requestFrame(`"reused"`, "item/tool/requestUserInput", childQuestion))
					r := nextRequest(t, old.c)
					cause := codex.ErrRequestCanceled
					switch terminal {
					case "cancel":
						if err := r.Handle().Cancel(); err != nil {
							t.Fatal(err)
						}
					case "expired":
						cause = codex.ErrRequestExpired
					case "resolved":
						cause = codex.ErrRequestResolved
						emitRequests(t, old.c, treeFrame("serverRequest/resolved", `{"threadId":"child-1","requestId":"reused"}`))
					case "close":
						cause = codex.ErrClosed
						if err := old.c.Close(); err != nil {
							t.Fatal(err)
						}
					}
					awaitAnswer(t, r.Handle(), cause)
					next := startFake(t, "request-normal", codex.Limits{})
					other, err := next.c.Subscribe(context.Background(), "native-thread-42", codex.TreeOptions{EventQueue: 1})
					if err != nil {
						t.Fatal(err)
					}
					defer other.Close()
					if nextTree(t, s).Epoch == nextTree(t, other).Epoch {
						t.Fatal("epoch reused")
					}
					emitRequests(t, next.c, requestFrame(`"reused"`, "item/tool/requestUserInput", childQuestion))
					fresh := nextRequest(t, next.c)
					answerError(t, r.Handle().Answer(context.Background(), nil), r, codex.NotSent, cause)
					if fresh.Handle().Err() != nil {
						t.Fatal("old handle invalidated fresh request")
					}
					if err := fresh.Handle().Answer(context.Background(), json.RawMessage(`{"answers":{}}`)); err != nil {
						t.Fatal(err)
					}
					responseBarrier(t, next)
					if len(answerFrames(t, next.trace)) != 1 || len(answerFrames(t, old.trace)) != 0 {
						t.Fatal("late answer crossed epoch")
					}
					_ = s.Close()
				})
			}
		})
	}
}

func TestTreeTextThinkingAndUsageStayAtNativeSource(t *testing.T) {
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := startFake(t, "request-normal", codex.Limits{})
			s, err := f.c.Subscribe(context.Background(), "native-thread-42", codex.TreeOptions{})
			if err != nil {
				t.Fatal(err)
			}
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
				source, scope := "child-1", "child"
				if i >= 4 {
					source, scope = "native-thread-42", "root"
				}
				if e.Kind != codex.TreeNative || e.Native == nil || string(e.Raw) != want || e.Source.ThreadID != source || e.Scope != scope || e.UsageScope != "unknown" || e.Native.DecodeError != nil {
					t.Fatalf("source/content changed: %+v", e)
				}
				if i == 5 {
					item := e.Native.Data.(*types.ItemCompletedNotification)
					if item.Item.AgentMessage.Text != "parent final only" {
						t.Fatal("parent answer merged")
					}
				}
			}
			select {
			case e := <-s.Events():
				t.Fatalf("synthesized result/usage: %+v", e)
			default:
			}
		})
	}
}

func TestTreeQueueCapacityOverflowIsRejected(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		limits := codex.Limits{}
		options := codex.TreeOptions{EventQueue: int(^uint(0) >> 1)}
		if inherited {
			limits.EventQueue = options.EventQueue
			options.EventQueue = 0
		}
		f := startFake(t, "request-normal", limits)
		if s, err := f.c.Subscribe(context.Background(), "root", options); err == nil || s != nil {
			t.Fatal("overflowing reserved capacity accepted")
		}
	}
}
