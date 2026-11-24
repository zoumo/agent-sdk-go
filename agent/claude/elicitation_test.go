package agentclaude_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
)

// Synthetic control frames based on @anthropic-ai/claude-agent-sdk 0.3.293,
// package/sdk.d.ts SDKControlElicitationRequest and package/sdk.mjs control handling.
func claudeElicitationFrame(fields string) string {
	return `<{"type":"control_request","request_id":"outer","request":{"subtype":"elicitation","mcp_server_name":"sample","message":"Provide details"` + fields + `}}`
}

func openClaudeElicitation(t *testing.T, ctx context.Context, fields string, after ...string) (agent.Session, agent.EventStream) {
	t.Helper()
	cwd := t.TempDir()
	frames := []string{
		claudeSessionInitFrame(t, "elicit", cwd),
		claudeStartOrSteerFrame(t, "run"),
		claudeStateFrame("elicit", "running", "2026-11-07T14:00:00Z"),
		claudeElicitationFrame(fields),
	}
	session, stream := openClaudeInteractionSession(t, ctx, cwd, "passed", agent.Features{AgentQuestions: new(false)}, append(frames, after...)...)
	_, err := session.StartOrSteer(ctx, claudeTextInput("run"))
	require.NoError(t, err)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))
	return session, stream
}

func TestClaudeElicitationRoundTrip(t *testing.T) {
	const schema = `,"requested_schema":{"type":"object","properties":{"n":{"type":"integer","maximum":9007199254740993}}}`
	const form = `,"mode":"form"` + schema
	const url = `,"mode":"url","url":"https://example.test/auth","elicitation_id":"external-id"`
	for _, tc := range []struct {
		name    string
		fields  string
		kind    agent.InteractionKind
		answer  agent.Answer
		result  string
		outcome agent.InteractionOutcome
	}{
		{"omitted mode", schema, agent.InteractionForm, agent.FormAnswer{Outcome: agent.FormAccept, Content: map[string]any{"n": json.Number("9007199254740993")}}, `{"action":"accept","content":{"n":9007199254740993}}`, agent.InteractionAnswered},
		{"form accept", form, agent.InteractionForm, agent.FormAnswer{Outcome: agent.FormAccept, Content: map[string]any{"n": 3}}, `{"action":"accept","content":{"n":3}}`, agent.InteractionAnswered},
		{"empty form", "", agent.InteractionForm, agent.FormAnswer{Outcome: agent.FormAccept, Content: map[string]any{}}, `{"action":"accept","content":{}}`, agent.InteractionAnswered},
		{"form decline", form, agent.InteractionForm, agent.FormAnswer{Outcome: agent.FormDecline}, `{"action":"decline"}`, agent.InteractionAnswered},
		{"form cancel", form, agent.InteractionForm, agent.FormAnswer{Outcome: agent.FormCancel}, `{"action":"cancel"}`, agent.InteractionCancelled},
		{"url decline", url, agent.InteractionURL, agent.URLAnswer{Outcome: agent.URLDecline}, `{"action":"decline"}`, agent.InteractionAnswered},
		{"url cancel", url, agent.InteractionURL, agent.URLAnswer{Outcome: agent.URLCancel}, `{"action":"cancel"}`, agent.InteractionCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			session, stream := openClaudeElicitation(t, ctx, tc.fields,
				`>{"type":"control_response","response":{"subtype":"success","request_id":"outer","response":`+tc.result+`}}`,
				claudeStateFrame("elicit", "running", "2026-11-07T14:00:03Z"),
				claudeSubmissionFrame(t, 2, "finish"),
			)
			require.Equal(t, agent.StateRequiresAction, nextStateValue(t, ctx, stream))
			req := nextClaudeInteractionRequested(t, ctx, stream)
			require.NotEmpty(t, req.Interaction)
			assert.True(t, req.Blocking)
			assert.Equal(t, tc.kind, req.Request.Kind)
			assert.Empty(t, req.Request.ToolCallID)
			assert.Equal(t, "Provide details", req.Request.Message)
			switch {
			case tc.kind == agent.InteractionURL:
				assert.Equal(t, "https://example.test/auth", req.Request.URL)
			case tc.fields == "":
				assert.Equal(t, map[string]any{}, req.Request.RequestedSchema)
			default:
				assert.Equal(t, map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "integer", "maximum": json.Number("9007199254740993")}}}, req.Request.RequestedSchema)
			}

			_, err := session.Answer(ctx, req.Interaction, agent.PermissionAnswer{Outcome: agent.PermissionCancelled})
			requireAgentErrorReason(t, err, "answer_kind_mismatch")
			if tc.kind == agent.InteractionForm {
				receipt, err := session.Answer(ctx, req.Interaction, agent.FormAnswer{Outcome: agent.FormAccept, Content: map[string]any{"n": map[string]any{}}})
				require.Error(t, err)
				assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
				require.Equal(t, new(false), receipt.Acceptance.Sent)
			}
			receipt, err := session.Answer(ctx, req.Interaction, tc.answer)
			require.NoError(t, err)
			assert.Equal(t, req.Interaction, receipt.Interaction)
			assert.Equal(t, new(true), receipt.Acceptance.Sent)
			closed := nextClaudeInteractionClosed(t, ctx, stream)
			assert.Equal(t, req.Interaction, closed.Interaction)
			assert.Equal(t, tc.outcome, closed.Outcome)
			assert.Equal(t, "native_state_advanced", closed.Reason)
			_, err = session.Answer(ctx, req.Interaction, tc.answer)
			requireAgentErrorReason(t, err, agent.ErrorReasonInvalidInteractionRef)
			_, err = session.StartOrSteer(ctx, claudeTextInput("finish"))
			require.NoError(t, err)
			require.NoError(t, session.Close(ctx))
		})
	}
}

func TestClaudeAcceptedURLWaitsForMatchingCompletion(t *testing.T) {
	const fields = `,"mode":"url","url":"https://example.test/auth","elicitation_id":"external-id"`
	const answer = `>{"type":"control_response","response":{"subtype":"success","request_id":"outer","response":{"action":"accept"}}}`
	completion := func(sessionID, server, id string) string {
		return `<{"type":"system","subtype":"elicitation_complete","session_id":"` + sessionID +
			`","mcp_server_name":"` + server + `","elicitation_id":"` + id + `"}`
	}
	matching := completion("elicit", "sample", "external-id")
	running := claudeStateFrame("elicit", "running", "2026-11-07T14:00:03Z")
	for _, beforeState := range []bool{true, false} {
		name := "after state advances"
		if beforeState {
			name = "before state advances"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			frames := []string{answer, completion("elicit", "other", "external-id"), completion("elicit", "sample", "other")}
			if beforeState {
				frames = append(frames, matching, running)
			} else {
				frames = append(frames, running, matching)
			}
			idle := claudeStateFrame("elicit", "idle", "2026-11-07T14:00:04Z")
			frames = append(frames, matching, idle, claudeSubmissionFrame(t, 2, "finish"))
			session, stream := openClaudeElicitation(t, ctx, fields, frames...)
			require.Equal(t, agent.StateRequiresAction, nextStateValue(t, ctx, stream))
			req := nextClaudeInteractionRequested(t, ctx, stream)
			receipt, err := session.Answer(ctx, req.Interaction, agent.URLAnswer{Outcome: agent.URLAccept})
			require.NoError(t, err)
			require.Equal(t, new(true), receipt.Acceptance.Sent)
			for _, unmatched := range frames[1:3] {
				ev := nextEvent(t, ctx, stream)
				require.Equal(t, agent.UnsupportedNativeEventPayload{
					AgentType: "claude", Raw: json.RawMessage(unmatched[1:]),
				}, ev.Payload)
			}
			if !beforeState {
				require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))
			}
			closed := nextClaudeInteractionClosed(t, ctx, stream)
			assert.Equal(t, req.Interaction, closed.Interaction)
			assert.Equal(t, agent.InteractionCompleted, closed.Outcome)
			if beforeState {
				require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))
			}
			require.Equal(t, agent.StateIdle, nextStateValue(t, ctx, stream))
			_, err = session.StartOrSteer(ctx, claudeTextInput("finish"))
			require.NoError(t, err)
			require.NoError(t, session.Close(ctx))
			_, err = stream.Next(ctx)
			require.ErrorIs(t, err, io.EOF)
		})
	}
}

func TestClaudeElicitationNativeCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	session, stream := openClaudeElicitation(t, ctx, `,"mode":"url","url":"https://example.test"`,
		`<{"type":"control_cancel_request","request_id":"outer"}`,
		claudeSubmissionFrame(t, 2, "finish"),
	)
	require.Equal(t, agent.StateRequiresAction, nextStateValue(t, ctx, stream))
	req := nextClaudeInteractionRequested(t, ctx, stream)
	closed := nextClaudeInteractionClosed(t, ctx, stream)
	assert.Equal(t, req.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionCancelled, closed.Outcome)
	assert.Equal(t, "native_cancel", closed.Reason)
	_, err := session.Answer(ctx, req.Interaction, agent.URLAnswer{Outcome: agent.URLAccept})
	requireAgentErrorReason(t, err, agent.ErrorReasonInvalidInteractionRef)
	_, err = session.StartOrSteer(ctx, claudeTextInput("finish"))
	require.NoError(t, err)
	require.NoError(t, session.Close(ctx))
}

func TestClaudeAcceptedURLExpiresOnSessionClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	session, stream := openClaudeElicitation(t, ctx, `,"mode":"url","url":"https://example.test","elicitation_id":"external-id"`,
		`>{"type":"control_response","response":{"subtype":"success","request_id":"outer","response":{"action":"accept"}}}`,
		claudeStateFrame("elicit", "idle", "2026-11-07T14:00:03Z"),
		claudeSubmissionFrame(t, 2, "finish"),
	)
	require.Equal(t, agent.StateRequiresAction, nextStateValue(t, ctx, stream))
	req := nextClaudeInteractionRequested(t, ctx, stream)
	receipt, err := session.Answer(ctx, req.Interaction, agent.URLAnswer{Outcome: agent.URLAccept})
	require.NoError(t, err)
	require.Equal(t, new(true), receipt.Acceptance.Sent)
	require.Equal(t, agent.StateIdle, nextStateValue(t, ctx, stream))
	_, err = session.StartOrSteer(ctx, claudeTextInput("finish"))
	require.NoError(t, err)
	require.NoError(t, session.Close(ctx))
	closed := nextClaudeInteractionClosed(t, ctx, stream)
	assert.Equal(t, req.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionExpired, closed.Outcome)
	assert.Equal(t, agent.ErrorReasonSessionClosed, closed.Reason)
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestClaudeElicitationSessionClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{CWD: cwd, BeforeInput: []string{
		`{"type":"system","subtype":"session_state_changed","session_id":"__SESSION_ID__","state":"running"}`,
		strings.TrimPrefix(claudeElicitationFrame(`,"mode":"url","url":"https://example.test"`), "<"),
	}})
	provider := agentclaude.New(agentclaude.Config{CLIPath: peer.CLIPath, Env: peer.Env})
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = provider.Stop(context.Background()) })
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)
	requireStartupForwardingGap(t, ctx, stream)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextStateValue(t, ctx, stream))
	req := nextClaudeInteractionRequested(t, ctx, stream)
	_, err = session.Answer(ctx, "outer", agent.URLAnswer{Outcome: agent.URLAccept})
	requireAgentErrorReason(t, err, agent.ErrorReasonInvalidInteractionRef)
	require.NoError(t, session.Close(ctx))
	closed := nextClaudeInteractionClosed(t, ctx, stream)
	assert.Equal(t, req.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionCancelled, closed.Outcome)
	assert.Equal(t, agent.ErrorReasonSessionClosed, closed.Reason)
	_, err = session.Answer(ctx, req.Interaction, agent.URLAnswer{Outcome: agent.URLAccept})
	requireAgentErrorReason(t, err, agent.ErrorReasonSessionClosed)
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
	assert.Empty(t, readOpenPeerObservation(t, peer.Observation).Responses)
}

func TestClaudeElicitationInvalidRequestDeclined(t *testing.T) {
	for _, fields := range []string{`,"mode":"url"`, `,"mode":"future"`, `,"mode":null`, `,"requested_schema":[]`} {
		t.Run(fields, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			session, stream := openClaudeElicitation(t, ctx, fields,
				`>{"type":"control_response","response":{"subtype":"success","request_id":"outer","response":{"action":"decline"}}}`,
				claudeSubmissionFrame(t, 2, "finish"),
			)
			gap := nextEvent(t, ctx, stream)
			assert.Equal(t, agent.EventSessionError, gap.Type())
			assert.Equal(t, true, requireClaudeMeta(t, gap)["requestRejected"])
			_, err := session.StartOrSteer(ctx, claudeTextInput("finish"))
			require.NoError(t, err)
			require.NoError(t, session.Close(ctx))
		})
	}
}
