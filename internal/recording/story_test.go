package recording_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	recording "github.com/zoumo/agent-sdk-go/internal/recording"
)

func TestStoryReplayRejectsChangedAndExtraRequests(t *testing.T) {
	frames := []recording.Frame{{Ordinal: 1, Direction: "c2s", Wire: json.RawMessage(`{"id":1,"method":"initialize","params":{}}`)}, {Ordinal: 2, Direction: "s2c", Wire: json.RawMessage(`{"id":1,"result":{}}`)}}
	for _, tc := range []struct {
		name, input string
		bad         bool
	}{{"exact", "{\"params\":{},\"method\":\"initialize\",\"id\":1}\n", false}, {"changed", "{\"id\":1,\"method\":\"execute\"}\n", true}, {"extra", "{\"id\":1,\"method\":\"initialize\",\"params\":{}}\n{}\n", true}} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := recording.ReplayFrames(bytes.NewBufferString(tc.input), &out, frames)
			if tc.bad {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.JSONEq(t, `{"id":1,"result":{}}`, out.String())
			}
		})
	}
}
func TestStoryReservationNeverRenewsOnFailure(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, recording.ReserveStory(dir))
	require.Error(t, recording.ReserveStory(dir))
	st, e := os.Stat(filepath.Join(dir, "attempt.started"))
	require.NoError(t, e)
	require.Equal(t, os.FileMode(0600), st.Mode().Perm())
}

func TestIndependentEnvironmentRefusesHomeChangesAndSymlink(t *testing.T) {
	root := t.TempDir()
	p := recording.StoryPlan{Root: root, Home: filepath.Join(root, "home"), Config: filepath.Join(root, "config"), Provider: "claude"}
	path := filepath.Join(root, "native.env.private.json")
	values := map[string]string{"HOME": "/unrelated/private-home", "TMPDIR": filepath.Join(root, "tmp"), "CLAUDE_CONFIG_DIR": p.Config, "ANTHROPIC_AUTH_TOKEN": "secret-canary"}
	require.NoError(t, recording.WritePrivate(path, values))
	_, err := recording.NativeEnvironment(p)
	require.Error(t, err)
	values["HOME"] = p.Home
	require.NoError(t, recording.WritePrivate(path, values))
	env, err := recording.NativeEnvironment(p)
	require.NoError(t, err)
	require.Contains(t, env, "ANTHROPIC_AUTH_TOKEN=secret-canary")
	require.NoError(t, os.Rename(path, path+".original"))
	require.NoError(t, os.Symlink(path+".original", path))
	_, err = recording.NativeEnvironment(p)
	require.Error(t, err)
}

func TestStoryReplayPreservesLargeIntegerDifferences(t *testing.T) {
	frames := []recording.Frame{{Ordinal: 1, Direction: "c2s", Wire: json.RawMessage(`{"id":9007199254740993,"method":"initialize"}`)}}
	var out bytes.Buffer
	require.Error(t, recording.ReplayFrames(bytes.NewBufferString("{\"id\":9007199254740992,\"method\":\"initialize\"}\n"), &out, frames))
}

func TestStoryArgumentsDoNotEnableUnselectedTools(t *testing.T) {
	args, err := recording.StoryArguments("claude", "SDK-US-001")
	require.NoError(t, err)
	for i, arg := range args {
		if arg == "--tools" {
			require.Empty(t, args[i+1])
		}
	}
	args, err = recording.StoryArguments("claude", "SDK-US-009")
	require.NoError(t, err)
	for i, arg := range args {
		if arg == "--tools" {
			require.Equal(t, "Write", args[i+1])
		}
	}
	_, err = recording.StoryArguments("claude", "SDK-US-999")
	require.Error(t, err)
}

func TestStoryLoadSeparatesReplayFromLiveAuthority(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0700))
	p := recording.StoryPlan{Version: 1, Provider: "claude", Story: "SDK-US-001", Root: root, Home: filepath.Join(root, "home"), Config: filepath.Join(root, "config"), Project: filepath.Join(root, "project"), Raw: filepath.Join(root, "raw"), CLI: "/nonexistent/poison-cli", CLIVersion: "2.1.199"}
	for _, dir := range []string{p.Home, p.Config, p.Project, p.Raw} {
		require.NoError(t, os.Mkdir(dir, 0700))
	}
	path := filepath.Join(root, "plan.json")
	var err error
	p.Args, err = recording.StoryArguments(p.Provider, p.Story)
	require.NoError(t, err)
	require.NoError(t, recording.WritePrivate(path, p))
	t.Setenv("SDK_REAL_AGENT_E2E", "0")
	_, err = recording.LoadStory(path, p.Provider, true)
	require.NoError(t, err, "replay must not open poisoned CLI or absent auth")
	t.Setenv("SDK_REAL_AGENT_E2E", "1")
	t.Setenv("CI", "true")
	_, err = recording.LoadStory(path, p.Provider, false)
	require.ErrorContains(t, err, "CI recording forbidden")
	t.Setenv("CI", "")
	p.Args = append(p.Args, "--resume", "unrelated-private-session")
	require.NoError(t, recording.WritePrivate(path, p))
	_, err = recording.LoadStory(path, p.Provider, false)
	require.ErrorContains(t, err, "unreviewed native")
}

func TestRetiredGoLiveStoryModesRejectBeforeLoadingPlan(t *testing.T) {
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	t.Setenv("SDK_REAL_AGENT_E2E", "1")
	t.Setenv("CI", "")
	for _, provider := range []string{"codex", "claude"} {
		for _, mode := range []string{"--story-plan", "--story-proxy"} {
			t.Run(provider+mode, func(t *testing.T) {
				os.Args = []string{"record", mode, "/nonexistent/private-plan"}
				// Nil callbacks and a nonexistent plan prove this returns before
				// loading credentials or reaching any legacy native opener.
				handled, err := recording.StoryCommand(provider, nil, nil, nil)
				require.True(t, handled)
				require.ErrorContains(t, err, "Go-driven live stories retired")
			})
		}
	}
}
