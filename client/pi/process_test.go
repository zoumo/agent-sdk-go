package pi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
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
