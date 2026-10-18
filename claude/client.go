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

	"github.com/zoumo/agent-sdk-go/claude/types"
	"github.com/zoumo/agent-sdk-go/internal/queue"
)

// Limits is the callback-free resource configuration shared with types.
type Limits = types.Limits

// Options configures a locally installed CLI. Env, when non-nil, replaces the
// child's entire environment; nil inherits os.Environ. WorkDir, Model and
// ResumeID are passed literally, without a shell. Existing CLI settings apply.
type Options struct {
	CLIPath string
	WorkDir string
	Model   string
	// ResumeID asks a fresh CLI process to resume its native session. It is
	// passed literally; the SDK does not replay messages or restore pending work.
	ResumeID string
	// Subagents bounds automatic relationship observation and verified history.
	Subagents         SubagentOptions
	Env               []string
	InitializeTimeout time.Duration
	ShutdownTimeout   time.Duration
	Limits            Limits
	PermissionHandler PermissionHandler
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
		name string
		v    *int
		def  int
		max  int
	}{
		{"EventBuffer", &o.Limits.EventBuffer, 128, 65536}, {"FrameBytes", &o.Limits.FrameBytes, 4 << 20, 64 << 20},
		{"PendingControls", &o.Limits.PendingControls, 32, 65536}, {"ControlBuffer", &o.Limits.ControlBuffer, 32, 65536},
		{"StderrBytes", &o.Limits.StderrBytes, 8192, 16 << 20}, {"PendingPermissions", &o.Limits.PendingPermissions, 128, 65536},
		{"PermissionWorkers", &o.Limits.PermissionWorkers, 4, 1024}, {"PermissionHistory", &o.Limits.PermissionHistory, 4096, 1 << 20},
	} {
		if *pair.v < 0 || *pair.v > pair.max {
			return fmt.Errorf("claude: %s must be between 0 and %d", pair.name, pair.max)
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

// Client owns one subprocess. Its methods are concurrency-safe except Receive.
// Receive is a single-consumer stream; callers must not call it concurrently.
type Client struct {
	tree                                *managedTree
	opts                                Options
	cmd                                 *exec.Cmd
	stdin, stdout, stderr               *os.File
	events                              queue.Queue[Event]
	responses                           chan incomingControl
	permissionJobs                      chan *PermissionRequest
	writeGate                           chan struct{}
	done                                chan struct{}
	stop                                chan struct{}
	cleaned                             chan struct{}
	stdoutDone, stderrDone, processDone chan struct{}
	stopOnce                            sync.Once
	mu                                  sync.Mutex
	terminal                            error
	closing                             bool
	aborted                             bool
	stopped                             bool
	processErr                          error
	cleanupErr                          error
	stderrTail                          []byte
	sessionID                           string
	pending                             map[string]pendingControl
	nextID                              atomic.Uint64
	epoch                               uint64
	permissions                         map[string]*permissionState
	permissionIDs                       map[string]struct{}
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
	tree, err := newManagedTree(options)
	if err != nil {
		return nil, err
	}
	// History has already been consumed into bounded private state.
	options.Subagents = tree.options
	args := []string{"--output-format", "stream-json", "--verbose", "--input-format", "stream-json", "--permission-prompt-tool", "stdio", "--include-partial-messages"}
	if options.Model != "" {
		args = append(args, "--model="+options.Model)
	}
	if options.ResumeID != "" {
		// The CLI takes an optional resume value. Binding it to the flag also
		// prevents a dash-leading value from becoming a different CLI option.
		args = append(args, "--resume="+options.ResumeID)
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
	c := &Client{tree: tree, opts: options, cmd: cmd, stdin: inputW, stdout: outputR, stderr: errorR,
		responses:      make(chan incomingControl, options.Limits.ControlBuffer),
		permissionJobs: make(chan *PermissionRequest, options.Limits.ControlBuffer), epoch: connectionEpoch.Add(1),
		permissions: make(map[string]*permissionState), permissionIDs: make(map[string]struct{}),
		writeGate: make(chan struct{}, 1), done: make(chan struct{}), stop: make(chan struct{}), cleaned: make(chan struct{}),
		stdoutDone: make(chan struct{}), stderrDone: make(chan struct{}), processDone: make(chan struct{}), pending: make(map[string]pendingControl)}
	tree.client = c
	go c.readStdout()
	go c.readStderr()
	go c.respondControls()
	if options.PermissionHandler != nil {
		for range options.Limits.PermissionWorkers {
			go c.dispatchPermissions()
		}
	}
	go func() { err := cmd.Wait(); c.mu.Lock(); c.processErr = err; c.mu.Unlock(); close(c.processDone) }()
	go c.ownLifecycle()
	initCtx, cancel := context.WithTimeout(ctx, options.InitializeTimeout)
	defer cancel()
	initialize := map[string]any{"subtype": "initialize", "hooks": nil, "forwardSubagentText": true}
	if _, err := c.control(initCtx, "initialize", initialize); err != nil {
		c.fail(&Error{Op: "initialize", Err: err})
		_ = c.Abort()
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
		_ = c.Abort()
		return nil, c.operationError()
	default:
	}
	return c, nil
}

// Send writes one complete user-message frame. Success proves only a completed
// local write. A canceled or failed started write is conservatively classified
// as unknown outcome and terminates this connection; it is never retried.
func (c *Client) Send(ctx context.Context, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// A JSON string cannot be shorter than its original bytes. Reject obviously
	// oversized caller input before encoding; escapes/envelope are checked below.
	if len(text) > c.opts.Limits.FrameBytes {
		return &ResourceLimitError{Resource: "outbound frame bytes", Limit: c.opts.Limits.FrameBytes}
	}
	return c.writeJSON(ctx, map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}, "parent_tool_use_id": nil, "session_id": ""})
}

// RequestedResumeID returns the value supplied in Options.ResumeID. It is a
// request, not evidence that the CLI found or resumed that session.
func (c *Client) RequestedResumeID() string { return c.opts.ResumeID }

// SessionID returns the most recent nonempty native session ID observed in a
// system or result frame. Before such a frame arrives it returns ("", false),
// even when a resume ID was requested. The last observed ID remains available
// after Close. Receive still exposes the original session fields and raw frame.
func (c *Client) SessionID() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionID, c.sessionID != ""
}

// Interrupt asks the CLI to interrupt active work and waits for the matching
// control acknowledgment. Success is not a turn result or proof of tool rollback;
// later results remain available through Receive. If cancellation or disconnect
// occurs after writing, the outcome may be unknown. Nothing is resent or restored.
func (c *Client) Interrupt(ctx context.Context) error {
	_, err := c.control(ctx, "interrupt", map[string]any{"subtype": "interrupt"})
	return err
}

// Receive returns a direct Event, SubagentAssociated, SubagentCreated or
// ObservationState value. The first value reports unverified forwarding. A root result or idle does not
// end observation. Canceling ctx cancels only this wait; call Close to release the process.
// A nil context returns an error without consuming any event.
func (c *Client) Receive(ctx context.Context) (ReceivedEvent, error) {
	if ctx == nil {
		return nil, queue.ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	aborted := c.aborted
	c.mu.Unlock()
	if aborted {
		return nil, c.operationError()
	}
	record, err := c.tree.receive(ctx)
	if err != nil {
		return nil, err
	}
	return record.event(), nil
}

func (c *Client) receiveNative(ctx context.Context) (Event, error) {
	event, err := c.events.Pop(ctx)
	if errors.Is(err, io.EOF) {
		return c.receiveEnd(ctx)
	}
	return event, err
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
// of consumer progress. Already accepted events can still be drained by Receive.
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

// Abort shuts down the process like Close, then discards the unconsumed native
// backlog. Later Receive calls return the terminal error or ErrClosed. A Receive
// already in progress may deliver an event before Abort takes effect.
// Bounded relationship state remains owned by Client until it is collected.
func (c *Client) Abort() error {
	err := c.Close()
	c.mu.Lock()
	c.aborted = true
	c.mu.Unlock()
	_ = c.events.Abort(nil)
	return err
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failLocked(err)
}

func (c *Client) failLocked(err error) {
	if err != nil {
		if c.terminal == nil || c.terminal == ErrUnexpectedEOF {
			c.terminal = err
		} else if (errors.Is(err, ErrProtocol) && !errors.Is(c.terminal, ErrProtocol)) ||
			(errors.Is(err, ErrResourceLimit) && !errors.Is(c.terminal, ErrResourceLimit)) ||
			(errors.Is(err, ErrUnknownOutcome) && !errors.Is(c.terminal, ErrUnknownOutcome)) ||
			(errors.Is(err, ErrPermissionWriteFailed) && !errors.Is(c.terminal, ErrPermissionWriteFailed)) {
			// Shutdown can win the race with a final read or response write.
			// Retain its more specific failure after that operation settles.
			c.terminal = errors.Join(c.terminal, err)
		}
	}
	c.stopped = true
	for id, permission := range c.permissions {
		permission.status = permissionClosed
		cause := err
		if cause == nil {
			cause = ErrClosed
		}
		permission.cancel(cause)
		delete(c.permissions, id)
	}
	c.stopOnce.Do(func() { close(c.done); close(c.stop) })
}

func (c *Client) writeJSON(ctx context.Context, value any) error {
	return c.writeJSONChecked(ctx, value, nil)
}

func (c *Client) writeJSONChecked(ctx context.Context, value any, authorize func() error) error {
	// Serialize encoding as well as writing: concurrent callers do not each
	// retain an SDK-encoded frame while waiting for the writer.
	select {
	case c.writeGate <- struct{}{}:
		defer func() { <-c.writeGate }()
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	frame, err := json.Marshal(value)
	if err != nil {
		return &Error{Op: "encode", Err: err}
	}
	if len(frame) > c.opts.Limits.FrameBytes {
		return &ResourceLimitError{Resource: "outbound frame bytes", Limit: c.opts.Limits.FrameBytes}
	}
	frame = append(frame, '\n')
	if err := ctx.Err(); err != nil {
		return err
	}
	if authorize != nil {
		if err := authorize(); err != nil {
			return err
		}
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id := "go_" + strconv.FormatUint(c.nextID.Add(1), 10)
	waiter := pendingControl{subtype: subtype, reply: make(chan controlReply, 1)}
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return nil, ErrClosed
	default:
	}
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
	if (event.System != nil || event.Result != nil) && event.SessionID != "" {
		c.mu.Lock()
		c.sessionID = event.SessionID
		c.mu.Unlock()
	}
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
	if event.Cancel != nil {
		if err := c.cancelPermission(event.Cancel.RequestID); err != nil {
			return err
		}
	}
	if event.Control != nil {
		permission, err := c.registerIncoming(event.Control)
		if errors.Is(err, ErrClosed) {
			return c.events.Push(event, int64(len(event.Raw)))
		}
		if err != nil {
			return err
		}
		work := incomingControl{subtype: event.Control.Subtype, permission: permission}
		if work.subtype == "can_use_tool" && c.opts.PermissionHandler != nil {
			select {
			case c.permissionJobs <- permission:
			default:
				return &ResourceLimitError{Resource: "permission handler queue", Limit: c.opts.Limits.ControlBuffer}
			}
		} else {
			select {
			case c.responses <- work:
			default:
				return &ResourceLimitError{Resource: "incoming control queue", Limit: c.opts.Limits.ControlBuffer}
			}
		}
	}
	return c.events.Push(event, int64(len(event.Raw)))
}

func (c *Client) readStdout() {
	defer close(c.stdoutDone)
	defer c.events.Close(nil)
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
			if errors.Is(err, ErrClosed) {
				return
			}
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
// or spawning an unbounded goroutine per request. Human handlers have a
// separate bounded pool, so they cannot strand unsupported-control responses.
func (c *Client) respondControls() {
	for {
		select {
		case <-c.done:
			return
		case request := <-c.responses:
			if request.subtype == "can_use_tool" {
				_ = request.permission.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny, Message: "No permission handler is configured"})
				continue
			}
			_ = request.permission.answerUnsupported(context.Background(), request.subtype)
		}
	}
}

func awaitUntil(ch <-chan struct{}, until time.Time) bool {
	select {
	case <-ch:
		return true
	default:
	}
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
	// A same-group descendant can close its inherited pipes yet keep running.
	// Once the direct child has exited, finish any remaining group members even
	// when all pipes were already drained. On other platforms this is a harmless
	// Kill of the already-exited direct child, not a descendant-tree guarantee.
	select {
	case <-c.processDone:
		_ = terminateProcess(c.cmd, true)
	default:
	}
	_ = c.stdout.Close()
	_ = c.stderr.Close()
	// File.Close unblocks the pipe readers; do not return while they can mutate
	// accepted-event ordering or the final stderr snapshot.
	<-c.stdoutDone
	<-c.stderrDone
	// Closing stdin releases an active pipe write. Wait for that serialized
	// writer to record its result before exposing the final diagnostics. Do not
	// wait beyond the same shutdown budget or for arbitrary permission callbacks.
	writerSettled := false
	select {
	case c.writeGate <- struct{}{}:
		writerSettled = true
	default:
		timer := time.NewTimer(max(time.Until(start.Add(c.opts.ShutdownTimeout)), 0))
		select {
		case c.writeGate <- struct{}{}:
			writerSettled = true
		case <-timer.C:
		}
		timer.Stop()
	}
	if writerSettled {
		<-c.writeGate
	} else {
		c.mu.Lock()
		c.cleanupErr = ErrShutdownTimeout
		c.mu.Unlock()
	}
	c.mu.Lock()
	if !c.closing && c.processErr != nil && !errors.Is(c.terminal, c.processErr) {
		c.terminal = errors.Join(c.terminal, &Error{Op: "process", Err: c.processErr})
	}
	c.mu.Unlock()
}
