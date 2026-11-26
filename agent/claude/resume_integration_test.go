package agentclaude_test

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	claudetypes "github.com/zoumo/agent-sdk-go/client/claude/types"
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
			writeResumeTranscript(t, configDir, resumeProjectKey(t, cwd), claudeResumePeerSessionID, cwd)
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
			name: "only in another project",
			setup: func(t *testing.T, configDir, _ string) {
				otherCwd := t.TempDir()
				writeResumeTranscript(t, configDir, resumeProjectKey(t, otherCwd), claudeResumePeerSessionID, otherCwd)
			},
		},
		{
			name: "wrong cwd",
			setup: func(t *testing.T, configDir, cwd string) {
				writeResumeTranscript(
					t,
					configDir,
					resumeProjectKey(t, cwd),
					claudeResumePeerSessionID,
					t.TempDir(),
				)
			},
		},
		{
			name: "corrupt partial",
			setup: func(t *testing.T, configDir, cwd string) {
				writeResumeSource(
					t,
					configDir,
					resumeProjectKey(t, cwd),
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
					resumeProjectKey(t, cwd),
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
				directory := filepath.Join(configDir, "projects", resumeProjectKey(t, cwd))
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
					resumeProjectKey(t, cwd),
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
		resumeProjectKey(t, cwd),
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

func TestProviderResumePreservesCommandUpdateOrder(t *testing.T) {
	for _, timing := range []string{"before status", "after status"} {
		t.Run(timing, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			cwd, config := t.TempDir(), newResumeConfigDir(t)
			writeResumeTranscript(t, config, resumeProjectKey(t, cwd), claudeResumePeerSessionID, cwd)
			frames := []string{
				`{"type":"system","subtype":"init","session_id":"__SESSION_ID__","cwd":"__CWD__","claude_code_version":"2.1.293","slash_commands":["initial"]}`,
				`{"type":"system","subtype":"commands_changed","session_id":"__SESSION_ID__","commands":[{"name":"updated"}]}`,
			}
			scenario := claudeResumePeerScenario{CWD: cwd}
			if timing == "before status" {
				scenario.Frames = frames
			} else {
				scenario.AfterStatus = frames
			}
			peer := newClaudeResumePeer(t, scenario)
			core := newResumeProvider(t, peer.CLIPath, peer.Env, config, time.Second)
			session, err := core.Resume(ctx, agent.ResumeOptions{
				SessionID: claudeResumePeerSessionID, OpenOptions: agent.OpenOptions{Cwd: cwd},
			})
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)
			stream, err := session.Events()
			require.NoError(t, err)
			requireStartupForwardingGap(t, ctx, stream)
			for _, name := range []string{"initial", "updated"} {
				assert.Equal(t, agent.AvailableCommandsUpdatePayload{
					AvailableCommands: []agent.AvailableCommand{{Name: name}},
				}, nextEvent(t, ctx, stream).Payload)
			}
		})
	}
}

func TestProviderResumeDropsReplayContent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	configDir := newResumeConfigDir(t)
	writeResumeTranscript(
		t,
		configDir,
		resumeProjectKey(t, cwd),
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
	requireStartupForwardingGap(t, ctx, stream)
	event, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventAgentMessage, event.Type())
	assert.Equal(t, "live-message:0", event.ItemID)
}

func TestProviderResumePreservesCallerHooksWithoutPrivateCallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cwd, config := t.TempDir(), newResumeConfigDir(t)
	writeResumeTranscript(t, config, resumeProjectKey(t, cwd), claudeResumePeerSessionID, cwd)
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{CWD: cwd})
	hooks := json.RawMessage(`{"SessionStart":[{"matcher":"startup","hookCallbackIds":["caller-start"]}],"Stop":[{"hookCallbackIds":["caller-stop"]}]}`)
	provider := newResumeProviderWithHooks(t, peer.CLIPath, peer.Env, config, time.Second, hooks)
	session, err := provider.Resume(ctx, agent.ResumeOptions{SessionID: claudeResumePeerSessionID, OpenOptions: agent.OpenOptions{Cwd: cwd}})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	require.NoError(t, session.Close(ctx))
	observation := readOpenPeerObservation(t, peer.Observation)
	assert.Empty(t, observation.CallbackID)
	assert.JSONEq(t, string(hooks), string(observation.Hooks))
	assert.Empty(t, observation.UserFrames)
}

func TestProviderResumeCallbackCannotReplaceStatus(t *testing.T) {
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
				resumeProjectKey(t, cwd),
				claudeResumePeerSessionID,
				cwd,
			)
			peer := newClaudeResumePeer(t, claudeResumePeerScenario{
				CWD:            cwd,
				TranscriptPath: transcript,
				StatusIgnore:   true,
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
			assert.Equal(t, agent.ErrorUnavailable, agent.CodeOf(err))
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
		resumeProjectKey(t, cwd),
		claudeResumePeerSessionID,
		cwd,
	)
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{
		CWD:            cwd,
		TranscriptPath: transcript,
		StatusIgnore:   true,
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
				resumeProjectKey(t, cwd),
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
		resumeProjectKey(t, cwd),
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
		resumeProjectKey(t, cwd),
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

func TestProviderResumeLateEvidenceTerminatesReturnedSession(t *testing.T) {
	for _, name := range []string{"wrong session", "wrong cwd", "replay init", "duplicate init", "wrong main message"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cwd, config := t.TempDir(), newResumeConfigDir(t)
			writeResumeTranscript(t, config, resumeProjectKey(t, cwd), claudeResumePeerSessionID, cwd)
			frame := resumeInitFrame(t, claudeResumePeerSessionID, cwd)
			frames := []string{frame}
			switch name {
			case "wrong session":
				frames = []string{resumeInitFrame(t, "22222222-2222-4222-8222-222222222222", cwd)}
			case "wrong cwd":
				frames = []string{resumeInitFrame(t, claudeResumePeerSessionID, t.TempDir())}
			case "replay init":
				frames = []string{resumeFrame(t, map[string]any{"type": "system", "subtype": "init", "session_id": claudeResumePeerSessionID, "cwd": cwd, "isReplay": true})}
			case "duplicate init":
				frames = []string{frame, frame}
			case "wrong main message":
				frames = []string{resumeFrame(t, map[string]any{"type": "system", "subtype": "session_state_changed", "session_id": "22222222-2222-4222-8222-222222222222", "state": "idle"})}
			}
			peer := newClaudeResumePeer(t, claudeResumePeerScenario{CWD: cwd, Frames: []string{resumeLifecycleFrame(t, "hook_started")}, AfterStatus: frames, AfterStatusDelays: []int{100}})
			provider := newResumeProvider(t, peer.CLIPath, peer.Env, config, time.Second)
			session, err := provider.Resume(ctx, agent.ResumeOptions{SessionID: claudeResumePeerSessionID, OpenOptions: agent.OpenOptions{Cwd: cwd}})
			require.NoError(t, err)
			t.Cleanup(func() {
				if err := session.Close(context.Background()); err != nil {
					assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(err))
				}
			})
			waitSessionDone(t, session)
			assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(session.Err()))
		})
	}
}

func TestProviderResumeStatusIdentityAllowsOneMatchingLateInit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	configDir := newResumeConfigDir(t)
	transcript := writeResumeTranscript(
		t,
		configDir,
		resumeProjectKey(t, cwd),
		claudeResumePeerSessionID,
		cwd,
	)
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{
		CWD:            cwd,
		TranscriptPath: transcript,
		AfterStatus: []string{
			resumeInitFrame(t, claudeResumePeerSessionID, cwd),
		},
		AfterStatusDelays: []int{100},
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
		"claude_code_version": "2.1.293",
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
