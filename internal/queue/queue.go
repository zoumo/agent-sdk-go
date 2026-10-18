// Package queue adapts ChannX to typed event streams, with atomic admission and
// explicit drain/abort lifecycle. Each Queue owns one ChannX instance.
package queue

import (
	"context"
	"errors"
	"io"
	"math"
	"sync"
	"time"

	chanx "github.com/zoumo/golib/channx"
)

var (
	ErrNil          = errors.New("queue: nil receiver")
	ErrNilContext   = errors.New("queue: nil context")
	ErrClosed       = errors.New("queue: closed")
	ErrInvalidBytes = errors.New("queue: negative byte estimate")
	ErrSizeOverflow = errors.New("queue: byte estimate overflow")
)

// Item supplies a value and its caller-estimated retained size. Bytes does not
// impose a memory limit or account for the queue's own allocations.
type Item[T any] struct {
	Value T
	Bytes int64
}

// Stats describes pending values, excluding popped values. OldestAge is always
// zero: ChannX does not expose the oldest buffered envelope.
type Stats struct {
	Items     int
	Bytes     int64
	OldestAge time.Duration
	Closed    bool
}

type command[T any] struct {
	items []Item[T]
	close bool
	ack   chan struct{}
}

// Queue is a dynamically growing event FIFO whose zero value is ready for use.
// It must not be copied after first use. Growth is limited by available memory;
// delivery is not persistent. Admission is serialized, and Pop calls share one
// cancellable consumer token. Only the owner goroutine writes to or closes the
// underlying ChannX. Callers must Close and drain, or Abort, when finished.
type Queue[T any] struct {
	mu        sync.Mutex
	ch        *chanx.ChannX
	commands  chan command[T]
	ownerDone chan struct{}
	token     chan struct{}
	abort     chan struct{}
	aborted   chan struct{}
	abortOnce sync.Once
	pending   []Item[T] // only the current envelope; protected by token
	items     int
	bytes     int64
	closed    bool
	cause     error
}

func (q *Queue[T]) initLocked() {
	if q.ch != nil {
		return
	}
	q.ch = chanx.New(chanx.MaxBufferSize(0), chanx.InChanSize(0), chanx.OutChanSzie(0))
	q.commands = make(chan command[T])
	q.ownerDone = make(chan struct{})
	q.token = make(chan struct{}, 1)
	q.token <- struct{}{}
	q.abort = make(chan struct{})
	q.aborted = make(chan struct{})
	go q.own()
}

// own is the sole writer and lifecycle owner of ChannX. Its command channel is
// an unbuffered rendezvous, not another event backlog. It never takes q.mu.
func (q *Queue[T]) own() {
	defer close(q.ownerDone)
	for {
		cmd := <-q.commands
		if cmd.close {
			q.ch.Close()
			close(cmd.ack)
			return
		}
		q.ch.In() <- cmd.items
		close(cmd.ack)
	}
}

// Push admits one value without waiting for an event consumer.
func (q *Queue[T]) Push(value T, bytes int64) error {
	return q.PushBatch([]Item[T]{{Value: value, Bytes: bytes}})
}

// PushBatch admits all items as one envelope, contiguously, or rejects the entire
// batch. Its acknowledgement waits for ChannX admission, never consumer drain.
func (q *Queue[T]) PushBatch(items []Item[T]) error {
	if q == nil {
		return ErrNil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrClosed
	}
	total := q.bytes
	for _, item := range items {
		if item.Bytes < 0 {
			return ErrInvalidBytes
		}
		if item.Bytes > math.MaxInt64-total {
			return ErrSizeOverflow
		}
		total += item.Bytes
	}
	if len(items) == 0 {
		return nil
	}
	q.initLocked()
	// Copy the envelope so callers retain no write access to accepted slots.
	accepted := append([]Item[T](nil), items...)
	q.items += len(accepted)
	q.bytes = total
	ack := make(chan struct{})
	q.commands <- command[T]{items: accepted, ack: ack}
	<-ack
	return nil
}

// Pop waits for a value or terminal cause. Cancellation cancels only this wait;
// even an envelope fetched concurrently with cancellation remains available to
// the next Pop. Nil contexts are invalid.
func (q *Queue[T]) Pop(ctx context.Context) (T, error) {
	var zero T
	if q == nil {
		return zero, ErrNil
	}
	if ctx == nil {
		return zero, ErrNilContext
	}
	q.mu.Lock()
	if err := ctx.Err(); err != nil {
		q.mu.Unlock()
		return zero, err
	}
	q.initLocked()
	q.mu.Unlock()
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-q.abort:
		return zero, q.terminal()
	case <-q.token:
	}
	defer func() { q.token <- struct{}{} }()
	// Check before receiving an envelope; repeat under the lock before removing
	// an item, since cancellation or abort may race with the channel receive.
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	select {
	case <-q.abort:
		return zero, q.terminal()
	default:
	}
	if len(q.pending) == 0 {
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-q.abort:
			return zero, q.terminal()
		case raw, ok := <-q.ch.Out():
			if !ok {
				return zero, q.terminal()
			}
			q.pending = raw.([]Item[T])
		}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	select {
	case <-q.abort:
		return zero, q.cause
	default:
	}
	item := q.pending[0]
	q.pending[0] = Item[T]{}
	q.pending = q.pending[1:]
	if len(q.pending) == 0 {
		q.pending = nil
	}
	q.items--
	q.bytes -= item.Bytes
	return item.Value, nil
}

func (q *Queue[T]) terminal() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.cause
}

// Close rejects further admission and requests the owner to close ChannX after
// all acknowledged pushes. It does not wait for consumer drain. The first cause
// is preserved; a nil cause becomes io.EOF. Drain or Abort to release resources.
func (q *Queue[T]) Close(cause error) error {
	if q == nil {
		return ErrNil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.initLocked()
	q.closeLocked(cause)
	return nil
}

func (q *Queue[T]) closeLocked(cause error) {
	if q.closed {
		return
	}
	q.closed = true
	if cause == nil {
		cause = io.EOF
	}
	q.cause = cause
	ack := make(chan struct{})
	q.commands <- command[T]{close: true, ack: ack}
	<-ack
}

// Abort wakes waiting Pops, closes admission through the owner, and discards
// pending envelopes until ChannX terminates. It is O(backlog), independent of an
// external consumer, and preserves an earlier Close's cause.
func (q *Queue[T]) Abort(cause error) error {
	if q == nil {
		return ErrNil
	}
	q.abortOnce.Do(func() {
		q.mu.Lock()
		q.initLocked()
		close(q.abort)
		q.closeLocked(cause)
		q.mu.Unlock()
		<-q.token
		clear(q.pending)
		q.pending = nil
		for raw := range q.ch.Out() {
			clear(raw.([]Item[T]))
		}
		<-q.ownerDone
		q.mu.Lock()
		q.items = 0
		q.bytes = 0
		q.mu.Unlock()
		q.token <- struct{}{}
		close(q.aborted)
	})
	<-q.aborted
	return nil
}

// Stats returns a concurrent snapshot. A nil queue is closed and empty.
func (q *Queue[T]) Stats() Stats {
	if q == nil {
		return Stats{Closed: true}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return Stats{Items: q.items, Bytes: q.bytes, Closed: q.closed}
}
