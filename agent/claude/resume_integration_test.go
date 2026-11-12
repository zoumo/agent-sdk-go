package agentclaude_test

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	claudetypes "github.com/zoumo/agent-sdk-go/claude/types"
)

func TestProviderResumeRequiresExplicitConfigDir(t *testing.T) {
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{})
	for _, configDir := range []string{"", "relative/config"} {
		t.Run(configDir, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			provider := newResumeProvider(
				t,
				peer.CLIPath,
				peer.Env,
				configDir,
				time.Second,
			)

			_, err := provider.Resume(ctx, agent.ResumeOptions{
				SessionID:   claudeResumePeerSessionID,
				OpenOptions: agent.OpenOptions{Cwd: t.TempDir()},
			})
			require.Error(t, err)
			assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))
			require.NoFileExists(t, peer.SessionMark)
		})
	}
}

func TestProviderResumeChecksVersionBeforeSessionLaunch(t *testing.T) {
	tests := []struct {
		name       string
		missingCLI bool
		scenario   claudeResumePeerScenario
		code       agent.ErrorCode
	}{
		{
			name:       "execution failure",
			missingCLI: true,
			code:       agent.ErrorUnavailable,
		},
		{
			name: "unsupported",
			scenario: claudeResumePeerScenario{
				VersionOutput: "2.1.257 (Claude Code)\n",
			},
			code: agent.ErrorUnsupported,
		},
		{
			name: "prerelease is not the reviewed version",
			scenario: claudeResumePeerScenario{
				VersionOutput: "2.1.258-dev (Claude Code)\n",
			},
			code: agent.ErrorUnsupported,
		},
		{
			name: "four-part version is not the reviewed version",
			scenario: claudeResumePeerScenario{
				VersionOutput: "2.1.258.1 (Claude Code)\n",
			},
			code: agent.ErrorUnsupported,
		},
		{
			name: "one unsupported version rejects mixed output",
			scenario: claudeResumePeerScenario{
				VersionOutput: "2.1.258 (Claude Code)\n2.1.257 (Claude Code)\n",
			},
			code: agent.ErrorUnsupported,
		},
		{
			name: "unconfirmed",
			scenario: claudeResumePeerScenario{
				VersionOutput: "Claude Code\n",
			},
			code: agent.ErrorUnsupported,
		},
		{
			name: "version command fails",
			scenario: claudeResumePeerScenario{
				VersionExit: true,
			},
			code: agent.ErrorUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cwd := t.TempDir()
			configDir := newResumeConfigDir(t)
			writeResumeTranscript(t, configDir, "project", claudeResumePeerSessionID, cwd)
			peer := newClaudeResumePeer(t, tt.scenario)
			cliPath := peer.CLIPath
			if tt.missingCLI {
				cliPath = filepath.Join(t.TempDir(), "missing-claude")
			}
			provider := newResumeProvider(
				t,
				cliPath,
				peer.Env,
				configDir,
				time.Second,
			)

			_, err := provider.Resume(ctx, agent.ResumeOptions{
				SessionID:   claudeResumePeerSessionID,
				OpenOptions: agent.OpenOptions{Cwd: cwd},
			})
			require.Error(t, err)
			assert.Equal(t, tt.code, agent.CodeOf(err))
			require.NoFileExists(t, peer.SessionMark)
		})
	}
}

func TestProviderResumeRejectsMetadataBeforeSessionLaunch(t *testing.T) {
	type setup func(t *testing.T, configDir, cwd string)
	tests := []struct {
		name  string
		setup setup
	}{
		{
			name: "missing",
			setup: func(t *testing.T, configDir, _ string) {
				require.NoError(t, os.MkdirAll(filepath.Join(configDir, "projects"), 0o700))
			},
		},
		{
			name: "wrong cwd",
			setup: func(t *testing.T, configDir, _ string) {
				writeResumeTranscript(
					t,
					configDir,
					"project",
					claudeResumePeerSessionID,
					t.TempDir(),
				)
			},
		},
		{
			name: "ambiguous",
			setup: func(t *testing.T, configDir, cwd string) {
				writeResumeTranscript(t, configDir, "project-a", claudeResumePeerSessionID, cwd)
				writeResumeTranscript(t, configDir, "project-b", claudeResumePeerSessionID, cwd)
			},
		},
		{
			name: "corrupt partial",
			setup: func(t *testing.T, configDir, cwd string) {
				writeResumeSource(
					t,
					configDir,
					"project",
					claudeResumePeerSessionID,
					resumeMetadataLine(t, claudeResumePeerSessionID, cwd)+"{bad}\n",
				)
			},
		},
		{
			name: "identity conflict",
			setup: func(t *testing.T, configDir, cwd string) {
				writeResumeSource(
					t,
					configDir,
					"project",
					claudeResumePeerSessionID,
					resumeMetadataLine(t, "22222222-2222-4222-8222-222222222222", cwd),
				)
			},
		},
		{
			name: "nonregular",
			setup: func(t *testing.T, configDir, cwd string) {
				target := writeResumeTranscript(
					t,
					configDir,
					"target-project",
					claudeResumePeerSessionID,
					cwd,
				)
				directory := filepath.Join(configDir, "projects", "project")
				require.NoError(t, os.MkdirAll(directory, 0o700))
				require.NoError(t, os.Symlink(
					target,
					filepath.Join(directory, claudeResumePeerSessionID+".jsonl"),
				))
			},
		},
		{
			name: "inaccessible",
			setup: func(t *testing.T, configDir, cwd string) {
				path := writeResumeTranscript(
					t,
					configDir,
					"project",
					claudeResumePeerSessionID,
					cwd,
				)
				require.NoError(t, os.Chmod(path, 0))
				t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cwd := t.TempDir()
			configDir := newResumeConfigDir(t)
			tt.setup(t, configDir, cwd)
			peer := newClaudeResumePeer(t, claudeResumePeerScenario{
				Frames: []string{resumeInitFrame(t, claudeResumePeerSessionID, cwd)},
			})
			provider := newResumeProvider(
				t,
				peer.CLIPath,
				peer.Env,
				configDir,
				time.Second,
			)

			_, err := provider.Resume(ctx, agent.ResumeOptions{
				SessionID:   claudeResumePeerSessionID,
				OpenOptions: agent.OpenOptions{Cwd: cwd},
			})
			require.Error(t, err)
			assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))
			require.NoFileExists(t, peer.SessionMark)
		})
	}
}

func TestProviderResumeConfirmsInitIdentityAndAllowsGrowth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	configDir := newResumeConfigDir(t)
	transcript := writeResumeTranscript(
		t,
		configDir,
		"project",
		claudeResumePeerSessionID,
		cwd,
	)
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{
		CWD:            cwd,
		TranscriptPath: transcript,
		Mutation:       "append",
		Frames: []string{
			resumeInitFrame(t, claudeResumePeerSessionID, cwd),
		},
	})
	provider := newResumeProvider(
		t,
		peer.CLIPath,
		peer.Env,
		configDir,
		3*time.Second,
	)

	session, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID:   claudeResumePeerSessionID,
		OpenOptions: agent.OpenOptions{Cwd: cwd},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	assert.Equal(t, claudeResumePeerSessionID, session.ID())
	require.FileExists(t, peer.SessionMark)
}

func TestProviderResumeDropsReplayContent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	configDir := newResumeConfigDir(t)
	writeResumeTranscript(
		t,
		configDir,
		"project",
		claudeResumePeerSessionID,
		cwd,
	)
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{
		CWD: cwd,
		Frames: []string{
			resumeInitFrame(t, claudeResumePeerSessionID, cwd),
			resumeFrame(t, map[string]any{
				"type":       "assistant",
				"session_id": claudeResumePeerSessionID,
				"isReplay":   true,
				"message": map[string]any{
					"id": "saved-message",
					"content": []any{
						map[string]any{"type": "text", "text": "saved"},
					},
				},
			}),
			resumeFrame(t, map[string]any{
				"type":       "assistant",
				"session_id": claudeResumePeerSessionID,
				"message": map[string]any{
					"id": "live-message",
					"content": []any{
						map[string]any{
							"type": "text", "text": "live", "apiBlockIndex": 0,
						},
					},
				},
			}),
		},
	})
	provider := newResumeProvider(
		t,
		peer.CLIPath,
		peer.Env,
		configDir,
		time.Second,
	)

	session, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID:   claudeResumePeerSessionID,
		OpenOptions: agent.OpenOptions{Cwd: cwd},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)
	event, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventAgentMessage, event.Type())
	assert.Equal(t, "live-message:0", event.ItemID)
}

func TestProviderResumeConfirmsCallbackOnlyIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	configDir := newResumeConfigDir(t)
	transcript := writeResumeTranscript(
		t,
		configDir,
		"project",
		claudeResumePeerSessionID,
		cwd,
	)
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{
		CWD:            cwd,
		TranscriptPath: transcript,
		Frames: []string{
			resumeCallbackFrame(t, "callback-only", nil),
		},
	})
	provider := newResumeProviderWithHooks(
		t,
		peer.CLIPath,
		peer.Env,
		configDir,
		3*time.Second,
		json.RawMessage(`{
			"SessionStart": [{"matcher":"startup","hookCallbackIds":["caller-start"]}],
			"Stop": [{"hookCallbackIds":["caller-stop"]}]
		}`),
	)

	session, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID:   claudeResumePeerSessionID,
		OpenOptions: agent.OpenOptions{Cwd: cwd},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	assert.Equal(t, claudeResumePeerSessionID, session.ID())

	observation := waitResumeObservation(t, peer.Observation, 1)
	assert.True(t, strings.HasPrefix(
		observation.CallbackID,
		"agent-sdk-go:claude:resume-probe:",
	))
	var hooks map[string][]map[string]any
	require.NoError(t, json.Unmarshal(observation.Hooks, &hooks))
	require.Len(t, hooks["SessionStart"], 2)
	assert.Equal(t, "caller-start", hooks["SessionStart"][0]["hookCallbackIds"].([]any)[0])
	assert.Equal(t, observation.CallbackID, hooks["SessionStart"][1]["hookCallbackIds"].([]any)[0])
	assert.Equal(t, "caller-stop", hooks["Stop"][0]["hookCallbackIds"].([]any)[0])
	requireCallbackResponse(t, observation.Responses[0], "callback-only")
}

func TestProviderResumeRejectsWrongCallbackInput(t *testing.T) {
	tests := []struct {
		name      string
		overrides func(t *testing.T) map[string]any
	}{
		{
			name: "event",
			overrides: func(*testing.T) map[string]any {
				return map[string]any{"hook_event_name": "Stop"}
			},
		},
		{
			name: "source",
			overrides: func(*testing.T) map[string]any {
				return map[string]any{"source": "startup"}
			},
		},
		{
			name: "session",
			overrides: func(*testing.T) map[string]any {
				return map[string]any{"session_id": "different-session"}
			},
		},
		{
			name: "cwd",
			overrides: func(t *testing.T) map[string]any {
				return map[string]any{"cwd": t.TempDir()}
			},
		},
		{
			name: "transcript",
			overrides: func(t *testing.T) map[string]any {
				path := filepath.Join(t.TempDir(), "different.jsonl")
				require.NoError(t, os.WriteFile(path, []byte("{}\n"), 0o600))
				return map[string]any{"transcript_path": path}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			configDir := newResumeConfigDir(t)
			transcript := writeResumeTranscript(
				t,
				configDir,
				"project",
				claudeResumePeerSessionID,
				cwd,
			)
			peer := newClaudeResumePeer(t, claudeResumePeerScenario{
				CWD:            cwd,
				TranscriptPath: transcript,
				Frames: []string{
					resumeCallbackFrame(t, "wrong-callback", tt.overrides(t)),
				},
			})
			provider := newResumeProvider(
				t,
				peer.CLIPath,
				peer.Env,
				configDir,
				time.Second,
			)

			session, err := provider.Resume(ctx, agent.ResumeOptions{
				SessionID:   claudeResumePeerSessionID,
				OpenOptions: agent.OpenOptions{Cwd: cwd},
			})
			require.Error(t, err)
			assert.Nil(t, session)
			assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(err))
			require.FileExists(t, peer.SessionMark)
		})
	}
}

func TestProviderResumeLifecycleOnlyTimesOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	configDir := newResumeConfigDir(t)
	transcript := writeResumeTranscript(
		t,
		configDir,
		"project",
		claudeResumePeerSessionID,
		cwd,
	)
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{
		CWD:            cwd,
		TranscriptPath: transcript,
		Frames: []string{
			resumeLifecycleFrame(t, "hook_started"),
			resumeLifecycleFrame(t, "hook_progress"),
			resumeLifecycleFrame(t, "hook_response"),
		},
	})
	provider := newResumeProvider(
		t,
		peer.CLIPath,
		peer.Env,
		configDir,
		time.Second,
	)

	session, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID:   claudeResumePeerSessionID,
		OpenOptions: agent.OpenOptions{Cwd: cwd},
	})
	require.Error(t, err)
	assert.Nil(t, session)
	assert.Equal(t, agent.ErrorUnavailable, agent.CodeOf(err))
	require.FileExists(t, peer.SessionMark)
}

func TestProviderResumeRejectsContradictoryInit(t *testing.T) {
	tests := []struct {
		name string
		id   string
		cwd  func(t *testing.T, cwd string) string
	}{
		{
			name: "session",
			id:   "22222222-2222-4222-8222-222222222222",
			cwd:  func(_ *testing.T, cwd string) string { return cwd },
		},
		{
			name: "cwd",
			id:   claudeResumePeerSessionID,
			cwd:  func(t *testing.T, _ string) string { return t.TempDir() },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			configDir := newResumeConfigDir(t)
			transcript := writeResumeTranscript(
				t,
				configDir,
				"project",
				claudeResumePeerSessionID,
				cwd,
			)
			peer := newClaudeResumePeer(t, claudeResumePeerScenario{
				CWD:            cwd,
				TranscriptPath: transcript,
				Frames: []string{
					resumeInitFrame(t, tt.id, tt.cwd(t, cwd)),
				},
			})
			provider := newResumeProvider(
				t,
				peer.CLIPath,
				peer.Env,
				configDir,
				time.Second,
			)

			session, err := provider.Resume(ctx, agent.ResumeOptions{
				SessionID:   claudeResumePeerSessionID,
				OpenOptions: agent.OpenOptions{Cwd: cwd},
			})
			require.Error(t, err)
			assert.Nil(t, session)
			assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(err))
		})
	}
}

func TestProviderResumeRejectsReplayInit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	configDir := newResumeConfigDir(t)
	transcript := writeResumeTranscript(
		t,
		configDir,
		"project",
		claudeResumePeerSessionID,
		cwd,
	)
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{
		CWD:            cwd,
		TranscriptPath: transcript,
		Frames: []string{resumeFrame(t, map[string]any{
			"type":       "system",
			"subtype":    "init",
			"session_id": claudeResumePeerSessionID,
			"cwd":        cwd,
			"isReplay":   true,
		})},
	})
	provider := newResumeProvider(
		t,
		peer.CLIPath,
		peer.Env,
		configDir,
		time.Second,
	)

	session, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID:   claudeResumePeerSessionID,
		OpenOptions: agent.OpenOptions{Cwd: cwd},
	})
	require.Error(t, err)
	assert.Nil(t, session)
	assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(err))
}

func TestProviderResumeRejectsPostIdentityTranscriptReplacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	configDir := newResumeConfigDir(t)
	transcript := writeResumeTranscript(
		t,
		configDir,
		"project",
		claudeResumePeerSessionID,
		cwd,
	)
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{
		CWD:            cwd,
		TranscriptPath: transcript,
		Mutation:       "replace",
		Replacement:    resumeMetadataLine(t, claudeResumePeerSessionID, cwd),
		Frames: []string{
			resumeInitFrame(t, claudeResumePeerSessionID, cwd),
		},
	})
	provider := newResumeProvider(
		t,
		peer.CLIPath,
		peer.Env,
		configDir,
		time.Second,
	)

	session, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID:   claudeResumePeerSessionID,
		OpenOptions: agent.OpenOptions{Cwd: cwd},
	})
	require.Error(t, err)
	assert.Nil(t, session)
	assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(err))
}

func TestProviderResumeAnswersLateCallbackOnceAndKeepsInitIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	configDir := newResumeConfigDir(t)
	transcript := writeResumeTranscript(
		t,
		configDir,
		"project",
		claudeResumePeerSessionID,
		cwd,
	)
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{
		CWD:            cwd,
		TranscriptPath: transcript,
		Frames: []string{
			resumeInitFrame(t, claudeResumePeerSessionID, cwd),
			resumeCallbackFrame(t, "late-callback", nil),
		},
		FrameDelays: []int{0, 100},
	})
	provider := newResumeProvider(
		t,
		peer.CLIPath,
		peer.Env,
		configDir,
		time.Second,
	)

	session, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID:   claudeResumePeerSessionID,
		OpenOptions: agent.OpenOptions{Cwd: cwd},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	observation := waitResumeObservation(t, peer.Observation, 1)
	requireCallbackResponse(t, observation.Responses[0], "late-callback")
	requireSessionAlive(t, session, 200*time.Millisecond)
}

func TestProviderResumeLateEvidenceTerminatesReturnedSession(t *testing.T) {
	tests := []struct {
		name       string
		frames     func(t *testing.T, cwd, transcript string) ([]string, []int)
		callbackID string
	}{
		{
			name: "duplicate callback after init",
			frames: func(t *testing.T, cwd, _ string) ([]string, []int) {
				return []string{
					resumeInitFrame(t, claudeResumePeerSessionID, cwd),
					resumeCallbackFrame(t, "first-callback", nil),
					resumeCallbackFrame(t, "second-callback", nil),
				}, []int{0, 100, 100}
			},
			callbackID: "first-callback",
		},
		{
			name: "wrong callback after init",
			frames: func(t *testing.T, cwd, _ string) ([]string, []int) {
				return []string{
					resumeInitFrame(t, claudeResumePeerSessionID, cwd),
					resumeCallbackFrame(t, "wrong-late", map[string]any{
						"session_id": "different-session",
					}),
				}, []int{0, 100}
			},
			callbackID: "wrong-late",
		},
		{
			name: "duplicate callback identity",
			frames: func(t *testing.T, _, _ string) ([]string, []int) {
				return []string{
					resumeCallbackFrame(t, "identity-callback", nil),
					resumeCallbackFrame(t, "duplicate-identity", nil),
				}, []int{0, 100}
			},
			callbackID: "identity-callback",
		},
		{
			name: "duplicate init identity",
			frames: func(t *testing.T, cwd, _ string) ([]string, []int) {
				return []string{
					resumeInitFrame(t, claudeResumePeerSessionID, cwd),
					resumeInitFrame(t, claudeResumePeerSessionID, cwd),
				}, []int{0, 100}
			},
		},
		{
			name: "duplicate late init after callback identity",
			frames: func(t *testing.T, cwd, _ string) ([]string, []int) {
				return []string{
					resumeCallbackFrame(t, "identity-callback", nil),
					resumeInitFrame(t, claudeResumePeerSessionID, cwd),
					resumeInitFrame(t, claudeResumePeerSessionID, cwd),
				}, []int{0, 100, 100}
			},
			callbackID: "identity-callback",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			configDir := newResumeConfigDir(t)
			transcript := writeResumeTranscript(
				t,
				configDir,
				"project",
				claudeResumePeerSessionID,
				cwd,
			)
			frames, delays := tt.frames(t, cwd, transcript)
			peer := newClaudeResumePeer(t, claudeResumePeerScenario{
				CWD:            cwd,
				TranscriptPath: transcript,
				Frames:         frames,
				FrameDelays:    delays,
			})
			provider := newResumeProvider(
				t,
				peer.CLIPath,
				peer.Env,
				configDir,
				time.Second,
			)

			session, err := provider.Resume(ctx, agent.ResumeOptions{
				SessionID:   claudeResumePeerSessionID,
				OpenOptions: agent.OpenOptions{Cwd: cwd},
			})
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)
			waitSessionDone(t, session)
			assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(session.Err()))
			if tt.callbackID != "" {
				observation := waitResumeObservation(t, peer.Observation, 1)
				requireCallbackResponse(t, observation.Responses[0], tt.callbackID)
				assert.Len(t, observation.Responses, 1)
			}
		})
	}
}

func TestProviderResumeCallbackIdentityAllowsOneMatchingLateInit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	configDir := newResumeConfigDir(t)
	transcript := writeResumeTranscript(
		t,
		configDir,
		"project",
		claudeResumePeerSessionID,
		cwd,
	)
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{
		CWD:            cwd,
		TranscriptPath: transcript,
		Frames: []string{
			resumeCallbackFrame(t, "identity-callback", nil),
			resumeInitFrame(t, claudeResumePeerSessionID, cwd),
		},
		FrameDelays: []int{0, 100},
	})
	provider := newResumeProvider(
		t,
		peer.CLIPath,
		peer.Env,
		configDir,
		time.Second,
	)

	session, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID:   claudeResumePeerSessionID,
		OpenOptions: agent.OpenOptions{Cwd: cwd},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	requireSessionAlive(t, session, 250*time.Millisecond)
	require.Eventually(t, func() bool {
		return agentclaude.InjectSupportedForTest(session)
	}, 3*time.Second, time.Millisecond)

	receipt, err := session.(agent.Injector).Inject(ctx, claudeTextInput("resumed context"))
	require.NoError(t, err)
	assert.Equal(t, agent.Acceptance{Sent: new(true)}, receipt.Acceptance)

	require.NoError(t, session.Close(ctx))
	observation := readOpenPeerObservation(t, peer.Observation)
	require.Len(t, observation.UserFrames, 1)
	assert.JSONEq(t,
		`{"type":"user","message":{"role":"user","content":"resumed context"},"session_id":"","parent_tool_use_id":null,"shouldQuery":false}`,
		string(observation.UserFrames[0]),
	)
}

func TestProviderResumeCallbackIdentityRejectsContradictoryLateInit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	configDir := newResumeConfigDir(t)
	transcript := writeResumeTranscript(
		t,
		configDir,
		"project",
		claudeResumePeerSessionID,
		cwd,
	)
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{
		CWD:            cwd,
		TranscriptPath: transcript,
		Frames: []string{
			resumeCallbackFrame(t, "identity-callback", nil),
			resumeInitFrame(t, "22222222-2222-4222-8222-222222222222", cwd),
		},
		FrameDelays: []int{0, 100},
	})
	provider := newResumeProvider(
		t,
		peer.CLIPath,
		peer.Env,
		configDir,
		time.Second,
	)

	session, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID:   claudeResumePeerSessionID,
		OpenOptions: agent.OpenOptions{Cwd: cwd},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	waitSessionDone(t, session)
	assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(session.Err()))
}

func newResumeProvider(
	t *testing.T,
	cliPath string,
	env []string,
	configDir string,
	timeout time.Duration,
) *agentclaude.Core {
	t.Helper()
	return newResumeProviderWithHooks(t, cliPath, env, configDir, timeout, nil)
}

func newResumeProviderWithHooks(
	t *testing.T,
	cliPath string,
	env []string,
	configDir string,
	timeout time.Duration,
	hooks json.RawMessage,
) *agentclaude.Core {
	t.Helper()
	config := agentclaude.Config{
		CLIPath:           cliPath,
		Env:               env,
		ConfigDir:         configDir,
		InitializeTimeout: timeout,
	}
	if hooks != nil {
		config.Initialize = &claudetypes.InitializeOptions{Hooks: hooks}
	}
	provider := agentclaude.New(config)
	_, err := provider.Start(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, provider.Stop(context.Background()))
	})
	return provider
}

func newResumeConfigDir(t *testing.T) string {
	t.Helper()
	configDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "projects"), 0o700))
	return configDir
}

func writeResumeTranscript(
	t *testing.T,
	configDir string,
	project string,
	id string,
	cwd string,
) string {
	t.Helper()
	return writeResumeSource(
		t,
		configDir,
		project,
		id,
		resumeMetadataLine(t, id, cwd),
	)
}

func writeResumeSource(
	t *testing.T,
	configDir string,
	project string,
	id string,
	source string,
) string {
	t.Helper()
	directory := filepath.Join(configDir, "projects", project)
	require.NoError(t, os.MkdirAll(directory, 0o700))
	path := filepath.Join(directory, id+".jsonl")
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	return path
}

func resumeMetadataLine(t *testing.T, id, cwd string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"type":      "user",
		"sessionId": id,
		"cwd":       cwd,
		"message": map[string]any{
			"role":    "user",
			"content": "saved",
		},
	})
	require.NoError(t, err)
	return string(data) + "\n"
}

func resumeInitFrame(t *testing.T, id, cwd string) string {
	t.Helper()
	return resumeFrame(t, map[string]any{
		"type":                "system",
		"subtype":             "init",
		"session_id":          id,
		"cwd":                 cwd,
		"claude_code_version": "2.1.258",
	})
}

func resumeCallbackFrame(
	t *testing.T,
	requestID string,
	overrides map[string]any,
) string {
	t.Helper()
	input := map[string]any{
		"hook_event_name": "SessionStart",
		"source":          "resume",
		"session_id":      claudeResumePeerSessionIDID,
		"cwd":             claudeResumePeerCwdID,
		"transcript_path": claudeResumePeerTranscriptID,
	}
	maps.Copy(input, overrides)
	return resumeFrame(t, map[string]any{
		"type":       "control_request",
		"request_id": requestID,
		"request": map[string]any{
			"subtype":     "hook_callback",
			"callback_id": claudeResumePeerCallbackID,
			"input":       input,
		},
	})
}

func resumeLifecycleFrame(t *testing.T, subtype string) string {
	t.Helper()
	return resumeFrame(t, map[string]any{
		"type":       "system",
		"subtype":    subtype,
		"session_id": claudeResumePeerSessionIDID,
		"hook_id":    "resume-hook",
		"hook_name":  "SessionStart:resume",
	})
}

func resumeFrame(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

func waitResumeObservation(
	t *testing.T,
	path string,
	responses int,
) claudeResumePeerObservation {
	t.Helper()
	var observation claudeResumePeerObservation
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(data, &observation) != nil {
			return false
		}
		return len(observation.Responses) >= responses
	}, 3*time.Second, 10*time.Millisecond)
	return observation
}

func requireCallbackResponse(
	t *testing.T,
	raw json.RawMessage,
	requestID string,
) {
	t.Helper()
	var wire struct {
		Type     string `json:"type"`
		Response struct {
			Subtype   string         `json:"subtype"`
			RequestID string         `json:"request_id"`
			Response  map[string]any `json:"response"`
		} `json:"response"`
	}
	require.NoError(t, json.Unmarshal(raw, &wire))
	assert.Equal(t, "control_response", wire.Type)
	assert.Equal(t, "success", wire.Response.Subtype)
	assert.Equal(t, requestID, wire.Response.RequestID)
	assert.Empty(t, wire.Response.Response)
}

func requireSessionAlive(t *testing.T, session agent.Session, duration time.Duration) {
	t.Helper()
	select {
	case <-session.Done():
		require.Fail(t, "session terminated unexpectedly", "error: %v", session.Err())
	case <-time.After(duration):
	}
}

func waitSessionDone(t *testing.T, session agent.Session) {
	t.Helper()
	select {
	case <-session.Done():
	case <-time.After(3 * time.Second):
		require.Fail(t, "session did not terminate")
	}
}
