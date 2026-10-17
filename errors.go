package codex

import (
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrClosed             = errors.New("codex: connection closed")
	ErrLimit              = errors.New("codex: resource limit reached")
	ErrProtocol           = errors.New("codex: protocol error")
	ErrFrameTooLarge      = errors.New("codex: frame too large")
	ErrWriteTimeout       = errors.New("codex: write timeout")
	ErrShutdownTimeout    = errors.New("codex: owned process did not reap before shutdown timeout")
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

// RPCError is a definitive server error response, not a no-side-effects guarantee.
type RPCError struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
	// Raw retains the entire error object, including future unknown members.
	Raw json.RawMessage `json:"-"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("codex RPC error %d: %s", e.Code, e.Message) }
