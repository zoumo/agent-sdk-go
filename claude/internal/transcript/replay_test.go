// SPDX-License-Identifier: Apache-2.0
package transcript

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplayDependencies(t *testing.T) {
	cap := `{"ordinal":1,"direction":"client","channel":"control","data":{"type":"control_request","request_id":"old","request":{"forwardSubagentText":true}}}
{"ordinal":2,"direction":"server","channel":"protocol","data":{"type":"notice"}}
{"ordinal":3,"direction":"server","channel":"control","data":{"type":"control_response","response":{"request_id":"old"}}}
`
	input := `{"type":"control_request","request_id":"new","request":{"forwardSubagentText":true}}` + "\n"
	var out bytes.Buffer
	done := false
	if err := Replay(strings.NewReader(input), &out, strings.NewReader(cap), func() error { done = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !done || !strings.Contains(out.String(), `"request_id":"new"`) || strings.Index(out.String(), "notice") > strings.Index(out.String(), "response") {
		t.Fatal(out.String())
	}
	for _, bad := range []string{"", strings.Replace(input, "true", "false", 1), input + input} {
		if Replay(strings.NewReader(bad), &bytes.Buffer{}, strings.NewReader(cap), nil) == nil {
			t.Fatal("accepted mismatch/EOF/extra")
		}
	}
}
func TestSafetyGuards(t *testing.T) {
	for _, s := range []string{"sk-ant-abcdefghijklmnop", "Authorization: Bearer canary-secret", "access_token=x", "refresh_token=x", "api_key=canary", "/Users/canary/private", "/home/canary/private", "canary@example.com"} {
		if Scan([]byte(s)) == nil {
			t.Fatal("canary accepted")
		}
	}
	if Scan([]byte(`{"type":"result","result":"total=18"}`)) != nil {
		t.Fatal("safe frame")
	}
	if LiveGuard("record", "1", "true", "/approved/claude") == nil || LiveGuard("", "1", "", "/approved/claude") == nil || LiveGuard("e2e", "0", "", "/approved/claude") == nil || LiveGuard("record", "1", "", "claude") == nil {
		t.Fatal("guard accepted")
	}
	dir := t.TempDir()
	real, _ := filepath.EvalSymlinks(dir)
	_ = os.WriteFile(filepath.Join(real, "numbers.txt"), []byte("7\n11\n"), 0600)
	if !SafeFile(real, "numbers.txt", "numbers.txt") || SafeFile(real, "../numbers.txt", "numbers.txt") {
		t.Fatal("boundary")
	}
	_ = os.Symlink(filepath.Join(real, "numbers.txt"), filepath.Join(real, "summary.txt"))
	if SafeFile(real, "summary.txt", "summary.txt") {
		t.Fatal("symlink accepted")
	}
}

func TestSyntheticInflightReverseAndCancel(t *testing.T) {
	capture, err := os.ReadFile("testdata/synthetic-faults/controls.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var input bytes.Buffer
	for _, line := range bytes.Split(bytes.TrimSpace(capture), []byte{'\n'}) {
		var f Frame
		if json.Unmarshal(line, &f) != nil {
			t.Fatal("invalid synthetic fixture")
		}
		if f.Direction == "client" {
			b := bytes.ReplaceAll(f.Data, []byte(`"request_id":"a"`), []byte(`"request_id":"dynamic-a"`))
			b = bytes.ReplaceAll(b, []byte(`"request_id":2`), []byte(`"request_id":9007199254740993`))
			input.Write(b)
			input.WriteByte('\n')
		}
	}
	var output bytes.Buffer
	if err := Replay(bytes.NewReader(input.Bytes()), &output, bytes.NewReader(capture), nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"request_id":9007199254740993`)) || !bytes.Contains(output.Bytes(), []byte(`"request_id":"dynamic-a"`)) {
		t.Fatal("numeric/string mapping lost")
	}
	for _, bad := range [][]byte{
		bytes.Replace(input.Bytes(), []byte(`"request_id":"permission"`), []byte(`"request_id":"wrong"`), 1),
		bytes.Replace(input.Bytes(), []byte(`"request_id":9007199254740993`), []byte(`"request_id":"9007199254740993"`), 1),
		append(append([]byte{}, input.Bytes()...), []byte(`{"type":"control_response","response":{"request_id":"permission"}}`)...),
	} {
		if Replay(bytes.NewReader(bad), &bytes.Buffer{}, bytes.NewReader(capture), nil) == nil {
			t.Fatal("bad reverse ID/type/duplicate accepted")
		}
	}
	missing := bytes.Replace(capture, []byte(`"request_id":"permission"`), []byte(`"missing":"permission"`), 1)
	if Replay(bytes.NewReader(input.Bytes()), &bytes.Buffer{}, bytes.NewReader(missing), nil) == nil {
		t.Fatal("missing ID accepted")
	}
	// Bounds are checked before any unbounded allocation or live fallback.
	if Replay(strings.NewReader(""), &bytes.Buffer{}, strings.NewReader(strings.Repeat("x", (2<<20)+1)), nil) == nil {
		t.Fatal("oversize capture accepted")
	}
	if Replay(strings.NewReader(strings.Repeat("x", (2<<20)+1)), &bytes.Buffer{}, bytes.NewReader(capture), nil) == nil {
		t.Fatal("oversize client accepted")
	}
}
