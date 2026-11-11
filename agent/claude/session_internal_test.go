package agentclaude

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/claude"
)

func TestUnknownStateClearsPreviousStateAndRemainsActive(t *testing.T) {
	session := &session{}
	assert.Nil(t, session.updateState(agent.StateRequiresAction))

	previous := session.markUnknownState()
	require.NotNil(t, previous)
	assert.Equal(t, agent.StateRequiresAction, *previous)
	assert.True(t, session.workActive)
	assert.Nil(t, session.state)

	assert.Nil(t, session.updateState(agent.StateIdle))
	assert.False(t, session.workActive)
}

func TestStartOrSteerRefusesConcurrentOrUnknownWrite(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*session)
	}{
		{
			name:  "concurrent write",
			setup: func(s *session) { s.submitting = true },
		},
		{
			name:  "unknown write",
			setup: func(s *session) { s.submitUnknown = true },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := &session{}
			test.setup(session)

			receipt, err := session.StartOrSteer(context.Background(), agent.Input{
				Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "retained"}}},
			})
			require.NoError(t, err)
			assert.Equal(t, agent.InputNotApplied, receipt.Result)
			assert.Empty(t, receipt.Submission)
			require.NotNil(t, receipt.Acceptance.Sent)
			assert.False(t, *receipt.Acceptance.Sent)
			assert.Nil(t, receipt.Acceptance.NativeAccepted)
			assert.Nil(t, receipt.Acceptance.NativeRecorded)
		})
	}
}

func TestKnownNativeStateClearsUnknownWrite(t *testing.T) {
	session := &session{submitUnknown: true}
	session.updateState(agent.StateRunning)
	assert.True(t, session.submitUnknown)

	session.updateState(agent.StateIdle)
	assert.False(t, session.submitUnknown)

	session.submitUnknown = true
	session.markUnknownState()
	assert.True(t, session.submitUnknown)
}

func TestUnknownWriteRemainsLatchedAfterRunningState(t *testing.T) {
	session := &session{}
	stateVersion := session.stateVersion

	session.updateState(agent.StateRunning)
	session.finishInputWrite(true, stateVersion)

	assert.True(t, session.submitUnknown)
	assert.True(t, session.workActive)
}

func TestRelationshipObservationBecomesDiagnosticGap(t *testing.T) {
	session := &session{id: "native-session"}
	received := claude.Received{
		Kind: claude.ReceivedObservation,
		Observation: &claude.Observation{
			Reason:   "tool-owner-unconfirmed",
			Evidence: "native-tool-use",
			Source: claude.SubagentIdentity{
				SessionID: "native-session",
				ToolUseID: "tool-1",
			},
			Parent: &claude.SubagentIdentity{
				SessionID: "native-session",
				TaskID:    "task-1",
			},
		},
	}

	events, err := session.convertReceived(received)
	require.NoError(t, err)
	require.Len(t, events, 1)
	payload, ok := events[0].Payload.(agent.SessionErrorPayload)
	require.True(t, ok)
	assert.Equal(t, "native_relationship_gap", payload.Reason)
	meta := requireClaudeEventMeta(t, events[0])
	assert.Equal(t, "tool-owner-unconfirmed", meta["observationReason"])
	assert.Equal(t, "native-tool-use", meta["evidence"])
	assert.Equal(t, "tool-1", meta["source"].(map[string]any)["toolUseId"])
	assert.Equal(t, "task-1", meta["parent"].(map[string]any)["taskId"])

	for _, reason := range []string{"member-confirmed", "parent-confirmed"} {
		t.Run(reason, func(t *testing.T) {
			confirmed := received
			confirmed.Observation = &claude.Observation{Reason: reason}
			events, err := session.convertReceived(confirmed)
			require.NoError(t, err)
			assert.Empty(t, events)
		})
	}
}

func TestUserMessageTextBlocksRemainOneContentArray(t *testing.T) {
	session := &session{id: "native-session"}
	events, err := session.userEvent(claude.Received{
		Source: claude.SubagentIdentity{
			SessionID:     "native-session",
			AssociationID: "task:task-1",
			TaskID:        "task-1",
			ToolUseID:     "tool-1",
		},
		Parent: &claude.SubagentIdentity{
			SessionID: "native-session",
			TaskID:    "parent-task",
		},
		Event: &claude.Event{
			Type: "user",
			UUID: "event-uuid",
			User: &claude.Message{
				ID: "user-message",
				Content: claude.Content{Blocks: []claude.ContentBlock{
					{
						Type: "text",
						Text: &claude.TextBlock{Text: "child prompt"},
					},
					{
						Type: "text",
						Text: &claude.TextBlock{Text: "additional context"},
					},
				}},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, agent.EventUserMessage, events[0].Type())
	assert.Equal(t, "user-message", events[0].ItemID)
	assert.Equal(t, agent.UserMessage{
		MessageID: "user-message",
		Content: []agent.ContentBlock{
			{Text: &agent.ContentBlockText{Text: "child prompt"}},
			{Text: &agent.ContentBlockText{Text: "additional context"}},
		},
	}, events[0].Payload)
	meta := requireClaudeEventMeta(t, events[0])
	assert.Equal(t, 2, meta["blockCount"])
	assert.Equal(t, "task-1", meta["source"].(map[string]any)["taskId"])
	assert.Equal(t, "parent-task", meta["parent"].(map[string]any)["taskId"])
}

func TestMixedUserContentIsUnsupported(t *testing.T) {
	tests := []struct {
		name   string
		blocks []claude.ContentBlock
	}{
		{
			name: "text tool result text",
			blocks: []claude.ContentBlock{
				{
					Type: "text",
					Text: &claude.TextBlock{Text: "inspect the file"},
				},
				{
					Type: "tool_result",
					ToolResult: &claude.ToolResultBlock{
						ToolUseID: "tool-1",
						Content:   json.RawMessage(`"file contents"`),
					},
				},
				{
					Type: "text",
					Text: &claude.TextBlock{Text: "then summarize it"},
				},
			},
		},
		{
			name: "tool result before text",
			blocks: []claude.ContentBlock{
				{
					Type: "tool_result",
					ToolResult: &claude.ToolResultBlock{
						ToolUseID: "tool-1",
						Content:   json.RawMessage(`"file contents"`),
					},
				},
				{
					Type: "text",
					Text: &claude.TextBlock{Text: "summarize it"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := &session{
				id: "native-session",
				calls: map[string]claudeCall{"tool-1": {
					identity: claudeToolIdentity{
						kind:     agent.ToolKindRead,
						toolName: "Read",
						provider: agent.ToolProviderAgent,
					},
				}},
			}
			events, err := session.userEvent(claude.Received{
				Event: &claude.Event{
					Type: "user",
					UUID: "event-uuid",
					User: &claude.Message{
						ID:      "mixed-message",
						Content: claude.Content{Blocks: tt.blocks},
					},
				},
			})
			require.NoError(t, err)
			require.Len(t, events, 1)
			assert.Equal(t, agent.EventSessionError, events[0].Type())
			assert.Empty(t, events[0].ItemID)
			payload, ok := events[0].Payload.(agent.SessionErrorPayload)
			require.True(t, ok)
			assert.Equal(t, "user_message_mixed_content_unsupported", payload.Reason)
			meta := requireClaudeEventMeta(t, events[0])
			assert.Equal(t, len(tt.blocks), meta["blockCount"])
		})
	}
}

func TestMixedUserContentReportsUnsupportedBlockWithoutDroppingText(t *testing.T) {
	session := &session{id: "native-session"}
	events, err := session.userEvent(claude.Received{
		Event: &claude.Event{
			Type: "user",
			UUID: "event-uuid",
			User: &claude.Message{
				ID: "mixed-message",
				Content: claude.Content{Blocks: []claude.ContentBlock{
					{
						Type: "text",
						Text: &claude.TextBlock{Text: "describe this"},
					},
					{Type: "image", Raw: json.RawMessage(`{"type":"image"}`)},
				}},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, agent.EventUserMessage, events[0].Type())
	payload, ok := events[1].Payload.(agent.SessionErrorPayload)
	require.True(t, ok)
	assert.Equal(t, "user_message_content_unsupported", payload.Reason)
	meta := requireClaudeEventMeta(t, events[1])
	assert.Equal(t, "image", meta["blockType"])
}

func TestMissingContextWindowPreservesUsage(t *testing.T) {
	session := &session{id: "native-session"}
	events, err := session.usageEvents(claude.Received{
		Event: &claude.Event{
			Type: "result",
			Result: &claude.ResultEvent{
				ModelUsage: map[string]claude.ModelUsage{
					"synthetic-model": {
						InputTokens:  7,
						OutputTokens: 3,
						Raw:          json.RawMessage(`{"costUSD":0.25}`),
					},
				},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, agent.EventSessionUsageUpdate, events[0].Type())
	assert.Equal(t, agent.UsageUpdatePayload{
		Used: 10,
		Size: 0,
		Cost: &agent.UsageCost{
			Amount:   json.Number("0.25"),
			Currency: "USD",
		},
	}, events[0].Payload)
	meta := requireClaudeEventMeta(t, events[0])
	assert.Equal(t, true, meta["contextWindowAbsent"])
}

func TestAbortedAssistantFrameBecomesGap(t *testing.T) {
	session := &session{id: "native-session"}
	events, err := session.assistantEvent(claude.Received{
		Event: &claude.Event{
			Type:      "assistant",
			UUID:      "aborted-frame",
			Raw:       json.RawMessage(`{"type":"assistant","aborted":true}`),
			Assistant: &claude.Message{ID: "aborted-response"},
		},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	payload, ok := events[0].Payload.(agent.SessionErrorPayload)
	require.True(t, ok)
	assert.Equal(t, "assistant_aborted", payload.Reason)
}

func TestVisibleBlockTextDistinguishesLegalEmptyFromMalformed(t *testing.T) {
	tests := []struct {
		name  string
		block string
		kind  string
		text  string
		valid bool
	}{
		{
			name:  "legal empty text",
			block: `{"type":"text","text":""}`,
			kind:  "text",
			valid: true,
		},
		{
			name:  "missing text",
			block: `{"type":"text"}`,
			kind:  "text",
		},
		{
			name:  "null text",
			block: `{"type":"text","text":null}`,
			kind:  "text",
		},
		{
			name:  "wrong text type",
			block: `{"type":"text","text":1}`,
			kind:  "text",
		},
		{
			name:  "legal empty thinking",
			block: `{"type":"thinking","thinking":""}`,
			kind:  "thinking",
			valid: true,
		},
		{
			name:  "null thinking",
			block: `{"type":"thinking","thinking":null}`,
			kind:  "thinking",
		},
		{
			name:  "other block",
			block: `{"type":"tool_use","id":"tool-1"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var block map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(test.block), &block))
			kind, text, valid := visibleBlockText(block)
			assert.Equal(t, test.kind, kind)
			assert.Equal(t, test.text, text)
			assert.Equal(t, test.valid, valid)
		})
	}
}

func TestMalformedAssistantBlockBecomesGap(t *testing.T) {
	session := &session{id: "native-session"}
	events, err := session.assistantEvent(claude.Received{
		Event: &claude.Event{
			Type: "assistant",
			UUID: "malformed-frame",
			Assistant: &claude.Message{
				ID: "response-malformed",
				Content: claude.Content{
					Raw: json.RawMessage(`[{"type":"text","text":null,"apiBlockIndex":0}]`),
				},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	payload, ok := events[0].Payload.(agent.SessionErrorPayload)
	require.True(t, ok)
	assert.Equal(t, "assistant_block_content_invalid", payload.Reason)
	meta := requireClaudeEventMeta(t, events[0])
	assert.Equal(t, "text", meta["blockType"])
	assert.Equal(t, 0, meta["blockIndex"])
	state := session.blocks[claudeBlockKey{
		response: "response-malformed",
		index:    0,
	}]
	assert.Nil(t, state)
}

func TestEmptyAssistantBlockCompletesWithoutEvent(t *testing.T) {
	session := &session{id: "native-session"}
	events, err := session.assistantEvent(claude.Received{
		Event: &claude.Event{
			Type: "assistant",
			UUID: "empty-frame",
			Assistant: &claude.Message{
				ID: "response-empty",
				Content: claude.Content{
					Raw: json.RawMessage(`[{"type":"text","text":"","apiBlockIndex":0}]`),
				},
			},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, events)
	state := session.blocks[claudeBlockKey{
		response: "response-empty",
		index:    0,
	}]
	require.NotNil(t, state)
	assert.True(t, state.completed)
	assert.Empty(t, state.completedText)
}

func TestNewResponseReleasesPreviousBlockState(t *testing.T) {
	session := &session{}
	first := claudeBlockKey{parent: "agent-1", response: "response-1", index: 0}
	second := claudeBlockKey{parent: "agent-1", response: "response-2", index: 0}

	require.NoError(t, session.beginResponse(first.parent, first.response))
	_, err := session.trackedBlock(first, "text")
	require.NoError(t, err)
	require.NoError(t, session.beginResponse(second.parent, second.response))
	_, err = session.trackedBlock(second, "text")
	require.NoError(t, err)

	assert.NotContains(t, session.blocks, first)
	assert.Contains(t, session.blocks, second)
}

func TestZeroLengthAssistantTextDoesNotCreateEvent(t *testing.T) {
	session := &session{id: "native-session"}
	const (
		response = "response-empty"
		index    = 0
	)
	require.NoError(t, session.beginResponse("", response))
	require.NoError(t, session.noteBlockStart("", index, "text", "", ""))

	events, err := session.assistantEvent(claude.Received{
		Event: &claude.Event{
			Type: "assistant",
			UUID: "empty-frame",
			Raw:  json.RawMessage(`{"type":"assistant","uuid":"empty-frame"}`),
			Assistant: &claude.Message{
				ID:      response,
				Content: claude.Content{Text: new(string)},
			},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestSemanticSystemNotificationBecomesGap(t *testing.T) {
	session := &session{id: "native-session"}
	for _, subtype := range []string{"api_retry", "compact_boundary"} {
		t.Run(subtype, func(t *testing.T) {
			raw := json.RawMessage(
				`{"type":"system","subtype":"` + subtype + `","session_id":"native-session"}`,
			)
			events, err := session.convertReceived(claude.Received{
				Kind: claude.ReceivedNative,
				Event: &claude.Event{
					Type:    "system",
					Subtype: subtype,
					Raw:     raw,
				},
				Raw: raw,
			})
			require.NoError(t, err)
			require.Len(t, events, 1)
			payload, ok := events[0].Payload.(agent.SessionErrorPayload)
			require.True(t, ok)
			assert.Equal(t, "native_event_unsupported", payload.Reason)
			meta := requireClaudeEventMeta(t, events[0])
			assert.Equal(t, subtype, meta["subtype"])
		})
	}
}

func requireClaudeEventMeta(t *testing.T, event agent.Event) map[string]any {
	t.Helper()
	meta, ok := event.Meta["claude"].(map[string]any)
	require.True(t, ok)
	return meta
}
