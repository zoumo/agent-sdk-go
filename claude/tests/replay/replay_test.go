// SPDX-License-Identifier: Apache-2.0
package transcript

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReplayAdapterCompletion(t *testing.T) {
	cap := `{"ordinal":1,"direction":"client","channel":"control","data":{"type":"control_request","request_id":"old","request":{"forwardSubagentText":true}}}
{"ordinal":2,"direction":"server","channel":"protocol","data":{"type":"notice"}}
{"ordinal":3,"direction":"server","channel":"control","data":{"type":"control_response","response":{"request_id":"old"}}}
`
	input := `{"type":"control_request","request_id":"new","request":{"forwardSubagentText":true}}` + "\n"
	var out bytes.Buffer
	done := false
	{
		err := Replay(strings.NewReader(input), &out, strings.NewReader(cap), func() error { done = true; return nil })
		require.NoError(t, err)
	}
	require.True(t, done, out.String())
	require.Contains(t, out.String(), `"request_id":"new"`, out.String())
	require.False(t, strings.Index(out.String(), "notice") > strings.Index(out.String(), "response"), out.String())
	for _, tc := range []struct{ name, input string }{
		{"eof", ""}, {"mismatch", strings.Replace(input, "true", "false", 1)}, {"extra", input + input},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, Replay(strings.NewReader(tc.input), &bytes.Buffer{}, strings.NewReader(cap), nil), "accepted mismatch/EOF/extra")
		})
	}
}

// These limits belong to the JSONL-to-process adapter, before the protocol
// matcher runs. Protocol ID and matching matrices live with the tool.
func TestReplayAdapterBounds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		capture string
		want    string
	}{
		{"capture bytes", strings.Repeat("x", (16<<20)+1), "capture byte limit exceeded"},
		{"line bytes", strings.Repeat("x", (2<<20)+1), "token too long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			completed := false
			err := Replay(strings.NewReader(""), &output, strings.NewReader(tc.capture),
				func() error { completed = true; return nil })
			require.ErrorContains(t, err, tc.want)
			require.Empty(t, output.String())
			require.False(t, completed)
		})
	}
}
