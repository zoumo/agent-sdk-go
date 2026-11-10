package agentcodex

// Stable Error.Reason values specific to the Codex adapter.
const (
	ErrorReasonEventDecodeFailed            = "event_decode_failed"
	ErrorReasonExecutionOptionsNotEncodable = "execution_options_not_encodable"
	ErrorReasonFormOutcomeUnsupported       = "form_outcome_unsupported"
	ErrorReasonInvalidNativeEvent           = "invalid_native_event"
	ErrorReasonPermissionCancelUnsupported  = "permission_cancel_unsupported"
	ErrorReasonProviderStartFailed          = "provider_start_failed"
	ErrorReasonStartOrSteerIdentityMissing  = "start_or_steer_identity_missing"
)
