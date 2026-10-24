package codex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func nextSessionNative(t *testing.T, s *codex.Session) codex.Received {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		r, err := s.Receive(ctx)
		require.NoError(t, err)
		if r.Kind == codex.ReceivedEvent {
			return r
		}
		require.Equal(t, codex.ReceivedObservation, r.Kind)
	}
}

func (obs *observationSuite) TestSessionCurrentVersionMetadata() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := obs.fixture(t, "request-normal", codex.Limits{})
			s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
			require.NoError(t, err, err)
			defer s.Close()
			emitRequests(t, f.c, treeFrame("thread/started", boundsRelation))
			nextSessionNative(t, s)
			frames := []string{
				treeFrame("turn/started", `{"threadId":"child-1","turn":{"id":"child-turn","rootTurnId":"chain-root-turn","items":[],"status":"inProgress","future":null}}`),
				treeFrame("item/started", `{"threadId":"native-thread-42","turnId":"root-turn","startedAtMs":1,"item":{"type":"subAgentActivity","id":"activity","kind":"started","agentThreadId":"child-1","agentPath":"display-only","model":"current-model","reasoningEffort":"high","future":null}}`),
				treeFrame("item/completed", `{"threadId":"native-thread-42","turnId":"root-turn","completedAtMs":2,"item":{"type":"agentMessage","id":"partial","text":"partial parent answer","phase":"partial_answer"}}`),
				treeFrame("error", `{"threadId":"child-1","turnId":"child-turn","willRetry":false,"error":{"message":"opaque provider error","codexErrorInfo":{}}}`),
				treeFrame("turn/completed", `{"threadId":"native-thread-42","turn":{"id":"root-turn","rootTurnId":null,"items":[],"status":"completed"}}`),
				treeChildRaw,
			}
			emitRequests(t, f.c, frames...)
			for i, want := range frames {
				e := nextSessionNative(t, s)
				require.False(t, e.Kind != codex.ReceivedEvent || e.Event == nil || e.Event.DecodeError != nil || string(e.Raw) != want, "current wire changed: %+v", e)
				switch i {
				case 0:
					n := e.Event.Data.(*types.TurnStartedNotification)
					{
						got, ok := n.Turn.RootTurnID.Get()
						require.False(t, !ok || got != "chain-root-turn" || e.Source.TurnID != "child-turn", "turn chain conflated with thread membership")
					}
				case 1:
					n := e.Event.Data.(*types.ItemStartedNotification).Item.SubAgentActivity
					model, ok := n.Model.Get()
					effort, known := n.ReasoningEffort.Get()
					require.False(t, !ok || model != "current-model" || !known || effort != "high" || string(n.UnknownFields["future"]) != "null", "current activity metadata lost")
				case 2:
					n := e.Event.Data.(*types.ItemCompletedNotification).Item.AgentMessage
					{
						phase, ok := n.Phase.Get()
						require.False(t, !ok || phase != types.MessagePhasePartialAnswer || n.Text != "partial parent answer", "partial answer reinterpreted")
					}
				case 4:
					n := e.Event.Data.(*types.TurnCompletedNotification)
					require.True(t, n.Turn.RootTurnID.IsNull(), "explicit null lost")
				case 5:
					require.Equal(t, "child-1", e.Source.ThreadID, "parent completion closed child observation")
				}
			}
			select {
			case <-s.Done():
				require.FailNow(t, "test invariant", "activity/partial answer ended observation")
			default:
			}
		})
	}
}

func (obs *observationSuite) TestSessionTextThinkingAndUsageStayAtNativeSource() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := obs.fixture(t, "request-normal", codex.Limits{})
			s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
			require.NoError(t, err, err)
			defer s.Close()
			emitRequests(t, f.c, treeFrame("thread/started", boundsRelation))
			// Drain initial diagnostic, association, and its native notification.
			nextSessionNative(t, s)
			frames := []string{
				treeChildRaw, treeChildRaw,
				treeFrame("item/reasoning/textDelta", `{"threadId":"child-1","turnId":"child-turn","itemId":"thinking","delta":"private child reasoning","contentIndex":0,"future":null}`),
				treeFrame("thread/tokenUsage/updated", `{"threadId":"child-1","turnId":"child-turn","tokenUsage":{"total":{"totalTokens":5,"inputTokens":3,"cachedInputTokens":0,"cacheWriteInputTokens":0,"outputTokens":2,"reasoningOutputTokens":0},"last":{"totalTokens":2,"inputTokens":1,"cachedInputTokens":0,"cacheWriteInputTokens":0,"outputTokens":1,"reasoningOutputTokens":0},"modelContextWindow":null}}`),
				treeFrame("thread/tokenUsage/updated", `{"threadId":"native-thread-42","turnId":"root-turn","tokenUsage":{"total":{"totalTokens":10,"inputTokens":6,"cachedInputTokens":0,"cacheWriteInputTokens":0,"outputTokens":4,"reasoningOutputTokens":0},"last":{"totalTokens":4,"inputTokens":3,"cachedInputTokens":0,"cacheWriteInputTokens":0,"outputTokens":1,"reasoningOutputTokens":0},"modelContextWindow":null},"future":null}`),
				treeFrame("item/completed", `{"threadId":"native-thread-42","turnId":"root-turn","completedAtMs":8,"item":{"type":"agentMessage","id":"final","text":"parent final only","phase":"final_answer"}}`),
			}
			emitRequests(t, f.c, frames...)
			for i, want := range frames {
				e := nextSessionNative(t, s)
				source := "child-1"
				if i >= 4 {
					source = "native-thread-42"
				}
				require.False(t, e.Kind != codex.ReceivedEvent || e.Event == nil || string(e.Raw) != want || e.Source.ThreadID != source || e.Event.DecodeError != nil, "source/content changed: %+v", e)
				if i == 5 {
					item := e.Event.Data.(*types.ItemCompletedNotification)
					require.Equal(t, "parent final only", item.Item.AgentMessage.Text, "parent answer merged")
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			_, err = s.Receive(ctx)
			require.ErrorIs(t, err, context.DeadlineExceeded, "synthesized result/usage")
		})
	}
}

func (obs *observationSuite) TestSessionParentIdleChildActive() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := obs.fixture(t, "tree-lifecycle", codex.Limits{})
			s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
			require.NoError(t, err, err)
			defer s.Close()
			_, err = s.Submit(context.Background(), codex.Input{Input: []types.UserInput{}})
			require.NoError(t, err)
			activities := 0
			rootIdle, rootDone, childActive, delta, interrupted, nextRun := false, false, false, false, false, false
			for _, e := range sessionUntilDone(t, s) {
				if e.Event == nil {
					continue
				}
				if e.Event.Method == "item/completed" && strings.Contains(string(e.Raw), "subAgentActivity") {
					require.NoError(t, e.Event.DecodeError, e.Event.DecodeError)
					activities++
				}
				if e.Event.Method == "thread/status/changed" {
					if e.Source.ThreadID == "child-1" {
						childActive = strings.Contains(string(e.Raw), `"active"`)
					}
					if e.Source.ThreadID == "native-thread-42" && strings.Contains(string(e.Raw), `"idle"`) {
						rootIdle = true
					}
				}
				if e.Event.Method == "turn/completed" && e.Source.ThreadID == "native-thread-42" {
					require.False(t, !rootIdle || !childActive, "parent idle lost or child status changed")
					rootDone = true
				}
				if e.Event.Method == "item/agentMessage/delta" {
					delta = rootDone && childActive && string(e.Raw) == treeChildRaw && e.Source.TurnID == "child-turn"
				}
				if e.Event.Method == "turn/completed" && e.Source.ThreadID == "child-1" {
					interrupted = strings.Contains(string(e.Raw), `"interrupted"`)
				}
				if e.Event.Method == "turn/started" && e.Source.ThreadID == "child-1" {
					nextRun = e.Source.TurnID == "next-child-turn"
				}
			}
			require.False(t, !rootDone || !delta || !interrupted || !nextRun || activities != 2, "lifecycle: %v %v %v %v", rootDone, delta, interrupted, nextRun)
			select {
			case <-s.Done():
				require.FailNow(t, "test invariant", "parent ended observation")
			default:
			}
			{
				err = f.c.Call(context.Background(), "tree-step", nil, nil)
				require.NoError(t, err, err)
			}
			{
				e := nextSessionNative(t, s)
				require.False(t, e.Event == nil || string(e.Raw) != treeChildRaw, "child no longer observed")
			}
		})
	}
}

func sessionUntilDone(t *testing.T, s *codex.Session) []codex.Received {
	t.Helper()
	var records []codex.Received
	for {
		r := nextSessionNative(t, s)
		records = append(records, r)
		if r.Event.Method == "future/done" {
			return records
		}
	}
}

func (obs *observationSuite) TestSessionV2NonMembershipEvidence() {
	t := obs.T()
	valid := v2Activity("native-thread-42", "child-1", "probe", "started")
	cases := []struct{ name, method, params string }{
		{"item-started", "item/started", strings.Replace(valid, `"completedAtMs":3`, `"startedAtMs":3`, 1)},
		{"interacted", "item/completed", v2Activity("native-thread-42", "child-1", "probe", "interacted")},
		{"interrupted", "item/completed", v2Activity("native-thread-42", "child-1", "probe", "interrupted")},
		{"completed", "item/completed", v2Activity("native-thread-42", "child-1", "probe", "completed")},
		{"future-kind", "item/completed", v2Activity("native-thread-42", "child-1", "probe", "future")},
		{"empty-child", "item/completed", v2Activity("native-thread-42", "", "probe", "started")},
		{"empty-item", "item/completed", v2Activity("native-thread-42", "child-1", "", "started")},
		{"empty-parent", "item/completed", v2Activity("", "child-1", "probe", "started")},
		{"empty-turn", "item/completed", strings.Replace(valid, `"parent-turn"`, `""`, 1)},
		{"missing-child", "item/completed", strings.Replace(valid, `"agentThreadId":"child-1",`, ``, 1)},
		{"null-child", "item/completed", strings.Replace(valid, `"agentThreadId":"child-1"`, `"agentThreadId":null`, 1)},
		{"duplicate-child", "item/completed", strings.Replace(valid, `"agentThreadId":"child-1"`, `"agentThreadId":"child-1","agentThreadId":"other"`, 1)},
		{"conflicting-item", "item/completed", strings.Replace(valid, `"turnId"`, `"itemId":"other","turnId"`, 1)},
		{"bad-model", "item/completed", strings.Replace(valid, `"selected-model"`, `7`, 1)},
	}
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					f := obs.fixture(t, "request-normal", codex.Limits{})
					s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
					require.NoError(t, err)
					defer s.Close()
					frame := treeFrame(tc.method, tc.params)
					marker := treeFrame("future/done", `{"threadId":"native-thread-42"}`)
					emitRequests(t, f.c, frame, treeChildRaw, treeFrame("future/other", `{"threadId":"other"}`), marker)
					forwarded := false
					for _, r := range sessionUntilDone(t, s) {
						require.NotEqual(t, "child-1", r.Source.ThreadID, "invalid evidence admitted a child")
						if string(r.Raw) == frame {
							forwarded = true
						}
					}
					diagnostics := f.c.Unattributed()
					foundChild, foundFrame := false, false
					for _, r := range diagnostics.Records {
						if string(r.Raw) == treeChildRaw {
							foundChild = true
						}
						if string(r.Raw) == frame {
							foundFrame = true
						}
					}
					require.True(t, foundChild, "unowned child Raw lost")
					require.True(t, forwarded || foundFrame, "original malformed/unknown evidence lost")
				})
			}
		})
	}
}

// These are migrated native protocol guarantees, independent of old observer gates.
func (obs *observationSuite) TestSessionLegacyNativeChildEvidence() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			for _, mode := range []string{"tree-success", "tree-failed", "tree-resume"} {
				t.Run(mode, func(t *testing.T) {
					f := obs.fixture(t, mode, codex.Limits{})
					s := openTurnSession(t, f.c, context.Background())
					_, err := s.Submit(context.Background(), codex.Input{Input: []types.UserInput{}})
					require.NoError(t, err)
					deltas, spawns, bad, child2 := 0, 0, 0, 0
					for _, r := range sessionUntilDone(t, s) {
						require.NotEqual(t, "unrelated-root", r.Source.ThreadID)
						switch r.Event.Method {
						case "item/completed":
							spawns++
							require.Contains(t, string(r.Raw), `"future":null`)
						case "item/agentMessage/delta":
							if r.Event.DecodeError != nil {
								bad++
								require.Contains(t, string(r.Raw), `"itemId":"broken"`)
								break
							}
							deltas++
							require.Equal(t, treeChildRaw, string(r.Raw))
							require.Equal(t, treeChildParams, string(r.Params))
							require.Equal(t, types.EventIdentity{ThreadID: "child-1", TurnID: "child-turn", ItemID: "child-item"}, r.Source)
							require.Equal(t, "[null,9007199254740993]", string(r.Event.Data.(*types.AgentMessageDeltaNotification).UnknownFields["future"]))
						case "future/child":
							child2++
							require.Equal(t, "child-2", r.Source.ThreadID)
							require.Nil(t, r.Event.Data)
						}
					}
					require.Equal(t, 2, spawns, "repeated native facts must survive")
					if mode == "tree-success" {
						require.Equal(t, 2, deltas)
						require.Equal(t, 1, bad)
						require.Equal(t, 1, child2)
					} else {
						require.Zero(t, deltas)
						require.Zero(t, bad)
						require.Zero(t, child2)
					}
					null, absent := false, false
					for _, record := range f.c.Unattributed().Records {
						null = null || string(record.Raw) == `{"method":"future/null","params":null}`
						absent = absent || string(record.Raw) == `{"method":"future/absent"}`
					}
					require.True(t, null)
					require.True(t, absent)
				})
			}
		})
	}
}

func (obs *observationSuite) TestSessionV2StartedActivityAndDuplicateFacts() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := obs.fixture(t, "request-normal", codex.Limits{})
			s := openTurnSession(t, f.c, context.Background())
			spawn := treeFrame("item/completed", v2Activity("native-thread-42", "child-1", "v2-spawn", "started"))
			done := treeFrame("turn/completed", `{"threadId":"native-thread-42","turn":{"id":"parent-turn","rootTurnId":"chain-only","items":[],"status":"completed"}}`)
			emitRequests(t, f.c, treeChildRaw, spawn, spawn, done, treeChildRaw, treeFrame("future/done", `{"threadId":"native-thread-42"}`))
			events := sessionUntilDone(t, s)
			require.Len(t, events, 5)
			for _, r := range events[:2] {
				require.Equal(t, spawn, string(r.Raw))
				a := r.Event.Data.(*types.ItemCompletedNotification).Item.SubAgentActivity
				require.Equal(t, "v2-spawn", a.ID)
				require.Equal(t, types.SubAgentActivityKindStarted, a.Kind)
				require.Equal(t, "child-1", a.AgentThreadID)
				require.Equal(t, "/display/only", a.AgentPath)
				require.Equal(t, "[null,9007199254740993]", string(a.UnknownFields["future"]))
			}
			require.Equal(t, done, string(events[2].Raw))
			require.Equal(t, treeChildRaw, string(events[3].Raw))
			require.Equal(t, treeChildRaw, string(f.c.Unattributed().Records[0].Raw))
		})
	}
}

func (obs *observationSuite) TestSessionNativeNestedConflictsKeepFacts() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := obs.fixture(t, "tree-nested", codex.Limits{})
			s := openTurnSession(t, f.c, context.Background())
			_, err := s.Submit(context.Background(), codex.Input{Input: []types.UserInput{}})
			require.NoError(t, err)
			conflicts, cycles, unknown, activities, delta, terminals := 0, 0, 0, 0, 0, 0
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			for {
				r, err := s.Receive(ctx)
				require.NoError(t, err)
				if o := r.Observation; o != nil {
					if strings.Contains(o.Reason, "conflict") {
						conflicts++
					}
					if strings.Contains(o.Reason, "cycle") {
						cycles++
					}
					continue
				}
				require.NotNil(t, r.Event)
				switch r.Event.Method {
				case "future/grand":
					t.Fatal("quarantined grandchild was backfilled")
				case "item/agentMessage/delta":
					delta++
					require.Equal(t, treeChildRaw, string(r.Raw))
				case "item/completed":
					if strings.Contains(string(r.Raw), "futureItem") {
						unknown++
						require.Contains(t, string(r.Raw), `"payload":null`)
					} else {
						terminals++
					}
				case "item/started":
					if r.Source.ThreadID == "grand" {
						activities++
						require.Contains(t, string(r.Raw), "future-kind")
					}
				}
				if r.Event.Method == "future/done" {
					break
				}
			}
			require.Equal(t, 1, conflicts)
			require.Equal(t, 1, cycles)
			require.Equal(t, 2, unknown)
			require.Equal(t, 1, activities)
			require.Equal(t, 1, delta)
			require.Equal(t, 6, terminals)
			q := f.c.Unattributed().Records
			require.GreaterOrEqual(t, len(q), 5)
			require.Contains(t, string(q[0].Raw), `"ordinal":1`)
			require.Contains(t, string(q[1].Raw), `"ordinal":2`)
			// Retained first parent wins again after mutation of earlier delivered bytes.
			require.NoError(t, f.c.Call(context.Background(), "tree-conflict", nil, nil))
			rejected := false
			for {
				r, err := s.Receive(ctx)
				require.NoError(t, err)
				if r.Observation != nil {
					rejected = rejected || strings.Contains(r.Observation.Reason, "conflict")
				}
				if r.Event != nil && r.Event.Method == "future/done" {
					break
				}
			}
			require.True(t, rejected)
		})
	}
}

func (obs *observationSuite) TestSessionChildRequestBacklogEpochAndWaitCancellation() {
	t := obs.T()
	for _, transport := range []string{"stdio", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			for _, terminal := range []string{"cancel", "resolved", "close"} {
				t.Run(terminal, func(t *testing.T) {
					f := obs.fixture(t, "request-normal", codex.Limits{})
					s := openTurnSession(t, f.c, context.Background())
					emitRequests(t, f.c, treeFrame("thread/started", boundsRelation))
					for _, tc := range []struct{ method, params, result string }{
						{"item/commandExecution/requestApproval", strings.ReplaceAll(approvalParams, "native-thread-42", "child-1"), `{"decision":"decline"}`},
						{"item/tool/requestUserInput", childQuestion, `{"answers":{"q":{"answers":["caller choice"]}}}`},
					} {
						emitRequests(t, f.c, requestFrame(`"child-request"`, tc.method, tc.params))
						r := nextOwnedRequest(t, fixture{c: f.c, session: s})
						require.Equal(t, tc.params, string(r.Params()))
						data, err := r.Data()
						require.NoError(t, err)
						require.NotNil(t, data)
						if q, ok := data.(*types.ToolRequestUserInputParams); ok {
							require.Equal(t, "child-turn", q.TurnID)
							require.Equal(t, "question-item", q.ItemID)
							require.Len(t, q.Questions, 1)
						}
						// Leave a native burst unread while cancellation and original authority work.
						var frames []string
						for range 32 {
							frames = append(frames, treeChildRaw)
						}
						emitRequests(t, f.c, frames...)
						wait, cancel := context.WithCancel(context.Background())
						cancel()
						_, err = s.Receive(wait)
						require.ErrorIs(t, err, context.Canceled)
						require.NoError(t, r.Handle().Err())
						start := make(chan struct{})
						results := make(chan error, 12)
						for range 12 {
							go func() { <-start; results <- r.Handle().Answer(context.Background(), json.RawMessage(tc.result)) }()
						}
						close(start)
						successes := 0
						for range 12 {
							if err := <-results; err == nil {
								successes++
							} else {
								answerError(t, err, r, codex.NotSent, codex.ErrRequestAnswered)
							}
						}
						require.Equal(t, 1, successes)
						for range 32 {
							require.Equal(t, treeChildRaw, string(nextSessionNative(t, s).Raw))
						}
						responseBarrier(t, f)
						answers := answerFrames(t, f.trace)
						require.Equal(t, tc.result, string(answers[len(answers)-1]["result"]))
					}
					emitRequests(t, f.c, requestFrame(`"reused"`, "item/tool/requestUserInput", childQuestion))
					old := nextOwnedRequest(t, fixture{c: f.c, session: s})
					cause := codex.ErrRequestCanceled
					switch terminal {
					case "cancel":
						require.NoError(t, old.Handle().Cancel())
					case "resolved":
						cause = codex.ErrRequestResolved
						emitRequests(t, f.c, treeFrame("serverRequest/resolved", `{"threadId":"child-1","requestId":"reused"}`))
					case "close":
						cause = codex.ErrClosed
						require.NoError(t, s.Close())
					}
					awaitAnswer(t, old.Handle(), cause)
					fresh := startRequestFake(t, "request-normal", codex.Limits{})
					emitRequests(t, fresh.c, requestFrame(`"reused"`, "item/tool/requestUserInput", strings.ReplaceAll(childQuestion, "child-1", "native-thread-42")))
					current := nextOwnedRequest(t, fresh)
					answerError(t, old.Handle().Answer(context.Background(), nil), old, codex.NotSent, cause)
					require.NoError(t, current.Handle().Answer(context.Background(), json.RawMessage(`{"answers":{}}`)))
					responseBarrier(t, fresh)
					require.Len(t, answerFrames(t, fresh.trace), 1)
				})
			}
		})
	}
}

func TestSessionLargeFaultBacklogAndConcurrentAbort(t *testing.T) {
	for _, fault := range []bool{false, true} {
		t.Run(fmt.Sprint(fault), func(t *testing.T) {
			f := startRequestFake(t, "request-normal", codex.Limits{})
			var frames []string
			for i := range 8192 {
				frames = append(frames, treeFrame("future/sequence", fmt.Sprintf(`{"threadId":"native-thread-42","i":%d}`, i)))
			}
			emitRequests(t, f.c, frames...)
			if fault {
				_ = f.c.Call(context.Background(), "turn-malformed-envelope", nil, nil)
				awaitTurnSession(t, f.session, codex.ErrProtocol)
			} else {
				require.NoError(t, f.session.Close())
			}
			for i := range 8192 {
				r := nextSessionNative(t, f.session)
				var p struct {
					I int `json:"i"`
				}
				require.NoError(t, json.Unmarshal(r.Params, &p))
				require.Equal(t, i, p.I)
			}
			_, err := f.session.Receive(context.Background())
			if fault {
				require.ErrorIs(t, err, codex.ErrProtocol)
			} else {
				require.ErrorIs(t, err, io.EOF)
			}
			var wg sync.WaitGroup
			for range 32 {
				wg.Go(func() { _ = f.session.Close(); _ = f.session.Abort(); _ = f.session.Err() })
			}
			wg.Wait()
			_, err = f.session.Receive(context.Background())
			if fault {
				require.ErrorIs(t, err, codex.ErrProtocol)
			} else {
				require.ErrorIs(t, err, io.EOF)
			}
		})
	}
}
