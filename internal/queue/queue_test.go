package queue

import (
	"context"
	"errors"
	"io"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type QueueSuite struct{ suite.Suite }

func TestQueueSuite(t *testing.T) { suite.Run(t, new(QueueSuite)) }

func (s *QueueSuite) TestContracts() {
	for _, tc := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"nil receiver", func(t *testing.T) {
			var q *Queue[int]
			require.ErrorIs(t, q.Push(1, 0), ErrNil)
			require.ErrorIs(t, q.PushBatch(nil), ErrNil)
			_, err := q.Pop(context.Background())
			require.ErrorIs(t, err, ErrNil)
			require.ErrorIs(t, q.Close(nil), ErrNil)
			require.ErrorIs(t, q.Abort(nil), ErrNil)
			assert.Equal(t, Stats{Closed: true}, q.Stats())
		}},
		{"zero value and nil payload", func(t *testing.T) {
			var q Queue[*int]

			t.Cleanup(func() { _ = q.Abort(nil) })
			assert.Equal(t, Stats{}, q.Stats())
			require.NoError(t, q.Push(nil, 4))
			require.Equal(t, 1, q.Stats().Items)
			assert.EqualValues(t, 4, q.Stats().Bytes)
			assert.Zero(t, q.Stats().OldestAge)
			_, err := q.Pop(nil)
			require.ErrorIs(t, err, ErrNilContext)
			assert.Equal(t, 1, q.Stats().Items)
			value, err := q.Pop(context.Background())
			require.NoError(t, err)
			assert.Nil(t, value)
			assert.Equal(t, Stats{}, q.Stats())
		}},
		{"close drains first cause", func(t *testing.T) {
			var q Queue[int]

			t.Cleanup(func() { _ = q.Abort(nil) })
			terminal := errors.New("connection lost")
			require.NoError(t, q.PushBatch([]Item[int]{{1, 3}, {2, 5}}))
			require.NoError(t, q.Close(terminal))
			require.NoError(t, q.Close(io.EOF))
			require.ErrorIs(t, q.Push(3, 1), ErrClosed)
			require.ErrorIs(t, q.PushBatch(nil), ErrClosed)
			for _, want := range []int{1, 2} {
				got, err := q.Pop(context.Background())
				require.NoError(t, err)
				assert.Equal(t, want, got)
			}
			_, err := q.Pop(context.Background())
			assert.ErrorIs(t, err, terminal)
			assert.Equal(t, Stats{Closed: true}, q.Stats())
		}},
		{"abort releases drain and preserves cause", func(t *testing.T) {
			var q Queue[int]

			t.Cleanup(func() { _ = q.Abort(nil) })
			terminal := errors.New("first failure")
			require.NoError(t, q.Push(1, 6))
			require.NoError(t, q.Close(terminal))
			require.NoError(t, q.Abort(io.EOF))
			_, err := q.Pop(context.Background())
			assert.ErrorIs(t, err, terminal)
			assert.Equal(t, Stats{Closed: true}, q.Stats())
			assert.Nil(t, q.pending)
		}},
		{"nil terminal causes", func(t *testing.T) {
			for _, abort := range []bool{false, true} {
				var q Queue[int]

				t.Cleanup(func() { _ = q.Abort(nil) })
				if abort {
					require.NoError(t, q.Abort(nil))
				} else {
					require.NoError(t, q.Close(nil))
				}
				_, err := q.Pop(context.Background())
				assert.ErrorIs(t, err, io.EOF)
			}
		}},
		{"cancel does not consume", func(t *testing.T) {
			var q Queue[int]

			t.Cleanup(func() { _ = q.Abort(nil) })
			require.NoError(t, q.Push(42, 3))
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := q.Pop(ctx)
			require.ErrorIs(t, err, context.Canceled)
			got, err := q.Pop(context.Background())
			require.NoError(t, err)
			assert.Equal(t, 42, got)
			ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
			defer cancel()
			_, err = q.Pop(ctx)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.NoError(t, q.Push(43, 1))
			got, err = q.Pop(context.Background())
			require.NoError(t, err)
			assert.Equal(t, 43, got)
		}},
		{"atomic validation", func(t *testing.T) {
			for _, invalid := range []struct {
				items []Item[int]
				err   error
			}{
				{[]Item[int]{{1, 0}, {2, -1}}, ErrInvalidBytes},
				{[]Item[int]{{1, math.MaxInt64}, {2, 1}}, ErrSizeOverflow},
			} {
				var q Queue[int]

				t.Cleanup(func() { _ = q.Abort(nil) })
				require.NoError(t, q.Push(0, 1))
				require.ErrorIs(t, q.PushBatch(invalid.items), invalid.err)
				assert.Equal(t, 1, q.Stats().Items)
				assert.EqualValues(t, 1, q.Stats().Bytes)
				got, err := q.Pop(context.Background())
				require.NoError(t, err)
				assert.Zero(t, got)
			}
			var q Queue[int]

			t.Cleanup(func() { _ = q.Abort(nil) })
			require.NoError(t, q.Push(1, math.MaxInt64))
			require.ErrorIs(t, q.Push(2, 1), ErrSizeOverflow)
			assert.EqualValues(t, math.MaxInt64, q.Stats().Bytes)
		}},
	} {
		s.Run(tc.name, func() { tc.run(s.T()) })
	}
}

func (s *QueueSuite) TestSlowConsumerAndLargeBatch() {
	t := s.T()
	for _, batch := range []bool{false, true} {
		var q Queue[int]

		t.Cleanup(func() { _ = q.Abort(nil) })
		const count = 100000
		if batch {
			items := make([]Item[int], count)
			for i := range items {
				items[i] = Item[int]{i, 2}
			}
			require.NoError(t, q.PushBatch(items))
		} else {
			for i := range count {
				require.NoError(t, q.Push(i, 2))
			}
		}
		assert.Equal(t, count, q.Stats().Items)
		assert.EqualValues(t, count*2, q.Stats().Bytes)
		require.NoError(t, q.Close(nil))
		for want := range count {
			got, err := q.Pop(context.Background())
			require.NoError(t, err)
			require.Equal(t, want, got)
		}
		_, err := q.Pop(context.Background())
		assert.ErrorIs(t, err, io.EOF)
		assert.Nil(t, q.pending)
	}
}

func (s *QueueSuite) TestConcurrentProducers() {
	t := s.T()
	type value struct{ producer, index int }
	var q Queue[value]

	t.Cleanup(func() { _ = q.Abort(nil) })
	const producers = 8
	const count = 2000
	start := make(chan struct{})
	errs := make(chan error, producers)
	var workers sync.WaitGroup
	for producer := range producers {
		workers.Go(func() {
			<-start
			for i := range count {
				if err := q.Push(value{producer, i}, 1); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	finished := make(chan struct{})
	go func() { workers.Wait(); _ = q.Close(nil); close(finished) }()
	close(start)
	next := make([]int, producers)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for range producers * count {
		got, err := q.Pop(ctx)
		require.NoError(t, err)
		require.Equal(t, next[got.producer], got.index)
		next[got.producer]++
	}
	<-finished
	close(errs)
	for err := range errs {
		assert.NoError(t, err)
	}
	_, err := q.Pop(ctx)
	assert.ErrorIs(t, err, io.EOF)
	for _, n := range next {
		assert.Equal(t, count, n)
	}
}

func (s *QueueSuite) TestBatchCloseRace() {
	t := s.T()
	for range 100 {
		var q Queue[int]

		t.Cleanup(func() { _ = q.Abort(nil) })
		start := make(chan struct{})
		results := make(chan error, 2)
		var workers sync.WaitGroup
		for producer := range 2 {
			workers.Go(func() {
				<-start
				items := make([]Item[int], 100)
				for i := range items {
					items[i] = Item[int]{producer*100 + i, 1}
				}
				results <- q.PushBatch(items)
			})
		}
		workers.Go(func() { <-start; _ = q.Close(nil) })
		close(start)
		workers.Wait()
		close(results)
		accepted := 0
		for err := range results {
			if err == nil {
				accepted++
			} else {
				assert.ErrorIs(t, err, ErrClosed)
			}
		}
		assert.Equal(t, accepted*100, q.Stats().Items)
		for range accepted {
			first, err := q.Pop(context.Background())
			require.NoError(t, err)
			require.Zero(t, first%100)
			for i := 1; i < 100; i++ {
				got, err := q.Pop(context.Background())
				require.NoError(t, err)
				require.Equal(t, first+i, got)
			}
		}
		_, err := q.Pop(context.Background())
		require.ErrorIs(t, err, io.EOF)
	}
}

func (s *QueueSuite) TestWaitersWake() {
	t := s.T()
	for _, abort := range []bool{false, true} {
		var q Queue[int]

		t.Cleanup(func() { _ = q.Abort(nil) })
		const count = 10
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		results := make(chan error, count)
		for range count {
			go func() { _, err := q.Pop(ctx); results <- err }()
		}
		if abort {
			s.NoError(q.Abort(nil))
		} else {
			s.NoError(q.Close(nil))
		}
		for range count {
			s.ErrorIs(<-results, io.EOF)
		}
		cancel()
	}
	var q Queue[int]

	t.Cleanup(func() { _ = q.Abort(nil) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := make(chan int, 1)
	go func() { v, _ := q.Pop(ctx); got <- v }()
	s.NoError(q.Push(23, 0))
	s.Equal(23, <-got)
}

func (s *QueueSuite) TestStorageReleasesReferences() {
	t := s.T()
	var q Queue[*int]
	t.Cleanup(func() { _ = q.Abort(nil) })
	first, second, third := 1, 2, 3
	input := []Item[*int]{{&first, 1}, {&second, 2}, {&third, 3}}
	require.NoError(t, q.PushBatch(input))
	input[1].Value = nil // accepted envelope must be an independent copy
	got, err := q.Pop(context.Background())
	require.NoError(t, err)
	assert.Same(t, &first, got)
	backing := q.pending[:cap(q.pending)]
	require.Len(t, backing, 2)
	assert.Same(t, &second, backing[0].Value)
	got, err = q.Pop(context.Background())
	require.NoError(t, err)
	assert.Same(t, &second, got)
	assert.Nil(t, backing[0].Value)
	assert.Zero(t, backing[0].Bytes)
	require.NoError(t, q.Abort(nil))
	assert.Nil(t, backing[1].Value)
	assert.Zero(t, backing[1].Bytes)
	assert.Nil(t, q.pending)
	assert.Equal(t, Stats{Closed: true}, q.Stats())
	select {
	case <-q.ownerDone:
	default:
		t.Fatal("owner still running after abort")
	}
	select {
	case _, ok := <-q.ch.Out():
		assert.False(t, ok)
	default:
		t.Fatal("ChannX still running after abort")
	}
}

func (s *QueueSuite) TestAbortBurstAndConcurrentLifecycle() {
	t := s.T()
	for range 20 {
		var q Queue[int]
		t.Cleanup(func() { _ = q.Abort(nil) })
		for i := range 1000 {
			require.NoError(t, q.Push(i, 1))
		}
		var workers sync.WaitGroup
		results := make(chan error, 8)
		for i := range 8 {
			workers.Go(func() {
				if i%2 == 0 {
					results <- q.Close(nil)
				} else {
					results <- q.Abort(nil)
				}
			})
		}
		workers.Wait()
		close(results)
		for err := range results {
			require.NoError(t, err)
		}
		assert.Equal(t, Stats{Closed: true}, q.Stats())
		select {
		case <-q.ownerDone:
		default:
			t.Fatal("owner still running")
		}
		select {
		case _, ok := <-q.ch.Out():
			assert.False(t, ok)
		default:
			t.Fatal("ChannX manager still running")
		}
	}
}

func (s *QueueSuite) TestCancellationAtAdmissionKeepsValue() {
	t := s.T()
	for i := range 1000 {
		var q Queue[int]
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- q.Push(i, 1); cancel() }()
		got, err := q.Pop(ctx)
		require.NoError(t, <-result)
		if err != nil {
			require.ErrorIs(t, err, context.Canceled)
			got, err = q.Pop(context.Background())
			require.NoError(t, err)
		}
		assert.Equal(t, i, got)
		assert.Zero(t, q.Stats().Items)
		require.NoError(t, q.Abort(nil))
	}
}
