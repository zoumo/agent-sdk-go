package agentclaude

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/claude"
	claudetypes "github.com/zoumo/agent-sdk-go/claude/types"
)

func TestConfigIsCopiedAndNativeOptionsTargetResume(t *testing.T) {
	forward := true
	env := []string{}
	config := Config{
		CLIPath:   "claude-fixture",
		Env:       env,
		ConfigDir: "/explicit/claude-config",
		Initialize: &claudetypes.InitializeOptions{
			Hooks:               json.RawMessage(`{"hook":"fixed"}`),
			Agents:              json.RawMessage(`{"agent":"fixed"}`),
			ForwardSubagentText: &forward,
		},
		ChildDetailPolicy: claude.ChildDetailPreserve,
		Subagents: claude.SubagentOptions{
			History: []claude.SubagentHistory{{SessionID: "session", TaskID: "task"}},
		},
	}
	provider := New(config)

	config.Env = append(config.Env, "MUTATED=1")
	config.ConfigDir = "/mutated"
	forward = false
	config.Initialize.Hooks[1] = 'X'
	config.Initialize.Agents[1] = 'X'
	config.Subagents.History[0].TaskID = "mutated"

	assert.NotNil(t, provider.config.Env)
	assert.Empty(t, provider.config.Env)
	assert.Equal(t, "/explicit/claude-config", provider.config.ConfigDir)
	require.NotNil(t, provider.config.Initialize)
	require.NotNil(t, provider.config.Initialize.ForwardSubagentText)
	assert.True(t, *provider.config.Initialize.ForwardSubagentText)
	assert.JSONEq(t, `{"hook":"fixed"}`, string(provider.config.Initialize.Hooks))
	assert.JSONEq(t, `{"agent":"fixed"}`, string(provider.config.Initialize.Agents))
	assert.Equal(t, "task", provider.config.Subagents.History[0].TaskID)

	options := provider.nativeOptions(
		"/workspace",
		"resume-id",
		agent.Features{},
		nil,
		agent.ExecutionOptions{},
	)
	assert.Equal(t, "claude-fixture", options.CLIPath)
	assert.Equal(t, "/workspace", options.WorkDir)
	assert.Equal(t, "resume-id", options.ResumeID)
	assert.Equal(t, claude.ChildDetailPreserve, options.ChildDetailPolicy)
}

func TestNilEnvironmentStaysNil(t *testing.T) {
	provider := New(Config{})
	assert.Nil(t, provider.config.Env)
}

func TestNativeOptionsDisableQuestionsOnlyWhenRequested(t *testing.T) {
	provider := New(Config{})
	enabled := true
	disabled := false

	assert.Empty(t,
		provider.nativeOptions(
			"/workspace",
			"",
			agent.Features{AgentQuestions: &enabled},
			nil,
			agent.ExecutionOptions{},
		).DisallowedTools,
	)
	assert.Equal(t,
		[]string{"AskUserQuestion"},
		provider.nativeOptions(
			"/workspace",
			"",
			agent.Features{AgentQuestions: &disabled},
			nil,
			agent.ExecutionOptions{},
		).DisallowedTools,
	)
}

func TestFormAnswersValidatesNativeQuestionShapes(t *testing.T) {
	questions := map[string]claude.Question{
		"Which format?":   {},
		"Which sections?": {MultiSelect: true},
	}
	tests := []struct {
		name    string
		content map[string]any
		code    agent.ErrorCode
		reason  string
	}{
		{
			name:    "missing answers",
			content: map[string]any{},
			code:    agent.ErrorInvalidArgument,
			reason:  agent.ErrorReasonInvalidFormAnswer,
		},
		{
			name: "unknown question",
			content: map[string]any{"answers": map[string]any{
				"Other?": "json",
			}},
			code:   agent.ErrorInvalidArgument,
			reason: agent.ErrorReasonUnknownFormQuestion,
		},
		{
			name: "array for single select",
			content: map[string]any{"answers": map[string]any{
				"Which format?": []any{"json", "yaml"},
			}},
			code:   agent.ErrorInvalidArgument,
			reason: agent.ErrorReasonInvalidFormAnswer,
		},
		{
			name: "non-string multi select label",
			content: map[string]any{"answers": map[string]any{
				"Which sections?": []any{"intro", json.Number("1")},
			}},
			code:   agent.ErrorInvalidArgument,
			reason: agent.ErrorReasonInvalidFormAnswer,
		},
		{
			name: "non string answer",
			content: map[string]any{"answers": map[string]any{
				"Which format?": json.Number("1"),
			}},
			code:   agent.ErrorInvalidArgument,
			reason: agent.ErrorReasonInvalidFormAnswer,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := formAnswers(test.content, questions)
			require.Error(t, err)
			assert.Equal(t, test.code, agent.CodeOf(err))
			var classified *agent.Error
			require.ErrorAs(t, err, &classified)
			assert.Equal(t, test.reason, classified.Reason)
		})
	}

	answers, err := formAnswers(map[string]any{
		"answers": map[string]any{
			"Which format?":   "json",
			"Which sections?": []any{"intro", "end"},
		},
	}, questions)
	require.NoError(t, err)
	format := "json"
	sections := "intro, end"
	assert.Equal(t, map[string]claude.QuestionAnswer{
		"Which format?":   {Text: &format},
		"Which sections?": {Text: &sections},
	}, answers)

	for _, labels := range []any{[]any{"intro", "end"}, []string{"intro", "end"}} {
		answers, err := formAnswers(map[string]any{
			"answers": map[string]any{"Which sections?": labels},
		}, questions)
		require.NoError(t, err)
		assert.Equal(t, map[string]claude.QuestionAnswer{
			"Which sections?": {Text: &sections},
		}, answers)
	}
}

func TestAnswerTerminalOutcomePreservesUnknownWrite(t *testing.T) {
	interaction := &pendingInteraction{}
	outcome, reason := answerTerminalOutcome(
		interaction,
		errors.Join(claude.ErrPermissionCancelled, claude.ErrUnknownOutcome),
	)
	assert.Equal(t, agent.InteractionFailed, outcome)
	assert.Equal(t, agent.ErrorReasonAnswerOutcomeUnknown, reason)
}

func TestInitBufferRejectsExcessEventsAndBytes(t *testing.T) {
	t.Run("events", func(t *testing.T) {
		var buffer initBuffer
		for range maxInitBufferedEvents {
			require.NoError(t, buffer.add(claude.Received{}))
		}
		err := buffer.add(claude.Received{})
		assert.Equal(t, agent.ErrorResourceExhausted, agent.CodeOf(err))
	})

	t.Run("bytes", func(t *testing.T) {
		var buffer initBuffer
		err := buffer.add(claude.Received{Raw: make([]byte, maxInitBufferedBytes+1)})
		assert.Equal(t, agent.ErrorResourceExhausted, agent.CodeOf(err))
	})
}

// The pinned embedded bridge validates a reviewed startup declaration, not the
// complete executable argv. SDK feature and MCP options still reach OpenStreams
// intact.
func TestEmbeddedStartupDeclarationKeepsFullSDKFeatureOptions(t *testing.T) {
	options := claude.SessionOptions{
		Model:           "synthetic",
		ResumeID:        "resume-id",
		DisallowedTools: []string{"AskUserQuestion"},
		MCPServers: []claude.MCPServer{{
			Name:      "fixture",
			Transport: claude.MCPTransportStdio,
			Command:   "fixture-mcp",
		}},
	}
	declaration, err := TestifyNativeArgs(options)
	require.NoError(t, err)
	assert.Contains(t, declaration, "--model=synthetic")
	assert.Contains(t, declaration, "--resume=resume-id")
	assert.NotContains(t, declaration, "--disallowedTools=AskUserQuestion")
	assert.NotContains(t, strings.Join(declaration, "\n"), "--mcp-config=")
	full, err := claude.NativeArgs(options)
	require.NoError(t, err)
	assert.Contains(t, full, "--disallowedTools=AskUserQuestion")
	assert.Contains(t, strings.Join(full, "\n"), "--mcp-config=")
	assert.Equal(t, []string{"AskUserQuestion"}, options.DisallowedTools)
}
