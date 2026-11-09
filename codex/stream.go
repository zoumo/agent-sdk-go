package codex

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// NewStreams connects the native Codex client to caller-provided directional
// streams. It performs the same initialize exchange as NewStdio without
// resolving or starting an executable.
func NewStreams(ctx context.Context, options StreamOptions, transport StreamTransport) (*Client, error) {
	if ctx == nil {
		return nil, errors.New("codex: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if transport.Reader == nil || transport.Writer == nil {
		return nil, errors.New("codex: stream reader and writer are required")
	}
	if sameStreamEndpoint(transport.Reader, transport.Writer) {
		return nil, errors.New("codex: stream reader and writer must differ")
	}
	if err := validateStreamArgs(options.Args); err != nil {
		return nil, err
	}
	limits, err := options.Limits.normalized()
	if err != nil {
		return nil, err
	}
	c := newClient(limits)
	c.streamMode = true
	c.streamReader = transport.Reader
	c.streamWriter = transport.Writer
	c.streamStderr = transport.Stderr
	c.streamWait = transport.Wait
	c.workers.Go(func() { defer close(c.readDone); c.readLoop() })
	c.workers.Go(c.writeLoop)
	if transport.Stderr != nil {
		c.workers.Go(c.drainStderr)
	}
	go c.cleanup()
	return c.initialize(ctx, options.Initialize)
}

func validateStreamArgs(args []string) error {
	seen := make(map[string]bool, len(args))
	for i := 0; i < len(args); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		if seen[name] {
			return fmt.Errorf("%w: duplicate native startup argument %q", ErrInvalidInput, name)
		}
		seen[name] = true
		switch name {
		case "app-server":
			if hasValue {
				return fmt.Errorf("%w: unsupported native startup argument %q", ErrInvalidInput, args[i])
			}
		case "--listen":
			if !hasValue {
				i++
				if i == len(args) {
					return fmt.Errorf("%w: missing value for --listen", ErrInvalidInput)
				}
				value = args[i]
			}
			if value != "stdio://" {
				return fmt.Errorf("%w: unsupported native transport %q", ErrInvalidInput, value)
			}
		default:
			return fmt.Errorf("%w: unsupported native startup argument %q", ErrInvalidInput, name)
		}
	}
	return nil
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
