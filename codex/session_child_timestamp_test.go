package codex

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestChildDetailEmissionTimestampIsMetadataOnly(t *testing.T) {
	params := json.RawMessage(`{"threadId":"child","turnId":"turn","itemId":"item","delta":"text"}`)
	for _, tc := range []struct {
		name, extra string
		suppress    bool
	}{
		{"old envelope", "", true},
		{"native 0.163 timestamp", `,"emittedAtMs":1793290769913`, true},
		{"zero timestamp", `,"emittedAtMs":0`, true},
		{"exact large integer", `,"emittedAtMs":9007199254740993`, true},
		{"null", `,"emittedAtMs":null`, false},
		{"string", `,"emittedAtMs":"1793290769913"`, false},
		{"float", `,"emittedAtMs":1.5`, false},
		{"negative", `,"emittedAtMs":-1`, false},
		{"overflow", `,"emittedAtMs":9223372036854775808`, false},
		{"boolean", `,"emittedAtMs":true`, false},
		{"unknown lifecycle", `,"emittedAtMs":1793290769913,"lifecycle":"completed"`, false},
		{"wrong case", `,"emittedatms":1793290769913`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(`{"method":"item/agentMessage/delta","params":` + string(params) + tc.extra + `}`)
			s := &Session{root: "root", members: map[string]struct{}{"root": {}, "child": {}}, childDetailPolicy: ChildDetailSuppress}
			value := Received{Kind: ReceivedEvent, Raw: raw, Params: params, Source: types.EventIdentity{ThreadID: "child"}, Event: &types.Event{Method: "item/agentMessage/delta"}}
			require.Equal(t, tc.suppress, s.suppressChildDetailLocked(value))
			value.Source.ThreadID = "root"
			require.False(t, s.suppressChildDetailLocked(value), "root progress must remain visible")
			value.Source.ThreadID = "unknown"
			require.False(t, s.suppressChildDetailLocked(value), "unconfirmed child must not be suppressed")
		})
	}
}
