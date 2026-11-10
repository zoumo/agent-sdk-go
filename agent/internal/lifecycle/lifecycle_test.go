package lifecycle_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/lifecycle"
)

type fakeResource struct {
	id int
}

type fakeSession struct {
	id int
}

func TestStartAcquiresOnceAndReturnsCapabilityCopies(t *testing.T) {
	var acquireCalls, releaseCalls atomic.Int32
	resource := &fakeResource{id: 1}
	base := agent.Capabilities{
		agent.CapabilityTextInput:  true,
		agent.CapabilityPermission: true,
	}
	provider := lifecycle.New[*fakeResource, *fakeSession](
		base,
		func(context.Context) (*fakeResource, error) {
			acquireCalls.Add(1)
			return resource, nil
		},
		func(context.Context, *fakeResource) error {
			releaseCalls.Add(1)
			return nil
		},
	)

	base[agent.CapabilityForm] = true
	base[agent.CapabilityTextInput] = false
	first, err := provider.Start(context.Background())
	require.NoError(t, err)
	assert.Equal(t, agent.Capabilities{
		agent.CapabilityTextInput:  true,
		agent.CapabilityPermission: true,
	}, first)
	assert.Equal(t, int32(1), acquireCalls.Load())

	first[agent.CapabilityTextInput] = false
	first[agent.CapabilityForm] = true
	second, err := provider.Start(context.Background())
	require.NoError(t, err)
	assert.Equal(t, agent.Capabilities{
		agent.CapabilityTextInput:  true,
		agent.CapabilityPermission: true,
	}, second)
	assert.Equal(t, int32(1), acquireCalls.Load())

	active, err := provider.Resource()
	require.NoError(t, err)
	assert.Same(t, resource, active)

	require.NoError(t, provider.Stop(context.Background()))
	assert.Equal(t, int32(1), releaseCalls.Load())
}

func TestOperationsBeforeStartAreRejected(t *testing.T) {
	provider := lifecycle.New[*fakeResource, *fakeSession](
		agent.Capabilities{agent.CapabilityTextInput: true},
		func(context.Context) (*fakeResource, error) {
			return &fakeResource{}, nil
		},
		func(context.Context, *fakeResource) error {
			return nil
		},
	)

	err := provider.Open(
		context.Background(),
		func(context.Context, *fakeResource) (*fakeSession, error) {
			t.Fatal("Open callback must not run before Start")
			return nil, nil
		},
	)
	requireLifecycleError(
		t,
		err,
		agent.ErrorFailedPrecondition,
		"provider_not_started",
	)
	resource, err := provider.Resource()
	requireLifecycleError(
		t,
		err,
		agent.ErrorFailedPrecondition,
		"provider_not_started",
	)
	assert.Nil(t, resource)
	require.NoError(t, provider.Stop(context.Background()))
}

func TestFailedStartLeavesLifecycleRetryable(t *testing.T) {
	startFailure := errors.New("synthetic acquire failure")
	var acquireCalls atomic.Int32
	resource := &fakeResource{id: 2}
	provider := lifecycle.New[*fakeResource, *fakeSession](
		agent.Capabilities{agent.CapabilityTextInput: true},
		func(context.Context) (*fakeResource, error) {
			if acquireCalls.Add(1) == 1 {
				return nil, startFailure
			}
			return resource, nil
		},
		func(context.Context, *fakeResource) error {
			return nil
		},
	)

	_, err := provider.Start(context.Background())
	require.ErrorIs(t, err, startFailure)
	_, err = provider.Resource()
	requireLifecycleError(
		t,
		err,
		agent.ErrorFailedPrecondition,
		"provider_not_started",
	)

	_, err = provider.Start(context.Background())
	require.NoError(t, err)
	active, err := provider.Resource()
	require.NoError(t, err)
	assert.Same(t, resource, active)
	require.NoError(t, provider.Stop(context.Background()))
	assert.Equal(t, int32(2), acquireCalls.Load())
}

func TestStopBeforeStartIsTerminalWithoutRelease(t *testing.T) {
	var releaseCalls atomic.Int32
	var openCalls atomic.Int32
	provider := lifecycle.New[*fakeResource, *fakeSession](
		agent.Capabilities{agent.CapabilityTextInput: true},
		func(context.Context) (*fakeResource, error) {
			t.Fatal("acquire must not run after Stop")
			return nil, nil
		},
		func(context.Context, *fakeResource) error {
			releaseCalls.Add(1)
			return nil
		},
	)

	require.NoError(t, provider.Stop(context.Background()))
	require.NoError(t, provider.Stop(context.Background()))
	assert.Equal(t, int32(0), releaseCalls.Load())

	_, err := provider.Start(context.Background())
	requireLifecycleError(t, err, agent.ErrorClosed, "provider_stopped")
	err = provider.Open(
		context.Background(),
		func(context.Context, *fakeResource) (*fakeSession, error) {
			openCalls.Add(1)
			return nil, nil
		},
	)
	requireLifecycleError(t, err, agent.ErrorClosed, "provider_stopped")
	assert.Equal(t, int32(0), openCalls.Load())
	_, err = provider.Resource()
	requireLifecycleError(t, err, agent.ErrorClosed, "provider_stopped")
}

func TestSuccessfulStopIsTerminalAndReleasesOnce(t *testing.T) {
	var releaseCalls atomic.Int32
	provider := lifecycle.New[*fakeResource, *fakeSession](
		agent.Capabilities{agent.CapabilityTextInput: true},
		func(context.Context) (*fakeResource, error) {
			return &fakeResource{}, nil
		},
		func(context.Context, *fakeResource) error {
			releaseCalls.Add(1)
			return nil
		},
	)

	_, err := provider.Start(context.Background())
	require.NoError(t, err)
	require.NoError(t, provider.Stop(context.Background()))
	require.NoError(t, provider.Stop(context.Background()))
	assert.Equal(t, int32(1), releaseCalls.Load())

	_, err = provider.Start(context.Background())
	requireLifecycleError(t, err, agent.ErrorClosed, "provider_stopped")
	err = provider.Open(
		context.Background(),
		func(context.Context, *fakeResource) (*fakeSession, error) {
			return &fakeSession{}, nil
		},
	)
	requireLifecycleError(t, err, agent.ErrorClosed, "provider_stopped")
	resource, err := provider.Resource()
	requireLifecycleError(t, err, agent.ErrorClosed, "provider_stopped")
	assert.Nil(t, resource)
}

func TestOpenRegistersSessionsUntilEachIdentityCloses(t *testing.T) {
	resource := &fakeResource{}
	var openCalls atomic.Int32
	provider := lifecycle.New[*fakeResource, *fakeSession](
		agent.Capabilities{agent.CapabilityTextInput: true},
		func(context.Context) (*fakeResource, error) {
			return resource, nil
		},
		func(context.Context, *fakeResource) error {
			return nil
		},
	)
	_, err := provider.Start(context.Background())
	require.NoError(t, err)

	first := &fakeSession{id: 1}
	second := &fakeSession{id: 2}
	for _, session := range []*fakeSession{first, second} {
		err := provider.Open(
			context.Background(),
			func(_ context.Context, active *fakeResource) (*fakeSession, error) {
				openCalls.Add(1)
				assert.Same(t, resource, active)
				return session, nil
			},
		)
		require.NoError(t, err)
	}
	assert.Equal(t, int32(2), openCalls.Load())

	requireLifecycleError(
		t,
		provider.Stop(context.Background()),
		agent.ErrorFailedPrecondition,
		"provider_has_open_sessions",
	)
	provider.SessionClosed(first)
	provider.SessionClosed(first)
	requireLifecycleError(
		t,
		provider.Stop(context.Background()),
		agent.ErrorFailedPrecondition,
		"provider_has_open_sessions",
	)
	provider.SessionClosed(second)
	require.NoError(t, provider.Stop(context.Background()))
}

func TestOpenFailureSettlesInFlightReservation(t *testing.T) {
	openFailure := errors.New("synthetic identity validation failure")
	provider := lifecycle.New[*fakeResource, *fakeSession](
		agent.Capabilities{agent.CapabilityTextInput: true},
		func(context.Context) (*fakeResource, error) {
			return &fakeResource{}, nil
		},
		func(context.Context, *fakeResource) error {
			return nil
		},
	)
	_, err := provider.Start(context.Background())
	require.NoError(t, err)

	err = provider.Open(
		context.Background(),
		func(context.Context, *fakeResource) (*fakeSession, error) {
			return nil, openFailure
		},
	)
	require.ErrorIs(t, err, openFailure)
	require.NoError(t, provider.Stop(context.Background()))
}

func TestStopRejectsInFlightOpenWithoutRelease(t *testing.T) {
	var releaseCalls atomic.Int32
	provider := lifecycle.New[*fakeResource, *fakeSession](
		agent.Capabilities{agent.CapabilityTextInput: true},
		func(context.Context) (*fakeResource, error) {
			return &fakeResource{}, nil
		},
		func(context.Context, *fakeResource) error {
			releaseCalls.Add(1)
			return nil
		},
	)
	_, err := provider.Start(context.Background())
	require.NoError(t, err)

	openEntered := make(chan struct{})
	releaseOpen := make(chan struct{})
	var releaseOnce sync.Once
	finishOpen := func() {
		releaseOnce.Do(func() {
			close(releaseOpen)
		})
	}
	defer finishOpen()
	opened := &fakeSession{}
	openResult := make(chan error, 1)
	go func() {
		openResult <- provider.Open(
			context.Background(),
			func(context.Context, *fakeResource) (*fakeSession, error) {
				close(openEntered)
				<-releaseOpen
				return opened, nil
			},
		)
	}()
	receiveWithin(t, openEntered, "Open callback entry")

	stopResult := make(chan error, 1)
	go func() {
		stopResult <- provider.Stop(context.Background())
	}()
	stopErr := receiveWithin(t, stopResult, "Stop rejection during Open")
	requireLifecycleError(
		t,
		stopErr,
		agent.ErrorFailedPrecondition,
		"provider_has_open_sessions",
	)
	assert.Equal(t, int32(0), releaseCalls.Load())

	finishOpen()
	require.NoError(t, receiveWithin(t, openResult, "Open completion"))
	requireLifecycleError(
		t,
		provider.Stop(context.Background()),
		agent.ErrorFailedPrecondition,
		"provider_has_open_sessions",
	)
	provider.SessionClosed(opened)
	require.NoError(t, provider.Stop(context.Background()))
	assert.Equal(t, int32(1), releaseCalls.Load())
}

func TestStopAdmissionBlocksLaterOperationsUntilReleaseReturns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	releaseFailure := errors.New("synthetic release failure")
	releaseEntered := make(chan struct{})
	releaseReturned := make(chan struct{})
	var releaseEntryOnce, releaseReturnOnce sync.Once
	finishRelease := func() {
		releaseReturnOnce.Do(func() {
			close(releaseReturned)
		})
	}
	defer finishRelease()

	var releaseCalls atomic.Int32
	provider := lifecycle.New[*fakeResource, *fakeSession](
		agent.Capabilities{agent.CapabilityTextInput: true},
		func(context.Context) (*fakeResource, error) {
			return &fakeResource{}, nil
		},
		func(context.Context, *fakeResource) error {
			releaseCalls.Add(1)
			releaseEntryOnce.Do(func() {
				close(releaseEntered)
			})
			<-releaseReturned
			return releaseFailure
		},
	)
	_, err := provider.Start(ctx)
	require.NoError(t, err)

	stopResult := make(chan error, 1)
	go func() {
		stopResult <- provider.Stop(ctx)
	}()
	receiveWithin(t, releaseEntered, "release callback entry")
	assert.Equal(t, int32(1), releaseCalls.Load())

	type startResult struct {
		capabilities agent.Capabilities
		err          error
	}
	startEntered := make(chan struct{})
	starts := make(chan startResult, 1)
	go func() {
		close(startEntered)
		capabilities, startErr := provider.Start(ctx)
		starts <- startResult{capabilities: capabilities, err: startErr}
	}()
	receiveWithin(t, startEntered, "later Start entry")

	openEntered := make(chan struct{})
	var openCallbacks atomic.Int32
	opens := make(chan error, 1)
	go func() {
		close(openEntered)
		opens <- provider.Open(
			ctx,
			func(context.Context, *fakeResource) (*fakeSession, error) {
				openCallbacks.Add(1)
				return &fakeSession{}, nil
			},
		)
	}()
	receiveWithin(t, openEntered, "later Open entry")

	repeatedStopEntered := make(chan struct{})
	repeatedStops := make(chan error, 1)
	go func() {
		close(repeatedStopEntered)
		repeatedStops <- provider.Stop(ctx)
	}()
	receiveWithin(t, repeatedStopEntered, "repeated Stop entry")

	select {
	case result := <-starts:
		require.FailNowf(t, "Start crossed an admitted Stop", "result: %+v", result)
	case openErr := <-opens:
		require.FailNowf(t, "Open crossed an admitted Stop", "error: %v", openErr)
	case repeatedStopErr := <-repeatedStops:
		require.FailNowf(t, "repeated Stop returned early", "error: %v", repeatedStopErr)
	case <-time.After(50 * time.Millisecond):
	}

	finishRelease()

	firstStopErr := receiveWithin(t, stopResult, "admitted Stop completion")
	require.ErrorIs(t, firstStopErr, releaseFailure)
	start := receiveWithin(t, starts, "later Start result")
	assert.Nil(t, start.capabilities)
	requireLifecycleError(
		t,
		start.err,
		agent.ErrorClosed,
		"provider_stopped",
	)
	requireLifecycleError(
		t,
		receiveWithin(t, opens, "later Open result"),
		agent.ErrorClosed,
		"provider_stopped",
	)
	assert.Zero(t, openCallbacks.Load())

	repeatedStopErr := receiveWithin(t, repeatedStops, "repeated Stop result")
	require.ErrorIs(t, repeatedStopErr, releaseFailure)
	require.Same(t, firstStopErr, repeatedStopErr)
	require.Same(t, firstStopErr, provider.Stop(ctx))
	assert.Equal(t, int32(1), releaseCalls.Load())

	_, err = provider.Start(ctx)
	requireLifecycleError(t, err, agent.ErrorClosed, "provider_stopped")
	err = provider.Open(
		ctx,
		func(context.Context, *fakeResource) (*fakeSession, error) {
			openCallbacks.Add(1)
			return &fakeSession{}, nil
		},
	)
	requireLifecycleError(t, err, agent.ErrorClosed, "provider_stopped")
	assert.Zero(t, openCallbacks.Load())
}

func TestConcurrentStartAcquiresOnce(t *testing.T) {
	var acquireCalls atomic.Int32
	acquireEntered := make(chan struct{})
	releaseAcquire := make(chan struct{})
	var enteredOnce sync.Once
	provider := lifecycle.New[*fakeResource, *fakeSession](
		agent.Capabilities{agent.CapabilityTextInput: true},
		func(context.Context) (*fakeResource, error) {
			acquireCalls.Add(1)
			enteredOnce.Do(func() {
				close(acquireEntered)
			})
			<-releaseAcquire
			return &fakeResource{}, nil
		},
		func(context.Context, *fakeResource) error {
			return nil
		},
	)

	type startResult struct {
		capabilities agent.Capabilities
		err          error
	}
	const callers = 8
	results := make(chan startResult, callers)
	for range callers {
		go func() {
			capabilities, err := provider.Start(context.Background())
			results <- startResult{capabilities: capabilities, err: err}
		}()
	}
	receiveWithin(t, acquireEntered, "acquisition entry")
	close(releaseAcquire)

	for range callers {
		result := receiveWithin(t, results, "concurrent Start result")
		require.NoError(t, result.err)
		assert.Equal(t, agent.Capabilities{
			agent.CapabilityTextInput: true,
		}, result.capabilities)
	}
	assert.Equal(t, int32(1), acquireCalls.Load())
	require.NoError(t, provider.Stop(context.Background()))
}

func TestReleaseFailureIsTerminalAndCached(t *testing.T) {
	releaseFailure := errors.New("synthetic release failure")
	var releaseCalls atomic.Int32
	provider := lifecycle.New[*fakeResource, *fakeSession](
		agent.Capabilities{agent.CapabilityTextInput: true},
		func(context.Context) (*fakeResource, error) {
			return &fakeResource{}, nil
		},
		func(context.Context, *fakeResource) error {
			releaseCalls.Add(1)
			return releaseFailure
		},
	)
	_, err := provider.Start(context.Background())
	require.NoError(t, err)

	first := provider.Stop(context.Background())
	second := provider.Stop(context.Background())
	require.ErrorIs(t, first, releaseFailure)
	assert.Same(t, first, second)
	assert.Equal(t, int32(1), releaseCalls.Load())

	_, err = provider.Start(context.Background())
	requireLifecycleError(t, err, agent.ErrorClosed, "provider_stopped")
	resource, err := provider.Resource()
	requireLifecycleError(t, err, agent.ErrorClosed, "provider_stopped")
	assert.Nil(t, resource)
}

func TestLifecycleSupportsNoProviderResource(t *testing.T) {
	provider := lifecycle.New[struct{}, *fakeSession](
		agent.Capabilities{agent.CapabilityTextInput: true},
		nil,
		nil,
	)

	capabilities, err := provider.Start(context.Background())
	require.NoError(t, err)
	assert.Equal(t, agent.Capabilities{
		agent.CapabilityTextInput: true,
	}, capabilities)
	_, err = provider.Resource()
	require.NoError(t, err)
	require.NoError(t, provider.Stop(context.Background()))
}

func requireLifecycleError(
	t *testing.T,
	err error,
	code agent.ErrorCode,
	reason string,
) {
	t.Helper()
	require.Error(t, err)
	var classified *agent.Error
	require.ErrorAs(t, err, &classified)
	assert.Equal(t, code, classified.Code)
	assert.Equal(t, reason, classified.Reason)
	assert.NotEmpty(t, classified.Message)
}

func receiveWithin[T any](t *testing.T, values <-chan T, operation string) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", operation)
		var zero T
		return zero
	}
}
