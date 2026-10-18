package live_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// This test spends at most two turn/start attempts and never retries a write.
// It inherits normal CLI login; no credentials or raw payloads are logged.
func TestStdioLifecycleHistory(t *testing.T) {
	if os.Getenv("RUN_MAC_STDIO_LIVE") != "1" {
		t.Skip("live scenario skipped: set RUN_MAC_STDIO_LIVE=1 and MAC_LIVE_CODEX to opt in; offline pass is not live evidence")
	}
	executable := os.Getenv("MAC_LIVE_CODEX")
	if executable == "" {
		t.Fatal("MAC_LIVE_CODEX must select an explicit executable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cwd := t.TempDir()
	var attempts, unexpected atomic.Int32
	defer func() {
		t.Logf("turn_start_attempts=%d remaining_global_budget=%d unexpected_requests=%d", attempts.Load(), 9-attempts.Load(), unexpected.Load())
	}()
	open := func() *codex.Client {
		initCtx, stop := context.WithTimeout(ctx, 60*time.Second)
		defer stop()
		c, err := codex.NewStdio(initCtx, codex.StdioOptions{Executable: executable, Args: []string{"app-server"}, Dir: cwd,
			Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "sdk-mac-live-ticket01", Version: "1"}}})
		if err != nil {
			t.Fatalf("initialize failed (no raw error logged): %T", err)
		}
		t.Log("initialize response and initialized write succeeded; server user agent present:", c.ServerInfo().UserAgent != "")
		t.Cleanup(func() { _ = c.Close() })
		go func() {
			for r := range c.Requests() {
				unexpected.Add(1)
				rejectCtx, stop := context.WithTimeout(ctx, 30*time.Second)
				_ = r.Handle().Reject(rejectCtx, &codex.RPCError{Code: -32601, Message: "unexpected interaction in text-only validation"})
				stop()
			}
		}()
		return c
	}
	closeClient := func(c *codex.Client) {
		start := time.Now()
		done := make(chan error, 1)
		go func() { done <- c.Close() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("client Close failed: %T", err)
			}
			select {
			case <-c.Done():
			default:
				t.Fatal("client Done open after Close")
			}
			if !errors.Is(c.Err(), codex.ErrClosed) {
				t.Fatal("unexpected client terminal cause")
			}
			t.Logf("Close succeeded; Done closed; ErrClosed; owned child reaped by public Close contract; elapsed_ms=%d", time.Since(start).Milliseconds())
		case <-time.After(5 * time.Second):
			t.Fatal("Close exceeded 5 second observation window")
		}
	}
	history := func(c *codex.Client, id string, turnIDs []string) {
		r, err := c.ReadThread(ctx, types.ThreadReadParams{ThreadID: id, IncludeTurns: types.Some(true)})
		if err != nil {
			t.Fatalf("thread/read failed: %T", err)
		}
		if r.Thread.ID != id || len(r.Thread.Turns) != len(turnIDs) {
			t.Fatal("history identity/count mismatch")
		}
		for i, turn := range r.Thread.Turns {
			if turn.ID != turnIDs[i] || turn.Status != types.TurnStatusCompleted {
				t.Fatal("history turn identity/status mismatch")
			}
			found := false
			for _, item := range turn.Items {
				if item.AgentMessage != nil && strings.TrimSpace(item.AgentMessage.Text) == "SDK_MAC_LIVE_OK" {
					found = true
				}
			}
			if !found {
				t.Fatal("history missing synthetic agent marker")
			}
		}
		t.Logf("thread/read same native thread and %d completed native turn IDs; synthetic agent marker present in each", len(turnIDs))
	}
	observe := func(c *codex.Client, id, prompt string) string {
		s, err := c.Subscribe(ctx, id, types.SubscriptionOptions{})
		if err != nil {
			t.Fatal("Subscribe failed")
		}
		defer s.Close()
		attempts.Add(1)
		started, err := c.StartTurn(ctx, types.TurnStartParams{ThreadID: id, Input: []types.UserInput{{Text: types.Ptr(types.TextUserInput{Text: prompt})}}})
		if err != nil {
			var ce *codex.CallError
			if errors.As(err, &ce) {
				t.Logf("turn/start dispatch=%s", ce.Dispatch)
			}
			t.Fatalf("turn/start failed; no replay: %T", err)
		}
		if started.Turn.ID == "" {
			t.Fatal("empty native turn ID")
		}
		t.Logf("turn/start responded; native turn T%d recorded in memory", attempts.Load())
		timer := time.NewTimer(120 * time.Second)
		defer timer.Stop()
		var delta strings.Builder
		agent, turnStarted := false, false
		for {
			select {
			case <-timer.C:
				interruptCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
				_, _ = c.InterruptTurn(interruptCtx, types.TurnInterruptParams{ThreadID: id, TurnID: started.Turn.ID})
				stop()
				t.Fatal("turn observation timeout; one explicit interrupt attempted, no replay")
			case <-s.Done():
				t.Fatalf("subscription ended early: %T", s.Err())
			case e, ok := <-s.Events():
				if !ok || s.Err() != nil {
					t.Fatal("event stream ended/lost")
				}
				if e.Raw == nil {
					continue
				}
				native := e
				if native.DecodeError != nil {
					t.Fatalf("event decode failure method=%s", native.Method)
				}
				switch v := native.Data.(type) {
				case *types.TurnStartedNotification:
					if v.ThreadID == id && v.Turn.ID == started.Turn.ID {
						turnStarted = true
						t.Log("matched turn/started")
					}
				case *types.AgentMessageDeltaNotification:
					if v.ThreadID == id && v.TurnID == started.Turn.ID {
						delta.WriteString(v.Delta)
					}
				case *types.ItemCompletedNotification:
					if v.ThreadID == id && v.TurnID == started.Turn.ID && v.Item.AgentMessage != nil {
						agent = strings.TrimSpace(v.Item.AgentMessage.Text) == "SDK_MAC_LIVE_OK"
						t.Log("matched item/completed agentMessage")
					}
				case *types.ErrorNotification:
					t.Fatal("native error notification received; raw payload withheld")
				case *types.TurnCompletedNotification:
					if v.ThreadID == id && v.Turn.ID == started.Turn.ID {
						if v.Turn.Status != types.TurnStatusCompleted || !turnStarted || !agent || strings.TrimSpace(delta.String()) != "SDK_MAC_LIVE_OK" || unexpected.Load() != 0 {
							t.Fatal("completion/content/interaction assertion failed")
						}
						if err := s.Close(); err != nil {
							t.Fatal("subscription Close failed")
						}
						select {
						case <-s.Done():
						default:
							t.Fatal("subscription Done open")
						}
						if s.Err() != nil {
							t.Fatal("subscription loss/terminal error")
						}
						t.Log("matched turn/completed completed; delta and agent marker exact; subscription Close/Done/Err passed")
						return started.Turn.ID
					}
				}
			}
		}
	}
	c := open()
	thread, err := c.StartThread(ctx, types.ThreadStartParams{Cwd: types.Some(cwd), Ephemeral: types.Some(false), Sandbox: types.Some(types.SandboxModeReadOnly)})
	if err != nil {
		t.Fatalf("thread/start failed; no replay: %T", err)
	}
	id := thread.Thread.ID
	if id == "" {
		t.Fatal("empty thread ID")
	}
	t.Log("thread/start persistent native thread A created in empty temporary cwd; read-only sandbox")
	first := observe(c, id, "Do not use tools or access files. Reply with exactly SDK_MAC_LIVE_OK and nothing else.")
	history(c, id, []string{first})
	closeClient(c)
	c = open()
	history(c, id, []string{first})
	resumed, err := c.ResumeThread(ctx, types.ThreadResumeParams{ThreadID: id})
	if err != nil {
		t.Fatalf("thread/resume failed; no replacement or replay: %T", err)
	}
	if resumed.Thread.ID != id {
		t.Fatal("resume identity mismatch")
	}
	t.Log("new connection thread/resume returned same native thread A")
	second := observe(c, id, "Do not use tools or access files. Return exactly the marker from your previous assistant reply, with no additional text.")
	history(c, id, []string{first, second})
	detached, err := c.UnsubscribeThread(ctx, types.ThreadUnsubscribeParams{ThreadID: id})
	if err != nil {
		t.Fatalf("native unsubscribe failed: %T", err)
	}
	if detached.Status != types.ThreadUnsubscribeStatusUnsubscribed {
		t.Fatal("native unsubscribe unexpected status")
	}
	t.Log("thread/unsubscribe status=unsubscribed")
	closeClient(c)
}
