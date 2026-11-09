package agentclaude_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
)

func TestProviderMapsAdditionalSystemPromptToClaudeOpenAndResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	openPrompt := "  open line\r\n--leading-dash\n附加指令 ünïcødé 🚀\t"
	resumePrompt := "resume only\n--model=other\r\n保留 CRLF 🚀  "

	openFrames := append([]string(nil), mockClaudeInit...)
	openFrames = append(openFrames,
		`<{"type":"system","subtype":"init","session_id":"prompt-open","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
	)
	resumeFrames := append([]string(nil), mockClaudeInit...)
	resumeFrames = append(resumeFrames,
		`<{"type":"system","subtype":"init","session_id":"prompt-resume","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
	)
	openPeer := prepareClaudePeer(t, "passed", openFrames...)
	resumePeer := prepareClaudePeer(t, "passed", resumeFrames...)
	launcher, captures := captureSequencedClaudeLauncher(t, openPeer, resumePeer)
	provider := agentclaude.New(agentclaude.Config{CLIPath: launcher})
	t.Cleanup(func() { require.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	opened, err := provider.Open(ctx, agent.OpenOptions{
		Cwd:                    cwd,
		AdditionalSystemPrompt: openPrompt,
	})
	require.NoError(t, err)
	require.NoError(t, opened.Close(ctx))
	openArgv := readClaudeArgv(t, captures[0])
	assert.Equal(t, []string{"--append-system-prompt=" + openPrompt},
		claudeAdditionalPromptArguments(openArgv))
	assertNoSystemPromptReplacement(t, openArgv)

	resumed, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID: "prompt-resume",
		OpenOptions: agent.OpenOptions{
			Cwd:                    cwd,
			AdditionalSystemPrompt: resumePrompt,
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, resumed)
	resumeArgv := readClaudeArgv(t, captures[1])
	assert.Equal(t, []string{"--append-system-prompt=" + resumePrompt},
		claudeAdditionalPromptArguments(resumeArgv))
	assertNoSystemPromptReplacement(t, resumeArgv)
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
