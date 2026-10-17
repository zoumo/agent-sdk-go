# Claude Agent Go SDK: core design

Status: Implemented experimental core; live interoperability remains unverified. Date: 2026-10-17.

## Product boundary

A native Go thin client for a locally installed Claude Code CLI, intended module github.com/zoumo/claude-agent-sdk-go. It owns subprocess transport, bidirectional NDJSON protocol, request correlation, session events, and in-memory interaction safety. It does not depend on another Go SDK. Native Claude session persistence remains owned by the CLI. Host applications own business journals, durable recovery, UI, provider-neutral events, and policy decisions.

P0 means actual implemented public behavior, not placeholder APIs. Hook registration, SDK-hosted MCP, broad configuration parity, session forking, remote transports, and durable recovery are deferred. A result is a turn result, not proof the process or background work has finished.

## Public behavior

Names below are design vocabulary; exact exported identifiers settle during implementation without weakening these contracts.

- Open a client with explicit CLI path or ordinary executable discovery, working directory, optional model and native resume ID, bounded initialize timeout, limits, and permission handler. Spawn without a shell; preserve argv values literally. Full inherited settings remain the CLI's behavior; a permission handler is not a sandbox or a gate on every tool.
- Start stdout/stderr draining before initialize. Initialization is a matched outbound control request registered before writing. Open returns only after successful initialization, or returns a useful error after bounded cleanup.
- Send a user message. Successful return means its complete NDJSON frame was written, never model acceptance, execution, or exactly-once delivery. No background fire-and-forget send failures.
- Receive events through a single consumer stream/iterator with an inspectable terminal error and Done signal independent of event capacity. Context cancellation of a receive wait does not pretend to cancel a model turn.
- Expose typed system/session, assistant, user, result, stream-delta, tool-use and tool-result views while retaining each complete raw JSON frame. Unknown valid message types and unknown nested fields/blocks survive unchanged as raw data. Protocol-invalid NDJSON is an explicit terminal error.
- Distinguish requested resume ID from the native ID actually observed in CLI messages. Until observed, native ID is unknown. Resume only passes the explicit ID into a fresh local process; no implicit user-message replay or pending-interaction restoration.
- Interrupt through the official matched control request. An acknowledged interrupt is not proof of tool rollback; a later result remains independently observable.
- Expose deferred incoming permission handles with original request ID, tool-use ID when supplied, tool name, raw input/context, connection-local epoch, cancellation context and an answer operation. A handler can retain the handle and return immediately, or wait without blocking the reader. There is one answer authority: no second automatic callback-return response.
- Reply allow with original or explicitly updated input, or deny with a message, to the original control_request. No configured handler defaults to deny. Handle unsupported control requests with an observable unsupported error response, not silent success.
- AskUserQuestion is special only when it arrives as can_use_tool: retain its original input and permit a caller-provided updated-input answer through that same handle. An assistant tool_use named AskUserQuestion is an observable content event, not authorization to synthesize a user/tool_result response. Typed question-schema convenience and automatic answer mapping require additional official schema evidence and are deferred; generic updated input is available in P0.

## In-memory safety and lifecycle

Incoming request states are pending, answering, answered, cancelled, closed, or write-failed. Only pending can reserve an answer. Reserving atomically consumes the one response attempt; even an ambiguous write failure cannot retry automatically. Validate handle epoch and state at write authorization, including a final cancellation check under the same serialization boundary that starts the write. A cancellation already observed before write authorization prevents the write. Cancellation concurrent with an already-started OS write cannot retract bytes; report that outcome conservatively.

control_cancel_request immediately invalidates the matching handle, cancels its context and emits an observable cancellation event with the original request ID, even for an unknown/stale ID. Duplicate incoming request IDs must never create a second live responder; treat duplicate active IDs as protocol error. IDs already terminal in this epoch remain guarded by a bounded tombstone registry; exhaustion is an explicit resource-limit terminal error, not eviction that could revive a stale request. New client instances get distinct epochs. Closing, transport failure and reader termination invalidate every pending handle and wake all pending controls.

The stdout reader never invokes user handlers synchronously and never waits on a slow event consumer. Bound event count, frame bytes, outstanding controls, inbound interaction count, responder work and stderr tail. On any exceeded bound, atomically record a typed terminal resource/overflow error, invalidate controls/handles, and begin bounded shutdown. Drain already accepted events in order, then expose the terminal error independently; the frame that exceeded capacity is not claimed as delivered. Fail-fast overflow trades continuity for bounded memory and explicit loss, not silent drops. No unlimited goroutine-per-request workaround.

Serial writes retain full-frame boundaries. Background read/write errors, malformed/truncated frames, unexpected EOF, nonzero exit, initialization timeout and shutdown timeout remain observable with bounded diagnostic stderr (never automatically logged). Request waiters select on response, caller cancellation and transport termination. A context timeout after bytes might have been written yields an unknown-outcome classification; it does not prove the CLI ignored the request.

One owner reaps the child. Close is idempotent and concurrency safe: cancel handles, close stdin, bounded drain/wait, terminate then kill if needed, close pipes and finish. Retain accepted events before terminal failure. POSIX child/process-group teardown is tested where supported; do not advertise durable process adoption or unverified platform-specific descendant guarantees. Handler code must respect context: Go cannot forcibly stop arbitrary user callbacks. SDK shutdown must not wait forever for a callback that ignores cancellation.

## Public test seam

Exercise only the exported client API against a scripted local subprocess acting as the CLI. The fixture observes argv/stdin and emits official-source-derived stdout frames; it can delay, close pipes, saturate output and spawn a descendant. Use no internal router assertions as acceptance criteria. A narrow internal process seam may assist deterministic fault injection, but public behavior remains the oracle. Fresh contexts read this design and their one ticket.

Tests use isolated temporary HOME/workdirs, no Claude account credentials, no network/model calls, deterministic synchronization rather than sleep-based guesses, complete suite plus race detector. Ordered accepted events, exact emitted wire envelope/request ID, terminal errors and absence of extra responses are observable assertions. Tests must exercise the real public reader/writer path, avoiding the mistaken attribution of an unused internal queue as a public-path bug.

## Evidence and compatibility

Authoritative protocol pin: official anthropics/claude-agent-sdk-python at f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab. Source examined on 2026-10-17:
- https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/query.py — initialize, register-before-write controls, request/response envelopes, asynchronous permission handling, control_cancel_request, interrupt, allow updatedInput / deny.
- https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/transport/subprocess_cli.py — stream-json argv, verbose output, native resume option, subprocess transport.
- https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/types.py — message/control shapes; can_use_tool only receives operations the CLI would prompt for.
- https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_cli_version.py — bundled CLI reference 2.1.302.

These sources establish a protocol target, not verified runtime compatibility. Real CLI 2.1.302 compatibility: UNKNOWN. No CLI/account/model-backed tests have been run for this new SDK. Fixtures are synthetic source-derived tests and must be labelled that way. Do not claim official endorsement or production readiness.

Comparative validation informed adverse cases only: severity1 d167fbef06759f127f5623bcf685c05b422de5df and Roasbeef a886b29cee2478f48251cfaab02142683ee43d6b. Their passing mock suites do not validate this SDK or live question semantics. No competitor implementation is a dependency or protocol authority.

A separately authorized live compatibility gate must pin the installed CLI and test ordinary messages, native ID/resume without resend, interrupt, original permission request/answer, AskUserQuestion's actual path, cancel during human wait, late answer rejection, disconnect after write, overload and process teardown. If this gate is not authorized, ship only with explicit unverified/experimental compatibility wording, not a passing live badge.

## Delivery and licensing proposal

Use mise to pin Go 1.26.9, matching the independently validated compiler; verify the effective compiler during implementation. Use standard-library-first native Go, ordinary go test and race checks, and a compilable public example with an explicit installed-CLI prerequisite. Original code is licensed Apache-2.0. Avoid copied implementation snippets; if any MIT source is copied or adapted, retain its original copyright/license notices and document provenance. Publication follows completion and independent review of all five tickets.

## P0 ticket frontier and deferred scope

Five implementation tickets: basic session stream; native resume and interrupt; deferred permission controls; per-request cancellation and stale answers; bounded overload and lifecycle hardening. All five are required before the first experimental release. Core basic cleanup/error behavior exists from ticket 1; later hardening is not permission for earlier tickets to hide errors or leak children.

P1 candidates: evidence-backed typed question helpers, hooks, SDK MCP, additional configuration and session fork controls. P2 candidates: remote transport and other transport backends. Host business journals, UI, durable recovery and automatic resend are not SDK backlog promises.

