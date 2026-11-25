---
type: Development Standard
title: SDK 测试规则
status: stable
description: "Defines test layers, isolation requirements, and the evidence that Go tests must provide for SDK acceptance."
tags: [testing, acceptance, development]
sources:
  - id: acceptance-guide
    resource: "sdk/validation-cases.md"
    title: "SDK acceptance guide"
---

# Testing standards

Apply these rules to Go tests and generated Go tests. Execution commands and
validation results are maintained separately and must reference these rules.
Map each acceptance target to native evidence and public SDK checks. Report
the outcomes from the corresponding test runs. [SDK acceptance guide](sdk/validation-cases.md)

## 测试层级与自动化边界

术语定义见[共享术语](../GLOSSARY.md)。本仓库采用三个测试层级，
集成层分为仿真与专项集成两类执行方式：

| 层级 | 验证范围 | 依赖与数据 | 自动化与 CI 要求 |
|---|---|---|---|
| 单元测试 | 函数或独立模块的行为合同 | 局部输入、受控替身 | 必需 |
| 集成测试：专项集成 | SDK、协议、进程及工具之间的协作与异常处理 | 受控进程、临时存储、人工构造的协议与故障 | 必需 |
| 集成测试：仿真 | 真实用户流程在当前 SDK 中的回归行为 | 来自真实 Agent 的录制、进程内 Agent mock | 必需 |
| 真实 E2E | 被测 SDK 对接真实 Agent 后达成用户目标 | 真实 CLI、模型、认证和执行环境 | 按需显式执行，不是必需门禁 |

常规自动化与 CI 的必需覆盖止于集成层，包含专项集成和仿真。
真实 E2E 不作为 PR 合并、常规发布或离线验收的必需条件；
测试脚本可以支持真实 E2E，但须由操作者显式启用，CI 不执行。
CI 可下载工具链和依赖；测试执行不依赖真实 Agent、模型服务或其认证。
静态检查、生成一致性、race、vet 和独立消费者检查是补充工程门禁，
不构成第四层，也不因真实 E2E 可选而取消。

用户故事及其验证 case 只表达真实可执行、可录制的使用流程。
仿真与真实 E2E 使用同一用户目标、前置条件和验收语义；
人工故障、极限边界和竞态由代码自己的单元或专项集成测试覆盖，
不新增专项检查目录体系，也不把合成回执登记为真实 case 的录制证据。

仿真须经被测 SDK 的公共接口执行流程，同时检查业务预期、协议匹配、
录制完整消费和 fixture 清理。单独将录制中的客户端帧送入 mock，只证明协议自回放，
不能替代 SDK 仿真验收。录制缺失或目标行为未触发须报告未验证；
必需仿真覆盖缺失时不能用跳过或自动连接真实 Agent 使门禁通过。固定索引及采用规则见
[审核采用与门禁合同](../tests/simulation/docs/design.md#审核采用与固定回归门禁)。
固定门禁由 Go 测试进程直接执行，测试构建当前 driver 并保留其进程隔离；
统一离线入口必须运行该严格测试。缺材料也保留独立 UT、专项集成、race、
vet、生成、依赖与打包检查，CI 保存覆盖及失败诊断。成功、失败和可处理取消后原样
归档短物理运行目录；归档失败必须报错并保留现场。显式输出路径由调用方拥有。

真实 E2E 须区分流程断言、采集完整性和清理结果。
录制成功不等于目标达成，回放复现失败也不等于用户流程通过。
官方 SDK 生成的录制可作为仿真输入，但不能单独证明本仓库 Go SDK 的真实 E2E。
真实 E2E 未执行不影响离线门禁通过，结果须明确标注未执行，
不能由仿真结果推断当前真实 Agent 兼容性。

## 测试编写规则

- Use `github.com/stretchr/testify/assert` and `require` for assertions. Use
  `require` when later steps depend on the check; use `assert` for independent
  checks that can continue and report additional failures.
- Prefer `testify/suite` for shared setup/teardown, client lifecycle and related
  stateful scenarios. Use named table-driven cases for single-function checks;
  suite methods may contain table-driven subtests. Avoid wrapping a second
  lifecycle/assertion framework inside suite.
- Suite tests run sequentially: do not call `t.Parallel` inside them. Preserve
  concurrent, synctest and timing-sensitive coverage with native `testing`
  tables/subtests where needed. Keep internal worker concurrency and barriers.
- Never call `require`/`FailNow` from a worker goroutine. Collect worker and
  callback outcomes through channels, synchronization or a bounded collector,
  then assert on the owning test goroutine. Preserve cleanup and cancellation.
- Preserve all cases, expected values, error classifications, unknown-field
  checks, backpressure, cancellation and race assertions. Do not lower coverage,
  loosen deadlines or substitute mocks for recorded protocol replay to make a
  migration pass.
- Keep Go `Example` output contracts, `TestMain`/subprocess dispatch, testing
  entry points, helpers, cleanup, environment management and explicit live skips
  native where those APIs are required. Helpers should serve a concrete protocol
  or synchronization need; suite does not implement a fake agent or replay peer.
- Change the generator for generated tests, then regenerate and verify. Preserve
  every schema case and its native type/data; do not hand-edit only the output.
- Keep explicitly enabled real CLI/agent checks under the tested client or adapter
  in its `tests/e2e` directory.
  Retain opt-in gates and default skips. Document initialization/transport-only
  probes accurately; the directory name does not prove full model/task coverage.
- Keep capability, source inspection, offline/synthetic tests and real recording
  evidence separate. A skipped live check is not a live pass. Default tests and
  CI do not run real agents or collect recordings.

Cross-provider native assertions live in `client/tests/`. Cross-adapter assertions
for the unified contract live in `agent/tests/`. Keep provider-specific tests
with the tested client or adapter. Shared synthetic peers and fixture helpers
live in `internal/testsupport/`.

The SDK simulation process accepts a single request with supplied peers. Native
and adapter scenarios own their business assertions. The runner validates
provenance and checks typed reports. Each child-delivery policy and each remote
main or probe run needs its own complete report.

Keep pure protocol matching tests with Replay. Keep SDK integration checks for
original request handles, event delivery, cancellation, and transport cleanup
with the SDK code. Synthetic inputs establish code integration evidence.
