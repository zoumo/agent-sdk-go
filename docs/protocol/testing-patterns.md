# Official test patterns inspected

Source inspection date: 2026-10-17. Every link below is pinned to official Codex
revision `d27764b82f7118f674371e6d6e76271d9d606edb`. Exact Git blob and SHA-256
checksums are in [sources.json](sources.json). None of these upstream suites was
run during SDK implementation, and no test source was copied into this SDK.

## Rust: app-server integration boundary

The official [TestAppServer helper](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/codex-rs/app-server/tests/common/test_app_server.rs)
launches a real test app-server subprocess with piped stdin/stdout/stderr, an
isolated Codex home, a separately drained diagnostic stream, correlation IDs, and
explicit initialization. It checks the initialize response ID before writing
`initialized`. Helpers distinguish responses from notifications and use deadlines.

The [stdio connection suite](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/codex-rs/app-server/tests/suite/v2/connection_handling_stdio_tests.rs)
exercises EOF/SIGTERM cleanup, open pipes, unread stderr, blocked writes, and bounded
exit. These tests verify the server's shutdown behavior; they do not prove this
Go client's ownership or cleanup behavior.

The [mock model server](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/codex-rs/app-server/tests/common/mock_model_server.rs)
uses a local HTTP Responses endpoint, ordered synthetic SSE responses, and exact
request-count expectations. This replaces the model service while leaving the
app-server implementation real. It is a different boundary from a scripted fake
app-server peer.

## TypeScript: exec SDK boundary

The official [TypeScript exec implementation](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/sdk/typescript/src/exec.ts)
launches `codex exec --experimental-json`. It is not the bidirectional app-server
JSON-RPC transport. Its event/result behavior cannot be used as app-server wire
conformance evidence.

[exec.test.ts](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/sdk/typescript/tests/exec.test.ts)
mocks process spawn with an event-emitting fake child and independent stdin,
stdout, and stderr streams. It tests early exit before stream close and inspects
exact argument construction. [codexExecSpy.ts](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/sdk/typescript/tests/codexExecSpy.ts)
records arguments and environment while preserving the configured spawn behavior.
[responsesProxy.ts](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/sdk/typescript/tests/responsesProxy.ts)
provides a local model-response HTTP/SSE fixture and captured request bodies/headers.

Useful transferable ideas are independent stream lifecycle controls, exact
argv/environment assertions, final output arriving near exit, and counting
requests. The Go tests implement those ideas at their own exported-client seam.

## Python: app-server SDK and separate live tests

The pinned official tree contains both `sdk/python` and `sdk/python-runtime`.
Python SDK tests are present; no absence claim is made.

[app_server_harness.py](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/sdk/python/tests/app_server_harness.py)
starts a local queued Responses HTTP/SSE fixture, records requests, creates an
isolated workspace/Codex home, and configures a real app-server binary.
[test_app_server_lifecycle.py](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/sdk/python/tests/test_app_server_lifecycle.py)
uses public synchronous and asynchronous clients to exercise initialization,
thread metadata, and persisted history. Again, a real app-server with a mock model
is not a fake app-server contract test.

[test_async_client_behavior.py](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/sdk/python/tests/test_async_client_behavior.py)
stubs wrapped synchronous methods and checks overlapping async calls and delegated
notification registration. That is a unit-test seam, not wire coverage.

[test_real_app_server_integration.py](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/sdk/python/tests/test_real_app_server_integration.py)
is separately gated by `RUN_REAL_CODEX_TESTS=1`, prepares a pinned runtime, and
includes actual turn/model examples. Its presence does not mean those tests ran,
that all tests are offline, or that its runtime/schema pair matches this Go SDK.
We did not invoke that gate or launch any real Codex binary.

## Application to this SDK

The primary seam remains a separately compiled consumer of exported Go APIs
against a scripted owned child process. It checks received frames, native IDs,
returned results/errors, process exit, and deadlines rather than private queues
or maps. Unit model-fidelity tests supplement that seam. Race-detector results
are recorded separately from fake-peer behavior and live compatibility.

Read the actual [evidence record](evidence.json) for executed Go commands and
scenarios. Upstream patterns guide independently authored tests; upstream passing
status, paid service behavior, and broader SDK functionality are not inherited.
