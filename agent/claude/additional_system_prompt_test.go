package agentclaude_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

func TestProviderMapsAdditionalSystemPromptToClaudeOpenAndResume(t *testing.T) {
	openPrompt := "  open line\r\n--leading-dash\n附加指令 ünïcødé 🚀\t"
	resumePrompt := "resume only\n--model=other\r\n保留 CRLF 🚀  "
	tests := []struct {
		name      string
		resume    bool
		prompt    string
		sessionID string
	}{
		{name: "open", prompt: openPrompt, sessionID: "prompt-open"},
		{name: "resume", resume: true, prompt: resumePrompt, sessionID: "prompt-resume"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()

			frames := claudeAdditionalPromptInitFrames(t, cwd, tt.sessionID)
			peer := prepareClaudePeer(t, "passed", frames...)
			launcher, captures := captureSequencedClaudeLauncher(t, peer)
			provider := agentclaude.New(agentclaude.Config{CLIPath: launcher})
			t.Cleanup(func() { require.NoError(t, provider.Stop(context.Background())) })

			_, err := provider.Start(ctx)
			require.NoError(t, err)
			var session agent.Session
			if tt.resume {
				session, err = provider.Resume(ctx, agent.ResumeOptions{
					SessionID: tt.sessionID,
					OpenOptions: agent.OpenOptions{
						Cwd:                    cwd,
						AdditionalSystemPrompt: tt.prompt,
					},
				})
			} else {
				session, err = provider.Open(ctx, agent.OpenOptions{
					Cwd:                    cwd,
					AdditionalSystemPrompt: tt.prompt,
				})
			}
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)
			require.NoError(t, session.Close(ctx))

			argv := readClaudeArgv(t, captures[0])
			assert.Equal(t, []string{"--append-system-prompt=" + tt.prompt},
				claudeAdditionalPromptArguments(argv))
			assertNoSystemPromptReplacement(t, argv)
		})
	}
}

func TestProviderOmitsEmptyAdditionalSystemPromptOnClaudeOpenAndResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()

	openFrames := append([]string(nil), mockClaudeInit...)
	openFrames = append(openFrames,
		`<{"type":"system","subtype":"init","session_id":"prompt-open-empty","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
	)
	resumeFrames := append([]string(nil), mockClaudeInit...)
	resumeFrames = append(resumeFrames,
		`<{"type":"system","subtype":"init","session_id":"prompt-resume-empty","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
	)
	openPeer := prepareClaudePeer(t, "passed", openFrames...)
	resumePeer := prepareClaudePeer(t, "passed", resumeFrames...)
	launcher, captures := captureSequencedClaudeLauncher(t, openPeer, resumePeer)
	provider := agentclaude.New(agentclaude.Config{CLIPath: launcher})
	t.Cleanup(func() { require.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	opened, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	require.NoError(t, opened.Close(ctx))
	openArgv := readClaudeArgv(t, captures[0])
	assert.Empty(t, claudeAdditionalPromptArguments(openArgv))
	assertNoSystemPromptReplacement(t, openArgv)

	resumed, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID:   "prompt-resume-empty",
		OpenOptions: agent.OpenOptions{Cwd: cwd},
	})
	require.NoError(t, err)
	require.NoError(t, resumed.Close(ctx))
	resumeArgv := readClaudeArgv(t, captures[1])
	assert.Empty(t, claudeAdditionalPromptArguments(resumeArgv))
	assertNoSystemPromptReplacement(t, resumeArgv)
}

func TestProviderRejectsInvalidAdditionalSystemPromptBeforeClaudeLaunch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	marker := filepath.Join(cwd, "must-not-launch")
	launcher := filepath.Join(cwd, "capture-launch")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(marker) + "\nexit 69\n"
	require.NoError(t, os.WriteFile(launcher, []byte(script), 0o700))
	provider := agentclaude.New(agentclaude.Config{CLIPath: launcher})
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Stop(context.Background())) })

	for _, invalid := range []struct {
		name       string
		value      string
		wantReason string
	}{
		{
			name:       "invalid UTF-8",
			value:      "additional-prompt-secret-t02-" + string([]byte{0xff}),
			wantReason: "additional_system_prompt_invalid_utf8",
		},
		{
			name:       "NUL",
			value:      "additional-prompt-secret-t02\x00tail",
			wantReason: "additional_system_prompt_contains_nul",
		},
	} {
		for _, operation := range []string{"open", "resume"} {
			t.Run(invalid.name+"/"+operation, func(t *testing.T) {
				options := agent.OpenOptions{
					Cwd:                    cwd,
					AdditionalSystemPrompt: invalid.value,
				}
				if operation == "open" {
					_, err = provider.Open(ctx, options)
				} else {
					_, err = provider.Resume(ctx, agent.ResumeOptions{
						SessionID:   "prompt-existing",
						OpenOptions: options,
					})
				}
				_, statErr := os.Stat(marker)
				assert.ErrorIs(t, statErr, os.ErrNotExist)
				require.Error(t, err)
				assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
				var classified *agent.Error
				require.ErrorAs(t, err, &classified)
				assert.Equal(t, invalid.wantReason, classified.Reason)
				assert.NotContains(t, err.Error(), "additional-prompt-secret-t02")
			})
		}
	}
}

func TestProviderDoesNotExposeAdditionalSystemPromptOnNativeFailure(t *testing.T) {
	const sentinel = "additional-prompt-secret-provider-path-t02"
	tests := []struct {
		name   string
		resume bool
		event  bool
	}{
		{name: "open error"},
		{name: "resume error", resume: true},
		{name: "open native event", event: true},
		{name: "resume native event", resume: true, event: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			logs := captureDefaultLogs(t)
			t.Cleanup(func() { assert.NotContains(t, logs.String(), sentinel) })
			cwd := t.TempDir()

			var frames []string
			if tt.event {
				frames = claudeAdditionalPromptInitFrames(t, cwd, "prompt-error")
				frames = append(frames,
					`<{"type":"assistant","session_id":"prompt-error","message":{"id":"prompt-error-message","content":[{"type":"text","text":"synthetic native event","apiBlockIndex":0}]}}`,
				)
			} else {
				frames = []string{
					mockClaudeInit[0],
					`<{"type":"control_response","response":{"subtype":"error","request_id":"init","error":"synthetic initialization rejection"}}`,
				}
			}

			peer := prepareClaudePeer(t, "passed", frames...)
			launcher, _ := captureSequencedClaudeLauncher(t, peer)
			provider := agentclaude.New(agentclaude.Config{CLIPath: launcher})
			t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

			_, err := provider.Start(ctx)
			require.NoError(t, err)
			options := agent.OpenOptions{
				Cwd:                    cwd,
				AdditionalSystemPrompt: sentinel,
			}
			var session agent.Session
			if tt.resume {
				session, err = provider.Resume(ctx, agent.ResumeOptions{
					SessionID:   "prompt-error",
					OpenOptions: options,
				})
			} else {
				session, err = provider.Open(ctx, options)
			}
			if !tt.event {
				require.Error(t, err)
				assert.NotContains(t, err.Error(), sentinel)
				assert.NotContains(t, fmt.Sprintf("%+v", err), sentinel)
			} else {
				require.NoError(t, err)
				closeSessionOnCleanup(t, session)
				stream, streamErr := session.Events()
				require.NoError(t, streamErr)
				event, nextErr := stream.Next(ctx)
				require.NoError(t, nextErr)
				assert.Equal(t, agent.EventAgentMessage, event.Type())
				encoded, encodeErr := json.Marshal(event)
				require.NoError(t, encodeErr)
				assert.NotContains(t, string(encoded), sentinel)
			}
			assert.NotContains(t, logs.String(), sentinel)
		})
	}
}

func TestProviderDoesNotRestoreEmptyAdditionalSystemPromptOnResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	const (
		sessionID = "prompt-shared"
		prompt    = "resume prompt must not be restored"
	)

	peers := []*mocktest.Session{
		prepareClaudePeer(
			t,
			"passed",
			claudeAdditionalPromptInitFrames(t, cwd, sessionID)...,
		),
		prepareClaudePeer(
			t,
			"passed",
			claudeAdditionalPromptInitFrames(t, cwd, sessionID)...,
		),
		prepareClaudePeer(
			t,
			"passed",
			claudeAdditionalPromptInitFrames(t, cwd, sessionID)...,
		),
	}
	launcher, captures := captureSequencedClaudeLauncher(t, peers...)
	provider := agentclaude.New(agentclaude.Config{CLIPath: launcher})
	t.Cleanup(func() { require.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	opened, err := provider.Open(ctx, agent.OpenOptions{
		Cwd:                    cwd,
		AdditionalSystemPrompt: prompt,
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, opened)
	require.Equal(t, sessionID, opened.ID())
	require.NoError(t, opened.Close(ctx))

	for _, additionalPrompt := range []string{prompt, ""} {
		resumed, resumeErr := provider.Resume(ctx, agent.ResumeOptions{
			SessionID: sessionID,
			OpenOptions: agent.OpenOptions{
				Cwd:                    cwd,
				AdditionalSystemPrompt: additionalPrompt,
			},
		})
		require.NoError(t, resumeErr)
		closeSessionOnCleanup(t, resumed)
		require.NoError(t, resumed.Close(ctx))
	}

	for index, wantPrompt := range []string{prompt, prompt, ""} {
		argv := readClaudeArgv(t, captures[index])
		if wantPrompt == "" {
			assert.Empty(t, claudeAdditionalPromptArguments(argv))
		} else {
			assert.Equal(t, []string{"--append-system-prompt=" + wantPrompt},
				claudeAdditionalPromptArguments(argv))
		}
		assertNoSystemPromptReplacement(t, argv)
	}
}

func claudeAdditionalPromptInitFrames(
	t *testing.T,
	cwd string,
	sessionID string,
) []string {
	t.Helper()
	frames := append([]string(nil), mockClaudeInit...)
	return append(frames,
		`<{"type":"system","subtype":"init","session_id":`+jsonString(t, sessionID)+
			`,"cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
	)
}

func captureDefaultLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	logs := new(bytes.Buffer)
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
	})
	return logs
}

func claudeAdditionalPromptArguments(argv []string) []string {
	var found []string
	for _, arg := range argv {
		if strings.HasPrefix(arg, "--append-system-prompt=") {
			found = append(found, arg)
		}
	}
	return found
}

func assertNoSystemPromptReplacement(t *testing.T, argv []string) {
	t.Helper()
	for _, arg := range argv {
		assert.False(t, strings.HasPrefix(arg, "--system-prompt"))
	}
}
