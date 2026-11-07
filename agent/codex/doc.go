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
// Command and file-change approvals stay bound to their original native request
// and may select only an option offered by that request. Native user-input forms
// support accepted answers; decline and cancel are rejected because their native
// settlement is not proven. Interaction references are opaque, session-scoped,
// one-shot, and remain pending until a native lifecycle fact closes them.
//
// The provider declares text input and permission capabilities. It declares form
// support only when the native experimental API is enabled, and a session removes
// form support when AgentQuestions is disabled. System prompts and additional
// directories are rejected before opening a session. URL, image, audio, resource,
// and resource-link interactions or inputs are not advertised.
//
// Several native facts intentionally have no unified representation. Codex does
// not provide a selectable settings-option catalog or available-command catalog
// through this adapter. Archive and unarchive actions are supported, but their
// notifications have no unified payload. Plan text deltas and reasoning part
// boundaries are not represented. Turn plan updates are non-fatal
// plan_identity_unavailable errors because their native turn scope has no item
// ID required by the unified plan event; the raw plan, turn ID, explanation and
// entry count remain in Meta.
//
// Permission-profile approvals are unsupported, as are native item and
// notification variants without a proven mapping. Successful lineage
// observations are attached to child event Meta; lineage failures are reported
// as non-fatal session errors. Unsupported notifications and items are reported
// as session errors with stable reasons and native metadata; they are not
// silently discarded. Saved resume history remains query-only and never becomes
// live unified events.
package agentcodex
