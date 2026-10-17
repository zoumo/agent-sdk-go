// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"errors"
	"fmt"
)

var (
	ErrClosed          = errors.New("claude: client is closed")
	ErrProtocol        = errors.New("claude: invalid protocol frame")
	ErrResourceLimit   = errors.New("claude: resource limit exceeded")
	ErrUnexpectedEOF   = errors.New("claude: unexpected end of stdout")
	ErrShutdownTimeout = errors.New("claude: shutdown timed out")
	ErrUnknownOutcome  = errors.New("claude: operation outcome is unknown")
)

// Error describes an operation or terminal failure. Stderr is a bounded tail,
// never logged by the SDK, and may contain sensitive CLI diagnostics.
// OutcomeUnknown forbids assuming that cancellation or a write error prevented
// the CLI from acting. The SDK never automatically resends a frame.
type Error struct {
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
type ControlError struct{ RequestID, Subtype, Message string }

func (e *ControlError) Error() string {
	return fmt.Sprintf("claude: control %s (%s): %s", e.Subtype, e.RequestID, e.Message)
}
