// SPDX-License-Identifier: Apache-2.0

package claude_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	claude "github.com/zoumo/claude-agent-sdk-go"
)

// These original subprocess cases extend the official test_subprocess_buffering.py,
// test_transport.py and test_close_cancellation.py behavioral contracts. They use
// real pipes and public API observations, never a replacement internal transport.
func runOverloadFixture(mode string, reader *bufio.Reader) {
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		var wire struct {
			Type      string
			RequestID string `json:"request_id"`
			Message   struct{ Content string }
		}
		if json.Unmarshal(line, &wire) != nil {
			os.Exit(30)
		}
		if wire.Type == "control_request" {
			if mode == "overload_ack_full" {
				sessionReply(wire.RequestID, "")
			} else {
				fixtureEmit(map[string]any{"type": "control_seen", "wire": json.RawMessage(line)})
			}
			continue
		}
		switch wire.Message.Content {
		case "permission":
			fixtureEmit(map[string]any{"type": "control_request", "request_id": "waiting", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Read", "input": map[string]any{}}})
		case "fill":
			for i := 0; i < 3; i++ {
				fixtureEmit(map[string]any{"type": "sequence", "seq": i})
			}
		case "overflow":
			fixtureEmit(map[string]any{"type": "rejected"})
		case "malformed":
			fmt.Fprintln(os.Stdout, `{"type":`)
			// Keep the process alive until stdin closes: this makes the immediate
			// protocol cause deterministic instead of racing process exit.
			io.Copy(io.Discard, reader)
			return
		case "oversize":
			fmt.Fprintln(os.Stdout, strings.Repeat("x", 2049))
			return
		case "truncated":
			fmt.Fprint(os.Stdout, `{"type":"unfinished"}`)
			return
		case "exit":
			fmt.Fprint(os.Stderr, strings.Repeat("diagnostic", 300)+"last-diagnostic")
			os.Exit(23)
		case "eof":
			return
		}
	}
}

func TestConfigurationRejectsIntegerOverflowBeforeSpawn(t *testing.T) {
	fields := []string{"EventBuffer", "FrameBytes", "PendingControls", "ControlBuffer", "StderrBytes", "PendingPermissions", "PermissionWorkers", "PermissionHistory"}
	maxima := []int{65536, 64 << 20, 65536, 65536, 16 << 20, 65536, 1024, 1 << 20}
	for i, field := range fields {
		for _, value := range []int{-1, maxima[i] + 1, int(^uint(0) >> 1)} {
			t.Run(fmt.Sprintf("%s/%d", field, value), func(t *testing.T) {
				opts := fixtureOptions(t, "echo")
				capture := filepath.Join(opts.WorkDir, "must-not-start.json")
				opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
				reflect.ValueOf(&opts.Limits).Elem().FieldByName(field).SetInt(int64(value))
				c, err := claude.Open(context.Background(), opts)
				if c != nil || err == nil || !strings.Contains(err.Error(), field) {
					t.Fatalf("invalid limit: %v, %v", c, err)
				}
				if _, err := os.Stat(capture); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("invalid options spawned child: %v", err)
				}
			})
		}
	}
}

func TestFullEventQueueStillDispatchesControlResponses(t *testing.T) {
	c := openFixture(t, "overload_ack_full", func(o *claude.Options) { o.Limits.EventBuffer = 3 })
	send(t, c, "fill")
	// The reply is emitted after all three events, proving stdout crossed the
	// entire full queue while the application has not called Receive.
	if err := c.Interrupt(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
		t.Fatalf("full but not exceeded queue closed: %v", c.Err())
	default:
	}
	for i := 0; i < 3; i++ {
		e := receive(t, c)
		var v struct{ Seq int }
		json.Unmarshal(e.Raw, &v)
		if e.Type != "sequence" || v.Seq != i {
			t.Fatalf("order: %s", e.Raw)
		}
	}
}

func TestOverloadWakesInterruptAndPermissionWithRetainedOrder(t *testing.T) {
	requests := make(chan *claude.PermissionRequest, 1)
	release := make(chan struct{})
	defer close(release)
	c := openFixture(t, "overload_controls", func(o *claude.Options) {
		o.Limits.EventBuffer = 3
		o.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p; <-release }
	})
	send(t, c, "permission")
	if receive(t, c).Control == nil {
		t.Fatal("missing permission")
	}
	p := awaitPermission(t, requests)
	pending := interruptAsync(c, context.Background())
	receiveInterrupt(t, c)
	send(t, c, "fill")
	send(t, c, "overflow")
	awaitDone(t, c)
	if !errors.Is(context.Cause(p.Context()), claude.ErrResourceLimit) {
		t.Fatalf("handle cause: %v", context.Cause(p.Context()))
	}
	if err := awaitInterrupt(t, pending); !errors.Is(err, claude.ErrResourceLimit) || !errors.Is(err, claude.ErrUnknownOutcome) {
		t.Fatalf("interrupt: %v", err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Close(); !errors.Is(err, claude.ErrResourceLimit) {
				t.Errorf("Close: %v", err)
			}
		}()
	}
	complete := make(chan struct{})
	go func() { wg.Wait(); close(complete) }()
	awaitSignal(t, complete)
	for i := 0; i < 3; i++ {
		e := receive(t, c)
		var v struct{ Seq int }
		json.Unmarshal(e.Raw, &v)
		if e.Type != "sequence" || v.Seq != i {
			t.Fatalf("accepted order: %s", e.Raw)
		}
	}
	_, err := c.Receive(context.Background())
	var limit *claude.ResourceLimitError
	if !errors.As(err, &limit) || limit.Resource != "event buffer" || limit.Limit != 3 {
		t.Fatalf("terminal: %v", err)
	}
	assertPermissionError(t, p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow}), "waiting", claude.ErrClosed)
}

func TestTransportFailuresWakeAllPendingWork(t *testing.T) {
	for _, mode := range []string{"malformed", "oversize", "truncated", "eof", "exit"} {
		t.Run(mode, func(t *testing.T) {
			requests := make(chan *claude.PermissionRequest, 1)
			c := openFixture(t, "overload_controls", func(o *claude.Options) {
				o.Limits.FrameBytes = 2048
				o.Limits.StderrBytes = 32
				o.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p }
			})
			send(t, c, "permission")
			receive(t, c)
			p := awaitPermission(t, requests)
			var pending []<-chan error
			for range 3 {
				pending = append(pending, interruptAsync(c, context.Background()))
				receiveInterrupt(t, c)
			}
			send(t, c, "fill")
			send(t, c, mode)
			awaitDone(t, c)
			for _, call := range pending {
				if err := awaitInterrupt(t, call); err == nil || !errors.Is(err, claude.ErrUnknownOutcome) {
					t.Fatalf("pending control: %v", err)
				}
			}
			awaitSignal(t, p.Context().Done())
			if context.Cause(p.Context()) == nil {
				t.Fatal("missing handle cancellation cause")
			}
			closed := c.Close()
			want := claude.ErrProtocol
			if mode == "oversize" {
				want = claude.ErrResourceLimit
			}
			if mode == "eof" {
				want = claude.ErrUnexpectedEOF
			}
			if mode != "exit" && !errors.Is(closed, want) {
				t.Fatalf("final cause: %v", closed)
			}
			if mode == "exit" {
				var detail *claude.Error
				if !errors.As(closed, &detail) || detail.ExitCode == nil || *detail.ExitCode != 23 || len(detail.Stderr) > 32 || !strings.HasSuffix(detail.Stderr, "last-diagnostic") {
					t.Fatalf("exit diagnostics: %#v", detail)
				}
			}
			for i := 0; i < 3; i++ {
				e := receive(t, c)
				var v struct{ Seq int }
				json.Unmarshal(e.Raw, &v)
				if e.Type != "sequence" || v.Seq != i {
					t.Fatalf("retained order: %s", e.Raw)
				}
			}
			if _, err := c.Receive(context.Background()); err == nil {
				t.Fatal("missing terminal error")
			}
			assertPermissionError(t, p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow}), "waiting", claude.ErrClosed)
		})
	}
}

func TestShutdownDoesNotWaitForIgnoringCallback(t *testing.T) {
	requests := make(chan *claude.PermissionRequest, 1)
	release := make(chan struct{})
	returned := make(chan struct{})
	defer close(release)
	c := openFixture(t, "overload_controls", func(o *claude.Options) {
		o.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p; <-release; close(returned) }
	})
	send(t, c, "permission")
	receive(t, c)
	p := awaitPermission(t, requests)
	start := time.Now()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("Close waited for callback")
	}
	if !errors.Is(context.Cause(p.Context()), claude.ErrClosed) {
		t.Fatalf("callback context: %v", context.Cause(p.Context()))
	}
	select {
	case <-returned:
		t.Fatal("callback unexpectedly returned")
	default:
	}
}
