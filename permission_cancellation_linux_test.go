//go:build linux

// SPDX-License-Identifier: Apache-2.0

package claude_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	claude "github.com/zoumo/claude-agent-sdk-go"
)

// Linux FIFOs provide deterministic cross-process barriers without network
// access, timing guesses, or access to SDK internals. All assertions use the
// public client and bytes actually consumed from its subprocess stdin.
func permissionWireBarrier(t *testing.T) (*claude.Client, <-chan *claude.PermissionRequest, *os.File, string) {
	t.Helper()
	dir := t.TempDir()
	fifoPath, transcript := filepath.Join(dir, "gate"), filepath.Join(dir, "stdin.ndjson")
	if err := syscall.Mkfifo(fifoPath, 0600); err != nil {
		t.Fatal(err)
	}
	// Linux permits opening both ends at once. The fixture opens only its read
	// end, and test writes are one byte, so neither endpoint can fill the FIFO.
	gate, err := os.OpenFile(fifoPath, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gate.Close() })
	requests := make(chan *claude.PermissionRequest, 2)
	c := openFixture(t, "permission_controls", func(opts *claude.Options) {
		opts.Env = append(opts.Env, "CLAUDE_GO_WRITE_BARRIER="+fifoPath, "CLAUDE_GO_TRANSCRIPT="+transcript)
		opts.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p }
	})
	return c, requests, gate, transcript
}

func releasePermissionWire(t *testing.T, gate *os.File, action byte) {
	t.Helper()
	if _, err := gate.Write([]byte{action}); err != nil {
		t.Fatal(err)
	}
}

func expectPermissionWireEvent(t *testing.T, c *claude.Client, kind string) claude.Event {
	t.Helper()
	event := receive(t, c)
	if event.Type != kind {
		t.Fatalf("expected %s, got %s", kind, event.Raw)
	}
	return event
}

func startBlockedPermissionWriter(t *testing.T, c *claude.Client, id string, p *claude.PermissionRequest) <-chan error {
	t.Helper()
	send(t, c, "block_write:"+id)
	expectPermissionWireEvent(t, c, "writer_ready")
	results := make(chan error, 1)
	go func() {
		if p == nil {
			results <- c.Send(context.Background(), strings.Repeat("user-writer", 250000))
		} else {
			results <- p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionDeny, Message: strings.Repeat("answer-writer", 220000)})
		}
	}()
	event := expectPermissionWireEvent(t, c, "writer_prefix")
	var wire struct{ Prefix string }
	if err := json.Unmarshal(event.Raw, &wire); err != nil {
		t.Fatal(err)
	}
	if p != nil && (!strings.HasPrefix(wire.Prefix, `{"response":`) || !strings.Contains(wire.Prefix, `"request_id":"`+id+`"`)) {
		t.Fatalf("expected actual answer prefix, got %q", wire.Prefix)
	}
	if p == nil && !strings.HasPrefix(wire.Prefix, `{"message":`) {
		t.Fatalf("expected actual user prefix, got %q", wire.Prefix)
	}
	select {
	case err := <-results:
		t.Fatalf("large frame completed before read barrier: %v", err)
	default:
	}
	return results
}

func TestPermissionQueuedWriterCancellationPreventsAllAnswerBytes(t *testing.T) {
	c, requests, gate, path := permissionWireBarrier(t)
	emitPermission(t, c, "queued", "Read", json.RawMessage(`{}`))
	p := awaitPermission(t, requests)
	blockedSend := startBlockedPermissionWriter(t, c, p.RequestID(), nil)
	ctx := &answerReservationContext{Context: context.Background(), reserved: make(chan struct{})}
	answers := make(chan error, 1)
	go func() { answers <- p.Answer(ctx, claude.PermissionAnswer{Behavior: claude.PermissionAllow}) }()
	awaitSignal(t, ctx.reserved)
	releasePermissionWire(t, gate, 'q')
	if event := expectPermissionWireEvent(t, c, "control_cancel_request"); event.Cancel.RequestID != p.RequestID() {
		t.Fatal("wrong cancellation ID")
	}
	err := awaitPermissionError(t, answers)
	assertPermissionError(t, err, p.RequestID(), claude.ErrPermissionCancelled)
	if errors.Is(err, claude.ErrUnknownOutcome) {
		t.Fatalf("never-authorized answer falsely ambiguous: %v", err)
	}
	releasePermissionWire(t, gate, 'd')
	expectPermissionWireEvent(t, c, "writer_finished")
	if err := awaitPermissionError(t, blockedSend); err != nil {
		t.Fatalf("queued cancellation interrupted unrelated write: %v", err)
	}
	assertPermissionError(t, p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow}), p.RequestID(), claude.ErrPermissionCancelled)
	if got := permissionResponses(t, permissionSnapshot(t, c)); len(got) != 0 {
		t.Fatalf("cancelled queued answer reached wire: %#v", got)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if got := permissionResponses(t, permissionTranscript(t, path)); len(got) != 0 {
		t.Fatalf("late response after snapshot: %#v", got)
	}
}

func TestPermissionQueuedWriterCloseOrCallerCancel(t *testing.T) {
	for _, callerCancels := range []bool{false, true} {
		t.Run(map[bool]string{false: "close", true: "caller cancellation"}[callerCancels], func(t *testing.T) {
			c, requests, gate, path := permissionWireBarrier(t)
			emitPermission(t, c, "queued", "Read", json.RawMessage(`{}`))
			p := awaitPermission(t, requests)
			blockedSend := startBlockedPermissionWriter(t, c, p.RequestID(), nil)
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &answerReservationContext{Context: base, reserved: make(chan struct{})}
			answers := make(chan error, 1)
			go func() { answers <- p.Answer(ctx, claude.PermissionAnswer{Behavior: claude.PermissionAllow}) }()
			awaitSignal(t, ctx.reserved)
			closeResults := make(chan error, 1)
			want := claude.ErrClosed
			if callerCancels {
				want = claude.ErrPermissionWriteFailed
				cancel()
			} else {
				go func() { closeResults <- c.Close() }()
			}
			err := awaitPermissionError(t, answers)
			assertPermissionError(t, err, p.RequestID(), want)
			if errors.Is(err, claude.ErrUnknownOutcome) {
				t.Fatalf("queued answer inherited another write's ambiguity: %v", err)
			}
			awaitDone(t, c)
			if err := awaitPermissionError(t, blockedSend); err == nil {
				t.Fatal("blocked user write unexpectedly completed")
			}
			releasePermissionWire(t, gate, 'x')
			if callerCancels {
				_ = c.Close()
			} else {
				_ = awaitPermissionError(t, closeResults)
			}
			assertPermissionError(t, p.Answer(context.Background(), claude.PermissionAnswer{}), p.RequestID(), want)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte(`{"response":`)) {
				t.Fatal("queued answer emitted any response bytes")
			}
		})
	}
}

func TestPermissionStartedWriteCancellationAndCloseAreAmbiguous(t *testing.T) {
	for _, cliCancels := range []bool{false, true} {
		t.Run(map[bool]string{false: "close", true: "CLI cancellation"}[cliCancels], func(t *testing.T) {
			c, requests, gate, path := permissionWireBarrier(t)
			emitPermission(t, c, "started", "Read", json.RawMessage(`{}`))
			p := awaitPermission(t, requests)
			answers := startBlockedPermissionWriter(t, c, p.RequestID(), p)
			closeResults := make(chan error, 1)
			want := claude.ErrPermissionWriteFailed
			if cliCancels {
				want = claude.ErrPermissionCancelled
				releasePermissionWire(t, gate, 'c')
				expectPermissionWireEvent(t, c, "control_cancel_request")
			} else {
				go func() { closeResults <- c.Close() }()
			}
			err := awaitPermissionError(t, answers)
			assertPermissionError(t, err, p.RequestID(), want)
			if !errors.Is(err, claude.ErrUnknownOutcome) {
				t.Fatalf("started response falsely known not written: %v", err)
			}
			awaitDone(t, c)
			releasePermissionWire(t, gate, 'x')
			if cliCancels {
				_ = c.Close()
			} else {
				_ = awaitPermissionError(t, closeResults)
			}
			for range 2 {
				assertPermissionError(t, p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow}), p.RequestID(), want)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// A partial response was really emitted; no second envelope can be
			// appended by retry or by the callback returning after shutdown.
			if bytes.Count(data, []byte(`{"response":`)) != 1 {
				t.Fatalf("expected one partial response prefix, got %d", bytes.Count(data, []byte(`{"response":`)))
			}
			last := data[bytes.LastIndexByte(data, '\n')+1:]
			if !bytes.HasPrefix(last, []byte(`{"response":`)) || json.Valid(last) {
				t.Fatal("fixture did not preserve an interrupted response on stdin")
			}
		})
	}
}

func TestPermissionStartedWriteCanFinishBeforeCancellation(t *testing.T) {
	c, requests, gate, path := permissionWireBarrier(t)
	emitPermission(t, c, "completed", "Read", json.RawMessage(`{}`))
	p := awaitPermission(t, requests)
	answers := startBlockedPermissionWriter(t, c, p.RequestID(), p)
	releasePermissionWire(t, gate, 'd')
	expectPermissionWireEvent(t, c, "writer_finished")
	if err := awaitPermissionError(t, answers); err != nil {
		t.Fatal(err)
	}
	emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": p.RequestID()})
	expectPermissionWireEvent(t, c, "control_cancel_request")
	assertPermissionError(t, p.Answer(context.Background(), claude.PermissionAnswer{}), p.RequestID(), claude.ErrPermissionUsed)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if got := permissionResponses(t, permissionTranscript(t, path)); len(got) != 1 {
		t.Fatalf("completed answer was duplicated: %d", len(got))
	}
}
