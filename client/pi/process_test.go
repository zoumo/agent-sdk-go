package pi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/pi"
)

// A synthetic process peer for exact argv, native identity and orderly EOF.
func TestPiProcessPeer(t *testing.T) {
	if os.Getenv("PI_TEST_PROCESS_PEER") != "1" {
		return
	}
	args := os.Args
	state := map[string]string{"sessionId": "fresh"}
	if i := slices.Index(args, "--session"); i >= 0 && i+1 < len(args) {
		body, err := os.ReadFile(args[i+1])
		if err != nil {
			os.Exit(2)
		}
		var h struct{ ID string }
		if json.Unmarshal(body, &h) != nil {
			os.Exit(2)
		}
		state["sessionId"] = h.ID
		state["sessionFile"] = args[i+1]
	}
	if i := slices.Index(args, "--append-system-prompt"); i >= 0 && i+1 < len(args) {
		text, err := os.ReadFile(args[i+1])
		if err != nil || string(text) != os.Getenv("PI_TEST_PROMPT") {
			os.Exit(3)
		}
	}
	if i := slices.Index(args, "--mode"); i < 0 || i+1 >= len(args) || args[i+1] != "rpc" {
		os.Exit(4)
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var c command
		if json.Unmarshal(sc.Bytes(), &c) != nil {
			os.Exit(5)
		}
		reply := map[string]any{"type": "response", "command": c.Type, "success": true, "id": c.ID, "data": state}
		if err := json.NewEncoder(os.Stdout).Encode(reply); err != nil {
			os.Exit(6)
		}
		if hold := os.Getenv("PI_TEST_STDOUT_HOLD"); hold != "" {
			child := exec.CommandContext(t.Context(), args[0], "-test.run=^TestPiStdoutHolder$", "--", hold)
			child.Stdout = os.Stdout
			if err := child.Start(); err != nil {
				os.Exit(9)
			}
			if _, err := fmt.Fprintln(os.Stdout, `{"type":"agent_end","messages":[]}`); err != nil {
				os.Exit(10)
			}
			os.Exit(7)
		}
	}
	if err := sc.Err(); err != nil {
		os.Exit(7)
	}
	if marker := os.Getenv("PI_TEST_EOF_MARKER"); marker != "" {
		if err := os.WriteFile(marker, []byte("stdin closed"), 0o600); err != nil {
			os.Exit(8)
		}
	}
	os.Exit(0)
}

// TestPiStdoutHolder keeps the inherited pipe open until the test releases it.
func TestPiStdoutHolder(t *testing.T) {
	if os.Getenv("PI_TEST_PROCESS_PEER") != "1" {
		return
	}
	hold := os.Args[len(os.Args)-1]
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(hold); os.IsNotExist(err) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	os.Exit(0)
}

func TestOpenFinishesWhenDescendantHoldsStdout(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	hold := filepath.Join(t.TempDir(), "hold-stdout")
	require.NoError(t, os.WriteFile(hold, nil, 0o600))
	exe, err := os.Executable()
	require.NoError(t, err)
	s, err := pi.Open(ctx, pi.Options{
		Command: exe,
		Args:    []string{"-test.run=^TestPiProcessPeer$", "--"},
		Env:     []string{"PI_TEST_PROCESS_PEER=1", "PI_TEST_STDOUT_HOLD=" + hold, "GORACE=atexit_sleep_ms=0"},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, os.Remove(hold))
		_ = s.CloseAndDiscard()
	})

	start := time.Now()
	select {
	case <-s.Done():
		t.Logf("Session.Done closed after %v", time.Since(start))
	case <-time.After(4 * time.Second):
		t.Fatalf("Session.Done still open after %v with inherited stdout", time.Since(start))
	}
	var exitErr *exec.ExitError
	require.ErrorAs(t, s.Err(), &exitErr)
	assert.Equal(t, 7, exitErr.ExitCode())
	event, err := s.Receive(ctx)
	require.NoError(t, err)
	assert.Equal(t, "agent_end", event.Type)
	assert.JSONEq(t, `{"type":"agent_end","messages":[]}`, string(event.Raw))
	_, err = s.Receive(ctx)
	assert.ErrorIs(t, err, exitErr)
}

func TestOpenAndExactResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	work := t.TempDir()
	marker := filepath.Join(work, "peer-exit")
	exe, err := os.Executable()
	require.NoError(t, err)
	base := pi.Options{Cwd: work, SessionDir: work, Command: exe, Args: []string{"-test.run=^TestPiProcessPeer$", "--"}, Env: []string{"PI_TEST_PROCESS_PEER=1", "PI_TEST_PROMPT=existing.txt", "PI_TEST_EOF_MARKER=" + marker, "GORACE=atexit_sleep_ms=0"}, AdditionalSystemPrompt: "existing.txt"}
	require.NoError(t, os.WriteFile(filepath.Join(work, "existing.txt"), []byte("different file contents"), 0o600))
	openCtx, cancelOpen := context.WithCancel(ctx)
	defer cancelOpen()
	opened, err := pi.Open(openCtx, base)
	require.NoError(t, err)
	t.Cleanup(func() { _ = opened.CloseAndDiscard() })
	cancelOpen()
	state, err := opened.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, "fresh", opened.ID())
	assert.Equal(t, opened.ID(), state.SessionID)
	require.NoError(t, opened.Close())
	assert.FileExists(t, marker)
	path := filepath.Join(work, "prior.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf(`{"type":"session","version":3,"id":"known-id","cwd":%q}`, work)+"\n"), 0o600))
	o := base
	o.ResumeID = "known-id"
	s, err := pi.Open(ctx, o)
	require.NoError(t, err)
	require.Equal(t, "known-id", s.ID())
	require.NoError(t, s.Close())
	o.SessionFile = path
	s, err = pi.Open(ctx, o)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	o.Command = "/not/executed"
	o.ResumeID = "known"
	_, err = pi.Open(ctx, o)
	require.ErrorContains(t, err, "differs")
	o.SessionFile = ""
	_, err = pi.Open(ctx, o)
	require.ErrorIs(t, err, os.ErrNotExist)
	o.ResumeID = "known-id"
	o.Cwd = t.TempDir()
	_, err = pi.Open(ctx, o)
	require.ErrorContains(t, err, "differs from requested cwd")
	o.Cwd = work
	require.NoError(t, os.WriteFile(filepath.Join(work, "duplicate.jsonl"), []byte(fmt.Sprintf(`{"type":"session","id":%q,"cwd":%q}`, o.ResumeID, work)+"\n"), 0o600))
	_, err = pi.Open(ctx, o)
	require.ErrorContains(t, err, "ambiguous")
}
