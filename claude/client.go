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
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zoumo/agent-sdk-go/claude/types"
	"github.com/zoumo/agent-sdk-go/internal/queue"
)

// Limits is the callback-free resource configuration shared with types.
type Limits = types.Limits

// Options configures a locally installed CLI. Env, when non-nil, replaces the
// child's entire environment; nil inherits os.Environ. WorkDir, Model, Effort,
// Tools, DisallowedTools, SessionID and ResumeID are passed without a shell.
// ClaimSession resolves WorkDir to an absolute path. Existing CLI settings apply.
//
//nolint:recvcheck // Canonical configuration API uses value and pointer receivers.
type Options struct {
	CLIPath string
	WorkDir string
	Model   string
	// Effort passes the native creation-only --effort value literally. Empty omits it.
	Effort string
	// AppendSystemPrompt appends text after the CLI's own system instructions
	// through one native --append-system-prompt=<text> argv element. It does
	// not replace provider defaults, and empty omits the option. The text must
	// be valid UTF-8 and contain no NUL. Argument vectors are observable
	// transport data, not secret storage.
	AppendSystemPrompt string
	// SDKMCPServers names application-hosted native SDK MCP servers.
	SDKMCPServers []string
	// MCPServers declares external stdio, HTTP or SSE MCP servers. The SDK
	// copies these values when opening the Session.
	MCPServers []MCPServer
	// StrictMCPConfig adds --strict-mcp-config, asking the CLI to ignore MCP
	// servers from other settings, only when true.
	StrictMCPConfig bool
	// HandleSDKMCP answers application-hosted MCP requests, including requests
	// received before initialization completes. Nil delivers them through Receive.
	HandleSDKMCP func(context.Context, IncomingRequest) error
	Thinking     *types.ThinkingConfig
	Initialize   *types.InitializeOptions
	// SessionStartProbe appends one private SessionStart callback matcher to
	// Initialize.Hooks. Nil leaves initialization unchanged.
	SessionStartProbe *SessionStartProbe
	// SessionID assigns a new native session through --session-id. It is passed
	// literally and is mutually exclusive with ResumeID. Initialize does not echo
	// this identity; later native main-session messages are checked against it.
	SessionID string
	// ClaimSession starts the CLI as a parked spare through --await-claim and
	// binds it to WorkDir with one claim_session request before Open returns. The
	// CLI assigns the native identity and reports it in the claim response, so
	// this path needs neither --session-id nor user input. It requires a CLI that
	// supports --await-claim and is mutually exclusive with SessionID and
	// ResumeID.
	ClaimSession bool
	// ResumeID asks a fresh CLI process to resume its native session. It is
	// passed literally; the SDK does not replay messages or restore pending work.
	ResumeID string
	// Tools is passed as a comma-separated native --tools allowlist. Nil or
	// empty omits the option; one empty string disables ordinary built-in tools.
	Tools []string
	// DisallowedTools is passed as a comma-separated native --disallowedTools
	// option. Nil or empty omits the option.
	DisallowedTools []string
	// ChildDetailPolicy fixes optional Receive filtering for this Session.
	// Empty and preserve retain all current delivery. It does not change source output.
	ChildDetailPolicy ChildDetailPolicy
	// Subagents bounds automatic relationship observation and verified history.
	Subagents         SubagentOptions
	Env               []string
	InitializeTimeout time.Duration
	ShutdownTimeout   time.Duration
	Limits            Limits

	sessionStartProbeCallbackID string
}

func (o *Options) defaults() error {
	if o.SessionID != "" && o.ResumeID != "" {
		return dispatchFailure("open", fmt.Errorf("%w: session ID and resume ID are mutually exclusive", ErrInvalidInput))
	}
	if o.ClaimSession && (o.SessionID != "" || o.ResumeID != "") {
		return dispatchFailure("open", fmt.Errorf("%w: claimed session cannot also assign or resume an identity", ErrInvalidInput))
	}
	if o.ClaimSession {
		workDir, err := filepath.Abs(o.WorkDir)
		if err != nil {
			return dispatchFailure("open", err)
		}
		o.WorkDir = workDir
	}
	switch o.ChildDetailPolicy {
	case "", ChildDetailPreserve, ChildDetailSuppress:
	default:
		return fmt.Errorf("claude: child detail policy %q: %w", o.ChildDetailPolicy, ErrInvalidInput)
	}
	if err := validateInputEncoding(struct {
		Model, Effort, AppendSystemPrompt string
		Tools                             []string
		MCPServers                        []MCPServer
	}{Model: o.Model, Effort: o.Effort, AppendSystemPrompt: o.AppendSystemPrompt, Tools: o.Tools, MCPServers: o.MCPServers}); err != nil {
		return dispatchFailure("open", err)
	}
	if strings.ContainsRune(o.AppendSystemPrompt, '\x00') {
		return dispatchFailure("open", fmt.Errorf("%w: append system prompt contains NUL", ErrInvalidInput))
	}
	if err := validateBuiltinTools(o.Tools); err != nil {
		return dispatchFailure("open", err)
	}
	if err := validateThinkingConfig(o.Thinking); err != nil {
		return dispatchFailure("open", err)
	}
	if err := validateSDKMCPServers(o.SDKMCPServers); err != nil {
		return dispatchFailure("open", err)
	}
	if err := validateMCPServers(o.MCPServers, o.SDKMCPServers); err != nil {
		return dispatchFailure("open", err)
	}
	if err := o.applySessionStartProbe(); err != nil {
		return dispatchFailure("initialize", err)
	}
	if o.Initialize != nil {
		data, err := json.Marshal(o.Initialize)
		if err != nil {
			return dispatchFailure("initialize", err)
		}
		var frozen types.InitializeOptions
		if err := json.Unmarshal(data, &frozen); err != nil {
			return err
		}
		o.Initialize = &frozen
	}
	o.SDKMCPServers = append([]string(nil), o.SDKMCPServers...)
	o.Tools = slices.Clone(o.Tools)
	o.MCPServers = cloneMCPServers(o.MCPServers)
	if o.CLIPath == "" {
		o.CLIPath = "claude"
	}
	if o.InitializeTimeout < 0 || o.ShutdownTimeout < 0 {
		return errors.New("claude: timeouts must not be negative")
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
		{"EventBuffer", &o.Limits.EventBuffer, 128, 65536}, //nolint:staticcheck // deprecated input is still validated
		{"FrameBytes", &o.Limits.FrameBytes, 4 << 20, 64 << 20},
		{"PendingControls", &o.Limits.PendingControls, 32, 65536},
		{"ControlBuffer", &o.Limits.ControlBuffer, 32, 65536},
		{"StderrBytes", &o.Limits.StderrBytes, 8192, 16 << 20},
		{"PendingPermissions", &o.Limits.PendingPermissions, 128, 65536},
		{"PermissionHistory", &o.Limits.PermissionHistory, 4096, 1 << 20},
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

func validateBuiltinTools(tools []string) error {
	for _, tool := range tools {
		if tool == "" && len(tools) != 1 {
			return fmt.Errorf("%w: tools must contain tool names or a single empty string", ErrInvalidInput)
		}
	}
	return nil
}

type controlReply struct {
	data json.RawMessage
	err  error
}
type pendingControl struct {
	subtype      string
	fullResponse bool
	reply        chan controlReply
}

// interaction owns native subprocess, control and writer mechanics.
// Session owns input/receive admission and observation state.
type interaction struct {
	session                             *Session
	opts                                Options
	cmd                                 *exec.Cmd
	stdin, stdout, stderr               *os.File
	streamReader                        io.ReadCloser
	streamWriter                        io.WriteCloser
	streamStderr                        io.ReadCloser
	streamWait                          func(context.Context) error
	streamMode                          bool
	inputMu                             sync.Mutex
	inputClosed                         bool
	inputCloseErr                       error
	inputClosedCh                       chan struct{}
	stdoutEOF                           bool
	responses                           chan incomingControl
	writeGate                           chan struct{}
	done                                chan struct{}
	stop                                chan struct{}
	cleaned                             chan struct{}
	stdoutDone, stderrDone, processDone chan struct{}
	stopOnce                            sync.Once
	mu                                  sync.Mutex
	terminal                            error
	closing                             bool
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
	sessionStartProbeReplySent          atomic.Bool
	claiming                            atomic.Bool
	claimMu                             sync.Mutex
	claimBuffered                       []Event
	claimBufferedBytes                  int
}

func newInteraction(options Options, session *Session) *interaction {
	c := &interaction{
		session: session, opts: options,
		responses:   make(chan incomingControl, options.Limits.ControlBuffer),
		epoch:       connectionEpoch.Add(1),
		permissions: make(map[string]*permissionState), permissionIDs: make(map[string]struct{}),
		writeGate: make(chan struct{}, 1), done: make(chan struct{}), stop: make(chan struct{}), cleaned: make(chan struct{}),
		stdoutDone: make(chan struct{}), stderrDone: make(chan struct{}), processDone: make(chan struct{}), pending: make(map[string]pendingControl),
		inputClosedCh: make(chan struct{}),
	}
	c.claiming.Store(options.ClaimSession)
	return c
}

func openInteraction(ctx context.Context, options Options, session *Session) (*interaction, error) {
	if ctx == nil {
		return nil, queue.ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := options.defaults(); err != nil {
		return nil, err
	}
	args, err := interactionArgs(options)
	if err != nil {
		return nil, &Error{Op: "arguments", Err: err}
	}
	if _, err := CheckCLIVersion(ctx, options); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(context.WithoutCancel(ctx), options.CLIPath, args...)
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
	c := newInteraction(options, session)
	c.cmd, c.stdin, c.stdout, c.stderr = cmd, inputW, outputR, errorR
	if session != nil {
		if session.bindClient(c) {
			// A terminated owner cannot admit records from this process.
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

func (c *interaction) startup(ctx context.Context) error {
	if err := c.initialize(ctx); err != nil {
		return err
	}
	if c.opts.ClaimSession {
		if err := c.claim(ctx); err != nil {
			c.cleanupFailedStartup()
			return c.startupError("claim_session", err)
		}
	}
	return nil
}

func (c *interaction) start() {
	go c.readStdout()
	go c.readStderr()
	go c.respondControls()
	if !c.streamMode {
		go func() {
			err := c.cmd.Wait()
			c.mu.Lock()
			c.processErr = err
			c.mu.Unlock()
			close(c.processDone)
		}()
	}
	go c.ownLifecycle()
}

func (c *interaction) initialize(ctx context.Context) error {
	initCtx, cancel := context.WithTimeout(ctx, c.opts.InitializeTimeout)
	defer cancel()
	forward := true
	initialize := struct {
		Subtype string `json:"subtype"`
		types.InitializeOptions
	}{Subtype: "initialize", InitializeOptions: types.InitializeOptions{ForwardSubagentText: &forward}}
	if c.opts.Initialize != nil {
		initialize.InitializeOptions = *c.opts.Initialize
	}
	if _, err := c.control(initCtx, "initialize", initialize); err != nil {
		c.mu.Lock()
		if !c.closing {
			c.failLocked(&Error{Op: "initialize", Err: err})
		}
		c.mu.Unlock()
		c.cleanupFailedStartup()
		return c.startupError("initialize", err)
	}
	select {
	case <-c.done:
		c.cleanupFailedStartup()
		return c.operationError()
	default:
	}
	return nil
}

func (c *interaction) startupError(op string, err error) error {
	final := c.Err()
	startup := &Error{Op: op, Err: errors.Join(err, final)}
	var diagnostics *Error
	if errors.As(final, &diagnostics) {
		startup.Stderr, startup.ExitCode = diagnostics.Stderr, diagnostics.ExitCode
	}
	startup.OutcomeUnknown = errors.Is(startup.Err, ErrUnknownOutcome)
	return startup
}

// Open aborts its unreturned FIFO after failed startup cleanup.
func (c *interaction) cleanupFailedStartup() { _ = c.Close() }

// Send writes one complete user-message frame. Success proves only a completed
// local write. A canceled or failed started write is conservatively classified
// as unknown outcome and terminates this connection; it is never retried.
func (c *interaction) Send(ctx context.Context, text string) error {
	return c.sendInput(ctx, Input{Text: text})
}

// RequestedResumeID returns the value supplied in Options.ResumeID. It is a
// request, not evidence that the CLI found or resumed that session.
func (c *interaction) RequestedResumeID() string { return c.opts.ResumeID }

// SessionID returns the most recent nonempty native session ID observed in a
// claim response, system or result frame. Before such evidence arrives it returns
// ("", false), even when a resume ID was requested. The last observed ID remains
// available after Close. Receive still exposes the original session fields and raw frame.
func (c *interaction) SessionID() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionID, c.sessionID != ""
}

// Interrupt asks the CLI to interrupt active work and waits for the matching
// control acknowledgment. Success is not a turn result or proof of tool rollback;
// later results remain available through Receive. If cancellation or disconnect
// occurs after writing, the outcome may be unknown. Nothing is resent or restored.
func (c *interaction) Interrupt(ctx context.Context) error {
	_, err := c.control(ctx, "interrupt", map[string]any{"subtype": "interrupt"})
	return err
}

// Done closes as soon as shutdown or a terminal failure is observed, independent
// of consumer progress. Already accepted events can still be drained by Receive.
func (c *interaction) Done() <-chan struct{} { return c.done }

// Err returns the currently known terminal error, with a snapshot of the bounded
// stderr tail. Cleanup can add exit diagnostics; Close waits for that cleanup.
// Err is nil during operation and after a successful explicit Close.
func (c *interaction) Err() error {
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

func (c *interaction) operationError() error {
	if err := c.Err(); err != nil {
		return err
	}
	return ErrClosed
}

// Close is idempotent and concurrency-safe. It closes stdin, gives the child a
// bounded chance to exit, then terminates/kills it as needed. It does not wait
// for the event consumer. It returns terminal or cleanup failures, if any.
func (c *interaction) Close() error {
	c.mu.Lock()
	c.closing = true
	c.mu.Unlock()
	c.fail(nil)
	<-c.cleaned
	return c.Err()
}

// Abort delegates abandoned-backlog cleanup to the sole Session owner.
func (c *interaction) Abort() error { return c.session.Abort() }

func (c *interaction) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failLocked(err)
}

func (c *interaction) failLocked(err error) {
	if err != nil {
		if c.terminal == nil || errors.Is(c.terminal, ErrUnexpectedEOF) {
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
	if c.session != nil {
		c.session.finish(err)
	}
	c.stopOnce.Do(func() { close(c.done); close(c.stop) })
}

func (c *interaction) writeJSON(ctx context.Context, value any) error {
	return c.writeJSONChecked(ctx, value, nil)
}

func (c *interaction) writeJSONChecked(ctx context.Context, value any, authorize func() error) error {
	return c.writeEncodedChecked(ctx, func() ([]byte, error) { return json.Marshal(value) }, authorize)
}

// Admission covers preparation and encoding, not just the final OS write.
func (c *interaction) writeEncodedChecked(ctx context.Context, encode func() ([]byte, error), authorize func() error) error {
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
	if c.streamMode && c.inputIsClosed() {
		return ErrInputClosed
	}
	frame, err := encode()
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
		if c.streamMode {
			_ = c.closeInput()
		} else {
			_ = c.stdin.Close()
		}
		close(canceled)
	})
	n, err := c.outboundWriter().Write(frame)
	if !stopCancel() {
		<-canceled
		return &Error{Op: "write", Err: ctx.Err(), OutcomeUnknown: true}
	}
	if err == nil && n != len(frame) {
		err = io.ErrShortWrite
	}
	inputClosed := c.streamMode && c.inputIsClosed()
	if err != nil && inputClosed {
		err = ErrInputClosed
	}
	if err != nil {
		e := &Error{Op: "write", Err: err, OutcomeUnknown: true}
		if !errors.Is(err, ErrInputClosed) {
			c.fail(e)
		}
		return e
	}
	return nil
}

func (c *interaction) outboundWriter() io.Writer {
	if c.streamMode {
		return c.streamWriter
	}
	return c.stdin
}

func (c *interaction) inputIsClosed() bool {
	c.inputMu.Lock()
	defer c.inputMu.Unlock()
	return c.inputClosed
}

func (c *interaction) closeInput() error {
	c.inputMu.Lock()
	defer c.inputMu.Unlock()
	if c.inputClosed {
		if c.inputCloseErr != nil {
			return c.inputCloseErr
		}
		return ErrInputClosed
	}
	c.inputClosed = true
	if c.streamMode {
		c.inputCloseErr = c.streamWriter.Close()
	} else {
		c.inputCloseErr = c.stdin.Close()
	}
	close(c.inputClosedCh)
	return c.inputCloseErr
}

// CloseInput half-closes the SDK outbound writer while retaining the reader for
// final peer frames. It is idempotent and does not wait for cleanup.
func (c *interaction) CloseInput() error {
	if !c.streamMode {
		return ErrStreamRequired
	}
	return c.closeInput()
}

// control registers its waiter before writing. Keep this shared path for future
// control operations so their correlation and teardown rules stay identical.
func (c *interaction) control(ctx context.Context, subtype string, request any) (json.RawMessage, error) {
	return c.controlRequest(ctx, subtype, request, false)
}

func (c *interaction) controlRequest(ctx context.Context, subtype string, request any, fullResponse bool) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id := "go_" + strconv.FormatUint(c.nextID.Add(1), 10)
	waiter := pendingControl{subtype: subtype, fullResponse: fullResponse, reply: make(chan controlReply, 1)}
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
	if err := c.writeEncodedChecked(ctx, func() ([]byte, error) {
		return json.Marshal(map[string]any{"type": "control_request", "request_id": id, "request": request})
	}, nil); err != nil {
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

func (c *interaction) route(event Event) error {
	if event.Type == "control_response" {
		reply := event.Response.Response
		if reply.RequestID == "" || (reply.Subtype != "success" && reply.Subtype != "error") {
			return errors.New("invalid control response")
		}
		c.mu.Lock()
		waiter, found := c.pending[reply.RequestID]
		if found {
			delete(c.pending, reply.RequestID)
		}
		c.mu.Unlock()
		if found {
			result := controlReply{data: reply.Response}
			if waiter.fullResponse {
				result.data = append(json.RawMessage(nil), event.Raw...)
			}
			if reply.Subtype == "error" {
				result.err = &ControlError{RequestID: reply.RequestID, Subtype: waiter.subtype, Message: reply.Error, Raw: append(json.RawMessage(nil), event.Raw...)}
			}
			waiter.reply <- result
		}
		return nil
	}
	if c.claiming.Load() {
		if event.Control != nil && c.ownsSDKMCPRequest(event.Control) && c.opts.HandleSDKMCP != nil {
			return c.routeNative(event)
		}
		c.claimMu.Lock()
		if c.claiming.Load() {
			err := c.bufferClaimedFrame(event)
			c.claimMu.Unlock()
			return err
		}
		c.claimMu.Unlock()
	}
	return c.routeNative(event)
}

// routeNative processes native control authority and admits Session records.
func (c *interaction) routeNative(event Event) error {
	if (event.System != nil || event.Result != nil) && event.SessionID != "" {
		c.mu.Lock()
		c.sessionID = event.SessionID
		c.mu.Unlock()
	}
	if event.Cancel != nil {
		if err := c.cancelPermission(event.Cancel.RequestID); err != nil {
			return err
		}
	}
	if event.Control != nil {
		if requestID, ok := c.ownsSessionStartProbe(event.Control); ok {
			if !c.sessionStartProbeReplySent.Swap(true) {
				work := incomingControl{probeRequestID: requestID}
				select {
				case c.responses <- work:
				default:
					return &ResourceLimitError{Resource: "incoming control queue", Limit: c.opts.Limits.ControlBuffer}
				}
			}
			return c.acceptEvent(event)
		}
		permission, err := c.registerIncoming(event.Control)
		if errors.Is(err, ErrClosed) {
			return c.acceptEvent(event)
		}
		if err != nil {
			return err
		}
		if c.ownsSDKMCPRequest(event.Control) && c.opts.HandleSDKMCP != nil {
			work := incomingControl{
				subtype: event.Control.Subtype, permission: permission,
				request: &IncomingRequest{permission: permission, method: event.Control.Subtype, raw: bytes.Clone(event.Raw)},
			}
			select {
			case c.responses <- work:
			default:
				return &ResourceLimitError{Resource: "incoming control queue", Limit: c.opts.Limits.ControlBuffer}
			}
			return nil
		}
		if c.session != nil && (event.Control.Subtype == "can_use_tool" || c.ownsSDKMCPRequest(event.Control)) {
			return c.session.acceptRequest(event, permission)
		}
		work := incomingControl{subtype: event.Control.Subtype, permission: permission}
		select {
		case c.responses <- work:
		default:
			return &ResourceLimitError{Resource: "incoming control queue", Limit: c.opts.Limits.ControlBuffer}
		}
	}
	return c.acceptEvent(event)
}

func (c *interaction) readStdout() {
	defer close(c.stdoutDone)
	input := io.Reader(c.stdout)
	if c.streamMode {
		input = c.streamReader
	}
	reader := bufio.NewReaderSize(input, min(c.opts.Limits.FrameBytes+1, 32<<10))
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
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			c.mu.Lock()
			closing := c.closing
			stopped := c.stopped
			if c.streamMode && errors.Is(err, io.EOF) {
				c.stdoutEOF = true
			}
			c.mu.Unlock()
			if len(frame) > 0 {
				c.fail(&Error{Op: "read", Err: fmt.Errorf("%w: truncated NDJSON frame", ErrProtocol)})
			} else if c.streamMode {
				switch {
				case closing || stopped:
				case errors.Is(err, io.EOF):
					if !c.inputIsClosed() {
						c.fail(&Error{Op: "read", Err: fmt.Errorf("%w: peer closed before SDK input", ErrUnexpectedEOF)})
					}
				default:
					c.fail(&Error{Op: "read", Err: err})
				}
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
			c.fail(&Error{Op: "decode", Err: fmt.Errorf("%w: %w", ErrProtocol, err), Raw: bytes.Clone(frame)})
			return
		}
		if err := c.route(event); err != nil {
			if errors.Is(err, ErrClosed) {
				return
			}
			if !errors.Is(err, ErrResourceLimit) {
				err = fmt.Errorf("%w: %w", ErrProtocol, err)
			}
			c.fail(&Error{Op: "route", Err: err, Raw: bytes.Clone(frame)})
			return
		}
		frame = frame[:0]
	}
}

func (c *interaction) readStderr() {
	defer close(c.stderrDone)
	reader := io.Reader(c.stderr)
	if c.streamMode {
		if c.streamStderr == nil {
			return
		}
		reader = c.streamStderr
	}
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
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

// One bounded protocol responder handles unsupported controls independently of
// Session consumption. Real permission requests wait for their original handles.
func (c *interaction) respondControls() {
	for {
		select {
		case <-c.done:
			return
		case request := <-c.responses:
			if request.request != nil {
				if err := c.opts.HandleSDKMCP(request.permission.Context(), *request.request); err != nil {
					c.fail(&Error{Op: "SDK MCP handler", Err: err})
				}
				continue
			}
			if request.probeRequestID != "" {
				_ = c.answerSessionStartProbe(request.probeRequestID)
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

func (c *interaction) ownLifecycle() {
	defer close(c.cleaned)
	defer func() {
		if c.session != nil {
			c.session.finish(c.Err())
		}
	}()
	if c.streamMode {
		c.ownStreamLifecycle()
		return
	}
	select {
	case <-c.stop:
	case <-c.stdoutDone:
	case <-c.processDone:
	}
	start := time.Now()
	phase := c.opts.ShutdownTimeout / 3
	// Process exit does not prove stdout was consumed. Let the reader admit
	// the final native frames before sealing the Session FIFO. A descendant
	// may retain the pipe, so this uses the existing first shutdown phase.
	select {
	case <-c.processDone:
		select {
		case <-c.stop:
		default:
			_ = awaitUntil(c.stdoutDone, start.Add(phase))
		}
	default:
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
	c.settleWriter(start)
	c.mu.Lock()
	if !c.closing && c.processErr != nil && !errors.Is(c.terminal, c.processErr) {
		c.terminal = errors.Join(c.terminal, &Error{Op: "process", Err: c.processErr})
	}
	c.mu.Unlock()
}

func (c *interaction) ownStreamLifecycle() {
	select {
	case <-c.stop:
	case <-c.stdoutDone:
	case <-c.inputClosedCh:
	}
	// The shutdown budget begins at termination, not at Session startup.
	start := time.Now()

	if !c.inputIsClosed() {
		_ = c.closeInput()
	}
	var waitErr error
	if c.streamWait != nil {
		waitCtx, cancel := context.WithDeadline(context.Background(), start.Add(c.opts.ShutdownTimeout))
		waitErr = c.streamWait(waitCtx)
		cancel()
	}

	if waitErr != nil {
		// Close stops local work; it does not turn a failed peer completion into
		// success. failLocked retains an earlier specific protocol cause while
		// replacing a generic premature-EOF classification.
		c.fail(&Error{Op: "wait", Err: waitErr})
	}
	if waitErr != nil {
		_ = c.streamReader.Close()
	}

	readComplete := awaitUntil(c.stdoutDone, start.Add(c.opts.ShutdownTimeout))
	if !readComplete {
		c.mu.Lock()
		closing := c.closing
		terminal := c.terminal
		c.mu.Unlock()
		if !closing && terminal == nil {
			c.fail(&Error{Op: "read", Err: fmt.Errorf("%w: peer did not close its output", ErrUnexpectedEOF)})
		}
		_ = c.streamReader.Close()
		<-c.stdoutDone
		readComplete = true
	}
	_ = c.streamReader.Close()
	if c.streamStderr != nil {
		_ = c.streamStderr.Close()
	}
	<-c.stderrDone
	c.settleWriter(start)

	c.mu.Lock()
	closing := c.closing
	terminal := c.terminal
	readEOF := c.stdoutEOF
	c.mu.Unlock()
	if terminal == nil && waitErr == nil && !closing {
		switch {
		case !c.inputIsClosed():
			c.fail(ErrShutdownTimeout)
		case !readEOF || !readComplete:
			c.fail(&Error{Op: "read", Err: fmt.Errorf("%w: peer ended without a complete output", ErrUnexpectedEOF)})
		default:
			c.fail(nil)
		}
	}
}

// Closing the outbound side releases an active write. Wait for that serialized
// writer to record its result before exposing final diagnostics, without
// exceeding the existing shutdown budget or waiting for arbitrary callbacks.
func (c *interaction) settleWriter(start time.Time) {
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
		return
	}
	c.mu.Lock()
	c.cleanupErr = ErrShutdownTimeout
	c.mu.Unlock()
}

func interactionArgs(options Options) ([]string, error) {
	args := []string{"--output-format", "stream-json", "--verbose", "--input-format", "stream-json", "--permission-prompt-tool", "stdio", "--include-partial-messages"}
	args = append(args, thinkingArgs(options.Thinking)...)
	if options.Model != "" {
		args = append(args, "--model="+options.Model)
	}
	if len(options.SDKMCPServers) > 0 || len(options.MCPServers) > 0 {
		servers := map[string]any{}
		for _, name := range options.SDKMCPServers {
			servers[name] = map[string]string{"type": "sdk", "name": name}
		}
		for _, server := range options.MCPServers {
			servers[server.Name] = nativeMCPServerConfig(server)
		}
		config, err := json.Marshal(map[string]any{"mcpServers": servers})
		if err != nil {
			return nil, err
		}
		args = append(args, "--mcp-config="+string(config))
	}
	if options.StrictMCPConfig {
		args = append(args, "--strict-mcp-config")
	}
	if options.Effort != "" {
		args = append(args, "--effort="+options.Effort)
	}
	if options.AppendSystemPrompt != "" {
		args = append(args, "--append-system-prompt="+options.AppendSystemPrompt)
	}
	if len(options.Tools) > 0 {
		args = append(args, "--tools="+strings.Join(options.Tools, ","))
	}
	if len(options.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools="+strings.Join(options.DisallowedTools, ","))
	}
	if options.ResumeID != "" {
		// The CLI takes an optional resume value. Binding it to the flag also
		// prevents a dash-leading value from becoming a different CLI option.
		args = append(args, "--resume="+options.ResumeID)
	}
	if options.SessionID != "" {
		args = append(args, "--session-id="+options.SessionID)
	}
	if options.ClaimSession {
		args = append(args, "--await-claim")
	}
	return args, nil
}
