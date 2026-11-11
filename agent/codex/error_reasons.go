package agentcodex

// Stable Error.Reason values specific to the Codex adapter.
const (
	ErrorReasonEventDecodeFailed           = "event_decode_failed"
	ErrorReasonSessionConfigNotEncodable   = "session_config_not_encodable"
	ErrorReasonFormOutcomeUnsupported      = "form_outcome_unsupported"
	ErrorReasonInvalidNativeEvent          = "invalid_native_event"
	ErrorReasonPermissionCancelUnsupported = "permission_cancel_unsupported"
	ErrorReasonCoreStartFailed             = "core_start_failed"
	ErrorReasonStartOrSteerIdentityMissing = "start_or_steer_identity_missing"
)
