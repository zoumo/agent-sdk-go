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
// ToolCallPayload and ToolResultPayload carry the required provider-native
// (ToolName, Provider) identity and SDK-assigned Kind. ToolName and Provider
// form the identity; Kind is an ACP-derived action category, not a permission
// decision or proof that tools from different providers behave equivalently. A
// result copies the provider, tool name and kind observed on its call.
// ToolChunkPayload is deliberately only {id, content} because a chunk can
// arrive before the complete tool identity.
//
// Tool status remains a provider-specific lifecycle projection. Tool call and
// result JSON requires kind, toolName and provider and has no scope field. This
// unreleased schema is intentionally breaking: strict decoding rejects the
// legacy name member and old arbitrary kind strings, with no compatibility
// alias. The accepted provider forms, kind boundaries, provider mappings and
// exact validation rules are defined by ToolProvider, ToolKind and the
// unified-agent design under harness/modules/unified-agent.
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
// OpenOptions.AdditionalSystemPrompt adds optional caller text for that Open or
// Resume call only. Empty omits native prompt configuration. A nonempty value
// must be valid UTF-8 and contain no NUL; it is forwarded literally without
// trimming or normalization. Codex maps it to developerInstructions, never
// baseInstructions. Claude maps it to one --append-system-prompt=<text> argv
// element. The option has no runtime setter and adds no Capability, Feature,
// Event, Meta member or persistent Session state. Resume requires the complete
// value again and does not restore an earlier SDK value; an empty resume request
// does not promise to clear native saved instructions. The SDK makes no exact
// ordering or child-Agent inheritance promise. Native app-server requests and
// Claude argv may expose the text, so it is not secret storage. Unified validation
// and adapter errors, Events and logs do not echo it. The former unpublished
// SystemPrompt field is removed without a compatibility alias.
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
// ExampleProvider runs the complete open, start-or-steer, stream, answer, close,
// resume, and stop flow over two synthetic providers without launching a native
// CLI.
// ExampleMCPServer validates an offline final MCP list without connecting to a
// server.
package agent
