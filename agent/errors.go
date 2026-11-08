package agent

import "errors"

// ErrorCode is a stable classification that callers may use without parsing
// native diagnostic text.
type ErrorCode string

const (
	ErrorInvalidArgument    ErrorCode = "invalid_argument"
	ErrorFailedPrecondition ErrorCode = "failed_precondition"
	ErrorUnsupported        ErrorCode = "unsupported"
	ErrorUnavailable        ErrorCode = "unavailable"
	ErrorResourceExhausted  ErrorCode = "resource_exhausted"
	ErrorOutcomeUnknown     ErrorCode = "outcome_unknown"
	ErrorNativeRejected     ErrorCode = "native_rejected"
	ErrorClosed             ErrorCode = "closed"
)

// Error carries a stable classification while retaining the native cause.
// Message is suitable for callers and must not contain credentials or
// recovery material.
type Error struct {
	Code    ErrorCode
	Reason  string
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	switch {
	case e.Code != "" && e.Message != "":
		return string(e.Code) + ": " + e.Message
	case e.Code != "" && e.Reason != "":
		return string(e.Code) + ": " + e.Reason
	case e.Code != "":
		return string(e.Code)
	case e.Message != "":
		return e.Message
	case e.Reason != "":
		return e.Reason
	default:
		return "agent error"
	}
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// CodeOf returns the nearest classified error code in err's unwrap chain.
// An empty result means the error has not been classified.
func CodeOf(err error) ErrorCode {
	var target *Error
	if !errors.As(err, &target) || target == nil {
		return ""
	}
	return target.Code
}

func invalidArgument(reason, message string) error {
	return &Error{
		Code:    ErrorInvalidArgument,
		Reason:  reason,
		Message: message,
	}
}
