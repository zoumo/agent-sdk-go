package agent

import "context"

// Provider owns the shared connection or process used by its sessions.
type Provider interface {
	// Start acquires the provider-owned shared runtime resources. Repeated calls
	// return the same capabilities without creating another owner.
	Start(ctx context.Context) (Capabilities, error)

	// Open creates a new native main session after its real identity is known.
	Open(ctx context.Context, options OpenOptions) (Session, error)

	// Resume restores an existing native main session instead of creating one.
	Resume(ctx context.Context, options ResumeOptions) (Session, error)

	// Stop releases only resources owned by this provider. It must reject the
	// call while any session remains open.
	Stop(ctx context.Context) error
}

// InitialInputProvider supports providers that need an initial input before
// native session identity can be confirmed.
type InitialInputProvider interface {
	// ValidateInitialInput validates without opening a session or writing to
	// the native protocol.
	ValidateInitialInput(input Input) error

	OpenWithInput(
		ctx context.Context,
		options OpenOptions,
		input Input,
	) (Session, error)

	ResumeWithInput(
		ctx context.Context,
		options ResumeOptions,
		input Input,
	) (Session, error)
}
