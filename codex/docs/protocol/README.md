# Pinned protocol provenance

The [canonical compatibility record](../../README.md#compatibility) distinguishes
implementation from test evidence. Its complete machine-readable matrix is
[capabilities.json](capabilities.json). All source records in
[sources.json](sources.json) point to the same immutable official Codex revision,
`092d3acd6bec3e3a14bdc7e7a2810ab628ab759d` (workspace version `0.162.1`).

## What was inspected

1. `app-server-protocol/src/protocol/common.rs`: the complete declared client
   request, server request, and notification registries, including source-only
   legacy/internal entries. Native method names are preserved exactly.
2. `app-server-protocol/src/rpc.rs`: envelopes, optional request trace context,
   string-or-i64 identifiers, error payloads, and the absent `jsonrpc` member.
3. Stable aggregate schema and the experimental precomputed export archive:
   parameter/result definitions, schema membership, and native types.
4. `app-server-protocol/src/export.rs`: why generated aggregates omit legacy
   client requests, internal raw-response notifications, and, in the stable
   export, experimental request variants and fields.
5. Official stdio, WebSocket, local control socket, and transport dispatch source:
   line framing, EOF, WebSocket controls, and the separate managed-daemon shutdown
   handshake. No generic client cancel method was invented.
6. Official Rust, TypeScript, and Python test sources, for independently applied
   test-design patterns only. See [inspected test sources](#inspected-test-sources).

The inventory is the source-registry union, not just the smaller stable schema.
`in_stable_schema` and `in_experimental_schema` expose each entry's presence.
`experimental_gate` records explicit variant-level gating; `field_experimental_gates`
indicates parameter inspection and is not an exhaustive list of gated fields.
Follow the referenced native definition for individual field semantics. A schema
membership flag is not an SDK support or server-runtime availability guarantee.

## Generated material

[types/schema.json](../../types/schema.json) contains the definition closure selected by
[generate_protocol.py](../../scripts/generate_protocol.py), including thread list/read,
turn/item history, settings, approvals and Session lifecycle roots. The generator
verifies the official experimental archive's Git blob and extracts its
`json_schema["codex_app_server_protocol.schemas.json"]` member before generating
Go bindings and synthetic round-trip cases. The generator owns the exact root set.

These adaptations preserve optional values, raw unknown fields/variants and number
literals; they are not a strict JSON Schema validator or an official OpenAI Go SDK.
Native minimum/maximum and additionalProperties constraints are not all enforced.
Annotation-only nodes such as outputSchema remain arbitrary JSON; CodexErrorInfo
retains the official string/object fallback. Representability is not server acceptance.

The archive, extracted-aggregate checksum, paths, revision, Git blob IDs, and
SHA-256 digests are in [sources.json](sources.json). SHA-256 values describe exact
source bytes, without whitespace normalization. The stable schema is retained as
an inspected source, not the source of the final generated bindings.

## Server request resolution

The pinned [request registry](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/common.rs#L1780-L1850)
declares the four modern request methods `item/commandExecution/requestApproval`,
`item/fileChange/requestApproval`, `item/permissions/requestApproval`, and
`item/tool/requestUserInput`, plus deprecated `applyPatchApproval` and
`execCommandApproval`. Model availability does not imply a feature is enabled
by a server or that legacy methods are emitted by its current execution paths.

The [resolved notification definition](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/v2/notification.rs#L71-L77)
contains only `threadId` and `requestId`. The [emitter](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/thread_lifecycle.rs#L887-L911)
sends the original native request ID to connections subscribed to that thread.
The [response handlers](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/bespoke_event_handling.rs#L1637-L1689)
emit resolution after their response wait finishes, including an error or an
interrupted-turn transition, before applying the result. Thus `serverRequest/resolved`
means that a pending server request has ceased to require an answer; it does not
prove that the client approved it, that a command succeeded, or that a result was
accepted. It has no native turn ID or resolution-reason member.

Legacy approval parameters use `conversationId: ThreadId` in the
[v1 definitions](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/v1.rs#L141-L181).
That field can provide thread identity for matching a received resolution, but
source inspection did not find a current production sender/response handler for
the legacy approval wire methods. Legacy resolution emission is not verified.

The complete pinned method registries contain no generic request cancellation
method or cancellation notification. `turn/interrupt` is an explicit native
turn action, while approval decisions such as `cancel` are response values.
Neither should be invented or sent as a side effect of canceling a local wait.

## Remote transport and authentication

The exact pinned [WebSocket writer](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-transport/src/transport/websocket.rs#L290-L327)
serializes each outgoing JSON envelope into one complete text message. Its
[reader](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-transport/src/transport/websocket.rs#L331-L389)
forwards a complete text message for JSON decoding, replies to Ping with the same
Pong payload, accepts Pong, and exits on Close. Binary messages are unsupported:
the server drops them, while this SDK terminates its connection with `ErrProtocol`
rather than silently losing unexpected application data. The official
[test client](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/connection_handling_websocket.rs#L907-L928)
also rejects incoming binary. Newline-delimited stdio framing is not reused for WS.

An independently authored streaming guard enforces the server masking and
canonical length rules from [RFC 6455 sections 5.1–5.2](https://www.rfc-editor.org/rfc/rfc6455#section-5.1)
before the transport library decodes messages. It rejects masked server frames,
nonminimal extended lengths, and 64-bit lengths with their high bit set, including
otherwise-valid JSON payloads. The dependency still owns fragmentation, control
frames, and message limits. No RFC code or third-party implementation was copied.

The [upgrade handler](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-transport/src/transport/websocket.rs#L89-L125)
rejects browser Origin headers and invokes the pinned
[bearer policy](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/websocket-auth/src/lib.rs#L286-L318).
Capability-token and signed-bearer modes are server policy; this client forwards
only the caller's explicit token in the Authorization header. It does not issue,
refresh, validate claims for, or discover credentials. The native source checks
authentication during upgrade, without promising post-upgrade expiry/revocation.

Verified WSS, URL restrictions, no redirects or ambient HTTP proxy, and the
non-loopback plaintext opt-in are SDK safety choices. They do not certify a live
TLS terminator, account, token issuer, or server deployment. Each explicit new
connection repeats initialization; no disconnect path reconnects or replays RPCs.

## Licensing

Original SDK code is licensed under Apache-2.0. Derived official protocol names,
schema material, generated bindings, and the method inventory come from OpenAI
Codex under Apache-2.0. The native [LICENSE](../../LICENSE) contains that license,
and [NOTICE](../../NOTICE) preserves the upstream notice. No competing Go
SDK implementation or test source is incorporated. Official tests were read;
the Go tests are independently authored.

The WebSocket transport dependency is
[`github.com/coder/websocket` v1.8.15](https://github.com/coder/websocket/releases/tag/v1.8.15),
source commit `9c8faadccd1b679e811a79ce506f8a10237251ad`, under ISC. The unmodified
[license notice](third-party/coder-websocket-LICENSE.txt) is included. Exact Go
module checksums, license SHA-256, immutable source links, and CI action pins are
recorded in [dependencies.json](dependencies.json); module checksums also remain
in root `go.sum`. No transport-library test/source is copied into SDK code. This
dependency provides WebSocket mechanics, not Codex models or a competing SDK.

## Rechecking this record

Run the offline consistency check from the native `codex/` directory:

```sh
python3 docs/protocol/verify_inventory.py
```

To verify every inspected upstream file and registry against an official checkout
at the exact revision, pass its checkout directory:

```sh
python3 docs/protocol/verify_inventory.py path/to/official-codex-checkout
```

The script performs no network calls and launches no Codex process. It checks
source SHA-256 and Git blob IDs, matrix completeness against the Rust registries,
source-line locations, schema memberships, and evidence references. It is an
inventory check, not a live protocol test. Public evidence records must name a
committed SDK revision when available; a pre-commit working-tree result must say
so instead of claiming the base commit contains the tested changes.

## Session source evidence

The following source bytes were inspected on 2026-10-25 at the same
`092d3acd6bec3e3a14bdc7e7a2810ab628ab759d` pin. These are source findings,
not new runtime tests; source inspection does not establish configuration effects,
complete child forwarding, recovery or live query coverage.

| 编号 | 官方拥有源（同一 commit） | SHA256 与结论 |
|---|---|---|
| C1 | [v2/thread.rs](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/v2/thread.rs) | `553d99d01eed0c7a9073f481938f7dd68ef633879be0e1ffd8373778c937b158`；236–332 settings/model/effort/notification，1394–1465 ancestor/source/archive/page，1551 response cursor。 |
| C2 | [v2/turn.rs](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/v2/turn.rs) | `eb1a99cc72a65af84f933073881bfc0440288fb629de7ec92b7c1b50d3530779`；turn/start 与带 expectedTurnId 的 steer 是不同请求。 |
| C3 | [turn_processor.rs](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/turn_processor.rs#L931) | `50bc2dd50ba86c569a81abbe527000735ae90280dcaf8502e9e8c1641864f7b7`；settings 提交 Op::ThreadSettings 后空响应；不能证明当前执行已切换。 |
| C4 | [thread_processor.rs](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/thread_processor.rs#L2524) | `2c40a6db590c9823a6775b28c2e125544583246318ce038166d28fb57a10ab29`；DirectChildrenOf/DescendantsOf、互斥过滤和存储错误；3607 起 best-effort auto-attach 不是完整观察证明。 |
| C5 | [thread_lifecycle.rs](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/thread_lifecycle.rs) | `967cbda6493c971232d5434af690e0c86a318589492d3c14344ee249a7267457`；resume 返回 thread，warm resume 可重发 pending requests，不能作为无副作用自动订阅。 |

For remote connection loss, the fixed
[unload state machine](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/thread_lifecycle.rs#L20-L104)
requires both no listeners and an inactive thread; disconnect alone does not
stop an active turn. Stdio uses the separate
[single-client shutdown path](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/lib.rs#L1163-L1187).
[Warm resume](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/thread_lifecycle.rs#L714-L836)
reattaches and can resend pending native requests; it is not a notification-log
replay and does not restore an old Go answer handle's authority. The SDK reports
connection loss without automatic reconnect or resend.

## Test migration audit

[test-migrations.json](test-migrations.json) retains the historical base, each
retired test path, exact historical evidence IDs and current replacement files.
The inventory verifier rejects unknown missing paths, unmapped/duplicate evidence
IDs and missing replacement targets. These mappings preserve traceability; they
do not transfer historical passes to new code. Current Session checks preserve
native arrival order, original request authority and drain semantics. Former tree
consumers, ancestry-first ordering, UUID suppression and inferred run/creation
state are superseded by the [Session contract](../../../harness/modules/native-session/design.md).
Historical per-test narrative audits remain in Git history.

## Inspected test sources

Official Rust app-server, TypeScript exec and Python SDK tests informed the choice
of public protocol seams and isolated peers. Exact sources/hashes remain in
[sources.json](sources.json); Go tests are independently authored. Those distinct
SDK surfaces and inspected tests are not additional Go runtime coverage.

## Descendant evidence

Current descendant observation has source/synthetic evidence only; no E3 actual
provider/mock-backend or E4 real-model pass is claimed. Auto-attach is best-effort.
Thread/item identity is native; SDK membership/coverage uses a separate Received
kind. thread/started, thread/status/changed, reasoning deltas and token usage stay
raw-only notifications; native Thread/item values use generated types. Raw support
does not establish typed control APIs. No ACP wire is implemented.

- **A**: [new-thread attach / lag without resync](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/lib.rs#L1291-L1319)
  and [best-effort per-connection listener](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/thread_processor.rs#L3607-L3639).
- **C/I**: [collab tool and native item definitions](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/v2/item.rs).
- **V**: [V2 successful spawn / Started](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/tools/handlers/multi_agents_v2/spawn.rs#L185-L239),
  [canonical activity pair](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/tools/handlers/multi_agents_v2.rs#L45-L54),
  and [official completed Started child-ID test, no collab](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/turn_start.rs#L4493-L4593).
- **T**: [native Thread definitions](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/v2/thread_data.rs#L204-L271).
- **N/R**: [notification / reverse-request registry](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/common.rs).
- **L**: [TurnComplete handling](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/bespoke_event_handling.rs#L186-L203)
  and [per-thread idle publication](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/thread_status.rs#L148-L158).
