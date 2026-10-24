//go:build darwin || linux

// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The native peer stops consuming its stdin after a real 64-byte prefix. A
// separate FIFO releases it, so these checks use OS transport evidence rather
// than substituting writer state or assuming a scheduling delay.
func sessionWriteBarrier(t *testing.T) (*Session, *os.File, string) {
	t.Helper()
	dir := t.TempDir()
	gatePath := filepath.Join(dir, "gate")
	path := filepath.Join(dir, "wire.jsonl")
	require.NoError(t, syscall.Mkfifo(gatePath, 0600))
	gate, err := os.OpenFile(gatePath, os.O_RDWR, 0600)
	require.NoError(t, err)
	t.Cleanup(func() { _ = gate.Close() })
	s := openSessionFixture(t, func(o *SessionOptions) {
		o.Env = append(o.Env, "CLAUDE_GO_TRANSCRIPT="+path, "CLAUDE_GO_WRITE_BARRIER="+gatePath)
	})
	return s, gate, path
}
func sessionWireSignal(t *testing.T, s *Session, kind string) {
	t.Helper()
	require.Eventually(t, func() bool {
		for _, r := range s.Unattributed().Records {
			if r.Event.Type == kind {
				return true
			}
		}
		return false
	}, time.Second, time.Millisecond)
}
func sessionRelease(t *testing.T, gate *os.File, action byte) {
	t.Helper()
	_, err := gate.Write([]byte{action})
	require.NoError(t, err)
}

func TestSessionPermissionQueuedCallerCancelPreservesLiveAuthority(t *testing.T) {
	s, gate, path := sessionWriteBarrier(t)
	p := sessionPermission(t, s, "queued", "Read", `{}`)
	submitFrame(t, s, "block_write:queued")
	sessionWireSignal(t, s, "writer_ready")
	started := make(chan error, 1)
	go func() {
		_, err := s.Submit(context.Background(), Input{Text: strings.Repeat("large", 500000)})
		started <- err
	}()
	sessionWireSignal(t, s, "writer_prefix")
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &answerReservationContext{Context: base, reserved: make(chan struct{})}
	answered := make(chan error, 1)
	go func() { answered <- p.Approve(ctx, ApprovalOptions{}) }()
	awaitSignal(t, ctx.reserved)
	cancel()
	err := awaitPermissionError(t, answered)
	var dispatch *DispatchError
	require.ErrorAs(t, err, &dispatch)
	require.Equal(t, NotSent, dispatch.State)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, p.Context().Err())
	require.NoError(t, s.Err())
	sessionRelease(t, gate, 'd')
	require.NoError(t, awaitPermissionError(t, started))
	sessionWireSignal(t, s, "writer_finished")
	require.NoError(t, p.Deny(context.Background(), DenialOptions{Message: "retry valid"}))
	require.NoError(t, s.Interrupt(context.Background(), InterruptOptions{}))
	require.NoError(t, s.Close())
	require.Len(t, permissionResponses(t, permissionTranscript(t, path)), 1)
}

func TestSessionPermissionStartedWriteOutcomeUnknownCannotRetry(t *testing.T) {
	s, gate, path := sessionWriteBarrier(t)
	p := sessionPermission(t, s, "started", "Read", `{}`)
	raw := p.Raw()
	submitFrame(t, s, "block_write:started")
	sessionWireSignal(t, s, "writer_ready")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	answered := make(chan error, 1)
	go func() { answered <- p.Deny(ctx, DenialOptions{Message: strings.Repeat("large", 500000)}) }()
	sessionWireSignal(t, s, "writer_prefix")
	cancel()
	err := awaitPermissionError(t, answered)
	var dispatch *DispatchError
	require.ErrorAs(t, err, &dispatch)
	require.Equal(t, OutcomeUnknown, dispatch.State)
	require.ErrorIs(t, err, ErrUnknownOutcome)
	require.Error(t, p.Approve(context.Background(), ApprovalOptions{}))
	sessionRelease(t, gate, 'x')
	_ = s.Close()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, 1, bytes.Count(data, []byte(`{"response":`)))
	require.Equal(t, raw, p.Raw())
}

func TestSessionConfigurationQueuedCancelAndStartedWrite(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(map[bool]string{false: "queued", true: "started"}[started], func(t *testing.T) {
			s, gate, _ := sessionWriteBarrier(t)
			submitFrame(t, s, "block_write:config")
			sessionWireSignal(t, s, "writer_ready")
			var blocking chan error
			if !started {
				blocking = make(chan error, 1)
				go func() {
					_, err := s.Submit(context.Background(), Input{Text: strings.Repeat("large", 500000)})
					blocking <- err
				}()
				sessionWireSignal(t, s, "writer_prefix")
			}
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &answerReservationContext{Context: base, reserved: make(chan struct{})}
			model := "small"
			if started {
				model = strings.Repeat("large", 500000)
			}
			results := make(chan error, 1)
			go func() { _, err := s.SetModel(ctx, ModelChange{Model: &model}); results <- err }()
			if started {
				sessionWireSignal(t, s, "writer_prefix")
			} else {
				awaitSignal(t, ctx.reserved)
			}
			cancel()
			err := awaitPermissionError(t, results)
			var d *DispatchError
			require.ErrorAs(t, err, &d)
			if started {
				require.Equal(t, OutcomeUnknown, d.State)
				sessionRelease(t, gate, 'x')
				_ = s.Close()
			} else {
				require.Equal(t, NotSent, d.State)
				require.NoError(t, s.Err())
				sessionRelease(t, gate, 'd')
				require.NoError(t, awaitPermissionError(t, blocking))
				sessionWireSignal(t, s, "writer_finished")
				_, err = s.SetModel(context.Background(), ModelChange{})
				require.NoError(t, err)
			}
		})
	}
}
