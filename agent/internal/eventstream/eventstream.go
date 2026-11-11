package eventstream

import (
	"context"
	"sync"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/internal/queue"
)

type End struct {
	Explicit bool
	Cause    error
}

type Hooks struct {
	BeginClose  func(End)
	CloseEvents func(End) []agent.Event
	CloseNative func() error
	FinalCause  func() error
	Release     func()
}

type Stream struct {
	hooks  Hooks
	events queue.Queue[agent.Event]

	mu              sync.Mutex
	claimed         bool
	closing         bool
	explicitClose   bool
	observed        bool
	end             End
	closeErr        error
	observationDone chan struct{}
	done            chan struct{}
}

func New(hooks Hooks) *Stream {
	return &Stream{
		hooks:           hooks,
		observationDone: make(chan struct{}),
		done:            make(chan struct{}),
	}
}

func (s *Stream) Claim() (agent.EventStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.explicitClose {
		return nil, &agent.Error{
			Code:    agent.ErrorClosed,
			Reason:  agent.ErrorReasonSessionClosed,
			Message: "session is closed",
		}
	}
	if s.claimed {
		return nil, &agent.Error{
			Code:    agent.ErrorFailedPrecondition,
			Reason:  agent.ErrorReasonEventsAlreadyClaimed,
			Message: "the session event stream has already been claimed",
		}
	}
	s.claimed = true
	return &reader{stream: s}, nil
}

func (s *Stream) Publish(events []agent.Event) error {
	if len(events) == 0 {
		return nil
	}
	items := make([]queue.Item[agent.Event], 0, len(events))
	for _, event := range events {
		items = append(items, queue.Item[agent.Event]{Value: event, Bytes: 1})
	}
	return s.events.PushBatch(items)
}

func (s *Stream) Close(context.Context) error {
	s.begin(End{Explicit: true}, false)
	<-s.done
	return s.closeErr
}

func (s *Stream) ObservationEnded(cause error) {
	s.begin(End{Cause: cause}, true)
}

func (s *Stream) Done() <-chan struct{} { return s.done }

func (s *Stream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.end.Cause
}

func (s *Stream) begin(end End, observed bool) {
	s.mu.Lock()
	if end.Explicit {
		s.explicitClose = true
	}
	if observed && !s.observed {
		s.observed = true
		s.end.Cause = end.Cause
		close(s.observationDone)
	}
	if s.closing {
		s.mu.Unlock()
		return
	}
	s.closing = true
	s.end.Explicit = end.Explicit
	begin := s.end
	s.mu.Unlock()

	if s.hooks.BeginClose != nil {
		s.hooks.BeginClose(begin)
	}
	go s.finish()
}

func (s *Stream) finish() {
	if s.hooks.CloseNative != nil {
		s.closeErr = s.hooks.CloseNative()
	}
	<-s.observationDone
	s.mu.Lock()
	end := s.end
	s.mu.Unlock()
	if end.Cause == nil && s.hooks.FinalCause != nil {
		end.Cause = s.hooks.FinalCause()
		s.mu.Lock()
		s.end.Cause = end.Cause
		s.mu.Unlock()
	}
	if s.hooks.CloseEvents != nil {
		_ = s.Publish(s.hooks.CloseEvents(end))
	}
	_ = s.events.Close(end.Cause)
	if s.hooks.Release != nil {
		s.hooks.Release()
	}
	close(s.done)
}

type reader struct {
	stream *Stream
}

func (r *reader) Next(ctx context.Context) (agent.Event, error) {
	return r.stream.events.Pop(ctx)
}
