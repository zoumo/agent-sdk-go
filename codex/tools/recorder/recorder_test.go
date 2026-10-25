package recorder_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex/tools/recorder"
)

func TestProfileRequiresPinnedCleanIsolatedNonsecretInputs(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, e)
	p := recorder.LaunchProfile{Version: 1, Scene: "r1", Model: "synthetic-model", Effort: "high", CLIVersion: "0.162.1", SDKRevision: "52b329a77333e55063a97eaf1304c82b30a99a59", IsolationReview: "synthetic review only"}
	p.CLI = filepath.Join(root, "synthetic-cli")
	require.NoError(t, os.WriteFile(p.CLI, []byte("not a model"), 0700))
	hash := sha256.Sum256([]byte("not a model"))
	p.CLISHA256 = hex.EncodeToString(hash[:])
	p.IsolatedHome = filepath.Join(root, "home")
	p.Project = filepath.Join(root, "project")
	p.RawDir = filepath.Join(root, "raw")
	p.Ledger = filepath.Join(root, "ledger")
	for _, dir := range []string{p.IsolatedHome, p.Project, p.RawDir} {
		require.NoError(t, os.Mkdir(dir, 0700))
	}
	require.NoError(t, os.WriteFile(p.Ledger, []byte("synthetic"), 0600))
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: p.SDKRevision}, {Key: "vcs.modified", Value: "false"}}}
	b, e := json.Marshal(p)
	require.NoError(t, e)
	_, e = recorder.ValidateProfile(bytes.NewReader(b), info)
	require.NoError(t, e)
	for _, name := range []string{"child", "version", "digest", "overlap", "relative", "revision", "missing isolation"} {
		t.Run(name, func(t *testing.T) {
			bad := p
			switch name {
			case "child":
				bad.Scene = "s3"
			case "version":
				bad.CLIVersion = "0.164.0"
			case "digest":
				bad.CLISHA256 = "bad"
			case "overlap":
				bad.Project = bad.IsolatedHome
			case "relative":
				bad.RawDir = "relative"
			case "revision":
				bad.SDKRevision = "unknown"
			case "missing isolation":
				bad.IsolationReview = ""
			}
			b, e := json.Marshal(bad)
			require.NoError(t, e)
			_, e = recorder.ValidateProfile(bytes.NewReader(b), info)
			require.Error(t, e)
		})
	}
	_, e = recorder.ValidateProfile(bytes.NewReader(b), nil)
	require.Error(t, e)
	info.Settings[1].Value = "true"
	_, e = recorder.ValidateProfile(bytes.NewReader(b), info)
	require.Error(t, e)
}
func TestRecordingEntrypointRefusesCIAndProxyBeforeReadingFiles(t *testing.T) {
	old := os.Args
	t.Cleanup(func() { os.Args = old })
	os.Args = []string{"record", "--record", "/does/not/exist"}
	t.Setenv("SDK_REAL_AGENT_E2E", "1")
	t.Setenv("CI", "1")
	require.ErrorContains(t, recorder.Run(), "CI")
	os.Args[1] = "--proxy"
	require.ErrorContains(t, recorder.Run(), "disabled")
}
