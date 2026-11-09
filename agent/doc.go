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
// This package intentionally has no unified history, LoadSession, replay, root
// client, or multi-session aggregation. Codex and Claude keep their native
// history APIs, and Resume does not turn saved content into live events. MASS
// resource identity and persistence also remain outside this contract.
//
// OpenOptions.MCPServers carries the final provider-neutral external MCP list for
// one Open or Resume call. The caller that interprets Runtime configuration must
// compose Runtime defaults and the Session declaration before calling a provider.
// Providers do not merge or persist that list; Resume requires a new final list.
// The repository README section "External MCP servers" describes the composition
// and provider-specific limits.
//
// The unified contract has only explicit stdio and MCP Streamable HTTP transports.
// It has no SSE, MCPConfigMode, StrictMCPConfig, SDKMCPServers, or ambient-clear
// control. Provider-specific field restrictions are rejected before native side
// effects. The SDK forwards environment and header values without resolving them;
// it does not persist MCP configuration or include its values in unified Events,
// Meta, or logs. MCP validation and adapter field-support errors do not echo those
// values. Open or Resume errors can wrap native diagnostics as a cause, which
// callers must protect separately. Open or Resume success does not prove MCP
// connectivity, authentication, or tool discovery.
//
// ExampleProvider runs the complete open, submit, stream, answer, close, resume,
// and stop flow over two synthetic providers without launching a native CLI.
// ExampleMCPServer validates an offline final MCP list without connecting to a
// server.
package agent
