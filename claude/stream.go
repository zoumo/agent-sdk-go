// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"fmt"
	"io"
	"reflect"

	"github.com/zoumo/agent-sdk-go/internal/queue"
)

// StreamTransport supplies already-open directional native protocol streams.
// Reader carries peer-to-SDK bytes and Writer carries SDK-to-peer bytes. Stderr
// is optional diagnostics. Wait is the authoritative peer result when present;
// nil is valid only when reader EOF alone is authoritative.
type StreamTransport struct {
	Reader io.ReadCloser
	Writer io.WriteCloser
	Stderr io.ReadCloser
	Wait   func(context.Context) error
}

// NativeArgs returns the exact native Claude argument vector Open and
// OpenStreams use for options. It performs no executable lookup or process
// start.
func NativeArgs(options SessionOptions) ([]string, error) {
	o := Options(options)
	if err := o.defaults(); err != nil {
		return nil, err
	}
	return interactionArgs(o)
}

// OpenStreams connects an owned Session to caller-provided protocol streams.
// It performs the same startup exchanges as Open without resolving CLIPath,
// starting a process or changing the host cwd or environment.
func OpenStreams(ctx context.Context, options SessionOptions, transport StreamTransport) (*Session, error) {
	if ctx == nil {
		return nil, queue.ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o := Options(options)
	if o.Env == nil {
		// A stream caller supplies the effective native snapshot. Falling back to
		// the host environment here would make replay depend on ambient state.
		o.Env = []string{}
	}
	if err := o.defaults(); err != nil {
		return nil, err
	}
	s, err := newSession(o)
	if err != nil {
		return nil, err
	}
	if _, err := openStreamInteraction(ctx, o, s, transport); err != nil {
		_ = s.fifo.Abort(err)
		return nil, err
	}
	return s, nil
}

func openStreamInteraction(ctx context.Context, options Options, session *Session, transport StreamTransport) (*interaction, error) {
	if ctx == nil {
		return nil, queue.ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if transport.Reader == nil || transport.Writer == nil {
		return nil, &Error{Op: "stream", Err: fmt.Errorf("%w: stream reader and writer are required", ErrInvalidInput)}
	}
	if sameStreamEndpoint(transport.Reader, transport.Writer) {
		return nil, &Error{Op: "stream", Err: fmt.Errorf("%w: stream reader and writer must differ", ErrInvalidInput)}
	}
	if err := options.defaults(); err != nil {
		return nil, err
	}
	if _, err := interactionArgs(options); err != nil {
		return nil, &Error{Op: "arguments", Err: err}
	}
	c := newInteraction(options, session)
	c.streamMode = true
	c.streamReader = transport.Reader
	c.streamWriter = transport.Writer
	c.streamStderr = transport.Stderr
	c.streamWait = transport.Wait
	if session != nil {
		if session.bindClient(c) {
			// A terminated owner cannot admit records from this stream.
			c.mu.Lock()
			c.closing = true
			c.failLocked(nil)
			c.mu.Unlock()
		}
	}
	c.start()
	if err := c.startup(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func sameStreamEndpoint(left, right any) bool {
	leftValue, rightValue := reflect.ValueOf(left), reflect.ValueOf(right)
	if !leftValue.IsValid() || !rightValue.IsValid() || leftValue.Type() != rightValue.Type() {
		return false
	}
	if leftValue.Type().Comparable() {
		return leftValue.Interface() == rightValue.Interface()
	}
	if leftValue.Kind() == reflect.Pointer {
		return leftValue.Pointer() == rightValue.Pointer()
	}
	return false
}
