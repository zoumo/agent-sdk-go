package recorder_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/tools/mock"
	"github.com/zoumo/agent-sdk-go/codex/tools/recorder"
	recording "github.com/zoumo/agent-sdk-go/codex/tools/replay"
)

func init() {
	if len(os.Args) > 1 && os.Args[1] == "--recorder-test-peer" {
		os.Exit(mock.Main(context.Background(), os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
}
func captureProfile(t *testing.T, scene string) recorder.LaunchProfile {
	t.Helper()
	dir, e := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, e)
	p := recorder.LaunchProfile{Scene: scene, SDKRevision: "52b329a77333e55063a97eaf1304c82b30a99a59", Ledger: filepath.Join(dir, "ledger"), Project: filepath.Join(dir, "project"), RawDir: filepath.Join(dir, "raw")}
	require.NoError(t, os.Mkdir(p.Project, 0700))
	require.NoError(t, os.Mkdir(p.RawDir, 0700))
	h := `{"version":1,"authorization":"synthetic-only","revision":"` + p.SDKRevision + `","startedAt":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","maxRoots":3,"maxTurns":6}` + "\n"
	require.NoError(t, os.WriteFile(p.Ledger, []byte(h), 0600))
	return p
}
func TestCaptureReservesBeforeFailedOpenerAndNeverRetries(t *testing.T) {
	p := captureProfile(t, "r1")
	p.Model = "synthetic-model"
	p.Effort = "high"
	calls := 0
	opener := func(ctx context.Context, p recorder.LaunchProfile) (*codex.Client, func() (recording.TransportResult, error), error) {
		calls++
		b, e := os.ReadFile(p.Ledger)
		require.NoError(t, e)
		require.Contains(t, string(b), `"operation":"root"`)
		return nil, func() (recording.TransportResult, error) {
			return recording.TransportResult{Reaped: true, CleanupComplete: true}, nil
		}, errors.New("synthetic startup failure")
	}
	_, err := recorder.CaptureWithPeer(context.Background(), p, opener)
	require.ErrorContains(t, err, "synthetic startup failure")
	require.Equal(t, 1, calls)
	// New output directories cannot reset the same aggregate attempt.
	p.RawDir = filepath.Join(filepath.Dir(p.RawDir), "raw2")
	require.NoError(t, os.Mkdir(p.RawDir, 0700))
	_, err = recorder.CaptureWithPeer(context.Background(), p, opener)
	require.Error(t, err)
	require.Equal(t, 1, calls)
}
func TestOriginalCaptureOrchestrationWithSyntheticPeers(t *testing.T) {
	for _, story := range []string{"s1", "s2"} {
		t.Run(story, func(t *testing.T) { testOriginalCapture(t, story) })
	}
}
func testOriginalCapture(t *testing.T, story string) {
	p := captureProfile(t, story)
	prefix, e := recording.Load(story)
	require.NoError(t, e)
	init, _, _, e := recording.Params(prefix)
	require.NoError(t, e)
	// Reuse only native lifecycle shapes; this constructed scenario is synthetic,
	// never published as a fresh recording or modifying retained original bytes.
	fs := append([]recording.Frame(nil), prefix[:5]...)

	// Original captures may include early thread notifications; use the exact
	// initialize/initialized/thread-start prefix from the synthetic baseline.
	var threadStartIndex int
	for i, f := range fs {
		if f.Direction == "c2s" && strings.Contains(string(f.Wire), `"thread/start"`) {
			threadStartIndex = i
		}
	}
	require.NotZero(t, threadStartIndex)
	var start map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(fs[threadStartIndex].Wire, &start))
	var originalParams map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(start["params"], &originalParams))
	start["params"] = json.RawMessage(`{"cwd":` + string(mustJSONTest(p.Project)) + `,"ephemeral":true,"environments":[],"sandbox":"read-only","runtimeWorkspaceRoots":[` + string(mustJSONTest(p.Project)) + `]}`)
	if tools, ok := originalParams["dynamicTools"]; ok {
		var params map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(start["params"], &params))
		params["dynamicTools"] = tools
		start["params"] = mustJSONTest(params)
	}
	fs[threadStartIndex].Wire = mustJSONTest(start)
	// Use original full S2 frame ordering, replacing only client root creation;
	// private replay handshakes are unnecessary in this public capture driver.
	fs = append(fs, prefix[5:]...)
	// Drop resolution frames to avoid converting a synthetic scheduling race into
	// a provider success claim. They remain covered in strict real replay tests.
	filtered := fs[:0]
	for _, f := range fs {
		if !strings.Contains(string(f.Wire), `"serverRequest/resolved"`) {
			f.Ordinal = len(filtered) + 1
			filtered = append(filtered, f)
		}
	}
	fs = filtered
	scenario := mock.Scenario{Version: 1, Provenance: "synthetic", Frames: fs}
	b, e := json.Marshal(scenario)
	require.NoError(t, e)
	path := filepath.Join(t.TempDir(), "scenario.json")
	require.NoError(t, os.WriteFile(path, b, 0600))
	exe, e := os.Executable()
	require.NoError(t, e)
	opener := func(ctx context.Context, _ recorder.LaunchProfile) (*codex.Client, func() (recording.TransportResult, error), error) {
		c, e := codex.NewStdio(ctx, codex.StdioOptions{Executable: exe, Args: []string{"--recorder-test-peer", "--scenario", path, "app-server", "--listen=stdio://"}, Env: []string{}, Initialize: init})
		return c, func() (recording.TransportResult, error) {
			return recording.TransportResult{Reaped: true, CleanupComplete: true}, c.Close()
		}, e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, e := recorder.CaptureWithPeer(ctx, p, opener)
	require.NoError(t, e)
	require.True(t, out.Original.Completed)
	if story == "s1" {
		require.Equal(t, 2, out.Original.Tools)
	} else {
		require.Equal(t, 1, out.Original.Questions)
	}
	require.Nil(t, out.ProviderRequests)
	require.Nil(t, out.USD)
	b, e = os.ReadFile(p.Ledger)
	require.NoError(t, e)
	require.Contains(t, string(b), `"operation":"turn1"`)
	require.NotContains(t, string(b), `"turn2"`)
}
func mustJSONTest(v any) json.RawMessage {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
