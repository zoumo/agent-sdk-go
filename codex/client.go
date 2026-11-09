// Package codex provides native long-running Sessions over Codex app-server.
// Open fixes a main identity. Session.Receive is the single FIFO for
// native events, real IncomingRequests and observation evidence.
// Submit retains native turn semantics; results do not close the Session.
// Client is the explicit shared I/O resource and raw extension seam. Native
// request handles retain single-use authority. See README.md for the
// pinned source target and evidence limits.
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

type pendingCall struct {
	id       ID
	method   string
	ctx      context.Context
	started  bool
	writing  bool
	done     chan struct{}
	result   json.RawMessage
	err      error
	finished bool
	accept   func(json.RawMessage) error
	owner    *Session
}
type outbound struct {
	frame   []byte
	call    *pendingCall
	answer  *answerAttempt
	ctx     context.Context
	written chan error
}

// Client owns one app-server connection and, for stdio only, its child process.
// It never reconnects or replays requests. Calls are
// safe concurrently; a context only cancels local waiting, never server execution.
type Client struct {
	mu                 sync.Mutex
	limits             Limits
	cmd                *exec.Cmd
	stdin              *os.File
	stdout             *os.File
	stderr             *os.File
	streamReader       io.ReadCloser
	streamWriter       io.WriteCloser
	streamStderr       io.ReadCloser
	streamWait         func(context.Context) error
	streamMode         bool
	inputMu            sync.Mutex
	inputClosed        bool
	inputCloseErr      error
	inputClosedCh      chan struct{}
	readEOF            bool
	terminalClosed     bool
	closing            bool
	remote             *websocket.Conn
	remoteToken        string
	queue              chan outbound
	pending            map[ID]*pendingCall
	reverse            map[ID]*requestState
	requestHistory     map[requestKey]*requestState
	ownership          sessionOwnership
	quarantine         []UnattributedRecord
	quarantineBytes    int
	quarantineSequence uint64
	quarantineEvicted  uint64
	quarantineExpired  uint64
	quarantineTimer    *time.Timer
	nextID             int64
	ready              bool
	experimentalAPI    bool
	cause              error
	done               chan struct{}
	cleaned            chan struct{}
	reaped             chan struct{}
	readDone           chan struct{}
	closeErr           error
	serverInfo         json.RawMessage
	diag               DiagnosticSnapshot
	workers            sync.WaitGroup
}

// NewStdio starts the explicitly selected executable, completes initialize then
// writes initialized before returning. The context bounds construction only.
func NewStdio(ctx context.Context, options StdioOptions) (*Client, error) {
	if ctx == nil {
		return nil, errors.New("codex: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.Executable == "" {
		return nil, errors.New("codex: executable is required")
	}
	limits, err := options.Limits.normalized()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(context.WithoutCancel(ctx), options.Executable, options.Args...)
	cmd.Env = options.Env
	cmd.Dir = options.Dir
	// Own our pipe endpoints explicitly: Cmd.Wait must not close stdout before the
	// reader has consumed a final buffered response.
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		outR.Close()
		outW.Close()
		return nil, err
	}
	all := []*os.File{inR, inW, outR, outW, errR, errW}
	cmd.Stdin = inR
	cmd.Stdout = outW
	cmd.Stderr = errW
	if err = cmd.Start(); err != nil {
		for _, f := range all {
			f.Close()
		}
		return nil, err
	}
	inR.Close()
	outW.Close()
	errW.Close()
	c := newClient(limits)
	c.cmd, c.stdin, c.stdout, c.stderr = cmd, inW, outR, errR
	go func() {
		waitErr := cmd.Wait()
		close(c.reaped)
		// Allow the reader to consume a final response, but do not depend on EOF:
		// a descendant may retain inherited stdout after the owned child exits.
		drain := min(limits.ShutdownTimeout/2, 100*time.Millisecond)
		timer := time.NewTimer(drain)
		defer timer.Stop()
		select {
		case <-c.readDone:
		case <-timer.C:
			c.fail(fmt.Errorf("codex: owned child exited: %w", errors.Join(io.EOF, waitErr)))
		}
	}()
	c.workers.Add(3)
	go func() { defer c.workers.Done(); defer close(c.readDone); c.readLoop() }()
	go func() { defer c.workers.Done(); c.writeLoop() }()
	go func() { defer c.workers.Done(); c.drainStderr() }()
	go c.cleanup()
	return c.initialize(ctx, options.Initialize)
}

func newClient(limits Limits) *Client {
	c := &Client{limits: limits, queue: make(chan outbound, limits.OutboundQueue), pending: make(map[ID]*pendingCall), reverse: make(map[ID]*requestState), requestHistory: make(map[requestKey]*requestState), done: make(chan struct{}), cleaned: make(chan struct{}), reaped: make(chan struct{}), readDone: make(chan struct{}), inputClosedCh: make(chan struct{})}
	c.ownership.client = c
	return c
}

func (c *Client) initialize(ctx context.Context, params types.InitializeParams) (*Client, error) {
	capabilities, _ := params.Capabilities.Get()
	c.experimentalAPI = capabilities.ExperimentalAPI.ValueOr(false)
	var info types.InitializeResponse
	err := c.call(ctx, "initialize", params, &info, true)
	if err != nil {
		c.fail(err)
		_ = c.Close()
		return nil, err
	}
	// Keep immutable raw server info: caller mutations cannot race the client.
	c.serverInfo, err = json.Marshal(info)
	if err == nil {
		err = c.notify(ctx, "initialized")
	}
	if err != nil {
		c.fail(err)
		_ = c.Close()
		return nil, err
	}
	c.mu.Lock()
	if c.cause != nil {
		err = c.cause
	} else {
		c.ready = true
	}
	c.mu.Unlock()
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) ServerInfo() types.InitializeResponse {
	var v types.InitializeResponse
	_ = json.Unmarshal(c.serverInfo, &v)
	return v
}

func (c *Client) StartThread(ctx context.Context, p types.ThreadStartParams) (types.ThreadStartResponse, error) {
	var r types.ThreadStartResponse
	err := c.Call(ctx, "thread/start", p, &r)
	return r, err
}

// ListThreads returns one native thread/list page. It does not create a Session,
// attach threads, or fetch further pages. Defaults and filters belong to the server.
func (c *Client) ListThreads(ctx context.Context, p types.ThreadListParams) (types.ThreadListResponse, error) {
	var r types.ThreadListResponse
	err := c.Call(ctx, "thread/list", p, &r)
	return r, err
}

// ReadThread reads native metadata (prefer IncludeTurns: types.Some(false)).
// It does not resume or subscribe, even when this connection owns a Session.
func (c *Client) ReadThread(ctx context.Context, p types.ThreadReadParams) (types.ThreadReadResponse, error) {
	var r types.ThreadReadResponse
	err := c.Call(ctx, "thread/read", p, &r)
	return r, err
}

// StartTurn returns only the native response. Events, including early or stale
// completions, never change its result. Open a Session before starting a turn to retain its native facts.
func (c *Client) StartTurn(ctx context.Context, p types.TurnStartParams) (types.TurnStartResponse, error) {
	var r types.TurnStartResponse
	err := c.ownership.call(ctx, p.ThreadID, "turn/start", p, &r)
	return r, err
}

// InterruptTurn explicitly requests interruption of the caller's native turn.
// A successful acknowledgement is not evidence that execution has completed.
func (c *Client) InterruptTurn(ctx context.Context, p types.TurnInterruptParams) (types.TurnInterruptResponse, error) {
	var r types.TurnInterruptResponse
	err := c.Call(ctx, "turn/interrupt", p, &r)
	return r, err
}

// Call invokes a raw protocol extension with the same bounds and no-replay rules
// as typed operations. nil params omits params; json.RawMessage("null") sends null.
// A nil result discards the result. Raw calls do not certify method support.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	return c.call(ctx, method, params, result, false)
}

func (c *Client) call(ctx context.Context, method string, params, result any, initializing bool) error {
	return c.callAccept(ctx, method, params, result, initializing, nil)
}

func (c *Client) callAccept(ctx context.Context, method string, params, result any, initializing bool, accept func(json.RawMessage) error) error {
	return c.callOwned(ctx, method, params, result, initializing, accept, nil)
}

func (c *Client) callOwned(ctx context.Context, method string, params, result any, initializing bool, accept func(json.RawMessage) error, owner *Session) error {
	fail := func(id ID, dispatch DispatchState, err error) error {
		return c.safeError(&CallError{Method: method, ID: id, Dispatch: dispatch, Cause: err})
	}
	if ctx == nil {
		return fail(ID{}, NotSent, errors.New("codex: nil context"))
	}
	ctx, cancel := context.WithTimeout(ctx, c.limits.CallTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return fail(ID{}, NotSent, err)
	}
	if method == "" || (!initializing && (method == "initialize" || method == "initialized")) {
		return fail(ID{}, NotSent, fmt.Errorf("%w: reserved or empty method", ErrProtocol))
	}
	var raw json.RawMessage
	if params != nil {
		var err error
		raw, err = json.Marshal(params)
		if err != nil {
			return fail(ID{}, NotSent, err)
		}
	}
	c.mu.Lock()
	if c.cause != nil {
		err := c.cause
		c.mu.Unlock()
		return fail(ID{}, NotSent, err)
	}
	if c.streamMode && c.inputIsClosed() {
		c.mu.Unlock()
		return fail(ID{}, NotSent, ErrInputClosed)
	}
	if owner != nil && owner.closed {
		c.mu.Unlock()
		return fail(ID{}, NotSent, ErrClosed)
	}
	if !initializing && !c.ready {
		c.mu.Unlock()
		return fail(ID{}, NotSent, fmt.Errorf("%w: not initialized", ErrProtocol))
	}
	if len(c.pending) >= c.limits.PendingCalls || c.nextID == math.MaxInt64 {
		c.mu.Unlock()
		return fail(ID{}, NotSent, ErrLimit)
	}
	c.nextID++
	id := IntegerID(c.nextID)
	frame, err := json.Marshal(struct {
		ID     ID              `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params,omitempty"`
	}{id, method, raw})
	if err != nil {
		c.mu.Unlock()
		return fail(id, NotSent, err)
	}
	if len(frame) > c.limits.MaxFrameBytes {
		c.mu.Unlock()
		return fail(id, NotSent, ErrFrameTooLarge)
	}
	if err = ctx.Err(); err != nil {
		c.mu.Unlock()
		return fail(id, NotSent, err)
	}
	p := &pendingCall{id: id, method: method, ctx: ctx, done: make(chan struct{}), accept: accept, owner: owner}
	c.pending[id] = p
	select {
	case c.queue <- outbound{frame: frame, call: p, ctx: ctx}:
	default:
		delete(c.pending, id)
		c.mu.Unlock()
		return fail(id, NotSent, ErrLimit)
	}
	c.mu.Unlock()
	select {
	case <-p.done:
	case <-ctx.Done():
		c.mu.Lock()
		writing := false
		if !p.finished {
			writing = p.writing
			delete(c.pending, id)
			dispatch := NotSent
			if p.started {
				dispatch = OutcomeUnknown
			}
			p.err = fail(id, dispatch, ctx.Err())
			p.finished = true
			close(p.done)
		}
		c.mu.Unlock()
		// A canceled blocked write cannot retain an unbounded writer or later send
		// bytes after a caller was told it was not sent. Other active calls fail too.
		if writing {
			c.fail(ctx.Err())
		}
	}
	if p.err != nil {
		return p.err
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(p.result, result); err != nil {
		return fail(id, Responded, fmt.Errorf("%w: decode %s result: %w", ErrProtocol, method, err))
	}
	return nil
}

func (c *Client) notify(ctx context.Context, method string) error {
	ctx, cancel := context.WithTimeout(ctx, c.limits.CallTimeout)
	defer cancel()
	frame, err := json.Marshal(struct {
		Method string `json:"method"`
	}{method})
	if err != nil {
		return err
	}
	written := make(chan error, 1)
	select {
	case <-c.done:
		return c.Err()
	case <-ctx.Done():
		return ctx.Err()
	case c.queue <- outbound{frame: frame, ctx: ctx, written: written}:
	}
	select {
	case err := <-written:
		return err
	case <-c.done:
		return c.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case m := <-c.queue:
			c.mu.Lock()
			if c.cause != nil {
				c.mu.Unlock()
				return
			}
			if c.streamMode && c.inputIsClosed() {
				if m.answer != nil {
					m.answer.finishLocked(ErrInputClosed)
				}
				if m.call != nil {
					c.finishLocked(m.call, nil, &CallError{Method: m.call.method, ID: m.call.id, Dispatch: NotSent, Cause: ErrInputClosed})
				}
				c.mu.Unlock()
				if m.written != nil {
					m.written <- ErrInputClosed
				}
				continue
			}
			if err := m.ctx.Err(); err != nil {
				if m.answer != nil {
					m.answer.finishLocked(err)
				}
				if m.call != nil {
					c.finishLocked(m.call, nil, &CallError{Method: m.call.method, ID: m.call.id, Dispatch: NotSent, Cause: err})
				}
				c.mu.Unlock()
				if m.written != nil {
					m.written <- err
				}
				continue
			}
			if m.answer != nil {
				if m.answer.finished {
					c.mu.Unlock()
					continue
				}
				m.answer.started = true
				m.answer.writing = true
			}
			if m.call != nil {
				if m.call.finished {
					c.mu.Unlock()
					continue
				}
				m.call.started = true
				m.call.writing = true
			}
			c.mu.Unlock()
			var finished atomic.Bool
			timer := time.AfterFunc(c.limits.WriteTimeout, func() {
				if finished.CompareAndSwap(false, true) {
					c.fail(ErrWriteTimeout)
				}
			})
			err := c.writeFrame(m.ctx, m.frame)
			finished.Store(true)
			timer.Stop()
			if err != nil && c.streamMode && c.inputIsClosed() {
				err = ErrInputClosed
			}
			c.mu.Lock()
			if m.answer != nil {
				m.answer.writing = false
				m.answer.finishLocked(err)
			}
			if m.call != nil {
				m.call.writing = false
				if errors.Is(err, ErrInputClosed) {
					dispatch := NotSent
					if m.call.started {
						dispatch = OutcomeUnknown
					}
					c.finishLocked(m.call, nil, &CallError{Method: m.call.method, ID: m.call.id, Dispatch: dispatch, Cause: ErrInputClosed})
				}
			}
			c.mu.Unlock()
			if m.written != nil {
				m.written <- err
			}
			if err != nil && !errors.Is(err, ErrInputClosed) {
				c.fail(err)
				return
			}
		}
	}
}

func (c *Client) finishLocked(p *pendingCall, result json.RawMessage, err error) {
	if p.finished {
		return
	}
	delete(c.pending, p.id)
	p.result = result
	p.err = c.safeError(err)
	p.finished = true
	close(p.done)
}

func (c *Client) fail(cause error) {
	cause = c.safeError(cause)
	if cause == nil {
		cause = ErrClosed
	}
	c.mu.Lock()
	if c.cause != nil || c.terminalClosed {
		c.mu.Unlock()
		return
	}
	c.cause = cause
	c.terminalClosed = true
	for _, p := range c.pending {
		state := NotSent
		if p.started {
			state = OutcomeUnknown
		}
		c.finishLocked(p, nil, &CallError{Method: p.method, ID: p.id, Dispatch: state, Cause: cause})
	}
	for _, r := range c.reverse {
		if r.attempt != nil {
			r.attempt.finishLocked(cause)
		} else {
			r.finishLocked(cause)
		}
	}
	for s := range c.ownership.sessionsLocked() {
		terminal := cause
		if errors.Is(cause, ErrClosed) {
			terminal = nil
		}
		s.finishLocked(terminal)
	}
	c.requestHistory = nil
	close(c.done)
	c.mu.Unlock()
	if c.remote != nil {
		_ = c.remote.CloseNow()
	} else if c.streamMode {
		_ = c.closeInput()
	} else {
		_ = c.stdin.Close()
	}
}

func (c *Client) cleanup() {
	if c.streamMode {
		c.cleanupStream()
		return
	}
	<-c.done
	if c.remote != nil {
		c.finishCleanup()
		return
	}
	deadline := time.NewTimer(c.limits.ShutdownTimeout)
	defer deadline.Stop()
	grace := time.NewTimer(c.limits.ShutdownTimeout / 2)
	defer grace.Stop()
	select {
	case <-c.reaped:
	case <-grace.C:
		_ = c.cmd.Process.Kill()
		select {
		case <-c.reaped:
		case <-deadline.C:
			c.mu.Lock()
			c.closeErr = ErrShutdownTimeout
			c.mu.Unlock()
		}
	}
	_ = c.stdout.Close()
	_ = c.stderr.Close()
	c.finishCleanup()
}

func (c *Client) finishCleanup() {
	c.workers.Wait()
	// Release queued frame payloads after the writer has exited.
	for {
		select {
		case <-c.queue:
		default:
			close(c.cleaned)
			return
		}
	}
}

func (c *Client) cleanupStream() {
	select {
	case <-c.inputClosedCh:
	case <-c.done:
	case <-c.readDone:
	}
	deadline := time.Now().Add(c.limits.ShutdownTimeout)
	_ = c.closeInput()

	var waitErr error
	if c.streamWait != nil {
		waitCtx, cancel := context.WithDeadline(context.Background(), deadline)
		waitErr = c.streamWait(waitCtx)
		cancel()
		if waitErr != nil {
			// Close is a request to stop this connection, not permission to hide
			// the peer's authoritative completion failure. Replace only a generic
			// local-close or premature-EOF cause; a specific terminal cause wins.
			c.authoritativeStreamFailure(waitErr)
			_ = c.streamReader.Close()
		}
	}

	readComplete := awaitUntil(c.readDone, deadline)
	_ = c.closeInput()
	if !readComplete {
		_ = c.streamReader.Close()
		readComplete = awaitUntil(c.readDone, time.Now().Add(c.limits.ShutdownTimeout))
	}
	_ = c.streamReader.Close()
	if c.streamStderr != nil {
		_ = c.streamStderr.Close()
	}

	c.mu.Lock()
	terminal := c.terminalClosed
	closing := c.closing
	readEOF := c.readEOF
	c.mu.Unlock()
	inputClosed := c.inputIsClosed()
	switch {
	case terminal:
	case waitErr != nil:
		c.fail(waitErr)
	case !inputClosed:
		c.fail(ErrShutdownTimeout)
	case !readEOF || !readComplete:
		if !closing {
			c.fail(fmt.Errorf("%w: peer ended without an input-acknowledged EOF", ErrUnexpectedEOF))
		}
	default:
		c.completeStream()
	}

	c.mu.Lock()
	if c.closeErr == nil && c.cause != nil && !errors.Is(c.cause, ErrClosed) {
		c.closeErr = c.cause
	}
	c.mu.Unlock()
	c.finishCleanup()
}

func (c *Client) authoritativeStreamFailure(cause error) {
	c.mu.Lock()
	terminal := c.terminalClosed
	generic := c.cause == nil || errors.Is(c.cause, ErrClosed) || errors.Is(c.cause, ErrUnexpectedEOF)
	if terminal && generic {
		c.cause = c.safeError(cause)
	}
	c.mu.Unlock()
	if !terminal {
		c.fail(cause)
	}
}

func (c *Client) completeStream() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminalClosed {
		return
	}
	c.terminalClosed = true
	c.cause = nil
	for session := range c.ownership.sessionsLocked() {
		session.finishLocked(nil)
	}
	close(c.done)
}

func (c *Client) inputIsClosed() bool {
	c.inputMu.Lock()
	defer c.inputMu.Unlock()
	return c.inputClosed
}

func (c *Client) closeInput() error {
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
// final peer frames. It is intended for stream transports.
func (c *Client) CloseInput() error {
	if !c.streamMode {
		return ErrStreamRequired
	}
	return c.closeInput()
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

// Close is idempotent and bounded. A remote client closes only its socket. A
// stdio client closes stdin, allows a bounded grace period, then kills only the
// child it started and waits for reaping. It never sends a protocol shutdown RPC
// or kills an unrelated process, process group, or remote server.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.streamMode {
		c.closing = true
	}
	terminal := c.terminalClosed
	c.mu.Unlock()
	if !terminal {
		c.fail(ErrClosed)
	}
	<-c.cleaned
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeErr
}
func (c *Client) Done() <-chan struct{}           { return c.done }
func (c *Client) Err() error                      { c.mu.Lock(); defer c.mu.Unlock(); return c.cause }
func (c *Client) Diagnostics() DiagnosticSnapshot { c.mu.Lock(); defer c.mu.Unlock(); return c.diag }
func saturatingAdd(a, b uint64) uint64 {
	if math.MaxUint64-a < b {
		return math.MaxUint64
	}
	return a + b
}

func (c *Client) drainStderr() {
	reader := io.Reader(c.stderr)
	if c.streamMode {
		reader = c.streamStderr
	}
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			c.mu.Lock()
			c.diag.StderrBytes = saturatingAdd(c.diag.StderrBytes, uint64(n))
			c.diag.Tail += "[stderr redacted]\n"
			if len(c.diag.Tail) > c.limits.DiagnosticBytes {
				c.diag.Tail = c.diag.Tail[len(c.diag.Tail)-c.limits.DiagnosticBytes:]
			}
			c.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}
