package agentclaude_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
)

func TestResumeFailureReleasesUnreturnedQueues(t *testing.T) {
	// Isolate the goroutine count from other tests in this package.
	const childEnv = "AGENT_SDK_TEST_RESUME_QUEUE_CLEANUP"
	if os.Getenv(childEnv) != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		exe, err := os.Executable()
		require.NoError(t, err)
		cmd := exec.CommandContext(ctx, exe, "-test.run=^TestResumeFailureReleasesUnreturnedQueues$", "-test.v")
		cmd.Env = append(os.Environ(), childEnv+"=1", "GORACE=atexit_sleep_ms=0")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		return
	}

	for _, test := range []struct {
		name   string
		reason string
		frames func(string) []string
	}{
		{
			name:   "identity rejection",
			reason: agentclaude.ErrorReasonResumeInitDuplicate,
			frames: func(init string) []string { return []string{init, init} },
		},
		{
			name:   "buffered conversion rejection",
			reason: agentclaude.ErrorReasonConflictingToolBlockIdentity,
			frames: func(init string) []string {
				return []string{
					init,
					fmt.Sprintf(`{"type":"assistant","session_id":%q,"message":{"id":"r0","role":"assistant","content":[{"type":"text","text":"buffered text"}]}}`, claudeResumePeerSessionID),
					fmt.Sprintf(`{"type":"stream_event","session_id":%q,"event":{"type":"message_start","message":{"id":"r1"}}}`, claudeResumePeerSessionID),
					resumeToolBlockStart("tool-a"),
					resumeToolBlockStart("tool-b"),
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := resumeQueueWorkers(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd, config := t.TempDir(), newResumeConfigDir(t)
			writeResumeTranscript(t, config, resumeProjectKey(t, cwd), claudeResumePeerSessionID, cwd)
			init := resumeInitFrame(t, claudeResumePeerSessionID, cwd)
			peer := newClaudeResumePeer(t, claudeResumePeerScenario{CWD: cwd, Frames: test.frames(init)})
			core := newResumeProvider(t, peer.CLIPath, peer.Env, config, 3*time.Second)
			for range 3 {
				session, err := core.Resume(ctx, agent.ResumeOptions{
					SessionID:   claudeResumePeerSessionID,
					OpenOptions: agent.OpenOptions{Cwd: cwd},
				})
				require.Nil(t, session)
				var adapterErr *agent.Error
				require.ErrorAs(t, err, &adapterErr)
				require.Equal(t, test.reason, adapterErr.Reason)
			}
			require.NoError(t, core.Stop(ctx))
			require.Equal(t, before, resumeQueueWorkers(t), "rejected resumes must not strand queue workers")
		})
	}
}

func resumeToolBlockStart(id string) string {
	return fmt.Sprintf(`{"type":"stream_event","session_id":%q,"event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":%q,"name":"Read"}}}`, claudeResumePeerSessionID, id)
}

func resumeQueueWorkers(t *testing.T) int {
	t.Helper()
	var stacks bytes.Buffer
	require.NoError(t, pprof.Lookup("goroutine").WriteTo(&stacks, 2))
	return bytes.Count(stacks.Bytes(), []byte("channx.(*ChannX).process("))
}
