// Package queue provides a concurrent, dynamically growing FIFO. Each Queue owns
// its storage; it has no protocol knowledge and starts no goroutines.
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

// Stats describes values still pending in the queue, excluding popped values.
type Stats struct {
	Items     int
	Bytes     int64
	OldestAge time.Duration
	Closed    bool
}

type entry[T any] struct {
	value    T
	bytes    int64
	admitted time.Time
}

// Queue is a FIFO whose zero value is ready for use. It must not be copied after
// first use. Growth is bounded only by available memory; it is not persistent.
// Concurrent producers are ordered by admission under the queue mutex.
type Queue[T any] struct {
	mu      sync.Mutex
	ring    *chanx.SelfAdaptiveRingBuffer
	changed chan struct{}
	bytes   int64
	closed  bool
	cause   error
}

// Push admits one value without waiting for a consumer.
func (q *Queue[T]) Push(value T, bytes int64) error {
	return q.PushBatch([]Item[T]{{Value: value, Bytes: bytes}})
}

// PushBatch admits all items contiguously or rejects the entire batch. Validation
// failures and closure never partially admit a batch. No callbacks run here.
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
	if q.ring == nil {
		q.ring = chanx.NewSelfAdptiveRingBuffer(64, 0)
	}
	now := time.Now()
	for _, item := range items {
		// With maxSize=0 the upstream ring grows instead of rejecting admission.
		if !q.ring.Put(entry[T]{value: item.Value, bytes: item.Bytes, admitted: now}) {
			panic("queue: unbounded ring rejected admission")
		}
	}
	q.bytes = total
	q.signalLocked()
	return nil
}

// Pop waits for a value or terminal cause. Cancellation cancels only this wait;
// it never removes a value. A canceled context takes precedence over pending
// values when observed under the mutex before removal. Nil contexts are invalid.
func (q *Queue[T]) Pop(ctx context.Context) (T, error) {
	var zero T
	if q == nil {
		return zero, ErrNil
	}
	if ctx == nil {
		return zero, ErrNilContext
	}
	q.mu.Lock()
	for {
		if err := ctx.Err(); err != nil {
			q.mu.Unlock()
			return zero, err
		}
		if q.ring != nil {
			raw, ok := q.ring.Pop()
			if ok {
				item := raw.(entry[T])
				q.bytes -= item.bytes
				// Pop clears the upstream slot. Release even a grown backing array when
				// empty instead of retaining the peak allocation for a future burst.
				if q.ring.IsEmpty() {
					q.ring = nil
				}
				q.mu.Unlock()
				return item.value, nil
			}
		}
		if q.closed {
			err := q.cause
			q.mu.Unlock()
			return zero, err
		}
		if q.changed == nil {
			q.changed = make(chan struct{})
		}
		changed := q.changed
		q.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
		}
		q.mu.Lock()
	}
}

// Close rejects further pushes, wakes waiters, and lets accepted values drain.
// The first terminal cause is preserved; a nil cause becomes io.EOF.
func (q *Queue[T]) Close(cause error) error {
	if q == nil {
		return ErrNil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closeLocked(cause)
	return nil
}

// Abort closes the queue and drops all pending values. After an earlier Close,
// it discards the remaining drain contents while preserving that Close's cause.
func (q *Queue[T]) Abort(cause error) error {
	if q == nil {
		return ErrNil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ring = nil
	q.bytes = 0
	q.closeLocked(cause)
	return nil
}

func (q *Queue[T]) closeLocked(cause error) {
	if !q.closed {
		q.closed = true
		if cause == nil {
			cause = io.EOF
		}
		q.cause = cause
	}
	q.signalLocked()
}

func (q *Queue[T]) signalLocked() {
	if q.changed != nil {
		close(q.changed)
		q.changed = nil
	}
}

// Stats returns a concurrent snapshot. A nil queue is closed and empty.
func (q *Queue[T]) Stats() Stats {
	if q == nil {
		return Stats{Closed: true}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	stats := Stats{Bytes: q.bytes, Closed: q.closed}
	if q.ring != nil {
		stats.Items = q.ring.Len()
		if raw, ok := q.ring.Peek(); ok {
			stats.OldestAge = time.Since(raw.(entry[T]).admitted)
		}
	}
	return stats
}
