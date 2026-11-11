package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"maps"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

// sessionOwnership coordinates all uses of native thread identities on one
// connection. Its state shares Client.mu with response admission, membership and
// request routing. Network waits never hold that mutex. Only this module changes
// registrations, reservations and attachment release fences; native membership
// and original request authority remain with Session and the request registry.
type sessionOwnership struct {
	client       *Client
	owners       map[string]*Session
	attachments  map[string]*sessionAttachment
	reservations int
	operations   map[string]int
}

func (o *sessionOwnership) ownerLocked(id string) *Session { return o.owners[id] }

// Callers iterate under Client.mu without receiving a mutable registry.
func (o *sessionOwnership) sessionsLocked() iter.Seq[*Session] { return maps.Values(o.owners) }

// open owns capacity reservation, native dispatch and construction failure
// cleanup. Start binds within response admission; resume binds before dispatch.
func (o *sessionOwnership) open(ctx context.Context, s *Session, options SessionOptions) error {
	c := o.client
	c.mu.Lock()
	var err error
	if c.cause != nil {
		err = c.cause
	} else if len(o.owners)+o.reservations >= c.limits.Subscriptions {
		err = ErrLimit
	} else {
		o.reservations++
	}
	c.mu.Unlock()
	if err == nil {
		defer func() { c.mu.Lock(); o.reservations--; c.mu.Unlock() }()
		if options.Resume != nil {
			err = o.resume(ctx, s, *options.Resume)
		} else {
			var response types.ThreadStartResponse
			err = c.callAccept(ctx, "thread/start", options.Thread, &response, false, func(raw json.RawMessage) error {
				var r types.ThreadStartResponse
				if err := json.Unmarshal(raw, &r); err != nil {
					return err
				}
				if r.Thread.ID == "" {
					return ErrProtocol
				}
				if err := o.bindLocked(s, r.Thread.ID); err != nil {
					return err
				}
				o.attachments[r.Thread.ID].managed = true
				s.startRaw = append(json.RawMessage(nil), raw...)
				return nil
			})
		}
	}
	if err != nil {
		s.cancel()
		if s.owned {
			_ = c.Close()
		}
	}
	return err
}

// bindLocked registers identity before the reader processes the next record.
func (o *sessionOwnership) bindLocked(s *Session, id string) error {
	if o.operations[id] > 0 || o.owners[id] != nil {
		return ErrOwnershipConflict
	}
	if len(o.owners) >= o.client.limits.Subscriptions {
		return ErrLimit
	}
	if o.owners == nil {
		o.owners = make(map[string]*Session)
	}
	if err := s.parents.Root(id); err != nil {
		return err
	}
	if !o.retainLocked(s, id) {
		return ErrOwnershipConflict
	}
	s.members[id] = struct{}{}
	s.root = id
	o.owners[id] = s
	return nil
}

// Notifications may precede the resume response; saved turns remain separate
// from Receive. A possibly dispatched failure detaches once, retaining a fence
// if the release cannot be confirmed.
func (o *sessionOwnership) resume(ctx context.Context, s *Session, params types.ThreadResumeParams) error {
	c := o.client
	c.mu.Lock()
	err := o.bindLocked(s, params.ThreadID)
	c.mu.Unlock()
	if err != nil {
		return &CallError{Method: "thread/resume", Dispatch: NotSent, Cause: err}
	}
	err = c.callOwned(ctx, "thread/resume", params, nil, false, func(raw json.RawMessage) error {
		result := resumeThreadResult{expectedID: params.ThreadID}
		if err := json.Unmarshal(raw, &result); err != nil {
			return fmt.Errorf("%w: decode thread/resume result: %w", ErrProtocol, err)
		}
		o.attachments[s.root].managed = true
		s.resumeRaw = append(json.RawMessage(nil), raw...)
		return nil
	}, s)
	if err != nil {
		var call *CallError
		c.mu.Lock()
		if errors.As(err, &call) && call.Dispatch != NotSent {
			o.attachments[s.root].managed = true
		}
		c.mu.Unlock()
		return errors.Join(err, s.Close())
	}
	return nil
}

// call reserves a low-level operation for its entire dispatch and wait. Multiple
// low-level operations may coexist; their count fences out Session admission and
// observation, rather than serializing unrelated native calls.
func (o *sessionOwnership) call(ctx context.Context, id, method string, params, result any) error {
	c := o.client
	c.mu.Lock()
	if o.busyLocked(id) || o.owners[id] != nil {
		c.mu.Unlock()
		return ErrOwnershipConflict
	}
	if o.operations == nil {
		o.operations = make(map[string]int)
	}
	o.operations[id]++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		o.operations[id]--
		if o.operations[id] == 0 {
			delete(o.operations, id)
		}
	}()
	return c.Call(ctx, method, params, result)
}

// close stops admission before cleanup. Only confirmed release removes the main
// owner while the connection is usable; an uncertain release keeps it fenced.
// Session.cleanupOnce serializes callers, without blocking the reader on I/O.
func (o *sessionOwnership) close(s *Session) error {
	c := o.client
	c.mu.Lock()
	s.finishLocked(nil)
	c.mu.Unlock()
	var err error
	if s.owned {
		err = c.Close()
		_ = o.release(s)
	} else {
		err = o.release(s)
	}
	c.mu.Lock()
	if o.owners[s.root] == s && (err == nil || c.cause != nil) {
		delete(o.owners, s.root)
	}
	c.mu.Unlock()
	return err
}

// Native subscriptions are sets of connection IDs, not one attachment per
// resume. Keep references for all confirmed observers separately from main
// ownership and request authority. All fields are protected by Client.mu.
type sessionAttachment struct {
	observers       map[*Session]struct{}
	managed         bool
	releasing       bool
	uncertain       error
	inputHalfClosed bool
}

func (o *sessionOwnership) retainLocked(s *Session, id string) bool {
	if o.operations[id] > 0 {
		return false
	}
	if o.attachments == nil {
		o.attachments = make(map[string]*sessionAttachment)
	}
	a := o.attachments[id]
	if a == nil {
		a = &sessionAttachment{observers: make(map[*Session]struct{})}
		o.attachments[id] = a
	}
	if a.releasing || a.uncertain != nil || a.inputHalfClosed {
		return false
	}
	a.observers[s] = struct{}{}
	return true
}

// release removes this observer and releases only physical
// attachments no remaining Session needs. Failed or impossible release keeps a
// fence; neither a new owner nor a new observer can silently reuse it.
func (o *sessionOwnership) release(s *Session) error {
	c := o.client
	c.mu.Lock()
	var release []string
	var result error
	for id, a := range o.attachments {
		if _, ok := a.observers[s]; !ok {
			continue
		}
		if c.cause != nil || c.terminalClosed {
			delete(a.observers, s)
			if len(a.observers) == 0 {
				delete(o.attachments, id)
			}
			continue
		}
		delete(a.observers, s)
		if len(a.observers) != 0 {
			continue
		}
		if a.uncertain != nil {
			result = errors.Join(result, a.uncertain)
			continue
		}
		if a.inputHalfClosed {
			continue
		}
		if !a.managed || c.cause != nil {
			delete(o.attachments, id)
			continue
		}
		a.releasing = true
		release = append(release, id)
	}
	c.mu.Unlock()
	for _, id := range release {
		if c.streamsInputHalfClosed() {
			c.mu.Lock()
			o.retainInputHalfClosedFenceLocked(id)
			c.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), c.limits.ShutdownTimeout)
		r, err := c.unsubscribeThread(ctx, types.ThreadUnsubscribeParams{ThreadID: id})
		cancel()
		if err == nil && r.Status != types.ThreadUnsubscribeStatusUnsubscribed && r.Status != types.ThreadUnsubscribeStatusNotLoaded && r.Status != types.ThreadUnsubscribeStatusNotSubscribed {
			err = errors.New("codex: unknown unsubscribe result")
		}
		c.mu.Lock()
		if err == nil || c.cause != nil {
			delete(o.attachments, id)
		} else if c.transport.StreamInput() != nil && errors.Is(err, ErrInputClosed) {
			o.retainInputHalfClosedFenceLocked(id)
		} else {
			a := o.attachments[id]
			a.releasing = false
			a.uncertain = err
		}
		c.mu.Unlock()
		if !errors.Is(err, ErrInputClosed) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (o *sessionOwnership) retainInputHalfClosedFenceLocked(id string) {
	a := o.attachments[id]
	if a == nil {
		return
	}
	a.releasing = false
	a.inputHalfClosed = true
}

// busyLocked covers both active observers and release tombstones.
func (o *sessionOwnership) busyLocked(id string) bool {
	a := o.attachments[id]
	return a != nil && (len(a.observers) != 0 || a.releasing || a.uncertain != nil || a.inputHalfClosed)
}
