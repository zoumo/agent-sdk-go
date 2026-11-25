---
type: Decision
title: SDK 和 Replay 的包归属
status: stable
x-agent-sdk-go-decision-status: accepted
x-agent-sdk-go-decision-date: "2026-11-26"
description: "规定 SDK、Replay、命令和测试的归属，以及新 provider 的接入顺序。"
tags: [architecture, dependencies, replay]
---

# SDK 和 Replay 的包归属

一个 Go module 和一个版本交付原生客户端、统一接口与 Replay。新增 provider 可以在同一次变更中提交客户端、adapter、官方 driver 和录制验收。通过目录作用域和依赖检查隔离 SDK 运行时与测试工具。

## 公开入口与依赖

| 入口 | 职责 |
| --- | --- |
| `client/<provider>` | 原生协议、进程、会话和历史。原生类型随客户端发布。 |
| `agent` | 统一 Core、Session 和事件合同。 |
| `agent/<provider>` | 依赖统一合同和对应的原生客户端，映射 provider 能力。 |
| `mcp` | 原生客户端和统一接口共用的公开 MCP 配置。 |
| `replay` | 公开 fixture、录制和回放接口。官方 driver 和材料归 Replay。 |
| `cmd/agent-replay` | 版本化安装入口。main 调用 `replay/cli` 执行命令。 |

在包内用未导出声明隐藏实现。多包共享的领域实现放在该领域的 `internal/`。例如，`agent/internal/` 的共享实现服务于统一接口和 adapter，`client/internal/` 的关系维护服务于原生客户端。跨顶层目录的共享实现放在根 `internal/`。SDK 生产包独立于 Replay、测试辅助包和其他 provider 的实现。

## 测试归属

包内测试可以直接使用私有测试辅助代码。各 provider 的仿真和显式 E2E 随被测入口维护。跨 provider 的原生矩阵归 `client/tests/`。跨 adapter 的统一合同验收归 `agent/tests/`。合成 fixture 和 SDK 侧 mock 连接由 `internal/testsupport/` 共享。

`tests/simulation/` 调度场景，检查录制身份、覆盖、严格消费和清理。SDK 仿真和 mock 连接通过公开 `replay` 接口消费材料。Replay 的工具跨包和安装验收归 `tests/replay/`。[SDK 仿真指南](../../tests/simulation/README.md)

## 接入新 provider

1. 固定原生协议及可重现的上游版本。在客户端目录记录来源、协议兼容边界和本地验证入口。[原生 SDK 合同](../../client/docs/architecture.md)
2. 实现客户端，并验证原生事件、进程和会话的生命周期。为 `agent` 实现对应 adapter，只声明有原生依据的能力。[统一行为合同](../../agent/docs/session.md)
3. 将官方 driver、上游来源和许可放在 Replay。定义可录制的用户流程，按 Replay 规则采集并审核材料。
4. 先验证官方 driver 严格回放。再用公开客户端或 adapter 执行 SDK 仿真。分别记录通过的业务断言、完整消费、EOF 和清理证据。[SDK 验收条件](../sdk/validation-cases.md)
5. 运行离线门禁和独立消费者检查。需要验证实际 CLI 或模型时，使用所属包的显式 E2E 入口。[测试指南](../testing.md)
