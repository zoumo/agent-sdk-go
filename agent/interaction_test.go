package agent_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

type embeddedPermissionAnswer struct {
	agent.PermissionAnswer
}

func TestInteractionRequestValidationRejectsInvalidKindFieldCombinations(t *testing.T) {
	schema := map[string]any{"type": "object"}
	options := []agent.PermissionOption{
		{OptionID: "allow", Name: "Allow", Kind: agent.PermissionAllowOnce},
		{OptionID: "reject", Name: "Reject", Kind: agent.PermissionRejectOnce},
	}

	tests := []struct {
		name    string
		request agent.InteractionRequest
	}{
		{
			name: "permission missing tool call",
			request: agent.InteractionRequest{
				Kind:    agent.InteractionPermission,
				Options: options,
			},
		},
		{
			name: "permission without options",
			request: agent.InteractionRequest{
				Kind:       agent.InteractionPermission,
				ToolCallID: "tool-1",
			},
		},
		{
			name: "permission with unknown option kind",
			request: agent.InteractionRequest{
				Kind:       agent.InteractionPermission,
				ToolCallID: "tool-1",
				Options: []agent.PermissionOption{
					{OptionID: "allow", Name: "Allow", Kind: "allow"},
				},
			},
		},
		{
			name: "permission with duplicate option identity",
			request: agent.InteractionRequest{
				Kind:       agent.InteractionPermission,
				ToolCallID: "tool-1",
				Options:    []agent.PermissionOption{options[0], options[0]},
			},
		},
		{
			name: "permission carrying form fields",
			request: agent.InteractionRequest{
				Kind:            agent.InteractionPermission,
				ToolCallID:      "tool-1",
				Options:         options,
				RequestedSchema: schema,
			},
		},
		{
			name: "form missing schema",
			request: agent.InteractionRequest{
				Kind:    agent.InteractionForm,
				Message: "Choose",
			},
		},
		{
			name: "form carrying permission fields",
			request: agent.InteractionRequest{
				Kind:            agent.InteractionForm,
				ToolCallID:      "tool-1",
				Message:         "Choose",
				RequestedSchema: schema,
			},
		},
		{
			name: "URL missing destination",
			request: agent.InteractionRequest{
				Kind:    agent.InteractionURL,
				Message: "Open the URL",
			},
		},
		{
			name: "unknown kind",
			request: agent.InteractionRequest{
				Kind: "oauth",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.request.Validate()

			require.Error(t, err)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
		})
	}
}

func TestValidInteractionRequests(t *testing.T) {
	schema := map[string]any{"type": "object"}
	tests := []agent.InteractionRequest{
		{
			Kind:       agent.InteractionPermission,
			ToolCallID: "tool-1",
			Title:      "Run command?",
			Options: []agent.PermissionOption{
				{OptionID: "allow", Name: "Allow", Kind: agent.PermissionAllowOnce},
			},
			Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "ls"}}},
		},
		{
			Kind:            agent.InteractionForm,
			Message:         "Choose an option",
			RequestedSchema: schema,
		},
		{
			Kind:    agent.InteractionURL,
			Message: "Finish sign-in",
			URL:     "https://example.test/sign-in",
		},
	}

	for _, request := range tests {
		require.NoError(t, request.Validate())
	}
}

type embeddedAnswer struct {
	agent.Answer
}

func TestAnswerRejectsEmbeddedInterfaceWrappers(t *testing.T) {
	for _, tt := range []struct {
		name       string
		answer     agent.Answer
		wantReason string
	}{
		{name: "nil embedded value", answer: embeddedAnswer{}, wantReason: "unsupported_answer"},
		{name: "nil embedded pointer", answer: &embeddedAnswer{}, wantReason: "unsupported_answer"},
		{
			name:       "populated embedded value",
			answer:     embeddedAnswer{Answer: agent.PermissionAnswer{Outcome: agent.PermissionSelected, OptionID: "allow"}},
			wantReason: "unsupported_answer",
		},
		{
			name:       "populated embedded pointer",
			answer:     &embeddedAnswer{Answer: agent.PermissionAnswer{Outcome: agent.PermissionSelected, OptionID: "allow"}},
			wantReason: "unsupported_answer",
		},
		{name: "typed nil wrapper", answer: (*embeddedAnswer)(nil), wantReason: "missing_answer"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				got, err := agent.MatchAnswer(agent.InteractionPermission, tt.answer)
				assert.Nil(t, got)
				var classified *agent.Error
				require.ErrorAs(t, err, &classified)
				assert.Equal(t, agent.ErrorInvalidArgument, classified.Code)
				assert.Equal(t, tt.wantReason, classified.Reason)
			})
			require.NotPanics(t, func() {
				err := agent.ValidateAnswer(agent.InteractionPermission, tt.answer)
				var classified *agent.Error
				require.ErrorAs(t, err, &classified)
				assert.Equal(t, agent.ErrorInvalidArgument, classified.Code)
				assert.Equal(t, tt.wantReason, classified.Reason)
			})
		})
	}
}

func TestMatchAnswerCanonicalizesAndValidates(t *testing.T) {
	permission := agent.PermissionAnswer{
		Outcome:  agent.PermissionSelected,
		OptionID: "allow",
	}
	form := agent.FormAnswer{
		Outcome: agent.FormAccept,
		Content: map[string]any{"choice": "a"},
	}
	url := agent.URLAnswer{Outcome: agent.URLAccept}

	tests := []struct {
		name       string
		kind       agent.InteractionKind
		answer     agent.Answer
		want       agent.Answer
		wantReason string
	}{
		{
			name:   "permission value",
			kind:   agent.InteractionPermission,
			answer: permission,
			want:   permission,
		},
		{
			name:   "permission pointer",
			kind:   agent.InteractionPermission,
			answer: &permission,
			want:   permission,
		},
		{
			name:   "form value",
			kind:   agent.InteractionForm,
			answer: form,
			want:   form,
		},
		{
			name:   "form pointer",
			kind:   agent.InteractionForm,
			answer: &form,
			want:   form,
		},
		{
			name:   "URL value",
			kind:   agent.InteractionURL,
			answer: url,
			want:   url,
		},
		{
			name:   "URL pointer",
			kind:   agent.InteractionURL,
			answer: &url,
			want:   url,
		},
		{
			name:       "nil",
			kind:       agent.InteractionPermission,
			wantReason: "missing_answer",
		},
		{
			name:       "typed nil permission",
			kind:       agent.InteractionPermission,
			answer:     (*agent.PermissionAnswer)(nil),
			wantReason: "missing_answer",
		},
		{
			name:       "typed nil form",
			kind:       agent.InteractionForm,
			answer:     (*agent.FormAnswer)(nil),
			wantReason: "missing_answer",
		},
		{
			name:       "typed nil URL",
			kind:       agent.InteractionURL,
			answer:     (*agent.URLAnswer)(nil),
			wantReason: "missing_answer",
		},
		{
			name:       "wrong kind",
			kind:       agent.InteractionPermission,
			answer:     form,
			wantReason: "answer_kind_mismatch",
		},
		{
			name:       "unsupported wrapper",
			kind:       agent.InteractionPermission,
			answer:     embeddedPermissionAnswer{PermissionAnswer: permission},
			wantReason: "unsupported_answer",
		},
		{
			name:       "unsupported wrapper pointer",
			kind:       agent.InteractionPermission,
			answer:     &embeddedPermissionAnswer{PermissionAnswer: permission},
			wantReason: "unsupported_answer",
		},
		{
			name:       "selected permission without option",
			kind:       agent.InteractionPermission,
			answer:     agent.PermissionAnswer{Outcome: agent.PermissionSelected},
			wantReason: "missing_permission_option_id",
		},
		{
			name: "canceled permission with option",
			kind: agent.InteractionPermission,
			answer: agent.PermissionAnswer{
				Outcome:  agent.PermissionCancelled,
				OptionID: "allow",
			},
			wantReason: "unexpected_permission_option_id",
		},
		{
			name:       "unknown permission outcome",
			kind:       agent.InteractionPermission,
			answer:     agent.PermissionAnswer{Outcome: "maybe", OptionID: "allow"},
			wantReason: "invalid_permission_outcome",
		},
		{
			name:       "accepted form without content",
			kind:       agent.InteractionForm,
			answer:     agent.FormAnswer{Outcome: agent.FormAccept},
			wantReason: "missing_form_answer",
		},
		{
			name: "accepted form with invalid content",
			kind: agent.InteractionForm,
			answer: agent.FormAnswer{
				Outcome: agent.FormAccept,
				Content: map[string]any{"choice": func() {}},
			},
			wantReason: "invalid_form_answer",
		},
		{
			name: "declined form with content",
			kind: agent.InteractionForm,
			answer: agent.FormAnswer{
				Outcome: agent.FormDecline,
				Content: map[string]any{"choice": "a"},
			},
			wantReason: "unexpected_form_answer",
		},
		{
			name:       "unknown form outcome",
			kind:       agent.InteractionForm,
			answer:     agent.FormAnswer{Outcome: "maybe"},
			wantReason: "invalid_form_outcome",
		},
		{
			name:       "unknown URL outcome",
			kind:       agent.InteractionURL,
			answer:     agent.URLAnswer{Outcome: "open"},
			wantReason: "invalid_url_outcome",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := agent.MatchAnswer(tt.kind, tt.answer)

			if tt.wantReason != "" {
				var classified *agent.Error
				require.ErrorAs(t, err, &classified)
				assert.Equal(t, agent.ErrorInvalidArgument, classified.Code)
				assert.Equal(t, tt.wantReason, classified.Reason)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.IsType(t, tt.want, got)
		})
	}
}

func TestValidateAnswerChecksKindAndOutcome(t *testing.T) {
	tests := []struct {
		name    string
		kind    agent.InteractionKind
		answer  agent.Answer
		wantErr bool
	}{
		{
			name:    "nil",
			kind:    agent.InteractionPermission,
			wantErr: true,
		},
		{
			name:    "typed nil permission",
			kind:    agent.InteractionPermission,
			answer:  (*agent.PermissionAnswer)(nil),
			wantErr: true,
		},
		{
			name:    "typed nil form",
			kind:    agent.InteractionForm,
			answer:  (*agent.FormAnswer)(nil),
			wantErr: true,
		},
		{
			name:    "typed nil URL",
			kind:    agent.InteractionURL,
			answer:  (*agent.URLAnswer)(nil),
			wantErr: true,
		},
		{
			name:    "wrong kind",
			kind:    agent.InteractionPermission,
			answer:  agent.FormAnswer{Outcome: agent.FormAccept, Content: map[string]any{}},
			wantErr: true,
		},
		{
			name:   "pointer permission",
			kind:   agent.InteractionPermission,
			answer: &agent.PermissionAnswer{Outcome: agent.PermissionSelected, OptionID: "allow"},
		},
		{
			name:   "pointer form",
			kind:   agent.InteractionForm,
			answer: &agent.FormAnswer{Outcome: agent.FormAccept, Content: map[string]any{"choice": "a"}},
		},
		{
			name:   "pointer URL",
			kind:   agent.InteractionURL,
			answer: &agent.URLAnswer{Outcome: agent.URLAccept},
		},
		{
			name:   "selected permission",
			kind:   agent.InteractionPermission,
			answer: agent.PermissionAnswer{Outcome: agent.PermissionSelected, OptionID: "allow"},
		},
		{
			name:    "selected permission without option",
			kind:    agent.InteractionPermission,
			answer:  agent.PermissionAnswer{Outcome: agent.PermissionSelected},
			wantErr: true,
		},
		{
			name:   "canceled permission",
			kind:   agent.InteractionPermission,
			answer: agent.PermissionAnswer{Outcome: agent.PermissionCancelled},
		},
		{
			name:    "canceled permission with option",
			kind:    agent.InteractionPermission,
			answer:  agent.PermissionAnswer{Outcome: agent.PermissionCancelled, OptionID: "allow"},
			wantErr: true,
		},
		{
			name:    "unknown permission outcome",
			kind:    agent.InteractionPermission,
			answer:  agent.PermissionAnswer{Outcome: "maybe", OptionID: "allow"},
			wantErr: true,
		},
		{
			name:   "accepted form",
			kind:   agent.InteractionForm,
			answer: agent.FormAnswer{Outcome: agent.FormAccept, Content: map[string]any{"choice": "a"}},
		},
		{
			name:    "accepted form without content",
			kind:    agent.InteractionForm,
			answer:  agent.FormAnswer{Outcome: agent.FormAccept},
			wantErr: true,
		},
		{
			name:    "declined form with content",
			kind:    agent.InteractionForm,
			answer:  agent.FormAnswer{Outcome: agent.FormDecline, Content: map[string]any{"choice": "a"}},
			wantErr: true,
		},
		{
			name:   "canceled form",
			kind:   agent.InteractionForm,
			answer: agent.FormAnswer{Outcome: agent.FormCancel},
		},
		{
			name:   "accepted URL",
			kind:   agent.InteractionURL,
			answer: agent.URLAnswer{Outcome: agent.URLAccept},
		},
		{
			name:    "unknown URL outcome",
			kind:    agent.InteractionURL,
			answer:  agent.URLAnswer{Outcome: "open"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := agent.ValidateAnswer(tt.kind, tt.answer)

			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestInteractionRequestRoundTripsJSONByKind(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"choice": map[string]any{"type": "string"},
		},
	}
	original := agent.InteractionRequest{
		Kind:            agent.InteractionForm,
		Message:         "Choose",
		RequestedSchema: schema,
	}

	encoded, err := json.Marshal(original)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"kind": "form",
		"message": "Choose",
		"requestedSchema": {
			"type": "object",
			"properties": {
				"choice": {"type": "string"}
			}
		}
	}`, string(encoded))

	var decoded agent.InteractionRequest
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, original, decoded)
}
