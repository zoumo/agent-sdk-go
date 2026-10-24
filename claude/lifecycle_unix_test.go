//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func (s *processLifecycleSuite) TestCloseReapsUncooperativeChild() {
	t := s.T()
	opts := fixtureOptions(t, "ignore_eof")
	capture := filepath.Join(opts.WorkDir, "pid.json")
	opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
	c, err := openTestInteraction(context.Background(), opts)
	require.NoError(t, err)
	data, err := os.ReadFile(capture)
	require.NoError(t, err)
	var process struct{ PID int }
	json.Unmarshal(data, &process)
	start := time.Now()
	{
		err := c.Close()
		require.NoError(t, err)
	}
	require.False(t, time.Since(start) > 3*time.Second, "Close exceeded bound")
	{
		err := syscall.Kill(process.PID, 0)
		require.ErrorIsf(t, err, syscall.ESRCH, "child still exists or is unreaped: pid %d, %v", process.PID, err)
	}
}

func (s *processLifecycleSuite) TestInitializationTimeoutReapsChild() {
	t := s.T()
	opts := fixtureOptions(t, "init_hang")
	opts.InitializeTimeout = 150 * time.Millisecond
	capture := filepath.Join(opts.WorkDir, "pid.json")
	opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
	_, err := openTestInteraction(context.Background(), opts)
	require.ErrorIsf(t, err, context.DeadlineExceeded, "Open = %v", err)
	data, err := os.ReadFile(capture)
	require.NoError(t, err)
	var process struct{ PID int }
	json.Unmarshal(data, &process)
	{
		err := syscall.Kill(process.PID, 0)
		require.ErrorIsf(t, err, syscall.ESRCH, "startup child still exists: pid %d, %v", process.PID, err)
	}
}

// These are actual same-process-group descendants, not mocked Wait or Kill.
func runLifecycleDescendant() {
	ready := os.NewFile(3, "ready")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	if os.Getenv("CLAUDE_GO_CLOSE_OUTPUT") == "1" {
		_ = os.Stdout.Close()
		_ = os.Stderr.Close()
	}
	_, _ = ready.Write([]byte{1})
	_ = ready.Close()
	<-signals
	_ = os.WriteFile(os.Getenv("CLAUDE_GO_TERMINATED"), []byte("SIGTERM received"), 0600)
}

func runLifecycleFixture(mode string, reader *bufio.Reader) {
	if _, err := reader.ReadBytes('\n'); err != nil {
		return
	}
	if mode == "lifecycle_ignore_term" {
		signal.Ignore(syscall.SIGTERM)
		fixtureEmit(map[string]any{"type": "uncooperative_ready", "pid": os.Getpid()})
		_, _ = io.Copy(io.Discard, reader)
		time.Sleep(time.Hour)
		return
	}
	// Keep the parent alive to reap its descendant after group termination in
	// the ordinary case. The pipe-holder case deliberately exits first.
	signal.Ignore(syscall.SIGTERM)
	readyR, readyW, err := os.Pipe()
	if err != nil {
		os.Exit(31)
	}
	exe, _ := os.Executable()
	descendant := exec.Command(exe)
	descendant.Env = []string{"HOME=" + os.Getenv("HOME"), "CLAUDE_GO_FIXTURE=lifecycle_descendant", "CLAUDE_GO_TERMINATED=" + os.Getenv("CLAUDE_GO_TERMINATED"), "GORACE=atexit_sleep_ms=0"}
	if mode == "lifecycle_closed_pipes" {
		descendant.Env = append(descendant.Env, "CLAUDE_GO_CLOSE_OUTPUT=1")
	}
	descendant.Stdout, descendant.Stderr = os.Stdout, os.Stderr
	descendant.ExtraFiles = []*os.File{readyW}
	if err := descendant.Start(); err != nil {
		os.Exit(32)
	}
	_ = readyW.Close()
	var ready [1]byte
	if _, err := io.ReadFull(readyR, ready[:]); err != nil {
		os.Exit(33)
	}
	_ = readyR.Close()
	fixtureEmit(map[string]any{"type": "tree_ready", "pid": os.Getpid(), "descendant": descendant.Process.Pid})
	if mode == "lifecycle_parent_exit" || mode == "lifecycle_closed_pipes" {
		return
	}
	_ = descendant.Wait()
}

func (s *processLifecycleSuite) TestCloseEscalatesToKillForIgnoredSIGTERM() {
	t := s.T()
	c := openFixture(t, "lifecycle_ignore_term")
	send(t, c, "start")
	event := receive(t, c)
	var info struct{ PID int }
	{
		err := json.Unmarshal(event.Raw, &info)
		require.NoError(t, err)
	}
	start := time.Now()
	{
		err := c.Close()
		require.NoError(t, err)
	}
	require.False(t, time.Since(start) > 3*time.Second, "kill escalation was unbounded")
	{
		err := syscall.Kill(info.PID, 0)
		require.ErrorIsf(t, err, syscall.ESRCH, "direct child not reaped: %v", err)
	}
}

func (s *processLifecycleSuite) TestProcessGroupTeardownAndDescendantHoldingStdout() {
	t := s.T()
	ps, err := exec.LookPath("ps")
	require.Nil(t, err, "process-state verification requires ps:", err)
	for _, mode := range []string{"lifecycle_tree", "lifecycle_parent_exit", "lifecycle_closed_pipes"} {
		t.Run(mode, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "terminated")
			c := openFixture(t, mode, func(o *Options) { o.Env = append(o.Env, "CLAUDE_GO_TERMINATED="+marker) })
			send(t, c, "start")
			event := receive(t, c)
			var info struct{ PID, Descendant int }
			{
				err := json.Unmarshal(event.Raw, &info)
				require.NoError(t, err)
			}
			require.Falsef(t, info.PID <= 0 || info.Descendant <= 0, "bad tree: %s", event.Raw)
			t.Cleanup(func() { _ = syscall.Kill(info.Descendant, syscall.SIGKILL) })
			start := time.Now()
			if mode != "lifecycle_tree" {
				awaitDone(t, c)
			}
			err := c.Close()
			if mode != "lifecycle_tree" {
				require.ErrorIsf(t, err, ErrUnexpectedEOF, "unexpected exit hidden: %v", err)
			} else {
				require.NoError(t, err)
			}
			require.False(t, time.Since(start) > 3*time.Second, "descendant pipe kept close blocked")
			{
				err := syscall.Kill(info.PID, 0)
				require.ErrorIsf(t, err, syscall.ESRCH, "direct child unreaped: %v", err)
			}
			if mode != "lifecycle_closed_pipes" {
				{
					data, err := os.ReadFile(marker)
					require.Falsef(t, err != nil || string(data) != "SIGTERM received", "group signal did not reach descendant: %q %v", data, err)
				}
			}
			// Only the direct child can be reaped by this SDK. An orphan descendant's
			// zombie can await the host reaper; it must no longer be alive or hold pipes.
			deadline := time.Now().Add(3 * time.Second)
			for {
				out, err := exec.Command(ps, "-o", "stat=", "-p", strconv.Itoa(info.Descendant)).Output()
				state := strings.TrimSpace(string(out))
				if err == nil && strings.HasPrefix(state, "Z") {
					break
				}
				if errors.Is(syscall.Kill(info.Descendant, 0), syscall.ESRCH) {
					break
				}
				require.Falsef(t, err != nil || time.Now().After(deadline), "descendant still alive: state=%q error=%v", state, err)
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

// Process lifecycle cases retain their platform-specific isolated peers.
type processLifecycleSuite struct{ suite.Suite }

func TestProcessLifecycleSuite(t *testing.T) { suite.Run(t, new(processLifecycleSuite)) }
