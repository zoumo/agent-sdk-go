package agentclaude_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zoumo/agent-sdk-go/agent/claude"
)

func TestStableErrorReasons(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"answer failed", agentclaude.ErrorReasonAnswerFailed, "answer_failed"},
		{"assistant block coordinate invalid", agentclaude.ErrorReasonAssistantBlockCoordinateInvalid, "assistant_block_coordinate_invalid"},
		{"assistant block coordinate missing", agentclaude.ErrorReasonAssistantBlockCoordinateMissing, "assistant_block_coordinate_missing"},
		{"conflicting tool block identity", agentclaude.ErrorReasonConflictingToolBlockIdentity, "conflicting_tool_block_identity"},
		{"initialize buffer exhausted", agentclaude.ErrorReasonInitializeBufferExhausted, "initialize_buffer_exhausted"},
		{"initialize identity missing", agentclaude.ErrorReasonInitializeIdentityMissing, "initialize_identity_missing"},
		{"initialize replayed", agentclaude.ErrorReasonInitializeReplayed, "initialize_replayed"},
		{"initialize wait failed", agentclaude.ErrorReasonInitializeWaitFailed, "initialize_wait_failed"},
		{"live block capacity", agentclaude.ErrorReasonLiveBlockCapacity, "live_block_capacity"},
		{"live block lane capacity", agentclaude.ErrorReasonLiveBlockLaneCapacity, "live_block_lane_capacity"},
		{"multiple content blocks unsupported", agentclaude.ErrorReasonMultipleContentBlocksUnsupported, "multiple_content_blocks_unsupported"},
		{"nil context", agentclaude.ErrorReasonNilContext, "nil_context"},
		{"resume callback duplicate", agentclaude.ErrorReasonResumeCallbackDuplicate, "resume_callback_duplicate"},
		{"resume callback identity mismatch", agentclaude.ErrorReasonResumeCallbackIdentityMismatch, "resume_callback_identity_mismatch"},
		{"resume config dir not absolute", agentclaude.ErrorReasonResumeConfigDirNotAbsolute, "resume_config_dir_not_absolute"},
		{"resume config dir required", agentclaude.ErrorReasonResumeConfigDirRequired, "resume_config_dir_required"},
		{"resume identity conflict", agentclaude.ErrorReasonResumeIdentityConflict, "resume_identity_conflict"},
		{"resume identity mismatch", agentclaude.ErrorReasonResumeIdentityMismatch, "resume_identity_mismatch"},
		{"resume identity timeout", agentclaude.ErrorReasonResumeIdentityTimeout, "resume_identity_timeout"},
		{"resume identity wait failed", agentclaude.ErrorReasonResumeIdentityWaitFailed, "resume_identity_wait_failed"},
		{"resume init duplicate", agentclaude.ErrorReasonResumeInitDuplicate, "resume_init_duplicate"},
		{"resume init replayed", agentclaude.ErrorReasonResumeInitReplayed, "resume_init_replayed"},
		{"resume postcheck file changed", agentclaude.ErrorReasonResumePostcheckFileChanged, "resume_postcheck_file_changed"},
		{"resume postcheck mismatch", agentclaude.ErrorReasonResumePostcheckMismatch, "resume_postcheck_mismatch"},
		{"resume postcheck query failed", agentclaude.ErrorReasonResumePostcheckQueryFailed, "resume_postcheck_query_failed"},
		{"resume preflight cwd mismatch", agentclaude.ErrorReasonResumePreflightCwdMismatch, "resume_preflight_cwd_mismatch"},
		{"resume preflight file invalid", agentclaude.ErrorReasonResumePreflightFileInvalid, "resume_preflight_file_invalid"},
		{"resume preflight identity mismatch", agentclaude.ErrorReasonResumePreflightIdentityMismatch, "resume_preflight_identity_mismatch"},
		{"resume preflight incomplete", agentclaude.ErrorReasonResumePreflightIncomplete, "resume_preflight_incomplete"},
		{"resume preflight missing", agentclaude.ErrorReasonResumePreflightMissing, "resume_preflight_missing"},
		{"resume preflight query failed", agentclaude.ErrorReasonResumePreflightQueryFailed, "resume_preflight_query_failed"},
		{"resume probe ID failed", agentclaude.ErrorReasonResumeProbeIDFailed, "resume_probe_id_failed"},
		{"resume version query failed", agentclaude.ErrorReasonResumeVersionQueryFailed, "resume_version_query_failed"},
		{"resume version unconfirmed", agentclaude.ErrorReasonResumeVersionUnconfirmed, "resume_version_unconfirmed"},
		{"resume version unsupported", agentclaude.ErrorReasonResumeVersionUnsupported, "resume_version_unsupported"},
		{"session cwd mismatch", agentclaude.ErrorReasonSessionCWDMismatch, "session_cwd_mismatch"},
		{"start or steer acceptance missing", agentclaude.ErrorReasonStartOrSteerAcceptanceMissing, "start_or_steer_acceptance_missing"},
		{"submission ID failed", agentclaude.ErrorReasonSubmissionIDFailed, "submission_id_failed"},
		{"tool call capacity", agentclaude.ErrorReasonToolCallCapacity, "tool_call_capacity"},
		{"tool result content unsupported", agentclaude.ErrorReasonToolResultContentUnsupported, "tool_result_content_unsupported"},
		{"tool result text invalid", agentclaude.ErrorReasonToolResultTextInvalid, "tool_result_text_invalid"},
		{"MCP server field unsupported", agentclaude.ErrorReasonMCPServerFieldUnsupported(2, "cwd"), "mcp_server_2_cwd_unsupported"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, test.got)
		})
	}
}
