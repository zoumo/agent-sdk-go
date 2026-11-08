package agent

import "context"

// Provider owns provider-scoped lifecycle resources. An adapter may share one
// native connection across sessions or create native resources per session.
type Provider interface {
	// Start acquires provider-scoped resources and enters the started state.
	// Repeated calls return the same capabilities without creating another
	// owner. After a successful Stop, Start cannot be called again.
	Start(ctx context.Context) (Capabilities, error)

	// Open creates a new native main session after its real identity is known.
	// The provider must have been started successfully.
	Open(ctx context.Context, options OpenOptions) (Session, error)

	// Resume restores an existing native main session instead of creating one.
	// The provider must have been started successfully. Saved native history may
	// support recovery but must not be emitted as live events.
	Resume(ctx context.Context, options ResumeOptions) (Session, error)

	// Stop releases only resources owned by this provider. It rejects the call
	// while any session remains open. A successful stop is terminal: Start,
	// Open, and Resume cannot run afterward. Repeating Stop after all sessions
	// are closed returns the same successful result.
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
