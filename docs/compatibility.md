# Compatibility and feature inventory

This is the canonical compatibility statement for this Go SDK. It is a deliberately limited, experimental native client, not feature parity with the official Python SDK.

## Subagent observation (tickets 01–06)

The [per-capability subagent matrix](subagent-compatibility.md) is the detailed
subagent compatibility statement: native availability, SDK implementation,
pinned versions, E1/E2 evidence and foreground/background/nested/legacy limits.
OpenTree owns one stream with automatic initialize-time forwarding, lossless
Native and SDK-derived Associated/Created/ObservationState records. **Created is
conditional for exact CLI 2.1.258**, verified locally by `--version`; see the fixed
foreground/corroborated-result rules in the subagent matrix. Version inspection
is not live subagent validation. No E3/E4 subagent tests ran; prior partial live
checks below do not apply.
Session-state emission is opt-in; background snapshots REPLACE, not lifecycle
edges. Parent result, child terminal and native idle remain independent.
See the [runnable offline example](../examples/subagents-offline/main.go) and
[ticket06 validation evidence](evidence/subagent-ticket06.md).

## Official documentation baseline

The [official Agent SDK overview](https://code.claude.com/docs/en/agent-sdk/overview) was reviewed on 2026-10-17 UTC. Its resolved URL was unchanged; fetched HTML SHA256 was `122dae0da4f76a8ebcb79164b4633491d011d7518bcc70fdb33f7eee9308aa67`. This is a mutable documentation page, not a versioned protocol specification. It describes the official Python/TypeScript SDKs; this repository is an independent Go implementation, not an Anthropic product.

The overview's capability categories map as follows: built-in tools execute in the CLI; permission interaction and native session resume have limited Go APIs; hooks, SDK-defined subagents, MCP administration/hosting, skills/commands/memory configuration and plugins have no supported Go configuration APIs. Inherited CLI behavior is not evidence of Go API parity. See the inventories below for precise boundaries.

Use of the installed CLI and service remains subject to their applicable terms and authentication requirements; this repository's Apache-2.0 license covers its own code, not the CLI or service.

## Versions and evidence

- Official protocol source baseline: `anthropics/claude-agent-sdk-python` commit `f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab`.
- That source's `_cli_version.py` references Claude Code **2.1.302**. This is a source target only.
- **Fully runtime-validated Claude Code versions: none.** On 2026-10-18, bounded partial Mac checks ran against installed **2.1.258**, configured model `theta/GLM-5.3[1m]`, with eight synthetic inputs. Normal results, resume, permissions, question answers and pending-question interruption were observed; target **2.1.302** and the complete checklist remain unvalidated. See [ticket04 evidence and explicit limits](evidence/type-ticket04.md). The earlier synthetic inventory below is not upgraded to full live coverage.
- Compiler target: Go 1.26.9 via mise. See the checked-in toolchain pin.
- Implemented means a Go public API/code path exists. Mock-tested means exported API tests exercised a scripted subprocess based on the pinned source. Race-tested means that synthetic suite ran with Go's race detector. Neither means real CLI-tested.
- Unsupported means no supported Go API is provided. Raw preservation means received JSON can be inspected losslessly; it does not implement the corresponding action, schema, callback, or semantic guarantee.

### Reproducible source provenance

[Official source archive](https://github.com/anthropics/claude-agent-sdk-python/archive/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab.tar.gz), SHA256 `07a88363bb6bd3774e79dea95793d8bc14071a2f80dd61f0c457adf2eccb76cc`.

Source file hashes below refer to exact file bytes from that commit. Paths are relative to `src/claude_agent_sdk/`.

| Official source | SHA256 |
| --- | --- |
| [types.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/types.py) | `c06ccef3487b2f5ad81000a72a772b823760db1c32e85693eea57c8b16a93aa2` |
| [client.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/client.py) | `980f956d963338b39f4fd5224e69594eb513bfda37e5b215cf94bb0499ea047c` |
| [query.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/query.py) | `5528bc5bc57992f833f7a1056d1e2847eb71b070766764c84365c2e49a05162e` |
| [__init__.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/__init__.py) | `1582188cfc10ce1eb3892a6e7c6d5639cdb0cca5e9e6ef1a1f5ebf68b75a072b` |
| [_cli_version.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_cli_version.py) | `2fc8a1aad4b2bb7d598143a6edbe79229173c2a4be6f75acec2909f4bd4a6f4b` |
| [_internal/query.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/query.py) | `0562517de949f4431177f204fe84cda3265f495d0eb0a949b9ced96cc5bd8276` |
| [_internal/message_parser.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/message_parser.py) | `febb1aee19e47e03433d89cd4b1f8c5636950e206df77e4f4ca2738b0c900393` |
| [_internal/transport/subprocess_cli.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/transport/subprocess_cli.py) | `23c0b35acfcff5426255f1f17433e66fc7f8f6d5b8f339e93e085233c569e11c` |

## Implemented Go behavior and test evidence

### Independent data package

The canonical definitions for existing events, messages, content blocks,
results, stream views, raw incoming controls/cancellations, permission answers,
permission behaviors, and resource limits live in
`github.com/zoumo/claude-agent-sdk-go/types`. Ticket01 established the package
boundary and compatible aliases without adding permission features.
`types.ParseEvent` owns the frame decoder; the runtime delegates to that same
implementation. Ticket02 adds public `ParseEvent` at the root, bidirectional
message/content/result codecs, explicit JSON presence overrides, envelope
metadata, server tool content, init/task system fields, model usage, rate limits
and conversation reset views. Unknown fields and open discriminators survive
semantic JSON round trips. See [the JSON contract](message-json.md) for exact
scope, construction, Raw/typed precedence and absent/null/zero behavior, and
[type provenance and licenses](type-sources.md). Aggregate usage remains raw JSON.

Ticket03 adds detached `PermissionMetadata`, `PermissionResult`, nested
`ControlResponse`/`ControlResponseData`, and AskUserQuestion input, question,
option and string/array answer data with the same Raw/typed/presence contract.
`Event.Response` exposes control responses. `PermissionRequest.Metadata()`
returns an owned snapshot; it cannot create or transfer answer authority.
`AskUserQuestionInput.WithAnswers` validates full question-text keys and retains
questions/unknown input. Runtime answers still use the original connection-local
handle and the existing `PermissionAnswer` subset. Suggestions and unknown result
fields do not enable persistent updates. See [ticket03 evidence](evidence/type-ticket03.md).

The root aliases these types and permission constants. Compile-time tests cover
cross-package event/answer method signatures, content literals, and original
keyed `Options`/`Limits` literals. Named-type reflection now reports the
canonical `types` package; `reflect.Type.PkgPath`, `%T`, and similar formatted
type names may change and are not promised compatible.

`types.Options` is the callback-free startup configuration. The root's
`OptionsFromTypes` and `Options.Types` explicitly copy its fields without
embedding, defaulting, validation, or cloning `Env`. The root `Options` keeps
its old field layout and callback. Nil versus empty environments remain
distinct. `PermissionRequest`, `PermissionHandler`, error types/sentinels,
process state, locks, registries, queues, and callback execution remain in the
runtime package; `types` has no root import and only standard-library
dependencies. `Open` retains all existing defaults and resource checks.

`types/json_test.go` covers bidirectional wire contracts, presence, edits and union validation.
`types/events_test.go` exercises the independently imported decoder, raw byte
ownership, unknown shapes, malformed existing shapes, and content codecs.
`types_compatibility_test.go` verifies aliases, configuration conversion, and
an existing isolated subprocess through the adapter. The full existing suite
continues to cover cancellation, single-use answers, error propagation,
literal arguments, limits, and unknown write outcomes. CI also checks the
data package's dependency boundary. These are synthetic checks, with no real
CLI, account, or Mac validation.

### Runtime behavior

Verified on 2026-10-18 with Go 1.26.9 on Linux/amd64. “PASS” below refers only to original synthetic subprocess fixtures. Real-CLI examples compile but do not run. The subagents-offline example runs against its own synthetic peer (E2 only). [Ticket05 evidence](evidence/subagent-ticket05.md) additionally records macOS darwin/arm64 offline execution; no platform has subagent live validation.

| Go behavior / public API | Implemented | Mock-tested | Race-tested | Real CLI-tested | Evidence (test file: cases) |
| --- | --- | --- | --- | --- | --- |
| Local launch, literal arguments, environment, matched initialize | Yes | PASS | PASS | UNKNOWN | `client_test.go`: `TestOpenUsesLiteralArgumentsAndMatchedInitialize`, `TestStartupFailuresAreBounded`, `TestStderrIsDrainedDuringInitialize` |
| Complete-frame Send, concurrent serialization, pre-write rejection | Yes | PASS | PASS | UNKNOWN | `client_test.go`: `TestConcurrentSendPreservesFrames`, `TestLocalOversizeRejectedWithoutWriting`, `TestSendCancellationBeforeWriteDoesNotClose` |
| Typed messages/content/results/deltas and raw exposed events | Yes, subset | PASS | PASS | UNKNOWN | `client_test.go`: `TestTypedViewsAndLosslessUnknowns`, `TestFragmentedUnicodeAndLargeFrame` |
| Requested resume versus observed native ID, no automatic replay | Yes | PASS | PASS | UNKNOWN | `resume_interrupt_test.go`: `TestResumeUsesFreshProcessLiteralArgAndNoReplay`, `TestSessionIdentityDistinguishesRequestAndObservation`, `TestSessionIdentityConcurrentReaders` |
| Matched interrupt, stale/out-of-order replies, independent turn result | Yes | PASS | PASS | UNKNOWN | `resume_interrupt_test.go`: `TestInterruptCorrelatesOutOfOrderAndUnknownResponses`, `TestInterruptAcknowledgmentIsSeparateFromTurnResult`, `TestInterruptImmediateAcknowledgments` |
| Deferred permission handles, original IDs/context, allow/update/deny | Yes | PASS | PASS | UNKNOWN | `permissions_test.go`: `TestDeferredPermissionHumanWaitKeepsTransportMoving`, `TestDeferredPermissionRetainedHandleUsesExactUpdatedInput`, `TestPermissionAnswerConcurrentSingleUse` |
| Default deny and explicit unsupported-control errors | Yes | PASS | PASS | UNKNOWN | `client_test.go`: `TestMissingPermissionHandlerDeniesAndUnknownControlErrors` |
| AskUserQuestion data and control input versus assistant tool-use event | Typed input/questions/options/string or array answers; original control handle only | PASS | PASS | UNKNOWN | `types/permission_data_test.go`, `question_interaction_test.go`: full-text keys, unknown input retention, nested response, cancellation; `permissions_test.go`: `TestAssistantQuestionIsOnlyAnEvent`; no live question claim |
| Per-request cancellation, unknown/finished IDs, late-answer rejection | Yes | PASS | PASS | UNKNOWN | `permission_cancellation_test.go`: `TestPermissionTypedCancellationIncludesUnknownAndCompletedIDs`, `TestPermissionCancelledIgnoringHandlerCannotAnswerLater` |
| Epoch-bound handles, single attempt, retired-ID protection | Yes | PASS | PASS | UNKNOWN | `permissions_test.go`: `TestPermissionCloseInvalidatesAndFreshClientHasNewEpoch`, `TestIncomingControlHistoryRejectsRetiredIDReuse`, `TestPermissionRegistryBoundsFailClosed` |
| Known pre-write rejection versus started-write unknown outcome | Yes | PASS (Linux FIFO) | PASS | UNKNOWN | `permission_cancellation_linux_test.go`: `TestQueuedSendAndInterruptDoNotInheritStartedWriteAmbiguity`, `TestPermissionQueuedWriterCancellationPreventsAllAnswerBytes`, `TestPermissionStartedWriteCancellationAndCloseAreAmbiguous` |
| Consumed failed answers, invalid answer/panic fail closed | Yes | PASS | PASS | UNKNOWN | `permission_cancellation_test.go`: `TestPermissionFailedAttemptRemainsDistinctAfterClose`; `permissions_test.go`: `TestPermissionInvalidAnswerAndPanicFailClosed`, `TestPermissionPanicAfterAnswerDoesNotCreateSecondResponse` |
| Finite limits validated before process startup | Yes | PASS | PASS | UNKNOWN | `overload_test.go`: `TestConfigurationRejectsIntegerOverflowBeforeSpawn` |
| Full queue permits correlated control dispatch; overflow is terminal | Yes | PASS | PASS | UNKNOWN | `overload_test.go`: `TestFullEventQueueStillDispatchesControlResponses`, `TestOverloadWakesInterruptAndPermissionWithRetainedOrder`; `client_test.go`: `TestOverflowIsTerminalAndDoneDoesNotNeedConsumer` |
| Bounded control waits, permission work, history, automatic replies | Yes | PASS | PASS | UNKNOWN | `resume_interrupt_test.go`: `TestInterruptPendingLimitRejectsWithoutWriting`; `permissions_test.go`: `TestPermissionWorkerBoundAndCloseDoNotWaitForUserCode`; `permission_cancellation_test.go`: `TestPermissionUnknownCancelHistoryExhaustionIsExplicit`; `permission_cancellation_linux_test.go`: `TestAutomaticResponseQueueOverflowUnblocksStartedWriter` |
| Disconnect/malformed/oversize/truncated output wakes pending work | Yes | PASS | PASS | UNKNOWN | `overload_test.go`: `TestTransportFailuresWakeAllPendingWork`; `resume_interrupt_test.go`: `TestInterruptWaitersWakeOnTermination` |
| Accepted output drains before final failure; Done does not need space | Yes | PASS | PASS | UNKNOWN | `client_test.go`: `TestMalformedFramesRetainAcceptedEvents`, `TestCloseRetainsBufferedEvents`, `TestCloseAfterDoneRetainsFinalProtocolError`; `overload_test.go`: `TestOverloadWakesInterruptAndPermissionWithRetainedOrder` |
| Bounded stderr, exit status, independent terminal diagnostics | Yes | PASS | PASS | UNKNOWN | `client_test.go`: `TestExitErrorFollowsLastEventWithBoundedDiagnostics`; `overload_test.go`: `TestTransportFailuresWakeAllPendingWork` |
| Idempotent Close, initialization failure reaping, terminate/kill | Yes | PASS (Linux) | PASS | UNKNOWN | `client_test.go`: `TestConcurrentCloseAndReceiveWake`; `lifecycle_unix_test.go`: `TestInitializationTimeoutReapsChild`, `TestCloseReapsUncooperativeChild`, `TestCloseEscalatesToKillForIgnoredSIGTERM` |
| Process-group descendants, including stdout holders and closed pipes | POSIX group implementation | PASS (Linux) | PASS (Linux) | UNKNOWN | `lifecycle_unix_test.go`: `TestProcessGroupTeardownAndDescendantHoldingStdout` (parent alive, parent exited, descendant closed pipes) |
| Close does not wait for handlers ignoring cancellation | Yes | PASS | PASS | UNKNOWN | `overload_test.go`: `TestShutdownDoesNotWaitForIgnoringCallback`; `permissions_test.go`: `TestPermissionWorkerBoundAndCloseDoNotWaitForUserCode` |

Raw preservation covers frames exposed by `Receive` and permission metadata. Correlated `control_response` frames are consumed internally, including initialization metadata; there is no generic raw-control or server-info API. Typed views intentionally cover only the fields in `types/events.go`. These Go P0 views are handwritten. There is no automatic upstream type synchronization or generated binding layer. The official inventory below was mechanically enumerated from the pinned Python AST and then manually classified; this is an audit of that revision, not a generated API or a future drift guarantee.

A process exit can wake `Done`, pending controls and permission contexts before stdout finishes decoding its final bytes. Those immediate causes describe the earliest known failure; `Close`, terminal `Receive`, and `Err` after cleanup retain a later, more specific protocol/resource error and exit diagnostics. New or queued writes rejected before starting return `ErrClosed` (or their caller-context error) without inheriting another operation's `ErrUnknownOutcome`. Inspect `Client.Err` separately for that connection's cause.

### Resource and shutdown contract

Zero selects the default. Negative values or values above the validated ceiling fail before spawning the CLI. Memory also depends on frame size, retained typed/raw copies, caller-owned input and any events/handles the application keeps; these are finite queue/resource bounds, not a fixed total RSS promise.

| Limit | Default | Maximum accepted | Exhaustion behavior |
| --- | ---: | ---: | --- |
| `EventBuffer` | 128 | 65,536 | Incoming overflow closes connection; accepted events remain ordered |
| `FrameBytes` | 4 MiB | 64 MiB | Maximum UTF-8 JSON bytes, excluding newline; incoming excess is terminal, local Send excess is pre-write/nonterminal |
| `PendingControls` | 32 | 65,536 | Local excess rejected before writing; connection remains usable |
| `ControlBuffer` | 32 each | 65,536 each | Separate automatic-response and permission-handler queues; incoming excess is terminal |
| `StderrBytes` | 8 KiB | 16 MiB | Retains only newest diagnostic bytes; never automatically logs |
| `PendingPermissions` | 128 | 65,536 | Counts unanswered incoming controls; incoming excess is terminal |
| `PermissionWorkers` | 4 | 1,024 | Concurrent handler calls; ignoring cancellation consumes a worker |
| `PermissionHistory` | 4,096 | 1,048,576 | Distinct incoming request/cancellation IDs; no eviction; exhaustion is terminal |

One automatic-response worker and the configured handler pool replace unbounded per-request goroutines. There is one serialized encoder/pipe writer; application goroutines waiting to call it remain the application's responsibility. Encoding may temporarily expand escaped text; callers should bound their own input and concurrency. A reserved permission answer that fails local validation/size checks consumes its attempt and terminates the connection, unlike an oversized ordinary Send.

`InitializeTimeout` defaults to 60 seconds. `ShutdownTimeout` defaults to 3 seconds, shared across drain, terminate and kill phases; final pipe closure wakes readers/writers. Contexts bound caller writes/control waits. `Close` is independent of caller cancellation and consumer progress, but cannot guarantee OS scheduling deadlines or reap a process stuck in an uninterruptible kernel state; `ErrShutdownTimeout` exposes failure to finish in the budget. One wait owner continues to reap the direct child when the OS permits.

On Linux, macOS, FreeBSD, OpenBSD, NetBSD and DragonFly, the CLI starts in its own process group. Cleanup escalates group signals for pipe holders and kills remaining same-group members after the direct child exits, including descendants that closed their inherited pipes. Only Linux/amd64 has executed tests. Orphan descendants are reaped by the host, not by this SDK; descendants that escape the process group/session are outside this guarantee. Other platforms kill the direct child only; descendant-tree cleanup is unsupported and unverified. Arbitrary Go callback goroutines cannot be forcibly stopped and may outlive the client if they ignore their context.

### Reproduce the synthetic checks

The module uses only the Go standard library. Install the compiler pinned in `mise.toml`, then run from the repository root:

```sh
mise exec go@1.26.9 -- go version
mise exec go@1.26.9 -- go test -count=1 -timeout 180s ./...
mise exec go@1.26.9 -- go test -race -count=1 -timeout 300s ./...
mise exec go@1.26.9 -- go vet ./...
mise exec go@1.26.9 -- go test -race -count=30 -timeout 300s -run 'Test(FullEventQueue|OverloadWakes|TransportFailures|QueuedSendAndInterrupt|AutomaticResponseQueue|CloseAfterDone|ShutdownDoesNotWait)' ./...
mise exec go@1.26.9 -- go test -race -count=10 -timeout 180s -run 'TestProcessGroup|TestCloseEscalates' ./...
```

The full test/race/vet and focused repeated checks passed in a cleared environment with an isolated HOME, `GOENV=off`, `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`, and `GORACE=atexit_sleep_ms=0`. Every fixture additionally replaces its subprocess environment and uses a temporary HOME/workdir. CI repeats test/race/vet with no account or live CLI. These results do not execute the Python suite or any model-backed integration test.


## Official-test behavioral references

The user-facing upstream [tests on main](https://github.com/anthropics/claude-agent-sdk-python/tree/main/tests) were resolved through the GitHub commits API on 2026-10-17 to the same pinned commit above. Go tests are original public-client/subprocess scenarios informed by these behavioral contracts, not a claim that the Python suite was ported in full or run against this Go code. No official implementation/test source is redistributed. Unsupported hooks, MCP, session stores and other official test categories are not passed by analogy.

| Pinned official test source | Behavior used | SHA256 |
| --- | --- | --- |
| [test_query.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/tests/test_query.py) | initialization and control envelopes | `2c8ae92d78b46ff2bbd82c1afae4cf7c7ad239f7cfb5e74fe446586e0e7402a2` |
| [test_message_parser.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/tests/test_message_parser.py) | typed content and message parsing | `99a6712c872eba46e260f8c6e439ef52158453f7326f5a8fba9c4dc79234adee` |
| [test_subprocess_buffering.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/tests/test_subprocess_buffering.py) | chunk boundaries, Unicode, frame limits | `3bbec7d2893851542fe6107d2402915402f37b0c5d0d64dfdd10630e41d0fac7` |
| [test_close_cancellation.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/tests/test_close_cancellation.py) | cleanup despite cancellation | `8f0d3266f6d01ada50d04cdea381c2e3988251f7fbdf9c4e17d26bee14689d9a` |
| [test_session_resume.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/tests/test_session_resume.py) | native resume option semantics | `a19d5c86b447d1bd58b2e53b0d0f40fa43f332989f5f95cd945eb81b083469ce` |
| [test_streaming_client.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/tests/test_streaming_client.py) | stream lifecycle and interrupt | `68627a1d9f279dbf35b93050fba0c410065d63fdc7e059e9c424774ba7d08383` |
| [test_transport.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/tests/test_transport.py) | literal launch arguments and subprocess errors | `f5f795d75a3b8cecb6b0ec417cd727f1015c80765dbaead6b02f6a4c92772338` |
| [test_tool_callbacks.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/tests/test_tool_callbacks.py) | original-ID allow/deny and updated input | `13fbe491655501a4026723be3f74c90d7007dcf6143fa2d9c7bce7ee5a47db74` |

Cancellation/duplicate/epoch guards, explicit overflow and adversarial teardown tests additionally enforce this Go SDK’s design; they are not claims of Python API equivalence.

## Official control protocol inventory

These are CLI-wire operations, not local session-file utilities. The runtime source `_internal/query.py` is authoritative where a subtype is absent from the static TypedDict union.

| Operation | Go support | Boundary |
| --- | --- | --- |
| `initialize` | Implemented, matched request/response | Matched handshake and optional forwardSubagentText; OpenTree enables it automatically. No hook/agent/skills registration or prompt snapshot |
| `interrupt` | Implemented, matched request/response | Acknowledgment is not rollback and does not replace a later turn result |
| `can_use_tool` | Implemented, deferred handle | Original ID and input retained; allow/updated input or deny; no updated-permission rules or deny-interrupt shortcut |
| `control_cancel_request` | Implemented | Observable request ID; invalidates handles; no durable cross-process cancellation journal |
| `control_response` success/error | Implemented correlation | Unknown/stale IDs do not satisfy another waiter; these response frames are consumed internally, not exposed as events |
| `set_permission_mode` | Unsupported | No dynamic permission-mode setter |
| `set_model` | Unsupported | Launch-time model selection is separate |
| `rewind_files` | Unsupported | No file checkpoint or rewind API |
| `mcp_status`, `mcp_reconnect`, `mcp_toggle` | Unsupported | No MCP administration API |
| `get_context_usage` | Unsupported | No context-usage query API |
| `stop_task` | Unsupported | No background-task control API |
| `hook_callback` | Unsupported; explicit error response | Hook output schemas and registration are not implemented |
| `mcp_message` | Unsupported; explicit error response | No SDK-hosted MCP server or JSON-RPC bridge |
| Unknown incoming control subtype | Explicit unsupported error | Never silent success |

`AskUserQuestion` is only answerable here when received as `can_use_tool`. Callers supply updated input through the original permission handle. An assistant tool-use block with that name is an event only: the SDK does not inject a synthetic tool-result message. Actual live question flow remains unverified.

## Official client methods

All 15 public methods on the pinned `ClaudeSDKClient` are accounted for separately from top-level package exports and wire operations.

| Official method | Go equivalent / boundary |
| --- | --- |
| `connect` | `Open`; initializes immediately and owns one local process |
| `receive_messages` | `Receive`; one consumer, raw and partial typed views |
| `query` | `Send` only accepts text, not arbitrary async input dictionaries or caller-set session envelope IDs |
| `interrupt` | `Interrupt`; correlated acknowledgment |
| `set_permission_mode` | Unsupported |
| `set_model` | Unsupported dynamic setter; launch-time `Options.Model` only |
| `rewind_files` | Unsupported |
| `reconnect_mcp_server` | Unsupported |
| `toggle_mcp_server` | Unsupported |
| `stop_task` | Unsupported |
| `get_mcp_status` | Unsupported |
| `get_context_usage` | Unsupported |
| `get_server_info` | Unsupported; initialization metadata is not exposed |
| `receive_response` | No turn-bounded convenience iterator; callers inspect `Event.Result` |
| `disconnect` | Idempotent `Close` with bounded process/pipe cleanup |

Python async-context-manager methods have no direct Go equivalent; use `defer client.Close()`. The exported Python `Transport` extension point and its `connect`, `write`, `read_messages`, `close`, `is_ready`, and `end_input` methods are not supported as a Go custom-transport API. There is no standalone stdin-half-close API.

## Official launch/configuration inventory

Every one of the 49 fields of the pinned `ClaudeAgentOptions` is listed. An equivalent Go option need not have the same name or default. Settings and policies inherited by the CLI can affect execution even where this SDK has no configuration API; permission callbacks are not a sandbox and are not guaranteed to see every tool.

| Official option | Go support / difference |
| --- | --- |
| `tools` | Unsupported |
| `allowed_tools` | Unsupported |
| `system_prompt` | Unsupported |
| `mcp_servers` | Unsupported |
| `strict_mcp_config` | Unsupported |
| `permission_mode` | Unsupported |
| `continue_conversation` | Unsupported |
| `resume` | Implemented: explicit native ID; no replay or pending-handle restoration |
| `session_id` | Unsupported |
| `max_turns` | Unsupported |
| `max_budget_usd` | Unsupported |
| `disallowed_tools` | Unsupported |
| `model` | Implemented: launch model |
| `fallback_model` | Unsupported |
| `betas` | Unsupported |
| `permission_prompt_tool_name` | Unsupported |
| `cwd` | Implemented: launch working directory |
| `cli_path` | Implemented: explicit executable or executable discovery |
| `settings` | Unsupported |
| `add_dirs` | Unsupported |
| `env` | Implemented differently: nil inherits; non-nil replaces the complete child environment (Python merges overrides into inherited environment) |
| `extra_args` | Unsupported |
| `max_buffer_size` | Implemented differently: finite frame-byte and queue/resource limits |
| `debug_stderr` | Unsupported |
| `stderr` | Unsupported |
| `can_use_tool` | Implemented differently: deferred, single-answer permission handles |
| `hooks` | Unsupported |
| `user` | Unsupported |
| `include_partial_messages` | Always enabled with --include-partial-messages; no disable option |
| `include_hook_events` | Unsupported |
| `forward_subagent_text` | Implemented as Options.ForwardSubagentText; OpenTree enables it before initialize. E1/E2 only, live unverified; see [subagent matrix](subagent-compatibility.md) |
| `verbatim_prompts` | Unsupported |
| `fork_session` | Unsupported |
| `resume_session_at` | Unsupported |
| `resume_drops_turn` | Unsupported |
| `agents` | Unsupported |
| `setting_sources` | Unsupported |
| `skills` | Unsupported |
| `sandbox` | Unsupported |
| `plugins` | Unsupported |
| `max_thinking_tokens` | Unsupported |
| `thinking` | Unsupported |
| `effort` | Unsupported |
| `output_format` | Unsupported |
| `enable_file_checkpointing` | Unsupported |
| `session_store` | Unsupported |
| `session_store_flush` | Unsupported |
| `load_timeout_ms` | Unsupported |
| `task_budget` | Unsupported |

Go-specific finite limits, caller-context write deadlines and initialize/shutdown timeouts, terminal errors, and connection-local permission guards are described in the API and README. These are Go-client contracts, not claims that Python has identical behavior. No arbitrary-arguments escape hatch is advertised as supported feature parity.

## Official hooks

All hook registration, matching, timeouts, callback outputs, async/continue conversion, and hook policy helpers are unsupported. The public `HookEvent` union at this pin is:

`Literal['PreToolUse'] | Literal['PostToolUse'] | Literal['PostToolUseFailure'] | Literal['UserPromptSubmit'] | Literal['Stop'] | Literal['SubagentStop'] | Literal['PreCompact'] | Literal['Notification'] | Literal['SubagentStart'] | Literal['PermissionRequest']`

The source also exports hook-specific output schemas beyond that union (listed below). Their presence is not treated as proof that this Go SDK can register or execute them. Received hook-related JSON can survive as raw events without typed hook functionality.

## Official public symbols and helper inventory

This exhaustive list follows all 159 names in `__init__.__all__` at the pinned commit. “Partial typed/raw” means a corresponding Go event/view covers the basic P0 fields and retains the complete wire JSON, not that every Python field has a dedicated Go member. Python-language names and wrappers are not reproduced verbatim.

| Official symbol | Kind / Go support |
| --- | --- |
| `query` | Python one-shot helper: unsupported; use explicit Open/Send/Receive/Close |
| `__version__` | Python package metadata: not a Go compatibility API |
| `Transport` | Python transport extension point: unsupported; Go local subprocess only |
| `ClaudeSDKClient` | Client abstraction: partial equivalent via Go client |
| `PermissionMode` | Configuration/control schema: unsupported; see option/control inventories |
| `EffortLevel` | Configuration/control schema: unsupported; see option/control inventories |
| `McpServerConfig` | Configuration/control schema: unsupported; see option/control inventories |
| `McpSdkServerConfig` | Configuration/control schema: unsupported; see option/control inventories |
| `McpServerStatus` | Configuration/control schema: unsupported; see option/control inventories |
| `McpServerStatusConfig` | Configuration/control schema: unsupported; see option/control inventories |
| `McpServerConnectionStatus` | Configuration/control schema: unsupported; see option/control inventories |
| `McpServerInfo` | Configuration/control schema: unsupported; see option/control inventories |
| `McpStatusResponse` | Configuration/control schema: unsupported; see option/control inventories |
| `McpToolAnnotations` | Configuration/control schema: unsupported; see option/control inventories |
| `McpToolInfo` | Configuration/control schema: unsupported; see option/control inventories |
| `UserMessage` | CLI data type: partial typed/raw equivalent |
| `AssistantMessage` | CLI data type: partial typed/raw equivalent |
| `SystemMessage` | CLI data type: partial typed/raw equivalent |
| `SystemInitData` | CLI data type: partial typed/raw equivalent |
| `SystemInitMcpServer` | CLI data/schema: raw preservation only; dedicated typed parity unsupported |
| `SystemInitPlugin` | CLI data/schema: raw preservation only; dedicated typed parity unsupported |
| `SystemInitPluginError` | CLI data/schema: raw preservation only; dedicated typed parity unsupported |
| `FastModeState` | CLI data/schema: raw preservation only; dedicated typed parity unsupported |
| `FastModeDisabledReason` | CLI data/schema: raw preservation only; dedicated typed parity unsupported |
| `TaskStartedMessage` | Shared typed SystemEvent task fields; raw unknown metadata retained |
| `TaskProgressMessage` | Shared typed SystemEvent task fields; usage remains raw JSON |
| `TaskUpdatedMessage` | Shared typed SystemEvent task_id and raw patch; no computed lifecycle state |
| `TaskNotificationMessage` | Shared typed SystemEvent task fields; usage remains raw JSON |
| `TaskNotificationStatus` | CLI data/schema: raw preservation only; dedicated typed parity unsupported |
| `TaskUpdatedStatus` | CLI data/schema: raw preservation only; dedicated typed parity unsupported |
| `TERMINAL_TASK_STATUSES` | CLI data/schema: raw preservation only; dedicated typed parity unsupported |
| `TaskUsage` | CLI data/schema: raw preservation only; dedicated typed parity unsupported |
| `ResultMessage` | CLI data type: partial typed/raw equivalent |
| `ModelUsage` | Typed map under wire modelUsage, camelCase fields and unknown retention |
| `DeferredToolUse` | Typed ToolUseBlock result field, without execution API |
| `RateLimitEvent` | Typed RateLimitEvent view |
| `RateLimitInfo` | Typed core wire fields with raw unknown retention |
| `RateLimitStatus` | CLI data/schema: raw preservation only; dedicated typed parity unsupported |
| `RateLimitType` | CLI data/schema: raw preservation only; dedicated typed parity unsupported |
| `StreamEvent` | CLI data type: partial typed/raw equivalent |
| `ConversationResetMessage` | Typed ConversationResetEvent view; no session-store action |
| `Message` | CLI data type: partial typed/raw equivalent |
| `MessageOrigin` | Typed open Kind with remaining keys raw |
| `MessageOriginKind` | CLI data/schema: raw preservation only; dedicated typed parity unsupported |
| `TaskNotificationOriginSubkind` | CLI data/schema: raw preservation only; dedicated typed parity unsupported |
| `ClaudeAgentOptions` | Configuration: partial, see exhaustive option inventory |
| `TaskBudget` | Configuration/control schema: unsupported; see option/control inventories |
| `TextBlock` | CLI data type: partial typed/raw equivalent |
| `ThinkingBlock` | CLI data type: partial typed/raw equivalent |
| `ThinkingConfig` | Configuration/control schema: unsupported; see option/control inventories |
| `ThinkingConfigAdaptive` | Configuration/control schema: unsupported; see option/control inventories |
| `ThinkingConfigEnabled` | Configuration/control schema: unsupported; see option/control inventories |
| `ThinkingConfigDisabled` | Configuration/control schema: unsupported; see option/control inventories |
| `ToolUseBlock` | CLI data type: partial typed/raw equivalent |
| `ToolResultBlock` | CLI data type: partial typed/raw equivalent |
| `ServerToolName` | CLI data/schema: raw preservation only; dedicated typed parity unsupported |
| `ServerToolUseBlock` | Typed server tool branch, no client answer API |
| `ServerToolResultBlock` | Typed server result identifiers with opaque content |
| `ContentBlock` | CLI data type: partial typed/raw equivalent |
| `ContextUsageCategory` | Configuration/control schema: unsupported; see option/control inventories |
| `ContextUsageResponse` | Configuration/control schema: unsupported; see option/control inventories |
| `CanUseTool` | Callback: replaced with deferred Go permission handle |
| `CanUseToolShadowedWarning` | Python warning helper: unsupported; CLI policy caveat documented |
| `ToolPermissionContext` | Permission data: raw context and identifiers preserved |
| `PermissionResult` | Permission decision: allow/update input/deny subset |
| `PermissionResultAllow` | Permission decision: updatedInput supported; updatedPermissions unsupported |
| `PermissionResultDeny` | Permission decision: deny message supported; interrupt shortcut unsupported |
| `PermissionUpdate` | Configuration/control schema: unsupported; see option/control inventories |
| `HookCallback` | Hook schema/helper: unsupported |
| `HookContext` | Hook schema/helper: unsupported |
| `HookInput` | Hook schema/helper: unsupported |
| `HookEventMessage` | CLI data/schema: raw preservation only; dedicated typed parity unsupported |
| `BaseHookInput` | Hook schema/helper: unsupported |
| `PreToolUseHookInput` | Hook schema/helper: unsupported |
| `PostToolUseHookInput` | Hook schema/helper: unsupported |
| `PostToolUseFailureHookInput` | Hook schema/helper: unsupported |
| `PostToolUseFailureHookSpecificOutput` | Hook schema/helper: unsupported |
| `UserPromptSubmitHookInput` | Hook schema/helper: unsupported |
| `StopHookInput` | Hook schema/helper: unsupported |
| `SubagentStopHookInput` | Hook schema/helper: unsupported |
| `PreCompactHookInput` | Hook schema/helper: unsupported |
| `NotificationHookInput` | Hook schema/helper: unsupported |
| `SubagentStartHookInput` | Hook schema/helper: unsupported |
| `PermissionRequestHookInput` | Hook schema/helper: unsupported |
| `NotificationHookSpecificOutput` | Hook schema/helper: unsupported |
| `SubagentStartHookSpecificOutput` | Hook schema/helper: unsupported |
| `PermissionRequestHookSpecificOutput` | Hook schema/helper: unsupported |
| `PreToolUseHookSpecificOutput` | Hook schema/helper: unsupported |
| `PostToolUseHookSpecificOutput` | Hook schema/helper: unsupported |
| `UserPromptSubmitHookSpecificOutput` | Hook schema/helper: unsupported |
| `SessionStartHookSpecificOutput` | Hook schema/helper: unsupported |
| `HookEffort` | Hook schema/helper: unsupported |
| `BackgroundTaskSummary` | Hook schema/helper: unsupported |
| `SessionCronSummary` | Hook schema/helper: unsupported |
| `UserPromptExpansionHookSpecificOutput` | Hook schema/helper: unsupported |
| `PostToolBatchHookSpecificOutput` | Hook schema/helper: unsupported |
| `StopHookSpecificOutput` | Hook schema/helper: unsupported |
| `SubagentStopHookSpecificOutput` | Hook schema/helper: unsupported |
| `PermissionDeniedHookSpecificOutput` | Hook schema/helper: unsupported |
| `PreModelSwitchHookSpecificOutput` | Hook schema/helper: unsupported |
| `PostModelSwitchHookSpecificOutput` | Hook schema/helper: unsupported |
| `ElicitationHookSpecificOutput` | Hook schema/helper: unsupported |
| `ElicitationResultHookSpecificOutput` | Hook schema/helper: unsupported |
| `CwdChangedHookSpecificOutput` | Hook schema/helper: unsupported |
| `FileChangedHookSpecificOutput` | Hook schema/helper: unsupported |
| `WorktreeCreateHookSpecificOutput` | Hook schema/helper: unsupported |
| `MessageDisplayHookSpecificOutput` | Hook schema/helper: unsupported |
| `HookJSONOutput` | Hook schema/helper: unsupported |
| `HookMatcher` | Hook schema/helper: unsupported |
| `AgentDefinition` | Configuration/control schema: unsupported; see option/control inventories |
| `SettingSource` | Configuration/control schema: unsupported; see option/control inventories |
| `SdkPluginConfig` | Configuration/control schema: unsupported; see option/control inventories |
| `list_sessions` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `get_session_info` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `get_session_messages` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `list_subagents` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `get_subagent_messages` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `SDKSessionInfo` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `SessionMessage` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `SessionKey` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `SessionStore` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `SessionStoreEntry` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `SessionStoreFlushMode` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `SessionStoreListEntry` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `SessionSummaryEntry` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `SessionListSubkeysKey` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `InMemorySessionStore` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `fold_session_summary` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `MirrorErrorMessage` | CLI data/schema: raw preservation only; dedicated typed parity unsupported |
| `project_key_for_directory` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `import_session_to_store` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `list_sessions_from_store` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `get_session_info_from_store` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `get_session_messages_from_store` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `list_subagents_from_store` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `get_subagent_messages_from_store` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `rename_session` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `tag_session` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `delete_session` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `fork_session` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `ForkSessionResult` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `rename_session_via_store` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `tag_session_via_store` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `delete_session_via_store` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `fork_session_via_store` | Python session-file/store helper: unsupported (distinct from CLI --resume) |
| `SdkBeta` | Configuration/control schema: unsupported; see option/control inventories |
| `SandboxSettings` | Configuration/control schema: unsupported; see option/control inventories |
| `SandboxNetworkConfig` | Configuration/control schema: unsupported; see option/control inventories |
| `SandboxIgnoreViolations` | Configuration/control schema: unsupported; see option/control inventories |
| `create_sdk_mcp_server` | Python SDK-hosted MCP/tool helper: unsupported |
| `tool` | Python SDK-hosted MCP/tool helper: unsupported |
| `SdkMcpTool` | Python SDK-hosted MCP/tool helper: unsupported |
| `ToolAnnotations` | Python SDK-hosted MCP/tool helper: unsupported |
| `ClaudeSDKError` | Python exception helper: not reproduced; Go errors distinguish transport/protocol/control/resource/answer outcomes |
| `CLIConnectionError` | Python exception helper: not reproduced; Go errors distinguish transport/protocol/control/resource/answer outcomes |
| `CLINotFoundError` | Python exception helper: not reproduced; Go errors distinguish transport/protocol/control/resource/answer outcomes |
| `ProcessError` | Python exception helper: not reproduced; Go errors distinguish transport/protocol/control/resource/answer outcomes |
| `ResultError` | Python exception helper: not reproduced; Go errors distinguish transport/protocol/control/resource/answer outcomes |
| `CLIJSONDecodeError` | Python exception helper: not reproduced; Go errors distinguish transport/protocol/control/resource/answer outcomes |

## Additional official `types.py` schemas

These names are defined in the pinned types module but are not all re-exported by the package. No unlisted typed parity should be inferred.

| Schemas | Go support |
| --- | --- |
| `SystemPromptPreset`, `SystemPromptCustom`, `SystemPromptFile`, `ToolsPreset` | Unsupported configuration schemas |
| `PermissionUpdateDestination`, `PermissionRuleValue` | Unsupported persistent permission updates |
| `PermissionBehavior` | Only allow/deny answers supported; no ask/update-rule API |
| `HookEvent`, `HookSpecificOutput`, `AsyncHookJSONOutput`, `SyncHookJSONOutput` | Unsupported |
| `McpStdioServerConfig`, `McpSSEServerConfig`, `McpHttpServerConfig`, `McpSdkServerConfigStatus`, `McpClaudeAIProxyServerConfig` | Unsupported SDK configuration/status schemas |
| `AssistantMessageError` | Open optional Event.Error string; dedicated closed enum unsupported |
| `ThinkingDisplay` | Unsupported configuration schema |
| `SDKControlInterruptRequest`, `SDKControlPermissionRequest`, `SDKControlInitializeRequest` | Implemented P0 wire subset; see control inventory |
| `SDKControlSetPermissionModeRequest`, `SDKHookCallbackRequest`, `SDKControlMcpMessageRequest`, `SDKControlRewindFilesRequest`, `SDKControlMcpReconnectRequest`, `SDKControlMcpToggleRequest`, `SDKControlStopTaskRequest` | Unsupported operations; incoming unsupported requests get an error |
| `SDKControlRequest`, `ControlResponse`, `ControlErrorResponse`, `SDKControlResponse` | Implemented envelope/correlation subset; not a generic public control API |

## Boundaries and live gate

The CLI owns native session persistence. This SDK does not implement session browsing/mutation/import, a custom session store, business journals, UI, durable replay, provider-neutral aggregation, process adoption, or automatic resend. Unsupported SDK options may still exist in a user's installed CLI; this inventory does not disable or certify inherited CLI behavior.

Before making a real-CLI support claim, obtain separate authorization and execute the [live compatibility checklist](live-compatibility.md) against a recorded executable version. Keep live evidence separate from source review, mocks, race results and platform-specific teardown tests. A successful mock resume checks argv and observed IDs only; it does not prove CLI history persistence or model behavior.
