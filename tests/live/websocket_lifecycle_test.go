package live_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	codex "github.com/zoumo/codex-sdk-go"
	"github.com/zoumo/codex-sdk-go/protocol"
)

// This opt-in spends at most two turn/start attempts and never retries a write.
// It owns one isolated authenticated loopback server; Close owns only each socket.
// It inherits normal CLI login; no credentials or raw payloads are logged.
func TestWebSocketLifecycleHistory(t *testing.T) {
	if os.Getenv("RUN_MAC_WEBSOCKET_LIVE") != "1" {
		t.Skip("explicit live opt-in required")
	}
	executable := os.Getenv("MAC_LIVE_CODEX")
	if executable == "" {
		t.Fatal("MAC_LIVE_CODEX must select an explicit executable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cwd := t.TempDir()
	// Token material is separate from the model cwd and never logged.
	tokenDir := t.TempDir()
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		t.Fatal("random token generation failed")
	}
	token := hex.EncodeToString(tokenBytes)
	tokenPath := filepath.Join(tokenDir, "capability-token")
	if err := os.WriteFile(tokenPath, []byte(token), 0600); err != nil {
		t.Fatal("temporary token creation failed")
	}
	if st, err := os.Stat(tokenPath); err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("token permissions mismatch")
	}
	reservation, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("loopback reservation failed: %T", err)
	}
	address := reservation.Addr().String()
	_ = reservation.Close()
	endpoint := "ws://" + address
	server := exec.Command(executable, "app-server", "--listen", endpoint, "--ws-auth", "capability-token", "--ws-token-file", tokenPath)
	server.Dir = cwd
	server.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	server.Stdout, server.Stderr = io.Discard, io.Discard
	if err := server.Start(); err != nil {
		t.Fatalf("owned server start failed: %T", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- server.Wait() }()
	t.Cleanup(func() {
		_ = syscall.Kill(-server.Process.Pid, syscall.SIGTERM)
		select {
		case <-exited:
			t.Log("owned server exited and reaped after SIGTERM")
		case <-time.After(3 * time.Second):
			_ = syscall.Kill(-server.Process.Pid, syscall.SIGKILL)
			select {
			case <-exited:
				t.Log("owned server killed and reaped after termination budget")
			case <-time.After(2 * time.Second):
				t.Error("owned server reaping exceeded bound")
			}
		}
		groupDeadline := time.Now().Add(2 * time.Second)
		for syscall.Kill(-server.Process.Pid, 0) == nil && time.Now().Before(groupDeadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if syscall.Kill(-server.Process.Pid, 0) != syscall.ESRCH {
			_ = syscall.Kill(-server.Process.Pid, syscall.SIGKILL)
			t.Error("owned process group remained after graceful cleanup")
		} else {
			t.Log("owned process group absent after cleanup")
		}
		if err := os.Remove(tokenPath); err != nil {
			t.Error("temporary token removal failed")
		}
		if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
			t.Error("temporary token still present")
		}
		t.Log("process-only test credential removed; no persistent auth configuration")
	})
	readyDeadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case e := <-exited:
			exited <- e
			t.Fatal("owned server exited before readiness; stderr withheld")
		default:
		}
		conn, e := net.DialTimeout("tcp4", address, 100*time.Millisecond)
		if e == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(readyDeadline) {
			t.Fatal("owned loopback listener readiness timeout")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Verify that this exact child owns only the requested loopback listener.
	// Inspect only this launch tree, with PID/PPID and no argv/environment.
	processRows, err := exec.Command("/bin/ps", "-axo", "pid=,ppid=").Output()
	if err != nil {
		t.Fatal("owned process tree inspection failed")
	}
	owned := map[int]bool{server.Process.Pid: true}
	for changed := true; changed; {
		changed = false
		for _, row := range strings.Split(string(processRows), "\n") {
			fields := strings.Fields(row)
			if len(fields) != 2 {
				continue
			}
			pid, _ := strconv.Atoi(fields[0])
			parent, _ := strconv.Atoi(fields[1])
			if owned[parent] && !owned[pid] {
				owned[pid] = true
				changed = true
			}
		}
	}
	listeners := 0
	for pid := range owned {
		listening, e := exec.Command("/usr/sbin/lsof", "-nP", "-a", "-p", strconv.Itoa(pid), "-iTCP", "-sTCP:LISTEN", "-Fn").Output()
		if e != nil {
			continue
		}
		for _, line := range strings.Split(string(listening), "\n") {
			if strings.HasPrefix(line, "n") {
				listeners++
				if line != "n"+address {
					t.Fatal("owned listener is not exact requested loopback endpoint")
				}
			}
		}
	}
	if listeners != 1 {
		t.Fatal("owned process tree listener count mismatch")
	}
	t.Log("owned child has exactly one TCP listener on 127.0.0.1; temporary token mode 0600")
	badCtx, badCancel := context.WithTimeout(ctx, 5*time.Second)
	bad, badErr := codex.NewRemote(badCtx, codex.RemoteOptions{URL: endpoint, BearerToken: "invalid-ticket03-token", Initialize: protocol.InitializeParams{ClientInfo: protocol.ClientInfo{Name: "sdk-mac-live-ticket03-rejected", Version: "1"}}})
	badCancel()
	if bad != nil {
		_ = bad.Close()
		t.Fatal("incorrect credential unexpectedly accepted")
	}
	if !errors.Is(badErr, codex.ErrRemoteTransport) || !strings.Contains(badErr.Error(), "HTTP status 401") {
		t.Fatalf("expected auth rejection HTTP 401, got error type %T", badErr)
	}
	t.Log("incorrect temporary credential rejected with HTTP 401; no model turn dispatched")
	var attempts, unexpected atomic.Int32
	defer func() {
		t.Logf("turn_start_attempts=%d remaining_global_budget=%d unexpected_requests=%d", attempts.Load(), 2-attempts.Load(), unexpected.Load())
	}()
	open := func() *codex.Client {
		initCtx, stop := context.WithTimeout(ctx, 60*time.Second)
		defer stop()
		c, err := codex.NewRemote(initCtx, codex.RemoteOptions{URL: endpoint, BearerToken: token,
			Initialize: protocol.InitializeParams{ClientInfo: protocol.ClientInfo{Name: "sdk-mac-live-ticket03", Version: "1"}}})
		if err != nil {
			t.Fatalf("initialize failed (no raw error logged): %T", err)
		}
		if c.ServerInfo().UserAgent == "" {
			t.Fatal("server user agent absent")
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
			t.Logf("Close succeeded; Done closed; ErrClosed; remote socket closed without server ownership; elapsed_ms=%d", time.Since(start).Milliseconds())
		case <-time.After(5 * time.Second):
			t.Fatal("Close exceeded 5 second observation window")
		}
	}
	history := func(c *codex.Client, id string, turnIDs []string) {
		r, err := c.ReadThread(ctx, protocol.ThreadReadParams{ThreadID: id, IncludeTurns: protocol.Some(true)})
		if err != nil {
			t.Fatalf("thread/read failed: %T", err)
		}
		if r.Thread.ID != id || len(r.Thread.Turns) != len(turnIDs) {
			t.Fatal("history identity/count mismatch")
		}
		for i, turn := range r.Thread.Turns {
			if turn.ID != turnIDs[i] || turn.Status != protocol.TurnStatusCompleted {
				t.Fatal("history turn identity/status mismatch")
			}
			found := false
			for _, item := range turn.Items {
				if item.AgentMessage != nil && strings.TrimSpace(item.AgentMessage.Text) == "SDK_MAC_WS_OK" {
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
		s, err := c.Subscribe(ctx)
		if err != nil {
			t.Fatal("Subscribe failed")
		}
		defer s.Close()
		attempts.Add(1)
		started, err := c.StartTurn(ctx, protocol.TurnStartParams{ThreadID: id, Input: []protocol.UserInput{{Text: protocol.Ptr(protocol.TextUserInput{Text: prompt})}}})
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
				_, _ = c.InterruptTurn(interruptCtx, protocol.TurnInterruptParams{ThreadID: id, TurnID: started.Turn.ID})
				stop()
				t.Fatal("turn observation timeout; one explicit interrupt attempted, no replay")
			case <-s.Done():
				t.Fatalf("subscription ended early: %T", s.Err())
			case e, ok := <-s.Events():
				if !ok || s.Err() != nil {
					t.Fatal("event stream ended/lost")
				}
				if e.DecodeError != nil {
					t.Fatalf("event decode failure method=%s", e.Method)
				}
				switch v := e.Data.(type) {
				case *protocol.TurnStartedNotification:
					if v.ThreadID == id && v.Turn.ID == started.Turn.ID {
						turnStarted = true
						t.Log("matched turn/started")
					}
				case *protocol.AgentMessageDeltaNotification:
					if v.ThreadID == id && v.TurnID == started.Turn.ID {
						delta.WriteString(v.Delta)
					}
				case *protocol.ItemCompletedNotification:
					if v.ThreadID == id && v.TurnID == started.Turn.ID && v.Item.AgentMessage != nil {
						agent = strings.TrimSpace(v.Item.AgentMessage.Text) == "SDK_MAC_WS_OK"
						t.Log("matched item/completed agentMessage")
					}
				case *protocol.ErrorNotification:
					t.Fatal("native error notification received; raw payload withheld")
				case *protocol.TurnCompletedNotification:
					if v.ThreadID == id && v.Turn.ID == started.Turn.ID {
						if v.Turn.Status != protocol.TurnStatusCompleted || !turnStarted || !agent || strings.TrimSpace(delta.String()) != "SDK_MAC_WS_OK" || unexpected.Load() != 0 {
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
	thread, err := c.StartThread(ctx, protocol.ThreadStartParams{Cwd: protocol.Some(cwd), Ephemeral: protocol.Some(false), Sandbox: protocol.Some(protocol.SandboxModeReadOnly)})
	if err != nil {
		t.Fatalf("thread/start failed; no replay: %T", err)
	}
	id := thread.Thread.ID
	if id == "" {
		t.Fatal("empty thread ID")
	}
	t.Log("thread/start persistent native thread A created in empty temporary cwd; read-only sandbox")
	first := observe(c, id, "Do not use tools or access files. Reply with exactly SDK_MAC_WS_OK and nothing else.")
	history(c, id, []string{first})
	closeClient(c)
	select {
	case e := <-exited:
		exited <- e
		t.Fatal("client Close terminated independently owned server")
	default:
	}
	t.Log("owned server still running after first remote Close")
	c = open()
	t.Log("same owned listener accepted fresh authenticated initialize after first Close")
	history(c, id, []string{first})
	resumed, err := c.ResumeThread(ctx, protocol.ThreadResumeParams{ThreadID: id})
	if err != nil {
		t.Fatalf("thread/resume failed; no replacement or replay: %T", err)
	}
	if resumed.Thread.ID != id {
		t.Fatal("resume identity mismatch")
	}
	t.Log("new connection thread/resume returned same native thread A")
	second := observe(c, id, "Do not use tools or access files. Return exactly the marker from your previous assistant reply, with no additional text.")
	history(c, id, []string{first, second})
	detached, err := c.UnsubscribeThread(ctx, protocol.ThreadUnsubscribeParams{ThreadID: id})
	if err != nil {
		t.Fatalf("native unsubscribe failed: %T", err)
	}
	if detached.Status != protocol.ThreadUnsubscribeStatusUnsubscribed {
		t.Fatal("native unsubscribe unexpected status")
	}
	t.Log("thread/unsubscribe status=unsubscribed")
	closeClient(c)
	select {
	case e := <-exited:
		exited <- e
		t.Fatal("second Close terminated owned server")
	default:
	}
	probe, err := net.DialTimeout("tcp4", address, time.Second)
	if err != nil {
		t.Fatal("owned listener unavailable after second remote Close")
	}
	_ = probe.Close()
	t.Log("owned loopback listener remains connectable after second remote Close")
}
