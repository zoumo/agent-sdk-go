package agentcodex

import "github.com/zoumo/agent-sdk-go/agent"

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
