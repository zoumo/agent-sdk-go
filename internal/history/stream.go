// Package history contains only bounded replay delivery mechanics.
package history

import (
	"context"
	"sync"
)

// Stream owns one replay worker. Drain Records and check Err after Done.
type Stream[T any] struct {
	records chan T
	done    chan struct{}
	cancel  context.CancelFunc
	mu      sync.Mutex
	err     error
}

// Start installs the receiving channel before starting production. The context
// controls the complete replay lifetime, including blocked sends and source I/O.
func Start[T any](ctx context.Context, run func(context.Context, func(T) error) error, finish func(error) error) *Stream[T] {
	ctx, cancel := context.WithCancel(ctx)
	s := &Stream[T]{records: make(chan T), done: make(chan struct{}), cancel: cancel}
	go func() {
		defer cancel()
		err := run(ctx, func(record T) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			select {
			case s.records <- record:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		if err == nil {
			err = ctx.Err()
		}
		if finish != nil {
			err = finish(err)
		}
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		close(s.records)
		close(s.done)
	}()
	return s
}

func (s *Stream[T]) Records() <-chan T     { return s.records }
func (s *Stream[T]) Done() <-chan struct{} { return s.done }

// Err is final after Done closes. Before that it may be nil.
func (s *Stream[T]) Err() error { s.mu.Lock(); defer s.mu.Unlock(); return s.err }

// Close cancels and waits for the owned worker, including a blocked consumer.
// It does not close any caller-owned connection.
func (s *Stream[T]) Close() error { s.cancel(); <-s.done; return nil }
