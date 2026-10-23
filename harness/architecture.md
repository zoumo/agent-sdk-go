---
type: Architecture
title: SDK 架构与依赖边界
status: stable
---

# Architecture standards

These rules describe the agreed repository boundaries. For their context and
navigation, see the [architecture guide](../docs/architecture.md).

- Use one module, `github.com/zoumo/agent-sdk-go`, and one version. Keep a single
  root `go.mod`/`go.sum`; do not introduce nested modules or `go.work`.
- Keep the supported native SDK imports `codex`, `codex/types`, `claude` and
  `claude/types` independent. Do not add a root generic client, old-path forwarding
  packages, a common public question interface or a shared answer state machine.
- `codex` and `claude` must not import each other. Native SDK production code
  must not depend on recording/replay tools or fixture containers.
- Both native `types` packages must have standard-library-only **production**
  dependencies. Their tests may use testify. Dependency gates must distinguish
  production dependencies from test dependencies, and remain enabled.
- Shared runtime code must not import either native SDK or native `types`.
  `internal/lineage` accepts confirmed relationship facts; it does not parse
  native events, infer creation, send protocol messages or grant answer authority.
  Its precise contract and resource bounds live in [lineage](../docs/lineage.md).
- `internal/queue` shares only concurrent FIFO mechanics. It imports no native
  SDK, native types, tools or fixtures. Its only non-standard production dependency
  is the pinned `github.com/zoumo/golib/channx.ChannX` implementation; other golib
  packages must not enter SDK production dependency trees. Native types remain
  standard-library-only. Each Codex subscription and Claude client owns one
  ChannX; there is no process-wide queue, per-agent partition or second growing
  derived-event FIFO. See [event delivery](../docs/event-delivery.md).
- Each observer owns its graph. Native parsing, unknown data, identity aliases,
  run state, deduplication, event caches, transport and process lifecycle stay
  with the native SDK. Preserve explicit observation gaps and native subagent
  association behavior.
- Answers require their original native request/permission handle. Relationship
  records carry context only. Do not restore `TreeRecord`/`TreeEvent` APIs.

The [testing standards](testing.md) govern shared story acceptance. The
[layout standards](code-layout.md) distinguish SDK code from reusable tools,
command entry points and test data. Python validation scripts keep their native
generation, inventory, provenance and sanitization responsibilities.

## 项目技术选型边界

现有 SDK、原生工具和协议合同保持现状。
通用选型不要求迁移已有实现。
新增能力须遵守本页、[代码布局](code-layout.md)和[测试规则](testing.md)。
原生 types 包的生产依赖仍限于标准库。
原生 SDK 不依赖工具或录制代码。

命令入口仍位于各原生 SDK 的 `cmd/` 下。
通用目录模板不要求将入口迁移到模块根目录。
传输、超时、重试和流式行为遵循原生合同。
通用技术选型见 [Go 技术栈](principles/golang/technology-stack.md)。
