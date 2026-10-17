# Codex Go SDK foundation

Status: foundation design. Implementation status and validation evidence are recorded in [compatibility](../compatibility.md).

Intended public module: `github.com/zoumo/codex-sdk-go`. This is an independent thin Go client for the Codex app-server protocol, not a MASS subsystem or a wrapper around a competing SDK.

## Problem Statement

A Go application needs to own a local Codex app-server or connect to a remote one, exchange typed protocol requests, observe events, and answer server requests without losing native identity or silently repeating work. Existing evaluated clients expose useful patterns but do not jointly establish these reliability guarantees. An application should not have to adopt an opinionated turn runner, persistence model, or recovery UI to use the transport.

## Solution

Provide a small native Go SDK with explicit connection lifecycle, typed P0 methods, raw protocol escape hatches, loss-aware subscriptions, and one-shot server-request reply handles. Keep session interpretation and durable business state in the caller. Ship fake-peer-tested behavior first; report real-server compatibility separately and honestly.

## User Stories

1. As a Go developer, I want to launch an explicitly selected app-server executable, so that the SDK owns only the process I requested.
2. As a developer, I want startup and initialization to have deadlines, so that a failed child cannot hang startup.
3. As a developer, I want remote WS/WSS connections with bearer authentication, so that I can use an existing service.
4. As an operator, I want credentials omitted from logs and errors, so that debugging does not disclose secrets.
5. As a developer, I want initialization completed before ordinary calls, so that protocol ordering is predictable.
6. As a developer, I want integer and string request IDs kept distinct, so that replies cannot satisfy the wrong request.
7. As a developer, I want typed thread start and read, so that I can create and inspect a native thread.
8. As a developer, I want resume to preserve the supplied native identity, so that failure never silently creates another thread.
9. As a developer, I want explicit unsubscribe, so that I control server-side thread observation.
10. As a developer, I want typed turn start and interrupt, so that execution control is explicit.
11. As a developer, I want local call cancellation distinguished from server execution interruption, so that a context timeout is not misreported as stopped work.
12. As a developer, I want subscriptions installed before starting a turn, so that early events are observable.
13. As a developer, I want ordered raw events with optional typed payloads, so that unknown future methods remain usable.
14. As a developer, I want malformed known payloads preserved with a decoding error, so that incompatibilities are visible.
15. As a developer, I want overflow reported explicitly, so that I never mistake an incomplete stream for a complete one.
16. As a developer, I want slow consumers isolated from RPC response routing, so that my calls can still complete.
17. As a developer, I want server requests exposed as immutable answer handles, so that approvals and user input retain exact correlation.
18. As a developer, I want canceled, duplicate, expired, and stale answers rejected, so that a late UI action cannot answer a different request.
19. As a developer, I want handler work off the reader, so that a slow approval UI cannot freeze the connection.
20. As a developer, I want unknown outcomes reported without replay, so that communication failure never silently repeats side effects.
21. As an operator, I want bounded buffers, pending work, and shutdown, so that a hostile or stalled peer cannot grow resources indefinitely.
22. As a developer, I want close to release subscriptions, calls, reply handles, sockets, and owned processes, so that tests and applications can terminate.
23. As a maintainer, I want an external-package fake-peer contract suite, so that exported APIs rather than private implementation details define correctness.
24. As a user, I want implementation, mock evidence, live evidence, and protocol version shown separately, so that passing tests are not mistaken for live certification.
25. As a contributor, I want pinned tooling and traceable licenses, so that builds and reuse are reproducible.

## Implementation Decisions

### Boundary and lifecycle

- Expose an explicit client connection and immutable native protocol data. Proposed exported method vocabulary is Initialize, StartThread, ResumeThread, ReadThread, UnsubscribeThread, StartTurn, InterruptTurn, Subscribe, Requests, Call, and Close; exact Go signatures may be refined within these contracts.
- Provide owned-process stdio and remote WebSocket constructors. No implicit ambient stdin/stdout, executable download, credential lookup, authentication login, or global process ownership.
- Constructors finish the pinned protocol's initialize response and initialized notification before returning a ready client. Concurrent calls before readiness fail predictably; initialize failure closes resources. Expose server information from the handshake. Do not assume version-string similarity proves protocol compatibility.
- Require an explicit executable path/command and argument vector for local use; never construct a shell command. Keep stdout solely for protocol frames. Drain stderr into a bounded, redacted diagnostic facility. Close first attempts bounded graceful shutdown, then terminates/reaps only the owned child. Remote close never kills a server.
- Transport framing follows the pinned protocol: newline-delimited stdio envelopes and correctly framed WebSocket messages, rather than pretending a socket is a byte-stream JSONL transport. Wire JSON-RPC dialect must be checked against the exact official source, including whether the jsonrpc member is present.
- Remote authentication uses an Authorization bearer header supplied by the caller, never a URL query parameter. Default TLS certificate verification stays enabled. No credential forwarding across redirects. Unencrypted ws is permitted for loopback tests; non-loopback ws requires explicit insecure opt-in and documentation. No automatic reconnect in P0; a new connection means a new initialization and caller-directed reconciliation.

### Typed surface and identifiers

- P0 typed operations are initialize, thread/start, thread/resume, thread/read, thread/unsubscribe, turn/start, and turn/interrupt. Each exposes pinned native parameters and results without manufacturing thread/turn/item IDs. Optional features do not become mandatory fields through convenient Go zero values; preserve omission versus null when protocol semantics require it.
- Integer and string request IDs are a tagged union; integer 1 and string "1" are different keys. Preserve exact valid integer values without float64 conversion and round-trip arbitrary string IDs. Reject null, fractional, and invalid IDs where an identified request/response is required. Locally allocated RPC correlation IDs are allowed; fabricated native thread/turn/item IDs are not.
- Resume sends exactly the caller's thread ID. A mismatching response is a protocol error, and a missing/expired thread is returned as an error. Never fall back to thread/start.
- Expose a generic raw Call facility for compatible protocol additions. It has the same lifecycle, correlation, cancellation, bounds, and no-replay rules as typed methods. It does not certify untyped methods as supported.
- Do not expose a high-level TurnHandle, Run accumulator, implicit final-text selection, or automatic per-turn state machine. A stale turn/completed notification remains an event with its native IDs; it cannot rewrite the result of StartTurn. Callers own thread-and-turn filtering and per-item projections.

### Events and bounded work

- Subscribe is active when it returns. The caller subscribes before starting work. Deliver each subscriber's accepted events in reader arrival order, with raw method and raw parameters preserved even when typed decoding succeeds. Unknown methods/types remain observable; malformed known payloads include a decode error and original bytes.
- Bound frame bytes, queued outbound messages, pending calls, subscriptions, each event queue, active reverse requests, and diagnostic bytes using documented finite defaults and configurable positive limits. Admission failure occurs before sending when possible.
- A slow subscriber fails closed with an explicit overflow/loss cause, separately observable from normal Close even if its queue is full. Other subscribers and pending response routing continue. Do not silently evict events and continue an apparently healthy stream. Terminal status must not depend on enqueueing an error into the already-full event buffer.
- The reader never executes caller callbacks or waits for consumer channels. Incoming server requests use a separate bounded queue/handle registry. On exhausted reverse-request capacity, send a pinned-protocol-compatible overload error using reserved bounded control capacity, or fail the connection explicitly if that cannot be done; never block the reader indefinitely or invent a denial approval.
- Oversized or invalid envelope framing ends the connection with an explicit protocol/loss error. Unknown valid notification methods are not protocol faults. Unmatched/late response IDs cannot satisfy another pending call and remain diagnosable without unbounded logging.

### Reverse requests and cancellation

- Expose incoming requests with method, raw payload, exact tagged RPC ID, and an opaque connection-bound answer handle. Include typed supported approval/user-input payloads and raw fallback. Authorize no action automatically.
- An answer handle permits exactly one result or error response. It is invalidated by supported server cancellation, local handle cancellation/expiry, or connection close. Cancellation of a local handle does not claim cancellation of server execution. Match cancellation only using identifiers actually defined by the pinned wire protocol; do not invent a cancel notification.
- Stale handles from a prior connection, duplicate answers, and answers after cancellation fail locally without writing bytes. Distinguish safe pre-write validation failure from a write that may have partially succeeded: after ambiguous write the handle cannot be retried. Competing Answer/Cancel operations have one race-safe winner.
- If a handler convenience API is added, its bounded workers run away from the reader, receive cancellation contexts, and cannot block Close. Go cannot forcibly stop arbitrary user code: document cooperative cancellation and do not await a noncooperative callback indefinitely. A pull-based Requests API is sufficient for P0.
- Context cancellation before a request is written reports not-sent. After writing has begun, cancellation or connection loss reports outcome-unknown unless a matching response definitively settles the request. Preserve the underlying context/transport cause and dispatch evidence; do not equate transport delivery with server execution.
- No automatic request replay, including after internal RPC errors, reconnect, timeout, or partial writes. A server RPC error is preserved; it is not proof that no side effect occurred. Interrupt is a separate explicit RPC; its acknowledgement is not a turn-completed event.

### Compatibility and provenance

- Initial candidate protocol target: official Codex commit d27764b82f7118f674371e6d6e76271d9d606edb, reported as 0.160.1 in the prior evaluation. Exact target sources and schemas are recorded in [protocol provenance](../protocol/README.md); source verification does not establish live compatibility. If a required P0 method is absent, report the conflict before changing the target or inventing a wrapper.
- Maintain exactly one canonical compatibility matrix: `docs/compatibility.md`, enumerating the complete pinned official surface and this SDK’s support. README and release notes must link to it rather than maintain competing capability/version tables. Update evidence per ticket and include the tested SDK commit when available.
- Proposed Go toolchain: 1.26.9, managed with mise, matching the evaluated toolchain. Pin it and use GOTOOLCHAIN=local in verification so Go does not silently select another compiler. No Go binaries or global installations are part of this proposal.
- Proposed license for original SDK code: Apache-2.0. No product dependency on pmenglund or Zealbase. Independently implement the public contract against official protocol definitions. Any copied/generated third-party material requires its exact source revision, applicable license, preserved notices, and traceable attribution; investigate before copying, including adapting existing test code. The prior fixtures are behavioral evidence, not permission to relicense their source.

## Testing Decisions

- Proposed single highest seam: an external-package consumer using only exported SDK constructors, requests, subscriptions, answer handles, errors, and Close against controllable fake app-server peers. Use a fake owned child for stdio, an in-process loopback WS peer, and a local TLS WS peer with a test trust root. These are transport variants of one public contract seam, not tests coupled to private queues/maps.
- Verify complete observable traces, received bytes, returned native IDs, response classification, terminal causes, process exit, and bounded completion. Use deterministic barriers instead of timing sleeps wherever possible. Narrow parser tests supplement this seam only where malformed framing cannot be driven otherwise.
- Run external contract tests under go test ./... and go test -race ./.... Include an external consumer compilation test ensuring no public signature requires an inaccessible internal type.
- Prior art is the 2026-10-17 evaluation of competing clients and five reproduced adverse cases: stale same-thread completion, numeric/string ID collision, automatic retry of turn/start, completion arriving before start response, and subscriber overflow lacking an observable loss cause. Reproduce those scenarios independently against this SDK.
- Each vertical ticket includes its own errors, limits, cancellation and cleanup tests. Passing later tickets must not be required to make an earlier slice safely usable for its stated scope.
- No real account, credentials, paid model call, or live Codex session in this phase. Local TLS tests use synthetic certificates and dummy bearer values. Live validation needs separate authorization and updates only the live-evidence column.

## Out of Scope

MASS-specific adapters, durable application state, journals, recovery UI, policy decisions, durable approvals, automatic execution retries, automatic reconnect/session restoration, final-text aggregation, high-level run/TurnHandle abstractions, login/credential management, account APIs, MCP administration, turn/steer, thread/list, and additional history APIs. P1 can add steer/list/history and broader generated protocol coverage after P0 contracts and evidence are established.

## Further Notes

### Compatibility evidence

The canonical implementation, complete pinned surface inventory, and separate unit, mock, race, and live evidence are maintained in [docs/compatibility.md](../compatibility.md). This design document does not maintain a competing capability or version table.

### Source provenance

- Prior client evaluation: 2026-10-17 “Codex Go clients: MASS contract validation,” with adjacent adverse contract fixtures and JSONL results. This supplied behavioral requirements, not SDK implementation or live-certification evidence.
- Candidate official target: https://github.com/openai/codex/tree/d27764b82f7118f674371e6d6e76271d9d606edb . Exact source verification is recorded in [protocol provenance](../protocol/README.md).
- Available official checkout inspected for context: https://github.com/openai/codex/tree/806d9732c974bc8a51b8317c1bd8985544fe627c . This is not the selected compatibility pin.
- pmenglund evaluated HEAD: https://github.com/pmenglund/codex-sdk-go/tree/ebc984e331c824e2e8fcc6c68e37c01282bc63ea ; generated Codex 0.162.1 / 092d3acd6bec3e3a14bdc7e7a2810ab628ab759d.
- pmenglund evaluated older tag: https://github.com/pmenglund/codex-sdk-go/tree/3c76dc94c216fe6f8a7e0afef861697cbf756567 ; generated Codex 0.160.0 / a956835d020762cb2b570053af06f643a11c0ecc.
- Zealbase evaluated source: https://github.com/Zealbase/codex-app-server-go/tree/c3195187679cc04654b7097e975f7b4110b73874 ; generated schema metadata reports Codex 0.141.0. README/version labels are not a substitute for generated metadata.
