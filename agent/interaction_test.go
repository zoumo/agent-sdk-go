package agent_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

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
			Content: []agent.ContentBlock{agent.TextBlock{Text: "ls"}},
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
