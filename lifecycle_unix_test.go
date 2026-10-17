//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

// SPDX-License-Identifier: Apache-2.0
package claude_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	claude "github.com/zoumo/claude-agent-sdk-go"
)

func TestCloseReapsUncooperativeChild(t *testing.T) {
	opts := fixtureOptions(t, "ignore_eof")
	capture := filepath.Join(opts.WorkDir, "pid.json")
	opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
	c, err := claude.Open(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var process struct{ PID int }
	json.Unmarshal(data, &process)
	start := time.Now()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("Close exceeded bound")
	}
	if err := syscall.Kill(process.PID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("child still exists or is unreaped: pid %d, %v", process.PID, err)
	}
}

func TestInitializationTimeoutReapsChild(t *testing.T) {
	opts := fixtureOptions(t, "init_hang")
	opts.InitializeTimeout = 150 * time.Millisecond
	capture := filepath.Join(opts.WorkDir, "pid.json")
	opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
	_, err := claude.Open(context.Background(), opts)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Open = %v", err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var process struct{ PID int }
	json.Unmarshal(data, &process)
	if err := syscall.Kill(process.PID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("startup child still exists: pid %d, %v", process.PID, err)
	}
}
