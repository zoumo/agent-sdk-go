package codex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

// Request holds an immutable received snapshot separately from registry authority.
// Consuming/releasing the snapshot releases its Raw/Params payload; terminal
// replay protection retains no command, diff or question payload.
// Request is an immutable received server request. Accessors return independent
// copies; changing a decoded payload cannot change its identity or answer target.
// Requests are shared between pull consumers, not broadcast or replayed.
type Request struct {
	state  *requestState
	raw    json.RawMessage
	params json.RawMessage
}

// Raw returns an independent copy of the complete native request envelope.
func (r Request) Raw() json.RawMessage { return append(json.RawMessage(nil), r.raw...) }

// IncomingRequest carries the original connection registry capability.
// Zero values, parsing, history and diagnostic records cannot grant authority.
type IncomingRequest struct{ Request }

func (r IncomingRequest) Context() context.Context {
	if r.state == nil {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx
	}
	return r.state.ctx
}

func (r Request) ID() ID {
	if r.state == nil {
		return ID{}
	}
	return r.state.id
}
func (r Request) Method() string {
	if r.state == nil {
		return ""
	}
	return r.state.method
}
func (r Request) Params() json.RawMessage {
	if r.state == nil {
		return nil
	}
	return append(json.RawMessage(nil), r.params...)
}
func (r Request) Handle() AnswerHandle { return AnswerHandle{state: r.state} }

// Data decodes a fresh native payload. Unknown methods return nil, nil; malformed
// known payloads return a DecodeError while Params retains the original bytes.
func (r Request) Data() (any, error) {
	if r.state == nil {
		return nil, ErrInvalidAnswerHandle
	}
	var data any
	switch r.state.method {
	case "item/commandExecution/requestApproval":
		data = new(types.CommandExecutionRequestApprovalParams)
	case "item/fileChange/requestApproval":
		data = new(types.FileChangeRequestApprovalParams)
	case "item/permissions/requestApproval":
		data = new(types.PermissionsRequestApprovalParams)
	case "item/tool/requestUserInput":
		data = new(types.ToolRequestUserInputParams)
	case "applyPatchApproval":
		data = new(types.ApplyPatchApprovalParams)
	case "execCommandApproval":
		data = new(types.ExecCommandApprovalParams)
	default:
		return nil, nil
	}
	if err := json.Unmarshal(r.params, data); err != nil {
		return nil, r.state.client.safeError(&DecodeError{Method: r.state.method, Cause: err})
	}
	return data, nil
}

// AnswerHandle is an opaque, connection-bound, one-shot response capability.
// Copies share one state. It cannot be retargeted to another ID or connection.
// Native resolution and owner termination invalidate the same registry entry. No operation
// here claims to stop server execution; turn interruption is a separate RPC.
type AnswerHandle struct{ state *requestState }

// requestKey distinguishes tagged IDs and byte-identical native request data.
// Retained identities are bounded and never evicted to restore permission.
type requestKey struct {
	id     ID
	method string
	params [32]byte
}

type requestState struct {
	ctx       context.Context
	cancel    context.CancelFunc
	owner     *Session
	legacy    bool
	client    *Client
	id        ID
	method    string
	threadID  string
	hasThread bool
	done      chan struct{}
	terminal  error
	attempt   *answerAttempt
}
type answerAttempt struct {
	request                    *requestState
	done                       chan struct{}
	started, writing, finished bool
	err                        error
}

// Requests returns the connection's bounded pull queue, active from startup.
// Session-owned requests never enter this legacy queue.
// A stalled consumer never blocks the reader. Exhausting RequestQueue or
// ActiveRequests explicitly fails the connection with ErrRequestOverflow.
// Close closes this channel without waiting for consumers; buffered requests
// remain readable, but all their handles are invalidated. Native resolution can
// leave terminal requests in the queue. Check Handle.Err before presenting a UI.
func (c *Client) Requests() <-chan Request { return c.requests }

func (h AnswerHandle) Done() <-chan struct{} {
	if h.state == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return h.state.done
}

// Err is nil while waiting (including a response in progress). After Done it
// reports ErrRequestAnswered, local cancellation, native resolution, or
// connection failure. A successful write is not evidence of server acceptance.
func (h AnswerHandle) Err() error {
	if h.state == nil {
		return ErrInvalidAnswerHandle
	}
	c := h.state.client
	c.mu.Lock()
	defer c.mu.Unlock()
	return h.state.terminal
}

// Cancel abandons an unanswered handle locally. It sends no response or cancel
// notification. Call Reject with a caller-chosen RPC error to respond instead.
func (h AnswerHandle) Cancel() error {
	if h.state == nil {
		return ErrInvalidAnswerHandle
	}
	r := h.state
	c := r.client
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := r.unavailableLocked(); err != nil {
		return err
	}
	r.finishLocked(ErrRequestCanceled)
	return nil
}

// Answer sends exactly one caller-selected result, including null for nil.
// Marshal/validation, pre-canceled-context, frame-limit and queue-admission
// failures and confirmed NotSent cancellation leave a live handle usable.
// Native resolution/Close still invalidate it. Partial/ambiguous writes are never
// retried. Context cancellation during a blocked write fails the connection.
func (h AnswerHandle) Answer(ctx context.Context, result any) error {
	return h.respond(ctx, result, nil)
}

// Reject sends a caller-selected protocol error, never an approval decision.
// RPCError.Raw is diagnostic inbound data and is not emitted; Code, Message and
// Data define the outgoing error. A nil error is a safe pre-write failure.
func (h AnswerHandle) Reject(ctx context.Context, rpcError *RPCError) error {
	if rpcError == nil {
		return h.answerError(NotSent, errors.New("codex: nil response error"))
	}
	return h.respond(ctx, nil, rpcError)
}
func (h AnswerHandle) answerError(dispatch DispatchState, cause error) error {
	e := &AnswerError{Dispatch: dispatch, Cause: cause}
	if h.state != nil {
		e.ID = h.state.id
		e.Method = h.state.method
	}
	if h.state != nil {
		return h.state.client.safeError(e)
	}
	return e
}
func (h AnswerHandle) respond(ctx context.Context, result any, rpcError *RPCError) error {
	if h.state == nil {
		return h.answerError(NotSent, ErrInvalidAnswerHandle)
	}
	if ctx == nil {
		return h.answerError(NotSent, errors.New("codex: nil context"))
	}
	r := h.state
	c := r.client
	ctx, cancel := context.WithTimeout(ctx, c.limits.CallTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return h.answerError(NotSent, err)
	}
	c.mu.Lock()
	stateErr := r.unavailableLocked()
	c.mu.Unlock()
	if stateErr != nil {
		return h.answerError(NotSent, stateErr)
	}
	// Caller marshalers run outside the connection lock and reader. Close never
	// waits for caller code, and state is checked again after serialization.
	var frame []byte
	var err error
	if rpcError != nil {
		frame, err = json.Marshal(struct {
			ID    ID        `json:"id"`
			Error *RPCError `json:"error"`
		}{r.id, rpcError})
	} else {
		frame, err = json.Marshal(struct {
			ID     ID  `json:"id"`
			Result any `json:"result"`
		}{r.id, result})
	}
	if err != nil {
		return h.answerError(NotSent, err)
	}
	if len(frame) > c.limits.MaxFrameBytes {
		return h.answerError(NotSent, ErrFrameTooLarge)
	}
	c.mu.Lock()
	if err = r.unavailableLocked(); err == nil {
		err = ctx.Err()
	}
	if err != nil {
		c.mu.Unlock()
		return h.answerError(NotSent, err)
	}
	a := &answerAttempt{request: r, done: make(chan struct{})}
	select {
	case c.queue <- outbound{frame: frame, ctx: ctx, answer: a}:
		r.attempt = a
	default:
		c.mu.Unlock()
		return h.answerError(NotSent, ErrLimit)
	}
	c.mu.Unlock()
	select {
	case <-a.done:
	case <-ctx.Done():
		c.mu.Lock()
		writing := a.writing && !a.finished
		if !a.finished {
			a.finishLocked(ctx.Err())
		}
		c.mu.Unlock()
		if writing {
			c.fail(ctx.Err())
		}
	}
	return a.err
}
func (r *requestState) unavailableLocked() error {
	if r.terminal != nil {
		return r.terminal
	}
	if r.attempt != nil {
		return ErrRequestAnswered
	}
	if r.client.cause != nil {
		return r.client.cause
	}
	return nil
}
func (r *requestState) invalidateLocked(cause error) {
	r.finishLocked(cause)
	if r.attempt != nil {
		r.attempt.finishLocked(cause)
	}
}
func (r *requestState) finishLocked(cause error) {
	if r.terminal != nil {
		return
	}
	r.terminal = cause

	// Pointer check prevents a stale handle from deleting a newer reused ID.
	if r.client.reverse[r.id] == r {
		delete(r.client.reverse, r.id)
	}
	if r.cancel != nil {
		r.cancel()
	}
	close(r.done)
}
func (a *answerAttempt) finishLocked(cause error) {
	if a.finished {
		return
	}
	a.finished = true
	if cause != nil {
		dispatch := NotSent
		if a.started {
			dispatch = OutcomeUnknown
		}
		a.err = a.request.client.safeError(&AnswerError{Method: a.request.method, ID: a.request.id, Dispatch: dispatch, Cause: cause})
		r := a.request
		if !a.started && r.terminal == nil && r.client.cause == nil {
			// A confirmed unsent operation releases admission without destroying the
			// original live native capability. A finished queued attempt is skipped.
			r.attempt = nil
		} else {
			r.finishLocked(cause)
		}
	} else {
		a.request.finishLocked(ErrRequestAnswered)
	}
	close(a.done)
}

func (c *Client) receiveRequest(id ID, method string, params json.RawMessage, raw []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cause != nil {
		return c.cause
	}
	key := requestKey{id: id, method: method, params: sha256.Sum256(params)}
	if r := c.requestHistory[key]; r != nil {
		return c.deliverRequestLocked(r, params, raw)
	}
	if c.reverse[id] != nil {
		return fmt.Errorf("%w: conflicting active server request ID", ErrProtocol)
	}
	if len(c.requestHistory) >= c.limits.RequestIdentities {
		return ErrRequestOverflow
	}

	if len(c.reverse) >= c.limits.ActiveRequests {
		return ErrRequestOverflow
	}
	r := &requestState{client: c, id: id, method: method, done: make(chan struct{})}
	// Correlation needs the native threadId in addition to the exact tagged ID.
	// Malformed/absent thread identity cannot authorize resolution of a handle.
	identityField := "threadId"
	if method == "applyPatchApproval" || method == "execCommandApproval" {
		identityField = "conversationId"
	}
	if fields, err := objectFields(params); err == nil {
		if raw, ok := fields[identityField]; ok && len(raw) > 0 && raw[0] == '"' && validStringEscapes(raw) && json.Unmarshal(raw, &r.threadID) == nil {
			r.hasThread = true
		}
	}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	c.reverse[id] = r
	c.requestHistory[key] = r
	r.owner = c.requestOwnerLocked(r.threadID)
	r.legacy = len(c.sessions) == 0 && c.sessionReservations == 0
	return c.deliverRequestLocked(r, params, raw)
}

// Direct ownership takes precedence over observation. A real reverse request
// creates authority; membership only routes that original capability. Ambiguous
// ranges never select a map iteration winner or broadcast answer authority.
func (c *Client) requestOwnerLocked(thread string) *Session {
	if owner := c.sessions[thread]; owner != nil {
		if !owner.closed {
			return owner
		}
		return nil
	}
	var owner *Session
	for _, s := range c.sessions {
		if !s.closed && s.member(thread) {
			if owner != nil {
				return nil
			}
			owner = s
		}
	}
	return owner
}
func (c *Client) deliverRequestLocked(r *requestState, params json.RawMessage, raw []byte) error {
	request := Request{state: r, raw: append(json.RawMessage(nil), raw...), params: append(json.RawMessage(nil), params...)}
	// Ownership is assigned once at first reception. Late membership never
	// retroactively grants a quarantined request to a Session.
	if r.owner != nil {
		if !r.owner.closed {
			incoming := &IncomingRequest{Request: request}
			source := treeIdentity(params)
			source.ThreadID = r.threadID
			r.owner.acceptLocked(Received{Kind: ReceivedRequest, Request: incoming, Raw: request.Raw(), Params: request.Params(), Source: source, Origin: "live"})
		}
	} else if r.legacy {
		if len(c.requests) >= cap(c.requests) {
			r.finishLocked(ErrRequestOverflow)
			return ErrRequestOverflow
		}
		c.requests <- request
	} else {
		c.quarantineLocked(raw)
	}
	return nil
}

// The pinned serverRequest/resolved notification uses requestId and threadId.
// It is a resolution signal, not a generic cancellation method. Unknown and
// malformed signals remain observable events without invalidating any handle.
func (c *Client) resolveRequestLocked(params json.RawMessage) {
	fields, err := objectFields(params)
	if err != nil {
		return
	}
	id, err := ParseID(fields["requestId"])
	if err != nil {
		return
	}
	var thread string
	raw := fields["threadId"]
	if len(raw) == 0 || raw[0] != '"' || !validStringEscapes(raw) || json.Unmarshal(raw, &thread) != nil {
		return
	}
	r := c.reverse[id]
	if r != nil && r.hasThread && r.threadID == thread && r.terminal == nil {
		r.invalidateLocked(ErrRequestResolved)
	}
}
