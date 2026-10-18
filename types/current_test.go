package types_test

import (
	"encoding/json"
	"testing"

	"github.com/zoumo/codex-sdk-go/types"
)

func TestCurrentOpenErrorInfo(t *testing.T) {
	// The current official anyOf explicitly allows arbitrary string/object errors,
	// including objects that do not fit a known typed external-tag branch.
	for _, raw := range []string{`{}`, `{"future":{"big":9007199254740993,"nullable":null}}`, `{"httpConnectionFailed":{},"responseStreamDisconnected":{}}`, `{"httpConnectionFailed":"opaque"}`, `"futureError"`} {
		t.Run(raw, func(t *testing.T) {
			var value types.CodexErrorInfo
			schemaRoundTrip(t, &value, raw)
			if raw[0] == '{' && len(value.Unknown) == 0 {
				t.Fatal("opaque native error was interpreted as a known branch")
			}
		})
	}
	for _, raw := range []string{`null`, `false`, `[]`, `17`} {
		var value types.CodexErrorInfo
		if err := json.Unmarshal([]byte(raw), &value); err == nil {
			t.Fatalf("wrong native error kind accepted: %s", raw)
		}
	}
}
