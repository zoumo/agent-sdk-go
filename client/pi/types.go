// Package pi controls a Pi coding-agent v1.6.0 subprocess through its JSONL RPC protocol.
package pi

import (
	"encoding/json"
	"fmt"
)

// Event preserves one complete non-response RPC record, including unknown fields.
type Event struct {
	Type string
	Raw  json.RawMessage
}

// State is a snapshot returned by get_state. Raw preserves the entire native data value.
type State struct {
	SessionID           string          `json:"sessionId"`
	SessionFile         string          `json:"sessionFile"`
	ThinkingLevel       string          `json:"thinkingLevel"`
	IsStreaming         bool            `json:"isStreaming"`
	SteeringMode        QueueMode       `json:"steeringMode"`
	FollowUpMode        QueueMode       `json:"followUpMode"`
	PendingMessageCount int             `json:"pendingMessageCount"`
	Model               json.RawMessage `json:"model"`
	Raw                 json.RawMessage `json:"-"`
}

// Image holds the native base64 data and MIME type. Type must be "image".
type Image struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MIMEType string `json:"mimeType"`
}

// PromptResult describes native acceptance. It does not prove execution or consumption.
type PromptResult struct {
	Disposition string `json:"disposition"`
}

// CommandError is an explicit failure response from Pi.
type CommandError struct {
	Command string
	Message string
	Raw     json.RawMessage
}

func (e *CommandError) Error() string { return fmt.Sprintf("pi %s: %s", e.Command, e.Message) }

// DispatchError means the command reached write admission and its outcome is unknown.
// Sent is true only when the entire frame was written or a response was observed.
// A false Sent on this error does not exclude a partial write. Do not automatically retry.
type DispatchError struct {
	Sent bool
	Err  error
}

func (e *DispatchError) Error() string { return fmt.Sprintf("pi RPC outcome unknown: %v", e.Err) }
func (e *DispatchError) Unwrap() error { return e.Err }
