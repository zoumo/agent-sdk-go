package lifecycle

import (
	"context"
	"errors"
)

// FailureClass is the machine-readable terminal reason for an action.
type FailureClass string

const (
	FailureClassCanceled     FailureClass = "canceled"
	FailureClassAborted      FailureClass = "aborted"
	FailureClassTransport    FailureClass = "transport"
	FailureClassStrictReplay FailureClass = "strict-replay"
	FailureClassTimeout      FailureClass = "timeout"
	FailureClassCleanup      FailureClass = "cleanup"
	FailureClassInternal     FailureClass = "internal"
)

// ClassifiedError binds one failure class to its diagnostic without erasing
// the underlying error for callers that need context or I/O matching.
type ClassifiedError struct {
	Class FailureClass
	Err   error
}

func (e *ClassifiedError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return string(e.Class)
	}
	return string(e.Class) + ": " + e.Err.Error()
}

func (e *ClassifiedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ClassifyFailure returns nil for nil and preserves an existing terminal class.
func ClassifyFailure(class FailureClass, err error) error {
	if err == nil {
		return nil
	}
	if FailureClassOf(err) != "" {
		return err
	}
	return &ClassifiedError{Class: class, Err: err}
}

// FailureClassOf returns the first terminal class carried by an error tree.
func FailureClassOf(err error) FailureClass {
	var classified *ClassifiedError
	if errors.As(err, &classified) {
		return classified.Class
	}
	return ""
}

// ContextFailure returns the first cancellation cause with its terminal class.
func ContextFailure(ctx context.Context) error {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	cause := context.Cause(ctx)
	if FailureClassOf(cause) != "" {
		return cause
	}
	err := ctx.Err()
	if !errors.Is(cause, err) {
		cause = errors.Join(err, cause)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ClassifyFailure(FailureClassTimeout, cause)
	}
	return ClassifyFailure(FailureClassCanceled, cause)
}
