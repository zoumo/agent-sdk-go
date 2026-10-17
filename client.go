// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Limits bounds transport memory. Zero fields select defaults. A bound exceeded
// by incoming traffic terminates the connection explicitly; frames are not
// silently discarded and the stdout reader never waits for an event consumer.
type Limits struct {
	EventBuffer     int
	FrameBytes      int
	PendingControls int
	ControlBuffer   int
	StderrBytes     int
}

// Options configures a locally installed CLI. Env, when non-nil, replaces the
// child's entire environment; nil inherits os.Environ. WorkDir and Model are
// passed literally, without a shell. Existing CLI settings still apply.
type Options struct {
	CLIPath           string
	WorkDir           string
	Model             string
	Env               []string
	InitializeTimeout time.Duration
	ShutdownTimeout   time.Duration
	Limits            Limits
}

func (o *Options) defaults() error {
	if o.CLIPath == "" {
		o.CLIPath = "claude"
	}
	if o.InitializeTimeout < 0 || o.ShutdownTimeout < 0 {
		return fmt.Errorf("claude: timeouts must not be negative")
	}
	if o.InitializeTimeout == 0 {
		o.InitializeTimeout = 60 * time.Second
	}
	if o.ShutdownTimeout == 0 {
		o.ShutdownTimeout = 3 * time.Second
	}
	for _, pair := range []struct {
		v   *int
		def int
	}{
		{&o.Limits.EventBuffer, 128}, {&o.Limits.FrameBytes, 4 << 20},
		{&o.Limits.PendingControls, 32}, {&o.Limits.ControlBuffer, 32}, {&o.Limits.StderrBytes, 8192},
	} {
		if *pair.v < 0 {
			return fmt.Errorf("claude: limits must not be negative")
		}
		if *pair.v == 0 {
			*pair.v = pair.def
		}
	}
	return nil
}

type controlReply struct {
	data json.RawMessage
	err  error
}
type pendingControl struct {
	subtype string
	reply   chan controlReply
}

// Client owns one subprocess. Send, Close, Done and Err are concurrency-safe.
// Receive is a single-consumer stream; callers must not call it concurrently.
type Client struct {
	opts                                Options
	cmd                                 *exec.Cmd
	stdin, stdout, stderr               *os.File
	events                              chan Event
	responses                           chan *ControlRequest
	writeGate                           chan struct{}
	done                                chan struct{}
	stop                                chan struct{}
	cleaned                             chan struct{}
	stdoutDone, stderrDone, processDone chan struct{}
	stopOnce                            sync.Once
	mu                                  sync.Mutex
	terminal                            error
	closing                             bool
	processErr                          error
	cleanupErr                          error
	stderrTail                          []byte
	pending                             map[string]pendingControl
	nextID                              atomic.Uint64
}

// Open starts and initializes a local process. ctx bounds startup, not the
// lifetime of the returned client. Initialization failures include bounded
// cleanup before returning. No Claude CLI version probe or network call is made
// by the SDK itself; starting a real CLI can use its configured account.
func Open(ctx context.Context, options Options) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := options.defaults(); err != nil {
		return nil, err
	}
	args := []string{"--output-format", "stream-json", "--verbose", "--input-format", "stream-json", "--permission-prompt-tool", "stdio", "--include-partial-messages"}
	if options.Model != "" {
		args = append(args, "--model="+options.Model)
	}
	cmd := exec.Command(options.CLIPath, args...)
	cmd.Dir, cmd.Env = options.WorkDir, options.Env
	prepareProcess(cmd)
	// Own all pipe ends ourselves: exec.Cmd.Wait must not close stdout before the
	// reader has consumed the final bytes.
	inputR, inputW, err := os.Pipe()
	if err != nil {
		return nil, &Error{Op: "pipe", Err: err}
	}
	outputR, outputW, err := os.Pipe()
	if err != nil {
		inputR.Close()
		inputW.Close()
		return nil, &Error{Op: "pipe", Err: err}
	}
	errorR, errorW, err := os.Pipe()
	if err != nil {
		inputR.Close()
		inputW.Close()
		outputR.Close()
		outputW.Close()
		return nil, &Error{Op: "pipe", Err: err}
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inputR, outputW, errorW
	if err := cmd.Start(); err != nil {
		for _, file := range []*os.File{inputR, inputW, outputR, outputW, errorR, errorW} {
			file.Close()
		}
		return nil, &Error{Op: "start", Err: err}
	}
	inputR.Close()
	outputW.Close()
	errorW.Close()
	c := &Client{opts: options, cmd: cmd, stdin: inputW, stdout: outputR, stderr: errorR,
		events: make(chan Event, options.Limits.EventBuffer), responses: make(chan *ControlRequest, options.Limits.ControlBuffer),
		writeGate: make(chan struct{}, 1), done: make(chan struct{}), stop: make(chan struct{}), cleaned: make(chan struct{}),
		stdoutDone: make(chan struct{}), stderrDone: make(chan struct{}), processDone: make(chan struct{}), pending: make(map[string]pendingControl)}
	go c.readStdout()
	go c.readStderr()
	go c.respondControls()
	go func() { err := cmd.Wait(); c.mu.Lock(); c.processErr = err; c.mu.Unlock(); close(c.processDone) }()
	go c.ownLifecycle()
	initCtx, cancel := context.WithTimeout(ctx, options.InitializeTimeout)
	defer cancel()
	if _, err := c.control(initCtx, "initialize", map[string]any{"subtype": "initialize", "hooks": nil}); err != nil {
		c.fail(&Error{Op: "initialize", Err: err})
		_ = c.Close()
		final := c.Err()
		startup := &Error{Op: "initialize", Err: errors.Join(err, final)}
		var diagnostics *Error
		if errors.As(final, &diagnostics) {
			startup.Stderr, startup.ExitCode = diagnostics.Stderr, diagnostics.ExitCode
		}
		startup.OutcomeUnknown = errors.Is(startup.Err, ErrUnknownOutcome)
		return nil, startup
	}
	select {
	case <-c.done:
		_ = c.Close()
		return nil, c.operationError()
	default:
	}
	return c, nil
}

// Send writes one complete user-message frame. Success proves only a completed
// local write. A canceled or failed started write is conservatively classified
// as unknown outcome and terminates this connection; it is never retried.
func (c *Client) Send(ctx context.Context, text string) error {
	return c.writeJSON(ctx, map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}, "parent_tool_use_id": nil, "session_id": ""})
}

// Receive returns accepted events in arrival order, then the terminal error (or
// io.EOF after an ordinary Close). Canceling ctx only cancels this receive wait.
func (c *Client) Receive(ctx context.Context) (Event, error) {
	select {
	case event, ok := <-c.events:
		if ok {
			return event, nil
		}
		return c.receiveEnd(ctx)
	default:
	}
	select {
	case event, ok := <-c.events:
		if ok {
			return event, nil
		}
		return c.receiveEnd(ctx)
	case <-ctx.Done():
		return Event{}, ctx.Err()
	}
}
func (c *Client) receiveEnd(ctx context.Context) (Event, error) {
	select {
	case <-c.cleaned:
		if err := c.Err(); err != nil {
			return Event{}, err
		}
		return Event{}, io.EOF
	case <-ctx.Done():
		return Event{}, ctx.Err()
	}
}

// Done closes as soon as shutdown or a terminal failure is observed, independent
// of event capacity. Already accepted events can still be drained by Receive.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err returns the currently known terminal error, with a snapshot of the bounded
// stderr tail. Cleanup can add exit diagnostics; Close waits for that cleanup.
// Err is nil during operation and after a successful explicit Close.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal == nil && c.cleanupErr == nil {
		return nil
	}
	err := errors.Join(c.terminal, c.cleanupErr)
	e := &Error{Op: "session", Err: err, Stderr: string(c.stderrTail)}
	if errors.Is(err, ErrUnknownOutcome) {
		e.OutcomeUnknown = true
	}
	var exit *exec.ExitError
	if errors.As(c.processErr, &exit) {
		code := exit.ExitCode()
		e.ExitCode = &code
	}
	return e
}
func (c *Client) operationError() error {
	if err := c.Err(); err != nil {
		return err
	}
	return ErrClosed
}

// Close is idempotent and concurrency-safe. It closes stdin, gives the child a
// bounded chance to exit, then terminates/kills it as needed. It does not wait
// for the event consumer. It returns terminal or cleanup failures, if any.
func (c *Client) Close() error {
	c.mu.Lock()
	c.closing = true
	c.mu.Unlock()
	c.fail(nil)
	<-c.cleaned
	return c.Err()
}
func (c *Client) fail(err error) {
	c.mu.Lock()
	if err != nil {
		if c.terminal == nil || c.terminal == ErrUnexpectedEOF {
			c.terminal = err
		} else if (errors.Is(err, ErrProtocol) && !errors.Is(c.terminal, ErrProtocol)) ||
			(errors.Is(err, ErrResourceLimit) && !errors.Is(c.terminal, ErrResourceLimit)) {
			// Process exit can win the race with decoding its final stdout.
			// Keep the more specific reader failure once those bytes are drained.
			c.terminal = errors.Join(c.terminal, err)
		}
	}
	c.mu.Unlock()
	c.stopOnce.Do(func() { close(c.done); close(c.stop) })
}

func (c *Client) writeJSON(ctx context.Context, value any) error {
	frame, err := json.Marshal(value)
	if err != nil {
		return &Error{Op: "encode", Err: err}
	}
	if len(frame) > c.opts.Limits.FrameBytes {
		return &ResourceLimitError{Resource: "outbound frame bytes", Limit: c.opts.Limits.FrameBytes}
	}
	frame = append(frame, '\n')
	select {
	case c.writeGate <- struct{}{}:
		defer func() { <-c.writeGate }()
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.operationError()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-c.done:
		return c.operationError()
	default:
	}
	// Closing a pipe unblocks an in-flight Write on supported Go platforms. There
	// is no detached write goroutine that can later send after cancellation.
	canceled := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() {
		c.fail(&Error{Op: "write", Err: ctx.Err(), OutcomeUnknown: true})
		_ = c.stdin.Close()
		close(canceled)
	})
	n, err := c.stdin.Write(frame)
	if !stopCancel() {
		<-canceled
		return &Error{Op: "write", Err: ctx.Err(), OutcomeUnknown: true}
	}
	if err == nil && n != len(frame) {
		err = io.ErrShortWrite
	}
	if err != nil {
		e := &Error{Op: "write", Err: err, OutcomeUnknown: true}
		c.fail(e)
		return e
	}
	return nil
}

// control registers its waiter before writing. Keep this shared path for future
// control operations so their correlation and teardown rules stay identical.
func (c *Client) control(ctx context.Context, subtype string, request any) (json.RawMessage, error) {
	id := "go_" + strconv.FormatUint(c.nextID.Add(1), 10)
	waiter := pendingControl{subtype: subtype, reply: make(chan controlReply, 1)}
	c.mu.Lock()
	if len(c.pending) >= c.opts.Limits.PendingControls {
		c.mu.Unlock()
		return nil, &ResourceLimitError{Resource: "pending controls", Limit: c.opts.Limits.PendingControls}
	}
	c.pending[id] = waiter
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	if err := c.writeJSON(ctx, map[string]any{"type": "control_request", "request_id": id, "request": request}); err != nil {
		return nil, err
	}
	// Prefer an already-arrived response when the peer exits immediately after it.
	select {
	case reply := <-waiter.reply:
		return reply.data, reply.err
	default:
	}
	select {
	case reply := <-waiter.reply:
		return reply.data, reply.err
	case <-ctx.Done():
		return nil, &Error{Op: subtype, Err: ctx.Err(), OutcomeUnknown: true}
	case <-c.done:
		return nil, &Error{Op: subtype, Err: c.operationError(), OutcomeUnknown: true}
	}
}

func (c *Client) route(event Event) error {
	if event.Type == "control_response" {
		var wire struct {
			Response struct {
				Subtype   string          `json:"subtype"`
				RequestID string          `json:"request_id"`
				Response  json.RawMessage `json:"response"`
				Error     string          `json:"error"`
			} `json:"response"`
		}
		if err := json.Unmarshal(event.Raw, &wire); err != nil {
			return err
		}
		reply := wire.Response
		if reply.RequestID == "" || (reply.Subtype != "success" && reply.Subtype != "error") {
			return fmt.Errorf("invalid control response")
		}
		c.mu.Lock()
		waiter, found := c.pending[reply.RequestID]
		if found {
			delete(c.pending, reply.RequestID)
		}
		c.mu.Unlock()
		if found {
			result := controlReply{data: reply.Response}
			if reply.Subtype == "error" {
				result.err = &ControlError{RequestID: reply.RequestID, Subtype: waiter.subtype, Message: reply.Error}
			}
			waiter.reply <- result
		}
		return nil
	}
	if event.Control != nil {
		select {
		case c.responses <- event.Control:
		default:
			return &ResourceLimitError{Resource: "incoming control queue", Limit: c.opts.Limits.ControlBuffer}
		}
	}
	select {
	case c.events <- event:
		return nil
	default:
		return &ResourceLimitError{Resource: "event buffer", Limit: c.opts.Limits.EventBuffer}
	}
}

func (c *Client) readStdout() {
	defer close(c.stdoutDone)
	defer close(c.events)
	reader := bufio.NewReaderSize(c.stdout, min(c.opts.Limits.FrameBytes+1, 32<<10))
	var frame []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		allowance := c.opts.Limits.FrameBytes
		if len(fragment) > 0 && fragment[len(fragment)-1] == '\n' {
			allowance++
		}
		if len(frame)+len(fragment) > allowance {
			c.fail(&ResourceLimitError{Resource: "incoming frame bytes", Limit: c.opts.Limits.FrameBytes})
			return
		}
		frame = append(frame, fragment...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			c.mu.Lock()
			closing := c.closing
			c.mu.Unlock()
			if len(frame) > 0 {
				c.fail(&Error{Op: "read", Err: fmt.Errorf("%w: truncated NDJSON frame", ErrProtocol)})
			} else if !closing && !errors.Is(err, io.EOF) {
				c.fail(&Error{Op: "read", Err: err})
			}
			return
		}
		frame = frame[:len(frame)-1]
		if len(frame) > c.opts.Limits.FrameBytes {
			c.fail(&ResourceLimitError{Resource: "incoming frame bytes", Limit: c.opts.Limits.FrameBytes})
			return
		}
		event, err := parseEvent(frame)
		if err != nil {
			c.fail(&Error{Op: "decode", Err: fmt.Errorf("%w: %v", ErrProtocol, err)})
			return
		}
		if err := c.route(event); err != nil {
			if !errors.Is(err, ErrResourceLimit) {
				err = fmt.Errorf("%w: %v", ErrProtocol, err)
			}
			c.fail(err)
			return
		}
		frame = frame[:0]
	}
}

func (c *Client) readStderr() {
	defer close(c.stderrDone)
	buf := make([]byte, 4096)
	for {
		n, err := c.stderr.Read(buf)
		if n > 0 {
			c.mu.Lock()
			c.stderrTail = append(c.stderrTail, buf[:n]...)
			if excess := len(c.stderrTail) - c.opts.Limits.StderrBytes; excess > 0 {
				c.stderrTail = bytes.Clone(c.stderrTail[excess:])
			}
			c.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// One bounded worker prevents incoming replies from blocking the stdout reader
// or spawning an unbounded goroutine per request. Deferred handlers come later.
func (c *Client) respondControls() {
	for {
		select {
		case <-c.done:
			return
		case request := <-c.responses:
			response := map[string]any{"request_id": request.RequestID}
			if request.Subtype == "can_use_tool" {
				response["subtype"] = "success"
				response["response"] = map[string]any{"behavior": "deny", "message": "No permission handler is configured"}
			} else {
				response["subtype"] = "error"
				response["error"] = "Unsupported control request subtype: " + request.Subtype
			}
			if err := c.writeJSON(context.Background(), map[string]any{"type": "control_response", "response": response}); err != nil {
				return
			}
		}
	}
}

func awaitUntil(ch <-chan struct{}, until time.Time) bool {
	timer := time.NewTimer(max(time.Until(until), 0))
	defer timer.Stop()
	select {
	case <-ch:
		return true
	case <-timer.C:
		return false
	}
}

func (c *Client) ownLifecycle() {
	defer close(c.cleaned)
	select {
	case <-c.stop:
	case <-c.stdoutDone:
	case <-c.processDone:
	}
	c.mu.Lock()
	closing := c.closing
	processErr := c.processErr
	c.mu.Unlock()
	if !closing {
		if processErr != nil {
			c.fail(&Error{Op: "process", Err: processErr})
		} else {
			c.fail(ErrUnexpectedEOF)
		}
	} else {
		c.fail(nil)
	}
	_ = c.stdin.Close()
	start := time.Now()
	phase := c.opts.ShutdownTimeout / 3
	// A descendant can keep pipe ends open after the direct child exits; waiting
	// for each completion under one deadline bounds that case too.
	allDone := func(deadline time.Time) bool {
		return awaitUntil(c.processDone, deadline) && awaitUntil(c.stdoutDone, deadline) && awaitUntil(c.stderrDone, deadline)
	}
	if !allDone(start.Add(phase)) {
		_ = terminateProcess(c.cmd, false)
		if !allDone(start.Add(2 * phase)) {
			_ = terminateProcess(c.cmd, true)
			if !allDone(start.Add(c.opts.ShutdownTimeout)) {
				c.mu.Lock()
				c.cleanupErr = ErrShutdownTimeout
				c.mu.Unlock()
			}
		}
	}
	_ = c.stdout.Close()
	_ = c.stderr.Close()
	// File.Close unblocks the pipe readers; do not return while they can mutate
	// accepted-event ordering or the final stderr snapshot.
	<-c.stdoutDone
	<-c.stderrDone
	c.mu.Lock()
	if !c.closing && c.processErr != nil && !errors.Is(c.terminal, c.processErr) {
		c.terminal = errors.Join(c.terminal, &Error{Op: "process", Err: c.processErr})
	}
	c.mu.Unlock()
}
