package agentclaude_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
)

func TestClaudeOpenPassesLiteralExecutionOptionsAndReportsInitModel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-open",`+
			`"cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.258",`+
			`"model":" observed/model "}`,
		`<{"type":"assistant","session_id":"native-open","message":{`+
			`"id":"later-output","role":"assistant","model":" later/output ",`+
			`"content":[{"type":"text","text":"effort is Native/High",`+
			`"apiBlockIndex":0}]}}`,
	)
	provider, capture := startClaudeExecutionProvider(t, ctx, frames...)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: cwd,
		Execution: agent.ExecutionOptions{
			Model:           " Caller/Model v2 ",
			ReasoningEffort: " Native/High ",
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)

	assert.Equal(t, claudeExpectedArgs(
		"--model= Caller/Model v2 ",
		"--effort= Native/High ",
	), claudeCapturedArgv(t, capture))

	stream, err := session.Events()
	require.NoError(t, err)
	event, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventAgentMessage, event.Type())

	report := claudeExecutionOptionsReport(t, session)
	assert.Equal(t, " Caller/Model v2 ", report.Model.Requested)
	require.NotNil(t, report.Model.Effective)
	assert.Equal(t, " observed/model ", *report.Model.Effective)
	assert.Equal(t, " Native/High ", report.ReasoningEffort.Requested)
	assert.Nil(t, report.ReasoningEffort.Effective)
}

func TestClaudeOpenAndResumeOmitEmptyExecutionOptions(t *testing.T) {
	tests := []struct {
		name      string
		resume    bool
		sessionID string
		wantArgs  []string
	}{
		{
			name:      "open",
			sessionID: "native-open",
			wantArgs:  claudeExpectedArgs(),
		},
		{
			name:      "resume",
			resume:    true,
			sessionID: "native-resume",
			wantArgs:  claudeExpectedArgs("--resume=native-resume"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			frames := append([]string(nil), mockClaudeInit...)
			frames = append(frames,
				`<{"type":"system","subtype":"init","session_id":`+
					jsonString(t, test.sessionID)+`,"cwd":`+jsonString(t, cwd)+
					`,"claude_code_version":"2.1.258","model":" default/model "}`,
			)
			provider, capture := startClaudeExecutionProvider(t, ctx, frames...)
			var session agent.Session
			var err error
			if test.resume {
				session, err = provider.Resume(ctx, agent.ResumeOptions{
					SessionID:   test.sessionID,
					OpenOptions: agent.OpenOptions{Cwd: cwd},
				})
			} else {
				session, err = provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
			}
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)

			assert.Equal(t, test.wantArgs, claudeCapturedArgv(t, capture))
			report := claudeExecutionOptionsReport(t, session)
			assert.Empty(t, report.Model.Requested)
			require.NotNil(t, report.Model.Effective)
			assert.Equal(t, " default/model ", *report.Model.Effective)
			assert.Empty(t, report.ReasoningEffort.Requested)
			assert.Nil(t, report.ReasoningEffort.Effective)
		})
	}
}

func TestClaudeOpenPassesExecutionFieldsIndependently(t *testing.T) {
	tests := []struct {
		name       string
		execution  agent.ExecutionOptions
		wantArgs   []string
		wantModel  string
		wantEffort string
	}{
		{
			name:      "model only",
			execution: agent.ExecutionOptions{Model: "model-only"},
			wantArgs:  claudeExpectedArgs("--model=model-only"),
			wantModel: "model-only",
		},
		{
			name:       "effort only",
			execution:  agent.ExecutionOptions{ReasoningEffort: "effort-only"},
			wantArgs:   claudeExpectedArgs("--effort=effort-only"),
			wantEffort: "effort-only",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			frames := append([]string(nil), mockClaudeInit...)
			frames = append(frames,
				`<{"type":"system","subtype":"init","session_id":"native-open",`+
					`"cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.258",`+
					`"model":"default/model"}`,
			)
			provider, capture := startClaudeExecutionProvider(t, ctx, frames...)
			session, err := provider.Open(ctx, agent.OpenOptions{
				Cwd:       cwd,
				Execution: test.execution,
			})
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)

			assert.Equal(t, test.wantArgs, claudeCapturedArgv(t, capture))
			report := claudeExecutionOptionsReport(t, session)
			assert.Equal(t, test.wantModel, report.Model.Requested)
			require.NotNil(t, report.Model.Effective)
			assert.Equal(t, "default/model", *report.Model.Effective)
			assert.Equal(t, test.wantEffort, report.ReasoningEffort.Requested)
			assert.Nil(t, report.ReasoningEffort.Effective)
		})
	}
}

func TestClaudeResumePassesLiteralExecutionOptionsAndPreservesIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-resume",`+
			`"cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.258",`+
			`"model":" resumed/model "}`,
	)
	provider, capture := startClaudeExecutionProvider(t, ctx, frames...)
	session, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID: "native-resume",
		OpenOptions: agent.OpenOptions{
			Cwd: cwd,
			Execution: agent.ExecutionOptions{
				Model:           " Resume/Model ",
				ReasoningEffort: " Native/Resume ",
			},
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)

	assert.Equal(t, "native-resume", session.ID())
	assert.Equal(t, claudeExpectedArgs(
		"--model= Resume/Model ",
		"--effort= Native/Resume ",
		"--resume=native-resume",
	), claudeCapturedArgv(t, capture))
	report := claudeExecutionOptionsReport(t, session)
	assert.Equal(t, " Resume/Model ", report.Model.Requested)
	require.NotNil(t, report.Model.Effective)
	assert.Equal(t, " resumed/model ", *report.Model.Effective)
	assert.Equal(t, " Native/Resume ", report.ReasoningEffort.Requested)
	assert.Nil(t, report.ReasoningEffort.Effective)
}

func TestClaudeOpenPreservesNativeExecutionRejection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := []string{
		`>{"type":"control_request","request_id":"init",` +
			`"request":{"subtype":"initialize","hooks":null,"forwardSubagentText":true}}`,
		`<{"type":"control_response","response":{"subtype":"error",` +
			`"request_id":"init","error":"model rejected"}}`,
	}
	provider, _ := startClaudeExecutionProvider(t, ctx, frames...)

	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: t.TempDir(),
		Execution: agent.ExecutionOptions{
			Model: "rejected/model",
		},
	})
	require.Error(t, err)
	assert.Nil(t, session)
	assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(err))
}

func TestClaudeExecutionReporterRequiresInitModelObservation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-open",`+
			`"cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
	)
	provider, _ := startClaudeExecutionProvider(t, ctx, frames...)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: cwd,
		Execution: agent.ExecutionOptions{
			Model:           "requested/model",
			ReasoningEffort: "requested-effort",
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)

	report := claudeExecutionOptionsReport(t, session)
	assert.Equal(t, "requested/model", report.Model.Requested)
	assert.Nil(t, report.Model.Effective)
	assert.Equal(t, "requested-effort", report.ReasoningEffort.Requested)
	assert.Nil(t, report.ReasoningEffort.Effective)
}

func startClaudeExecutionProvider(
	t *testing.T,
	ctx context.Context,
	frames ...string,
) (*agentclaude.Provider, string) {
	t.Helper()
	peerSession := prepareClaudePeer(t, "passed", frames...)
	launcher, env, capture := launchClaudeMockCapturingArgs(t, peerSession)
	provider := agentclaude.New(agentclaude.Config{CLIPath: launcher, Env: env})
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	return provider, capture
}

func claudeExpectedArgs(extras ...string) []string {
	args := []string{
		"--output-format", "stream-json",
		"--verbose",
		"--input-format", "stream-json",
		"--permission-prompt-tool", "stdio",
		"--include-partial-messages",
	}
	return append(args, extras...)
}

func claudeCapturedArgv(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var argv []string
	require.NoError(t, json.Unmarshal(data, &argv))
	return argv
}

func claudeExecutionOptionsReport(
	t *testing.T,
	session agent.Session,
) agent.ExecutionOptionsReport {
	t.Helper()
	reporter, ok := session.(agent.ExecutionOptionsReporter)
	require.True(t, ok, "Claude session must report execution options")
	return reporter.EffectiveExecutionOptions()
}
