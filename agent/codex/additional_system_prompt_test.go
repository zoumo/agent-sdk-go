package agentcodex_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestProviderMapsAdditionalSystemPromptToOpenAndResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	openPrompt := "  open line\r\n--leading-dash\n附加指令 ünïcødé 🚀\t"
	resumePrompt := "resume only\n--model=other\r\n保留 CRLF 🚀  "

	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":`+codexAdditionalPromptStartParams(openPrompt)+`}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-open")+`}`,
	)
	frames = append(frames, codexCloseFrames(93, "root-open")...)
	frames = append(frames,
		`>{"id":94,"method":"thread/resume","params":`+codexAdditionalPromptResumeParams(resumePrompt)+`}`,
		`<{"id":94,"result":`+codexThreadResumeResult("root-resume")+`}`,
	)
	frames = append(frames, codexCloseFrames(95, "root-resume")...)

	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	opened, err := provider.Open(ctx, agent.OpenOptions{
		Cwd:                    "/synthetic",
		AdditionalSystemPrompt: openPrompt,
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, opened)
	require.NoError(t, opened.Close(ctx))

	resumed, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID: "root-resume",
		OpenOptions: agent.OpenOptions{
			Cwd:                    "/synthetic",
			AdditionalSystemPrompt: resumePrompt,
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, resumed)
	require.NoError(t, resumed.Close(ctx))
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

func codexAdditionalPromptStartParams(prompt string) string {
	return `{"cwd":"/synthetic","developerInstructions":` + marshalJSONString(prompt) + `}`
}

func codexAdditionalPromptResumeParams(prompt string) string {
	return `{"cwd":"/synthetic","developerInstructions":` + marshalJSONString(prompt) +
		`,"excludeTurns":true,"threadId":"root-resume"}`
}

func marshalJSONString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
