// Package agent defines the optional provider-neutral contract for one live
// Agent session.
//
// agentcodex.New and agentclaude.New construct concrete providers that implement
// Provider. A Provider owns its lifecycle and any native resources shared across
// sessions; an adapter may instead place native resources, such as Claude CLI
// processes, in each Session. After Start, Open creates a main session and Resume
// restores an existing native identity; both return the same Session contract.
// Each Session exposes one EventStream to one consumer, accepts text through
// Submit, answers original requests through opaque InteractionRef values, and
// releases its own attachment and session-owned resources through Close.
//
// Event payloads form a closed live union with stable session and item identity.
// Adapters preserve native order, time, raw evidence, and explicit capability
// gaps. They do not invent cross-provider equivalence.
//
// ToolCallPayload and ToolResultPayload identify a tool with the required
// (Provider, ToolName) pair and SDK-assigned Kind. Provider accepts exactly
// "agent", "dynamic", "mcp", and "mcp:<server>"; the empty "mcp:" suffix is
// invalid, while a nonempty server suffix may contain further colons. Kind is
// an ACP-derived action category, not a permission decision or proof that tools
// from different providers behave equivalently. A result copies the identity
// observed on its call. ToolChunkPayload is deliberately only {id, content}
// because a chunk can arrive before the complete tool identity.
//
// Tool status remains a provider-specific lifecycle projection. Codex retains
// its in-progress, completed, failed, declined and unknown values; Claude omits
// call status and uses completed or failed results. Tool JSON requires kind,
// toolName and provider and has no scope field. This unreleased schema is
// intentionally breaking: strict decoding rejects the legacy name member and
// old arbitrary kind strings, with no compatibility alias.
//
// This package intentionally has no unified history, LoadSession, replay, root
// client, or multi-session aggregation. Codex and Claude keep their native
// history APIs, and Resume does not turn saved content into live events. MASS
// resource identity and persistence also remain outside this contract.
//
// ExampleProvider runs the complete open, submit, stream, answer, close, resume,
// and stop flow over two synthetic providers without launching a native CLI.
package agent
