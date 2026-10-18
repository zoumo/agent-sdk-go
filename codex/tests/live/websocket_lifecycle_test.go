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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// This opt-in spends at most two turn/start attempts and never retries a write.
// It owns one isolated authenticated loopback server; Close owns only each socket.
// It inherits normal CLI login; no credentials or raw payloads are logged.
type WebSocketLifecycleHistorySuite struct {
	suite.Suite
	executable string
}

func TestWebSocketLifecycleHistory(t *testing.T) { suite.Run(t, new(WebSocketLifecycleHistorySuite)) }

func (s *WebSocketLifecycleHistorySuite) SetupTest() {
	t := s.T()
	if os.Getenv("RUN_MAC_WEBSOCKET_LIVE") != "1" {
		t.Skip("live scenario skipped: set RUN_MAC_WEBSOCKET_LIVE=1 and MAC_LIVE_CODEX to opt in; offline pass is not live evidence")
	}
	s.executable = os.Getenv("MAC_LIVE_CODEX")
	require.False(t, s.executable == "",
		"MAC_LIVE_CODEX must select an explicit executable")

}

func (s *WebSocketLifecycleHistorySuite) TestLifecycle() {
	t := s.T()
	executable := s.executable
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cwd := t.TempDir()
	// Token material is separate from the model cwd and never logged.
	tokenDir := t.TempDir()
	tokenBytes := make([]byte, 32)
	{
		_, err := rand.Read(tokenBytes)
		require.True(t, err == nil,
			"random token generation failed")
	}

	token := hex.EncodeToString(tokenBytes)
	tokenPath := filepath.Join(tokenDir, "capability-token")
	{
		err := os.WriteFile(tokenPath, []byte(token), 0600)
		require.True(t, err == nil,
			"temporary token creation failed")
	}
	{

		st, err := os.Stat(tokenPath)
		require.False(t, err != nil || st.Mode().Perm() != 0600,
			"token permissions mismatch")
	}

	reservation, err := net.Listen("tcp4", "127.0.0.1:0")
	require.True(t, err == nil,
		"loopback reservation failed: %T", err)

	address := reservation.Addr().String()
	_ = reservation.Close()
	endpoint := "ws://" + address
	server := exec.Command(executable, "app-server", "--listen", endpoint, "--ws-auth", "capability-token", "--ws-token-file", tokenPath)
	server.Dir = cwd
	server.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	server.Stdout, server.Stderr = io.Discard, io.Discard
	{
		err := server.Start()
		require.True(t, err == nil,
			"owned server start failed: %T", err)
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
				assert.Fail(t, "owned server reaping exceeded bound")
			}
		}
		groupDeadline := time.Now().Add(2 * time.Second)
		for syscall.Kill(-server.Process.Pid, 0) == nil && time.Now().Before(groupDeadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if syscall.Kill(-server.Process.Pid, 0) != syscall.ESRCH {
			_ = syscall.Kill(-server.Process.Pid, syscall.SIGKILL)
			assert.Fail(t, "owned process group remained after graceful cleanup")
		} else {
			t.Log("owned process group absent after cleanup")
		}
		{
			err := os.Remove(tokenPath)
			assert.True(t, err == nil,
				"temporary token removal failed")
		}
		{

			_, err := os.Stat(tokenPath)
			assert.True(t, os.IsNotExist(err),
				"temporary token still present")
		}

		t.Log("process-only test credential removed; no persistent auth configuration")
	})
	readyDeadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case e := <-exited:
			exited <- e
			require.FailNow(t, "owned server exited before readiness; stderr withheld")
		default:
		}
		conn, e := net.DialTimeout("tcp4", address, 100*time.Millisecond)
		if e == nil {
			_ = conn.Close()
			break
		}
		require.False(t, time.Now().After(readyDeadline),
			"owned loopback listener readiness timeout")

		time.Sleep(50 * time.Millisecond)
	}
	// Verify that this exact child owns only the requested loopback listener.
	// Inspect only this launch tree, with PID/PPID and no argv/environment.
	processRows, err := exec.Command("/bin/ps", "-axo", "pid=,ppid=").Output()
	require.True(t, err == nil,
		"owned process tree inspection failed")

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
				require.Equal(t, "n"+address, line,
					"owned listener is not exact requested loopback endpoint")

			}
		}
	}
	require.Equal(t, 1, listeners,
		"owned process tree listener count mismatch")

	t.Log("owned child has exactly one TCP listener on 127.0.0.1; temporary token mode 0600")
	badCtx, badCancel := context.WithTimeout(ctx, 5*time.Second)
	bad, badErr := codex.NewRemote(badCtx, codex.RemoteOptions{URL: endpoint, BearerToken: "invalid-ticket03-token", Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "sdk-mac-live-ticket03-rejected", Version: "1"}}})
	badCancel()
	if bad != nil {
		_ = bad.Close()
		require.FailNow(t, "incorrect credential unexpectedly accepted")
	}
	require.False(t, !errors.Is(badErr, codex.ErrRemoteTransport) || !strings.Contains(badErr.Error(), "HTTP status 401"),
		"expected auth rejection HTTP 401, got error type %T", badErr)

	t.Log("incorrect temporary credential rejected with HTTP 401; no model turn dispatched")
	var attempts, unexpected atomic.Int32
	defer func() {
		t.Logf("turn_start_attempts=%d remaining_global_budget=%d unexpected_requests=%d", attempts.Load(), 2-attempts.Load(), unexpected.Load())
	}()
	open := func() *codex.Client {
		initCtx, stop := context.WithTimeout(ctx, 60*time.Second)
		defer stop()
		c, err := codex.NewRemote(initCtx, codex.RemoteOptions{URL: endpoint, BearerToken: token,
			Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "sdk-mac-live-ticket03", Version: "1"}}})
		require.True(t, err == nil,
			"initialize failed (no raw error logged): %T", err)
		require.False(t, c.ServerInfo().UserAgent == "",
			"server user agent absent")

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
				require.FailNowf(t, "assertion failed", "client Close failed: %T", err)
			}
			select {
			case <-c.Done():
			default:
				require.FailNow(t, "client Done open after Close")
			}
			if !errors.Is(c.Err(), codex.ErrClosed) {
				require.FailNow(t, "unexpected client terminal cause")
			}
			t.Logf("Close succeeded; Done closed; ErrClosed; remote socket closed without server ownership; elapsed_ms=%d", time.Since(start).Milliseconds())
		case <-time.After(5 * time.Second):
			require.FailNow(t, "Close exceeded 5 second observation window")
		}
	}
	history := func(c *codex.Client, id string, turnIDs []string) {
		r, err := c.ReadThread(ctx, types.ThreadReadParams{ThreadID: id, IncludeTurns: types.Some(true)})
		require.True(t, err == nil,
			"thread/read failed: %T", err)
		require.False(t, r.Thread.ID != id || len(r.Thread.Turns) != len(turnIDs),
			"history identity/count mismatch")

		for i, turn := range r.Thread.Turns {
			require.False(t, turn.ID != turnIDs[i] || turn.Status != types.TurnStatusCompleted,
				"history turn identity/status mismatch")

			found := false
			for _, item := range turn.Items {
				if item.AgentMessage != nil && strings.TrimSpace(item.AgentMessage.Text) == "SDK_MAC_WS_OK" {
					found = true
				}
			}
			require.True(t, found,
				"history missing synthetic agent marker")

		}
		t.Logf("thread/read same native thread and %d completed native turn IDs; synthetic agent marker present in each", len(turnIDs))
	}
	observe := func(c *codex.Client, id, prompt string) string {
		s, err := c.Subscribe(ctx, id, types.SubscriptionOptions{})
		require.True(t, err == nil,
			"Subscribe failed")

		defer s.Close()
		attempts.Add(1)
		started, err := c.StartTurn(ctx, types.TurnStartParams{ThreadID: id, Input: []types.UserInput{{Text: types.Ptr(types.TextUserInput{Text: prompt})}}})
		if err != nil {
			var ce *codex.CallError
			if errors.As(err, &ce) {
				t.Logf("turn/start dispatch=%s", ce.Dispatch)
			}
			require.FailNowf(t, "assertion failed", "turn/start failed; no replay: %T", err)
		}
		require.False(t, started.Turn.ID == "",
			"empty native turn ID")

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
				require.FailNow(t, "turn observation timeout; one explicit interrupt attempted, no replay")
			case <-s.Done():
				require.FailNowf(t, "assertion failed", "subscription ended early: %T", s.Err())
			case e, ok := <-s.Events():
				if !ok || s.Err() != nil {
					require.FailNow(t, "event stream ended/lost")
				}
				if e.Raw == nil {
					continue
				}
				native := e
				if native.DecodeError != nil {
					require.FailNowf(t, "assertion failed", "event decode failure method=%s", native.Method)
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
						agent = strings.TrimSpace(v.Item.AgentMessage.Text) == "SDK_MAC_WS_OK"
						t.Log("matched item/completed agentMessage")
					}
				case *types.ErrorNotification:
					require.FailNow(t, "native error notification received; raw payload withheld")
				case *types.TurnCompletedNotification:
					if v.ThreadID == id && v.Turn.ID == started.Turn.ID {
						require.False(t, v.Turn.Status != types.TurnStatusCompleted || !turnStarted || !agent || strings.TrimSpace(delta.String()) != "SDK_MAC_WS_OK" || unexpected.Load() != 0,
							"completion/content/interaction assertion failed")
						{

							err := s.Close()
							require.True(t, err == nil,
								"subscription Close failed")
						}

						select {
						case <-s.Done():
						default:
							require.FailNow(t, "subscription Done open")
						}
						require.Equal(t, nil, s.Err(),
							"subscription loss/terminal error")

						t.Log("matched turn/completed completed; delta and agent marker exact; subscription Close/Done/Err passed")
						return started.Turn.ID
					}
				}
			}
		}
	}
	c := open()
	thread, err := c.StartThread(ctx, types.ThreadStartParams{Cwd: types.Some(cwd), Ephemeral: types.Some(false), Sandbox: types.Some(types.SandboxModeReadOnly)})
	require.True(t, err == nil,
		"thread/start failed; no replay: %T", err)

	id := thread.Thread.ID
	require.False(t, id == "",
		"empty thread ID")

	t.Log("thread/start persistent native thread A created in empty temporary cwd; read-only sandbox")
	first := observe(c, id, "Do not use tools or access files. Reply with exactly SDK_MAC_WS_OK and nothing else.")
	history(c, id, []string{first})
	closeClient(c)
	select {
	case e := <-exited:
		exited <- e
		require.FailNow(t, "client Close terminated independently owned server")
	default:
	}
	t.Log("owned server still running after first remote Close")
	c = open()
	t.Log("same owned listener accepted fresh authenticated initialize after first Close")
	history(c, id, []string{first})
	resumed, err := c.ResumeThread(ctx, types.ThreadResumeParams{ThreadID: id})
	require.True(t, err == nil,
		"thread/resume failed; no replacement or replay: %T", err)
	require.Equal(t, id, resumed.Thread.ID,
		"resume identity mismatch")

	t.Log("new connection thread/resume returned same native thread A")
	second := observe(c, id, "Do not use tools or access files. Return exactly the marker from your previous assistant reply, with no additional text.")
	history(c, id, []string{first, second})
	detached, err := c.UnsubscribeThread(ctx, types.ThreadUnsubscribeParams{ThreadID: id})
	require.True(t, err == nil,
		"native unsubscribe failed: %T", err)
	require.Equal(t, types.ThreadUnsubscribeStatusUnsubscribed, detached.Status,
		"native unsubscribe unexpected status")

	t.Log("thread/unsubscribe status=unsubscribed")
	closeClient(c)
	select {
	case e := <-exited:
		exited <- e
		require.FailNow(t, "second Close terminated owned server")
	default:
	}
	probe, err := net.DialTimeout("tcp4", address, time.Second)
	require.True(t, err == nil,
		"owned listener unavailable after second remote Close")

	_ = probe.Close()
	t.Log("owned loopback listener remains connectable after second remote Close")
}
