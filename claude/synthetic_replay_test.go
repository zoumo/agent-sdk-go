// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/stretchr/testify/require"
	transcript "github.com/zoumo/agent-sdk-go/claude/tests/replay"
)

func (s *syntheticObservationSuite) TestSyntheticTaskFailureReplay() {
	t := s.T()
	// Poison PATH proves default replay uses its explicit fake executable only.
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "live-started")
	poison := filepath.Join(dir, "claude")
	{
		err := os.WriteFile(poison, []byte("#!/bin/sh\nprintf live > '"+sentinel+"'\nexit 99\n"), 0700)
		require.NoError(t, err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("CLAUDE_E2E", "0")
	exe, err := os.Executable()
	require.NoError(t, err)
	marker := filepath.Join(dir, "consumed")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := Open(ctx, SessionOptions{CLIPath: exe, WorkDir: dir, Env: []string{"AGENT_MOCK_BIN=" + os.Getenv("AGENT_MOCK_BIN"), "HOME=" + dir, "PATH=" + dir, transcript.PeerEnvironment + "=1", "CLAUDE_GO_REPLAY_COMPLETE=" + marker, "GORACE=atexit_sleep_ms=0"}})
	require.NoError(t, err)
	defer client.Abort()
	prompt := transcript.TaskFailurePrompt
	{
		_, err = client.Submit(ctx, Input{Text: prompt})
		require.NoError(t, err)
	}
	nativeCount := 0
	resultSequence := uint64(0)
	idleSequence := uint64(0)
	rawCount := 0
	for idleSequence == 0 {
		record, err := client.Receive(ctx)
		require.NoError(t, err)
		{
			require.True(t, record.Observation == nil || record.Observation.Reason != "created", "error fixture cannot prove creation")
		}
		native := record.Event
		if native == nil {
			continue
		}
		nativeCount++
		if bytes.Contains(transcript.TaskFailureWire, native.Raw) {
			rawCount++
		} else {
			require.FailNow(t, "verification failed", "Raw changed")
		}
		if native.Result != nil {
			require.False(t, !native.Result.IsError || native.Subtype != "success", "lost native error semantics")
			resultSequence = uint64(nativeCount)
			require.True(t, bytes.Contains(native.Raw, []byte(`"terminal_reason":"synthetic_error"`)), "unknown native field missing")
		}
		if native.Subtype == "session_state_changed" && native.System != nil && native.System.State != nil && *native.System.State == "idle" {
			idleSequence = uint64(nativeCount)
		}
	}
	require.Falsef(t, nativeCount != 2 || rawCount != 2 || resultSequence == 0 || idleSequence <= resultSequence, "unexpected observations native=%d result=%d idle=%d", nativeCount, resultSequence, idleSequence)
	{
		err := client.Close()
		require.NoError(t, err)
	}
	{
		data, err := os.ReadFile(marker)
		require.False(t, err != nil || string(data) != "complete\n", "unconsumed fixture", err)
	}
	{
		_, err := os.Stat(sentinel)
		require.True(t, os.IsNotExist(err), "real CLI sentinel started")
	}
}
