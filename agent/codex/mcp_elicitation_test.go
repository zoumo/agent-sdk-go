package agentcodex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

// Synthetic peer input based on the field/key projection from Codex CLI 0.166.0:
// superagent-mvp-20261117/evidence/16/codex-public-approval-wire/native-approval-wire.jsonl,
// native-to-sdk request 0. The projection omits values. Representative values
// and expected reply fields follow rust-v0.166.0, core/src/mcp_tool_call.rs
// and tui/src/bottom_pane/mcp_server_elicitation.rs. This is not a recording.
func mcpToolRequest(id, thread, turn, persist string) string {
	return fmt.Sprintf(`<{"id":%s,"method":"mcpServer/elicitation/request","params":{
		"threadId":%q,"turnId":%q,"serverName":"superagent","mode":"form",
		"message":"Allow superagent to read issue 17?","requestedSchema":{"type":"object","properties":{}},
		"_meta":{"codex_approval_kind":"mcp_tool_call","tool_description":"Read an issue",
		         "tool_params":{"issue":17},"tool_params_display":[{"name":"issue","value":"17"}]%s}}}`, id, thread, turn, persist)
}

func TestMCPToolApprovalRoundTrip(t *testing.T) {
	tests := []struct {
		name, persist   string
		session, always bool
		option          string // Empty selects direct cancellation.
		response        string
		closed          agent.InteractionOutcome
	}{
		{"once without persist", "", false, false, "allow-once", `{"action":"accept","content":null,"_meta":null}`, agent.InteractionAnswered},
		{"reject", "", false, false, "reject-once", `{"action":"decline","content":null,"_meta":null}`, agent.InteractionAnswered},
		{"select cancel", "", false, false, "cancel", `{"action":"cancel","content":null,"_meta":{"codex_approval_cancellation_reason":"user"}}`, agent.InteractionCancelled},
		{"direct cancel", "", false, false, "", `{"action":"cancel","content":null,"_meta":{"codex_approval_cancellation_reason":"user"}}`, agent.InteractionCancelled},
		{"session only", `,"persist":"session"`, true, false, "allow-session", `{"action":"accept","content":null,"_meta":{"persist":"session"}}`, agent.InteractionAnswered},
		{"always only", `,"persist":"always"`, false, true, "allow-always", `{"action":"accept","content":null,"_meta":{"persist":"always"}}`, agent.InteractionAnswered},
		{"both choose once", `,"persist":["session","always"]`, true, true, "allow-once", `{"action":"accept","content":null,"_meta":null}`, agent.InteractionAnswered},
		{"future scope", `,"persist":["future","session","session"]`, true, false, "allow-session", `{"action":"accept","content":null,"_meta":{"persist":"session"}}`, agent.InteractionAnswered},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			frames := interactionSessionFrames(
				mcpToolRequest("0", "root-a", "turn-a", tt.persist),
				`>{"id":0,"result":`+tt.response+`}`,
				`<{"method":"serverRequest/resolved","params":{"threadId":"root-a","requestId":0}}`,
			)
			frames = append(frames, codexCloseFrames(94, "root-a")...)
			session, stream := openInteractionSession(t, ctx, frames, agent.Features{})
			submitInteractionTurn(t, ctx, session)
			require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
			require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
			requested := nextInteractionRequested(t, ctx, stream)
			req := requested.Request
			require.Equal(t, agent.InteractionPermission, req.Kind)
			raw, err := json.Marshal(req)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), "toolCallId")
			assert.Equal(t, "Allow superagent to read issue 17?", req.Title)
			require.Len(t, req.Content, 3)
			assert.Equal(t, "MCP server: superagent", req.Content[0].Text.Text)
			assert.Equal(t, "Read an issue", req.Content[1].Text.Text)
			assert.JSONEq(t, `{"issue":17}`, req.Content[2].Text.Text)
			want := map[string]agent.PermissionOptionKind{"allow-once": agent.PermissionAllow, "reject-once": agent.PermissionReject, "cancel": agent.PermissionCancel}
			scopes := map[string]agent.PermissionScope{"allow-once": agent.PermissionScopeOnce, "reject-once": agent.PermissionScopeOnce}
			if tt.session {
				want["allow-session"], scopes["allow-session"] = agent.PermissionAllow, agent.PermissionScopeSession
			}
			if tt.always {
				want["allow-always"], scopes["allow-always"] = agent.PermissionAllow, agent.PermissionScopeAlways
			}
			require.Len(t, req.Options, len(want))
			for _, option := range req.Options {
				assert.Equal(t, want[option.OptionID], option.Kind)
				assert.Equal(t, scopes[option.OptionID], option.Scope)
			}
			_, err = session.Answer(ctx, requested.Interaction, agent.FormAnswer{Outcome: agent.FormAccept, Content: map[string]any{}})
			require.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
			_, err = session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{Outcome: agent.PermissionSelected, OptionID: "allow-unoffered"})
			require.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
			answer := agent.PermissionAnswer{Outcome: agent.PermissionSelected, OptionID: tt.option}
			if tt.option == "" {
				answer.Outcome = agent.PermissionCancelled
			}
			receipt, err := session.Answer(ctx, requested.Interaction, answer)
			require.NoError(t, err)
			assert.Equal(t, new(true), receipt.Acceptance.Sent)
			closed := nextInteractionClosed(t, ctx, stream)
			assert.Equal(t, tt.closed, closed.Outcome)
			assert.Equal(t, "native_resolved", closed.Reason)
			assert.Equal(t, requested.Interaction, closed.Interaction)
			_, err = session.Answer(ctx, requested.Interaction, answer)
			require.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
		})
	}
}

func TestOrdinaryMCPElicitationRoundTrip(t *testing.T) {
	const form = `"mode":"form","requestedSchema":{"type":"object","properties":{"n":{"type":"integer","maximum":9007199254740993}},"required":["n"]}`
	const url = `"mode":"url","url":"https://example.test/link","elicitationId":"link-id"`
	for _, tt := range []struct {
		name, fields string
		kind         agent.InteractionKind
		answer       agent.Answer
		response     string
		outcome      agent.InteractionOutcome
	}{
		{"form accept", form, agent.InteractionForm, agent.FormAnswer{Outcome: agent.FormAccept, Content: map[string]any{"n": 3}}, `{"action":"accept","content":{"n":3},"_meta":null}`, agent.InteractionAnswered},
		{"empty form accept", `"mode":"form","requestedSchema":{"type":"object","properties":{}}`, agent.InteractionForm, agent.FormAnswer{Outcome: agent.FormAccept, Content: map[string]any{}}, `{"action":"accept","content":{},"_meta":null}`, agent.InteractionAnswered},
		{"form decline", form, agent.InteractionForm, agent.FormAnswer{Outcome: agent.FormDecline}, `{"action":"decline","content":null,"_meta":null}`, agent.InteractionAnswered},
		{"form cancel", form, agent.InteractionForm, agent.FormAnswer{Outcome: agent.FormCancel}, `{"action":"cancel","content":null,"_meta":null}`, agent.InteractionCancelled},
		{"url accept", url, agent.InteractionURL, agent.URLAnswer{Outcome: agent.URLAccept}, `{"action":"accept","content":null,"_meta":null}`, agent.InteractionAnswered},
		{"url decline", url, agent.InteractionURL, agent.URLAnswer{Outcome: agent.URLDecline}, `{"action":"decline","content":null,"_meta":null}`, agent.InteractionAnswered},
		{"url cancel", url, agent.InteractionURL, agent.URLAnswer{Outcome: agent.URLCancel}, `{"action":"cancel","content":null,"_meta":null}`, agent.InteractionCancelled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			// A standalone MCP request is independent of AgentQuestions and
			// of whether app-server observed a current turn.
			frames := append([]string(nil), mockCodexInit...)
			frames = append(frames,
				`>{"id":92,"method":"thread/start","params":{"config":{"tools.experimental_request_user_input.enabled":false},"cwd":"/synthetic"}}`,
				`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
				`<{"id":"ordinary","method":"mcpServer/elicitation/request","params":{"threadId":"root-a","turnId":null,"serverName":"sample","message":"Provide details","_meta":{"persist":"always"},`+tt.fields+`}}`,
				`<{"method":"thread/status/changed","params":{"threadId":"root-a","status":{"type":"idle"}}}`,
				`>{"id":"ordinary","result":`+tt.response+`}`,
				`<{"method":"serverRequest/resolved","params":{"threadId":"root-a","requestId":"ordinary"}}`,
			)
			frames = append(frames, codexCloseFrames(93, "root-a")...)
			session, stream := openInteractionSession(t, ctx, frames, agent.Features{AgentQuestions: new(false)})
			assert.True(t, session.Capabilities().Supports(agent.CapabilityForm))
			assert.True(t, session.Capabilities().Supports(agent.CapabilityURL))
			require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
			req := nextInteractionRequested(t, ctx, stream)
			require.Equal(t, tt.kind, req.Request.Kind)
			assert.Equal(t, "Provide details", req.Request.Message)
			if tt.kind == agent.InteractionURL {
				assert.Equal(t, "https://example.test/link", req.Request.URL)
			} else if tt.fields == form {
				schema, err := json.Marshal(req.Request.RequestedSchema)
				require.NoError(t, err)
				assert.JSONEq(t, `{"type":"object","properties":{"n":{"type":"integer","maximum":9007199254740993}},"required":["n"]}`, string(schema))
				assert.Contains(t, string(schema), "9007199254740993")
			}
			require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
			_, err := session.Answer(ctx, req.Interaction, agent.PermissionAnswer{Outcome: agent.PermissionCancelled})
			require.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
			_, err = session.Answer(ctx, req.Interaction, tt.answer)
			require.NoError(t, err)
			closed := nextInteractionClosed(t, ctx, stream)
			assert.Equal(t, req.Interaction, closed.Interaction)
			assert.Equal(t, tt.outcome, closed.Outcome)
			assert.Equal(t, "native_resolved", closed.Reason)
		})
	}
}

func TestMCPElicitationRejectsMalformedRequests(t *testing.T) {
	for _, tt := range []struct {
		name, fields, reason string
	}{
		{"missing schema", `"mode":"form"`, "invalid_native_request"},
		{"invalid properties", `"mode":"form","requestedSchema":{"type":"object","properties":[]}`, "invalid_native_request"},
		{"tool approval requiring input", `"mode":"form","requestedSchema":{"type":"object","properties":{},"required":["missing"]},"_meta":{"codex_approval_kind":"mcp_tool_call"}`, "invalid_native_request"},
		{"invalid persistence", `"mode":"form","requestedSchema":{"type":"object","properties":{}},"_meta":{"codex_approval_kind":"mcp_tool_call","persist":["session",42]}`, "invalid_native_request"},
		{"missing URL", `"mode":"url","elicitationId":"link-id"`, "invalid_native_request"},
		{"missing elicitation ID", `"mode":"url","url":"https://example.test"`, "invalid_native_request"},
		{"unknown mode", `"mode":"future"`, "native_request_unsupported"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			frames := interactionSessionFrames(
				`<{"id":"bad","method":"mcpServer/elicitation/request","params":{"threadId":"root-a","serverName":"sample","message":"Provide details",`+tt.fields+`}}`,
				`>{"id":"bad","error":{"code":-32602,"message":"Unsupported by the unified agent adapter"}}`,
			)
			frames = append(frames, codexCloseFrames(94, "root-a")...)
			session, stream := openInteractionSession(t, ctx, frames, agent.Features{})
			submitInteractionTurn(t, ctx, session)
			require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
			event := nextEvent(t, ctx, stream)
			require.Equal(t, agent.EventSessionError, event.Type())
			assert.Equal(t, tt.reason, sessionErrorReason(t, event))
			assert.Equal(t, true, eventCodexMeta(t, event)["requestRejected"])
		})
	}
}

func TestChildMCPApprovalRequiresOwningSessionAndLiveHandle(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	for i, id := range []string{"root-a", "other-root"} {
		frames = append(frames,
			fmt.Sprintf(`>{"id":%d,"method":"thread/start","params":{"cwd":"/synthetic"}}`, 92+i),
			fmt.Sprintf(`<{"id":%d,"result":%s}`, 92+i, codexThreadStartResult(id)),
		)
	}
	frames = append(frames,
		`>{"id":94,"method":"turn/start","params":{"input":[{"text":"run","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":94,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		codexTurnStartedFrame,
		`<{"method":"thread/started","params":{"thread":{"id":"child-a","parentThreadId":"root-a"}}}`,
		mcpToolRequest("0", "child-a", "child-turn", ""),
		`<{"method":"serverRequest/resolved","params":{"threadId":"root-a","requestId":0}}`,
		`<{"method":"thread/name/updated","params":{"threadId":"root-a","threadName":"After wrong-thread resolution"}}`,
		`>{"id":0,"result":{"action":"accept","content":null,"_meta":null}}`,
		`<{"method":"serverRequest/resolved","params":{"threadId":"child-a","requestId":0}}`,
		mcpToolRequest("1", "child-a", "child-turn", ""),
		// Synchronize native resolution after the second ref has been observed.
		`>{"id":95,"method":"thread/settings/update","params":{"threadId":"root-a","model":"synthetic-model-2"}}`,
		`<{"id":95,"result":{}}`,
		`<{"method":"serverRequest/resolved","params":{"threadId":"child-a","requestId":1}}`,
	)
	frames = append(frames, codexCloseFrames(96, "other-root")...)
	frames = append(frames, codexCloseFrames(97, "root-a")...)
	core := newTestProvider(t, prepareCodexPeer(t, frames...))
	t.Cleanup(func() { assert.NoError(t, core.Stop(context.Background())) })
	_, err := core.Start(ctx)
	require.NoError(t, err)
	root, err := core.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	closeSessionOnCleanup(t, root)
	other, err := core.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	closeSessionOnCleanup(t, other)
	stream, err := root.Events()
	require.NoError(t, err)
	submitInteractionTurn(t, ctx, root)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	child := nextEvent(t, ctx, stream)
	require.Equal(t, "child-a", eventCodexMeta(t, child)["sourceThreadId"])
	require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
	req := nextInteractionRequested(t, ctx, stream)
	require.Equal(t, agent.EventSessionInfoUpdate, nextEvent(t, ctx, stream).Type())
	answer := agent.PermissionAnswer{Outcome: agent.PermissionSelected, OptionID: "allow-once"}
	_, err = other.Answer(ctx, req.Interaction, answer)
	require.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
	_, err = root.Answer(ctx, req.Interaction, answer)
	require.NoError(t, err)
	closed := nextInteractionClosed(t, ctx, stream)
	assert.Equal(t, req.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionAnswered, closed.Outcome)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
	second := nextInteractionRequested(t, ctx, stream)
	require.NotEqual(t, req.Interaction, second.Interaction)
	_, err = root.(agent.ConfigController).SetConfig(ctx, agent.ConfigChange{ID: agent.ConfigModel, Value: "synthetic-model-2"})
	require.NoError(t, err)
	closed = nextInteractionClosed(t, ctx, stream)
	assert.Equal(t, second.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionCancelled, closed.Outcome)
	assert.Equal(t, "native_resolved", closed.Reason)
	_, err = root.Answer(ctx, second.Interaction, answer)
	require.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
}
