package eventenvelope

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestEnvelopeBuildsValidEventsWithStableIdentityAndFallbackTime(t *testing.T) {
	running := agent.StateRunning
	payload := agent.StateUpdatePayload{State: &running}
	meta := map[string]any{
		"provider": map[string]any{"raw": map[string]any{"future": true}},
	}
	envelope := Envelope{SessionID: "session-a"}
	before := time.Now().UTC()

	first, err := envelope.Event("", payload, time.Time{}, meta)
	require.NoError(t, err)
	second, err := envelope.Event("", payload, time.Time{}, meta)
	require.NoError(t, err)

	assert.NotEqual(t, first.EventID, second.EventID)
	assert.Equal(t, "session-a", first.SessionID)
	assert.Equal(t, agent.EventSessionStateUpdate, first.Type())
	assert.Equal(t, payload, first.Payload)
	assert.Equal(t, meta, first.Meta)
	assert.False(t, first.Time.Before(before))
	assert.Equal(t, time.UTC, first.Time.Location())
	require.NoError(t, first.Validate())
}

func TestEnvelopePreservesNativeEventTime(t *testing.T) {
	running := agent.StateRunning
	occurred := time.Date(2026, 11, 10, 9, 30, 0, 123, time.FixedZone("native", 8*60*60))

	event, err := (Envelope{SessionID: "session-a"}).Event(
		"",
		agent.StateUpdatePayload{State: &running},
		occurred,
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, occurred, event.Time)
}

func TestEnvelopeMapsIdentityAndValidationFailures(t *testing.T) {
	cause := errors.New("entropy unavailable")

	_, err := (Envelope{SessionID: "session-a"}).event(
		func() (string, error) { return "", cause },
		"",
		agent.StateUpdatePayload{},
		time.Time{},
		nil,
	)
	require.ErrorIs(t, err, cause)
	assertEnvelopeError(t, err, "event_id_failed")

	_, err = (Envelope{}).Event(
		"",
		agent.StateUpdatePayload{},
		time.Time{},
		nil,
	)
	assertEnvelopeError(t, err, "event_invalid")

	_, err = (Envelope{SessionID: "session-a"}).Event(
		"",
		agent.StateUpdatePayload{},
		time.Time{},
		map[string]any{"invalid": make(chan int)},
	)
	assertEnvelopeError(t, err, "event_invalid")

	_, err = (Envelope{SessionID: "session-a"}).Event(
		"",
		agent.AgentMessage{},
		time.Time{},
		nil,
	)
	assertEnvelopeError(t, err, "event_invalid")
}

func assertEnvelopeError(t *testing.T, err error, reason string) {
	t.Helper()

	var classified *agent.Error
	require.ErrorAs(t, err, &classified)
	assert.Equal(t, agent.ErrorUnavailable, classified.Code)
	assert.Equal(t, reason, classified.Reason)
}
