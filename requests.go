package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zoumo/codex-sdk-go/protocol"
)

// Request is an immutable received server request. Accessors return independent
// copies; changing a decoded payload cannot change its identity or answer target.
// Requests are shared between pull consumers, not broadcast or replayed.
type Request struct{ state *requestState }

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
	return append(json.RawMessage(nil), r.state.params...)
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
		data = new(protocol.CommandExecutionRequestApprovalParams)
	case "item/fileChange/requestApproval":
		data = new(protocol.FileChangeRequestApprovalParams)
	case "item/permissions/requestApproval":
		data = new(protocol.PermissionsRequestApprovalParams)
	case "item/tool/requestUserInput":
		data = new(protocol.ToolRequestUserInputParams)
	case "applyPatchApproval":
		data = new(protocol.ApplyPatchApprovalParams)
	case "execCommandApproval":
		data = new(protocol.ExecCommandApprovalParams)
	default:
		return nil, nil
	}
	if err := json.Unmarshal(r.state.params, data); err != nil {
		return nil, r.state.client.safeError(&DecodeError{Method: r.state.method, Cause: err})
	}
	return data, nil
}

// AnswerHandle is an opaque, connection-bound, one-shot response capability.
// Copies share one state. It cannot be retargeted to another ID or connection.
// Cancel/expiry/resolution only win before an answer is admitted. No operation
// here claims to stop server execution; turn interruption is a separate RPC.
type AnswerHandle struct{ state *requestState }

type requestState struct {
	client    *Client
	id        ID
	method    string
	params    json.RawMessage
	threadID  string
	hasThread bool
	done      chan struct{}
	terminal  error
	attempt   *answerAttempt
	timer     *time.Timer
}
type answerAttempt struct {
	request                    *requestState
	done                       chan struct{}
	started, writing, finished bool
	err                        error
}

// Requests returns the connection's bounded pull queue, active from startup.
// A stalled consumer never blocks the reader. Exhausting RequestQueue or
// ActiveRequests explicitly fails the connection with ErrRequestOverflow.
// Close closes this channel without waiting for consumers; buffered requests
// remain readable, but all their handles are invalidated. Handle expiry may also
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
// reports ErrRequestAnswered, local cancellation/expiry, native resolution, or
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
// failures leave the handle usable. Once admitted, the handle is consumed even
// if cancellation occurs before writing. Partial/ambiguous writes are never
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
		if r.timer != nil {
			r.timer.Stop()
		}
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
func (r *requestState) finishLocked(cause error) {
	if r.terminal != nil {
		return
	}
	r.terminal = cause
	if r.timer != nil {
		r.timer.Stop()
	}
	// Pointer check prevents a stale handle from deleting a newer reused ID.
	if r.client.reverse[r.id] == r {
		delete(r.client.reverse, r.id)
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
		a.request.finishLocked(cause)
	} else {
		a.request.finishLocked(ErrRequestAnswered)
	}
	close(a.done)
}

func (c *Client) receiveRequest(id ID, method string, params json.RawMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cause != nil {
		return c.cause
	}
	if _, exists := c.reverse[id]; exists {
		return fmt.Errorf("%w: duplicate active server request ID", ErrProtocol)
	}
	if len(c.reverse) >= c.limits.ActiveRequests || len(c.requests) >= cap(c.requests) {
		return ErrRequestOverflow
	}
	r := &requestState{client: c, id: id, method: method, params: append(json.RawMessage(nil), params...), done: make(chan struct{})}
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
	c.reverse[id] = r
	c.requests <- Request{state: r} // Capacity checked under the same producer lock.
	r.timer = time.AfterFunc(c.limits.RequestTimeout, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if r.terminal == nil && r.attempt == nil {
			r.finishLocked(ErrRequestExpired)
		}
	})
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
	if r != nil && r.hasThread && r.threadID == thread && r.terminal == nil && r.attempt == nil {
		r.finishLocked(ErrRequestResolved)
	}
}
