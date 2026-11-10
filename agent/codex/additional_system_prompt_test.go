package agentcodex_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestProviderMapsAdditionalSystemPromptToOpenAndResume(t *testing.T) {
	openPrompt := "  open line\r\n--leading-dash\n附加指令 ünïcødé 🚀\t"
	resumePrompt := "resume only\n--model=other\r\n保留 CRLF 🚀  "
	tests := []struct {
		name       string
		resume     bool
		prompt     string
		sessionID  string
		request    string
		nativeType string
	}{
		{
			name:       "open",
			prompt:     openPrompt,
			sessionID:  "root-open",
			request:    codexAdditionalPromptStartParams(openPrompt),
			nativeType: "thread/start",
		},
		{
			name:       "resume",
			resume:     true,
			prompt:     resumePrompt,
			sessionID:  "root-resume",
			request:    codexAdditionalPromptResumeParams(resumePrompt),
			nativeType: "thread/resume",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			frames := append([]string(nil), mockCodexInit...)
			frames = append(frames,
				`>{"id":92,"method":"`+tt.nativeType+`","params":`+tt.request+`}`,
				`<{"id":92,"result":`+codexAdditionalPromptResult(tt.resume, tt.sessionID)+`}`,
			)
			frames = append(frames, codexCloseFrames(93, tt.sessionID)...)

			peerSession := prepareCodexPeer(t, frames...)
			provider := newTestProvider(t, peerSession)
			t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

			_, err := provider.Start(ctx)
			require.NoError(t, err)

			var session agent.Session
			if tt.resume {
				session, err = provider.Resume(ctx, agent.ResumeOptions{
					SessionID: tt.sessionID,
					OpenOptions: agent.OpenOptions{
						Cwd:                    "/synthetic",
						AdditionalSystemPrompt: tt.prompt,
					},
				})
			} else {
				session, err = provider.Open(ctx, agent.OpenOptions{
					Cwd:                    "/synthetic",
					AdditionalSystemPrompt: tt.prompt,
				})
			}
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)
			require.NoError(t, session.Close(ctx))
		})
	}
}

func TestProviderOmitsEmptyAdditionalSystemPromptOnOpenAndResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-open")+`}`,
	)
	frames = append(frames, codexCloseFrames(93, "root-open")...)
	frames = append(frames,
		`>{"id":94,"method":"thread/resume","params":{"cwd":"/synthetic","excludeTurns":true,"threadId":"root-resume"}}`,
		`<{"id":94,"result":`+codexThreadResumeResult("root-resume")+`}`,
	)
	frames = append(frames, codexCloseFrames(95, "root-resume")...)

	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	opened, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	require.NoError(t, opened.Close(ctx))

	resumed, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID:   "root-resume",
		OpenOptions: agent.OpenOptions{Cwd: "/synthetic"},
	})
	require.NoError(t, err)
	require.NoError(t, resumed.Close(ctx))
}

func TestProviderRejectsInvalidAdditionalSystemPromptBeforeThreadRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	peerSession := prepareCodexPeer(t, mockCodexInit...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

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
					Cwd:                    "/synthetic",
					AdditionalSystemPrompt: invalid.value,
				}
				if operation == "open" {
					_, err = provider.Open(ctx, options)
				} else {
					_, err = provider.Resume(ctx, agent.ResumeOptions{
						SessionID:   "root-resume",
						OpenOptions: options,
					})
				}
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

			method := "thread/start"
			request := codexAdditionalPromptStartParams(sentinel)
			sessionID := "root-open-error"
			if tt.resume {
				method = "thread/resume"
				sessionID = "root-resume-error"
				request = codexAdditionalPromptResumeParamsForThread(sentinel, sessionID)
			}

			frames := append([]string(nil), mockCodexInit...)
			frames = append(frames, `>{"id":92,"method":"`+method+`","params":`+request+`}`)
			if tt.event {
				frames = append(frames,
					`<{"id":92,"result":`+codexAdditionalPromptResult(tt.resume, sessionID)+`}`,
					`<{"method":"error","params":{"threadId":`+marshalJSONString(sessionID)+`,"turnId":"turn-error","willRetry":false,"error":{"message":"synthetic native failure"}}}`,
				)
				frames = append(frames, codexCloseFrames(93, sessionID)...)
			} else {
				frames = append(frames,
					`<{"id":92,"error":{"code":-32602,"message":"synthetic thread rejection"}}`,
				)
				if tt.resume {
					frames = append(frames, codexCloseFrames(93, sessionID)...)
				}
			}

			peerSession := prepareCodexPeer(t, frames...)
			provider := newTestProvider(t, peerSession)
			t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

			_, err := provider.Start(ctx)
			require.NoError(t, err)
			options := agent.OpenOptions{
				Cwd:                    "/synthetic",
				AdditionalSystemPrompt: sentinel,
			}
			var session agent.Session
			if tt.resume {
				session, err = provider.Resume(ctx, agent.ResumeOptions{
					SessionID:   "root-resume-error",
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
				assert.Equal(t, agent.EventSessionError, event.Type())
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
	const (
		threadID = "root-shared"
		prompt   = "resume prompt must not be restored"
	)

	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":`+codexAdditionalPromptStartParams(prompt)+`}`,
		`<{"id":92,"result":`+codexThreadStartResult(threadID)+`}`,
	)
	frames = append(frames, codexCloseFrames(93, threadID)...)
	frames = append(frames,
		`>{"id":94,"method":"thread/resume","params":`+
			codexAdditionalPromptResumeParamsForThread(prompt, threadID)+`}`,
		`<{"id":94,"result":`+codexThreadResumeResult(threadID)+`}`,
	)
	frames = append(frames, codexCloseFrames(95, threadID)...)
	frames = append(frames,
		`>{"id":96,"method":"thread/resume","params":`+
			codexEmptyAdditionalPromptResumeParams(threadID)+`}`,
		`<{"id":96,"result":`+codexThreadResumeResult(threadID)+`}`,
	)
	frames = append(frames, codexCloseFrames(97, threadID)...)

	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	opened, err := provider.Open(ctx, agent.OpenOptions{
		Cwd:                    "/synthetic",
		AdditionalSystemPrompt: prompt,
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, opened)
	require.Equal(t, threadID, opened.ID())
	require.NoError(t, opened.Close(ctx))

	for _, additionalPrompt := range []string{prompt, ""} {
		resumed, resumeErr := provider.Resume(ctx, agent.ResumeOptions{
			SessionID: threadID,
			OpenOptions: agent.OpenOptions{
				Cwd:                    "/synthetic",
				AdditionalSystemPrompt: additionalPrompt,
			},
		})
		require.NoError(t, resumeErr)
		closeSessionOnCleanup(t, resumed)
		require.NoError(t, resumed.Close(ctx))
	}
}

func codexAdditionalPromptStartParams(prompt string) string {
	return `{"cwd":"/synthetic","developerInstructions":` + marshalJSONString(prompt) + `}`
}

func codexAdditionalPromptResumeParams(prompt string) string {
	return codexAdditionalPromptResumeParamsForThread(prompt, "root-resume")
}

func codexAdditionalPromptResumeParamsForThread(prompt, threadID string) string {
	return `{"cwd":"/synthetic","developerInstructions":` + marshalJSONString(prompt) +
		`,"excludeTurns":true,"threadId":` + marshalJSONString(threadID) + `}`
}

func codexEmptyAdditionalPromptResumeParams(threadID string) string {
	return `{"cwd":"/synthetic","excludeTurns":true,"threadId":` +
		marshalJSONString(threadID) + `}`
}

func codexAdditionalPromptResult(resume bool, threadID string) string {
	if resume {
		return codexThreadResumeResult(threadID)
	}
	return codexThreadStartResult(threadID)
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

func marshalJSONString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
