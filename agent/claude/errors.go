package agentclaude

import (
	"errors"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/claude"
)

func adapterError(
	code agent.ErrorCode,
	reason string,
	message string,
	cause error,
) error {
	return &agent.Error{
		Code:    code,
		Reason:  reason,
		Message: message,
		Cause:   cause,
	}
}

func nativeOperationError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if agent.CodeOf(err) != "" {
		return err
	}

	var controlErr *claude.ControlError
	switch {
	case errors.Is(err, claude.ErrInvalidInput):
		return adapterError(
			agent.ErrorInvalidArgument,
			operation+"_invalid_argument",
			operation+" has invalid input",
			err,
		)
	case errors.Is(err, claude.ErrUnsupported):
		return adapterError(
			agent.ErrorUnsupported,
			operation+"_unsupported",
			operation+" is unsupported",
			err,
		)
	case errors.Is(err, claude.ErrResourceLimit):
		return adapterError(
			agent.ErrorResourceExhausted,
			operation+"_resource_exhausted",
			operation+" exceeded a resource limit",
			err,
		)
	case errors.Is(err, claude.ErrClosed):
		return adapterError(
			agent.ErrorClosed,
			operation+"_closed",
			operation+" used a closed Claude session",
			err,
		)
	case errors.Is(err, claude.ErrUnknownOutcome):
		return adapterError(
			agent.ErrorOutcomeUnknown,
			operation+"_outcome_unknown",
			operation+" result is unknown; do not retry",
			err,
		)
	case errors.As(err, &controlErr):
		return adapterError(
			agent.ErrorNativeRejected,
			operation+"_native_rejected",
			operation+" was rejected by Claude",
			err,
		)
	default:
		return adapterError(
			agent.ErrorUnavailable,
			operation+"_failed",
			operation+" failed",
			err,
		)
	}
}

func sessionTerminationError(cause error) error {
	if cause == nil || agent.CodeOf(cause) != "" {
		return cause
	}
	return adapterError(
		agent.ErrorUnavailable,
		"session_terminated",
		"Claude session terminated",
		cause,
	)
}
