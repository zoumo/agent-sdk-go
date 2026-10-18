// SPDX-License-Identifier: Apache-2.0
package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	fixturedata "github.com/zoumo/agent-sdk-go/claude/tests/testdata"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReplayDependencies(t *testing.T) {
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
func TestSafetyGuards(t *testing.T) {
	for _, s := range []string{"sk-ant-abcdefghijklmnop", "Authorization: Bearer canary-secret", "access_token=x", "refresh_token=x", "api_key=canary", "/Users/canary/private", "/home/canary/private", "canary@example.com"} {
		t.Run(fmt.Sprintf("%v", s), func(t *testing.T) {
			require.Error(t, Scan([]byte(s)), "canary accepted")
		})
	}
	require.NoError(t, Scan([]byte(`{"type":"result","result":"total=18"}`)), "safe frame")
	for _, tc := range []struct{ name, mode, optIn, ci, cli string }{
		{"ci", "record", "1", "true", "/approved/claude"},
		{"missing mode", "", "1", "", "/approved/claude"},
		{"not opted in", "e2e", "0", "", "/approved/claude"},
		{"relative cli", "record", "1", "", "claude"},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Error(t, LiveGuard(tc.mode, tc.optIn, tc.ci, tc.cli), "guard accepted") })
	}
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(real, "numbers.txt"), []byte("7\n11\n"), 0600))
	require.True(t, SafeFile(real, "numbers.txt", "numbers.txt"), "boundary")
	require.False(t, SafeFile(real, "../numbers.txt", "numbers.txt"), "boundary")
	require.NoError(t, os.Symlink(filepath.Join(real, "numbers.txt"), filepath.Join(real, "summary.txt")))
	require.False(t, SafeFile(real, "summary.txt", "summary.txt"), "symlink accepted")
}

func TestSyntheticInflightReverseAndCancel(t *testing.T) {
	capture, err := fixturedata.Files.ReadFile("synthetic-faults/controls.jsonl")
	require.NoError(t, err)
	var input bytes.Buffer
	for _, line := range bytes.Split(bytes.TrimSpace(capture), []byte{'\n'}) {
		var f Frame
		require.NoError(t, json.Unmarshal(line, &f), "invalid synthetic fixture")
		if f.Direction == "client" {
			b := bytes.ReplaceAll(f.Data, []byte(`"request_id":"a"`), []byte(`"request_id":"dynamic-a"`))
			b = bytes.ReplaceAll(b, []byte(`"request_id":2`), []byte(`"request_id":9007199254740993`))
			input.Write(b)
			input.WriteByte('\n')
		}
	}
	var output bytes.Buffer
	{
		err := Replay(bytes.NewReader(input.Bytes()), &output, bytes.NewReader(capture), nil)
		require.NoError(t, err)
	}
	require.True(t, bytes.Contains(output.Bytes(), []byte(`"request_id":9007199254740993`)), "numeric/string mapping lost")
	require.True(t, bytes.Contains(output.Bytes(), []byte(`"request_id":"dynamic-a"`)), "numeric/string mapping lost")
	for _, tc := range []struct {
		name  string
		input []byte
	}{
		{"wrong reverse id", bytes.Replace(input.Bytes(), []byte(`"request_id":"permission"`), []byte(`"request_id":"wrong"`), 1)},
		{"numeric id changed to string", bytes.Replace(input.Bytes(), []byte(`"request_id":9007199254740993`), []byte(`"request_id":"9007199254740993"`), 1)},
		{"duplicate response", append(append([]byte{}, input.Bytes()...), []byte(`{"type":"control_response","response":{"request_id":"permission"}}`)...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, Replay(bytes.NewReader(tc.input), &bytes.Buffer{}, bytes.NewReader(capture), nil), "bad reverse ID/type/duplicate accepted")
		})
	}
	missing := bytes.Replace(capture, []byte(`"request_id":"permission"`), []byte(`"missing":"permission"`), 1)
	require.Error(t, Replay(bytes.NewReader(input.Bytes()), &bytes.Buffer{}, bytes.NewReader(missing), nil), "missing ID accepted")
	// Bounds are checked before any unbounded allocation or live fallback.
	require.Error(t, Replay(strings.NewReader(""), &bytes.Buffer{}, strings.NewReader(strings.Repeat("x", (2<<20)+1)), nil), "oversize capture accepted")
	require.Error(t, Replay(strings.NewReader(strings.Repeat("x", (2<<20)+1)), &bytes.Buffer{}, bytes.NewReader(capture), nil), "oversize client accepted")
}
