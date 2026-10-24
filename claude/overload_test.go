// SPDX-License-Identifier: Apache-2.0

package claude

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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
				c, err := NewClient(context.Background(), opts)
				require.Nilf(t, c, "invalid limit: %v, %v", c, err)
				require.Errorf(t, err, "invalid limit: %v, %v", c, err)
				require.Containsf(t, err.Error(), field, "invalid limit: %v, %v", c, err)
				{
					_, err := os.Stat(capture)
					require.ErrorIsf(t, err, os.ErrNotExist, "invalid options spawned child: %v", err)
				}
			})
		}
	}
}

func (s *controlInteractionSuite) TestEventBacklogStillDispatchesControlResponses() {
	t := s.T()
	c := s.openFixture(t, "overload_ack_full", func(o *Options) { o.Limits.EventBuffer = 3 })
	send(t, c, "fill")
	send(t, c, "overflow")
	// The reply is emitted after four events, proving stdout crossed the
	// backlog beyond EventBuffer while the application has not called Receive.
	{
		err := c.Interrupt(context.Background())
		require.NoError(t, err)
	}
	select {
	case <-c.Done():
		require.FailNowf(t, "unexpected test outcome", "event backlog closed: %v", c.Err())
	default:
	}
	for i := 0; i < 3; i++ {
		e := receive(t, c)
		var v struct{ Seq int }
		json.Unmarshal(e.Raw, &v)
		require.EqualValuesf(t, "sequence", e.Type, "order: %s", e.Raw)
		require.EqualValuesf(t, i, v.Seq, "order: %s", e.Raw)
	}
	require.Equal(t, "rejected", receive(t, c).Type)
}

func (s *controlInteractionSuite) TestBacklogThenProtocolFailureWakesInterruptAndPermissionWithRetainedOrder() {
	t := s.T()
	requests := make(chan *PermissionRequest, 1)
	release := make(chan struct{})
	defer close(release)
	c := s.openFixture(t, "overload_controls", func(o *Options) {
		o.Limits.EventBuffer = 3
		o.PermissionHandler = func(p *PermissionRequest) { requests <- p; <-release }
	})
	send(t, c, "permission")
	require.NotNil(t, receive(t, c).Control, "missing permission")
	p := awaitPermission(t, requests)
	pending := interruptAsync(c, context.Background())
	receiveInterrupt(t, c)
	send(t, c, "fill")
	send(t, c, "overflow")
	send(t, c, "malformed")
	awaitDone(t, c)
	require.ErrorIsf(t, context.Cause(p.Context()), ErrProtocol, "handle cause: %v", context.Cause(p.Context()))
	{
		err := awaitInterrupt(t, pending)
		require.ErrorIsf(t, err, ErrProtocol, "interrupt: %v", err)
		require.ErrorIsf(t, err, ErrUnknownOutcome, "interrupt: %v", err)
	}
	var wg sync.WaitGroup
	closeResults := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			closeResults <- c.Close()
		}()
	}
	complete := make(chan struct{})
	go func() { wg.Wait(); close(complete) }()
	awaitSignal(t, complete)
	close(closeResults)
	for err := range closeResults {
		assert.ErrorIs(t, err, ErrProtocol, "Close")
	}
	for i := 0; i < 3; i++ {
		e := receive(t, c)
		var v struct{ Seq int }
		json.Unmarshal(e.Raw, &v)
		require.EqualValuesf(t, "sequence", e.Type, "accepted order: %s", e.Raw)
		require.EqualValuesf(t, i, v.Seq, "accepted order: %s", e.Raw)
	}
	require.Equal(t, "rejected", receive(t, c).Type, "extra event beyond legacy capacity survived")
	_, err := nextNative(c, context.Background())
	require.ErrorIs(t, err, ErrProtocol)
	assertPermissionError(t, p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow}), "waiting", ErrClosed)
}

func (s *controlInteractionSuite) TestTransportFailuresWakeAllPendingWork() {
	t := s.T()
	for _, mode := range []string{"malformed", "oversize", "truncated", "eof", "exit"} {
		t.Run(mode, func(t *testing.T) {
			requests := make(chan *PermissionRequest, 1)
			c := s.openFixture(t, "overload_controls", func(o *Options) {
				o.Limits.FrameBytes = 2048
				o.Limits.StderrBytes = 32
				o.PermissionHandler = func(p *PermissionRequest) { requests <- p }
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
				{
					err := awaitInterrupt(t, call)
					require.Errorf(t, err, "pending control: %v", err)
					require.ErrorIsf(t, err, ErrUnknownOutcome, "pending control: %v", err)
				}
			}
			awaitSignal(t, p.Context().Done())
			require.NotNil(t, context.Cause(p.Context()), "missing handle cancellation cause")
			closed := c.Close()
			want := ErrProtocol
			if mode == "oversize" {
				want = ErrResourceLimit
			}
			if mode == "eof" {
				want = ErrUnexpectedEOF
			}
			require.Falsef(t, mode != "exit" && !errors.Is(closed, want), "final cause: %v", closed)
			if mode == "exit" {
				var detail *Error
				require.ErrorAsf(t, closed, &detail, "exit diagnostics: %#v", detail)
				require.NotNilf(t, detail.ExitCode, "exit diagnostics: %#v", detail)
				require.EqualValuesf(t, 23, *detail.ExitCode, "exit diagnostics: %#v", detail)
				require.Falsef(t, len(detail.Stderr) > 32, "exit diagnostics: %#v", detail)
				require.Truef(t, strings.HasSuffix(detail.Stderr, "last-diagnostic"), "exit diagnostics: %#v", detail)
			}
			for i := 0; i < 3; i++ {
				e := receive(t, c)
				var v struct{ Seq int }
				json.Unmarshal(e.Raw, &v)
				require.EqualValuesf(t, "sequence", e.Type, "retained order: %s", e.Raw)
				require.EqualValuesf(t, i, v.Seq, "retained order: %s", e.Raw)
			}
			{
				_, err := nextNative(c, context.Background())
				require.Error(t, err, "missing terminal error")
			}
			assertPermissionError(t, p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow}), "waiting", ErrClosed)
		})
	}
}

func (s *controlInteractionSuite) TestShutdownDoesNotWaitForIgnoringCallback() {
	t := s.T()
	requests := make(chan *PermissionRequest, 1)
	release := make(chan struct{})
	returned := make(chan struct{})
	defer close(release)
	c := s.openFixture(t, "overload_controls", func(o *Options) {
		o.PermissionHandler = func(p *PermissionRequest) { requests <- p; <-release; close(returned) }
	})
	send(t, c, "permission")
	receive(t, c)
	p := awaitPermission(t, requests)
	start := time.Now()
	{
		err := c.Close()
		require.NoError(t, err)
	}
	require.False(t, time.Since(start) > 3*time.Second, "Close waited for callback")
	require.ErrorIsf(t, context.Cause(p.Context()), ErrClosed, "callback context: %v", context.Cause(p.Context()))
	select {
	case <-returned:
		require.FailNow(t, "callback unexpectedly returned")
	default:
	}
}
