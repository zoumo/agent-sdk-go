package claude

import (
	"context"
	"sync"
)

// transcriptTestStream owns one parser-test worker, independently of Session.
// Drain Records and check Err after Done.
type transcriptTestStream struct {
	records chan Event
	done    chan struct{}
	cancel  context.CancelFunc
	mu      sync.Mutex
	err     error
}

// startTranscriptTestStream installs the receiving channel before starting production. The context
// controls the complete replay lifetime, including blocked sends and source I/O.
func startTranscriptTestStream(ctx context.Context, run func(context.Context, func(Event) error) error) *transcriptTestStream {
	ctx, cancel := context.WithCancel(ctx)
	s := &transcriptTestStream{records: make(chan Event), done: make(chan struct{}), cancel: cancel}
	go func() {
		defer cancel()
		err := run(ctx, func(record Event) error {
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
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		close(s.records)
		close(s.done)
	}()
	return s
}

func (s *transcriptTestStream) Records() <-chan Event { return s.records }
func (s *transcriptTestStream) Done() <-chan struct{} { return s.done }

// Err is final after Done closes. Before that it may be nil.
func (s *transcriptTestStream) Err() error { s.mu.Lock(); defer s.mu.Unlock(); return s.err }

// Close cancels and waits for the owned worker, including a blocked consumer.
// It does not close any caller-owned connection.
func (s *transcriptTestStream) Close() error { s.cancel(); <-s.done; return nil }
