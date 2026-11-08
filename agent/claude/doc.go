// Package agentclaude adapts the native Claude Session to the provider-neutral
// live agent contract.
//
// Provider Start only makes the provider lifecycle ready; each Session owns its
// native Claude CLI process. Open waits for a live native init before returning
// the real session identity, and Resume additionally verifies the requested
// session ID and working directory. Saved replay material is not converted into
// live events.
//
// The adapter maps Claude user and assistant text and thinking, message and
// content-block deltas, tool calls and results, session state, usage, available
// commands, native errors, permission requests, and AskUserQuestion forms. Mixed
// user blocks preserve their text and tool results; unsupported blocks are
// reported as gaps. Assistant frame UUIDs are deduplicated only within their
// active response; response-scoped block and UUID state is released when the next
// response starts. An aborted assistant frame is reported rather than presented as
// complete content.
// Message, response, block, and tool identities remain in event item IDs and
// Meta. The provider declares text input, permission, and form capabilities;
// disabling AgentQuestions removes form support and disables the native tool.
//
// Input is one plain text block without annotations. System prompts, additional
// directories, URL interactions, multi-block input, and native frames or answer
// extensions without an exact mapping are not advertised. TodoWrite tool calls
// also emit a plan event using the tool-use ID and native content/status entries;
// TaskCreate and TaskUpdate are explicit task_plan_snapshot_unavailable gaps
// because they do not provide a complete plan snapshot. AskUserQuestion accepts
// string answers and, for multi-select questions, string arrays. Multi-select
// arrays are joined with ", " before the native write; native array answers are
// not supported.
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
// This package has no unified history or NativeClient escape hatch. Native
// package-level session, metadata, message, and subagent queries remain the
// history APIs.
package agentclaude
