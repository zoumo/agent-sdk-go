package codex

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/zoumo/codex-sdk-go/protocol"
)

// Event preserves a notification's method and parameter JSON as received.
// Params is nil when the member was absent; explicit null remains the bytes "null". Data is a pointer to
// a supported protocol notification, or nil for unknown methods/decode failures.
// DecodeError is non-nil only for malformed supported payloads. Unknown fields
// and union variants remain in the native Data and in Params.
//
// Each subscriber owns its event's buffers and typed data independently. There
// is no replay or filtering; callers match native thread, turn, and item IDs.
type Event struct {
	Method      string
	Params      json.RawMessage
	Data        any
	DecodeError error
}

// Subscription is a local, ordered, bounded notification stream, active when
// Subscribe returns. Events closes on termination; queued events remain readable.
// Done closes immediately even if Events is full. After Done, Err distinguishes
// normal Close (nil), context cancellation, overflow, and connection failure.
// No method interrupts execution or unsubscribes a thread on the server.
type Subscription struct {
	client *Client
	events chan Event
	done   chan struct{}
	cause  error // protected, like all subscription state, by client.mu
	closed bool
	stop   func() bool
}

// Subscribe registers before returning, with Limits.EventQueue slots. It does
// not replay prior notifications. Limits.Subscriptions bounds active streams.
func (c *Client) Subscribe(ctx context.Context) (*Subscription, error) {
	if ctx == nil {
		return nil, errors.New("codex: nil context")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.cause != nil {
		return nil, c.cause
	}
	if !c.ready {
		return nil, ErrProtocol
	}
	if len(c.subscriptions) >= c.limits.Subscriptions {
		return nil, ErrLimit
	}
	s := &Subscription{client: c, events: make(chan Event, c.limits.EventQueue), done: make(chan struct{})}
	c.subscriptions[s] = struct{}{}
	s.stop = context.AfterFunc(ctx, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		s.finishLocked(ctx.Err())
	})
	return s, nil
}

func (s *Subscription) Events() <-chan Event  { return s.events }
func (s *Subscription) Done() <-chan struct{} { return s.done }
func (s *Subscription) Err() error {
	s.client.mu.Lock()
	defer s.client.mu.Unlock()
	return s.cause
}

// Close ends local observation normally, without discarding queued events. It is
// idempotent and does not wait for a consumer. The first terminal cause wins.
func (s *Subscription) Close() error {
	s.client.mu.Lock()
	defer s.client.mu.Unlock()
	s.finishLocked(nil)
	return nil
}

func (s *Subscription) finishLocked(cause error) {
	if s.closed {
		return
	}
	s.closed = true
	s.cause = cause
	delete(s.client.subscriptions, s)
	if s.stop != nil {
		s.stop()
	}
	close(s.done)
	close(s.events)
}

// All work here is bounded SDK decoding and nonblocking delivery, never caller
// callbacks. Each accepted event gets separate storage for every subscriber.
func (c *Client) publishLocked(method string, params json.RawMessage) {
	for s := range c.subscriptions {
		if len(s.events) == cap(s.events) {
			s.finishLocked(&SubscriptionOverflowError{Capacity: cap(s.events)})
			continue
		}
		event := decodeEvent(method, params)
		select {
		case s.events <- event:
		default:
			s.finishLocked(&SubscriptionOverflowError{Capacity: cap(s.events)})
		}
	}
}

func decodeEvent(method string, params json.RawMessage) Event {
	event := Event{Method: method, Params: append(json.RawMessage(nil), params...)}
	var data any
	switch method {
	case "serverRequest/resolved":
		data = new(protocol.ServerRequestResolvedNotification)
	case "turn/started":
		data = new(protocol.TurnStartedNotification)
	case "turn/completed":
		data = new(protocol.TurnCompletedNotification)
	case "item/started":
		data = new(protocol.ItemStartedNotification)
	case "item/completed":
		data = new(protocol.ItemCompletedNotification)
	case "item/agentMessage/delta":
		data = new(protocol.AgentMessageDeltaNotification)
	case "error":
		data = new(protocol.ErrorNotification)
	default:
		return event
	}
	if err := json.Unmarshal(params, data); err != nil {
		event.DecodeError = &DecodeError{Method: method, Cause: err}
	} else {
		event.Data = data
	}
	return event
}
