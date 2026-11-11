// Package corelease owns the Core-scoped state shared by unified Agent
// adapters. It stores native resources opaquely and never invokes Session
// methods.
package corelease

import (
	"context"
	"maps"
	"sync"

	"github.com/zoumo/agent-sdk-go/agent"
)

type state uint8

const (
	stateNew state = iota
	stateStarted
	stateStopped
)

// Core owns one Core-scoped resource and the identities of Sessions
// opened from it. The adapter supplies resource acquisition, release and the
// base capabilities.
type Core[R any, I comparable] struct {
	mu           sync.Mutex
	capabilities agent.Capabilities
	acquire      func(context.Context) (R, error)
	release      func(context.Context, R) error
	state        state
	resource     R
	opening      int
	sessions     map[I]struct{}
	stopErr      error
}

// New constructs Core lease tracking. Acquire and release callbacks run while
// Core mutation is serialized and must not re-enter this module. A nil
// acquire and release support adapters without a Core-scoped resource.
func New[R any, I comparable](
	capabilities agent.Capabilities,
	acquire func(context.Context) (R, error),
	release func(context.Context, R) error,
) *Core[R, I] {
	return &Core[R, I]{
		capabilities: maps.Clone(capabilities),
		acquire:      acquire,
		release:      release,
	}
}

// Start acquires the Core resource once and returns a copy of the base
// capabilities. A failed acquisition leaves the lifecycle retryable.
func (l *Core[R, I]) Start(ctx context.Context) (agent.Capabilities, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	switch l.state {
	case stateStarted:
		return maps.Clone(l.capabilities), nil
	case stateStopped:
		return nil, coreStoppedError()
	}

	var (
		resource R
		err      error
	)
	if l.acquire != nil {
		resource, err = l.acquire(ctx)
		if err != nil {
			return nil, err
		}
	}
	l.resource = resource
	l.state = stateStarted
	return maps.Clone(l.capabilities), nil
}

// Open reserves one in-flight operation, runs open outside the lifecycle lock,
// and settles the reservation automatically. A successful callback registers
// the returned Session identity; a failed callback registers nothing.
func (l *Core[R, I]) Open(
	ctx context.Context,
	open func(context.Context, R) (I, error),
) (I, error) {
	l.mu.Lock()
	switch l.state {
	case stateStarted:
	case stateStopped:
		l.mu.Unlock()
		var zero I
		return zero, coreStoppedError()
	default:
		l.mu.Unlock()
		var zero I
		return zero, coreNotStartedError()
	}
	l.opening++
	resource := l.resource
	l.mu.Unlock()

	identity, err := open(ctx, resource)

	l.mu.Lock()
	defer l.mu.Unlock()
	l.opening--
	if err != nil {
		var zero I
		return zero, err
	}
	if l.sessions == nil {
		l.sessions = make(map[I]struct{})
	}
	l.sessions[identity] = struct{}{}
	return identity, nil
}

// Resource returns the active Core resource. It never creates a new
// resource on demand.
func (l *Core[R, I]) Resource() (R, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	switch l.state {
	case stateStarted:
		return l.resource, nil
	case stateStopped:
		var zero R
		return zero, coreStoppedError()
	default:
		var zero R
		return zero, coreNotStartedError()
	}
}

// SessionClosed removes a Session identity. Repeated calls are harmless.
func (l *Core[R, I]) SessionClosed(identity I) {
	l.mu.Lock()
	delete(l.sessions, identity)
	l.mu.Unlock()
}

// Stop is terminal once admitted. It rejects while an Open is in flight or any
// Session remains registered, releases at most once, and caches the result.
func (l *Core[R, I]) Stop(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	switch l.state {
	case stateStopped:
		return l.stopErr
	case stateNew:
		l.state = stateStopped
		return nil
	}
	if l.opening != 0 || len(l.sessions) != 0 {
		return coreHasOpenSessionsError()
	}

	l.state = stateStopped
	if l.release != nil {
		l.stopErr = l.release(ctx, l.resource)
	} else {
		l.stopErr = nil
	}
	var zero R
	l.resource = zero
	return l.stopErr
}

func coreStoppedError() error {
	return &agent.Error{
		Code:    agent.ErrorClosed,
		Reason:  agent.ErrorReasonCoreStopped,
		Message: "core has been stopped",
	}
}

func coreNotStartedError() error {
	return &agent.Error{
		Code:    agent.ErrorFailedPrecondition,
		Reason:  agent.ErrorReasonCoreNotStarted,
		Message: "core must be started before opening a session",
	}
}

func coreHasOpenSessionsError() error {
	return &agent.Error{
		Code:    agent.ErrorFailedPrecondition,
		Reason:  agent.ErrorReasonCoreHasOpenSessions,
		Message: "core cannot stop while sessions are open",
	}
}
