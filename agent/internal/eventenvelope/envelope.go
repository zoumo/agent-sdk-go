// Package eventenvelope builds validated adapter-neutral event envelopes.
package eventenvelope

import (
	"time"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/internal/uuidv4"
)

// Envelope owns the fixed adapter-neutral session identity and creates event
// envelopes without interpreting adapter-specific metadata.
type Envelope struct {
	SessionID string
}

// Event builds and validates one unified event.
func (e Envelope) Event(
	itemID string,
	payload agent.Payload,
	occurred time.Time,
	meta map[string]any,
) (agent.Event, error) {
	return e.event(uuidv4.New, itemID, payload, occurred, meta)
}

func (e Envelope) event(
	newID func() (string, error),
	itemID string,
	payload agent.Payload,
	occurred time.Time,
	meta map[string]any,
) (agent.Event, error) {
	eventID, err := newID()
	if err != nil {
		return agent.Event{}, envelopeError(
			agent.ErrorReasonEventIDFailed,
			"create unified event identity",
			err,
		)
	}
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}

	event := agent.Event{
		EventID:   eventID,
		SessionID: e.SessionID,
		Time:      occurred,
		ItemID:    itemID,
		Payload:   payload,
		Meta:      meta,
	}
	if err := event.Validate(); err != nil {
		return agent.Event{}, envelopeError(
			agent.ErrorReasonEventInvalid,
			"build unified event",
			err,
		)
	}
	return event, nil
}

func envelopeError(reason, message string, cause error) error {
	return &agent.Error{
		Code:    agent.ErrorUnavailable,
		Reason:  reason,
		Message: message,
		Cause:   cause,
	}
}
