package claude

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionGetSettingsPreservesNativeResponse(t *testing.T) {
	session, transcript := openInputConfigSession(t)
	response, err := session.GetSettings(t.Context())
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"effective":{"effortLevel":"medium","fastMode":true},
		"sources":[{"source":"flagSettings","settings":{"fastMode":true}}],
		"applied":{"model":"main","effort":"medium","advisor":null,"ultracode":true}
	}`, string(response))
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = session.GetSettings(canceled)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, session.Close())
	frames := permissionTranscript(t, transcript)
	require.Len(t, frames, 1)
	assert.Contains(t, string(frames[0]), `"subtype":"get_settings"`)
	_, err = session.GetSettings(t.Context())
	require.ErrorIs(t, err, ErrClosed)
}

func TestSessionGetSettingsPreservesNativeRejection(t *testing.T) {
	session, _ := openInputConfigSessionWithEnv(t, "CLAUDE_GO_SETTINGS_MODE=reject_settings")
	_, err := session.GetSettings(t.Context())
	var native *ControlError
	require.ErrorAs(t, err, &native)
	assert.Equal(t, "settings unavailable", native.Message)
	require.NoError(t, session.Close())
}
