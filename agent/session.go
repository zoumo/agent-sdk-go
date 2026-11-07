package agent

import "context"

// Session owns one native main session attachment and its interaction
// authority. It does not own the provider's shared connection or process.
type Session interface {
	// ID is a confirmed native main-session identity and is nonempty.
	ID() string

	Capabilities() Capabilities
	EffectiveFeatures() EffectiveFeatures

	// Events returns the session's only event stream. Only the first successful
	// call may return a stream; later calls must fail.
	Events() (EventStream, error)

	Submit(ctx context.Context, input Input) (InputReceipt, error)
	Answer(
		ctx context.Context,
		interaction InteractionRef,
		answer Answer,
	) (InteractionReceipt, error)
	Interrupt(
		ctx context.Context,
		options InterruptOptions,
	) (InterruptReceipt, error)

	// Close releases this session attachment without stopping the provider and
	// without recursively stopping all child agents.
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
