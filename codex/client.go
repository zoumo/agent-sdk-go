// Package codex provides native long-running Sessions over Codex app-server.
// Open or Load fixes a main identity. Session.Receive is the single FIFO for
// native events, real IncomingRequests, observation evidence and Load boundaries.
// Submit retains native turn semantics; results do not close the Session.
// Client is the explicit shared I/O resource and raw extension seam. Native
// request handles retain single-use authority. See docs/compatibility.md for the
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
	mu                  sync.Mutex
	limits              Limits
	cmd                 *exec.Cmd
	stdin               *os.File
	stdout              *os.File
	stderr              *os.File
	remote              *websocket.Conn
	remoteToken         string
	queue               chan outbound
	pending             map[ID]*pendingCall
	reverse             map[ID]*requestState
	requestHistory      map[requestKey]*requestState
	requests            chan Request
	subscriptions       map[*Subscription]struct{}
	attachments         map[string]*sessionAttachment
	sessions            map[string]*Session
	sessionReservations int
	quarantine          []UnattributedRecord
	quarantineBytes     int
	quarantineSequence  uint64
	quarantineEvicted   uint64
	quarantineExpired   uint64
	quarantineTimer     *time.Timer
	threadOperations    map[string]int
	nextID              int64
	ready               bool
	experimentalAPI     bool
	cause               error
	done                chan struct{}
	cleaned             chan struct{}
	reaped              chan struct{}
	readDone            chan struct{}
	closeErr            error
	serverInfo          json.RawMessage
	diag                DiagnosticSnapshot
	workers             sync.WaitGroup
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
	cmd := exec.Command(options.Executable, options.Args...)
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
		drain := 100 * time.Millisecond
		if limits.ShutdownTimeout/2 < drain {
			drain = limits.ShutdownTimeout / 2
		}
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
	return &Client{limits: limits, queue: make(chan outbound, limits.OutboundQueue), pending: make(map[ID]*pendingCall), reverse: make(map[ID]*requestState), requestHistory: make(map[requestKey]*requestState), requests: make(chan Request, limits.RequestQueue), subscriptions: make(map[*Subscription]struct{}), done: make(chan struct{}), cleaned: make(chan struct{}), reaped: make(chan struct{}), readDone: make(chan struct{})}
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
func (c *Client) ReadThread(ctx context.Context, p types.ThreadReadParams) (types.ThreadReadResponse, error) {
	var r types.ThreadReadResponse
	err := c.Call(ctx, "thread/read", p, &r)
	return r, err
}

// StartTurn returns only the native response. Events, including early or stale
// completions, never change its result. Subscribe before starting a turn.
func (c *Client) StartTurn(ctx context.Context, p types.TurnStartParams) (types.TurnStartResponse, error) {
	finish, err := c.beginThreadOperation(p.ThreadID)
	if err != nil {
		return types.TurnStartResponse{}, err
	}
	defer finish()
	var r types.TurnStartResponse
	err = c.Call(ctx, "turn/start", p, &r)
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
func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	return c.call(ctx, method, params, result, false)
}
func (c *Client) call(ctx context.Context, method string, params any, result any, initializing bool) error {
	return c.callAccept(ctx, method, params, result, initializing, nil)
}

func (c *Client) callAccept(ctx context.Context, method string, params any, result any, initializing bool, accept func(json.RawMessage) error) error {
	return c.callOwned(ctx, method, params, result, initializing, accept, nil)
}

func (c *Client) callOwned(ctx context.Context, method string, params any, result any, initializing bool, accept func(json.RawMessage) error, owner *Session) error {
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
	if owner != nil && (method == "turn/start" || method == "thread/settings/update") {
		if err := owner.loadGateLocked(); err != nil {
			c.mu.Unlock()
			return fail(ID{}, NotSent, err)
		}
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
	frame, _ := json.Marshal(struct {
		Method string `json:"method"`
	}{method})
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
			c.mu.Lock()
			if m.answer != nil {
				m.answer.writing = false
				m.answer.finishLocked(err)
			}
			if m.call != nil {
				m.call.writing = false
			}
			c.mu.Unlock()
			if m.written != nil {
				m.written <- err
			}
			if err != nil {
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
	if c.cause != nil {
		c.mu.Unlock()
		return
	}
	c.cause = cause
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
	close(c.requests)
	for t := range c.subscriptions {
		terminal := cause
		if cause == ErrClosed {
			terminal = nil
		}
		if terminal != nil {
			t.disconnectLocked(terminal)
		} else {
			t.finishLocked(nil)
		}
	}
	for _, s := range c.sessions {
		terminal := cause
		if cause == ErrClosed {
			terminal = nil
		}
		s.finishLocked(terminal)
	}
	c.requestHistory = nil
	close(c.done)
	c.mu.Unlock()
	if c.remote != nil {
		_ = c.remote.CloseNow()
	} else {
		_ = c.stdin.Close()
	}
}
func (c *Client) cleanup() {
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

// Close is idempotent and bounded. A remote client closes only its socket. A
// stdio client closes stdin, allows a bounded grace period, then kills only the
// child it started and waits for reaping. It never sends a protocol shutdown RPC
// or kills an unrelated process, process group, or remote server.
func (c *Client) Close() error {
	c.fail(ErrClosed)
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
	buf := make([]byte, 4096)
	for {
		n, err := c.stderr.Read(buf)
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
