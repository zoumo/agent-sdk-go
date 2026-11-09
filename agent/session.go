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

	// StartOrSteer validates and copies input before writing to the native
	// protocol. It tries to join acceptable active work, otherwise it starts or
	// hands the input to native scheduling. It never queues, waits for idle,
	// persists, or retries. An adapter may retain internal attribution state for
	// accepted input without claiming the provider scheduled it. InputNotApplied
	// leaves Submission empty and leaves the input with the caller.
	StartOrSteer(ctx context.Context, input Input) (InputReceipt, error)

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

// InputResult describes what the provider could truthfully confirm about input
// after StartOrSteer. The values are part of the public contract.
type InputResult string

const (
	// InputAppliedCurrentWork means the provider accepted the input as current
	// work already observed for the session.
	InputAppliedCurrentWork InputResult = "applied_current_work"
	// InputAppliedNewWork means the provider accepted the input with a new work
	// identity, or no current work was known before the operation.
	InputAppliedNewWork InputResult = "applied_new_work"
	// InputAppliedBestEffort means the local write succeeded but the provider did
	// not prove where or whether the input was scheduled. An adapter may retain
	// internal candidate state for attribution without changing this result.
	InputAppliedBestEffort InputResult = "applied_best_effort"
	// InputNotApplied means the input was definitely not applied. Submission is
	// empty; the caller retains the input and controls any retry.
	InputNotApplied InputResult = "not_applied"
)

// InputReceipt identifies a submission, describes the observed result, and
// records the acceptance stages observed for it. Submission identifies the
// applied input; it is empty for InputNotApplied and does not prove execution
// started or completed.
type InputReceipt struct {
	Submission string
	Result     InputResult
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
