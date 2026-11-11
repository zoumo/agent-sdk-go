package codex_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/codex"
)

func TestSessionInjectValidatesBeforeDispatch(t *testing.T) {
	session := &codex.Session{}
	for _, items := range [][]json.RawMessage{
		nil,
		{},
		{json.RawMessage(`{"type":`)},
		{json.RawMessage(`{"type":"message"}`), nil},
	} {
		require.ErrorIs(t, session.Inject(context.Background(), items), codex.ErrInvalidInput)
	}
}
