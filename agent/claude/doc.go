// Package agentclaude adapts the native Claude Session to the provider-neutral
// live agent contract.
//
// Provider Start only makes the provider lifecycle ready; each Session owns its
// native Claude CLI process. Open waits for a live native init before returning
// the real session identity, and Resume additionally verifies the requested
// session ID and working directory. Saved replay material is not converted into
// live events.
//
// The adapter maps Claude text and thinking, message and content-block deltas,
// complete assistant content, tool calls and results, session state, usage,
// available commands, native errors, permission requests, and AskUserQuestion
// forms. Message, response, block, and tool identities remain in event item IDs
// and Meta. The provider declares text input, permission, and form capabilities;
// disabling AgentQuestions removes form support and disables the native tool.
//
// Input is one plain text block without annotations. System prompts, additional
// directories, URL interactions, multi-block input, task/todo plans without
// proven priority, and native frames or answer extensions without an exact
// mapping are not advertised. A frame that cannot be represented becomes a
// non-fatal session error with a stable reason and native metadata rather than a
// guessed normal event.
//
// This package has no unified history or NativeClient escape hatch. Native
// package-level session, metadata, message, and subagent queries remain the
// history APIs.
package agentclaude
