package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/zoumo/codex-sdk-go/protocol"
)

func TestTurnOutputSchemaArbitraryJSON(t *testing.T) {
	// outputSchema is an annotation-only JSON schema node in the official pin;
	// it accepts arbitrary JSON rather than an invented typed schema object.
	for _, value := range []string{`null`, `false`, `7`, `"value"`, `[]`, `{"type":"object","future":9007199254740993}`} {
		raw := `{"threadId":"thread","input":[],"outputSchema":` + value + `}`
		var p protocol.TurnStartParams
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatal(err)
		}
		if !p.OutputSchema.IsSet() {
			t.Fatal("outputSchema omitted")
		}
		if value == "null" {
			if !p.OutputSchema.IsNull() {
				t.Fatal("null lost")
			}
		} else if got, ok := p.OutputSchema.Get(); !ok || string(got) != value {
			t.Fatalf("opaque value changed: %s", got)
		}
		encoded, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(encoded, &fields)
		if string(fields["outputSchema"]) != value {
			t.Fatalf("round trip: %s", encoded)
		}
	}
	encoded, err := json.Marshal(protocol.TurnStartParams{ThreadID: "thread"})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &fields)
	if _, exists := fields["outputSchema"]; exists {
		t.Fatal("omitted schema became present")
	}
	if string(fields["input"]) != "[]" {
		t.Fatal("native input array lost")
	}
}

func TestTurnNotificationRequiredNativeFields(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		value     any
	}{
		{"turn-completed", `{"threadId":"x","turn":{"id":"x","items":[]}}`, new(protocol.TurnCompletedNotification)},
		{"item-started", `{"threadId":"x","turnId":"x","item":{"type":"future"}}`, new(protocol.ItemStartedNotification)},
		{"item-completed", `{"threadId":"x","turnId":"x","completedAtMs":null,"item":{"type":"future"}}`, new(protocol.ItemCompletedNotification)},
		{"delta", `{"threadId":"x","turnId":"x","itemId":"x","delta":null}`, new(protocol.AgentMessageDeltaNotification)},
		{"error", `{"threadId":"x","turnId":"x","error":{"message":"x"}}`, new(protocol.ErrorNotification)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(tc.raw), tc.value); err == nil {
				t.Fatal("malformed native payload accepted")
			}
		})
	}
}
