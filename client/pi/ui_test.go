package pi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/pi"
)

func TestExtensionUIResponseUsesOriginalIDWithoutAnAcknowledgement(t *testing.T) {
	const frame = `{"type":"extension_ui_request","id":"dialog-1","method":"confirm","title":"Confirm","message":"Do it?","timeout":1000,"future":true}`
	writes := make(chan json.RawMessage, 1)
	s, ctx := runPeer(t, func(p peer) error {
		if _, err := fmt.Fprintln(p.output, frame); err != nil {
			return err
		}
		var response json.RawMessage
		if err := p.input.Decode(&response); err != nil {
			return err
		}
		writes <- response
		next, err := p.receive()
		if err != nil {
			return err
		}
		if next.Type != "get_state" {
			return fmt.Errorf("unexpected command after response: %q", next.Type)
		}
		if err := p.reply(next, map[string]string{"sessionId": "native-1"}); err != nil {
			return err
		}
		_, err = p.receive()
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("expected EOF, got %w", err)
	})
	initial, err := s.Receive(ctx)
	require.NoError(t, err)
	_, err = initial.UIRequest()
	require.Error(t, err)
	dialog, err := s.Receive(ctx)
	require.NoError(t, err)
	req, err := dialog.UIRequest()
	require.NoError(t, err)
	assert.Equal(t, "confirm", req.Method)
	assert.Equal(t, int64(1000), req.TimeoutMS)
	assert.True(t, bytes.Equal([]byte(frame), req.Raw))
	require.Error(t, s.RespondUI(ctx, pi.UIResponse{ID: req.ID}))
	require.NoError(t, s.RespondUI(ctx, pi.UIResponse{ID: req.ID, Confirmed: new(false)}))
	assert.JSONEq(t, `{"type":"extension_ui_response","id":"dialog-1","confirmed":false}`, string(<-writes))
	state, err := s.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, "native-1", state.SessionID)
}
