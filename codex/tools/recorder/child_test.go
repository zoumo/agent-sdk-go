package recorder_test

import (
	"context"
	"fmt"
	codex "github.com/zoumo/agent-sdk-go/codex"
	recording "github.com/zoumo/agent-sdk-go/codex/tools/replay"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex/tools/recorder"
)

func TestChildAttemptBudgetSurvivesFailureCompletionAndReopen(t *testing.T) {
	dir, err := privateTestDir(t)
	require.NoError(t, err)
	path := filepath.Join(dir, "ledger")
	header := fmt.Sprintf(`{"version":2,"authorization":"synthetic-only","revision":"f0e5ddb2ea6bb54d204ef62d21386aafa7d5f2dd","startedAt":%q,"maxRoots":1,"maxTurns":1,"maxChildAttempts":3,"maxChildDepth":2}`+"\n", time.Now().UTC().Format(time.RFC3339Nano))
	require.NoError(t, os.WriteFile(path, []byte(header), 0600))
	require.NoError(t, recorder.Reserve(path, "r1", "root"))
	require.NoError(t, recorder.ReserveChildAttempt(path, "r1", "A", ""))
	// A failed creation remains spent. A sibling and nested attempt are distinct.
	require.NoError(t, recorder.ReserveChildAttempt(path, "r1", "B", ""))
	require.NoError(t, recorder.ReserveChildAttempt(path, "r1", "C", "A"))
	require.Error(t, recorder.ReserveChildAttempt(path, "r1", "A", ""))
	require.Error(t, recorder.ReserveChildAttempt(path, "r1", "D", ""))
	require.Error(t, recorder.ReserveChildAttempt(path, "r1", "deep", "C"))
	require.Error(t, recorder.ReserveChildAttempt(path, "r1", "orphan", "absent"))
	require.NoError(t, recorder.Reserve(path, "r1", "stop"))
	require.Error(t, recorder.ReserveChildAttempt(path, "r1", "after-stop", ""))
}

func TestChildRuntimeRefusesBeforeOpeningPeer(t *testing.T) {
	for _, version := range []string{"0.162.1", "0.164.0"} {
		t.Run(version, func(t *testing.T) {
			p := recorder.LaunchProfile{Scene: "child-nested", CLIVersion: version, Child: &recorder.ChildPolicy{Source: recorder.ChildRuntimeSource, MaxAttempts: 3, MaxDepth: 2, BackgroundSeconds: 10}}
			called := false
			_, err := recorder.CaptureWithPeer(t.Context(), p, func(context.Context, recorder.LaunchProfile) (*codex.Client, func() (recording.TransportResult, error), error) {
				called = true
				return nil, nil, nil
			})
			require.ErrorIs(t, err, recorder.ErrChildRuntimeUnsupported)
			require.False(t, called)
		})
	}
}

func TestChildDepthAndParallelCharges(t *testing.T) {
	dir, err := privateTestDir(t)
	require.NoError(t, err)
	path := filepath.Join(dir, "ledger")
	header := fmt.Sprintf(`{"version":2,"authorization":"synthetic","revision":"f0e5ddb2ea6bb54d204ef62d21386aafa7d5f2dd","startedAt":%q,"maxRoots":1,"maxTurns":1,"maxChildAttempts":9,"maxChildDepth":2}`+"\n", time.Now().UTC().Format(time.RFC3339Nano))
	require.NoError(t, os.WriteFile(path, []byte(header), 0600))
	require.Error(t, recorder.ReserveChildAttempt(path, "r1", "orphan", ""))
	require.NoError(t, recorder.Reserve(path, "r1", "root"))
	require.NoError(t, recorder.ReserveChildAttempt(path, "r1", "A", ""))
	require.NoError(t, recorder.ReserveChildAttempt(path, "r1", "C", "A"))
	require.ErrorContains(t, recorder.ReserveChildAttempt(path, "r1", "deep", "C"), "depth exhausted")
	results := make(chan error, 12)
	for i := 0; i < 12; i++ {
		go func(i int) { results <- recorder.ReserveChildAttempt(path, "r1", fmt.Sprint("parallel-", i), "") }(i)
	}
	for i := 0; i < 12; i++ {
		<-results
	} // Nonblocking journal locks may conservatively refuse contention.
	for i := 0; i < 12; i++ {
		_ = recorder.ReserveChildAttempt(path, "r1", fmt.Sprint("after-completion-", i), "")
	}
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, 9, strings.Count(string(b), `"operation":"child-attempt"`))
	require.ErrorContains(t, recorder.ReserveChildAttempt(path, "r1", "last", ""), "exhausted")
}

func TestLegacyLedgerCannotGrantChildren(t *testing.T) {
	dir, err := privateTestDir(t)
	require.NoError(t, err)
	path := filepath.Join(dir, "ledger")
	header := fmt.Sprintf(`{"version":1,"authorization":"synthetic","revision":"f0e5ddb2ea6bb54d204ef62d21386aafa7d5f2dd","startedAt":%q,"maxRoots":1,"maxTurns":1}`+"\n", time.Now().UTC().Format(time.RFC3339Nano))
	require.NoError(t, os.WriteFile(path, []byte(header), 0600))
	require.NoError(t, recorder.Reserve(path, "r1", "root"))
	require.Error(t, recorder.ReserveChildAttempt(path, "r1", "A", ""))
}
