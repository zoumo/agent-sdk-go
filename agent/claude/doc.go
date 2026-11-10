// Package agentclaude adapts the native Claude Session to the provider-neutral
// live agent contract.
//
// Provider Start only makes the provider lifecycle ready; each Session owns its
// native Claude CLI process. Open waits for a live native init before returning
// the real session identity. Resume first confirms the reviewed CLI version and
// the requested saved transcript under Config.ConfigDir, then accepts matching
// live init or its private SessionStart callback as the resumed identity.
// Saved replay material is not converted into live events.
//
// The adapter maps Claude user and assistant text and thinking, message and
// content-block deltas, tool calls and results, session state, usage, available
// commands, native errors, permission requests, and AskUserQuestion forms. A
// text-only user content array becomes one UserMessage content array, and a
// tool-result-only array emits tool results. Although the native message type
// permits text and tool_result in one user content array, observed Claude output
// does not produce that mixed shape. The adapter rejects it with a
// user_message_mixed_content_unsupported gap and emits no partial events.
// Unsupported blocks otherwise are reported as gaps. Assistant frame UUIDs are
// deduplicated only within their active response; response-scoped block and UUID
// state is released when the next response starts. An aborted assistant frame is
// reported rather than presented as complete content.
// Message, response, block, and tool identities remain in event item IDs and
// Meta. The provider declares text input, permission, and form capabilities;
// disabling AgentQuestions removes form support and disables the native tool.
//
// Tool calls and results preserve the exact native tool_use.name, including the
// complete mcp__... name. Their Claude-specific Provider and Kind mappings,
// result-copy rule and status projection are defined in
// harness/modules/unified-agent/design.md. Tool chunks remain {id, content}.
//
// Input is one plain text block without annotations. StartOrSteer writes with
// priority="next" and shouldQuery=true and reports only best-effort local write
// success. Internally it retains the submission as a possible interaction and
// interrupt target until native idle; that candidate does not claim the input
// was scheduled. Native state observations establish active work. System
// prompts cannot be replaced or changed after startup. A nonempty unified
// AdditionalSystemPrompt maps to native AppendSystemPrompt and exactly one
// --append-system-prompt=<text> argv element; empty omits it, and Resume requires
// the value supplied for that call. The complete field contract is defined in
// harness/modules/unified-agent/design.md#additional-system-prompt. Additional
// directories, URL interactions, multi-block input, and native frames or answer
// extensions without an exact mapping are not advertised. TodoWrite, TaskCreate,
// and TaskUpdate remain ordinary tool calls with their native input; they do not
// also produce a provider-neutral plan event. AskUserQuestion accepts string
// answers and, for multi-select questions, string arrays. Multi-select arrays
// are joined with ", " before the native write; native array answers are not
// supported.
// Open and Resume pass the final provider-neutral external MCP list to native
// Claude stdio or Streamable HTTP configuration. Stdio maps Command, Args, and
// Env; Streamable HTTP maps URL and Headers. Cwd, EnvHeaders, and
// BearerTokenEnvVar are rejected as unsupported before process launch rather than
// being dropped or rewritten. Native SSE, SDK-hosted MCP, and strict config remain
// available only through the claude package. The unified adapter does not merge
// ambient configuration: same-name precedence is target-CLI behavior and may vary
// by version or policy. Enterprise policy may also reject dynamic or strict native
// MCP configuration. A successful Open or Resume does not prove MCP connectivity,
// authentication, or tool discovery.
// Initialization, internal control responses, and positive member or parent
// relationship confirmations are lifecycle plumbing and produce no unified
// event. Confirmed source and parent identities remain in the Meta of subsequent
// native events; late relationship announcements do not rewrite already-emitted
// events. Every other frame that cannot be represented, including semantic
// system telemetry and relationship uncertainty, becomes a session error with a
// stable reason and native metadata rather than a guessed normal event.
// Conversion gaps are non-fatal; a native decoder/protocol failure terminates
// the stream after its diagnostic gap.
//
// Open and Resume use SessionConfig. Nonempty model and reasoning-effort values
// become literal Claude CLI arguments on open and resume; empty values are
// omitted. Runtime configuration uses the optional ConfigController.SetConfig
// path: model maps to native SetModel and effort maps to native SetEffort,
// including its native settings preflight. Updates affect provider-determined
// later work; they do not wait for idle, interrupt, steer, or restart the
// process. A successful receipt means only that Claude accepted the request.
// Claude init may report a native model observation, but the unified layer does
// not cache, infer, or report an observed current configuration.
//
// This package has no unified history or NativeClient escape hatch. Native
// package-level session, metadata, message, and subagent queries remain the
// history APIs.
package agentclaude
