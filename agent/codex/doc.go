// Package agentcodex adapts the native Codex app-server SDK to the optional
// adapter-neutral agent runtime contract.
//
// A Core owns one shared Codex connection. Sessions attach to native
// threads on that connection and expose one live unified event stream. Native
// history queries remain available through the adapter-specific NativeClient
// escape hatch; this package defines no unified history or replay API.
//
// The adapter maps root-turn session state, user and agent messages, reasoning,
// title and usage updates, native errors, and command, file-change,
// function-output, MCP, dynamic-tool, collaboration, web-search, image-view,
// sleep, and image-generation item activity. Child-thread item events stay on
// the main session stream with their source and confirmed parent identities in
// Meta. The session answers supported child interactions through their original
// native handles. Blocking child interactions keep the session in requires_action
// across root completion or idleness until native settlement. Child-thread lifecycle
// notifications remain explicit gaps.
//
// Tool calls and results use the Codex-specific ToolName, Provider and Kind
// mappings, result-copy rule and native status projection defined in
// agent/docs/session.md. Tool chunks remain {id, content}.
//
// Command, file-change and MCP tool approvals stay bound to their native request
// and may select only an option offered by that request. Additional sandbox
// permission approvals display the requested profile and its reason, when present.
// Callers can grant the whole profile for the turn or session, or deny it.
// Standard MCP form and URL elicitations support accept, decline and cancel. Native user-input forms
// support accepted answers; decline and cancel are rejected because their native
// settlement is not proven. Interaction references are opaque, session-scoped,
// one-shot, and remain pending until a native lifecycle fact closes them.
// StartOrSteer accepts ordered text, image, audio, and supported image links.
// It calls native turn/start. The response turn identity decides
// current versus new work, a recognized native refusal returns not_applied, and
// an unknown outcome remains unsettled until native turn evidence arrives.
//
// The Core supports permission, form and URL interactions. The experimental API
// and AgentQuestions control the native request_user_input tool independently
// of MCP elicitation. A nonempty unified
// AdditionalSystemPrompt maps to developerInstructions on thread/start and
// thread/resume; an empty value omits that field, and baseInstructions is never
// set. The complete field contract is defined in
// agent/docs/session.md#additional-system-prompt. Additional
// directories are rejected before opening a session. Image, audio, resource,
// and resource-link inputs are not advertised.
// Open and Resume use SessionConfig. Empty model and reasoning-effort values are
// omitted; nonempty values are passed literally to native thread start or
// resume. Runtime configuration uses the optional ConfigController.SetConfig
// path, mapping model and effort to native SetModel and SetThinking. Runtime
// updates require the experimental API and affect provider-determined later
// work; they do not wait for idle, interrupt, steer, or rebuild the session. A
// successful receipt means only that Codex accepted the request. The unified
// layer does not cache, infer, or report an observed current configuration from
// acknowledgements or settings notifications.
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
// Successful lineage observations are attached to child event Meta. Failures are reported
// as non-fatal session errors. Unsupported notifications and items are reported
// as session errors with stable reasons and native metadata; they are not
// silently discarded. Saved resume history remains query-only and never becomes
// live unified events.
//
// Open and Resume pass the final adapter-neutral external MCP list to the native
// thread config. Stdio maps Command, Args, resolved Env/EnvFrom, and Cwd.
// Streamable HTTP maps URL, resolved Headers/HeadersFrom, EnvHeaders, and
// BearerTokenEnvVar to their respective native fields. Codex ambient configuration is field-level
// recursively merged with these declarations instead of replaced as a whole, and
// a warm loaded thread may ignore new resume config. The adapter does not promise
// ambient replacement or live MCP tool-set changes. A successful Open or Resume
// does not prove MCP connectivity, authentication, or tool discovery.
package agentcodex
