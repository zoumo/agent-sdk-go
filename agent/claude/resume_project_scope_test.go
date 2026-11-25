package agentclaude_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/client/claude"
)

func TestProviderResumeIgnoresUnrelatedProjectEntry(t *testing.T) {
	for _, phase := range []string{"preflight", "postcheck"} {
		for _, identity := range []string{"init", "no init"} {
			t.Run(phase+"/"+identity, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				cwd := t.TempDir()
				configDir := newResumeConfigDir(t)
				transcript := writeResumeTranscript(t, configDir, resumeProjectKey(t, cwd), claudeResumePeerSessionID, cwd)
				entry := filepath.Join(configDir, "projects", ".DS_Store")
				mutation := ""
				if phase == "preflight" {
					require.NoError(t, os.WriteFile(entry, []byte("unrelated project entry"), 0o600))
				} else {
					require.NoFileExists(t, entry)
					mutation = "unrelated-project-entry"
				}
				var frames []string
				if identity == "init" {
					frames = []string{resumeInitFrame(t, claudeResumePeerSessionID, cwd)}
				}
				peer := newClaudeResumePeer(t, claudeResumePeerScenario{
					CWD: cwd, TranscriptPath: transcript, Mutation: mutation, Frames: frames,
				})
				provider := newResumeProvider(t, peer.CLIPath, peer.Env, configDir, time.Second)

				session, err := provider.Resume(ctx, agent.ResumeOptions{
					SessionID: claudeResumePeerSessionID, OpenOptions: agent.OpenOptions{Cwd: cwd},
				})
				require.NoError(t, err)
				closeSessionOnCleanup(t, session)
				assert.Equal(t, claudeResumePeerSessionID, session.ID())
				require.FileExists(t, peer.SessionMark)
				data, err := os.ReadFile(entry)
				require.NoError(t, err)
				assert.Equal(t, "unrelated project entry", string(data))

				unscoped, err := claude.GetSessionInfo(ctx, session.ID(), claude.SessionInfoOptions{
					Scope: claude.SessionQueryScope{ConfigDir: configDir},
				})
				require.ErrorContains(t, err, "non-directory project entry")
				assert.Equal(t, "unknown", unscoped.Coverage.Status)
				scoped, err := claude.GetSessionInfo(ctx, session.ID(), claude.SessionInfoOptions{
					Scope: claude.SessionQueryScope{ConfigDir: configDir, ProjectDir: cwd},
				})
				require.NoError(t, err)
				require.NotNil(t, scoped.Info)
				assert.Equal(t, "complete", scoped.Coverage.Status)
				assert.Equal(t, transcript, scoped.Info.Path)
			})
		}
	}
}

func resumeProjectKey(t *testing.T, cwd string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(cwd)
	require.NoError(t, err)
	return regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(canonical, "-")
}

func TestProviderResumeSelectsRequestedProjectWithDuplicateIDElsewhere(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	configDir := newResumeConfigDir(t)
	transcript := writeResumeTranscript(t, configDir, resumeProjectKey(t, cwd), claudeResumePeerSessionID, cwd)
	writeResumeTranscript(t, configDir, "other-project", claudeResumePeerSessionID, t.TempDir())
	_, err := claude.GetSessionInfo(ctx, claudeResumePeerSessionID, claude.SessionInfoOptions{
		Scope: claude.SessionQueryScope{ConfigDir: configDir},
	})
	require.ErrorContains(t, err, "session found in multiple projects")
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{
		CWD: cwd, TranscriptPath: transcript, Frames: []string{resumeInitFrame(t, claudeResumePeerSessionID, cwd)},
	})
	provider := newResumeProvider(t, peer.CLIPath, peer.Env, configDir, time.Second)
	session, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID: claudeResumePeerSessionID, OpenOptions: agent.OpenOptions{Cwd: cwd},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	assert.Equal(t, claudeResumePeerSessionID, session.ID())
}
