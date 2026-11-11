package types_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestW3cTraceContextRoundTrip(t *testing.T) {
	for _, wire := range []string{
		`{}`,
		`{"traceparent":null,"tracestate":null}`,
		`{"traceparent":"","tracestate":""}`,
		`{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01","tracestate":"vendor=value"}`,
		`{"traceparent":"parent","future":{"n":9007199254740993}}`,
	} {
		t.Run(wire, func(t *testing.T) {
			var trace types.W3cTraceContext
			require.NoError(t, json.Unmarshal([]byte(wire), &trace))
			encoded, err := json.Marshal(trace)
			require.NoError(t, err)
			assert.JSONEq(t, wire, string(encoded))
		})
	}
	for _, wire := range []string{`null`, `{"traceparent":42}`, `{"tracestate":false}`, `{"traceparent":"a","traceparent":"b"}`} {
		var trace types.W3cTraceContext
		assert.Error(t, json.Unmarshal([]byte(wire), &trace), wire)
	}
}
