package agent_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

func TestTraceContextValidate(t *testing.T) {
	for _, test := range []struct {
		name, parent, state string
		valid               bool
	}{
		{"sampled", traceparent, "", true},
		{"flags", strings.TrimSuffix(traceparent, "01") + "fe", "", true},
		{"future", "01" + traceparent[2:], "", true},
		{"future extension", "02" + traceparent[2:] + "-extension", "", true},
		{"vendor", traceparent, "vendor=value,1tenant@system=a b", true},
		{"whitespace", traceparent, " \tvendor=value \t, other=two", true},
		{"empty list members", traceparent, ",vendor=value, ,", true},
		{"whitespace only", traceparent, " \t ", true},
		{"max value", traceparent, "a=" + strings.Repeat("x", 256), true},
		{"max simple key", traceparent, strings.Repeat("a", 256) + "=x", true},
		{"max tenant key", traceparent, strings.Repeat("a", 241) + "@" + strings.Repeat("b", 14) + "=x", true},
		{"empty", "", "", false},
		{"missing parent", "", "vendor=value", false},
		{"zero trace", "00-00000000000000000000000000000000-00f067aa0ba902b7-01", "", false},
		{"zero span", "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01", "", false},
		{"reserved version", "ff" + traceparent[2:], "", false},
		{"uppercase", strings.ToUpper(traceparent), "", false},
		{"short", traceparent[:54], "", false},
		{"v00 extension", traceparent + "-extra", "", false},
		{"future separator", "01" + traceparent[2:] + "x", "", false},
		{"separator", strings.Replace(traceparent, "-", ":", 1), "", false},
		{"CR", traceparent + "\r", "", false},
		{"LF", traceparent, "vendor=a\nb", false},
		{"NUL", traceparent, "vendor=a\x00", false},
		{"unicode", traceparent, "vendor=你好", false},
		{"duplicate", traceparent, "a=1,a=2", false},
		{"uppercase key", traceparent, "Vendor=1", false},
		{"digit simple key", traceparent, "1vendor=1", false},
		{"empty value", traceparent, "a=", false},
		{"equals in value", traceparent, "a=x=y", false},
		{"key whitespace", traceparent, "a =x", false},
		{"tab in value", traceparent, "a=x\ty", false},
		{"DEL", traceparent, "a=x\x7f", false},
		{"empty tenant", traceparent, "@system=x", false},
		{"empty system", traceparent, "tenant@=x", false},
		{"digit system", traceparent, "tenant@1sys=x", false},
		{"multiple at signs", traceparent, "a@b@c=x", false},
		{"long value", traceparent, "a=" + strings.Repeat("x", 257), false},
		{"long key", traceparent, strings.Repeat("a", 257) + "=x", false},
		{"long tenant", traceparent, strings.Repeat("a", 242) + "@b=x", false},
		{"long system", traceparent, "a@" + strings.Repeat("b", 15) + "=x", false},
		{"many members", traceparent, strings.Join([]string{"a=1", "b=1", "c=1", "d=1", "e=1", "f=1", "g=1", "h=1", "i=1", "j=1", "k=1", "l=1", "m=1", "n=1", "o=1", "p=1", "q=1", "r=1", "s=1", "t=1", "u=1", "v=1", "w=1", "x=1", "y=1", "z=1", "aa=1", "ab=1", "ac=1", "ad=1", "ae=1", "af=1", "ag=1"}, ","), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			trace := agent.TraceContext{Traceparent: test.parent, Tracestate: test.state}
			before := trace
			if test.valid {
				assert.NoError(t, trace.Validate())
			} else {
				assert.Error(t, trace.Validate())
			}
			assert.Equal(t, before, trace)
		})
	}
}

func TestTraceContextContextAndBusinessSurface(t *testing.T) {
	_, present := agent.TraceContextFromContext(context.Background())
	assert.False(t, present)
	first := agent.TraceContext{Traceparent: traceparent, Tracestate: "vendor=value"}
	ctx := agent.WithTraceContext(context.Background(), first)
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	got, present := agent.TraceContextFromContext(child)
	require.True(t, present)
	assert.Equal(t, first, got)
	invalid := agent.TraceContext{Traceparent: "invalid"}
	got, present = agent.TraceContextFromContext(agent.WithTraceContext(child, invalid))
	require.True(t, present)
	assert.Equal(t, invalid, got, "reading does not imply validity")
	for _, value := range []any{
		agent.Input{Content: textContent("hello")},
		validEvent("message-1", agent.UserMessage{MessageID: "message-1", Content: textContent("hello")}),
	} {
		structure := reflect.TypeOf(value)
		for field := range structure.Fields() {
			assert.NotContains(t, strings.ToLower(field.Name), "trace")
		}
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), traceparent)
		assert.NotContains(t, string(encoded), `"trace"`)
	}
}
