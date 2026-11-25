---
type: Architecture
title: SDK 架构与依赖边界
status: stable
description: "Defines repository module and dependency boundaries, code layout, and coding conventions for the native and unified SDKs."
tags: [architecture, dependencies, development]
---

# Architecture standards

These rules describe the agreed repository boundaries. For document ownership and
navigation, see the [documentation index](index.md).
The [architecture decision](adr/0007-sdk-replay-package-ownership.md) defines package ownership and provider onboarding. See the [native behavior contract](../client/docs/architecture.md) for SDK behavior.

- Use one module, `github.com/zoumo/agent-sdk-go`, and one version. Keep a single
  root `go.mod`/`go.sum`; do not introduce nested modules or `go.work`.
- Keep the supported native SDK imports under `client/codex`, `client/claude`, and
  `client/pi` independent. The optional `agent` package is an adapter-neutral
  upper-layer contract, not a root client or a replacement for the native APIs.
  Do not add old-path forwarding packages or a shared native answer state machine.
  Common approval operations follow the [SDK behavior contract](../client/docs/architecture.md),
  preserving original native answer handles and native-specific options.
- The `agent` core production code must use only the standard library and the
  SDK-internal carrier validator and shared `mcp` contract described below. It must not
  import a native SDK, MASS or recording/replay code. Adapters may
  import the corresponding native SDK, `agent`, and reviewed SDK-internal
  mechanics (`agent/internal/corelease`, `agent/internal/eventstream`,
  `agent/internal/eventenvelope`, `agent/internal/interactionledger`,
  `agent/internal/submissiontrace`, `agent/internal/telemetry`,
  `internal/queue` and `internal/uuidv4`), but adapters must not import each
  other or the other native SDK, and native packages must not import `agent`.
- `internal/tracecontext` shares only standard-library W3C carrier validation
  between `agent`, `client/claude`, and `client/codex`. Each package owns
  its public types and context keys.
- `mcp` shares the external MCP server configuration, structural validation,
  and file value resolution between `agent`, `codex` and `claude`. It uses only
  the standard library and reads files through a caller-supplied filesystem.
  Native SDKs own native serialization and provider capability checks. Each
  entry point validates supported transports and fields before native side effects.
- The unified adapters use the OpenTelemetry Go trace and metric APIs.
  `agent/internal/telemetry` owns their shared instruments and result
  attributes. The adapters use `Config.TracerProvider` and
  `Config.MeterProvider` when set. Each unset field uses the corresponding
  global provider. The host owns provider setup, sampling, and export.
  The `agent.sdk.event.published` counter records each event accepted by the
  unified stream with `provider` and `event.type`. An unsupported native event
  also emits `agent.unsupported_native_event` with the provider, Session ID, and
  the available native discriminators. This span uses its own trace.
  The boundary check limits OpenTelemetry API imports to the adapters and
  shared instrumentation. The `go.opentelemetry.io/otel/sdk` packages and
  exporters are test-only.
- Trace values, context helpers, and `agent.Tracer` belong to the `agent`
  contract. The native SDKs and their `types` packages use the existing W3C
  carrier types and remain independent of OpenTelemetry.
- `codex` and `claude` must not import each other. Native SDK production code
  must not depend on recording/replay tools or fixture containers.
- Both native `types` packages must have **production** dependencies limited to
  the standard library and the shared `mcp` package. Their tests may use
  testify. Dependency gates must distinguish production dependencies from test
  dependencies, and remain enabled.
- Shared runtime code must stay independent of native packages.
  `client/internal/lineage` accepts confirmed relationship facts; it does not parse
  native events, infer creation, send protocol messages or grant answer authority.
  Its precise contract and resource bounds live in [lineage](../client/docs/session.md#lineage-shared-relationship-rules).
- `agent/internal/corelease` owns Core admission, opaque shared resources,
  capability snapshots, in-flight opens and Session leases. It may import only
  the standard library and the public `agent` contract.
- `agent/internal/eventstream` owns per-Session event delivery, the one-time
  stream claim, terminal cause, cleanup ordering, lease release and `Done`/`Err`.
  It may import only the standard library, public `agent` and `internal/queue`;
  its transitive non-standard dependencies additionally allow the carrier
  validator through `agent` and the reviewed `github.com/zoumo/golib/channx`
  through `internal/queue`.
  Neither shared module may import a native SDK or native `types`, an adapter,
  interaction state machines (including `interactionledger`), MASS,
  recording/replay code, tools or tests. Native receive, conversion, answer
  authority and interaction settlement remain adapter-owned. Go internal
  visibility restricts both modules to packages under `agent`.
- `agent/internal/interactionledger` owns only Session-scoped opaque interaction
  references, registration order and adapter-neutral authority slots. A slot
  may record pending, answering, sent or settled authority and settlement
  results, but never a native request, raw frame, request identifier, handle,
  reply callback or reference-to-adapter lookup. Each adapter's private registry
  remains the only route from a reference to native answer authority.
- `agent/internal/submissiontrace` runs the legacy `agent.Tracer` callback after
  local submission admission. The unified adapter uses that callback for
  `StartOrSteer` when `Config.Tracer` is set and still records the operation
  metric. The standard submission span uses a valid explicit `agent.TraceContext`
  as its parent when available, or inherits the active context span. Both paths
  send the carrier through the adapter's existing native protocol.
- `internal/queue` shares only concurrent FIFO mechanics. It imports no native
  SDK, native types, tools or fixtures. Its only non-standard production dependency
  is the pinned `github.com/zoumo/golib/channx.ChannX` implementation; other golib
  packages must not enter SDK production dependency trees. Native types may add
  only the shared `mcp` contract to the standard library.
  FIFO ownership and resource semantics live in [event delivery](../client/docs/session.md#delivery-event-delivery).
- `agent/internal/eventenvelope` shares only unified event-envelope construction:
  identity, fallback time, validation and error classification. It may depend on
  the standard library, `agent` and `internal/uuidv4`; adapter-specific payload
  conversion and Meta/raw extraction stay in each adapter.
- Native parsing, unknown data, aliases, execution identities, transport and
  process lifecycle stay with each native SDK. Session state ownership and
  internal coordination live in the [Session module design](../client/docs/session.md).
- Answers require their original native request/permission handle. Relationship
  records carry context only. Do not restore `TreeRecord`/`TreeEvent` APIs.

Testing and source placement follow the [testing standards](testing-standards.md) and
[code layout](#layout-code-layout-standards).

<a id="layout-code-layout-standards"></a>

## Code layout standards

See the [package ownership decision](adr/0007-sdk-replay-package-ownership.md)
for dependency direction, private visibility, and test placement. Move assets
with their owning packages. Update imports, embeds, scripts, examples, CI, and
documentation links when paths change.

Test hosts prepare caller-owned fixtures and open SDK-side streams through
`internal/testsupport/mocktest`. Check typed execution results before removing
the fixture. The SDK runner projects assertion inputs from exported material
and retains the source and projection hashes. Replay owns protocol
matching, fixture preparation, saved history materialization, and typed reports.
[Simulation inputs and evidence](../tests/simulation/README.md)

<a id="layout-simulation-data-and-integration-fixtures"></a>

### Simulation data and integration fixtures

Simulation outputs are persistent SDK-owned evidence outside immutable recordings.
Replay maintains reviewed materials and the fixed index. SDK simulation
obtains materials through the public prepare and export operations.
Source identity, observed versions, raw hashes, sanitization and publication
review follow the [recording data contract](../tests/simulation/docs/design.md#story-数据格式与存储).
Preserve original bytes and provenance when moving data; synthetic data must never
be labeled real.

The provider directories under `internal/testsupport/` contain synthetic protocol inputs for
code integration tests, with semantic names rather than simulation case identities.
Test-only peers and fault controls stay in test code, without a separate
technical-check registry. Story/case scope follows the [case catalog](sdk/validation-cases.md).

Replay 的库入口读取显式提供的录制或合成输入；内置场景入口从随版本
发布的合格材料中选择并准备资源。发布资格、环境和下游边界见
[仿真设计](../tests/simulation/docs/design.md#内置材料与下游直接使用)。
合成输入或工具专项测试不构成真实录制仿真证据。

## 编码约定

使用清楚的名称和语言惯用写法；注释解释原因，避免重复代码。按实际职责划分包，
包内实现优先使用未导出声明。需要分包时使用所属领域的 `internal/`。
复用须解决实际重复，性能优化须有实际需求。第三方依赖应有明确用途、版本与来源。
遵守已有项目配置，限制 lint 例外并说明原因；lint 不替代编译、测试和 race。
日志与协议 stdout 分离，不记录认证秘密；可复用模块不修改全局日志或遥测配置。
