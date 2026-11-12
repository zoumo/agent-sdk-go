package agentclaude_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestRestrictLocalToolsOnClaudeOpenAndResume(t *testing.T) {
	disabled := false
	for _, test := range []struct {
		name      string
		features  agent.Features
		effective agent.EffectiveFeatures
		deny      string
	}{
		{
			name:      "defaults",
			effective: agent.EffectiveFeatures{AgentQuestions: true},
		},
		{
			name:      "local tools restricted",
			features:  agent.Features{RestrictLocalTools: true},
			effective: agent.EffectiveFeatures{AgentQuestions: true, RestrictLocalTools: true},
			deny:      "--disallowedTools=Bash,PowerShell,Read,Glob,Grep,Write,Edit,NotebookEdit,EnterWorktree,ExitWorktree",
		},
		{
			name: "local tools restricted and questions disabled",
			features: agent.Features{
				AgentQuestions:     &disabled,
				RestrictLocalTools: true,
			},
			effective: agent.EffectiveFeatures{RestrictLocalTools: true},
			deny:      "--disallowedTools=AskUserQuestion,Bash,PowerShell,Read,Glob,Grep,Write,Edit,NotebookEdit,EnterWorktree,ExitWorktree",
		},
	} {
		for _, operation := range []string{"open", "resume"} {
			t.Run(test.name+"/"+operation, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				cwd := t.TempDir()
				options := agent.OpenOptions{Cwd: cwd, Features: test.features}
				var session agent.Session
				var argv []string
				var err error
				if operation == "resume" {
					provider, capture := startClaudeResumeExecutionProvider(
						t, ctx, cwd, claudeResumePeerSessionID,
						resumeInitFrame(t, claudeResumePeerSessionID, cwd),
					)
					session, err = provider.Resume(ctx, agent.ResumeOptions{
						SessionID:   claudeResumePeerSessionID,
						OpenOptions: options,
					})
					require.NoError(t, err)
					argv = readClaudeArgv(t, capture)
				} else {
					provider, capture := startClaudeExecutionProvider(t, ctx, mockClaudeInit...)
					session, err = provider.Open(ctx, options)
					require.NoError(t, err)
					argv = capture.argv(t)
				}
				closeSessionOnCleanup(t, session)
				options.Features.RestrictLocalTools = false
				assert.Equal(t, test.effective, session.EffectiveFeatures())
				assert.Equal(t, test.effective.AgentQuestions, session.Capabilities().Supports(agent.CapabilityForm))
				var denyArgs []string
				for _, arg := range argv {
					if strings.HasPrefix(arg, "--disallowedTools=") {
						denyArgs = append(denyArgs, arg)
					}
					assert.False(t, strings.HasPrefix(arg, "--tools="))
				}
				if test.deny == "" {
					assert.Empty(t, denyArgs)
				} else {
					assert.Equal(t, []string{test.deny}, denyArgs)
				}
				require.NoError(t, session.Close(ctx))
			})
		}
	}
}
