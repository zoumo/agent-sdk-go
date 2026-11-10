package agent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestStableErrorReasons(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"additional dirs unsupported", agent.ErrorReasonAdditionalDirsUnsupported, "additional_dirs_unsupported"},
		{"agent questions disabled", agent.ErrorReasonAgentQuestionsDisabled, "agent_questions_disabled"},
		{"answer kind unsupported", agent.ErrorReasonAnswerKindUnsupported, "answer_kind_unsupported"},
		{"answer outcome unknown", agent.ErrorReasonAnswerOutcomeUnknown, "answer_outcome_unknown"},
		{"content block unsupported", agent.ErrorReasonContentBlockUnsupported, "content_block_unsupported"},
		{"event delivery failed", agent.ErrorReasonEventDeliveryFailed, "event_delivery_failed"},
		{"event ID failed", agent.ErrorReasonEventIDFailed, "event_id_failed"},
		{"event invalid", agent.ErrorReasonEventInvalid, "event_invalid"},
		{"events already claimed", agent.ErrorReasonEventsAlreadyClaimed, "events_already_claimed"},
		{"invalid form answer", agent.ErrorReasonInvalidFormAnswer, "invalid_form_answer"},
		{"invalid form outcome", agent.ErrorReasonInvalidFormOutcome, "invalid_form_outcome"},
		{"invalid interaction ref", agent.ErrorReasonInvalidInteractionRef, "invalid_interaction_ref"},
		{"interaction not pending", agent.ErrorReasonInteractionNotPending, "interaction_not_pending"},
		{"interaction ref failed", agent.ErrorReasonInteractionRefFailed, "interaction_ref_failed"},
		{"missing answer", agent.ErrorReasonMissingAnswer, "missing_answer"},
		{"native request ended", agent.ErrorReasonNativeRequestEnded, "native_request_ended"},
		{"permission option not offered", agent.ErrorReasonPermissionOptionNotOffered, "permission_option_not_offered"},
		{"session closed", agent.ErrorReasonSessionClosed, "session_closed"},
		{"session terminated", agent.ErrorReasonSessionTerminated, "session_terminated"},
		{"text annotations unsupported", agent.ErrorReasonTextAnnotationsUnsupported, "text_annotations_unsupported"},
		{"unknown form question", agent.ErrorReasonUnknownFormQuestion, "unknown_form_question"},
		{"unsupported answer", agent.ErrorReasonUnsupportedAnswer, "unsupported_answer"},
		{"additional system prompt contains NUL", agent.ErrorReasonAdditionalSystemPromptContainsNUL, "additional_system_prompt_contains_nul"},
		{"additional system prompt invalid UTF-8", agent.ErrorReasonAdditionalSystemPromptInvalidUTF8, "additional_system_prompt_invalid_utf8"},
		{"execution option contains NUL", agent.ErrorReasonExecutionOptionContainsNUL, "execution_option_contains_nul"},
		{"execution option empty value", agent.ErrorReasonExecutionOptionEmptyValue, "execution_option_empty_value"},
		{"execution option invalid UTF-8", agent.ErrorReasonExecutionOptionInvalidUTF8, "execution_option_invalid_utf8"},
		{"execution option unknown", agent.ErrorReasonExecutionOptionUnknown, "execution_option_unknown"},
		{"missing model", agent.ErrorReasonMissingModel, "missing_model"},
		{"provider has open sessions", agent.ErrorReasonProviderHasOpenSessions, "provider_has_open_sessions"},
		{"provider not started", agent.ErrorReasonProviderNotStarted, "provider_not_started"},
		{"provider stopped", agent.ErrorReasonProviderStopped, "provider_stopped"},
		{"operation failed suffix", agent.ErrorReasonForOperation("open", agent.ErrorReasonSuffixFailed), "open_failed"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, test.got)
		})
	}
}

func TestOperationErrorReasonWithCode(t *testing.T) {
	tests := []struct {
		code agent.ErrorCode
		want string
	}{
		{agent.ErrorInvalidArgument, "answer_invalid_argument"},
		{agent.ErrorFailedPrecondition, "answer_failed_precondition"},
		{agent.ErrorUnsupported, "answer_unsupported"},
		{agent.ErrorUnavailable, "answer_unavailable"},
		{agent.ErrorResourceExhausted, "answer_resource_exhausted"},
		{agent.ErrorOutcomeUnknown, "answer_outcome_unknown"},
		{agent.ErrorNativeRejected, "answer_native_rejected"},
		{agent.ErrorClosed, "answer_closed"},
	}

	for _, test := range tests {
		t.Run(string(test.code), func(t *testing.T) {
			assert.Equal(t, test.want, agent.ErrorReasonForCode("answer", test.code))
		})
	}
}
