package eventstream_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/eventstream"
	"github.com/zoumo/agent-sdk-go/internal/queue"
)

func TestConcurrentClaimsAdmitOneConsumer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream := eventstream.New(eventstream.Hooks{})
		type result struct {
			reader agent.EventStream
			err    error
		}
		const workers = 32
		results := make(chan result, workers)
		var wait sync.WaitGroup
		for range workers {
			wait.Go(func() {
				reader, err := stream.Claim()
				results <- result{reader: reader, err: err}
			})
		}
		wait.Wait()
		close(results)
		var reader agent.EventStream
		for result := range results {
			if result.err == nil {
				require.Nil(t, reader)
				reader = result.reader
			} else {
				assertAgentReason(t, result.err, agent.ErrorFailedPrecondition, agent.ErrorReasonEventsAlreadyClaimed)
			}
		}
		require.NotNil(t, reader)
		stream.ObservationEnded(nil)
		<-stream.Done()
		_, err := reader.Next(context.Background())
		require.ErrorIs(t, err, io.EOF)
	})
}

func TestNaturalTerminationKeepsEventsClaimableAndReleasesBeforeDone(t *testing.T) {
	for _, test := range []struct {
		name  string
		cause error
	}{
		{name: "normal"},
		{name: "failed", cause: errors.New("observation failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				releasing := make(chan struct{})
				release := make(chan struct{})
				var order []string
				var begin, end eventstream.End
				stream := eventstream.New(eventstream.Hooks{
					BeginClose: func(value eventstream.End) {
						begin = value
						order = append(order, "begin")
					},
					CloseNative: func() error {
						order = append(order, "native")
						return nil
					},
					CloseEvents: func(value eventstream.End) []agent.Event {
						end = value
						order = append(order, "closures")
						return []agent.Event{{EventID: "closed-first"}, {EventID: "closed-second"}}
					},
					Release: func() {
						order = append(order, "release")
						close(releasing)
						<-release
					},
				})
				require.NoError(t, stream.Publish([]agent.Event{
					{EventID: "accepted-first"}, {EventID: "accepted-second"},
				}))
				stream.ObservationEnded(test.cause)
				<-releasing
				assertNotDone(t, stream.Done())

				reader, err := stream.Claim()
				require.NoError(t, err)
				for _, want := range []string{"accepted-first", "accepted-second", "closed-first", "closed-second"} {
					event, nextErr := reader.Next(context.Background())
					require.NoError(t, nextErr)
					assert.Equal(t, want, event.EventID)
				}
				assertTerminal(t, reader, test.cause)
				require.ErrorIs(t, stream.Publish([]agent.Event{{EventID: "too-late"}}), queue.ErrClosed)
				close(release)
				<-stream.Done()
				assert.Equal(t, []string{"begin", "native", "closures", "release"}, order)
				assert.Equal(t, eventstream.End{Cause: test.cause}, begin)
				assert.Equal(t, begin, end)
				assert.Equal(t, test.cause, stream.Err())

				_, err = stream.Claim()
				assertAgentReason(t, err, agent.ErrorFailedPrecondition, agent.ErrorReasonEventsAlreadyClaimed)
				require.NoError(t, stream.Close(context.Background()))
				assert.Equal(t, test.cause, stream.Err())
			})
		})
	}
}

func TestExplicitCloseWaitsForObservationSettlementAndCachesCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		nativeEntered := make(chan struct{})
		nativeReturn := make(chan struct{})
		closuresEntered := make(chan struct{})
		settle := make(chan struct{})
		cleanupErr := errors.New("cleanup failed")
		terminalErr := errors.New("native terminal failure after cleanup")
		var order []string
		var end eventstream.End
		stream := eventstream.New(eventstream.Hooks{
			BeginClose: func(eventstream.End) {
				order = append(order, "begin")
			},
			CloseNative: func() error {
				order = append(order, "native")
				close(nativeEntered)
				<-nativeReturn
				return cleanupErr
			},
			FinalCause: func() error {
				order = append(order, "cause")
				return terminalErr
			},
			CloseEvents: func(value eventstream.End) []agent.Event {
				end = value
				order = append(order, "closures")
				close(closuresEntered)
				<-settle
				return []agent.Event{{EventID: "closure"}}
			},
			Release: func() {
				order = append(order, "release")
			},
		})
		reader, err := stream.Claim()
		require.NoError(t, err)
		require.NoError(t, stream.Publish([]agent.Event{{EventID: "before-close"}}))
		closed := make(chan error, 1)
		go func() { closed <- stream.Close(context.Background()) }()
		<-nativeEntered
		_, err = stream.Claim()
		assertAgentReason(t, err, agent.ErrorClosed, agent.ErrorReasonSessionClosed)
		assertNotDone(t, stream.Done())
		require.NoError(t, stream.Publish([]agent.Event{{EventID: "during-close"}}))

		close(nativeReturn)
		synctest.Wait()
		select {
		case <-closuresEntered:
			t.Fatal("closure events ran before observation ended")
		default:
		}
		assertNotDone(t, stream.Done())

		stream.ObservationEnded(nil)
		<-closuresEntered
		assertNotDone(t, stream.Done())
		close(settle)
		require.ErrorIs(t, <-closed, cleanupErr)
		<-stream.Done()
		assert.Equal(t, eventstream.End{Explicit: true, Cause: terminalErr}, end)
		assert.Equal(t, []string{"begin", "native", "cause", "closures", "release"}, order)
		assert.Same(t, terminalErr, stream.Err())
		for _, want := range []string{"before-close", "during-close", "closure"} {
			event, nextErr := reader.Next(context.Background())
			require.NoError(t, nextErr)
			assert.Equal(t, want, event.EventID)
		}
		assertTerminal(t, reader, terminalErr)
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		assert.Same(t, cleanupErr, stream.Close(canceled))
		assert.Equal(t, []string{"begin", "native", "cause", "closures", "release"}, order)
	})
}

func TestExplicitCloseBeforeClaimRejectsStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		begun := make(chan struct{})
		stream := eventstream.New(eventstream.Hooks{
			BeginClose: func(eventstream.End) { close(begun) },
		})
		closed := make(chan error, 1)
		go func() { closed <- stream.Close(context.Background()) }()
		<-begun
		_, err := stream.Claim()
		assertAgentReason(t, err, agent.ErrorClosed, agent.ErrorReasonSessionClosed)
		stream.ObservationEnded(nil)
		require.NoError(t, <-closed)
		_, err = stream.Claim()
		assertAgentReason(t, err, agent.ErrorClosed, agent.ErrorReasonSessionClosed)
	})
}

func TestFirstObservationCauseRemainsStable(t *testing.T) {
	for _, test := range []struct {
		name  string
		cause error
	}{
		{name: "normal"},
		{name: "failed", cause: errors.New("first observation cause")},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				nativeEntered := make(chan struct{})
				nativeReturn := make(chan struct{})
				cleanupErr := errors.New("later cleanup error")
				stream := eventstream.New(eventstream.Hooks{
					CloseNative: func() error {
						close(nativeEntered)
						<-nativeReturn
						return cleanupErr
					},
				})
				stream.ObservationEnded(test.cause)
				<-nativeEntered
				stream.ObservationEnded(errors.New("later observation cause"))
				close(nativeReturn)
				<-stream.Done()
				stream.ObservationEnded(errors.New("cause after done"))
				assert.Equal(t, test.cause, stream.Err())
				reader, err := stream.Claim()
				require.NoError(t, err)
				assertTerminal(t, reader, test.cause)
				assert.Same(t, cleanupErr, stream.Close(context.Background()))
				assert.Equal(t, test.cause, stream.Err())
			})
		})
	}
}

func TestConcurrentExplicitAndNaturalTerminationRunsHooksOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cause := errors.New("observation failed")
		cleanupErr := errors.New("cleanup failed")
		var began, native, closures, released atomic.Int32
		var end eventstream.End
		stream := eventstream.New(eventstream.Hooks{
			BeginClose: func(eventstream.End) { began.Add(1) },
			CloseNative: func() error {
				native.Add(1)
				return cleanupErr
			},
			FinalCause: func() error { return errors.New("later native failure") },
			CloseEvents: func(value eventstream.End) []agent.Event {
				end = value
				closures.Add(1)
				return []agent.Event{{EventID: "closure"}}
			},
			Release: func() { released.Add(1) },
		})
		reader, err := stream.Claim()
		require.NoError(t, err)
		start := make(chan struct{})
		const workers = 32
		results := make(chan error, workers)
		var wait sync.WaitGroup
		for range workers {
			wait.Go(func() {
				<-start
				results <- stream.Close(context.Background())
			})
			wait.Go(func() {
				<-start
				stream.ObservationEnded(cause)
			})
		}
		close(start)
		wait.Wait()
		close(results)
		for result := range results {
			assert.Same(t, cleanupErr, result)
		}
		<-stream.Done()
		assert.Equal(t, int32(1), began.Load())
		assert.Equal(t, int32(1), native.Load())
		assert.Equal(t, int32(1), closures.Load())
		assert.Equal(t, int32(1), released.Load())
		assert.Same(t, cause, end.Cause)
		assert.Same(t, cause, stream.Err())
		event, err := reader.Next(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "closure", event.EventID)
		assertTerminal(t, reader, cause)
	})
}

func assertAgentReason(t *testing.T, err error, code agent.ErrorCode, reason string) {
	t.Helper()
	var classified *agent.Error
	require.ErrorAs(t, err, &classified)
	assert.Equal(t, code, classified.Code)
	assert.Equal(t, reason, classified.Reason)
}

func assertNotDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
		t.Fatal("Done closed before termination finished")
	default:
	}
}

func assertTerminal(t *testing.T, reader agent.EventStream, cause error) {
	t.Helper()
	for range 2 {
		_, err := reader.Next(context.Background())
		if cause == nil {
			require.ErrorIs(t, err, io.EOF)
		} else {
			assert.Same(t, cause, err)
		}
	}
}
