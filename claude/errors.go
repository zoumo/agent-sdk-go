// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrClosed                   = errors.New("claude: client is closed")
	ErrInvalidInput             = errors.New("claude: invalid input")
	ErrUnsupported              = errors.New("claude: unsupported operation")
	ErrProtocol                 = errors.New("claude: invalid protocol frame")
	ErrResourceLimit            = errors.New("claude: resource limit exceeded")
	ErrUnexpectedEOF            = errors.New("claude: unexpected end of stdout")
	ErrShutdownTimeout          = errors.New("claude: shutdown timed out")
	ErrUnknownOutcome           = errors.New("claude: operation outcome is unknown")
	ErrInvalidPermissionRequest = errors.New("claude: invalid permission handle")
	ErrInvalidPermissionAnswer  = errors.New("claude: invalid permission answer")
	ErrPermissionUsed           = errors.New("claude: permission answer already attempted")
	ErrPermissionWriteFailed    = errors.New("claude: permission response attempt failed")
	ErrPermissionCancelled      = errors.New("claude: permission request cancelled")
	ErrPermissionStale          = errors.New("claude: permission handle belongs to another connection epoch")
)

// DispatchState distinguishes a local rejection from a possibly written answer.
type DispatchState string

const (
	NotSent        DispatchState = "not-sent"
	OutcomeUnknown DispatchState = "outcome-unknown"
)

type DispatchError struct {
	Op    string
	State DispatchState
	Err   error
}

func (e *DispatchError) Error() string {
	return fmt.Sprintf("claude: %s (%s): %v", e.Op, e.State, e.Err)
}
func (e *DispatchError) Unwrap() error { return e.Err }
func (e *DispatchError) Is(target error) bool {
	return target == ErrUnknownOutcome && e.State == OutcomeUnknown
}

// Error describes an operation or terminal failure. Stderr is a bounded tail,
// never logged by the SDK, and may contain sensitive CLI diagnostics.
// OutcomeUnknown forbids assuming that cancellation or a write error prevented
// the CLI from acting. The SDK never automatically resends a frame.
type Error struct {
	// Raw retains the offending native frame for protocol/decode diagnostics.
	Raw            json.RawMessage
	Op             string
	Err            error
	Stderr         string
	ExitCode       *int
	OutcomeUnknown bool
}

func (e *Error) Error() string {
	s := "claude: " + e.Op + ": " + e.Err.Error()
	if e.OutcomeUnknown {
		s += " (outcome unknown)"
	}
	return s
}
func (e *Error) Unwrap() error        { return e.Err }
func (e *Error) Is(target error) bool { return target == ErrUnknownOutcome && e.OutcomeUnknown }

// ResourceLimitError identifies a bound that was exceeded. Incoming overflows
// terminate the client; a locally oversized Send is rejected before writing.
type ResourceLimitError struct {
	Resource string
	Limit    int
}

func (e *ResourceLimitError) Error() string {
	return fmt.Sprintf("claude: %s limit exceeded (%d)", e.Resource, e.Limit)
}
func (e *ResourceLimitError) Unwrap() error { return ErrResourceLimit }

// ControlError is an explicit error response to an outbound control request.
type ControlError struct {
	RequestID, Subtype, Message string
	Raw                         json.RawMessage
}

func (e *ControlError) Error() string {
	return fmt.Sprintf("claude: control %s (%s): %s", e.Subtype, e.RequestID, e.Message)
}

// PermissionError identifies a rejected or failed response attempt by its
// original incoming RequestID. Err distinguishes ErrPermissionUsed,
// ErrPermissionCancelled, ErrClosed, ErrPermissionStale and
// ErrPermissionWriteFailed through errors.Is. A rejection of a terminal handle
// does not itself close the connection. Failed reserved attempts and handler
// panics fail closed; already completed writes cannot be undone.
type PermissionError struct {
	RequestID string
	Err       error
}

func (e *PermissionError) Error() string {
	return fmt.Sprintf("claude: permission %s: %v", e.RequestID, e.Err)
}
func (e *PermissionError) Unwrap() error { return e.Err }
