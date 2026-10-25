//go:build linux

// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// Linux FIFOs provide deterministic cross-process barriers without network
// access, timing guesses, or access to SDK internals. All assertions use the
// public client and bytes actually consumed from its subprocess stdin.
func permissionWireBarrier(t *testing.T) (*interaction, *os.File, string) {
	t.Helper()
	dir := t.TempDir()
	fifoPath, transcript := filepath.Join(dir, "gate"), filepath.Join(dir, "stdin.ndjson")
	{
		err := syscall.Mkfifo(fifoPath, 0600)
		require.NoError(t, err)
	}
	// Linux permits opening both ends at once. The fixture opens only its read
	// end, and test writes are one byte, so neither endpoint can fill the FIFO.
	gate, err := os.OpenFile(fifoPath, os.O_RDWR, 0600)
	require.NoError(t, err)
	t.Cleanup(func() { _ = gate.Close() })

	c := openFixture(t, "permission_controls", func(opts *Options) {
		opts.Env = append(opts.Env, "CLAUDE_GO_WRITE_BARRIER="+fifoPath, "CLAUDE_GO_TRANSCRIPT="+transcript)

	})
	return c, gate, transcript
}

func releasePermissionWire(t *testing.T, gate *os.File, action byte) {
	t.Helper()
	{
		_, err := gate.Write([]byte{action})
		require.NoError(t, err)
	}
}

func expectPermissionWireEvent(t *testing.T, c *interaction, kind string) Event {
	t.Helper()
	event := receive(t, c)
	require.EqualValuesf(t, kind, eventKind(event), "expected %s, got %s", kind, event.Raw)
	return event
}

func startBlockedPermissionWriter(t *testing.T, c *interaction, id string, p *PermissionRequest) <-chan error {
	t.Helper()
	send(t, c, "block_write:"+id)
	expectPermissionWireEvent(t, c, "writer_ready")
	results := make(chan error, 1)
	go func() {
		if p == nil {
			results <- c.Send(context.Background(), strings.Repeat("user-writer", 250000))
		} else {
			results <- p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny, Message: strings.Repeat("answer-writer", 220000)})
		}
	}()
	event := expectPermissionWireEvent(t, c, "writer_prefix")
	var wire struct{ Prefix string }
	{
		err := json.Unmarshal(event.Raw, &wire)
		require.NoError(t, err)
	}
	require.Falsef(t, p != nil && (!strings.HasPrefix(wire.Prefix, `{"response":`) || !strings.Contains(wire.Prefix, `"request_id":"`+id+`"`)), "expected actual answer prefix, got %q", wire.Prefix)
	require.Falsef(t, p == nil && !strings.HasPrefix(wire.Prefix, `{"type":"user","session_id":`), "expected actual user prefix, got %q", wire.Prefix)
	select {
	case err := <-results:
		require.FailNowf(t, "unexpected test outcome", "large frame completed before read barrier: %v", err)
	default:
	}
	return results
}

func (s *controlInteractionSuite) TestPermissionQueuedWriterCancellationPreventsAllAnswerBytes() {
	t := s.T()
	c, gate, path := permissionWireBarrier(t)
	p := emitPermission(t, c, "queued", "Read", json.RawMessage(`{}`)).Permission
	blockedSend := startBlockedPermissionWriter(t, c, p.RequestID(), nil)
	ctx := &answerReservationContext{Context: context.Background(), reserved: make(chan struct{})}
	answers := make(chan error, 1)
	go func() { answers <- p.Answer(ctx, PermissionAnswer{Behavior: PermissionAllow}) }()
	awaitSignal(t, ctx.reserved)
	releasePermissionWire(t, gate, 'q')
	{
		event := expectPermissionWireEvent(t, c, "control_cancel_request")
		require.EqualValues(t, p.RequestID(), event.Cancel.RequestID, "wrong cancellation ID")
	}
	err := awaitPermissionError(t, answers)
	assertPermissionError(t, err, p.RequestID(), ErrPermissionCancelled)
	require.NotErrorIsf(t, err, ErrUnknownOutcome, "never-authorized answer falsely ambiguous: %v", err)
	releasePermissionWire(t, gate, 'd')
	expectPermissionWireEvent(t, c, "writer_finished")
	{
		err := awaitPermissionError(t, blockedSend)
		require.NoErrorf(t, err, "queued cancellation interrupted unrelated write: %v", err)
	}
	assertPermissionError(t, p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow}), p.RequestID(), ErrPermissionCancelled)
	{
		got := permissionResponses(t, permissionSnapshot(t, c))
		require.EqualValuesf(t, 0, len(got), "cancelled queued answer reached wire: %#v", got)
	}
	{
		err := c.Close()
		require.NoError(t, err)
	}
	{
		got := permissionResponses(t, permissionTranscript(t, path))
		require.EqualValuesf(t, 0, len(got), "late response after snapshot: %#v", got)
	}
}

func (s *controlInteractionSuite) TestPermissionQueuedWriterCloseOrCallerCancel() {
	t := s.T()
	for _, callerCancels := range []bool{false, true} {
		t.Run(map[bool]string{false: "close", true: "caller cancellation"}[callerCancels], func(t *testing.T) {
			c, gate, path := permissionWireBarrier(t)
			p := emitPermission(t, c, "queued", "Read", json.RawMessage(`{}`)).Permission
			blockedSend := startBlockedPermissionWriter(t, c, p.RequestID(), nil)
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &answerReservationContext{Context: base, reserved: make(chan struct{})}
			answers := make(chan error, 1)
			go func() { answers <- p.Answer(ctx, PermissionAnswer{Behavior: PermissionAllow}) }()
			awaitSignal(t, ctx.reserved)
			closeResults := make(chan error, 1)
			if callerCancels {
				cancel()
				err := awaitPermissionError(t, answers)
				require.ErrorIs(t, err, context.Canceled)
				var dispatch *DispatchError
				require.ErrorAs(t, err, &dispatch)
				require.Equal(t, NotSent, dispatch.State)
				require.NoError(t, c.Err())
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.False(t, bytes.Contains(data, []byte(`{"response":`)), "canceled queued answer emitted bytes")
				releasePermissionWire(t, gate, 'd')
				expectPermissionWireEvent(t, c, "writer_finished")
				require.NoError(t, awaitPermissionError(t, blockedSend), "caller cancellation interrupted unrelated write")
				require.Empty(t, permissionResponses(t, permissionSnapshot(t, c)))
				require.NoError(t, p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow}))
				assertPermissionReply(t, c, p.RequestID(), map[string]any{"behavior": "allow", "updatedInput": map[string]any{}})
				got := permissionResponses(t, permissionSnapshot(t, c))
				require.Len(t, got, 1)
				require.NoError(t, c.Close())
				require.Len(t, permissionResponses(t, permissionTranscript(t, path)), 1)
			} else {
				go func() { closeResults <- c.Close() }()
				err := awaitPermissionError(t, answers)
				assertPermissionError(t, err, p.RequestID(), ErrClosed)
				require.NotErrorIs(t, err, ErrUnknownOutcome)
				awaitDone(t, c)
				require.Error(t, awaitPermissionError(t, blockedSend))
				releasePermissionWire(t, gate, 'x')
				_ = awaitPermissionError(t, closeResults)
				assertPermissionError(t, p.Answer(context.Background(), PermissionAnswer{}), p.RequestID(), ErrClosed)
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.False(t, bytes.Contains(data, []byte(`{"response":`)), "queued answer emitted any response bytes")
			}
		})
	}
}

func (s *controlInteractionSuite) TestPermissionStartedWriteCancellationAndCloseAreAmbiguous() {
	t := s.T()
	for _, cliCancels := range []bool{false, true} {
		t.Run(map[bool]string{false: "close", true: "CLI cancellation"}[cliCancels], func(t *testing.T) {
			c, gate, path := permissionWireBarrier(t)
			p := emitPermission(t, c, "started", "Read", json.RawMessage(`{}`)).Permission
			answers := startBlockedPermissionWriter(t, c, p.RequestID(), p)
			closeResults := make(chan error, 1)
			want := ErrPermissionWriteFailed
			if cliCancels {
				want = ErrPermissionCancelled
				releasePermissionWire(t, gate, 'c')
				expectPermissionWireEvent(t, c, "control_cancel_request")
			} else {
				go func() { closeResults <- c.Close() }()
			}
			err := awaitPermissionError(t, answers)
			assertPermissionError(t, err, p.RequestID(), want)
			require.ErrorIsf(t, err, ErrUnknownOutcome, "started response falsely known not written: %v", err)
			awaitDone(t, c)
			releasePermissionWire(t, gate, 'x')
			if cliCancels {
				_ = c.Close()
			} else {
				_ = awaitPermissionError(t, closeResults)
			}
			for range 2 {
				assertPermissionError(t, p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow}), p.RequestID(), want)
			}
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			// A partial response was really emitted; no second envelope can be
			// appended by retry or by the callback returning after shutdown.
			require.EqualValuesf(t, 1, bytes.Count(data, []byte(`{"response":`)), "expected one partial response prefix, got %d", bytes.Count(data, []byte(`{"response":`)))
			last := data[bytes.LastIndexByte(data, '\n')+1:]
			require.True(t, bytes.HasPrefix(last, []byte(`{"response":`)), "fixture did not preserve an interrupted response on stdin")
			require.False(t, json.Valid(last), "fixture did not preserve an interrupted response on stdin")
		})
	}
}

func (s *controlInteractionSuite) TestPermissionStartedWriteCanFinishBeforeCancellation() {
	t := s.T()
	c, gate, path := permissionWireBarrier(t)
	p := emitPermission(t, c, "completed", "Read", json.RawMessage(`{}`)).Permission
	answers := startBlockedPermissionWriter(t, c, p.RequestID(), p)
	releasePermissionWire(t, gate, 'd')
	expectPermissionWireEvent(t, c, "writer_finished")
	{
		err := awaitPermissionError(t, answers)
		require.NoError(t, err)
	}
	emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": p.RequestID()})
	expectPermissionWireEvent(t, c, "control_cancel_request")
	assertPermissionError(t, p.Answer(context.Background(), PermissionAnswer{}), p.RequestID(), ErrPermissionUsed)
	{
		err := c.Close()
		require.NoError(t, err)
	}
	{
		got := permissionResponses(t, permissionTranscript(t, path))
		require.EqualValuesf(t, 1, len(got), "completed answer was duplicated: %d", len(got))
	}
}

func (s *controlInteractionSuite) TestQueuedSendAndInterruptDoNotInheritStartedWriteAmbiguity() {
	t := s.T()
	c, gate, _ := permissionWireBarrier(t)
	started := startBlockedPermissionWriter(t, c, "unused", nil)
	sendCtx := &answerReservationContext{Context: context.Background(), reserved: make(chan struct{})}
	queuedSend := make(chan error, 1)
	go func() { queuedSend <- c.Send(sendCtx, "must never be written") }()
	awaitSignal(t, sendCtx.reserved)
	controlCtx := &answerReservationContext{Context: context.Background(), reserved: make(chan struct{})}
	queuedControl := interruptAsync(c, controlCtx)
	awaitSignal(t, controlCtx.reserved)
	closeResults := make(chan error, 1)
	go func() { closeResults <- c.Close() }()
	for _, result := range []<-chan error{queuedSend, queuedControl} {
		err := awaitPermissionError(t, result)
		require.ErrorIsf(t, err, ErrClosed, "queued operation inherited ambiguity: %v", err)
		require.NotErrorIsf(t, err, ErrUnknownOutcome, "queued operation inherited ambiguity: %v", err)
	}
	{
		err := awaitPermissionError(t, started)
		require.ErrorIsf(t, err, ErrUnknownOutcome, "started write outcome: %v", err)
	}
	releasePermissionWire(t, gate, 'x')
	{
		err := awaitPermissionError(t, closeResults)
		require.ErrorIsf(t, err, ErrUnknownOutcome, "Close lost started write failure: %v", err)
	}
	require.ErrorIsf(t, c.Err(), ErrUnknownOutcome, "Err lost original cause: %v", c.Err())
}

func (s *controlInteractionSuite) TestAutomaticResponseQueueOverflowUnblocksStartedWriter() {
	t := s.T()
	dir := t.TempDir()
	fifo := filepath.Join(dir, "gate")
	path := filepath.Join(dir, "stdin.ndjson")
	{
		err := syscall.Mkfifo(fifo, 0600)
		require.NoError(t, err)
	}
	gate, err := os.OpenFile(fifo, os.O_RDWR, 0600)
	require.NoError(t, err)
	defer gate.Close()
	c := s.openFixture(t, "permission_controls", func(o *Options) {
		o.Limits.ControlBuffer = 2
		o.Limits.EventBuffer = 32
		o.Env = append(o.Env, "CLAUDE_GO_WRITE_BARRIER="+fifo, "CLAUDE_GO_TRANSCRIPT="+path)
	})
	started := startBlockedPermissionWriter(t, c, "unused", nil)
	releasePermissionWire(t, gate, 'r')
	awaitDone(t, c)
	{
		err := awaitPermissionError(t, started)
		require.ErrorIsf(t, err, ErrUnknownOutcome, "blocked writer: %v", err)
	}
	var limit *ResourceLimitError
	{
		err := c.Close()
		require.ErrorAsf(t, err, &limit, "response queue bound: %v", err)
		require.EqualValuesf(t, "incoming control queue", limit.Resource, "response queue bound: %v", err)
		require.EqualValuesf(t, 2, limit.Limit, "response queue bound: %v", err)
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.False(t, bytes.Contains(data, []byte(`{"response":`)), "queued automatic reply reached blocked stdin")
}
