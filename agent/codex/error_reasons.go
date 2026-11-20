package agentcodex

// Stable Error.Reason values specific to the Codex adapter.
const (
	ErrorReasonInvalidSandboxMode          = "invalid_sandbox_mode"
	ErrorReasonPermissionPresetMismatch    = "permission_preset_mismatch"
	ErrorReasonEventDecodeFailed           = "event_decode_failed"
	ErrorReasonSessionConfigNotEncodable   = "session_config_not_encodable"
	ErrorReasonFormOutcomeUnsupported      = "form_outcome_unsupported"
	ErrorReasonInvalidNativeEvent          = "invalid_native_event"
	ErrorReasonPermissionCancelUnsupported = "permission_cancel_unsupported"
	ErrorReasonCoreStartFailed             = "core_start_failed"
	ErrorReasonSandboxProfileConflict      = "sandbox_profile_conflict"
	ErrorReasonStartOrSteerIdentityMissing = "start_or_steer_identity_missing"
)
