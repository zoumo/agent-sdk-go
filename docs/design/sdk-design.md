# Claude Agent Go SDK: core design

Status: Implemented experimental core; live interoperability remains unverified.

## Product boundary

A native Go thin client for a locally installed Claude Code CLI, module github.com/zoumo/claude-agent-sdk-go. It owns subprocess transport, bidirectional NDJSON protocol, request correlation, session events, and in-memory interaction safety. It does not depend on another Go SDK. Native Claude session persistence remains owned by the CLI. Host applications own business journals, durable recovery, UI, provider-neutral events, and policy decisions.

Hook registration, SDK-hosted MCP, broad configuration parity, session forking, remote transports, and durable recovery are deferred. A result is a turn result, not proof the process or background work has finished.

## Public behavior

`Open` returns `*Client`; `Receive` returns `ReceivedEvent`. Native `Event` values
retain their official fields and Raw bytes. A private managed ledger supplies
separate `SubagentAssociated`, `SubagentCreated` and `ObservationState` values.
`Options.Subagents` controls bounds/history, while forwarding is automatic.

- Open a client with explicit CLI path or ordinary executable discovery, working directory, optional model and native resume ID, bounded initialize timeout, limits, and permission handler. Spawn without a shell; preserve argv values literally. Full inherited settings remain the CLI's behavior; a permission handler is not a sandbox or a gate on every tool.
- Start stdout/stderr draining before initialize. Initialization is a matched outbound control request registered before writing. Open returns only after successful initialization, or returns a useful error after bounded cleanup.
- Send a user message. Successful return means its complete NDJSON frame was written, never model acceptance, execution, or exactly-once delivery. No background fire-and-forget send failures.
- Receive events through a single consumer stream/iterator with an inspectable terminal error and Done signal independent of event capacity. Context cancellation of a receive wait does not pretend to cancel a model turn.
- Expose typed system/session, assistant, user, result, stream-delta, tool-use and tool-result views while retaining each complete raw JSON frame. Unknown valid message types and unknown nested fields/blocks survive unchanged as raw data. Protocol-invalid NDJSON is an explicit terminal error.
- Distinguish requested resume ID from the native ID actually observed in CLI messages. Until observed, native ID is unknown. Resume only passes the explicit ID into a fresh local process; no implicit user-message replay or pending-interaction restoration.
- Interrupt through the official matched control request. An acknowledged interrupt is not proof of tool rollback; a later result remains independently observable.
- Expose deferred incoming permission handles with original request ID, tool-use ID when supplied, tool name, raw input/context, connection-local epoch, cancellation context and an answer operation. A handler can retain the handle and return immediately, or wait without blocking the reader. There is one answer authority: no second automatic callback-return response.
- Reply allow with original or explicitly updated input, or deny with a message, to the original control_request. No configured handler defaults to deny. Handle unsupported control requests with an observable unsupported error response, not silent success.
- AskUserQuestion is special only when it arrives as can_use_tool: retain its original input and permit a caller-provided updated-input answer through that same handle. An assistant tool_use named AskUserQuestion is an observable content event, not authorization to synthesize a user/tool_result response. Typed `AskUserQuestionInput.WithAnswers` validates caller-supplied full-question-text keys and preserves the input; the application collects answers.

## In-memory safety and lifecycle

Incoming request states are pending, answering, answered, cancelled, closed, or write-failed. Only pending can reserve an answer. Reserving atomically consumes the one response attempt; even an ambiguous write failure cannot retry automatically. Validate handle epoch and state at write authorization, including a final cancellation check under the same serialization boundary that starts the write. A cancellation already observed before write authorization prevents the write. Cancellation concurrent with an already-started OS write cannot retract bytes; report that outcome conservatively.

control_cancel_request immediately invalidates the matching handle, cancels its context and emits an observable cancellation event with the original request ID, even for an unknown/stale ID. Duplicate incoming request IDs must never create a second live responder; treat duplicate active IDs as protocol error. IDs already terminal in this epoch remain guarded by a bounded tombstone registry; exhaustion is an explicit resource-limit terminal error, not eviction that could revive a stale request. New client instances get distinct epochs. Closing, transport failure and reader termination invalidate every pending handle and wake all pending controls.

The stdout reader never invokes user handlers synchronously and never waits on a slow event consumer. Bound event count, frame bytes, outstanding controls, inbound interaction count, responder work and stderr tail. On any exceeded bound, atomically record a typed terminal resource/overflow error, invalidate controls/handles, and begin bounded shutdown. Drain already accepted events in order, then expose the terminal error independently; the frame that exceeded capacity is not claimed as delivered. Fail-fast overflow trades continuity for bounded memory and explicit loss, not silent drops. No unlimited goroutine-per-request workaround.

Serial writes retain full-frame boundaries. Background read/write errors, malformed/truncated frames, unexpected EOF, nonzero exit, initialization timeout and shutdown timeout remain observable with bounded diagnostic stderr (never automatically logged). Request waiters select on response, caller cancellation and transport termination. A context timeout after bytes might have been written yields an unknown-outcome classification; it does not prove the CLI ignored the request.

One owner reaps the child. Close is idempotent and concurrency safe: cancel handles, close stdin, bounded drain/wait, terminate then kill if needed, close pipes and finish. Retain accepted events before terminal failure. POSIX child/process-group teardown is tested where supported; do not advertise durable process adoption or unverified platform-specific descendant guarantees. Handler code must respect context: Go cannot forcibly stop arbitrary user callbacks. SDK shutdown must not wait forever for a callback that ignores cancellation.

## Public test seam

Exercise only the exported client API against a scripted local subprocess acting as the CLI. The fixture observes argv/stdin and emits official-source-derived stdout frames; it can delay, close pipes, saturate output and spawn a descendant. Use no internal router assertions as acceptance criteria. A narrow internal process seam may assist deterministic fault injection, but public behavior remains the oracle. Internal relationship tests additionally inspect conservative attribution and bounded ledger state.

Tests use isolated temporary HOME/workdirs, no Claude account credentials, no network/model calls, deterministic synchronization rather than sleep-based guesses, complete suite plus race detector. Ordered accepted events, exact emitted wire envelope/request ID, terminal errors and absence of extra responses are observable assertions. Tests must exercise the real public reader/writer path, avoiding the mistaken attribution of an unused internal queue as a public-path bug.

## Compatibility and scope

The [compatibility inventory](../compatibility.md) describes implemented and
unsupported features and bounded runtime observations. [Type sources](../type-sources.md)
records pinned schema references and licenses. [Subagent rules](../subagent-events.md)
define conservative creation/association and observation gaps; no subagent live
validation is claimed. Synthetic tests and CI do not certify runtime compatibility.

The module uses the Go standard library and pins Go 1.26.9 through mise.
Original code is Apache-2.0; third-party research provenance is in [NOTICE](../../NOTICE).
Hooks, SDK-hosted MCP, broad configuration parity, fork/history helpers, remote
transports, durable recovery and automatic resend remain unsupported.
