package e2e_test

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

func TestWebSocketLifecycleHistory(t *testing.T) {
	if os.Getenv("CODEX_E2E_WEBSOCKET") != "1" {
		t.Skip("real-agent E2E scenario skipped: set CODEX_E2E_WEBSOCKET=1 and CODEX_E2E_CLI to opt in; offline pass is not real-agent evidence")
	}
	require.Empty(t, os.Getenv("CI"), "real-agent E2E tests are forbidden in CI")
	suite.Run(t, new(WebSocketLifecycleHistorySuite))
}

func (s *WebSocketLifecycleHistorySuite) SetupTest() {
	t := s.T()
	if os.Getenv("CODEX_E2E_WEBSOCKET") != "1" {
		t.Skip("real-agent E2E scenario skipped: set CODEX_E2E_WEBSOCKET=1 and CODEX_E2E_CLI to opt in; offline pass is not real-agent evidence")
	}
	s.executable = os.Getenv("CODEX_E2E_CLI")
	require.True(t, filepath.IsAbs(s.executable),
		"CODEX_E2E_CLI must select an absolute executable path")

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
	badCtx, badCancel := context.WithTimeout(ctx, 5*time.Second)
	bad, badErr := codex.NewRemote(badCtx, codex.RemoteOptions{URL: endpoint, BearerToken: "invalid-ticket03-token", Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "sdk-e2e-websocket-rejected", Version: "1"}}})
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
			Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "sdk-e2e-websocket", Version: "1"}}})
		require.True(t, err == nil,
			"initialize failed (no raw error logged): %T", err)
		require.False(t, c.ServerInfo().UserAgent == "",
			"server user agent absent")

		t.Log("initialize response and initialized write succeeded; server user agent present:", c.ServerInfo().UserAgent != "")
		t.Cleanup(func() { _ = c.Close() })

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
				if item.AgentMessage != nil && strings.TrimSpace(item.AgentMessage.Text) == "SDK_E2E_WS_OK" {
					found = true
				}
			}
			require.True(t, found,
				"history missing synthetic agent marker")

		}
		t.Logf("thread/read same native thread and %d completed native turn IDs; synthetic agent marker present in each", len(turnIDs))
	}
	observe := func(session *codex.Session, prompt string) string {
		id := session.ThreadID()
		defer session.Close()

		attempts.Add(1)
		submission, err := session.Submit(ctx, codex.Input{Input: []types.UserInput{{Text: types.Ptr(types.TextUserInput{Text: prompt})}}})
		if err != nil {
			var ce *codex.CallError
			if errors.As(err, &ce) {
				t.Logf("turn/start dispatch=%s", ce.Dispatch)
			}
			require.FailNowf(t, "assertion failed", "turn/start failed; no replay: %T", err)
		}
		require.False(t, submission.Response.Turn.ID == "",
			"empty native turn ID")

		t.Logf("turn/start responded; native turn T%d recorded in memory", attempts.Load())
		observeCtx, stopObserve := context.WithTimeout(ctx, 120*time.Second)
		defer stopObserve()

		var delta strings.Builder
		agent, turnStarted := false, false
		for {
			received, err := session.Receive(observeCtx)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					interruptCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
					_ = session.Interrupt(interruptCtx, codex.InterruptOptions{TurnID: submission.Response.Turn.ID})
					stop()
				}
				require.FailNowf(t, "observation ended", "native stream/timeout failure: %T", err)
			}
			if received.Request != nil {
				unexpected.Add(1)
				rejectCtx, stop := context.WithTimeout(ctx, 30*time.Second)
				_ = received.Request.Handle().Reject(rejectCtx, &codex.RPCError{Code: -32601, Message: "unexpected interaction in text-only validation"})
				stop()
				continue
			}
			if received.Event == nil {
				continue
			}
			e := *received.Event

			native := e
			if native.DecodeError != nil {
				require.FailNowf(t, "assertion failed", "event decode failure method=%s", native.Method)
			}
			switch v := native.Data.(type) {
			case *types.TurnStartedNotification:
				if v.ThreadID == id && v.Turn.ID == submission.Response.Turn.ID {
					turnStarted = true
					t.Log("matched turn/started")
				}
			case *types.AgentMessageDeltaNotification:
				if v.ThreadID == id && v.TurnID == submission.Response.Turn.ID {
					delta.WriteString(v.Delta)
				}
			case *types.ItemCompletedNotification:
				if v.ThreadID == id && v.TurnID == submission.Response.Turn.ID && v.Item.AgentMessage != nil {
					agent = strings.TrimSpace(v.Item.AgentMessage.Text) == "SDK_E2E_WS_OK"
					t.Log("matched item/completed agentMessage")
				}
			case *types.ErrorNotification:
				require.FailNow(t, "native error notification received; raw payload withheld")
			case *types.TurnCompletedNotification:
				if v.ThreadID == id && v.Turn.ID == submission.Response.Turn.ID {
					require.False(t, v.Turn.Status != types.TurnStatusCompleted || !turnStarted || !agent || strings.TrimSpace(delta.String()) != "SDK_E2E_WS_OK" || unexpected.Load() != 0,
						"completion/content/interaction assertion failed")
					{

						err := session.Close()
						require.True(t, err == nil,
							"subscription Close failed")
					}

					select {
					case <-session.Done():
					default:
						require.FailNow(t, "subscription Done open")
					}
					require.Equal(t, nil, session.Err(),
						"subscription loss/terminal error")

					t.Log("matched turn/completed completed; delta and agent marker exact; subscription Close/Done/Err passed")
					return submission.Response.Turn.ID
				}
			}
		}
	}
	c := open()
	session, err := codex.Open(ctx, codex.SessionOptions{Client: c, Thread: types.ThreadStartParams{Cwd: types.Some(cwd), Ephemeral: types.Some(false), Sandbox: types.Some(types.SandboxModeReadOnly)}})
	require.True(t, err == nil,
		"thread/start failed; no replay: %T", err)

	id := session.ThreadID()
	require.False(t, id == "",
		"empty thread ID")

	t.Log("thread/start persistent native thread A created in empty temporary cwd; read-only sandbox")
	first := observe(session, "Do not use tools or access files. Reply with exactly SDK_E2E_WS_OK and nothing else.")
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
	session = loadNativeSession(t, ctx, c, id, cwd)
	require.Equal(t, id, session.LoadState().ObservedIdentity, "resume identity mismatch")
	t.Log("new connection Load confirmed same native main identity")

	second := observe(session, "Do not use tools or access files. Return exactly the marker from your previous assistant reply, with no additional text.")
	history(c, id, []string{first, second})
	detached, err := c.UnsubscribeThread(ctx, types.ThreadUnsubscribeParams{ThreadID: id})
	require.True(t, err == nil,
		"native unsubscribe failed: %T", err)
	require.Contains(t, []types.ThreadUnsubscribeStatus{types.ThreadUnsubscribeStatusNotSubscribed, types.ThreadUnsubscribeStatusNotLoaded}, detached.Status,
		"native unsubscribe unexpected status")

	t.Log("Session.Close released attachment; native readback confirms no subscription")
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
