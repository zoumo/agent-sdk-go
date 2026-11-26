package agentcodex_test

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentcodex "github.com/zoumo/agent-sdk-go/agent/codex"
)

type testSession interface {
	agent.Session
	agent.Archiver
	agent.ConfigController
}

func TestPermissionAnswerPreservesAuthorityUntilNativeClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := interactionSessionFrames(commandApprovalRequest("approval-1"))
	frames = append(frames,
		`>{"id":"approval-1","result":{"decision":"accept"}}`,
	)
	frames = append(frames, codexCloseFrames(94, "root-a")...)
	session, stream := openInteractionSession(t, ctx, frames, agent.Features{})

	submitInteractionTurn(t, ctx, session)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
	requested := nextInteractionRequested(t, ctx, stream)
	assert.Equal(t, agent.InteractionPermission, requested.Request.Kind)
	assert.Equal(t, "cmd-1", requested.Request.ToolCallID)
	require.Len(t, requested.Request.Options, 2)

	_, err := session.Answer(ctx, requested.Interaction, agent.FormAnswer{
		Outcome: agent.FormAccept,
		Content: map[string]any{"answers": map[string]any{}},
	})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))

	_, err = session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
		Outcome:  agent.PermissionSelected,
		OptionID: "not-offered",
	})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))

	_, err = session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{Outcome: agent.PermissionCancelled})
	var classified *agent.Error
	require.ErrorAs(t, err, &classified)
	assert.Equal(t, agent.ErrorUnsupported, classified.Code)
	assert.Equal(t, agentcodex.ErrorReasonPermissionCancelUnsupported, classified.Reason)

	receipt, err := session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
		Outcome:  agent.PermissionSelected,
		OptionID: "allow-once",
	})
	require.NoError(t, err)
	assert.Equal(t, requested.Interaction, receipt.Interaction)
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.True(t, *receipt.Acceptance.Sent)
	assert.Nil(t, receipt.Acceptance.NativeAccepted)
	assert.Nil(t, receipt.Acceptance.NativeRecorded)

	_, err = session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
		Outcome:  agent.PermissionSelected,
		OptionID: "allow-once",
	})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))

	require.NoError(t, session.Close(ctx))
	closed := nextInteractionClosed(t, ctx, stream)
	assert.Equal(t, requested.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionAnswered, closed.Outcome)
	assert.Equal(t, agent.ErrorReasonSessionClosed, closed.Reason)
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestFileChangeApprovalUsesNativeOfferedDecision(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const request = `<{"id":"file-approval","method":"item/fileChange/requestApproval","params":{"threadId":"root-a","turnId":"turn-a","itemId":"file-1","startedAtMs":1700000200000,"reason":"write access"}}`
	frames := interactionSessionFrames(request)
	frames = append(frames,
		`>{"id":"file-approval","result":{"decision":"acceptForSession"}}`,
	)
	frames = append(frames, codexCloseFrames(94, "root-a")...)
	session, stream := openInteractionSession(t, ctx, frames, agent.Features{})

	submitInteractionTurn(t, ctx, session)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
	requested := nextInteractionRequested(t, ctx, stream)
	assert.Equal(t, "file-1", requested.Request.ToolCallID)
	assert.Equal(t, "Approve file changes", requested.Request.Title)
	assert.Len(t, requested.Request.Options, 4)

	receipt, err := session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
		Outcome:  agent.PermissionSelected,
		OptionID: "allow-session",
	})
	require.NoError(t, err)
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.True(t, *receipt.Acceptance.Sent)

	require.NoError(t, session.Close(ctx))
	closed := nextInteractionClosed(t, ctx, stream)
	assert.Equal(t, agent.InteractionAnswered, closed.Outcome)
	assert.Equal(t, agent.ErrorReasonSessionClosed, closed.Reason)
}

func TestDisabledAgentQuestionsRejectsNativeForm(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"config":{"tools.experimental_request_user_input.enabled":false},"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"run","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		codexTurnStartedFrame,
		formRequest("form-1", "turn-a", true),
		`>{"id":"form-1","error":{"code":-32602,"message":"Unsupported by the unified agent adapter"}}`,
	)
	frames = append(frames, codexCloseFrames(94, "root-a")...)
	disabled := false
	session, stream := openInteractionSession(t, ctx, frames, agent.Features{
		AgentQuestions: &disabled,
	})

	assert.False(t, session.EffectiveFeatures().AgentQuestions)
	assert.True(t, session.Capabilities().Supports(agent.CapabilityForm))
	submitInteractionTurn(t, ctx, session)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	gap := nextEvent(t, ctx, stream)
	require.Equal(t, agent.EventSessionError, gap.Type())
	assert.Equal(t, agent.ErrorReasonAgentQuestionsDisabled, sessionErrorReason(t, gap))
	assert.Equal(t, true, eventCodexMeta(t, gap)["requestRejected"])

	_, err := session.Answer(ctx, agent.InteractionRef("forged"), agent.FormAnswer{
		Outcome: agent.FormAccept,
		Content: map[string]any{"answers": map[string]any{}},
	})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
}

func TestPermissionProfileApprovalRoundTrip(t *testing.T) {
	// Synthetic protocol peer. Profile extensions belong to the grant; request
	// extensions (including response-like fields) do not.
	const profile = `{"fileSystem":{"entries":[{"path":{"type":"path","path":"/synthetic/output"},"access":"write"}],"globScanMaxDepth":9007199254740993,"read":null},"network":{"enabled":true,"futureLimit":9007199254740995},"futureProfile":{"extra":null}}`
	for _, tc := range []struct {
		id, response string
	}{
		{"allow-turn", `{"permissions":` + profile + `,"scope":"turn"}`},
		{"allow-session", `{"permissions":` + profile + `,"scope":"session"}`},
		{"reject-once", `{"permissions":{}}`},
	} {
		t.Run(tc.id, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			frames := interactionSessionFrames(
				`<{"id":"permissions","method":"item/permissions/requestApproval","params":{"cwd":"/synthetic","itemId":"permissions-item","permissions":`+profile+`,"reason":"Export the result","startedAtMs":1700000200000,"threadId":"root-a","turnId":"turn-a","scope":"always","strictAutoReview":true}}`,
				`>{"id":"permissions","result":`+tc.response+`}`,
				`<{"method":"serverRequest/resolved","params":{"threadId":"root-a","requestId":"permissions"}}`,
			)
			frames = append(frames, codexCloseFrames(94, "root-a")...)
			session, stream := openInteractionSession(t, ctx, frames, agent.Features{})
			submitInteractionTurn(t, ctx, session)
			require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
			require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
			event := nextInteractionRequested(t, ctx, stream)
			req := event.Request
			assert.True(t, event.Blocking)
			assert.Equal(t, agent.InteractionPermission, req.Kind)
			assert.Equal(t, "permissions-item", req.ToolCallID)
			require.Len(t, req.Content, 2)
			require.NotNil(t, req.Content[0].Text)
			require.NotNil(t, req.Content[1].Text)
			display := req.Content[0].Text.Text
			assert.JSONEq(t, profile, display)
			assert.Contains(t, display, "9007199254740993")
			assert.Contains(t, display, "9007199254740995")
			assert.Equal(t, "Export the result", req.Content[1].Text.Text)

			var selected string
			require.Len(t, req.Options, 3)
			for _, option := range req.Options {
				switch option.OptionID {
				case "allow-turn":
					assert.Equal(t, agent.PermissionAllow, option.Kind)
					assert.Equal(t, agent.PermissionScopeTurn, option.Scope)
				case "allow-session":
					assert.Equal(t, agent.PermissionAllow, option.Kind)
					assert.Equal(t, agent.PermissionScopeSession, option.Scope)
				case "reject-once":
					assert.Equal(t, agent.PermissionReject, option.Kind)
					assert.Equal(t, agent.PermissionScopeOnce, option.Scope)
				default:
					t.Fatalf("unexpected option %q", option.OptionID)
				}
				if option.OptionID == tc.id {
					selected = option.OptionID
				}
			}
			require.NotEmpty(t, selected)

			// A consumer may change its local display, but only the registered
			// options and the original native grant authorize a reply.
			req.Content[0].Text.Text = `{"extraGrant":true}`
			req.Options[0].OptionID = "allow-always"
			_, err := session.Answer(ctx, event.Interaction, agent.PermissionAnswer{Outcome: agent.PermissionSelected, OptionID: "allow-always"})
			require.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
			_, err = session.Answer(ctx, event.Interaction, agent.PermissionAnswer{Outcome: agent.PermissionCancelled})
			require.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))

			receipt, err := session.Answer(ctx, event.Interaction, agent.PermissionAnswer{Outcome: agent.PermissionSelected, OptionID: selected})
			require.NoError(t, err)
			assert.Equal(t, new(true), receipt.Acceptance.Sent)
			closed := nextInteractionClosed(t, ctx, stream)
			assert.Equal(t, event.Interaction, closed.Interaction)
			assert.Equal(t, agent.InteractionAnswered, closed.Outcome)
			assert.Equal(t, "native_resolved", closed.Reason)
		})
	}
}

func TestPermissionProfileApprovalRejectsNullProfile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := interactionSessionFrames(
		`<{"id":"permissions-1","method":"item/permissions/requestApproval","params":{"cwd":"/synthetic","itemId":"permissions-item","permissions":null,"startedAtMs":1700000200000,"threadId":"root-a","turnId":"turn-a"}}`,
		`>{"id":"permissions-1","error":{"code":-32602,"message":"Unsupported by the unified agent adapter"}}`,
	)
	frames = append(frames, codexCloseFrames(94, "root-a")...)
	session, stream := openInteractionSession(t, ctx, frames, agent.Features{})

	submitInteractionTurn(t, ctx, session)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	gap := nextEvent(t, ctx, stream)
	require.Equal(t, agent.EventSessionError, gap.Type())
	assert.Equal(t, "invalid_native_request", sessionErrorReason(t, gap))
	assert.Equal(t, true, eventCodexMeta(t, gap)["requestRejected"])
}

func TestChildInteractionsRetainOriginalAuthority(t *testing.T) {
	tests := []struct {
		name     string
		request  string
		response string
		answer   agent.Answer
		kind     agent.InteractionKind
	}{
		{
			name:     "MCP tool approval",
			request:  mcpToolRequest(`"child-request"`, "child-a", "child-turn", `,"persist":["always","session"]`),
			response: `>{"id":"child-request","result":{"action":"accept","content":null,"_meta":{"persist":"session"}}}`,
			answer:   agent.PermissionAnswer{Outcome: agent.PermissionSelected, OptionID: "allow-session"},
			kind:     agent.InteractionPermission,
		},
		{
			name:     "command",
			request:  `<{"id":"child-request","method":"item/commandExecution/requestApproval","params":{"threadId":"child-a","turnId":"child-turn","itemId":"child-command","startedAtMs":1700000200200,"command":"echo child","cwd":"/synthetic","availableDecisions":["accept","decline"]}}`,
			response: `>{"id":"child-request","result":{"decision":"accept"}}`,
			answer: agent.PermissionAnswer{
				Outcome:  agent.PermissionSelected,
				OptionID: "allow-once",
			},
			kind: agent.InteractionPermission,
		},
		{
			name:     "additional permissions",
			request:  `<{"id":"child-request","method":"item/permissions/requestApproval","params":{"cwd":"/synthetic","threadId":"child-a","turnId":"child-turn","itemId":"child-permissions","startedAtMs":1700000200200,"permissions":{"network":null}}}`,
			response: `>{"id":"child-request","result":{"permissions":{"network":null},"scope":"turn"}}`,
			answer:   agent.PermissionAnswer{Outcome: agent.PermissionSelected, OptionID: "allow-turn"},
			kind:     agent.InteractionPermission,
		},
		{
			name:     "file-change",
			request:  `<{"id":"child-request","method":"item/fileChange/requestApproval","params":{"threadId":"child-a","turnId":"child-turn","itemId":"child-file","startedAtMs":1700000200200}}`,
			response: `>{"id":"child-request","result":{"decision":"accept"}}`,
			answer: agent.PermissionAnswer{
				Outcome:  agent.PermissionSelected,
				OptionID: "allow-once",
			},
			kind: agent.InteractionPermission,
		},
		{
			name:     "form",
			request:  `<{"id":"child-request","method":"item/tool/requestUserInput","params":{"threadId":"child-a","turnId":"child-turn","itemId":"child-form","isBlocking":true,"questions":[{"id":"choice","header":"Choice","question":"Pick one","options":[{"label":"A","description":"Option A"}],"isOther":false,"isSecret":false}]}}`,
			response: `>{"id":"child-request","result":{"answers":{"choice":{"answers":["A"]}}}}`,
			answer: agent.FormAnswer{
				Outcome: agent.FormAccept,
				Content: map[string]any{"answers": map[string]any{
					"choice": map[string]any{"answers": []any{"A"}},
				}},
			},
			kind: agent.InteractionForm,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			frames := interactionSessionFrames(
				`<{"method":"thread/started","params":{"thread":{"id":"child-a","parentThreadId":"root-a"}}}`,
				test.request,
				test.response,
				`<{"method":"serverRequest/resolved","params":{"threadId":"child-a","requestId":"child-request"}}`,
			)
			frames = append(frames,
				`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"completed","startedAt":1700000200,"completedAt":1700000201}}}`,
			)
			frames = append(frames, codexCloseFrames(94, "root-a")...)
			session, stream := openInteractionSession(t, ctx, frames, agent.Features{})

			submitInteractionTurn(t, ctx, session)
			require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))

			child := nextEvent(t, ctx, stream)
			require.Equal(t, agent.EventSessionError, child.Type())
			assert.Equal(
				t,
				"child_thread_notification_unsupported",
				sessionErrorReason(t, child),
			)
			assert.Equal(t, "child-a", eventCodexMeta(t, child)["sourceThreadId"])

			require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
			requestEvent := nextEvent(t, ctx, stream)
			require.Equal(t, agent.EventInteractionRequested, requestEvent.Type())
			requested := payloadOf[agent.InteractionRequestedPayload](t, requestEvent)
			assert.Equal(t, test.kind, requested.Request.Kind)
			requestMeta := eventCodexMeta(t, requestEvent)
			assert.Equal(t, "child-a", requestMeta["sourceThreadId"])
			assert.Equal(t, "root-a", requestMeta["parentThreadId"])
			assert.Equal(t, "child-turn", requestMeta["turnId"])

			receipt, err := session.Answer(ctx, requested.Interaction, test.answer)
			require.NoError(t, err)
			require.NotNil(t, receipt.Acceptance.Sent)
			assert.True(t, *receipt.Acceptance.Sent)

			closed := nextInteractionClosed(t, ctx, stream)
			assert.Equal(t, requested.Interaction, closed.Interaction)
			assert.Equal(t, agent.InteractionAnswered, closed.Outcome)
			assert.Equal(t, "native_resolved", closed.Reason)
			require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))

			idle := nextStatePayload(t, ctx, stream)
			require.NotNil(t, idle.State)
			assert.Equal(t, agent.StateIdle, *idle.State)
		})
	}
}

func TestChildBlockingInteractionSurvivesRootSettlement(t *testing.T) {
	// A shared turn ID does not let a root lifecycle event settle a child handle.
	const childForm = `<{"id":"child-request","method":"item/tool/requestUserInput","params":{"threadId":"child-a","turnId":"turn-a","itemId":"child-form","isBlocking":true,"questions":[{"id":"choice","header":"Choice","question":"Pick one","options":[{"label":"A","description":"Option A"}],"isOther":false,"isSecret":false}]}}`
	tests := []struct {
		name       string
		settlement string
		stopReason string
	}{
		{
			name:       "turn completion",
			settlement: `<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"completed","startedAt":1700000200,"completedAt":1700000201}}}`,
			stopReason: "completed",
		},
		{
			name:       "thread idle",
			settlement: `<{"method":"thread/status/changed","params":{"threadId":"root-a","status":{"type":"idle"}}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			frames := interactionSessionFrames(
				`<{"method":"thread/started","params":{"thread":{"id":"child-a","parentThreadId":"root-a"}}}`,
				childForm,
				test.settlement,
				`>{"id":"child-request","result":{"answers":{"choice":{"answers":["A"]}}}}`,
				`<{"method":"serverRequest/resolved","params":{"threadId":"child-a","requestId":"child-request"}}`,
			)
			frames = append(frames, codexCloseFrames(94, "root-a")...)
			session, stream := openInteractionSession(t, ctx, frames, agent.Features{})

			submitInteractionTurn(t, ctx, session)
			require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))

			child := nextEvent(t, ctx, stream)
			require.Equal(t, agent.EventSessionError, child.Type())
			assert.Equal(
				t,
				"child_thread_notification_unsupported",
				sessionErrorReason(t, child),
			)

			require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
			requested := nextInteractionRequested(t, ctx, stream)
			assert.True(t, requested.Blocking)

			settled := nextStatePayload(t, ctx, stream)
			requiresAction := agent.StateRequiresAction
			require.NotNil(t, settled.PreviousState)
			require.NotNil(t, settled.State)
			assert.Equal(t, requiresAction, *settled.PreviousState)
			assert.Equal(t, requiresAction, *settled.State)
			assert.Equal(t, test.stopReason, settled.StopReason)

			receipt, err := session.Answer(ctx, requested.Interaction, agent.FormAnswer{
				Outcome: agent.FormAccept,
				Content: map[string]any{"answers": map[string]any{
					"choice": map[string]any{"answers": []any{"A"}},
				}},
			})
			require.NoError(t, err)
			require.NotNil(t, receipt.Acceptance.Sent)
			assert.True(t, *receipt.Acceptance.Sent)

			closed := nextInteractionClosed(t, ctx, stream)
			assert.Equal(t, requested.Interaction, closed.Interaction)
			assert.Equal(t, agent.InteractionAnswered, closed.Outcome)
			assert.Equal(t, "native_resolved", closed.Reason)
			require.Equal(t, agent.StateIdle, nextState(t, ctx, stream))
		})
	}
}

func TestNonblockingFormDoesNotRequireAction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := interactionSessionFrames(formRequest("form-1", "turn-a", false))
	frames = append(frames,
		`>{"id":"form-1","result":{"answers":{"choice":{"answers":["A"]}}}}`,
		`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"completed","startedAt":1700000200,"completedAt":1700000201}}}`,
	)
	frames = append(frames, codexCloseFrames(94, "root-a")...)
	session, stream := openInteractionSession(t, ctx, frames, agent.Features{})

	submitInteractionTurn(t, ctx, session)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	requested := nextInteractionRequested(t, ctx, stream)
	assert.False(t, requested.Blocking)
	assert.Equal(t, agent.InteractionForm, requested.Request.Kind)
	assert.Equal(t, "object", requested.Request.RequestedSchema["type"])
	assert.Contains(t, requested.Request.RequestedSchema, "x-codex-questions")
	assert.NotContains(t, requested.Request.RequestedSchema, "threadId")
	assert.NotContains(t, requested.Request.RequestedSchema, "turnId")
	assert.NotContains(t, requested.Request.RequestedSchema, "itemId")
	assert.NotContains(t, requested.Request.RequestedSchema, "isBlocking")
	assert.NotContains(t, requested.Request.RequestedSchema, "questions")
	properties, ok := requested.Request.RequestedSchema["properties"].(map[string]any)
	require.True(t, ok)
	answers, ok := properties["answers"].(map[string]any)
	require.True(t, ok)
	answerProperties, ok := answers["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, answerProperties, "choice")

	receipt, err := session.Answer(ctx, requested.Interaction, agent.FormAnswer{
		Outcome: agent.FormAccept,
		Content: map[string]any{
			"answers": map[string]any{
				"choice": map[string]any{"answers": []any{"A"}},
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.True(t, *receipt.Acceptance.Sent)

	closed := nextInteractionClosed(t, ctx, stream)
	assert.Equal(t, requested.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionAnswered, closed.Outcome)
	assert.Equal(t, "turn_completed", closed.Reason)
	require.Equal(t, agent.StateIdle, nextState(t, ctx, stream))
}

func TestFormAnswerValidationAndUnsupportedOutcomes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := interactionSessionFrames(formRequest("form-1", "turn-a", true))
	frames = append(frames,
		`>{"id":"form-1","result":{"answers":{"choice":{"answers":["A"]}}}}`,
		`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"completed","startedAt":1700000200,"completedAt":1700000201}}}`,
	)
	frames = append(frames, codexCloseFrames(94, "root-a")...)
	session, stream := openInteractionSession(t, ctx, frames, agent.Features{})

	submitInteractionTurn(t, ctx, session)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
	requested := nextInteractionRequested(t, ctx, stream)
	form := requested.Interaction

	tests := []struct {
		name   string
		answer agent.Answer
		code   agent.ErrorCode
		reason string
	}{
		{
			name: "wrong-kind",
			answer: agent.PermissionAnswer{
				Outcome:  agent.PermissionSelected,
				OptionID: "allow-once",
			},
			code: agent.ErrorInvalidArgument,
		},
		{
			name:   "missing-answers",
			answer: agent.FormAnswer{Outcome: agent.FormAccept, Content: map[string]any{}},
			code:   agent.ErrorInvalidArgument,
		},
		{
			name: "unknown-question",
			answer: agent.FormAnswer{
				Outcome: agent.FormAccept,
				Content: map[string]any{"answers": map[string]any{
					"other": map[string]any{"answers": []any{"A"}},
				}},
			},
			code: agent.ErrorInvalidArgument,
		},
		{
			name: "invalid-answer-shape",
			answer: agent.FormAnswer{
				Outcome: agent.FormAccept,
				Content: map[string]any{"answers": map[string]any{
					"choice": map[string]any{"answers": "A"},
				}},
			},
			code: agent.ErrorInvalidArgument,
		},
		{
			name:   "decline",
			answer: agent.FormAnswer{Outcome: agent.FormDecline},
			code:   agent.ErrorUnsupported,
			reason: agentcodex.ErrorReasonFormOutcomeUnsupported,
		},
		{
			name:   "cancel",
			answer: agent.FormAnswer{Outcome: agent.FormCancel},
			code:   agent.ErrorUnsupported,
			reason: agentcodex.ErrorReasonFormOutcomeUnsupported,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := session.Answer(ctx, form, test.answer)
			require.Error(t, err)
			assert.Equal(t, test.code, agent.CodeOf(err))
			if test.reason != "" {
				var classified *agent.Error
				require.ErrorAs(t, err, &classified)
				assert.Equal(t, test.reason, classified.Reason)
			}
		})
	}

	receipt, err := session.Answer(ctx, form, agent.FormAnswer{
		Outcome: agent.FormAccept,
		Content: map[string]any{"answers": map[string]any{
			"choice": map[string]any{"answers": []any{"A"}},
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, form, receipt.Interaction)

	closed := nextInteractionClosed(t, ctx, stream)
	assert.Equal(t, form, closed.Interaction)
	assert.Equal(t, agent.InteractionAnswered, closed.Outcome)
	assert.Equal(t, "turn_completed", closed.Reason)
	require.Equal(t, agent.StateIdle, nextState(t, ctx, stream))
}

func TestInteractionClosesFromNativeLifecycle(t *testing.T) {
	const answer = `>{"id":"approval-1","result":{"decision":"accept"}}`
	tests := []struct {
		name        string
		after       []string
		closeFirst  bool
		answered    bool
		wantOutcome agent.InteractionOutcome
		wantReason  string
		wantState   agent.SessionState
		hasState    bool
		report      string
	}{
		{
			name: "native_resolved",
			after: []string{
				answer,
				`<{"method":"serverRequest/resolved","params":{"threadId":"root-a","requestId":"approval-1"}}`,
			},
			answered:    true,
			wantOutcome: agent.InteractionAnswered,
			wantReason:  "native_resolved",
			wantState:   agent.StateRunning,
			hasState:    true,
			report:      "passed",
		},
		{
			name: "turn_completed_pending",
			after: []string{
				`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"completed","startedAt":1700000200,"completedAt":1700000201}}}`,
			},
			wantOutcome: agent.InteractionExpired,
			wantReason:  "turn_completed",
			wantState:   agent.StateIdle,
			hasState:    true,
			report:      "failed",
		},
		{
			name: "turn_completed_answered",
			after: []string{
				answer,
				`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"completed","startedAt":1700000200,"completedAt":1700000201}}}`,
			},
			answered:    true,
			wantOutcome: agent.InteractionAnswered,
			wantReason:  "turn_completed",
			wantState:   agent.StateIdle,
			hasState:    true,
			report:      "passed",
		},
		{
			name: "thread_idle_pending",
			after: []string{
				`<{"method":"thread/status/changed","params":{"threadId":"root-a","status":{"type":"idle"}}}`,
			},
			wantOutcome: agent.InteractionExpired,
			wantReason:  "thread_idle",
			wantState:   agent.StateIdle,
			hasState:    true,
			report:      "failed",
		},
		{
			name: "thread_idle_answered",
			after: []string{
				answer,
				`<{"method":"thread/status/changed","params":{"threadId":"root-a","status":{"type":"idle"}}}`,
			},
			answered:    true,
			wantOutcome: agent.InteractionAnswered,
			wantReason:  "thread_idle",
			wantState:   agent.StateIdle,
			hasState:    true,
			report:      "passed",
		},
		{
			name:        "session_close_pending",
			closeFirst:  true,
			wantOutcome: agent.InteractionCancelled,
			wantReason:  agent.ErrorReasonSessionClosed,
			report:      "failed",
		},
		{
			name:        "session_close_answered",
			after:       []string{answer},
			closeFirst:  true,
			answered:    true,
			wantOutcome: agent.InteractionAnswered,
			wantReason:  agent.ErrorReasonSessionClosed,
			report:      "passed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			frames := interactionSessionFrames(commandApprovalRequest("approval-1"))
			frames = append(frames, test.after...)
			frames = append(frames, codexCloseFrames(94, "root-a")...)
			session, stream := openInteractionSessionWithStatus(
				t,
				ctx,
				frames,
				agent.Features{},
				test.report,
			)

			submitInteractionTurn(t, ctx, session)
			require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
			require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
			requested := nextInteractionRequested(t, ctx, stream)

			if test.answered {
				receipt, err := session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
					Outcome:  agent.PermissionSelected,
					OptionID: "allow-once",
				})
				require.NoError(t, err)
				require.NotNil(t, receipt.Acceptance.Sent)
				assert.True(t, *receipt.Acceptance.Sent)
			}
			if test.closeFirst {
				require.NoError(t, session.Close(ctx))
				_, err := session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
					Outcome:  agent.PermissionSelected,
					OptionID: "allow-once",
				})
				require.Error(t, err)
				assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
			} else {
				t.Cleanup(func() { assert.NoError(t, session.Close(context.Background())) })
			}

			closed := nextInteractionClosed(t, ctx, stream)
			assert.Equal(t, requested.Interaction, closed.Interaction)
			assert.Equal(t, test.wantOutcome, closed.Outcome)
			assert.Equal(t, test.wantReason, closed.Reason)
			if test.hasState {
				payload := nextStatePayload(t, ctx, stream)
				require.NotNil(t, payload.State)
				assert.Equal(t, test.wantState, *payload.State)
				require.NotNil(t, payload.PreviousState)
				assert.Equal(t, agent.StateRequiresAction, *payload.PreviousState)
			}
		})
	}
}

func TestNativeResolutionCancelsPendingInteraction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := interactionSessionFrames(
		commandApprovalRequest("approval-1"),
		`>{"id":94,"method":"thread/settings/update","params":{"threadId":"root-a","model":"synthetic-model-2"}}`,
		`<{"id":94,"result":{}}`,
		`<{"method":"serverRequest/resolved","params":{"threadId":"root-a","requestId":"approval-1"}}`,
	)
	frames = append(frames, codexCloseFrames(95, "root-a")...)
	session, stream := openInteractionSession(t, ctx, frames, agent.Features{})

	submitInteractionTurn(t, ctx, session)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
	requested := nextInteractionRequested(t, ctx, stream)

	_, err := session.SetConfig(ctx, agent.ConfigChange{
		ID:    agent.ConfigModel,
		Value: "synthetic-model-2",
	})
	require.NoError(t, err)
	closed := nextInteractionClosed(t, ctx, stream)
	assert.Equal(t, requested.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionCancelled, closed.Outcome)
	assert.Equal(t, "native_resolved", closed.Reason)
	state := nextStatePayload(t, ctx, stream)
	require.NotNil(t, state.PreviousState)
	require.NotNil(t, state.State)
	assert.Equal(t, agent.StateRequiresAction, *state.PreviousState)
	assert.Equal(t, agent.StateRunning, *state.State)
}

func TestApprovalBeforeStartResponseClaimsTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	// The peer withholds the start response until the approval is answered and
	// an interrupt targets the turn. No turn/started notification is sent.
	frames := interactionThreadFrames(
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"run","type":"text"}],"threadId":"root-a"}}`,
		commandApprovalRequest("approval-1"),
		`>{"id":"approval-1","result":{"decision":"accept"}}`,
		`>{"id":94,"method":"turn/interrupt","params":{"threadId":"root-a","turnId":"turn-a"}}`,
		`<{"id":94,"result":{}}`,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		`<{"method":"serverRequest/resolved","params":{"threadId":"root-a","requestId":"approval-1"}}`,
	)
	frames = append(frames, codexCloseFrames(95, "root-a")...)
	session, stream := openInteractionSession(t, ctx, frames, agent.Features{})

	type submission struct {
		receipt agent.InputReceipt
		err     error
	}
	done := make(chan submission, 1)
	go func() {
		receipt, err := session.StartOrSteer(ctx, textInput("run"))
		done <- submission{receipt, err}
	}()

	require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
	request := nextInteractionRequested(t, ctx, stream)
	answer := agent.PermissionAnswer{Outcome: agent.PermissionSelected, OptionID: "allow-once"}
	answered, err := session.Answer(ctx, request.Interaction, answer)
	require.NoError(t, err)
	assert.Equal(t, request.Interaction, answered.Interaction)
	assert.Equal(t, agent.Acceptance{Sent: new(true)}, answered.Acceptance)
	interrupted, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	assert.Equal(t, agent.InterruptAcknowledged, interrupted.Result)
	assert.Equal(t, "turn-a", interrupted.Target)

	started := <-done
	require.NoError(t, started.err)
	assert.Equal(t, agent.InputAppliedNewWork, started.receipt.Result)
	assert.Equal(t, "turn-a", started.receipt.Submission)
	assertAccepted(t, started.receipt.Acceptance)
	closed := nextInteractionClosed(t, ctx, stream)
	assert.Equal(t, request.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionAnswered, closed.Outcome)
	assert.Equal(t, "native_resolved", closed.Reason)
	state := nextStatePayload(t, ctx, stream)
	assert.Equal(t, new(agent.StateRequiresAction), state.PreviousState)
	assert.Equal(t, new(agent.StateRunning), state.State)

	_, err = session.Answer(ctx, request.Interaction, answer)
	require.Equal(t, agent.ErrorReasonInvalidInteractionRef, errorReason(t, err))
	require.NoError(t, session.Close(ctx))
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestApprovalClaimsUnknownSubmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	frames := interactionThreadFrames(
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"run","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{"turn":{"id":"","items":[],"status":"inProgress"}}}`,
		commandApprovalRequest("approval-1"),
		`>{"id":"approval-1","result":{"decision":"accept"}}`,
		`>{"id":94,"method":"turn/start","params":{"input":[{"text":"adjust","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":94,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
	)
	frames = append(frames, codexCloseFrames(95, "root-a")...)
	session, stream, release := openSessionBeforeApproval(t, ctx, frames)

	_, err := session.StartOrSteer(ctx, textInput("run"))
	require.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(err))
	blocked, err := session.StartOrSteer(ctx, textInput("before approval"))
	require.NoError(t, err)
	require.Equal(t, agent.InputNotApplied, blocked.Result)
	assert.Equal(t, agent.Acceptance{Sent: new(false)}, blocked.Acceptance)

	release()
	require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
	request := nextInteractionRequested(t, ctx, stream)
	_, err = session.Answer(ctx, request.Interaction, agent.PermissionAnswer{
		Outcome: agent.PermissionSelected, OptionID: "allow-once",
	})
	require.NoError(t, err)
	adjusted, err := session.StartOrSteer(ctx, textInput("adjust"))
	require.NoError(t, err)
	assert.Equal(t, agent.InputAppliedCurrentWork, adjusted.Result)
	assert.Equal(t, "turn-a", adjusted.Submission)
	assertAccepted(t, adjusted.Acceptance)
}

func TestApprovalWithoutSubmissionIsRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	frames := interactionThreadFrames(
		commandApprovalRequest("approval-1"),
		`>{"id":"approval-1","error":{"code":-32602,"message":"Unsupported by the unified agent adapter"}}`,
	)
	frames = append(frames, codexCloseFrames(93, "root-a")...)
	session, stream, release := openSessionBeforeApproval(t, ctx, frames)
	release()
	gap := nextEvent(t, ctx, stream)
	require.Equal(t, agent.EventSessionError, gap.Type())
	assert.Equal(t, "stale_native_request", sessionErrorReason(t, gap))
	assert.Equal(t, true, eventCodexMeta(t, gap)["requestRejected"])
	interrupted, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	assert.Equal(t, agent.InterruptReceipt{Result: agent.InterruptNoActiveWork}, interrupted)
	require.NoError(t, session.Close(ctx))
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func openSessionBeforeApproval(
	t *testing.T,
	ctx context.Context,
	frames []string,
) (testSession, agent.EventStream, func()) {
	t.Helper()
	var gate *gatedFrameReader
	session, stream := openControlledInteractionSession(t, ctx, frames, testStreamWrappers{
		reader: func(reader io.ReadCloser) io.ReadCloser {
			gate = newGatedFrameReader(reader, []byte("item/commandExecution/requestApproval"))
			return gate
		},
	})
	t.Cleanup(gate.releaseOnce)
	return session, stream, gate.releaseOnce
}

func TestStaleInteractionRequestIsRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := interactionSessionFrames(
		`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"completed","startedAt":1700000200,"completedAt":1700000201}}}`,
		commandApprovalRequest("approval-1"),
		`>{"id":"approval-1","error":{"code":-32602,"message":"Unsupported by the unified agent adapter"}}`,
	)
	frames = append(frames, codexCloseFrames(94, "root-a")...)
	session, stream := openInteractionSession(t, ctx, frames, agent.Features{})

	submitInteractionTurn(t, ctx, session)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	require.Equal(t, agent.StateIdle, nextState(t, ctx, stream))
	gap := nextEvent(t, ctx, stream)
	require.Equal(t, agent.EventSessionError, gap.Type())
	assert.Equal(t, "stale_native_request", sessionErrorReason(t, gap))
	assert.Equal(t, true, eventCodexMeta(t, gap)["requestRejected"])
}

func TestConcurrentAnswersAdmitOneReceiver(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := interactionSessionFrames(commandApprovalRequest("approval-1"))
	frames = append(frames,
		`>{"id":"approval-1","result":{"decision":"accept"}}`,
	)
	frames = append(frames, codexCloseFrames(94, "root-a")...)
	session, stream := openInteractionSession(t, ctx, frames, agent.Features{})

	submitInteractionTurn(t, ctx, session)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
	requested := nextInteractionRequested(t, ctx, stream)

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			<-start
			_, err := session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
				Outcome:  agent.PermissionSelected,
				OptionID: "allow-once",
			})
			results <- err
		})
	}
	close(start)
	workers.Wait()
	close(results)
	var succeeded, rejected int
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case agent.CodeOf(err) == agent.ErrorFailedPrecondition:
			rejected++
		default:
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 1, succeeded)
	assert.Equal(t, 1, rejected)
}

func TestInteractionRefsAreSessionScopedAndOpaque(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	framesA := interactionSessionFrames(commandApprovalRequest("approval-1"))
	framesA = append(framesA, codexCloseFrames(94, "root-a")...)
	sessionA, streamA := openInteractionSessionWithStatus(
		t,
		ctx,
		framesA,
		agent.Features{},
		"failed",
	)

	submitInteractionTurn(t, ctx, sessionA)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, streamA))
	require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, streamA))
	requested := nextInteractionRequested(t, ctx, streamA)

	framesB := append([]string(nil), mockCodexInit...)
	framesB = append(framesB,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-b")+`}`,
	)
	framesB = append(framesB, codexCloseFrames(93, "root-b")...)
	sessionB, _ := openInteractionSession(t, ctx, framesB, agent.Features{})

	_, err := sessionB.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
		Outcome:  agent.PermissionSelected,
		OptionID: "allow-once",
	})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))

	_, err = sessionA.Answer(ctx, agent.InteractionRef("forged"), agent.PermissionAnswer{
		Outcome:  agent.PermissionSelected,
		OptionID: "allow-once",
	})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
}

func TestControlOperationsReturnNativeAcceptance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"run","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		`>{"id":94,"method":"turn/interrupt","params":{"threadId":"root-a","turnId":"turn-a"}}`,
		`<{"id":94,"result":{}}`,
		`>{"id":95,"method":"thread/archive","params":{"threadId":"root-a"}}`,
		`<{"id":95,"result":{}}`,
		`>{"id":96,"method":"thread/unarchive","params":{"threadId":"root-a"}}`,
		`<{"id":96,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":97,"method":"thread/settings/update","params":{"threadId":"root-a","model":"synthetic-model-2"}}`,
		`<{"id":97,"result":{}}`,
	)
	frames = append(frames, codexCloseFrames(98, "root-a")...)
	session, stream := openInteractionSession(t, ctx, frames, agent.Features{})

	noWork, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	assert.Equal(t, agent.InterruptReceipt{Result: agent.InterruptNoActiveWork}, noWork)

	submitInteractionTurn(t, ctx, session)
	interrupted, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	assert.Equal(t, "turn-a", interrupted.Target)
	assert.Equal(t, agent.InterruptAcknowledged, interrupted.Result)

	archived, err := session.Archive(ctx)
	require.NoError(t, err)
	assertAccepted(t, archived.Acceptance)

	unarchived, err := session.Unarchive(ctx)
	require.NoError(t, err)
	assertAccepted(t, unarchived.Acceptance)

	modelChange := agent.ConfigChange{
		ID:    agent.ConfigModel,
		Value: "synthetic-model-2",
	}
	model, err := session.SetConfig(ctx, modelChange)
	require.NoError(t, err)
	assert.Equal(t, modelChange, model.Change)
	assertAccepted(t, model.Acceptance)

	_, err = session.SetConfig(ctx, agent.ConfigChange{ID: agent.ConfigModel})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))

	require.NoError(t, session.Close(ctx))
	_, err = session.Interrupt(ctx, agent.InterruptOptions{})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
	_, err = session.Archive(ctx)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
	_, err = session.Unarchive(ctx)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
	_, err = session.SetConfig(ctx, agent.ConfigChange{
		ID:    agent.ConfigModel,
		Value: "synthetic-model-3",
	})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

const codexTurnStartedFrame = `<{"method":"turn/started","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"inProgress","startedAt":1700000200}}}`

func interactionThreadFrames(after ...string) []string {
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
	)
	return append(frames, after...)
}

func interactionSessionFrames(after ...string) []string {
	frames := interactionThreadFrames(
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"run","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		codexTurnStartedFrame,
	)
	return append(frames, after...)
}

func codexCloseFrames(id int, threadID string) []string {
	return []string{
		fmt.Sprintf(`>{"id":%d,"method":"thread/unsubscribe","params":{"threadId":%q}}`, id, threadID),
		fmt.Sprintf(`<{"id":%d,"result":{"status":"unsubscribed"}}`, id),
	}
}

func commandApprovalRequest(id string) string {
	return `<{"id":"` + id + `","method":"item/commandExecution/requestApproval","params":{"threadId":"root-a","turnId":"turn-a","itemId":"cmd-1","startedAtMs":1700000200000,"command":"echo hello","cwd":"/synthetic","availableDecisions":["accept","decline"]}}`
}

func formRequest(id, turnID string, blocking bool) string {
	return fmt.Sprintf(
		`<{"id":%q,"method":"item/tool/requestUserInput","params":{"threadId":"root-a","turnId":%q,"itemId":"form-item","isBlocking":%t,"questions":[{"id":"choice","header":"Choice","question":"Pick one","options":[{"label":"A","description":"Option A"}],"isOther":false,"isSecret":false}]}}`,
		id,
		turnID,
		blocking,
	)
}

func openInteractionSession(
	t *testing.T,
	ctx context.Context,
	frames []string,
	features agent.Features,
) (testSession, agent.EventStream) {
	t.Helper()
	return openInteractionSessionWithStatus(t, ctx, frames, features, "passed")
}

func openInteractionSessionWithStatus(
	t *testing.T,
	ctx context.Context,
	frames []string,
	features agent.Features,
	wantReportStatus string,
) (testSession, agent.EventStream) {
	t.Helper()
	peerSession := prepareCodexPeerWithStatus(t, wantReportStatus, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() {
		err := provider.Stop(context.Background())
		if wantReportStatus == "passed" {
			assert.NoError(t, err)
		} else {
			assert.Error(t, err, "a failed replay must remain visible through the provider")
		}
	})
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd:      "/synthetic",
		Features: features,
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)
	concrete, ok := session.(testSession)
	require.True(t, ok)
	return concrete, stream
}

func submitInteractionTurn(t *testing.T, ctx context.Context, session agent.Session) {
	t.Helper()
	_, err := session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "run"}}},
	})
	require.NoError(t, err)
}

func nextState(t *testing.T, ctx context.Context, stream agent.EventStream) agent.SessionState {
	t.Helper()
	payload := nextStatePayload(t, ctx, stream)
	require.NotNil(t, payload.State)
	return *payload.State
}

func nextStatePayload(
	t *testing.T,
	ctx context.Context,
	stream agent.EventStream,
) agent.StateUpdatePayload {
	t.Helper()
	event := nextEvent(t, ctx, stream)
	require.Equalf(
		t,
		agent.EventSessionStateUpdate,
		event.Type(),
		"event payload: %#v, meta: %#v",
		event.Payload,
		event.Meta,
	)
	payload, ok := event.Payload.(agent.StateUpdatePayload)
	require.True(t, ok)
	return payload
}

func nextInteractionRequested(
	t *testing.T,
	ctx context.Context,
	stream agent.EventStream,
) agent.InteractionRequestedPayload {
	t.Helper()
	event := nextEvent(t, ctx, stream)
	require.Equal(t, agent.EventInteractionRequested, event.Type())
	payload, ok := event.Payload.(agent.InteractionRequestedPayload)
	require.True(t, ok)
	return payload
}

func nextInteractionClosed(
	t *testing.T,
	ctx context.Context,
	stream agent.EventStream,
) agent.InteractionClosedPayload {
	t.Helper()
	event := nextEvent(t, ctx, stream)
	require.Equal(t, agent.EventInteractionClosed, event.Type())
	payload, ok := event.Payload.(agent.InteractionClosedPayload)
	require.True(t, ok)
	return payload
}

func assertAccepted(t *testing.T, acceptance agent.Acceptance) {
	t.Helper()
	require.NotNil(t, acceptance.Sent)
	require.NotNil(t, acceptance.NativeAccepted)
	assert.True(t, *acceptance.Sent)
	assert.True(t, *acceptance.NativeAccepted)
	assert.Nil(t, acceptance.NativeRecorded)
}

func TestOfferedApprovalOptionsRoundTripNativeDecision(t *testing.T) {
	const (
		command    = "item/commandExecution/requestApproval"
		file       = "item/fileChange/requestApproval"
		execPolicy = `{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["go","test"],"extension":"keep"}}`
		allowHost  = `{"applyNetworkPolicyAmendment":{"network_policy_amendment":{"action":"allow","host":"a.example","extension":"keep"}}}`
		denyHost   = `{"applyNetworkPolicyAmendment":{"network_policy_amendment":{"action":"deny","host":"b.example"}}}`
	)
	type choice struct {
		id       string
		kind     agent.PermissionOptionKind
		scope    agent.PermissionScope
		decision string
	}

	once := choice{"allow-once", agent.PermissionAllow, agent.PermissionScopeOnce, `"accept"`}
	session := choice{"allow-session", agent.PermissionAllow, agent.PermissionScopeSession, `"acceptForSession"`}
	reject := choice{"reject-once", agent.PermissionReject, agent.PermissionScopeOnce, `"decline"`}
	cancel := choice{"cancel", agent.PermissionCancel, "", `"cancel"`}
	for _, test := range []struct {
		name        string
		method      string
		extraParams string
		choices     []choice
	}{
		{"command defaults", command, "", []choice{once, reject}},
		{
			"offered command decisions", command, `,"availableDecisions":[` + denyHost + `,"acceptForSession",` + execPolicy + `,"cancel",` + allowHost + `]`,
			[]choice{
				{"native-decision-0", agent.PermissionReject, agent.PermissionScopeAlways, denyHost},
				session,
				{"native-decision-2", agent.PermissionAllow, agent.PermissionScopeAlways, execPolicy},
				cancel,
				{"native-decision-4", agent.PermissionAllow, agent.PermissionScopeAlways, allowHost},
			},
		},
		{"file", file, "", []choice{once, session, reject, cancel}},
	} {
		for index, selected := range test.choices {
			outcomes := []agent.PermissionOutcome{agent.PermissionSelected}
			if selected.kind == agent.PermissionCancel {
				outcomes = append(outcomes, agent.PermissionCancelled)
			}
			for _, outcome := range outcomes {
				t.Run(test.name+"/"+selected.id+"/"+string(outcome), func(t *testing.T) {
					ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
					defer stop()
					frames := interactionSessionFrames(
						`<{"id":"approval","method":"`+test.method+`","params":{"threadId":"root-a","turnId":"turn-a","itemId":"item","startedAtMs":1700000200000`+test.extraParams+`}}`,
						`>{"id":"approval","result":{"decision":`+selected.decision+`}}`,
						`<{"method":"serverRequest/resolved","params":{"threadId":"root-a","requestId":"approval"}}`,
					)
					frames = append(frames, codexCloseFrames(94, "root-a")...)
					live, stream := openInteractionSession(t, ctx, frames, agent.Features{})
					submitInteractionTurn(t, ctx, live)
					require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
					require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
					requested := nextInteractionRequested(t, ctx, stream)
					require.Len(t, requested.Request.Options, len(test.choices))
					for i, expected := range test.choices {
						option := requested.Request.Options[i]
						assert.Equal(t, expected.id, option.OptionID)
						assert.Equal(t, expected.kind, option.Kind)
						assert.Equal(t, expected.scope, option.Scope)
					}

					answer := agent.PermissionAnswer{Outcome: outcome}
					if outcome == agent.PermissionSelected {
						answer.OptionID = requested.Request.Options[index].OptionID
					}
					receipt, err := live.Answer(ctx, requested.Interaction, answer)
					require.NoError(t, err)
					require.NotNil(t, receipt.Acceptance.Sent)
					assert.True(t, *receipt.Acceptance.Sent)
					closed := nextInteractionClosed(t, ctx, stream)
					assert.Equal(t, requested.Interaction, closed.Interaction)
					assert.Equal(t, "native_resolved", closed.Reason)
					want := agent.InteractionAnswered
					if selected.kind == agent.PermissionCancel {
						want = agent.InteractionCancelled
					}
					assert.Equal(t, want, closed.Outcome)
				})
			}
		}
	}
}
