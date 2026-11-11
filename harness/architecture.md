---
type: Architecture
title: SDK 架构与依赖边界
status: stable
---

# Architecture standards

These rules describe the agreed repository boundaries. For document ownership and
navigation, see the [harness index](index.md).
The adapter-independent behavior contract and onboarding requirements live in
[SDK behavior contract](sdk/architecture.md); this page governs repository module
and dependency boundaries.

- Use one module, `github.com/zoumo/agent-sdk-go`, and one version. Keep a single
  root `go.mod`/`go.sum`; do not introduce nested modules or `go.work`.
- Keep the supported native SDK imports `codex`, `codex/types`, `claude` and
  `claude/types` independent. The optional `agent` package is an adapter-neutral
  upper-layer contract, not a root client or a replacement for the native APIs.
  Do not add old-path forwarding packages or a shared native answer state machine.
  Common approval operations follow the [SDK behavior contract](sdk/architecture.md),
  preserving original native answer handles and native-specific options.
- The `agent` core production code must use only the standard library and the
  SDK-internal carrier validator described below. It must not
  import either native SDK, MASS or recording/replay code. Adapters may
  import the corresponding native SDK, `agent`, and reviewed SDK-internal
  mechanics (`agent/internal/corelease`, `agent/internal/eventstream`,
  `agent/internal/eventenvelope`, `agent/internal/interactionledger`, `agent/internal/submissiontrace`,
  `internal/queue` and `internal/uuidv4`), but adapters must not import each
  other or the other native SDK, and native packages must not import `agent`.
- `internal/tracecontext` shares only standard-library W3C carrier validation
  between `agent`, `claude` and `codex`; public types and context keys remain
  package-owned and independent.
- Trace values, context helpers and the `agent.Tracer` seam are part of the
  standard-library-only core. OpenTelemetry or another telemetry SDK can
  implement `Tracer` and adapt `TraceContext` outside this repository, but must
  not become a production dependency of `agent`, `codex`, `claude` or either
  native `types` package.
- `codex` and `claude` must not import each other. Native SDK production code
  must not depend on recording/replay tools or fixture containers.
- Both native `types` packages must have standard-library-only **production**
  dependencies. Their tests may use testify. Dependency gates must distinguish
  production dependencies from test dependencies, and remain enabled.
- Shared runtime code must not import either native SDK or native `types`.
  `internal/lineage` accepts confirmed relationship facts; it does not parse
  native events, infer creation, send protocol messages or grant answer authority.
  Its precise contract and resource bounds live in [lineage](modules/native-session/design.md#lineage-shared-relationship-rules).
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
- `agent/internal/submissiontrace` owns only the best-effort local span lifecycle
  for one admitted unified submission. It may import the standard library and
  public `agent` contract; it must not import a native SDK, adapter, MASS or
  tracing implementation. Adapter-specific carrier conversion and wire mapping
  remain with each adapter.
- `internal/queue` shares only concurrent FIFO mechanics. It imports no native
  SDK, native types, tools or fixtures. Its only non-standard production dependency
  is the pinned `github.com/zoumo/golib/channx.ChannX` implementation; other golib
  packages must not enter SDK production dependency trees. Native types remain
  standard-library-only.
  FIFO ownership and resource semantics live in [event delivery](modules/native-session/design.md#delivery-event-delivery).
- `agent/internal/eventenvelope` shares only unified event-envelope construction:
  identity, fallback time, validation and error classification. It may depend on
  the standard library, `agent` and `internal/uuidv4`; adapter-specific payload
  conversion and Meta/raw extraction stay in each adapter.
- Native parsing, unknown data, aliases, execution identities, transport and
  process lifecycle stay with each native SDK. Session state ownership and
  internal coordination live in the [Session module design](modules/native-session/design.md).
- Answers require their original native request/permission handle. Relationship
  records carry context only. Do not restore `TreeRecord`/`TreeEvent` APIs.

Testing and source placement follow the [testing standards](testing.md) and
[code layout](#layout-code-layout-standards).

<a id="layout-code-layout-standards"></a>

## Code layout standards

Both native SDKs use the same separation of responsibilities:

| Location | Responsibility |
|---|---|
| `codex/`, `claude/` | Independent native SDK runtime and native types |
| `agent/` | Adapter-neutral Agent Core and live Session contracts, values, validation and event encoding |
| `tests/simulation/simulate/` | SDK-owned simulate, coverage, independent policy hosts, cancellation and authenticated loopback bridge |
| `tests/simulation/drivers/` | Go simulation process entry and driver documentation |
| `tests/simulation/drivers/codex/go/`, `tests/simulation/drivers/claude/go/` | Agent-owned Go SDK simulation flows, history, child and remote assertions and regression tests |
| `codex/tests/replay`, `claude/tests/replay` | Synthetic fixture decoding and in-process stream adapters with SDK integration tests; SDK exercise helpers remain test-only; protocol matching belongs to the pinned testify library |
| `internal/mocktest` | Shared test-only bridge to the pinned testify library, without native matching or SDK imports |
| `codex/tests/testdata/integration`, `claude/tests/testdata/integration` | Explicitly synthetic protocol inputs owned by code integration tests |
| `codex/tests/e2e`, `claude/tests/e2e` | Explicitly enabled real CLI/agent checks; scope and evidence are documented separately from offline tests |
| Each native `examples` | Genuine SDK usage and offline usage demonstrations |
| Each native `*_test.go` | Public API checks and SDK integration against in-process mock streams |
| `agent/*_test.go` | Public contract compile, validation, JSON and dependency checks |
| `tests/integration` | Shared code integration suite, native test adapters and synthetic protocol peers |

SDK test and simulation hosts link the exact `agent-sdk-testify` Go module. Tests
prepare a caller-owned fixture, start every declared connection with directional
in-process streams, and read the typed execution report before removing the
fixture directory. The exact version, checksum and test-only boundary are fixed
by [Go library mode](modules/agent-sdk-testify/library-mode.md).
`internal/mocktest` authors manifest-bound synthetic captures from SDK-owned
fixtures, creates the SDK-side streams and exposes typed evidence; it contains
no protocol matcher and imports neither native SDK. Simulation validates the
prepared schema-5 identity and route, exports the complete material outside the
fixture and validates the exported inventory, plans, native streams, reports and
history bindings. It never traverses the library's private storage.
SDK assertion projections are INTERNAL SDK data derived from the public export;
immutable source bytes, projected agent-mock frames and live runtime paths remain
separate. The linked library owns protocol matching, native preparation, history
materialization and typed reports. Library sources, official drivers, recordings
and scenarios are not copied into this repository. The SDK never installs,
locates, launches or falls back to a separate testify program.

The SDK runner imports the exact pinned module. It does not build another tool,
search `PATH` or fall back to an Agent. Prepared and exported schemas are public
library contracts; they do not grant permission to traverse private library
storage.

Do not add `cmd`, `pkg` or forwarding layers. Module responsibilities and dependency
gates follow the module boundaries above.

Each native `tests/testdata/embed.go` is a small standard-library-only fixture
container because `go:embed` cannot read a parent directory. Embedded paths
resolve within the container, independently of the caller’s working directory.
SDK tests and offline examples reuse fixture access; example-local synthetic data
may remain with its example. The pinned testify library owns protocol matching;
fixture adapters only decode data and connect directional streams. SDK
integration tests assert SDK results;
simulation operation sequences and business assertions live with the provider driver
under `tests/simulation/drivers/<agent>/go/`. Test-local SDK policies belong in `*_test.go`, not
exported runtime helper packages. Pure protocol matching matrices live with the
pinned testify library, without duplicate copies in SDK test adapters.

Move data rather than copying it. Update imports, embeds, scripts, examples, CI
and documentation links whenever paths change. The library's inventories retain
direct asset references and evidence limits, and link back to these layout rules.

<a id="layout-simulation-data-and-integration-fixtures"></a>

### Simulation data and integration fixtures

Simulation outputs are persistent SDK-owned evidence outside immutable recordings.
The pinned testify library owns reviewed material/index maintenance and publishes
the full material set through prepare/export. This repository retains no
recording, scenario or official-driver snapshot and never reads a library
material directory directly.
Source identity, observed versions, raw hashes, sanitization and publication
review follow the [recording data contract](modules/agent-sdk-testify/design.md#story-数据格式与存储).
Preserve original bytes and provenance when moving data; synthetic data must never
be labeled real.

Each native `tests/testdata/integration` contains synthetic protocol inputs for
code integration tests, with semantic names rather than simulation case identities.
Test-only peers and fault controls stay in test code, without a separate
technical-check registry. Story/case scope follows the [case catalog](sdk/validation-cases.md).

`agent-sdk-testify` 的库入口读取显式提供的录制或合成输入；内置场景入口从随版本
发布的合格材料中选择并准备资源。发布资格、环境和下游边界见
[仿真设计](modules/agent-sdk-testify/design.md#内置材料与下游直接使用)。
合成输入或工具专项测试不构成真实录制仿真证据。

## 编码约定

使用清楚的名称和语言惯用写法；注释解释原因，避免重复代码。按实际职责划分包，
沿用现有 SDK 平铺布局，不新增无需求的 cmd/pkg、框架或转发层。
复用须解决实际重复，性能优化须有实际需求。第三方依赖应有明确用途、版本与来源。
遵守已有项目配置，限制 lint 例外并说明原因；lint 不替代编译、测试和 race。
日志与协议 stdout 分离，不记录认证秘密；可复用模块不修改全局日志或遥测配置。
