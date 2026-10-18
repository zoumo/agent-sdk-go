// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bytes"

	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
	case 'r':
		for i := range 8 {
			fixtureEmit(map[string]any{"type": "control_request", "request_id": fmt.Sprintf("unsupported-%d", i), "request": map[string]any{"subtype": "future_control"}})
		}
		_, _ = io.Copy(transcript, reader)
		return nil, false
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
		require.FailNow(t, "synchronization barrier was not reached")
	}
}

func assertPermissionError(t *testing.T, err error, id string, reason error) {
	t.Helper()
	var permissionErr *PermissionError
	require.ErrorIsf(t, err, reason, "expected permission %q with %v, got %v", id, reason, err)
	require.ErrorAsf(t, err, &permissionErr, "expected permission %q with %v, got %v", id, reason, err)
	require.EqualValuesf(t, id, permissionErr.RequestID, "expected permission %q with %v, got %v", id, reason, err)
}

func (s *controlInteractionSuite) TestPermissionTypedCancellationIncludesUnknownAndCompletedIDs() {
	t := s.T()
	requests := make(chan *PermissionRequest, 2)
	c := s.openFixture(t, "permission_controls", func(opts *Options) {
		opts.PermissionHandler = func(p *PermissionRequest) { requests <- p }
	})
	emitPermission(t, c, "completed", "Read", json.RawMessage(`{}`))
	completed := awaitPermission(t, requests)
	{
		err := completed.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny})
		require.NoError(t, err)
	}
	assertPermissionReply(t, c, "completed", map[string]any{"behavior": "deny", "message": ""})
	emitPermission(t, c, "pending", "Read", json.RawMessage(`{}`))
	pending := awaitPermission(t, requests)
	for _, id := range []string{"pending", "unknown", "completed", "pending", "unknown"} {
		emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": id, "future": map[string]any{"preserved": true}})
		event := receive(t, c)
		require.EqualValuesf(t, "control_cancel_request", event.Type, "lost typed cancellation: %#v %s", event.Cancel, event.Raw)
		require.NotNilf(t, event.Cancel, "lost typed cancellation: %#v %s", event.Cancel, event.Raw)
		require.EqualValuesf(t, id, event.Cancel.RequestID, "lost typed cancellation: %#v %s", event.Cancel, event.Raw)
		require.Truef(t, bytes.Equal(event.Cancel.Raw, event.Raw), "lost typed cancellation: %#v %s", event.Cancel, event.Raw)
		require.Containsf(t, string(event.Cancel.Raw), `"preserved":true`, "lost typed cancellation: %#v %s", event.Cancel, event.Raw)
		event.Cancel.RequestID = "changed-by-consumer"
	}
	assertPermissionError(t, pending.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow}), "pending", ErrPermissionCancelled)
	assertPermissionError(t, completed.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow}), "completed", ErrPermissionUsed)
	require.ErrorIs(t, context.Cause(completed.Context()), ErrPermissionUsed, "late cancellation overwrote a completed outcome")
	require.ErrorIs(t, context.Cause(pending.Context()), ErrPermissionCancelled, "late cancellation overwrote a completed outcome")
	{
		got := permissionResponses(t, permissionSnapshot(t, c))
		require.EqualValuesf(t, 1, len(got), "cancel or retry produced extra response: %#v", got)
	}
	{
		err := c.Close()
		require.NoError(t, err)
	}
	// Closing the transport cannot erase already terminal per-handle outcomes.
	assertPermissionError(t, pending.Answer(context.Background(), PermissionAnswer{}), "pending", ErrPermissionCancelled)
	assertPermissionError(t, completed.Answer(context.Background(), PermissionAnswer{}), "completed", ErrPermissionUsed)
}

func (s *controlInteractionSuite) TestPermissionFailedAttemptRemainsDistinctAfterClose() {
	t := s.T()
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "oversized", true: "invalid"}[invalid], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "transcript.ndjson")
			requests := make(chan *PermissionRequest, 1)
			c := s.openFixture(t, "permission_controls", func(opts *Options) {
				opts.Limits.FrameBytes = 4096
				opts.Env = append(opts.Env, "CLAUDE_GO_TRANSCRIPT="+path)
				opts.PermissionHandler = func(p *PermissionRequest) { requests <- p }
			})
			emitPermission(t, c, "failed", "Read", json.RawMessage(`{}`))
			p := awaitPermission(t, requests)
			answer := PermissionAnswer{Behavior: PermissionDeny, Message: strings.Repeat("x", 8192)}
			underlying := ErrResourceLimit
			if invalid {
				answer.Behavior = "invalid"
				underlying = ErrInvalidPermissionAnswer
			}
			err := p.Answer(context.Background(), answer)
			assertPermissionError(t, err, "failed", ErrPermissionWriteFailed)
			require.ErrorIsf(t, err, underlying, "pre-write failure lost known outcome: %v", err)
			require.NotErrorIsf(t, err, ErrUnknownOutcome, "pre-write failure lost known outcome: %v", err)
			awaitDone(t, c)
			_ = c.Close()
			for range 2 {
				err = p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow})
				assertPermissionError(t, err, "failed", ErrPermissionWriteFailed)
				require.NotErrorIs(t, err, ErrClosed, "failed answer was hidden by transport closure")
			}
			{
				got := permissionResponses(t, permissionTranscript(t, path))
				require.EqualValuesf(t, 0, len(got), "consumed failure was retried: %#v", got)
			}
		})
	}
}

func (s *controlInteractionSuite) TestPermissionCancellationCrossesBacklogAndCloseInvalidatesOtherHandles() {
	t := s.T()
	path := filepath.Join(t.TempDir(), "transcript.ndjson")
	requests := make(chan *PermissionRequest, 2)
	c := s.openFixture(t, "permission_controls", func(opts *Options) {
		opts.Limits.EventBuffer = 1
		opts.Env = append(opts.Env, "CLAUDE_GO_TRANSCRIPT="+path)
		opts.PermissionHandler = func(p *PermissionRequest) { requests <- p }
	})
	emitPermission(t, c, "cancelled", "Read", json.RawMessage(`{}`))
	cancelled := awaitPermission(t, requests)
	emitPermission(t, c, "other", "Read", json.RawMessage(`{}`))
	other := awaitPermission(t, requests)
	// Neither event is consumed: cancellation must cross the legacy capacity.
	emitPermissionFrame(t, c, map[string]any{"type": "accepted-before-cancel"})
	emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": "cancelled"})
	awaitSignal(t, cancelled.Context().Done())
	require.ErrorIs(t, context.Cause(cancelled.Context()), ErrPermissionCancelled)
	require.NoError(t, c.Interrupt(context.Background()), "control reply blocked behind backlog")
	require.Nil(t, other.Context().Err(), "backlog invalidated unrelated permission")
	require.NoError(t, c.Close())
	awaitDone(t, c)
	require.ErrorIs(t, context.Cause(other.Context()), ErrClosed)
	require.EqualValues(t, "accepted-before-cancel", receive(t, c).Type)
	require.Equal(t, "cancelled", receive(t, c).Cancel.RequestID)
	_, err := nextNative(c, context.Background())
	require.ErrorIs(t, err, io.EOF)
	assertPermissionError(t, cancelled.Answer(context.Background(), PermissionAnswer{}), "cancelled", ErrPermissionCancelled)
	assertPermissionError(t, other.Answer(context.Background(), PermissionAnswer{}), "other", ErrClosed)
	_ = c.Close()
	{
		got := permissionResponses(t, permissionTranscript(t, path))
		require.EqualValuesf(t, 0, len(got), "cancelled or closed handle allowed response: %#v", got)
	}
}

func (s *controlInteractionSuite) TestPermissionUnknownCancelHistoryExhaustionIsExplicit() {
	t := s.T()
	path := filepath.Join(t.TempDir(), "transcript.ndjson")
	c := s.openFixture(t, "permission_controls", func(opts *Options) {
		opts.Limits.PermissionHistory = 1
		opts.Env = append(opts.Env, "CLAUDE_GO_TRANSCRIPT="+path)
	})
	for range 2 {
		emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": "first"})
		require.EqualValues(t, "first", receive(t, c).Cancel.RequestID, "duplicate cancellation was hidden")
	}
	emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": "excess"})
	awaitDone(t, c)
	var limit *ResourceLimitError
	{
		err := c.Close()
		require.ErrorAsf(t, err, &limit, "history exhaustion was not explicit: %v", err)
		require.EqualValuesf(t, "permission request history", limit.Resource, "history exhaustion was not explicit: %v", err)
	}
	{
		got := permissionResponses(t, permissionTranscript(t, path))
		require.EqualValuesf(t, 0, len(got), "unknown cancellation responded: %#v", got)
	}
}

func (s *controlInteractionSuite) TestPermissionCancelledIgnoringHandlerCannotAnswerLater() {
	t := s.T()
	requests := make(chan *PermissionRequest, 1)
	release := make(chan struct{})
	results := make(chan error, 1)
	c := s.openFixture(t, "permission_controls", func(opts *Options) {
		opts.PermissionHandler = func(p *PermissionRequest) {
			requests <- p
			<-release // Deliberately ignores cancellation until the test releases it.
			results <- p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow})
		}
	})
	emitPermission(t, c, "ignores-context", "Read", json.RawMessage(`{}`))
	p := awaitPermission(t, requests)
	emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": p.RequestID()})
	require.EqualValues(t, p.RequestID(), receive(t, c).Cancel.RequestID, "cancellation missing")
	close(release)
	assertPermissionError(t, awaitPermissionError(t, results), p.RequestID(), ErrPermissionCancelled)
	{
		got := permissionResponses(t, permissionSnapshot(t, c))
		require.EqualValuesf(t, 0, len(got), "uncooperative callback answered: %#v", got)
	}
}

func (s *controlInteractionSuite) TestPermissionDisconnectInvalidatesPendingHandle() {
	t := s.T()
	path := filepath.Join(t.TempDir(), "transcript.ndjson")
	requests := make(chan *PermissionRequest, 1)
	c := s.openFixture(t, "permission_controls", func(opts *Options) {
		opts.Env = append(opts.Env, "CLAUDE_GO_TRANSCRIPT="+path)
		opts.PermissionHandler = func(p *PermissionRequest) { requests <- p }
	})
	emitPermission(t, c, "disconnected", "Read", json.RawMessage(`{}`))
	p := awaitPermission(t, requests)
	send(t, c, "disconnect")
	awaitDone(t, c)
	require.NotNil(t, p.Context().Err(), "disconnect left a permission context live")
	_ = c.Close()
	for range 2 {
		assertPermissionError(t, p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow}), p.RequestID(), ErrClosed)
	}
	{
		got := permissionResponses(t, permissionTranscript(t, path))
		require.EqualValuesf(t, 0, len(got), "disconnected handle responded: %#v", got)
	}
}

func (s *controlInteractionSuite) TestPermissionMalformedCancellationIsProtocolFailure() {
	t := s.T()
	for _, frame := range []string{
		`{"type":"control_cancel_request"}`,
		`{"type":"control_cancel_request","request_id":""}`,
		`{"type":"control_cancel_request","request_id":null}`,
		`{"type":"control_cancel_request","request_id":7}`,
	} {
		t.Run(frame, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "transcript.ndjson")
			c := s.openFixture(t, "permission_controls", func(opts *Options) {
				opts.Env = append(opts.Env, "CLAUDE_GO_TRANSCRIPT="+path)
			})
			emitPermissionFrame(t, c, json.RawMessage(frame))
			awaitDone(t, c)
			{
				err := c.Close()
				require.ErrorIsf(t, err, ErrProtocol, "malformed cancellation silently accepted: %v", err)
			}
			{
				got := permissionResponses(t, permissionTranscript(t, path))
				require.EqualValuesf(t, 0, len(got), "malformed cancel responded: %#v", got)
			}
		})
	}
}
