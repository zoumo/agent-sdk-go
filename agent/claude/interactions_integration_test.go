package agentclaude_test

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

func TestClaudePermissionAnswerOutcomes(t *testing.T) {
	tests := []struct {
		name        string
		requestID   string
		response    string
		answer      agent.PermissionAnswer
		wantOutcome agent.InteractionOutcome
	}{
		{
			name:      "allow once",
			requestID: "permission-allow",
			response: `>{"type":"control_response","response":{"subtype":"success",` +
				`"request_id":"permission-allow","response":{"behavior":"allow",` +
				`"updatedInput":{"file_path":"a.txt"}}}}`,
			answer: agent.PermissionAnswer{
				Outcome:  agent.PermissionSelected,
				OptionID: "allow-once",
			},
			wantOutcome: agent.InteractionAnswered,
		},
		{
			name:      "reject once",
			requestID: "permission-reject",
			response: `>{"type":"control_response","response":{"subtype":"success",` +
				`"request_id":"permission-reject","response":{"behavior":"deny",` +
				`"message":"User denied the request"}}}`,
			answer: agent.PermissionAnswer{
				Outcome:  agent.PermissionSelected,
				OptionID: "reject-once",
			},
			wantOutcome: agent.InteractionAnswered,
		},
		{
			name:      "cancel",
			requestID: "permission-cancel",
			response: `>{"type":"control_response","response":{"subtype":"success",` +
				`"request_id":"permission-cancel","response":{"behavior":"deny",` +
				`"message":"User canceled the request"}}}`,
			answer: agent.PermissionAnswer{
				Outcome: agent.PermissionCancelled,
			},
			wantOutcome: agent.InteractionCancelled,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			frames := []string{
				claudeSessionInitFrame(t, "native-permission", cwd),
				claudeSubmitFrame(t, "run"),
				claudeStateFrame("native-permission", "running", "2026-11-07T14:00:00Z"),
				claudePermissionRequestFrame(
					test.requestID,
					"Read",
					"read-tool",
					`{"file_path":"a.txt"}`,
				),
				test.response,
				claudeStateFrame("native-permission", "running", "2026-11-07T14:00:03Z"),
			}
			session, stream := openClaudeInteractionSession(
				t,
				ctx,
				cwd,
				"passed",
				agent.Features{},
				frames...,
			)

			submitted, err := session.Submit(ctx, agent.Input{
				Content: []agent.ContentBlock{agent.TextBlock{Text: "run"}},
			})
			require.NoError(t, err)
			assert.NotEmpty(t, submitted.Submission)

			running := nextEvent(t, ctx, stream)
			assertStateUpdate(
				t,
				running,
				nil,
				agent.StateRunning,
				time.Date(2026, 11, 7, 14, 0, 0, 0, time.UTC),
			)
			requiresAction := nextEvent(t, ctx, stream)
			assertStateUpdate(
				t,
				requiresAction,
				statePtr(agent.StateRunning),
				agent.StateRequiresAction,
				requiresAction.Time,
			)
			requested := nextClaudeInteractionRequested(t, ctx, stream)
			assert.Equal(t, agent.InteractionPermission, requested.Request.Kind)
			assert.Equal(t, "read-tool", requested.Request.ToolCallID)
			assert.Equal(t, "Read file", requested.Request.Title)
			assert.True(t, requested.Blocking)
			require.Len(t, requested.Request.Options, 2)
			assert.Equal(t,
				agent.PermissionAllowOnce,
				requested.Request.Options[0].Kind,
			)
			assert.Equal(t,
				agent.PermissionRejectOnce,
				requested.Request.Options[1].Kind,
			)
			require.Len(t, requested.Request.Content, 1)
			raw, ok := requested.Request.Content[0].(agent.TextBlock)
			require.True(t, ok)
			assert.Contains(t, raw.Text, `"file_path":"a.txt"`)

			receipt, err := session.Answer(ctx, requested.Interaction, test.answer)
			require.NoError(t, err)
			assert.Equal(t, requested.Interaction, receipt.Interaction)
			require.NotNil(t, receipt.Acceptance.Sent)
			assert.True(t, *receipt.Acceptance.Sent)

			closed := nextClaudeInteractionClosed(t, ctx, stream)
			assert.Equal(t, requested.Interaction, closed.Interaction)
			assert.Equal(t, test.wantOutcome, closed.Outcome)
			assert.Equal(t, "native_state_advanced", closed.Reason)

			settled := nextEvent(t, ctx, stream)
			assertStateUpdate(
				t,
				settled,
				statePtr(agent.StateRequiresAction),
				agent.StateRunning,
				time.Date(2026, 11, 7, 14, 0, 3, 0, time.UTC),
			)

			_, err = session.Answer(ctx, requested.Interaction, test.answer)
			require.Error(t, err)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))

			require.NoError(t, session.Close(ctx))
			_, err = stream.Next(ctx)
			require.ErrorIs(t, err, io.EOF)
		})
	}
}

func TestClaudePermissionRejectsRepeatedAndPostCloseAnswers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := []string{
		claudeSessionInitFrame(t, "native-repeat", cwd),
		claudeSubmitFrame(t, "run"),
		claudeStateFrame("native-repeat", "running", "2026-11-07T14:10:00Z"),
		claudePermissionRequestFrame(
			"permission-repeat",
			"Read",
			"read-tool",
			`{"file_path":"a.txt"}`,
		),
		`>{"type":"control_response","response":{"subtype":"success",` +
			`"request_id":"permission-repeat","response":{"behavior":"allow",` +
			`"updatedInput":{"file_path":"a.txt"}}}}`,
	}
	session, stream := openClaudeInteractionSession(
		t,
		ctx,
		cwd,
		"passed",
		agent.Features{},
		frames...,
	)

	_, err := session.Submit(ctx, agent.Input{
		Content: []agent.ContentBlock{agent.TextBlock{Text: "run"}},
	})
	require.NoError(t, err)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextStateValue(t, ctx, stream))
	requested := nextClaudeInteractionRequested(t, ctx, stream)

	answer := agent.PermissionAnswer{
		Outcome:  agent.PermissionSelected,
		OptionID: "allow-once",
	}
	receipt, err := session.Answer(ctx, requested.Interaction, answer)
	require.NoError(t, err)
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.True(t, *receipt.Acceptance.Sent)

	_, err = session.Answer(ctx, requested.Interaction, answer)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))

	require.NoError(t, session.Close(ctx))
	closed := nextClaudeInteractionClosed(t, ctx, stream)
	assert.Equal(t, requested.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionAnswered, closed.Outcome)
	assert.Equal(t, "session_closed", closed.Reason)

	_, err = session.Answer(ctx, requested.Interaction, answer)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestClaudeConcurrentAnswersHaveSingleAdmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := []string{
		claudeSessionInitFrame(t, "native-concurrent", cwd),
		claudeSubmitFrame(t, "run"),
		claudeStateFrame("native-concurrent", "running", "2026-11-07T14:15:00Z"),
		claudePermissionRequestFrame(
			"permission-concurrent",
			"Read",
			"read-tool",
			`{"file_path":"a.txt"}`,
		),
		`>{"type":"control_response","response":{"subtype":"success",` +
			`"request_id":"permission-concurrent","response":{"behavior":"allow",` +
			`"updatedInput":{"file_path":"a.txt"}}}}`,
	}
	session, stream := openClaudeInteractionSession(
		t,
		ctx,
		cwd,
		"passed",
		agent.Features{},
		frames...,
	)

	_, err := session.Submit(ctx, agent.Input{
		Content: []agent.ContentBlock{agent.TextBlock{Text: "run"}},
	})
	require.NoError(t, err)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextStateValue(t, ctx, stream))
	requested := nextClaudeInteractionRequested(t, ctx, stream)

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			<-start
			_, answerErr := session.Answer(
				ctx,
				requested.Interaction,
				agent.PermissionAnswer{
					Outcome:  agent.PermissionSelected,
					OptionID: "allow-once",
				},
			)
			results <- answerErr
		})
	}
	close(start)
	workers.Wait()
	close(results)

	var succeeded, rejected int
	for answerErr := range results {
		switch {
		case answerErr == nil:
			succeeded++
		case agent.CodeOf(answerErr) == agent.ErrorFailedPrecondition:
			rejected++
		default:
			require.NoError(t, answerErr)
		}
	}
	assert.Equal(t, 1, succeeded)
	assert.Equal(t, 1, rejected)

	require.NoError(t, session.Close(ctx))
	closed := nextClaudeInteractionClosed(t, ctx, stream)
	assert.Equal(t, requested.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionAnswered, closed.Outcome)
	assert.Equal(t, "session_closed", closed.Reason)
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestClaudeInteractionRefsAreSessionScopedAndOpaque(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	firstFrames := append([]string(nil), mockClaudeInit...)
	firstFrames = append(firstFrames,
		claudeSessionInitFrame(t, "native-scoped-first", cwd),
		claudeSubmitFrame(t, "run"),
		claudeStateFrame("native-scoped-first", "running", "2026-11-07T14:17:00Z"),
		claudePermissionRequestFrame(
			"permission-scoped",
			"Read",
			"read-tool",
			`{"file_path":"a.txt"}`,
		),
		`>{"type":"control_response","response":{"subtype":"success",`+
			`"request_id":"permission-scoped","response":{"behavior":"allow",`+
			`"updatedInput":{"file_path":"a.txt"}}}}`,
	)
	secondFrames := append([]string(nil), mockClaudeInit...)
	secondFrames = append(secondFrames,
		claudeSessionInitFrame(t, "native-scoped-second", cwd),
	)
	firstPeer := prepareClaudePeer(t, "passed", firstFrames...)
	secondPeer := prepareClaudePeer(t, "passed", secondFrames...)
	provider := agentclaude.New(agentclaude.Config{
		CLIPath: sequencedClaudeLauncher(t, firstPeer, secondPeer),
	})
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	first, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, first)
	second, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, second)

	firstStream, err := first.Events()
	require.NoError(t, err)
	_, err = first.Submit(ctx, agent.Input{
		Content: []agent.ContentBlock{agent.TextBlock{Text: "run"}},
	})
	require.NoError(t, err)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, firstStream))
	require.Equal(t, agent.StateRequiresAction, nextStateValue(t, ctx, firstStream))
	requested := nextClaudeInteractionRequested(t, ctx, firstStream)
	assert.NotEmpty(t, requested.Interaction)

	answer := agent.PermissionAnswer{
		Outcome:  agent.PermissionSelected,
		OptionID: "allow-once",
	}
	_, err = second.Answer(ctx, requested.Interaction, answer)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))

	_, err = first.Answer(ctx, agent.InteractionRef("forged"), answer)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))

	receipt, err := first.Answer(ctx, requested.Interaction, answer)
	require.NoError(t, err)
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.True(t, *receipt.Acceptance.Sent)

	require.NoError(t, first.Close(ctx))
	require.NoError(t, second.Close(ctx))
	require.NoError(t, provider.Stop(ctx))
}

func TestClaudeFormAnswerOutcomes(t *testing.T) {
	const input = `{"questions":[{"question":"Which format?",` +
		`"header":"Format","options":[{"label":"json","description":"JSON"}],` +
		`"multiSelect":false},{"question":"Which sections?",` +
		`"options":[{"label":"intro"},{"label":"end"}],"multiSelect":true}]}`
	tests := []struct {
		name        string
		response    string
		answer      agent.FormAnswer
		wantOutcome agent.InteractionOutcome
	}{
		{
			name: "accept",
			response: `>{"type":"control_response","response":{"subtype":"success",` +
				`"request_id":"question","response":{"behavior":"allow","updatedInput":` +
				`{"questions":[{"question":"Which format?","header":"Format",` +
				`"options":[{"label":"json","description":"JSON"}],` +
				`"multiSelect":false},{"question":"Which sections?",` +
				`"options":[{"label":"intro"},{"label":"end"}],"multiSelect":true}],` +
				`"answers":{"Which format?":"json",` +
				`"Which sections?":["intro","end"]}}}}}`,
			answer: agent.FormAnswer{
				Outcome: agent.FormAccept,
				Content: map[string]any{"answers": map[string]any{
					"Which format?":   "json",
					"Which sections?": []any{"intro", "end"},
				}},
			},
			wantOutcome: agent.InteractionAnswered,
		},
		{
			name: "decline",
			response: `>{"type":"control_response","response":{"subtype":"success",` +
				`"request_id":"question","response":{"behavior":"deny",` +
				`"message":"User declined the questions"}}}`,
			answer:      agent.FormAnswer{Outcome: agent.FormDecline},
			wantOutcome: agent.InteractionCompleted,
		},
		{
			name: "cancel",
			response: `>{"type":"control_response","response":{"subtype":"success",` +
				`"request_id":"question","response":{"behavior":"deny",` +
				`"message":"User canceled the questions"}}}`,
			answer:      agent.FormAnswer{Outcome: agent.FormCancel},
			wantOutcome: agent.InteractionCancelled,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			frames := []string{
				claudeSessionInitFrame(t, "native-form", cwd),
				claudeSubmitFrame(t, "ask"),
				claudeStateFrame("native-form", "running", "2026-11-07T14:20:00Z"),
				claudePermissionRequestFrame(
					"question",
					"AskUserQuestion",
					"question-tool",
					input,
				),
				test.response,
				claudeStateFrame("native-form", "running", "2026-11-07T14:20:03Z"),
			}
			session, stream := openClaudeInteractionSession(
				t,
				ctx,
				cwd,
				"passed",
				agent.Features{},
				frames...,
			)

			_, err := session.Submit(ctx, agent.Input{
				Content: []agent.ContentBlock{agent.TextBlock{Text: "ask"}},
			})
			require.NoError(t, err)
			require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))
			require.Equal(t, agent.StateRequiresAction, nextStateValue(t, ctx, stream))
			requested := nextClaudeInteractionRequested(t, ctx, stream)
			assert.Equal(t, agent.InteractionForm, requested.Request.Kind)
			assert.Equal(t, "Answer native Claude questions", requested.Request.Message)
			requestedProperties, ok := requested.Request.RequestedSchema["properties"].(map[string]any)
			require.True(t, ok)
			answers, ok := requestedProperties["answers"].(map[string]any)
			require.True(t, ok)
			answerProperties, ok := answers["properties"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, map[string]any{"type": "string"}, answerProperties["Which format?"])
			multiSelectSchema, ok := answerProperties["Which sections?"].(map[string]any)
			require.True(t, ok)
			alternatives, ok := multiSelectSchema["anyOf"].([]any)
			require.True(t, ok)
			assert.Len(t, alternatives, 2)
			assert.Len(t, requested.Request.RequestedSchema["x-claude-questions"], 2)

			receipt, err := session.Answer(ctx, requested.Interaction, test.answer)
			require.NoError(t, err)
			assert.Equal(t, requested.Interaction, receipt.Interaction)
			require.NotNil(t, receipt.Acceptance.Sent)
			assert.True(t, *receipt.Acceptance.Sent)

			closed := nextClaudeInteractionClosed(t, ctx, stream)
			assert.Equal(t, requested.Interaction, closed.Interaction)
			assert.Equal(t, test.wantOutcome, closed.Outcome)
			assert.Equal(t, "native_state_advanced", closed.Reason)
			require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))

			require.NoError(t, session.Close(ctx))
			_, err = stream.Next(ctx)
			require.ErrorIs(t, err, io.EOF)
		})
	}
}

func TestClaudeFormRejectsMalformedAnswersBeforeWrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	const input = `{"questions":[{"question":"Which format?",` +
		`"options":[{"label":"json"},{"label":"yaml"}]},{"question":"Which formats?",` +
		`"options":[{"label":"json"},{"label":"yaml"}],"multiSelect":true}]}`
	frames := []string{
		claudeSessionInitFrame(t, "native-form-invalid", cwd),
		claudeSubmitFrame(t, "ask"),
		claudeStateFrame("native-form-invalid", "running", "2026-11-07T14:30:00Z"),
		claudePermissionRequestFrame(
			"question-invalid",
			"AskUserQuestion",
			"question-tool",
			input,
		),
		`>{"type":"control_response","response":{"subtype":"success",` +
			`"request_id":"question-invalid","response":{"behavior":"allow",` +
			`"updatedInput":{"questions":[{"question":"Which format?",` +
			`"options":[{"label":"json"},{"label":"yaml"}]},{"question":"Which formats?",` +
			`"options":[{"label":"json"},{"label":"yaml"}],"multiSelect":true}],` +
			`"answers":{"Which format?":"json",` +
			`"Which formats?":["json","yaml"]}}}}}`,
		claudeStateFrame("native-form-invalid", "running", "2026-11-07T14:30:03Z"),
	}
	session, stream := openClaudeInteractionSession(
		t,
		ctx,
		cwd,
		"passed",
		agent.Features{},
		frames...,
	)

	_, err := session.Submit(ctx, agent.Input{
		Content: []agent.ContentBlock{agent.TextBlock{Text: "ask"}},
	})
	require.NoError(t, err)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextStateValue(t, ctx, stream))
	requested := nextClaudeInteractionRequested(t, ctx, stream)

	tests := []struct {
		name   string
		answer agent.Answer
		code   agent.ErrorCode
		reason string
	}{
		{
			name: "wrong kind",
			answer: agent.PermissionAnswer{
				Outcome:  agent.PermissionSelected,
				OptionID: "allow-once",
			},
			code: agent.ErrorInvalidArgument,
		},
		{
			name:   "missing answers",
			answer: agent.FormAnswer{Outcome: agent.FormAccept, Content: map[string]any{}},
			code:   agent.ErrorInvalidArgument,
		},
		{
			name: "unknown question",
			answer: agent.FormAnswer{
				Outcome: agent.FormAccept,
				Content: map[string]any{"answers": map[string]any{
					"Other?": "json",
				}},
			},
			code:   agent.ErrorInvalidArgument,
			reason: "unknown_form_question",
		},
		{
			name: "array for single select",
			answer: agent.FormAnswer{
				Outcome: agent.FormAccept,
				Content: map[string]any{"answers": map[string]any{
					"Which format?": []any{"json", "yaml"},
				}},
			},
			code:   agent.ErrorInvalidArgument,
			reason: "invalid_form_answer",
		},
		{
			name: "non-string multi select label",
			answer: agent.FormAnswer{
				Outcome: agent.FormAccept,
				Content: map[string]any{"answers": map[string]any{
					"Which formats?": []any{"json", 1},
				}},
			},
			code:   agent.ErrorInvalidArgument,
			reason: "invalid_form_answer",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := session.Answer(ctx, requested.Interaction, test.answer)
			require.Error(t, err)
			assert.Equal(t, test.code, agent.CodeOf(err))
			if test.reason != "" {
				var classified *agent.Error
				require.ErrorAs(t, err, &classified)
				assert.Equal(t, test.reason, classified.Reason)
			}
		})
	}

	receipt, err := session.Answer(ctx, requested.Interaction, agent.FormAnswer{
		Outcome: agent.FormAccept,
		Content: map[string]any{"answers": map[string]any{
			"Which format?":  "json",
			"Which formats?": []any{"json", "yaml"},
		}},
	})
	require.NoError(t, err)
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.True(t, *receipt.Acceptance.Sent)

	closed := nextClaudeInteractionClosed(t, ctx, stream)
	assert.Equal(t, agent.InteractionAnswered, closed.Outcome)
	assert.Equal(t, "native_state_advanced", closed.Reason)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))

	require.NoError(t, session.Close(ctx))
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestClaudeDisabledQuestionsRejectNativeForm(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := []string{
		claudeSessionInitFrame(t, "native-form-disabled", cwd),
		claudeSubmitFrame(t, "ask"),
		claudeStateFrame("native-form-disabled", "running", "2026-11-07T14:40:00Z"),
		claudePermissionRequestFrame(
			"question-disabled",
			"AskUserQuestion",
			"question-tool",
			`{"questions":[{"question":"Which format?","options":[{"label":"json"}]}]}`,
		),
		`>{"type":"control_response","response":{"subtype":"success",` +
			`"request_id":"question-disabled","response":{"behavior":"deny",` +
			`"message":"AskUserQuestion is disabled for this session"}}}`,
	}
	disabled := false
	session, stream := openClaudeInteractionSessionWithLaunch(
		t,
		ctx,
		cwd,
		"passed",
		agent.Features{AgentQuestions: &disabled},
		launchClaudeMockWithoutUnsupportedArgs,
		frames...,
	)

	assert.False(t, session.EffectiveFeatures().AgentQuestions)
	assert.False(t, session.Capabilities().Supports(agent.CapabilityForm))
	_, err := session.Submit(ctx, agent.Input{
		Content: []agent.ContentBlock{agent.TextBlock{Text: "ask"}},
	})
	require.NoError(t, err)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))

	gap := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventSessionError, gap.Type())
	assert.Equal(t, agent.SessionErrorPayload{
		Message: "Claude questions are disabled for this session",
		Reason:  "agent_questions_disabled",
	}, gap.Payload)
	assert.Equal(t, true, requireClaudeMeta(t, gap)["requestRejected"])

	require.NoError(t, session.Close(ctx))
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestClaudeNativeCancelAndSessionCloseInvalidateInteraction(t *testing.T) {
	tests := []struct {
		name       string
		cancel     string
		closeFirst bool
		reason     string
		report     string
	}{
		{
			name:   "native cancel",
			cancel: `<{"type":"control_cancel_request","request_id":"permission-pending"}`,
			reason: "native_cancel",
			report: "passed",
		},
		{
			name:       "session close",
			closeFirst: true,
			reason:     "session_closed",
			report:     "failed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			frames := []string{
				claudeSessionInitFrame(t, "native-pending", cwd),
				claudeSubmitFrame(t, "run"),
				claudeStateFrame("native-pending", "running", "2026-11-07T14:50:00Z"),
				claudePermissionRequestFrame(
					"permission-pending",
					"Read",
					"read-tool",
					`{"file_path":"a.txt"}`,
				),
			}
			if test.cancel != "" {
				frames = append(frames, test.cancel)
			}
			session, stream := openClaudeInteractionSession(
				t,
				ctx,
				cwd,
				test.report,
				agent.Features{},
				frames...,
			)

			_, err := session.Submit(ctx, agent.Input{
				Content: []agent.ContentBlock{agent.TextBlock{Text: "run"}},
			})
			require.NoError(t, err)
			running := nextClaudeStateUpdate(t, ctx, stream)
			require.NotNil(t, running.State)
			assert.Equal(t, agent.StateRunning, *running.State)
			requiresAction := nextClaudeStateUpdate(t, ctx, stream)
			require.NotNil(t, requiresAction.PreviousState)
			require.NotNil(t, requiresAction.State)
			assert.Equal(t, agent.StateRunning, *requiresAction.PreviousState)
			assert.Equal(t, agent.StateRequiresAction, *requiresAction.State)
			requested := nextClaudeInteractionRequested(t, ctx, stream)

			if test.closeFirst {
				require.NoError(t, session.Close(ctx))
			}
			closed := nextClaudeInteractionClosed(t, ctx, stream)
			assert.Equal(t, requested.Interaction, closed.Interaction)
			assert.Equal(t, agent.InteractionCancelled, closed.Outcome)
			assert.Equal(t, test.reason, closed.Reason)

			_, err = session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
				Outcome:  agent.PermissionSelected,
				OptionID: "allow-once",
			})
			require.Error(t, err)
			if test.closeFirst {
				assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
			} else {
				assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
				require.NoError(t, session.Close(ctx))
			}
			_, err = stream.Next(ctx)
			require.ErrorIs(t, err, io.EOF)
		})
	}
}

func TestClaudeInterruptTargetsActiveSubmissionAndClearsOnIdle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := []string{
		claudeSessionInitFrame(t, "native-interrupt", cwd),
		claudeSubmitFrame(t, "run"),
		claudeStateFrame("native-interrupt", "running", "2026-11-07T15:00:00Z"),
		`>{"type":"control_request","request_id":"go_1","request":{"subtype":"interrupt"}}`,
		`<{"type":"control_response","response":{"subtype":"success",` +
			`"request_id":"go_1","response":{}}}`,
		claudeStateFrame("native-interrupt", "idle", "2026-11-07T15:00:03Z"),
	}
	session, stream := openClaudeInteractionSession(
		t,
		ctx,
		cwd,
		"passed",
		agent.Features{},
		frames...,
	)

	submitted, err := session.Submit(ctx, agent.Input{
		Content: []agent.ContentBlock{agent.TextBlock{Text: "run"}},
	})
	require.NoError(t, err)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))

	receipt, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	assert.Equal(t, submitted.Submission, receipt.Target)
	assert.Equal(t, agent.InterruptAcknowledged, receipt.Result)

	require.Equal(t, agent.StateIdle, nextStateValue(t, ctx, stream))
	noWork, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	assert.Equal(t, agent.InterruptReceipt{Result: agent.InterruptNoActiveWork}, noWork)

	require.NoError(t, session.Close(ctx))
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestClaudeInterruptAfterWriteReportsUnknownOutcome(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := []string{
		claudeSessionInitFrame(t, "native-interrupt-unknown", cwd),
		claudeSubmitFrame(t, "run"),
		claudeStateFrame("native-interrupt-unknown", "running", "2026-11-07T15:10:00Z"),
		`>{"type":"control_request","request_id":"go_1","request":{"subtype":"interrupt"}}`,
		`!`,
	}
	session, stream := openClaudeInteractionSession(
		t,
		ctx,
		cwd,
		"failed",
		agent.Features{},
		frames...,
	)

	_, err := session.Submit(ctx, agent.Input{
		Content: []agent.ContentBlock{agent.TextBlock{Text: "run"}},
	})
	require.NoError(t, err)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))

	_, err = session.Interrupt(ctx, agent.InterruptOptions{})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(err))

	select {
	case <-session.Done():
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}
}

func openClaudeInteractionSession(
	t *testing.T,
	ctx context.Context,
	cwd string,
	wantStatus string,
	features agent.Features,
	frames ...string,
) (agent.Session, agent.EventStream) {
	return openClaudeInteractionSessionWithLaunch(
		t,
		ctx,
		cwd,
		wantStatus,
		features,
		nil,
		frames...,
	)
}

func openClaudeInteractionSessionWithLaunch(
	t *testing.T,
	ctx context.Context,
	cwd string,
	wantStatus string,
	features agent.Features,
	launch func(testing.TB, *mocktest.Session) (string, []string),
	frames ...string,
) (agent.Session, agent.EventStream) {
	t.Helper()
	all := append([]string(nil), mockClaudeInit...)
	all = append(all, frames...)
	peerSession := prepareClaudePeer(t, wantStatus, all...)
	launcher, env := claudePeerLauncher(t, peerSession)
	if launch != nil {
		launcher, env = launch(t, peerSession)
	}
	provider := agentclaude.New(agentclaude.Config{
		CLIPath: launcher,
		Env:     env,
	})
	t.Cleanup(func() {
		assert.NoError(t, provider.Stop(context.Background()))
	})

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd:      cwd,
		Features: features,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = session.Close(context.Background())
	})
	stream, err := session.Events()
	require.NoError(t, err)
	return session, stream
}

func claudePeerLauncher(t testing.TB, peerSession *mocktest.Session) (string, []string) {
	t.Helper()
	launcher, err := peerSession.Launcher("main")
	require.NoError(t, err)
	return launcher, peerSession.Env
}

func claudeSessionInitFrame(t *testing.T, sessionID, cwd string) string {
	t.Helper()
	return `<{"type":"system","subtype":"init","session_id":"` + sessionID +
		`","cwd":` + jsonString(t, cwd) +
		`,"claude_code_version":"2.1.258"}`
}

func claudeSubmitFrame(t *testing.T, text string) string {
	t.Helper()
	return `>{"type":"user","session_id":"","parent_tool_use_id":null,` +
		`"message":{"role":"user","content":` + jsonString(t, text) + `}}`
}

func claudeStateFrame(sessionID, state, at string) string {
	return `<{"type":"system","subtype":"session_state_changed","session_id":"` +
		sessionID + `","state":"` + state + `","timestamp":"` + at + `"}`
}

func claudePermissionRequestFrame(
	requestID string,
	toolName string,
	toolUseID string,
	input string,
) string {
	return `<{"type":"control_request","request_id":"` + requestID +
		`","request":{"subtype":"can_use_tool","tool_name":"` + toolName +
		`","tool_use_id":"` + toolUseID + `","title":"Read file",` +
		`"input":` + input + `}}`
}

func nextClaudeInteractionRequested(
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

func nextClaudeInteractionClosed(
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

func nextStateValue(
	t *testing.T,
	ctx context.Context,
	stream agent.EventStream,
) agent.SessionState {
	t.Helper()
	payload := nextClaudeStateUpdate(t, ctx, stream)
	require.NotNil(t, payload.State)
	return *payload.State
}

func nextClaudeStateUpdate(
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
		"unexpected event: %#v",
		event,
	)
	payload, ok := event.Payload.(agent.StateUpdatePayload)
	require.True(t, ok)
	return payload
}
