// Copyright 2026 Claude Agent SDK for Go contributors
// SPDX-License-Identifier: Apache-2.0

// Package claude provides an experimental native client for a local Claude Code
// process. It does not bundle a CLI or manage accounts. Protocol fixtures target
// an official SDK source revision; live CLI interoperability is unverified.
//
// A successful Send means that a complete frame was written, not that the CLI
// accepted or executed it. A Result ends a turn, not the client. Always Close
// clients. Receive has one consumer; canceling a Receive wait does not interrupt
// a turn. The CLI's inherited settings and permissions still apply: denying
// incoming permission requests is not a sandbox or a gate on every tool.
//
// ResumeID requests native CLI resume in a fresh process without replaying any
// messages. SessionID reports only identity actually observed from the CLI.
// Interrupt waits for its matched control acknowledgment, not a turn result or
// tool rollback. No durable recovery or pending-interaction restoration is done.
//
// PermissionHandler receives deferred single-use handles on bounded workers.
// Returning from a handler never answers; PermissionRequest.Answer is the sole
// authority. Request contexts cancel on completion, CLI cancellation or close.
// Assistant tool-use events never create synthetic permission/tool responses.
//
// Pure data and existing JSON codecs have canonical definitions in the types
// subpackage. Root aliases preserve source compatibility, while reflected and
// formatted named-type paths may change. Options retains its callback and
// runtime-only Subagents resource/history configuration; OptionsFromTypes adapts
// callback-free types.Options. Open always requests native child forwarding.
// Receive returns direct native Event values and distinct SubagentAssociated,
// SubagentCreated and ObservationState convenience values. Relationship tracking
// is private; native fields and JSON remain unchanged. Event.Origin is the
// official MessageOrigin object. Forwarding is automatic.
package claude
