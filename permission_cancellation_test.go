// SPDX-License-Identifier: Apache-2.0

package claude_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	claude "github.com/zoumo/claude-agent-sdk-go"
)

// Original public-boundary tests informed by the pinned official Python
// tests/test_query.py::TestControlCancelRequest (cancel an in-flight callback,
// unknown ID, and a late cancel after completion), plus its retained-output
// shutdown cases and tests/test_close_cancellation.py. The Go-specific writer
// barrier, epoch and tombstone assertions extend those behavioral ideas;
// neither reference implementation nor test code is copied.

// A separate local FIFO tells the subprocess when to cancel or resume reading.
// Its prefix event proves bytes really reached stdin before the test proceeds.
// The full transcript includes partial writes and is the external oracle.
func permissionBlockedWire(reader *bufio.Reader, transcript *os.File, id string) (json.RawMessage, bool) {
	gate, err := os.Open(os.Getenv("CLAUDE_GO_WRITE_BARRIER"))
	if err != nil || transcript == nil {
		os.Exit(16)
	}
	defer gate.Close()
	fixtureEmit(map[string]any{"type": "writer_ready"})
	prefix := make([]byte, 64)
	if _, err := io.ReadFull(reader, prefix); err != nil {
		os.Exit(17)
	}
	if _, err := transcript.Write(prefix); err != nil {
		os.Exit(18)
	}
	fixtureEmit(map[string]any{"type": "writer_prefix", "prefix": string(prefix)})
	var action [1]byte
	if _, err := io.ReadFull(gate, action[:]); err != nil {
		os.Exit(19)
	}
	switch action[0] {
	case 'q', 'c':
		fixtureEmit(map[string]any{"type": "control_cancel_request", "request_id": id})
		if action[0] == 'q' {
			if _, err := io.ReadFull(gate, action[:]); err != nil || action[0] != 'd' {
				os.Exit(20)
			}
		} else {
			if _, err := io.ReadFull(gate, action[:]); err != nil || action[0] != 'x' {
				os.Exit(22)
			}
			_, _ = io.Copy(transcript, reader)
			return nil, false
		}
	case 'x':
		_, _ = io.Copy(transcript, reader)
		return nil, false
	case 'd':
	default:
		os.Exit(21)
	}
	rest, err := reader.ReadBytes('\n')
	_, _ = transcript.Write(rest)
	if err != nil {
		return nil, false
	}
	frame := append(prefix, rest...)
	fixtureEmit(map[string]any{"type": "writer_finished"})
	return frame, true
}

// Done is first consulted when Answer derives its write context, after the
// public API has reserved its sole attempt. This observes the caller's own
// context without exposing SDK state or replacing the transport.
type answerReservationContext struct {
	context.Context
	reserved chan struct{}
	once     sync.Once
}

func (c *answerReservationContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.reserved) })
	return c.Context.Done()
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("synchronization barrier was not reached")
	}
}

func assertPermissionError(t *testing.T, err error, id string, reason error) {
	t.Helper()
	var permissionErr *claude.PermissionError
	if !errors.Is(err, reason) || !errors.As(err, &permissionErr) || permissionErr.RequestID != id {
		t.Fatalf("expected permission %q with %v, got %v", id, reason, err)
	}
}

func TestPermissionTypedCancellationIncludesUnknownAndCompletedIDs(t *testing.T) {
	requests := make(chan *claude.PermissionRequest, 2)
	c := openFixture(t, "permission_controls", func(opts *claude.Options) {
		opts.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p }
	})
	emitPermission(t, c, "completed", "Read", json.RawMessage(`{}`))
	completed := awaitPermission(t, requests)
	if err := completed.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionDeny}); err != nil {
		t.Fatal(err)
	}
	assertPermissionReply(t, c, "completed", map[string]any{"behavior": "deny", "message": ""})
	emitPermission(t, c, "pending", "Read", json.RawMessage(`{}`))
	pending := awaitPermission(t, requests)
	for _, id := range []string{"pending", "unknown", "completed", "pending", "unknown"} {
		emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": id, "future": map[string]any{"preserved": true}})
		event := receive(t, c)
		if event.Type != "control_cancel_request" || event.Cancel == nil || event.Cancel.RequestID != id || !bytes.Equal(event.Cancel.Raw, event.Raw) || !strings.Contains(string(event.Cancel.Raw), `"preserved":true`) {
			t.Fatalf("lost typed cancellation: %#v %s", event.Cancel, event.Raw)
		}
		event.Cancel.RequestID = "changed-by-consumer"
	}
	assertPermissionError(t, pending.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow}), "pending", claude.ErrPermissionCancelled)
	assertPermissionError(t, completed.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow}), "completed", claude.ErrPermissionUsed)
	if !errors.Is(context.Cause(completed.Context()), claude.ErrPermissionUsed) || !errors.Is(context.Cause(pending.Context()), claude.ErrPermissionCancelled) {
		t.Fatal("late cancellation overwrote a completed outcome")
	}
	if got := permissionResponses(t, permissionSnapshot(t, c)); len(got) != 1 {
		t.Fatalf("cancel or retry produced extra response: %#v", got)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	// Closing the transport cannot erase already terminal per-handle outcomes.
	assertPermissionError(t, pending.Answer(context.Background(), claude.PermissionAnswer{}), "pending", claude.ErrPermissionCancelled)
	assertPermissionError(t, completed.Answer(context.Background(), claude.PermissionAnswer{}), "completed", claude.ErrPermissionUsed)
}

func TestPermissionFailedAttemptRemainsDistinctAfterClose(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "oversized", true: "invalid"}[invalid], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "transcript.ndjson")
			requests := make(chan *claude.PermissionRequest, 1)
			c := openFixture(t, "permission_controls", func(opts *claude.Options) {
				opts.Limits.FrameBytes = 4096
				opts.Env = append(opts.Env, "CLAUDE_GO_TRANSCRIPT="+path)
				opts.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p }
			})
			emitPermission(t, c, "failed", "Read", json.RawMessage(`{}`))
			p := awaitPermission(t, requests)
			answer := claude.PermissionAnswer{Behavior: claude.PermissionDeny, Message: strings.Repeat("x", 8192)}
			underlying := claude.ErrResourceLimit
			if invalid {
				answer.Behavior = "invalid"
				underlying = claude.ErrInvalidPermissionAnswer
			}
			err := p.Answer(context.Background(), answer)
			assertPermissionError(t, err, "failed", claude.ErrPermissionWriteFailed)
			if !errors.Is(err, underlying) || errors.Is(err, claude.ErrUnknownOutcome) {
				t.Fatalf("pre-write failure lost known outcome: %v", err)
			}
			awaitDone(t, c)
			_ = c.Close()
			for range 2 {
				err = p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow})
				assertPermissionError(t, err, "failed", claude.ErrPermissionWriteFailed)
				if errors.Is(err, claude.ErrClosed) {
					t.Fatal("failed answer was hidden by transport closure")
				}
			}
			if got := permissionResponses(t, permissionTranscript(t, path)); len(got) != 0 {
				t.Fatalf("consumed failure was retried: %#v", got)
			}
		})
	}
}

func TestPermissionCancelOverflowInvalidatesOtherHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.ndjson")
	requests := make(chan *claude.PermissionRequest, 2)
	c := openFixture(t, "permission_controls", func(opts *claude.Options) {
		opts.Limits.EventBuffer = 1
		opts.Env = append(opts.Env, "CLAUDE_GO_TRANSCRIPT="+path)
		opts.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p }
	})
	emitPermission(t, c, "cancelled", "Read", json.RawMessage(`{}`))
	cancelled := awaitPermission(t, requests)
	emitPermission(t, c, "other", "Read", json.RawMessage(`{}`))
	other := awaitPermission(t, requests)
	// Neither event is consumed: stdout ordering fills the queue before cancel.
	emitPermissionFrame(t, c, map[string]any{"type": "accepted-before-overflow"})
	emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": "cancelled"})
	awaitDone(t, c)
	if !errors.Is(context.Cause(cancelled.Context()), claude.ErrPermissionCancelled) || other.Context().Err() == nil {
		t.Fatal("cancel/overflow left a handle live")
	}
	if receive(t, c).Type != "accepted-before-overflow" {
		t.Fatal("accepted output lost")
	}
	_, err := c.Receive(context.Background())
	var limit *claude.ResourceLimitError
	if !errors.Is(err, claude.ErrResourceLimit) || !errors.As(err, &limit) || limit.Resource != "event buffer" {
		t.Fatalf("overflow was not terminal: %v", err)
	}
	assertPermissionError(t, cancelled.Answer(context.Background(), claude.PermissionAnswer{}), "cancelled", claude.ErrPermissionCancelled)
	assertPermissionError(t, other.Answer(context.Background(), claude.PermissionAnswer{}), "other", claude.ErrClosed)
	_ = c.Close()
	if got := permissionResponses(t, permissionTranscript(t, path)); len(got) != 0 {
		t.Fatalf("overflowed cancellation allowed response: %#v", got)
	}
}

func TestPermissionUnknownCancelHistoryExhaustionIsExplicit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.ndjson")
	c := openFixture(t, "permission_controls", func(opts *claude.Options) {
		opts.Limits.PermissionHistory = 1
		opts.Env = append(opts.Env, "CLAUDE_GO_TRANSCRIPT="+path)
	})
	for range 2 {
		emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": "first"})
		if receive(t, c).Cancel.RequestID != "first" {
			t.Fatal("duplicate cancellation was hidden")
		}
	}
	emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": "excess"})
	awaitDone(t, c)
	var limit *claude.ResourceLimitError
	if err := c.Close(); !errors.As(err, &limit) || limit.Resource != "permission request history" {
		t.Fatalf("history exhaustion was not explicit: %v", err)
	}
	if got := permissionResponses(t, permissionTranscript(t, path)); len(got) != 0 {
		t.Fatalf("unknown cancellation responded: %#v", got)
	}
}

func TestPermissionCancelledIgnoringHandlerCannotAnswerLater(t *testing.T) {
	requests := make(chan *claude.PermissionRequest, 1)
	release := make(chan struct{})
	results := make(chan error, 1)
	c := openFixture(t, "permission_controls", func(opts *claude.Options) {
		opts.PermissionHandler = func(p *claude.PermissionRequest) {
			requests <- p
			<-release // Deliberately ignores cancellation until the test releases it.
			results <- p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow})
		}
	})
	emitPermission(t, c, "ignores-context", "Read", json.RawMessage(`{}`))
	p := awaitPermission(t, requests)
	emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": p.RequestID()})
	if receive(t, c).Cancel.RequestID != p.RequestID() {
		t.Fatal("cancellation missing")
	}
	close(release)
	assertPermissionError(t, awaitPermissionError(t, results), p.RequestID(), claude.ErrPermissionCancelled)
	if got := permissionResponses(t, permissionSnapshot(t, c)); len(got) != 0 {
		t.Fatalf("uncooperative callback answered: %#v", got)
	}
}

func TestPermissionDisconnectInvalidatesPendingHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.ndjson")
	requests := make(chan *claude.PermissionRequest, 1)
	c := openFixture(t, "permission_controls", func(opts *claude.Options) {
		opts.Env = append(opts.Env, "CLAUDE_GO_TRANSCRIPT="+path)
		opts.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p }
	})
	emitPermission(t, c, "disconnected", "Read", json.RawMessage(`{}`))
	p := awaitPermission(t, requests)
	send(t, c, "disconnect")
	awaitDone(t, c)
	if p.Context().Err() == nil {
		t.Fatal("disconnect left a permission context live")
	}
	_ = c.Close()
	for range 2 {
		assertPermissionError(t, p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow}), p.RequestID(), claude.ErrClosed)
	}
	if got := permissionResponses(t, permissionTranscript(t, path)); len(got) != 0 {
		t.Fatalf("disconnected handle responded: %#v", got)
	}
}

func TestPermissionMalformedCancellationIsProtocolFailure(t *testing.T) {
	for _, frame := range []string{
		`{"type":"control_cancel_request"}`,
		`{"type":"control_cancel_request","request_id":""}`,
		`{"type":"control_cancel_request","request_id":null}`,
		`{"type":"control_cancel_request","request_id":7}`,
	} {
		t.Run(frame, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "transcript.ndjson")
			c := openFixture(t, "permission_controls", func(opts *claude.Options) {
				opts.Env = append(opts.Env, "CLAUDE_GO_TRANSCRIPT="+path)
			})
			emitPermissionFrame(t, c, json.RawMessage(frame))
			awaitDone(t, c)
			if err := c.Close(); !errors.Is(err, claude.ErrProtocol) {
				t.Fatalf("malformed cancellation silently accepted: %v", err)
			}
			if got := permissionResponses(t, permissionTranscript(t, path)); len(got) != 0 {
				t.Fatalf("malformed cancel responded: %#v", got)
			}
		})
	}
}
