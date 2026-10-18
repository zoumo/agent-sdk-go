package queue

import (
	"context"
	"errors"
	"io"
	"math"
	"reflect"
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
			assert.Equal(t, Stats{}, q.Stats())
			require.NoError(t, q.Push(nil, 4))
			require.Equal(t, 1, q.Stats().Items)
			assert.EqualValues(t, 4, q.Stats().Bytes)
			assert.Positive(t, q.Stats().OldestAge)
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
			terminal := errors.New("first failure")
			require.NoError(t, q.Push(1, 6))
			require.NoError(t, q.Close(terminal))
			require.NoError(t, q.Abort(io.EOF))
			_, err := q.Pop(context.Background())
			assert.ErrorIs(t, err, terminal)
			assert.Equal(t, Stats{Closed: true}, q.Stats())
			assert.Nil(t, q.ring)
		}},
		{"nil terminal causes", func(t *testing.T) {
			for _, abort := range []bool{false, true} {
				var q Queue[int]
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
				require.NoError(t, q.Push(0, 1))
				require.ErrorIs(t, q.PushBatch(invalid.items), invalid.err)
				assert.Equal(t, 1, q.Stats().Items)
				assert.EqualValues(t, 1, q.Stats().Bytes)
				got, err := q.Pop(context.Background())
				require.NoError(t, err)
				assert.Zero(t, got)
			}
			var q Queue[int]
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
		assert.Nil(t, q.ring)
	}
}

func (s *QueueSuite) TestConcurrentProducers() {
	t := s.T()
	type value struct{ producer, index int }
	var q Queue[value]
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
	for _, abort := range []bool{false, true} {
		var q Queue[int]
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := make(chan int, 1)
	go func() { v, _ := q.Pop(ctx); got <- v }()
	s.NoError(q.Push(23, 0))
	s.Equal(23, <-got)
}

func (s *QueueSuite) TestStorageReleasesReferences() {
	var q Queue[*int]
	first, second := 1, 2
	s.Require().NoError(q.Push(&first, 1))
	s.Require().NoError(q.Push(&second, 1))
	ring := q.ring
	_, err := q.Pop(context.Background())
	s.Require().NoError(err)
	// Inspect the upstream backing slice without unsafe: the popped interface
	// must be nil while the still-pending slot remains referenced.
	backing := reflect.ValueOf(ring).Elem().FieldByName("buf")
	s.True(backing.Index(0).IsNil())
	s.False(backing.Index(1).IsNil())
	_, err = q.Pop(context.Background())
	s.Require().NoError(err)
	s.True(backing.Index(1).IsNil())
	s.Nil(q.ring)
	for range 10000 {
		s.Require().NoError(q.Push(&first, 1))
	}
	s.Greater(q.ring.Cap(), 64)
	s.NoError(q.Abort(nil))
	s.Nil(q.ring)
	s.Equal(Stats{Closed: true}, q.Stats())
}
