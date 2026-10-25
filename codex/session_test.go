package codex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// Synthetic protocol peers exercise the public Session seam without a real agent.
func TestSessionOpenSubmitReceiveAndDrain(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	require.Equal(t, "native-thread-42", s.ThreadID())
	for range 2 {
		result := make(chan error, 1)
		go func() {
			_, err := s.Submit(context.Background(), codex.Input{Input: []types.UserInput{}})
			result <- err
		}()
		// First native result is stale, but does not close the Session.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		got, err := s.Receive(ctx)
		cancel()
		require.NoError(t, err)
		require.Equal(t, "turn/completed", got.Event.Method)
		require.NoError(t, f.c.Call(context.Background(), "release-start", nil, nil))
		require.NoError(t, <-result)
		for range 6 {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_, err = s.Receive(ctx)
			cancel()
			require.NoError(t, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.Receive(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, s.Interrupt(context.Background(), codex.InterruptOptions{TurnID: "native-turn-response"}))
	emitRequests(t, f.c, `{"method":"future/live","params":{"threadId":"native-thread-42"},"future":true}`)
	require.NoError(t, s.Close())
	<-s.Done()
	require.NoError(t, s.Err())
	got, err := s.Receive(context.Background())
	require.NoError(t, err)
	require.Contains(t, string(got.Raw), `"future":true`)
	_, err = s.Receive(context.Background())
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, f.c.Call(context.Background(), "echo", json.RawMessage(`{}`), nil))
}

func TestSessionMembershipOrderDuplicatesAndIsolation(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	_, err = codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.ErrorIs(t, err, codex.ErrOwnershipConflict)
	// A completed spawn confirms membership; old quarantined facts stay excluded.
	frames := []string{
		`{"method":"future/early","params":{"threadId":"child"}}`,
		`{"method":"item/completed","params":{"threadId":"native-thread-42","turnId":"t","completedAtMs":1,"item":{"type":"collabAgentToolCall","id":"comm","tool":"spawnAgent","status":"completed","senderThreadId":"native-thread-42","receiverThreadIds":["child"],"agentsStates":{}}}}`,
		`{"method":"future/child","params":{"threadId":"child"},"extra":{"n":9007199254740993}}`,
		`{"method":"turn/completed","params":{"threadId":"native-thread-42","turn":{"id":"t","status":"completed","items":[]}}}`,
		`{"method":"thread/started","params":{"thread":{"id":"child","parentThreadId":"native-thread-42"}}}`,
		`{"method":"item/completed","params":{"threadId":"child","turnId":"t","completedAtMs":8,"item":` + knownItem + `}}`,
		`{"method":"item/completed","params":{"threadId":"child","turnId":"t","completedAtMs":8,"item":` + knownItem + `}}`,
		`{"method":"future/other","params":{"threadId":"other"}}`,
	}
	emitRequests(t, f.c, frames...)
	require.NoError(t, s.Close())
	var methods []string
	for {
		got, err := s.Receive(context.Background())
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if got.Kind == codex.ReceivedEvent {
			methods = append(methods, got.Event.Method)
		}

	}
	require.Equal(t, []string{"item/completed", "future/child", "turn/completed", "thread/started", "item/completed", "item/completed"}, methods)
	diagnostics := f.c.Unattributed()
	require.Len(t, diagnostics.Records, 2)
	require.Contains(t, string(diagnostics.Records[0].Raw), "future/early")
	require.True(t, diagnostics.CoverageGap)
}

func TestSessionFaultCleansUpAndPreservesAcceptedFacts(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c, MaxMembers: 1})
	require.NoError(t, err)
	emitRequests(t, f.c, `{"method":"item/completed","params":{"threadId":"native-thread-42","turnId":"t","completedAtMs":1,"item":{"type":"collabAgentToolCall","id":"comm","tool":"spawnAgent","status":"completed","senderThreadId":"native-thread-42","receiverThreadIds":["child"],"agentsStates":{}}}}`)
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("resource failure did not terminate")
	}
	require.ErrorIs(t, s.Err(), codex.ErrLimit)
	// Automatic cleanup runs without an application Close or Receive.
	waitTraceWithin(t, f.trace, "thread/unsubscribe", time.Second)
	got, err := s.Receive(context.Background())
	require.NoError(t, err)
	require.Equal(t, "item/completed", got.Event.Method)
	_, err = s.Receive(context.Background())
	require.ErrorIs(t, err, codex.ErrLimit)
	require.NoError(t, s.Close())
	require.ErrorIs(t, s.Err(), codex.ErrLimit)
}

// This peer uses pinned native envelopes and sends facts immediately after the
// start response, independent of application scheduling or consumption.
type sessionPeer struct{ starts int }

func (p *sessionPeer) handle(mode string, m fakeEnvelope, send func(json.RawMessage, string)) bool {
	if !strings.HasPrefix(mode, "session-") {
		return false
	}
	switch m.Method {
	case "thread/start":
		p.starts++
		id := fmt.Sprintf("main-%d", p.starts)
		send(m.ID, strings.ReplaceAll(startResult, "native-thread-42", id))
		fmt.Fprintf(os.Stdout, `{"method":"future/opened","params":{"threadId":%q}}`+"\n", id)
		if mode == "session-owned-diagnostic" {
			fmt.Fprintln(os.Stdout, `{"method":"future/unattributed","params":{"threadId":"unowned"},"unknown":true}`)
		}
		if (mode == "session-block-write" || mode == "session-config-block-write" || mode == "session-block-answer" || mode == "session-resolve-answer") && p.starts == 2 {
			if mode == "session-block-answer" || mode == "session-resolve-answer" {
				fmt.Fprintln(os.Stdout, `{"id":"blocked-answer","method":"future/approval","params":{"threadId":"main-1"}}`)
			}
			// Read one byte to prove dispatch, then hold the reader while the large
			// remainder fills the pipe. The release gate is controlled by the test.
			var first [1]byte
			if _, err := io.ReadFull(os.Stdin, first[:]); err != nil {
				os.Exit(91)
			}
			if mode == "session-resolve-answer" {
				fmt.Fprintln(os.Stdout, `{"method":"serverRequest/resolved","params":{"threadId":"main-1","requestId":"blocked-answer"}}`)
			}
			marker := os.Getenv("CODEX_SDK_PID")
			if os.WriteFile(marker+".writing", []byte("started"), 0600) != nil {
				os.Exit(92)
			}
			until := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(marker + ".release"); err == nil {
					break
				}
				if time.Now().After(until) {
					os.Exit(93)
				}
				time.Sleep(time.Millisecond)
			}
			raw := append([]byte(nil), first[:]...)
			for {
				var next [1]byte
				if _, err := io.ReadFull(os.Stdin, next[:]); err != nil {
					os.Exit(94)
				}
				if next[0] == '\n' {
					break
				}
				raw = append(raw, next[0])
			}
			var submitted fakeEnvelope
			if json.Unmarshal(raw, &submitted) != nil {
				os.Exit(95)
			}
			if submitted.Method != "" {
				if submitted.Method == "thread/settings/update" {
					send(submitted.ID, `{}`)
				} else {
					send(submitted.ID, `{"turn":`+nativeTurn+`}`)
				}
			}
		}

	case "request-frames":
		var frames []string
		if json.Unmarshal(m.Params, &frames) != nil {
			os.Exit(89)
		}
		for _, frame := range frames {
			fmt.Fprintln(os.Stdout, frame)
		}
		send(m.ID, `{}`)
	case "thread/settings/update":
		var settings types.ThreadSettingsUpdateParams
		if json.Unmarshal(m.Params, &settings) != nil {
			os.Exit(96)
		}
		switch settings.Model.ValueOr("") {
		case "native-reject":
			fmt.Fprintf(os.Stdout, `{"id":%s,"error":{"code":-32602,"message":"native restriction","future":1}}`+"\n", m.ID)
			return true
		case "native-withhold":
			return true
		}
		send(m.ID, `{}`)
	case "turn/start":
		if mode == "session-wait" {
			return true
		}
		send(m.ID, `{"turn":`+nativeTurn+`}`)
	case "thread/unsubscribe":
		if mode == "session-detach-error" {
			fmt.Fprintf(os.Stdout, `{"id":%s,"error":{"code":-32001,"message":"detach failed"}}`+"\n", m.ID)
		} else {
			send(m.ID, `{"status":"unsubscribed"}`)
		}
	default:
		return false
	}
	return true
}

func TestSessionSharedConnectionIsolationAndPendingClose(t *testing.T) {
	f := startFake(t, "session-wait", codex.Limits{})
	a, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	b, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	for _, s := range []*codex.Session{a, b} {
		got, err := s.Receive(context.Background())
		require.NoError(t, err)
		require.Equal(t, s.ThreadID(), got.Source.ThreadID)
	}
	pending := make(chan error, 1)
	go func() {
		_, err := a.Submit(context.Background(), codex.Input{Input: []types.UserInput{}})
		pending <- err
	}()
	waitTrace(t, f.trace, "turn/start")
	require.NoError(t, a.Close())
	select {
	case err := <-pending:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("close retained a pending submit")
	}
	emitRequests(t, f.c, `{"method":"future/a","params":{"threadId":"main-1"}}`, `{"method":"future/b","params":{"threadId":"main-2"}}`)
	got, err := b.Receive(context.Background())
	require.NoError(t, err)
	require.Equal(t, "future/b", got.Event.Method)
	_, err = a.Receive(context.Background())
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, b.Interrupt(context.Background(), codex.InterruptOptions{TurnID: "explicit-native-turn"}))
	for _, m := range trace(t, f.trace) {
		if m.Method == "turn/interrupt" {
			require.JSONEq(t, `{"threadId":"main-2","turnId":"explicit-native-turn"}`, string(m.Params))
		}
	}
}

func TestSessionSlowConsumerAndConcurrentReceivers(t *testing.T) {
	f := startFake(t, "session-normal", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	_, err = s.Receive(context.Background())
	require.NoError(t, err)
	frames := make([]string, 2048)
	for i := range frames {
		frames[i] = fmt.Sprintf(`{"method":"future/ordered","params":{"threadId":"main-1","sequence":%d}}`, i)
	}
	emitRequests(t, f.c, frames...)
	_, err = s.Submit(context.Background(), codex.Input{Input: []types.UserInput{}})
	require.NoError(t, err)
	require.NoError(t, s.Close())
	for i := range 1024 {
		got, err := s.Receive(context.Background())
		require.NoError(t, err)
		var p struct {
			Sequence int `json:"sequence"`
		}
		require.NoError(t, json.Unmarshal(got.Params, &p))
		require.Equal(t, i, p.Sequence)
	}
	results := make(chan codex.Received, len(frames))
	failures := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			for {
				got, err := s.Receive(context.Background())
				if err == io.EOF {
					return
				}
				if err != nil {
					failures <- err
					return
				}
				results <- got
			}
		})
	}
	workers.Wait()
	close(results)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	seen := make(map[int]bool)
	for got := range results {
		var p struct {
			Sequence int `json:"sequence"`
		}
		require.NoError(t, json.Unmarshal(got.Params, &p))
		require.False(t, seen[p.Sequence])
		seen[p.Sequence] = true
	}
	require.Len(t, seen, len(frames)-1024)
}

func TestSessionQuarantineBoundsExpiryAndCopies(t *testing.T) {
	for _, tc := range []struct {
		name    string
		limits  codex.Limits
		frames  []string
		evicted uint64
	}{
		{"count", codex.Limits{QuarantineRecords: 1}, []string{`{"method":"future/a"}`, `{"method":"future/b"}`}, 1},
		{"bytes", codex.Limits{QuarantineBytes: 25}, []string{`{"method":"future/a"}`, `{"method":"future/b"}`}, 1},
		{"oversize", codex.Limits{QuarantineBytes: 1}, []string{`{"method":"future/a"}`}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.limits.QuarantineRetention = 30 * time.Millisecond
			f := startFake(t, "request-normal", tc.limits)
			emitRequests(t, f.c, tc.frames...)
			d := f.c.Unattributed()
			require.Equal(t, tc.evicted, d.Evicted)
			require.LessOrEqual(t, d.RetainedBytes, d.MaxBytes)
			require.Equal(t, uint64(len(tc.frames)), d.Received)
			if len(d.Records) > 0 {
				d.Records[0].Raw[0] = '!'
				require.Equal(t, byte('{'), f.c.Unattributed().Records[0].Raw[0])
			}
			require.Eventually(t, func() bool { return len(f.c.Unattributed().Records) == 0 }, time.Second, time.Millisecond)
			after := f.c.Unattributed()
			require.Equal(t, uint64(len(tc.frames))-tc.evicted, after.Expired)
			require.True(t, after.CoverageGap)
		})
	}
}

func TestSessionCleanupErrorDoesNotReplaceTermination(t *testing.T) {
	f := startFake(t, "session-detach-error", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	// Open binds at the start response; the peer emits future/opened afterwards.
	// A reader/RPC barrier proves admission before Close, without consuming it.
	emitRequests(t, f.c)
	err = s.Close()
	require.Error(t, err)
	require.Equal(t, err, s.Close())
	require.NoError(t, s.Err())
	got, err := s.Receive(context.Background())
	require.NoError(t, err)
	require.Equal(t, "future/opened", got.Event.Method)
	_, err = s.Receive(context.Background())
	require.ErrorIs(t, err, io.EOF)
}

func TestSessionConnectionFailureDrainsThenReportsCause(t *testing.T) {
	f := startFake(t, "session-normal", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	_ = f.c.Call(context.Background(), "exit", nil, nil)
	<-s.Done()
	cause := s.Err()
	require.Error(t, cause)
	got, err := s.Receive(context.Background())
	require.NoError(t, err)
	require.Equal(t, "future/opened", got.Event.Method)
	_, err = s.Receive(context.Background())
	require.ErrorIs(t, err, cause)
	require.NoError(t, s.Close())
	require.Equal(t, cause, s.Err())
}

func TestSessionRejectsUnknownInputWithoutDispatch(t *testing.T) {
	f := startFake(t, "session-normal", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	_, err = s.Submit(context.Background(), codex.Input{Input: []types.UserInput{{Unknown: json.RawMessage(`{"type":"futureInput","secret":"retain-only"}`)}}})
	require.ErrorIs(t, err, codex.ErrInvalidInput)
	for _, m := range trace(t, f.trace) {
		require.NotEqual(t, "turn/start", m.Method)
	}
}

func TestSessionCommunicationDoesNotGrantMembershipOrParent(t *testing.T) {
	f := startFake(t, "session-normal", codex.Limits{})
	x, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	y, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = x.Close(); _ = y.Close() })
	for _, s := range []*codex.Session{x, y} {
		_, err := s.Receive(context.Background())
		require.NoError(t, err)
	}
	emitRequests(t, f.c,
		`{"method":"item/completed","params":{"threadId":"main-1","turnId":"t","completedAtMs":1,"item":{"type":"collabAgentToolCall","id":"send","tool":"sendInput","status":"completed","senderThreadId":"main-1","receiverThreadIds":["main-2","external"],"agentsStates":{}}}}`,
		`{"method":"future/y","params":{"threadId":"main-2"}}`,
		`{"method":"future/external","params":{"threadId":"external"}}`)
	require.NoError(t, x.Close())
	require.NoError(t, y.Close())
	var events []string
	for {
		got, err := x.Receive(context.Background())
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if got.Event != nil {
			events = append(events, got.Event.Method)
		}
		if got.Observation != nil {
			require.Empty(t, got.Observation.ParentThreadID)
			require.Equal(t, "communication-target-outside-confirmed-scope", got.Observation.Reason)
		}
	}
	require.Equal(t, []string{"item/completed"}, events)
	got, err := y.Receive(context.Background())
	require.NoError(t, err)
	require.Equal(t, "future/y", got.Event.Method)
	_, err = y.Receive(context.Background())
	require.ErrorIs(t, err, io.EOF)
	require.Len(t, f.c.Unattributed().Records, 1)
}

func TestSessionParentDepthBoundsKnowledgeNotMembership(t *testing.T) {
	for _, policy := range []codex.ChildDetailPolicy{"", codex.ChildDetailSuppress} {
		t.Run(string(policy), func(t *testing.T) { testSessionParentDepthBoundsKnowledgeNotMembership(t, policy) })
	}
}

func testSessionParentDepthBoundsKnowledgeNotMembership(t *testing.T, policy codex.ChildDetailPolicy) {
	f := startFake(t, "session-normal", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{ChildDetailPolicy: policy, Client: f.c, MaxParentDepth: 1})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	_, err = s.Receive(context.Background())
	require.NoError(t, err)
	emitRequests(t, f.c,
		`{"method":"thread/started","params":{"thread":{"id":"a","parentThreadId":"main-1"}}}`,
		`{"method":"thread/started","params":{"thread":{"id":"b","parentThreadId":"a"}}}`,
		`{"method":"thread/started","params":{"thread":{"id":"c","parentThreadId":"b"}}}`,
		`{"method":"item/agentMessage/delta","params":{"threadId":"c","turnId":"t","itemId":"i","delta":"confirmed member, unknown parent"}}`,
		`{"method":"future/deep","params":{"threadId":"c"}}`)
	require.NoError(t, s.Close())
	var methods []string
	var gap bool
	for {
		got, err := s.Receive(context.Background())
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if got.Event != nil {
			methods = append(methods, got.Event.Method)
		}
		if got.Observation != nil && strings.Contains(got.Observation.Reason, "depth limit") {
			gap = true
		}
	}
	want := []string{"thread/started", "thread/started", "thread/started"}
	if policy != codex.ChildDetailSuppress {
		want = append(want, "item/agentMessage/delta")
	}
	want = append(want, "future/deep")
	require.Equal(t, want, methods)
	require.True(t, gap)
}

func TestSessionAbortDiscardsAbandonedBacklog(t *testing.T) {
	for _, policy := range []codex.ChildDetailPolicy{"", codex.ChildDetailSuppress} {
		t.Run(string(policy), func(t *testing.T) { testSessionAbortDiscardsAbandonedBacklog(t, policy) })
	}
}

func testSessionAbortDiscardsAbandonedBacklog(t *testing.T, policy codex.ChildDetailPolicy) {
	f := startFake(t, "session-normal", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{ChildDetailPolicy: policy, Client: f.c})
	require.NoError(t, err)
	emitRequests(t, f.c, `{"method":"future/unread","params":{"threadId":"main-1"}}`)
	require.NoError(t, s.Close())
	require.NoError(t, s.Abort())
	require.NoError(t, s.Abort())
	_, err = s.Receive(context.Background())
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, s.Err())
}

func TestSessionOwnedProcessCleanupWithoutConsumption(t *testing.T) {
	for _, policy := range []codex.ChildDetailPolicy{"", codex.ChildDetailSuppress} {
		t.Run(string(policy), func(t *testing.T) { testSessionOwnedProcessCleanupWithoutConsumption(t, policy) })
	}
}

func testSessionOwnedProcessCleanupWithoutConsumption(t *testing.T, policy codex.ChildDetailPolicy) {
	exe, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	pidPath := filepath.Join(dir, "pid")
	options := codex.StdioOptions{Executable: exe, Args: []string{"-test.run=^TestFakeChild$"}, Env: append(os.Environ(), "CODEX_SDK_HELPER=session-owned-diagnostic", "CODEX_SDK_TRACE="+tracePath, "CODEX_SDK_PID="+pidPath), Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "session-test", Version: "1"}}}
	s, err := codex.Open(context.Background(), codex.SessionOptions{ChildDetailPolicy: policy, Stdio: &options})
	require.NoError(t, err)
	// The synthetic turn response is a reader barrier after both opening frames.
	// Keep the FIFO unread until the owned process has been reaped.
	_, err = s.Submit(context.Background(), codex.Input{})
	require.NoError(t, err)
	require.NoError(t, s.Close())
	// Close reaps the process before the accepted event is consumed.
	d := s.Unattributed()
	require.Len(t, d.Records, 1)
	require.True(t, d.CoverageGap)
	require.Positive(t, d.MaxBytes)
	require.Contains(t, string(d.Records[0].Raw), `"unknown":true`)
	got, err := s.Receive(context.Background())
	require.NoError(t, err)
	require.Equal(t, "future/opened", got.Event.Method)
	_, err = s.Receive(context.Background())
	require.ErrorIs(t, err, io.EOF)
	for _, m := range trace(t, tracePath) {
		require.NotEqual(t, "thread/unsubscribe", m.Method)
	}
}

func TestSessionQuarantineRetentionSurvivesConnectionClose(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{QuarantineRetention: 30 * time.Millisecond})
	emitRequests(t, f.c, `{"method":"future/unknown","params":{"threadId":"unowned"}}`)
	require.Len(t, f.c.Unattributed().Records, 1)
	require.NoError(t, f.c.Close())
	require.Eventually(t, func() bool { d := f.c.Unattributed(); return len(d.Records) == 0 && d.Expired == 1 }, time.Second, time.Millisecond)
}

func TestSessionCloseDuringWriteKeepsSharedConnection(t *testing.T) {
	f := startFake(t, "session-block-write", codex.Limits{})
	a, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	b, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.WriteFile(f.pid+".release", nil, 0600); _ = a.Abort(); _ = b.Abort() })
	submit := make(chan error, 1)
	go func() {
		_, err := a.Submit(context.Background(), codex.Input{Input: []types.UserInput{{Text: &types.TextUserInput{Type: "text", Text: strings.Repeat("x", 256<<10)}}}})
		submit <- err
	}()
	require.Eventually(t, func() bool { _, err := os.Stat(f.pid + ".writing"); return err == nil }, time.Second, time.Millisecond)
	closed := make(chan error, 1)
	go func() { closed <- a.Close() }()
	select {
	case err := <-submit:
		var call *codex.CallError
		require.ErrorAs(t, err, &call)
		require.Equal(t, codex.OutcomeUnknown, call.Dispatch)
	case <-time.After(time.Second):
		t.Fatal("session waiter did not wake while write was blocked")
	}
	select {
	case <-b.Done():
		t.Fatal("closing A terminated B during a write")
	default:
	}
	require.NoError(t, os.WriteFile(f.pid+".release", nil, 0600))
	require.NoError(t, <-closed)
	_, err = b.Submit(context.Background(), codex.Input{Input: []types.UserInput{}})
	require.NoError(t, err)
	require.NoError(t, f.c.Err())
}

func TestSessionSiblingCommunicationKeepsConfirmedParents(t *testing.T) {
	f := startFake(t, "session-normal", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	emitRequests(t, f.c,
		`{"method":"thread/started","params":{"thread":{"id":"a","parentThreadId":"main-1"}}}`,
		`{"method":"thread/started","params":{"thread":{"id":"b","parentThreadId":"main-1"}}}`,
		`{"method":"item/completed","params":{"threadId":"a","turnId":"t","completedAtMs":1,"item":{"type":"collabAgentToolCall","id":"send","tool":"sendInput","status":"completed","senderThreadId":"a","receiverThreadIds":["b"],"agentsStates":{}}}}`,
		`{"method":"future/b","params":{"threadId":"b"}}`)
	require.NoError(t, s.Close())
	var parents []string
	var methods []string
	for {
		got, err := s.Receive(context.Background())
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if got.Event != nil {
			methods = append(methods, got.Event.Method)
		}
		if got.Observation != nil && got.Observation.ParentThreadID != "" {
			require.Equal(t, "parent-confirmed", got.Observation.Reason)
			parents = append(parents, got.Observation.ThreadID+":"+got.Observation.ParentThreadID)
		}
	}
	require.Equal(t, []string{"a:main-1", "b:main-1"}, parents)
	require.Equal(t, []string{"future/opened", "thread/started", "thread/started", "item/completed", "future/b"}, methods)
}

// Existing native consumers need the actual startup model/sandbox evidence,
// without issuing a second thread/start or reconstructing it from options.
func TestSessionStartResponseRetainsNativeEvidence(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{})
	session, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	defer session.Close()
	response, ok := session.StartResponse()
	require.True(t, ok)
	require.Equal(t, session.ThreadID(), response.Thread.ID)
	require.Equal(t, "fake", response.Model)
	require.NotNil(t, response.Sandbox.ReadOnly)
	require.JSONEq(t, `{"large":9007199254740993}`, string(response.Thread.UnknownFields["future"]))
	response.Thread.UnknownFields["future"][0] = '['
	response.Model = "changed"
	again, ok := session.StartResponse()
	require.True(t, ok)
	require.Equal(t, "fake", again.Model)
	require.JSONEq(t, `{"large":9007199254740993}`, string(again.Thread.UnknownFields["future"]))
	for _, wire := range trace(t, f.trace) {
		require.NotEqual(t, "thread/read", wire.Method)
	}
}

func TestSessionMalformedUnicodeNeverCreatesMembership(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	defer s.Close()
	emitRequests(t, f.c,
		`{"method":"thread/started","params":{"thread":{"id":"child-�","parentThreadId":"native-thread-42"}}}`,
		`{"method":"future/malformed-source","params":{"threadId":"child-\ud800"}}`,
		`{"method":"thread/started","params":{"thread":{"id":"intruder","parentThreadId":"child-\ud800"}}}`,
		`{"method":"thread/started","params":{"thread":{"id":"spawn-intruder","source":{"subAgent":{"thread_spawn":{"parent_thread_id":"child-\udc00"}}}}}}`,
		`{"method":"thread/started","params":{"thread":{"id":"bad-\ud800","parentThreadId":"native-thread-42"}}}`,
		`{"method":"future/intruder","params":{"threadId":"intruder"}}`,
		`{"method":"future/spawn-intruder","params":{"threadId":"spawn-intruder"}}`,
		`{"method":"future/bad-child","params":{"threadId":"bad-�"}}`,
		`{"method":"future/valid","params":{"threadId":"child-�","turnId":"\ud800","opaque":true}}`,
		`{"method":"future/main","params":{"threadId":"native-thread-42"}}`)
	require.NoError(t, s.Close())
	var methods []string
	for {
		r, e := s.Receive(context.Background())
		if e != nil {
			require.ErrorIs(t, e, io.EOF)
			break
		}
		if r.Event != nil {
			methods = append(methods, r.Event.Method)
			if r.Event.Method == "future/valid" {
				require.Equal(t, "child-�", r.Source.ThreadID)
				require.Empty(t, r.Source.TurnID)
				require.Contains(t, string(r.Raw), `\ud800`)
			}
		}
	}
	require.Equal(t, []string{"thread/started", "future/valid", "future/main"}, methods)
	q := f.c.Unattributed()
	require.Len(t, q.Records, 7)
	require.True(t, q.CoverageGap)
	require.Contains(t, string(q.Records[0].Raw), `\ud800`)
}
