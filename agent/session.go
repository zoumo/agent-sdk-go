package agent

import "context"

// Session owns one native main-session attachment, its interaction authority,
// and native resources whose lifetime ends with the session.
type Session interface {
	// ID is a confirmed native main-session identity and is nonempty.
	ID() string

	Capabilities() Capabilities
	EffectiveFeatures() EffectiveFeatures

	// Events returns the session's only event stream. Only the first successful
	// call may return a stream; later calls must fail. A stream has one
	// consumer and retains accepted events for draining after Close.
	Events() (EventStream, error)

	// Submit validates and copies input before writing to the native protocol.
	// It fails with failed_precondition while prior work remains unsettled.
	Submit(ctx context.Context, input Input) (InputReceipt, error)

	// Answer rechecks the live session, current work, request identity, and
	// answer kind before sending. An interaction is answerable at most once.
	Answer(
		ctx context.Context,
		interaction InteractionRef,
		answer Answer,
	) (InteractionReceipt, error)

	// Interrupt targets work already confirmed for this session. It returns
	// no_active_work without a native interrupt when no work is active.
	Interrupt(
		ctx context.Context,
		options InterruptOptions,
	) (InterruptReceipt, error)

	// Close releases this session attachment and its session-owned resources
	// without stopping the provider or recursively stopping all child agents. It
	// rejects new operations, invalidates unsettled interactions, does not wait
	// for external consumers, and returns the same cleanup result when repeated.
	Close(ctx context.Context) error

	// Done closes when session interaction terminates. Err retains the first
	// stable termination cause.
	Done() <-chan struct{}
	Err() error
}

// Acceptance records three independent stages of native acceptance.
type Acceptance struct {
	Sent           *bool
	NativeAccepted *bool
	NativeRecorded *bool
}

// InputReceipt identifies a submission and the acceptance stages observed for
// it. Submission identifies the submitted input; it does not prove execution
// started or completed.
type InputReceipt struct {
	Submission string
	Acceptance Acceptance
}

// InteractionReceipt identifies the answered interaction and acceptance
// stages observed during the answer operation.
type InteractionReceipt struct {
	Interaction InteractionRef
	Acceptance  Acceptance
}

// InterruptOptions is reserved for provider-neutral interrupt options.
type InterruptOptions struct{}

// InterruptResult describes the native control outcome of an interrupt.
type InterruptResult string

const (
	InterruptAcknowledged InterruptResult = "control_acknowledged"
	InterruptNoActiveWork InterruptResult = "no_active_work"
)

// InterruptReceipt identifies the target work known to the session and the
// control acknowledgement observed for the interrupt.
type InterruptReceipt struct {
	Target string
	Result InterruptResult
}
