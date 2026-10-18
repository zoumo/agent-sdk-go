// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/zoumo/agent-sdk-go/claude/tools/replay"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordedAuthenticationFailureReplay(t *testing.T) {
	// Poison PATH proves default replay uses its explicit fake executable only.
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "live-started")
	poison := filepath.Join(dir, "claude")
	if err := os.WriteFile(poison, []byte("#!/bin/sh\nprintf live > '"+sentinel+"'\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("CLAUDE_GO_LIVE", "0")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "consumed")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := Open(ctx, Options{CLIPath: exe, WorkDir: dir, Env: []string{"HOME=" + dir, "PATH=" + dir, transcript.PeerEnvironment + "=1", "CLAUDE_GO_REPLAY_COMPLETE=" + marker, "GORACE=atexit_sleep_ms=0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	prompt, err := transcript.Prompt()
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Send(ctx, prompt); err != nil {
		t.Fatal(err)
	}
	nativeCount := 0
	resultSequence := uint64(0)
	idleSequence := uint64(0)
	rawCount := 0
	for idleSequence == 0 {
		record, err := client.Receive(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := record.(SubagentCreated); ok {
			t.Fatal("error fixture cannot prove creation")
		}
		native, ok := record.(Event)
		if !ok {
			continue
		}
		nativeCount++
		if bytes.Contains(transcript.Wire, native.Raw) {
			rawCount++
		} else {
			t.Fatal("Raw changed")
		}
		if native.Result != nil {
			if !native.Result.IsError || native.Subtype != "success" {
				t.Fatal("lost native error semantics")
			}
			resultSequence = uint64(nativeCount)
			if !bytes.Contains(native.Raw, []byte(`"terminal_reason":"api_error"`)) {
				t.Fatal("unknown native field missing")
			}
		}
		if native.Subtype == "session_state_changed" && native.System != nil && native.System.State != nil && *native.System.State == "idle" {
			idleSequence = uint64(nativeCount)
		}
	}
	if nativeCount != 8 || rawCount != 8 || resultSequence == 0 || idleSequence <= resultSequence {
		t.Fatalf("unexpected observations native=%d result=%d idle=%d", nativeCount, resultSequence, idleSequence)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "complete\n" {
		t.Fatal("unconsumed fixture", err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("real CLI sentinel started")
	}
}
func TestRecordedManifestAndSecretScan(t *testing.T) {
	if err := transcript.Scan(transcript.Wire); err != nil {
		t.Fatal(err)
	}
	var m struct {
		Provenance string `json:"provenance"`
		Outcome    string `json:"outcome"`
		SHA        string `json:"wire_sha256"`
	}
	if err := json.Unmarshal(transcript.Manifest, &m); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(transcript.Wire)
	if m.Provenance != "real-sanitized" || m.Outcome != "authentication-blocked" || m.SHA != hex.EncodeToString(sum[:]) {
		t.Fatal("manifest mismatch")
	}
	if strings.Contains(string(transcript.Manifest), "model-success") {
		t.Fatal("mislabelled live coverage")
	}
}
