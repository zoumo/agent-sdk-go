package recorder_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex/tools/recorder"
)

func TestAggregateAttemptsSurviveReopenAndStartupFailure(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	path := filepath.Join(dir, "attempts.jsonl")
	header := `{"version":1,"authorization":"proposal-not-live-permission","revision":"52b329a77333e55063a97eaf1304c82b30a99a59","startedAt":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","maxRoots":3,"maxTurns":6}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(header), 0600))
	require.NoError(t, recorder.Reserve(path, "r1", "root"))
	// There is deliberately no release/refund API, even when startup failed.
	require.Error(t, recorder.Reserve(path, "r1", "root"))
	require.NoError(t, recorder.Reserve(path, "r1", "turn1"))
	require.NoError(t, recorder.Reserve(path, "r1", "turn2"))
	require.Error(t, recorder.Reserve(path, "r1", "turn2"))
	require.Error(t, recorder.Reserve(path, "r2", "turn1"))
	require.Error(t, recorder.Reserve(path, "s3", "root"))
	require.NoError(t, recorder.Reserve(path, "r2", "root"))
	require.NoError(t, recorder.Reserve(path, "r3", "root"))
	require.Error(t, recorder.Reserve(path, "s1", "root"))
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(b, []byte("broken\n")...), 0600))
	require.Error(t, recorder.Reserve(path, "r2", "turn1"))
}

func TestLedgerRefusesCorruptionExpiryAndUnsafePaths(t *testing.T) {
	dir, e := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, e)
	valid := `{"version":1,"authorization":"synthetic","revision":"52b329a77333e55063a97eaf1304c82b30a99a59","startedAt":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","maxRoots":1,"maxTurns":2}` + "\n"
	for _, tc := range []struct{ name, content string }{
		{"partial", valid[:len(valid)-1]},
		{"unknown", `{"future":true}` + "\n"},
		{"duplicate", valid + `{"scene":"r1","operation":"root"}` + "\n" + `{"scene":"r1","operation":"root"}` + "\n"},
		{"unordered", valid + `{"scene":"r1","operation":"turn1"}` + "\n"},
		{"expired", strings.Replace(valid, time.Now().UTC().Format("2006-01-02"), "2000-01-01", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name)
			require.NoError(t, os.WriteFile(path, []byte(tc.content), 0600))
			require.Error(t, recorder.Reserve(path, "r2", "root"))
		})
	}
	path := filepath.Join(dir, "valid")
	require.NoError(t, os.WriteFile(path, []byte(valid), 0600))
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(path, link))
	require.Error(t, recorder.Reserve(link, "r1", "root"))
	require.Error(t, recorder.Reserve("relative", "r1", "root"))
	require.NoError(t, os.Chmod(path, 0644))
	require.Error(t, recorder.Reserve(path, "r1", "root"))
}

func TestConcurrentAggregateReservationAndDurableStop(t *testing.T) {
	dir, e := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, e)
	path := filepath.Join(dir, "ledger")
	header := `{"version":1,"authorization":"synthetic","revision":"52b329a77333e55063a97eaf1304c82b30a99a59","startedAt":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","maxRoots":3,"maxTurns":6}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(header), 0600))
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { results <- recorder.Reserve(path, "r1", "root") }()
	}
	success := 0
	for i := 0; i < 8; i++ {
		if <-results == nil {
			success++
		}
	}
	require.Equal(t, 1, success)
	require.NoError(t, recorder.Reserve(path, "r1", "stop"))
	require.Error(t, recorder.Reserve(path, "r2", "root"))
}
