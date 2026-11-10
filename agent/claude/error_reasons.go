package agentclaude

import "fmt"

// Stable Error.Reason values specific to the Claude adapter.
const (
	ErrorReasonAnswerFailed                     = "answer_failed"
	ErrorReasonAssistantBlockCoordinateInvalid  = "assistant_block_coordinate_invalid"
	ErrorReasonAssistantBlockCoordinateMissing  = "assistant_block_coordinate_missing"
	ErrorReasonConflictingToolBlockIdentity     = "conflicting_tool_block_identity"
	ErrorReasonInitializeBufferExhausted        = "initialize_buffer_exhausted"
	ErrorReasonInitializeIdentityMissing        = "initialize_identity_missing"
	ErrorReasonInitializeReplayed               = "initialize_replayed"
	ErrorReasonInitializeWaitFailed             = "initialize_wait_failed"
	ErrorReasonLiveBlockCapacity                = "live_block_capacity"
	ErrorReasonLiveBlockLaneCapacity            = "live_block_lane_capacity"
	ErrorReasonMultipleContentBlocksUnsupported = "multiple_content_blocks_unsupported"
	ErrorReasonNilContext                       = "nil_context"
	ErrorReasonResumeCallbackDuplicate          = "resume_callback_duplicate"
	ErrorReasonResumeCallbackIdentityMismatch   = "resume_callback_identity_mismatch"
	ErrorReasonResumeConfigDirNotAbsolute       = "resume_config_dir_not_absolute"
	ErrorReasonResumeConfigDirRequired          = "resume_config_dir_required"
	ErrorReasonResumeIdentityConflict           = "resume_identity_conflict"
	ErrorReasonResumeIdentityMismatch           = "resume_identity_mismatch"
	ErrorReasonResumeIdentityTimeout            = "resume_identity_timeout"
	ErrorReasonResumeIdentityWaitFailed         = "resume_identity_wait_failed"
	ErrorReasonResumeInitDuplicate              = "resume_init_duplicate"
	ErrorReasonResumeInitReplayed               = "resume_init_replayed"
	ErrorReasonResumePostcheckFileChanged       = "resume_postcheck_file_changed"
	ErrorReasonResumePostcheckMismatch          = "resume_postcheck_mismatch"
	ErrorReasonResumePostcheckQueryFailed       = "resume_postcheck_query_failed"
	ErrorReasonResumePreflightCwdMismatch       = "resume_preflight_cwd_mismatch"
	ErrorReasonResumePreflightFileInvalid       = "resume_preflight_file_invalid"
	ErrorReasonResumePreflightIdentityMismatch  = "resume_preflight_identity_mismatch"
	ErrorReasonResumePreflightIncomplete        = "resume_preflight_incomplete"
	ErrorReasonResumePreflightMissing           = "resume_preflight_missing"
	ErrorReasonResumePreflightQueryFailed       = "resume_preflight_query_failed"
	ErrorReasonResumeProbeIDFailed              = "resume_probe_id_failed"
	ErrorReasonResumeVersionQueryFailed         = "resume_version_query_failed"
	ErrorReasonResumeVersionUnconfirmed         = "resume_version_unconfirmed"
	ErrorReasonResumeVersionUnsupported         = "resume_version_unsupported"
	ErrorReasonSessionCWDMismatch               = "session_cwd_mismatch"
	ErrorReasonStartOrSteerAcceptanceMissing    = "start_or_steer_acceptance_missing"
	ErrorReasonSubmissionIDFailed               = "submission_id_failed"
	ErrorReasonToolCallCapacity                 = "tool_call_capacity"
	ErrorReasonToolResultContentUnsupported     = "tool_result_content_unsupported"
	ErrorReasonToolResultTextInvalid            = "tool_result_text_invalid"
)

const errorReasonMCPServerFieldUnsupportedFormat = "mcp_server_%d_%s_unsupported"

// ErrorReasonMCPServerFieldUnsupported returns the stable reason for an
// unsupported Claude MCP server field.
func ErrorReasonMCPServerFieldUnsupported(index int, field string) string {
	return fmt.Sprintf(errorReasonMCPServerFieldUnsupportedFormat, index, field)
}
