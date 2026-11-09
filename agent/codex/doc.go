// Package agentcodex adapts the native Codex app-server SDK to the optional
// provider-neutral agent runtime contract.
//
// A Provider owns one shared Codex connection. Sessions attach to native
// threads on that connection and expose one live unified event stream. Native
// history queries remain available through the provider-specific NativeClient
// escape hatch; this package defines no unified history or replay API.
//
// The adapter maps root-turn session state, user and agent messages, reasoning,
// title and usage updates, native errors, and command, file-change,
// function-output, MCP, dynamic-tool, collaboration, web-search, image-view,
// sleep, and image-generation item activity. Child-thread item events stay on
// the main session stream with their source and confirmed parent identities in
// Meta. Confirmed child command, file-change, and user-input interactions keep
// their original native authority and can be answered through the same session;
// blocking child interactions keep the session in requires_action across root
// completion or idleness until native settlement. Child-thread lifecycle
// notifications remain explicit gaps.
//
// Tool calls and results use the Codex-specific ToolName, Provider and Kind
// mappings, result-copy rule and native status projection defined in
// harness/modules/unified-agent/design.md. Tool chunks remain {id, content}.
//
// Command and file-change approvals stay bound to their original native request
// and may select only an option offered by that request. Native user-input forms
// support accepted answers; decline and cancel are rejected because their native
// settlement is not proven. Interaction references are opaque, session-scoped,
// one-shot, and remain pending until a native lifecycle fact closes them.
// StartOrSteer calls native turn/start only: the response turn identity decides
// current versus new work, a recognized native refusal returns not_applied, and
// an unknown outcome remains unsettled until native turn evidence arrives.
//
// The provider declares text input and permission capabilities. It declares form
// support only when the native experimental API is enabled, and a session removes
// form support when AgentQuestions is disabled. System prompts and additional
// directories are rejected before opening a session. URL, image, audio, resource,
// and resource-link interactions or inputs are not advertised.
// Model and reasoning-effort execution options use native thread start/resume
// values and thread settings updates. Runtime updates require the experimental
// API and only update the fields exposed by ExecutionOptionsController.
//
// Several native facts intentionally have no unified representation. Codex does
// not provide an arbitrary selectable settings-option catalog or
// available-command catalog through this adapter. Archive and unarchive actions
// are supported, but their notifications have no unified payload. Native plan
// items, plan text deltas, and turn plan updates are intentionally not emitted,
// while their original types and notifications remain available in the native
// Codex package.
// Reasoning part boundaries are also not represented.
//
// Permission-profile approvals are unsupported, as are native item and
// notification variants without a proven mapping. Successful lineage
// observations are attached to child event Meta; lineage failures are reported
// as non-fatal session errors. Unsupported notifications and items are reported
// as session errors with stable reasons and native metadata; they are not
// silently discarded. Saved resume history remains query-only and never becomes
// live unified events.
//
// Open and Resume pass the final provider-neutral external MCP list to the native
// thread config. Stdio maps Command, Args, Env, and Cwd; Streamable HTTP maps URL,
// Headers, EnvHeaders, and BearerTokenEnvVar to the native header and bearer
// environment-variable fields. Codex ambient configuration is field-level
// recursively merged with these declarations instead of replaced as a whole, and
// a warm loaded thread may ignore new resume config. The adapter does not promise
// ambient replacement or live MCP tool-set changes. A successful Open or Resume
// does not prove MCP connectivity, authentication, or tool discovery.
package agentcodex
