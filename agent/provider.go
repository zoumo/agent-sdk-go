package agent

import "context"

// Provider owns provider-scoped lifecycle resources. An adapter may share one
// native connection across sessions or create native resources per session.
type Provider interface {
	// Start acquires provider-scoped resources and enters the started state.
	// It returns the base capabilities supplied by the adapter; the lifecycle
	// freezes a copy and returns copies from repeated calls without creating
	// another owner. After an admitted Stop, Start cannot be called again.
	Start(ctx context.Context) (Capabilities, error)

	// Open creates a new native main session after its real identity is known.
	// The provider must have been started successfully.
	Open(ctx context.Context, options OpenOptions) (Session, error)

	// Resume restores an existing native main session instead of creating one.
	// The provider must have been started successfully. Saved native history may
	// support recovery but must not be emitted as live events.
	Resume(ctx context.Context, options ResumeOptions) (Session, error)

	// Stop releases only resources owned by this provider. It rejects the call
	// while any Open or Resume is in flight or any session remains open. Once
	// admitted, Stop is terminal even if release fails: it releases at most
	// once, caches the release result, and prevents later Start, Open, or Resume.
	// Repeating Stop returns the same cached result.
	Stop(ctx context.Context) error
}
