// Copyright 2026 Claude Agent SDK for Go contributors
// SPDX-License-Identifier: Apache-2.0

// Package claude provides an independent native Session over an explicitly
// selected local Claude Code process. It does not bundle a CLI or manage accounts.
// Fixed source targets and evidence limits are documented in docs/compatibility.md.
//
// Open creates a Session with one FIFO. Receive delivers native events, real
// IncomingRequests and observation evidence. Submit success
// proves only a complete local write; it does not invent a native acknowledgment
// or a Claude Turn. Results do not end the Session. Native Raw, unknown fields
// and duplicate facts remain intact; optional parent knowledge never sorts facts.
//
// Answers require the real IncomingRequest's original permission handle. Copies
// share single-use authority, invalidated by native cancellation or termination.
// Session imposes no default answer or local approval deadline. Parsing, history
// and diagnostic material cannot create answer authority.
//
// ResumeID requests native continuation through Open; history reads do not
// establish a running Session or restore permission authority.
//
// Receive cancellation cancels only the wait. Interrupt's native acknowledgment
// does not prove rollback. Close cleans owned resources without waiting for the
// consumer; accepted events drain before the stable terminal cause. Abort
// explicitly abandons unread backlog. No reconnect or automatic resend occurs.
//
// Pure native data and JSON codecs live in types with standard-library-only
// production dependencies. Configuration acceptance is separate from observed
// effects. Query coverage and quarantine gaps remain explicit. The Session FIFO
// is limited by available memory, independently of bounded indexes and frames.
package claude
