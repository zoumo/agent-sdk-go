package agent

import "context"

// Injector is an optional Session operation that appends user context without
// requesting a model response. Native consumption timing is provider-specific.
// Inject never starts, steers, interrupts, queues or retries work on the caller's
// behalf. Callers must serialize it with other input operations for ordering.
type Injector interface {
	Inject(ctx context.Context, input Input) (InjectReceipt, error)
}

// InjectReceipt records only the observed acceptance stages of an injection.
// It has no work identity and does not prove model consumption or persistence.
// An outcome_unknown error must not be retried without reconciliation.
type InjectReceipt struct {
	Acceptance Acceptance
}
