# Official test patterns inspected

Source inspection recheck: 2026-10-18 (current CLI 0.162.1). Every link below is pinned to official Codex
revision `092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`. Exact Git blob and SHA-256
checksums are in [sources.json](sources.json). None of these upstream suites was
run during SDK implementation, and no test source was copied into this SDK.

## Rust: app-server integration boundary

The official [TestAppServer helper](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/common/test_app_server.rs)
launches a real test app-server subprocess with piped stdin/stdout/stderr, an
isolated Codex home, a separately drained diagnostic stream, correlation IDs, and
explicit initialization. It checks the initialize response ID before writing
`initialized`. Helpers distinguish responses from notifications and use deadlines.

The [stdio connection suite](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/connection_handling_stdio_tests.rs)
exercises EOF/SIGTERM cleanup, open pipes, unread stderr, blocked writes, and bounded
exit. These tests verify the server's shutdown behavior; they do not prove this
Go client's ownership or cleanup behavior.

The [mock model server](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/common/mock_model_server.rs)
uses a local HTTP Responses endpoint, ordered synthetic SSE responses, and exact
request-count expectations. This replaces the model service while leaving the
app-server implementation real. It is a different boundary from a scripted fake
app-server peer.

## TypeScript: exec SDK boundary

The official [TypeScript exec implementation](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/sdk/typescript/src/exec.ts)
launches `codex exec --experimental-json`. It is not the bidirectional app-server
JSON-RPC transport. Its event/result behavior cannot be used as app-server wire
conformance evidence.

[exec.test.ts](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/sdk/typescript/tests/exec.test.ts)
mocks process spawn with an event-emitting fake child and independent stdin,
stdout, and stderr streams. It tests early exit before stream close and inspects
exact argument construction. [codexExecSpy.ts](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/sdk/typescript/tests/codexExecSpy.ts)
records arguments and environment while preserving the configured spawn behavior.
[responsesProxy.ts](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/sdk/typescript/tests/responsesProxy.ts)
provides a local model-response HTTP/SSE fixture and captured request bodies/headers.

Useful transferable ideas are independent stream lifecycle controls, exact
argv/environment assertions, final output arriving near exit, and counting
requests. The Go tests implement those ideas at their own exported-client seam.

## Python: app-server SDK and separate live tests

The pinned official tree contains both `sdk/python` and `sdk/python-runtime`.
Python SDK tests are present; no absence claim is made.

[app_server_harness.py](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/sdk/python/tests/app_server_harness.py)
starts a local queued Responses HTTP/SSE fixture, records requests, creates an
isolated workspace/Codex home, and configures a real app-server binary.
[test_app_server_lifecycle.py](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/sdk/python/tests/test_app_server_lifecycle.py)
uses public synchronous and asynchronous clients to exercise initialization,
thread metadata, and persisted history. Again, a real app-server with a mock model
is not a fake app-server contract test.

[test_async_client_behavior.py](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/sdk/python/tests/test_async_client_behavior.py)
stubs wrapped synchronous methods and checks overlapping async calls and delegated
notification registration. That is a unit-test seam, not wire coverage.

[test_real_app_server_integration.py](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/sdk/python/tests/test_real_app_server_integration.py)
is separately gated by `RUN_REAL_CODEX_TESTS=1`, prepares a pinned runtime, and
includes actual turn/model examples. Its presence does not mean those tests ran,
that all tests are offline, or that its runtime/schema pair matches this Go SDK.
We did not invoke that gate or launch any real Codex binary.

## Application to this SDK

The primary seam remains a separately compiled consumer of exported Go APIs
against a scripted owned child process, plus loopback WS and synthetic-trust WSS
variants. It checks received frames, native IDs,
returned results/errors, process exit, and deadlines rather than private queues
or maps. Unit model-fidelity tests supplement that seam. Race-detector results
are recorded separately from fake-peer behavior and live compatibility.

Read the actual [evidence record](evidence.json) for executed Go commands and
scenarios. Upstream patterns guide independently authored tests; upstream passing
status, paid service behavior, and broader SDK functionality are not inherited.

## Ticket 02: independently applied event-test patterns

The new external-package tests keep the same fake-child boundary. The Rust
helper's separate response/notification handling informed assertions that events
cannot settle an outstanding RPC. The TypeScript fake child's independent stream
and exit controls informed failure tests, without borrowing exec wire shapes.
The Python async behavior suite's recorded registration/delegation sequence
informed checks that subscription registration is effective before work begins;
this SDK deliberately has no Python-style per-turn registration or waiting state.

`TestTurnEarlyEventsKeepResponseIdentity` withholds the start response behind a
second RPC until the caller has observed eight notifications, including stale and
interleaved identities and immediate completion. This uses a deterministic barrier,
not a sleep. `TestSubscriptionOverflowIsolatedAndInspectable` paces a healthy
observer while another leaves all events unread; `TestSlowSubscriberNeverBlocksResponse`
sends 4,096 notifications before a normal response. The separate terminal signal
must remain observable before the event backlog is drained. Both observers retain
all admitted events in order; capacity-driven termination is no longer expected. Exact request transcripts
check one explicit interrupt and no implicit interrupt or start replay.

These are original Go scenarios, not copied official tests or claims that the
upstream suites were executed. The earlier documented Rust/TypeScript/Python
transport distinctions and separate live-test boundary continue to apply.

## Ticket 03: resume identity and detachment patterns

The Rust [resume suite](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/thread_resume.rs#L2809-L2843)
checks metadata-only resume and unchanged native identity. Its
[alternate-selector cases](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/thread_resume.rs#L5993-L6113)
show that native history/path selection can supersede a supplied ID. This SDK's
typed wrapper deliberately rejects a returned-ID mismatch under its approved
strict identity contract; that restriction is documented rather than attributed
to the native protocol. The Rust
[unsubscribe suite](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/thread_unsubscribe.rs#L200)
checks that detaching does not interrupt an active turn. The Go fake-peer tests
assert absence of any outgoing interrupt and keep local observers independent;
they do not prove that a real server continues executing a turn.

The already-inspected [Python lifecycle cases](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/sdk/python/tests/test_app_server_lifecycle.py#L147-L184)
resume a server-returned ID and compare it with the original. The Go test carries
only that native string across two independently initialized fake peers. It does
not reconstruct history or use SDK persistence. The TypeScript
[resume argument assertions](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/sdk/typescript/tests/exec.test.ts#L76-L100)
informed exact outbound-method/parameter checks, with no reuse of its different
`codex exec` wire transport.

All new Go scenarios are independently authored. They count complete outgoing
method transcripts after errors, mismatches, confirmed dispatch cancellation,
timeout, close, and connection loss. No upstream test suite was run or copied;
real-server persistence, active-turn continuation, and live-model behavior remain
unverified.

## Ticket 04: server requests, answers, and resolution

The Rust [command approval case](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/turn_start.rs#L3020-L3085)
captures an incoming request's native ID, answers that ID with the native command
decision, and checks the matching `serverRequest/resolved` before turn completion.
The [file approval case](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/turn_start.rs#L3887-L3943)
similarly requires resolution before file-item completion. The
[user-input case](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/request_user_input.rs#L124-L174)
keeps question IDs in an answer map and checks thread, turn, item, blocking mode,
and the unchanged native request ID. The
[permissions case](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/request_permissions.rs#L839-L912)
returns native granted-permission profiles rather than an invented boolean
approval. Its resolution matcher ignores other request IDs in the same thread.

The [interrupt case](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/turn_interrupt.rs#L283-L328)
interrupts a turn with an outstanding command approval, then checks resolution of
that request before the interrupted terminal event. Therefore resolution is a
lifecycle signal, not proof of a successful answer or approval. No generic
request-cancellation frame is inferred from it.

These source patterns inform independently authored Go fake-peer scenarios.
Separately, `types/requests_test.go` checks exact signed-64-bit/string request
IDs, equal-text IDs of different kinds, numeric range/fraction rejection, native
modern and legacy decision shapes, permission-path `kind` discriminators,
optional/null fields, and unknown-field preservation. The generated schema
round trips exercise every included model and declared union branch. Those
model tests do not show that a real server emits a given request or accepts a
particular answer. No official suite or real Codex binary was executed for this
work; the existing TypeScript exec/Python app-server seam distinctions still
apply.

## Ticket 05: the same public contract over WS and WSS

The pinned Rust [WebSocket suite](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/connection_handling_websocket.rs#L68-L110)
initializes each connection independently and checks that equal request IDs on
separate connections cannot receive each other's replies. Its
[bearer cases](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/connection_handling_websocket.rs#L269-L299)
reject missing/invalid tokens before a valid authenticated initialization.
[Exact outgoing text messages](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/connection_handling_websocket.rs#L819-L825)
and [control-frame handling](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/connection_handling_websocket.rs#L907-L928)
are inspected as transport evidence. These tests launch the real Rust server
against a mock model; none was executed during this SDK work.

The Go suite independently applies these ideas at the existing external-package
consumer seam. Transport-neutral scenarios are reused through loopback WS and
local WSS bridges to the synthetic peer, without launching Codex. Process
ownership and stderr scenarios remain stdio-specific. Remote-specific tests cover
synthetic bearer headers, trusted/untrusted synthetic certificates, option
validation, non-loopback plaintext policy, redirects, complete-message framing,
control frames, bounded backpressure, and connection-only cleanup. Credentials
are dummy test values, and local CA material never leaves the fixture.

Normal/mock and race runs must be recorded separately in [evidence.json](evidence.json).
Running the existing behavior over WSS establishes synthetic transport behavior,
not production TLS deployment, bearer issuance, real Codex workflow, or paid model
compatibility. The configured CI workflow uses no service secrets or live test
step. A committed workflow is not a hosted CI pass; record a run URL and exact
revision if CI is later executed.
