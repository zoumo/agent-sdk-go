package types_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestCurrentOpenErrorInfo(t *testing.T) {
	// The current official anyOf explicitly allows arbitrary string/object errors,
	// including objects that do not fit a known typed external-tag branch.
	for _, raw := range []string{`{}`, `{"future":{"big":9007199254740993,"nullable":null}}`, `{"httpConnectionFailed":{},"responseStreamDisconnected":{}}`, `{"httpConnectionFailed":"opaque"}`, `"futureError"`} {
		t.Run(raw, func(t *testing.T) {
			var value types.CodexErrorInfo
			schemaRoundTrip(t, &value, raw)
			require.False(t, raw[0] == '{' && len(value.Unknown) == 0,
				"opaque native error was interpreted as a known branch")

		})
	}
	for _, raw := range []string{`null`, `false`, `[]`, `17`} {
		var value types.CodexErrorInfo
		{
			err := json.Unmarshal([]byte(raw), &value)
			require.Error(t, err,
				"wrong native error kind accepted: %s", raw)
		}

	}
}
