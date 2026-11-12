package agentclaude_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
)

func TestInjectWritesContextWithoutCreatingInterruptTarget(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := []string{
		claudeSessionInitFrame(t, "native-inject", cwd),
		claudeInjectFrame(t, "context"),
		`<{"type":"result","subtype":"success","session_id":"native-inject","num_turns":0,"is_error":false,"result":""}`,
		claudeStartOrSteerFrame(t, "query"),
		claudeStateFrame("native-inject", "running", "2026-11-11T10:00:00Z"),
		claudeInjectFrame(t, "more"),
		`<{"type":"result","subtype":"success","session_id":"native-inject","num_turns":0,"is_error":false,"result":""}`,
		claudeStateFrame("native-inject", "running", "2026-11-11T10:00:01Z"),
		`>{"type":"control_request","request_id":"go_1","request":{"subtype":"interrupt"}}`,
		`<{"type":"control_response","response":{"subtype":"success","request_id":"go_1","response":{}}}`,
	}
	session, stream := openClaudeInteractionSession(t, ctx, cwd, "passed", agent.Features{}, frames...)
	require.Eventually(t, func() bool {
		return agentclaude.InjectSupportedForTest(session)
	}, 3*time.Second, time.Millisecond)
	injector, ok := session.(agent.Injector)
	require.True(t, ok)
	receipt, err := injector.Inject(ctx, claudeTextInput("context"))
	require.NoError(t, err)
	assert.Equal(t, agent.Acceptance{Sent: new(true)}, receipt.Acceptance)
	interrupted, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	assert.Equal(t, agent.InterruptNoActiveWork, interrupted.Result)
	queried, err := session.StartOrSteer(ctx, claudeTextInput("query"))
	require.NoError(t, err)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))
	_, err = injector.Inject(ctx, claudeTextInput("more"))
	require.NoError(t, err)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))
	interrupted, err = session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	assert.Equal(t, queried.Submission, interrupted.Target)
}

func TestInjectRejectsUnconfirmedOrOldClaudeVersion(t *testing.T) {
	for _, version := range []string{"", "2.1.258", "2.1.292", "2.1.293-beta"} {
		t.Run(version, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			init := strings.Replace(claudeSessionInitFrame(t, "native-version", cwd),
				`"claude_code_version":"2.1.293"`, `"claude_code_version":"`+version+`"`, 1)
			session, _ := openClaudeInteractionSession(t, ctx, cwd, "passed", agent.Features{}, init)
			receipt, err := session.(agent.Injector).Inject(ctx, claudeTextInput("context"))
			require.Error(t, err)
			assert.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
			assert.Equal(t, agent.Acceptance{Sent: new(false)}, receipt.Acceptance)
		})
	}
}

func TestInjectRejectsLocalInvalidCanceledAndClosedClaudeInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	session, _ := openClaudeInteractionSession(t, ctx, cwd, "passed", agent.Features{},
		claudeSessionInitFrame(t, "native-inject-invalid", cwd))
	require.Eventually(t, func() bool {
		return agentclaude.InjectSupportedForTest(session)
	}, 3*time.Second, time.Millisecond)
	injector := session.(agent.Injector)
	receipt, err := injector.Inject(ctx, agent.Input{})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
	assert.Equal(t, agent.Acceptance{Sent: new(false)}, receipt.Acceptance)
	multiple := claudeTextInput("first")
	multiple.Content = append(multiple.Content, agent.ContentBlock{Text: &agent.ContentBlockText{Text: "second"}})
	receipt, err = injector.Inject(ctx, multiple)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
	assert.Equal(t, agent.Acceptance{Sent: new(false)}, receipt.Acceptance)
	annotated := claudeTextInput("annotated")
	annotated.Content[0].Text.Annotations = &agent.Annotations{}
	_, err = injector.Inject(ctx, annotated)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
	canceled, stop := context.WithCancel(ctx)
	stop()
	receipt, err = injector.Inject(canceled, claudeTextInput("canceled"))
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, agent.Acceptance{Sent: new(false)}, receipt.Acceptance)
	require.NoError(t, session.Close(ctx))
	_, err = injector.Inject(ctx, claudeTextInput("closed"))
	require.Error(t, err)
	assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
}

func claudeInjectFrame(t *testing.T, text string) string {
	t.Helper()
	return `>{"message":{"role":"user","content":` + jsonString(t, text) +
		`},"parent_tool_use_id":null,"session_id":"","shouldQuery":false,"type":"user"}`
}
