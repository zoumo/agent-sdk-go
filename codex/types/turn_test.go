package types_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestTurnOutputSchemaArbitraryJSON(t *testing.T) {
	// outputSchema is an annotation-only JSON schema node in the official pin;
	// it accepts arbitrary JSON rather than an invented typed schema object.
	for _, value := range []string{`null`, `false`, `7`, `"value"`, `[]`, `{"type":"object","future":9007199254740993}`} {
		t.Run(value, func(t *testing.T) {
			raw := `{"threadId":"thread","input":[],"outputSchema":` + value + `}`
			var p types.TurnStartParams
			require.NoError(t, json.Unmarshal([]byte(raw), &p))
			require.True(t, p.OutputSchema.IsSet(), "outputSchema omitted")
			if value == "null" {
				assert.True(t, p.OutputSchema.IsNull(), "null lost")
			} else {
				got, ok := p.OutputSchema.Get()
				require.True(t, ok, "opaque value missing")
				assert.Equal(t, value, string(got), "opaque value changed")
			}
			encoded, err := json.Marshal(p)
			require.NoError(t, err)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(encoded, &fields))
			assert.Equal(t, value, string(fields["outputSchema"]), "round trip: %s", encoded)
		})
	}
	t.Run("omitted", func(t *testing.T) {
		encoded, err := json.Marshal(types.TurnStartParams{ThreadID: "thread"})
		require.NoError(t, err)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(encoded, &fields))
		assert.NotContains(t, fields, "outputSchema", "omitted schema became present")
		assert.Equal(t, "[]", string(fields["input"]), "native input array lost")
	})
}

func TestTurnNotificationRequiredNativeFields(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		value     any
	}{
		{"turn-completed", `{"threadId":"x","turn":{"id":"x","items":[]}}`, new(types.TurnCompletedNotification)},
		{"item-started", `{"threadId":"x","turnId":"x","item":{"type":"future"}}`, new(types.ItemStartedNotification)},
		{"item-completed", `{"threadId":"x","turnId":"x","completedAtMs":null,"item":{"type":"future"}}`, new(types.ItemCompletedNotification)},
		{"delta", `{"threadId":"x","turnId":"x","itemId":"x","delta":null}`, new(types.AgentMessageDeltaNotification)},
		{"error", `{"threadId":"x","turnId":"x","error":{"message":"x"}}`, new(types.ErrorNotification)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			{
				err := json.Unmarshal([]byte(tc.raw), tc.value)
				require.Error(t, err,
					"malformed native payload accepted")
			}

		})
	}
}
