package codex

import (
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrSubscriptionOverflow = errors.New("codex: subscription overflow; events lost")
	ErrRemoteTransport      = errors.New("codex: remote transport failure")
	ErrClosed               = errors.New("codex: connection closed")
	ErrLimit                = errors.New("codex: resource limit reached")
	ErrProtocol             = errors.New("codex: protocol error")
	ErrFrameTooLarge        = errors.New("codex: frame too large")
	ErrWriteTimeout         = errors.New("codex: write timeout")
	ErrShutdownTimeout      = errors.New("codex: owned process did not reap before shutdown timeout")
	ErrInvalidAnswerHandle  = errors.New("codex: invalid answer handle")
	ErrRequestAnswered      = errors.New("codex: server request answer already admitted")
	ErrRequestCanceled      = errors.New("codex: server request abandoned locally")
	ErrRequestExpired       = errors.New("codex: server request handle expired locally")
	ErrRequestResolved      = errors.New("codex: server request resolved by server")
	ErrRequestOverflow      = fmt.Errorf("codex: server request capacity exhausted: %w", ErrLimit)
	// Deprecated: incoming requests now use Requests and never emit this error.
	ErrUnsupportedRequest = errors.New("codex: server request unsupported by this SDK slice")
)

// DispatchState reports delivery evidence, never whether server execution stopped.
type DispatchState string

const (
	NotSent        DispatchState = "not-sent"
	OutcomeUnknown DispatchState = "outcome-unknown"
	Responded      DispatchState = "responded"
)

// CallError preserves the underlying cause and conservative dispatch evidence.
// OutcomeUnknown requests must not be replayed without caller reconciliation.
type CallError struct {
	Method   string
	ID       ID
	Dispatch DispatchState
	Cause    error
}

func (e *CallError) Error() string {
	return fmt.Sprintf("codex: %s (%s): %v", e.Method, e.Dispatch, e.Cause)
}
func (e *CallError) Unwrap() error { return e.Cause }

// AnswerError preserves a one-shot server-response failure and its wire evidence.
// NotSent after queue admission still consumes the handle. OutcomeUnknown must
// never be replayed. A successful Answer returns nil (the frame was written),
// which is not evidence that the peer accepted or acted on the response.
type AnswerError struct {
	Method   string
	ID       ID
	Dispatch DispatchState
	Cause    error
}

func (e *AnswerError) Error() string {
	return fmt.Sprintf("codex: answer %s (%s): %v", e.Method, e.Dispatch, e.Cause)
}
func (e *AnswerError) Unwrap() error { return e.Cause }

// RPCError is a definitive server error response, not a no-side-effects guarantee.
type RPCError struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
	// Raw retains the entire error object, including future unknown members.
	// Remote errors redact reflected bearer credentials from Message, Data, and
	// Raw. Otherwise the original native data and bytes are preserved.
	Raw json.RawMessage `json:"-"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("codex RPC error %d: %s", e.Code, e.Message) }

// SubscriptionOverflowError terminates one incomplete stream. Previously queued
// events remain readable, but the event that exceeded Capacity was not accepted.
type SubscriptionOverflowError struct{ Capacity int }

func (e *SubscriptionOverflowError) Error() string {
	return fmt.Sprintf("%v (capacity %d)", ErrSubscriptionOverflow, e.Capacity)
}
func (e *SubscriptionOverflowError) Unwrap() error { return ErrSubscriptionOverflow }

// DecodeError reports a malformed supported request or notification payload.
// The original method and parameters remain available; the connection stays usable.
type DecodeError struct {
	Method string
	Cause  error
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("codex: decode %s payload: %v", e.Method, e.Cause)
}
func (e *DecodeError) Unwrap() error { return e.Cause }
