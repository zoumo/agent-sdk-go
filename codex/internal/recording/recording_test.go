package recording

import (
	"bytes"
	"context"
	"encoding/json"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func frame(n int, d, w string) Frame {
	return Frame{Ordinal: n, Direction: d, Epoch: 1, Channel: "protocol", Wire: json.RawMessage(w)}
}
func TestIDCorrelationAndEarlyNotification(t *testing.T) {
	f := []Frame{frame(1, "c2s", `{"id":101,"method":"one"}`), frame(2, "c2s", `{"id":102,"method":"two"}`), frame(3, "s2c", `{"method":"early","params":{"itemId":"item-001"}}`), frame(4, "s2c", `{"id":102,"result":{}}`), frame(5, "s2c", `{"id":101,"result":{}}`), frame(6, "s2c", `{"id":"reverse-1","method":"item/tool/call","params":{"callId":"item-001"}}`), frame(7, "c2s", `{"id":"reverse-1","result":{"success":true}}`)}
	in := `{"id":7,"method":"one"}` + "\n" + `{"id":"8","method":"two"}` + "\n" + `{"id":"reverse-1","result":{"success":true}}` + "\n" + `{"id":9,"method":"replay/assert"}` + "\n"
	var out bytes.Buffer
	if err := Peer(strings.NewReader(in), &out, f); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if !strings.Contains(lines[1], `"id":"8"`) || !strings.Contains(lines[2], `"id":7`) {
		t.Fatal("numeric/string dynamic IDs not preserved")
	}
}
func TestReplayFaults(t *testing.T) {
	f := []Frame{frame(1, "c2s", `{"id":101,"method":"one"}`), frame(2, "s2c", `{"id":101,"result":{}}`)}
	for _, in := range []string{"", `{"id":1,"method":"extra"}` + "\n", `{"id":1,"method":"one"}` + "\n", `{"id":1,"method":"one"}` + "\n" + `{"id":2,"method":"extra"}` + "\n"} {
		if err := Peer(strings.NewReader(in), io.Discard, f); err == nil {
			t.Fatal("accepted early EOF or extra/mismatched request")
		}
	}
	bad := append([]Frame(nil), f...)
	bad[1].Wire = json.RawMessage(`{"id":999,"result":{}}`)
	if err := Peer(strings.NewReader("{\"id\":1,\"method\":\"one\"}\n"), io.Discard, bad); err == nil {
		t.Fatal("accepted wrong response ID")
	}
}
func TestCaptureBounds(t *testing.T) {
	var out bytes.Buffer
	c := NewCapture(&out, 18)
	if err := c.Frame("c2s", []byte(`{"id":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.Frame("s2c", []byte(`{"id":2}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.Frame("s2c", []byte(`{}`)); err == nil {
		t.Fatal("no byte cutoff")
	}
	if strings.Count(out.String(), "\n") != 2 {
		t.Fatal("published over-limit frame")
	}
}
func TestLiveGuard(t *testing.T) {
	t.Setenv("SDK_REAL_AGENT_E2E", "0")
	if Guard("record") == nil {
		t.Fatal("default enabled")
	}
	t.Setenv("SDK_REAL_AGENT_E2E", "1")
	t.Setenv("CI", "1")
	if Guard("record") == nil {
		t.Fatal("CI enabled")
	}
	t.Setenv("CI", "")
	if Guard("replay") == nil {
		t.Fatal("wrong mode enabled")
	}
	if err := Guard("record"); err != nil {
		t.Fatal(err)
	}
}
func TestSyntheticFileBoundary(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(filepath.Join(t.TempDir(), "private"), root+"/numbers.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := readSynthetic(root); err == nil {
		t.Fatal("read symlink")
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), root+"/summary.txt"); err != nil {
		t.Fatal(err)
	}
	if err := writeSynthetic(root, "sum=42\n"); err == nil {
		t.Fatal("write symlink")
	}
}
func init() {
	if len(os.Args) == 2 && os.Args[1] == "--synthetic-peer" {
		b, _ := fixtures.ReadFile("testdata/synthetic-faults/lifecycle/protocol.jsonl")
		f, e := Decode(bytes.NewReader(b))
		if e == nil {
			e = Peer(os.Stdin, os.Stdout, f)
		}
		if e != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	if len(os.Args) != 3 || os.Args[1] != "--peer" {
		return
	}
	f, err := Load(os.Args[2])
	if err == nil {
		err = Peer(os.Stdin, os.Stdout, f)
	}
	if err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
func TestSyntheticManagedSession(t *testing.T) {
	b, err := fixtures.ReadFile("testdata/synthetic-faults/lifecycle/protocol.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	frames, err := Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	init, thread, turn, err := Params(frames)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	executable, _ := os.Executable()
	c, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: executable, Args: []string{"--synthetic-peer"}, Env: []string{}, Initialize: init})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	root := t.TempDir()
	if err = os.WriteFile(root+"/numbers.txt", []byte("17\n25\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Session(ctx, c, thread, turn, &Handler{Root: root, Story: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Completed || result.Tools != 2 {
		t.Fatal("missing lifecycle/tools")
	}
	if err = c.Call(ctx, "replay/assert", nil, nil); err != nil {
		t.Fatal(err)
	}
}
func TestRecordedManagedIntegration(t *testing.T) {
	if _, err := fixtures.ReadFile("testdata/transcripts/real-sanitized/s1/protocol.jsonl"); err != nil {
		t.Fatal("required recorded fixture missing")
	}
	// An executable named codex would leave a sentinel if accidentally started.
	poison := t.TempDir()
	sentinel := poison + "/started"
	if err := os.WriteFile(poison+"/codex", []byte("#!/bin/sh\nprintf poison > '"+sentinel+"'\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", poison)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, story := range []string{"s1", "s2"} {
		for i := 0; i < 3; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err = Replay(ctx, executable, story, io.Discard)
			cancel()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("real CLI discovered")
	}
}
func TestProxyChild(t *testing.T) {
	if os.Getenv("RECORD_PROXY_FAKE") != "1" {
		return
	}
	fmtLine := []byte("{\"method\":\"synthetic\",\"params\":{}}\n")
	if os.Getenv("RECORD_PROXY_OVERFLOW") == "1" {
		for i := 0; i < 100000; i++ {
			_, _ = os.Stdout.Write(fmtLine)
		}
	} else {
		_, _ = os.Stdout.Write(fmtLine)
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
	os.Exit(0)
}
func TestProxyOfflineCleanupAndLimit(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, overflow := range []bool{false, true} {
		dir := t.TempDir()
		env := []string{"RECORD_PROXY_FAKE=1"}
		if overflow {
			env = append(env, "RECORD_PROXY_OVERFLOW=1")
		}
		reader, writer := io.Pipe()
		defer reader.Close()
		defer writer.Close()
		if !overflow {
			go writer.Close()
		}
		result, e := Proxy(ProxyConfig{Executable: executable, Args: []string{"-test.run=^TestProxyChild$"}, Env: env, Dir: dir, RawDir: dir, Seconds: 3}, reader, io.Discard)
		if !result.Reaped || !result.CleanupComplete {
			t.Fatal("cleanup incomplete")
		}
		if overflow && e == nil {
			t.Fatal("overflow succeeded")
		}
		if !overflow && e != nil {
			t.Fatal(e)
		}
	}
}
func TestProxyOfflineDeadline(t *testing.T) {
	executable, _ := os.Executable()
	dir := t.TempDir()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	start := time.Now()
	result, err := Proxy(ProxyConfig{Executable: executable, Args: []string{"-test.run=^TestProxyChild$"}, Env: []string{"RECORD_PROXY_FAKE=1"}, Dir: dir, RawDir: dir, Seconds: 1}, reader, io.Discard)
	if err == nil || result.Reason != "deadline" || !result.Reaped || !result.CleanupComplete || time.Since(start) > 5*time.Second {
		t.Fatal("deadline cleanup contract")
	}
}

// Ensure importing this internal tooling cannot resolve a real CLI implicitly.
var _ = exec.ErrNotFound
