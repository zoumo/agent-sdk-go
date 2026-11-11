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
			agent.ErrorReasonForCode(operation, agent.ErrorInvalidArgument),
			operation+" has invalid input",
			err,
		)
	case errors.Is(err, claude.ErrUnsupported):
		return adapterError(
			agent.ErrorUnsupported,
			agent.ErrorReasonForCode(operation, agent.ErrorUnsupported),
			operation+" is unsupported",
			err,
		)
	case errors.Is(err, claude.ErrResourceLimit):
		return adapterError(
			agent.ErrorResourceExhausted,
			agent.ErrorReasonForCode(operation, agent.ErrorResourceExhausted),
			operation+" exceeded a resource limit",
			err,
		)
	case errors.Is(err, claude.ErrClosed):
		return adapterError(
			agent.ErrorClosed,
			agent.ErrorReasonForCode(operation, agent.ErrorClosed),
			operation+" used a closed Claude session",
			err,
		)
	case errors.Is(err, claude.ErrUnknownOutcome):
		return adapterError(
			agent.ErrorOutcomeUnknown,
			agent.ErrorReasonForCode(operation, agent.ErrorOutcomeUnknown),
			operation+" result is unknown; do not retry",
			err,
		)
	case errors.As(err, &controlErr):
		return adapterError(
			agent.ErrorNativeRejected,
			agent.ErrorReasonForCode(operation, agent.ErrorNativeRejected),
			operation+" was rejected by Claude",
			err,
		)
	default:
		return adapterError(
			agent.ErrorUnavailable,
			agent.ErrorReasonForOperation(operation, agent.ErrorReasonSuffixFailed),
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
		agent.ErrorReasonSessionTerminated,
		"Claude session terminated",
		cause,
	)
}
