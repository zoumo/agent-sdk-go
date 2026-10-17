# Pinned protocol provenance

The [canonical compatibility record](../compatibility.md) distinguishes
implementation from test evidence. Its complete machine-readable matrix is
[capabilities.json](capabilities.json). All source records in
[sources.json](sources.json) point to the same immutable official Codex revision,
`d27764b82f7118f674371e6d6e76271d9d606edb` (workspace version `0.160.1`).

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
   test-design patterns only. See [testing-patterns.md](testing-patterns.md).

The inventory is the source-registry union, not just the smaller stable schema.
`in_stable_schema` and `in_experimental_schema` expose each entry's presence.
`experimental_gate` records explicit variant-level gating; `field_experimental_gates`
indicates parameter inspection and is not an exhaustive list of gated fields.
Follow the referenced native definition for individual field semantics. A schema
membership flag is not an SDK support or server-runtime availability guarantee.

## Generated material

`protocol/schema.json` is the unmodified definition closure of
`InitializeParams`, `InitializeResponse`, `v2/ThreadStartParams`,
`v2/ThreadStartResponse`, `v2/ThreadReadParams`, `v2/ThreadReadResponse`, the
`v2/ThreadResume` and `v2/ThreadUnsubscribe` parameter/response pairs, the
`v2/TurnStart` and `v2/TurnInterrupt` parameter/response pairs, and six notification
roots: `v2/TurnStartedNotification`, `v2/TurnCompletedNotification`,
`v2/ItemStartedNotification`, `v2/ItemCompletedNotification`,
`v2/AgentMessageDeltaNotification`, and `v2/ErrorNotification`. Ticket 04 adds
the parameter/response roots for `CommandExecutionRequestApproval`,
`FileChangeRequestApproval`, `PermissionsRequestApproval`,
`ToolRequestUserInput`, `ApplyPatchApproval`, and `ExecCommandApproval`
(all top-level schema definitions), plus
`v2/ServerRequestResolvedNotification`. These 33 roots are extracted
from the official experimental archive's
`json_schema["codex_app_server_protocol.schemas.json"]` member.
`protocol/types_generated.go` is independently generated from that snapshot by
`scripts/generate_protocol.py`. The script verifies the upstream archive's exact
Git blob before refreshing the snapshot. The snapshot and Go code are adaptations;
they are not an upstream OpenAI Go SDK. The Go representation adds explicit
optional values and raw unknown-field/variant preservation. Annotation-only schema
nodes, such as `outputSchema`, remain arbitrary JSON instead of an invented
JSON-Schema model. The current closure has 154 definitions, generating 397 Go
types and 744 synthetic model round-trip cases. Mixed string/object approval
decisions retain their native wire representations; permission-path variants
retain their native `kind` discriminator. `RequestID` uses distinct string and
signed 64-bit integer variants without passing numbers through floating point.

The archive, extracted-aggregate checksum, paths, revision, Git blob IDs, and
SHA-256 digests are in [sources.json](sources.json). SHA-256 values describe exact
source bytes, without whitespace normalization. The stable schema is retained as
an inspected source, not the source of the final generated bindings.

## Server request resolution

The pinned [request registry](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/codex-rs/app-server-protocol/src/protocol/common.rs#L1761-L1831)
declares the four modern request methods `item/commandExecution/requestApproval`,
`item/fileChange/requestApproval`, `item/permissions/requestApproval`, and
`item/tool/requestUserInput`, plus deprecated `applyPatchApproval` and
`execCommandApproval`. Model availability does not imply a feature is enabled
by a server or that legacy methods are emitted by its current execution paths.

The [resolved notification definition](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/codex-rs/app-server-protocol/src/protocol/v2/notification.rs#L71-L77)
contains only `threadId` and `requestId`. The [emitter](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/codex-rs/app-server/src/request_processors/thread_lifecycle.rs#L864-L888)
sends the original native request ID to connections subscribed to that thread.
The [response handlers](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/codex-rs/app-server/src/bespoke_event_handling.rs#L1632-L1684)
emit resolution after their response wait finishes, including an error or an
interrupted-turn transition, before applying the result. Thus `serverRequest/resolved`
means that a pending server request has ceased to require an answer; it does not
prove that the client approved it, that a command succeeded, or that a result was
accepted. It has no native turn ID or resolution-reason member.

Legacy approval parameters use `conversationId: ThreadId` in the
[v1 definitions](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/codex-rs/app-server-protocol/src/protocol/v1.rs#L141-L181).
That field can provide thread identity for matching a received resolution, but
source inspection did not find a current production sender/response handler for
the legacy approval wire methods. Legacy resolution emission is not verified.

The complete pinned method registries contain no generic request cancellation
method or cancellation notification. `turn/interrupt` is an explicit native
turn action, while approval decisions such as `cancel` are response values.
Neither should be invented or sent as a side effect of canceling a local wait.

## Licensing

Original SDK code is licensed under Apache-2.0. Derived official protocol names,
schema material, generated bindings, and the method inventory come from OpenAI
Codex under Apache-2.0. The root [LICENSE](../../LICENSE) contains that license,
and root [NOTICE](../../NOTICE) preserves the upstream notice. No competing Go
SDK implementation or test source is incorporated. Official tests were read;
the Go tests are independently authored.

## Rechecking this record

Run the offline consistency check from the SDK repository root:

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
