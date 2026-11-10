package agent

// Stable Error.Reason values shared by the unified contract and provider
// adapters. Callers may match these values without parsing Error.Message.
const (
	ErrorReasonAdditionalDirsUnsupported  = "additional_dirs_unsupported"
	ErrorReasonAgentQuestionsDisabled     = "agent_questions_disabled"
	ErrorReasonAnswerKindUnsupported      = "answer_kind_unsupported"
	ErrorReasonAnswerOutcomeUnknown       = "answer_outcome_unknown"
	ErrorReasonContentBlockUnsupported    = "content_block_unsupported"
	ErrorReasonEventDeliveryFailed        = "event_delivery_failed"
	ErrorReasonEventIDFailed              = "event_id_failed"
	ErrorReasonEventInvalid               = "event_invalid"
	ErrorReasonEventsAlreadyClaimed       = "events_already_claimed"
	ErrorReasonInvalidFormAnswer          = "invalid_form_answer"
	ErrorReasonInvalidFormOutcome         = "invalid_form_outcome"
	ErrorReasonInvalidInteractionRef      = "invalid_interaction_ref"
	ErrorReasonInteractionNotPending      = "interaction_not_pending"
	ErrorReasonInteractionRefFailed       = "interaction_ref_failed"
	ErrorReasonMissingAnswer              = "missing_answer"
	ErrorReasonNativeRequestEnded         = "native_request_ended"
	ErrorReasonPermissionOptionNotOffered = "permission_option_not_offered"
	ErrorReasonSessionClosed              = "session_closed"
	ErrorReasonSessionTerminated          = "session_terminated"
	ErrorReasonTextAnnotationsUnsupported = "text_annotations_unsupported"
	ErrorReasonUnknownFormQuestion        = "unknown_form_question"
	ErrorReasonUnsupportedAnswer          = "unsupported_answer"
)

// Stable Error.Reason values produced by provider-neutral validation and
// lifecycle code shared by both adapters.
const (
	ErrorReasonAdditionalSystemPromptContainsNUL = "additional_system_prompt_contains_nul"
	ErrorReasonAdditionalSystemPromptInvalidUTF8 = "additional_system_prompt_invalid_utf8"
	ErrorReasonExecutionOptionContainsNUL        = "execution_option_contains_nul"
	ErrorReasonExecutionOptionEmptyValue         = "execution_option_empty_value"
	ErrorReasonExecutionOptionInvalidUTF8        = "execution_option_invalid_utf8"
	ErrorReasonExecutionOptionUnknown            = "execution_option_unknown"
	ErrorReasonMissingModel                      = "missing_model"
	ErrorReasonProviderHasOpenSessions           = "provider_has_open_sessions"
	ErrorReasonProviderNotStarted                = "provider_not_started"
	ErrorReasonProviderStopped                   = "provider_stopped"
)

// ErrorReasonSuffixFailed is the operation suffix for an unclassified native
// failure. Other operation suffixes are the corresponding ErrorCode values.
const ErrorReasonSuffixFailed = "_failed"

// ErrorReasonForOperation appends a stable reason suffix to an operation.
func ErrorReasonForOperation(operation, suffix string) string {
	return operation + suffix
}

// ErrorReasonForCode appends an ErrorCode to an operation.
func ErrorReasonForCode(operation string, code ErrorCode) string {
	return operation + "_" + string(code)
}
