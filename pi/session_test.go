package pi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/pi"
)

type command struct {
	ID       string     `json:"id"`
	Type     string     `json:"type"`
	Message  string     `json:"message"`
	Images   []pi.Image `json:"images"`
	Behavior string     `json:"streamingBehavior"`
	Model    string     `json:"modelId"`
	Provider string     `json:"provider"`
	Level    string     `json:"level"`
}

type peer struct {
	input  *json.Decoder
	output *io.PipeWriter
}

func (p peer) receive() (command, error) {
	var c command
	err := p.input.Decode(&c)
	return c, err
}

func (p peer) reply(c command, data any) error {
	return json.NewEncoder(p.output).Encode(map[string]any{"type": "response", "command": c.Type, "id": c.ID, "data": data, "success": true})
}

func runPeer(t *testing.T, serve func(peer) error) (*pi.Session, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	in, w := io.Pipe()
	r, out := io.Pipe()
	finished := make(chan error, 1)
	go func() {
		p := peer{input: json.NewDecoder(in), output: out}
		c, err := p.receive()
		if err == nil && c.Type != "get_state" {
			err = fmt.Errorf("first command %q", c.Type)
		}
		if err == nil {
			_, err = fmt.Fprintln(out, `{"type":"session_ready","future":true}`)
		}
		if err == nil {
			err = p.reply(c, map[string]any{"sessionId": "native-1", "model": map[string]string{"id": "m"}, "unknown": 1})
		}
		if err == nil {
			err = serve(p)
		}
		_ = out.Close()
		_ = in.Close()
		finished <- err
	}()
	s, err := pi.Attach(ctx, w, r, "native-1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.CloseAndDiscard(); require.NoError(t, <-finished) })
	return s, ctx
}

func TestRPCConversation(t *testing.T) {
	inputs := make(chan command, 8)
	s, ctx := runPeer(t, func(p peer) error {
		for {
			c, err := p.receive()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			inputs <- c
			switch c.Type {
			case "prompt", "steer", "follow_up":
				if _, err := fmt.Fprintln(p.output, `{"type":"message_end","id":9,"error":{"details":"opaque"},"message":{"content":"next"}}`); err != nil {
					return err
				}
				if err := p.reply(c, map[string]string{"disposition": "queued"}); err != nil {
					return err
				}
			case "get_state":
				if err := p.reply(c, map[string]any{"sessionId": "native-1", "model": map[string]string{"id": "m"}, "thinkingLevel": "high", "other": true}); err != nil {
					return err
				}
			default:
				if err := p.reply(c, nil); err != nil {
					return err
				}
			}
		}
	})
	require.Equal(t, "native-1", s.ID())
	img := []pi.Image{{Type: "image", Data: "YQ==", MIMEType: "image/png"}}
	result, err := s.Prompt(ctx, "read", img, "steer")
	require.NoError(t, err)
	assert.Equal(t, "queued", result.Disposition)
	c := <-inputs
	assert.Equal(t, "read", c.Message)
	assert.Equal(t, img, c.Images)
	assert.Equal(t, "steer", c.Behavior)
	_, err = s.Steer(ctx, "change", nil)
	require.NoError(t, err)
	assert.Equal(t, "steer", (<-inputs).Type)
	_, err = s.FollowUp(ctx, "then", nil)
	require.NoError(t, err)
	assert.Equal(t, "follow_up", (<-inputs).Type)
	require.NoError(t, s.SetModel(ctx, "deepseek", "m"))
	c = <-inputs
	assert.Equal(t, "deepseek", c.Provider)
	assert.Equal(t, "m", c.Model)
	require.NoError(t, s.SetThinkingLevel(ctx, "high"))
	assert.Equal(t, "high", (<-inputs).Level)
	state, err := s.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, "high", state.ThinkingLevel)
	assert.Contains(t, string(state.Raw), `"other":true`)
	<-inputs
	require.NoError(t, s.Abort(ctx))
	assert.Equal(t, "abort", (<-inputs).Type)
	require.NoError(t, s.Close())
	require.NoError(t, s.Close())
	<-s.Done()
	for _, kind := range []string{"session_ready", "message_end", "message_end", "message_end"} {
		event, err := s.Receive(ctx)
		require.NoError(t, err)
		assert.Equal(t, kind, event.Type)
		if kind == "message_end" {
			assert.Contains(t, string(event.Raw), `"id":9`)
			assert.Contains(t, string(event.Raw), `"error":{"details":"opaque"}`)
		} else {
			assert.JSONEq(t, `{"type":"session_ready","future":true}`, string(event.Raw))
		}
	}
	_, err = s.Receive(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestOutOfOrderResponses(t *testing.T) {
	firstRead := make(chan struct{})
	s, ctx := runPeer(t, func(p peer) error {
		first, err := p.receive()
		if err != nil {
			return err
		}
		close(firstRead)
		second, err := p.receive()
		if err != nil {
			return err
		}
		if err := p.reply(second, nil); err != nil {
			return err
		}
		return json.NewEncoder(p.output).Encode(map[string]any{"type": "response", "id": first.ID, "command": first.Type, "success": false, "error": "model rejected", "nativeDetail": 123})
	})
	first := make(chan error, 1)
	go func() { first <- s.SetModel(ctx, "provider", "id") }()
	<-firstRead
	require.NoError(t, s.Abort(ctx))
	var rejected *pi.CommandError
	require.ErrorAs(t, <-first, &rejected)
	assert.Contains(t, string(rejected.Raw), `"nativeDetail":123`)
	<-s.Done()
	assert.ErrorIs(t, s.Err(), io.ErrUnexpectedEOF)
}

func TestCancelledResponseDoesNotMisrouteNextCommand(t *testing.T) {
	firstRead := make(chan struct{})
	next := make(chan struct{})
	s, ctx := runPeer(t, func(p peer) error {
		c, err := p.receive()
		if err != nil {
			return err
		}
		close(firstRead)
		<-next
		if err := p.reply(c, nil); err != nil {
			return err
		}
		c, err = p.receive()
		if err != nil {
			return err
		}
		return p.reply(c, nil)
	})
	q, cancel := context.WithCancel(ctx)
	res := make(chan error, 1)
	go func() { res <- s.Abort(q) }()
	<-firstRead
	cancel()
	var unknown *pi.DispatchError
	require.ErrorAs(t, <-res, &unknown)
	assert.ErrorIs(t, unknown, context.Canceled)
	close(next)
	require.NoError(t, s.SetThinkingLevel(ctx, "low"))
}

func TestUnexpectedPartialRecord(t *testing.T) {
	s, ctx := runPeer(t, func(p peer) error { _, err := io.WriteString(p.output, `{"type":"truncated"}`); return err })
	<-s.Done()
	event, err := s.Receive(ctx)
	require.NoError(t, err)
	assert.Equal(t, "session_ready", event.Type)
	_, err = s.Receive(ctx)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestHandshakeIdentityMismatch(t *testing.T) {
	in, w := io.Pipe()
	r, out := io.Pipe()
	go func() {
		defer out.Close()
		defer in.Close()
		c, _ := bufio.NewReader(in).ReadBytes('\n')
		var cmd command
		_ = json.Unmarshal(c, &cmd)
		_ = (peer{output: out}).reply(cmd, map[string]string{"sessionId": "wrong-id"})
	}()
	session, err := pi.Attach(t.Context(), w, r, "expected-id")
	require.ErrorContains(t, err, "expected session")
	assert.Nil(t, session)
}

func TestAbortAndReuseSession(t *testing.T) {
	s, ctx := runPeer(t, func(p peer) error {
		steps := []struct {
			command, message, disposition, event string
		}{
			{"prompt", "first", "started", `{"type":"agent_start"}`},
			{"steer", "while running", "queued", "{\"type\":\"queue_update\",\"future\":\"first\u2028second\"}"},
			{"abort", "", "", ""},
			{"get_state", "", "", ""},
			{"prompt", "second", "started", `{"type":"message_end","message":{"role":"assistant","stopReason":"stop"}}`},
			{"prompt", "provider failure", "started", `{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"provider unavailable"}}`},
		}
		for _, step := range steps {
			c, err := p.receive()
			if err != nil {
				return err
			}
			if c.Type != step.command || c.Message != step.message {
				return fmt.Errorf("unexpected command %q with message %q", c.Type, c.Message)
			}
			var data any
			switch {
			case step.disposition != "":
				data = map[string]string{"disposition": step.disposition}
			case step.command == "get_state":
				data = map[string]any{"sessionId": "native-1", "isStreaming": false}
			}
			if step.command == "abort" {
				batch := `{"type":"message_end","message":{"role":"assistant","stopReason":"aborted","errorMessage":"interrupted"}}` + "\n" +
					`{"type":"agent_settled","aborted":true}` + "\n"
				if _, err := io.WriteString(p.output, batch); err != nil {
					return err
				}
			}
			if err := p.reply(c, data); err != nil {
				return err
			}
			if step.event != "" {
				// Split a record across writes and mix LF with CRLF.
				if _, err := io.WriteString(p.output, step.event[:8]); err != nil {
					return err
				}
				if _, err := io.WriteString(p.output, step.event[8:]+"\r\n"); err != nil {
					return err
				}
			}
			if step.command == "prompt" && step.message != "first" {
				if _, err := fmt.Fprintln(p.output, `{"type":"agent_settled","aborted":false}`); err != nil {
					return err
				}
			}
		}
		_, err := p.receive()
		if !errors.Is(err, io.EOF) {
			return fmt.Errorf("expected stdin EOF, got %w", err)
		}
		return nil
	})

	first, err := s.Prompt(ctx, "first", nil, "")
	require.NoError(t, err)
	assert.Equal(t, "started", first.Disposition)
	steered, err := s.Steer(ctx, "while running", nil)
	require.NoError(t, err)
	assert.Equal(t, "queued", steered.Disposition)
	require.NoError(t, s.Abort(ctx))

	receive := func(kind string) pi.Event {
		t.Helper()
		event, err := s.Receive(ctx)
		require.NoError(t, err)
		require.Equal(t, kind, event.Type)
		return event
	}
	receive("session_ready")
	receive("agent_start")
	assert.Contains(t, string(receive("queue_update").Raw), "first\u2028second")
	assert.Contains(t, string(receive("message_end").Raw), `"stopReason":"aborted"`)
	assert.Contains(t, string(receive("agent_settled").Raw), `"aborted":true`)

	state, err := s.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, "native-1", state.SessionID)
	assert.False(t, state.IsStreaming)
	second, err := s.Prompt(ctx, "second", nil, "")
	require.NoError(t, err)
	assert.Equal(t, "started", second.Disposition)
	assert.Contains(t, string(receive("message_end").Raw), `"stopReason":"stop"`)
	assert.Contains(t, string(receive("agent_settled").Raw), `"aborted":false`)

	third, err := s.Prompt(ctx, "provider failure", nil, "")
	require.NoError(t, err)
	assert.Equal(t, "started", third.Disposition)
	failed := receive("message_end")
	assert.Contains(t, string(failed.Raw), `"stopReason":"error"`)
	assert.Contains(t, string(failed.Raw), `"errorMessage":"provider unavailable"`)
	receive("agent_settled")
	require.NoError(t, s.Close())
}

func TestConcurrentCloseReleasesPendingRequestAndReceive(t *testing.T) {
	requestRead := make(chan struct{})
	s, ctx := runPeer(t, func(p peer) error {
		if _, err := p.receive(); err != nil {
			return err
		}
		close(requestRead)
		if _, err := p.receive(); !errors.Is(err, io.EOF) {
			return fmt.Errorf("expected stdin EOF, got %w", err)
		}
		return nil
	})
	_, err := s.Receive(ctx)
	require.NoError(t, err)
	request := make(chan error, 1)
	go func() { request <- s.Abort(ctx) }()
	<-requestRead

	received := make(chan error, 1)
	go func() {
		_, err := s.Receive(ctx)
		received <- err
	}()
	closed := make(chan error, 2)
	go func() { closed <- s.Close() }()
	go func() { closed <- s.Close() }()
	require.NoError(t, <-closed)
	require.NoError(t, <-closed)
	var unknown *pi.DispatchError
	require.ErrorAs(t, <-request, &unknown)
	assert.True(t, unknown.Sent)
	assert.ErrorIs(t, unknown, io.ErrClosedPipe)
	assert.ErrorIs(t, <-received, io.EOF)
	assert.ErrorIs(t, s.Abort(ctx), io.ErrClosedPipe)
}

func TestCloseAndDiscardReleasesUnreadEvents(t *testing.T) {
	s, ctx := runPeer(t, func(p peer) error {
		if _, err := p.receive(); !errors.Is(err, io.EOF) {
			return fmt.Errorf("expected stdin EOF, got %w", err)
		}
		return nil
	})
	require.NoError(t, s.CloseAndDiscard())
	_, err := s.Receive(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestPeerExitReleasesPendingRequest(t *testing.T) {
	s, ctx := runPeer(t, func(p peer) error {
		_, err := p.receive()
		return err
	})
	var unknown *pi.DispatchError
	require.ErrorAs(t, s.Abort(ctx), &unknown)
	assert.True(t, unknown.Sent)
	assert.ErrorIs(t, unknown, io.ErrUnexpectedEOF)
	assert.ErrorIs(t, s.Close(), io.ErrUnexpectedEOF)
}

type observedWriter struct {
	*io.PipeWriter
	started chan struct{}
}

func (w observedWriter) Write(data []byte) (int, error) {
	w.started <- struct{}{}
	return w.PipeWriter.Write(data)
}

func TestCancelWhileWriteIsBlockedThenClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	in, w := io.Pipe()
	r, out := io.Pipe()
	defer in.Close()
	defer out.Close()
	writer := observedWriter{PipeWriter: w, started: make(chan struct{}, 2)}
	handshake := make(chan error, 1)
	go func() {
		p := peer{input: json.NewDecoder(in), output: out}
		c, err := p.receive()
		if err == nil {
			err = p.reply(c, map[string]string{"sessionId": "native-1"})
		}
		handshake <- err
	}()
	s, err := pi.Attach(ctx, writer, r, "native-1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.CloseAndDiscard() })
	require.NoError(t, <-handshake)
	<-writer.started

	requestCtx, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()
	request := make(chan error, 1)
	go func() { request <- s.Abort(requestCtx) }()
	select {
	case <-writer.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancelRequest()
	var unknown *pi.DispatchError
	require.ErrorAs(t, <-request, &unknown)
	assert.ErrorIs(t, unknown, context.Canceled)
	assert.False(t, unknown.Sent)
	require.NoError(t, s.Close())
	<-s.Done()
}
