// Package agent defines the optional provider-neutral contract for one live
// Agent session.
//
// agentcodex.New and agentclaude.New construct concrete providers that implement
// Provider. A Provider owns its lifecycle and any native resources shared across
// sessions; an adapter may instead place native resources, such as Claude CLI
// processes, in each Session. After Start, Open creates a main session and Resume
// restores an existing native identity; both return the same Session contract.
// Each Session exposes one EventStream to one consumer, accepts text through
// StartOrSteer, answers original requests through opaque InteractionRef values,
// and releases its own attachment and session-owned resources through Close.
// StartOrSteer lets the adapter start new work, join acceptable current work, or
// report that input was not applied; it never queues, waits for idle, persists,
// or retries.
//
// Event payloads form a closed live union with stable session and item identity.
// Adapters preserve native order, time, raw evidence, and explicit capability
// gaps. They do not invent cross-provider equivalence.
//
// This package intentionally has no unified history, LoadSession, replay, root
// client, or multi-session aggregation. Codex and Claude keep their native
// history APIs, and Resume does not turn saved content into live events. MASS
// resource identity and persistence also remain outside this contract.
//
// ExampleProvider runs the complete open, start-or-steer, stream, answer, close,
// resume, and stop flow over two synthetic providers without launching a native
// CLI.
package agent
