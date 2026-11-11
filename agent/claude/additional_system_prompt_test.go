package agentclaude_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	"github.com/zoumo/agent-sdk-go/claude"
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
		{
			name:      "open",
			prompt:    openPrompt,
			sessionID: "33333333-3333-4333-8333-333333333333",
		},
		{
			name:      "resume",
			resume:    true,
			prompt:    resumePrompt,
			sessionID: "44444444-4444-4444-8444-444444444444",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			options := agent.OpenOptions{
				Cwd:                    cwd,
				AdditionalSystemPrompt: tt.prompt,
			}

			var session agent.Session
			var argv []string
			var err error
			if tt.resume {
				provider, capture := startClaudeResumeExecutionProvider(
					t,
					ctx,
					cwd,
					tt.sessionID,
					resumeInitFrame(t, tt.sessionID, cwd),
				)
				session, err = provider.Resume(ctx, agent.ResumeOptions{
					SessionID:   tt.sessionID,
					OpenOptions: options,
				})
				require.NoError(t, err)
				argv = readClaudeArgv(t, capture)
			} else {
				provider, capture := startClaudeExecutionProvider(
					t,
					ctx,
					claudeAdditionalPromptInitFrames(t, cwd, tt.sessionID)...,
				)
				session, err = provider.Open(ctx, options)
				require.NoError(t, err)
				argv = capture.argv(t)
			}
			closeSessionOnCleanup(t, session)
			require.NoError(t, session.Close(ctx))

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
	options := agent.OpenOptions{Cwd: cwd}

	openProvider, openCapture := startClaudeExecutionProvider(
		t,
		ctx,
		claudeAdditionalPromptInitFrames(
			t,
			cwd,
			"55555555-5555-4555-8555-555555555555",
		)...,
	)
	opened, err := openProvider.Open(ctx, options)
	require.NoError(t, err)
	require.NoError(t, opened.Close(ctx))
	openArgv := openCapture.argv(t)
	assert.Empty(t, claudeAdditionalPromptArguments(openArgv))
	assertNoSystemPromptReplacement(t, openArgv)

	resumeProvider, resumeCapture := startClaudeResumeExecutionProvider(
		t,
		ctx,
		cwd,
		"66666666-6666-4666-8666-666666666666",
		resumeInitFrame(
			t,
			"66666666-6666-4666-8666-666666666666",
			cwd,
		),
	)
	resumed, err := resumeProvider.Resume(ctx, agent.ResumeOptions{
		SessionID:   "66666666-6666-4666-8666-666666666666",
		OpenOptions: options,
	})
	require.NoError(t, err)
	require.NoError(t, resumed.Close(ctx))
	resumeArgv := readClaudeArgv(t, resumeCapture)
	assert.Empty(t, claudeAdditionalPromptArguments(resumeArgv))
	assertNoSystemPromptReplacement(t, resumeArgv)
}

func TestProviderRejectsInvalidAdditionalSystemPromptBeforeClaudeLaunch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	launched := 0
	provider := newTestClaudeProviderWithObserver(
		t,
		agentclaude.Config{},
		func(claude.SessionOptions) {
			launched++
		},
	)
	_, err := provider.Start(ctx)
	require.NoError(t, err)

	for _, invalid := range []struct {
		name       string
		value      string
		wantReason string
	}{
		{
			name:       "invalid UTF-8",
			value:      "additional-prompt-secret-t02-" + string([]byte{0xff}),
			wantReason: agent.ErrorReasonAdditionalSystemPromptInvalidUTF8,
		},
		{
			name:       "NUL",
			value:      "additional-prompt-secret-t02\x00tail",
			wantReason: agent.ErrorReasonAdditionalSystemPromptContainsNUL,
		},
	} {
		for _, operation := range []string{"open", "resume"} {
			t.Run(invalid.name+"/"+operation, func(t *testing.T) {
				options := agent.OpenOptions{
					Cwd:                    cwd,
					AdditionalSystemPrompt: invalid.value,
				}
				var operationErr error
				if operation == "open" {
					_, operationErr = provider.Open(ctx, options)
				} else {
					_, operationErr = provider.Resume(ctx, agent.ResumeOptions{
						SessionID:   "prompt-existing",
						OpenOptions: options,
					})
				}
				require.Error(t, operationErr)
				assert.Zero(t, launched)
				assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(operationErr))
				var classified *agent.Error
				require.ErrorAs(t, operationErr, &classified)
				assert.Equal(t, invalid.wantReason, classified.Reason)
				assert.NotContains(t, operationErr.Error(), "additional-prompt-secret-t02")
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
			sessionID := "77777777-7777-4777-8777-777777777777"
			options := agent.OpenOptions{
				Cwd:                    cwd,
				AdditionalSystemPrompt: sentinel,
			}

			var provider *agentclaude.Provider
			var session agent.Session
			var err error
			if !tt.resume {
				frames := claudeAdditionalPromptInitFrames(t, cwd, sessionID)
				if tt.event {
					frames = append(frames,
						`<{"type":"assistant","session_id":`+jsonString(t, sessionID)+
							`,"message":{"id":"prompt-error-message","content":[{"type":"text","text":"synthetic native event","apiBlockIndex":0}]}}`,
					)
				} else {
					frames = []string{
						mockClaudeInit[0],
						`<{"type":"control_response","response":{"subtype":"error","request_id":"init","error":"synthetic initialization rejection"}}`,
					}
				}
				provider, _ = startClaudeExecutionProvider(t, ctx, frames...)
				session, err = provider.Open(ctx, options)
			} else {
				frames := []string{resumeInitFrame(t, sessionID, cwd)}
				if tt.event {
					frames = append(frames, resumeFrame(t, map[string]any{
						"type":       "assistant",
						"session_id": sessionID,
						"message": map[string]any{
							"id": "prompt-error-message",
							"content": []any{
								map[string]any{
									"type":          "text",
									"text":          "synthetic native event",
									"apiBlockIndex": 0,
								},
							},
						},
					}))
				} else {
					frames = []string{resumeInitFrame(
						t,
						"99999999-9999-4999-8999-999999999999",
						cwd,
					)}
				}
				provider, _ = startClaudeResumeExecutionProvider(
					t,
					ctx,
					cwd,
					sessionID,
					frames...,
				)
				session, err = provider.Resume(ctx, agent.ResumeOptions{
					SessionID:   sessionID,
					OpenOptions: options,
				})
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
		sessionID = "88888888-8888-4888-8888-888888888888"
		prompt    = "resume prompt must not be restored"
	)

	openProvider, openCapture := startClaudeExecutionProvider(
		t,
		ctx,
		claudeAdditionalPromptInitFrames(t, cwd, sessionID)...,
	)
	opened, err := openProvider.Open(ctx, agent.OpenOptions{
		Cwd:                    cwd,
		AdditionalSystemPrompt: prompt,
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, opened)
	require.Equal(t, sessionID, opened.ID())
	require.NoError(t, opened.Close(ctx))
	openArgv := openCapture.argv(t)
	assert.Equal(t, []string{"--append-system-prompt=" + prompt},
		claudeAdditionalPromptArguments(openArgv))
	assertNoSystemPromptReplacement(t, openArgv)

	for _, additionalPrompt := range []string{prompt, ""} {
		provider, capture := startClaudeResumeExecutionProvider(
			t,
			ctx,
			cwd,
			sessionID,
			resumeInitFrame(t, sessionID, cwd),
		)
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

		argv := readClaudeArgv(t, capture)
		if additionalPrompt == "" {
			assert.Empty(t, claudeAdditionalPromptArguments(argv))
		} else {
			assert.Equal(t, []string{"--append-system-prompt=" + additionalPrompt},
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
