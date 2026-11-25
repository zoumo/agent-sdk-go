---
okf_version: "0.3"
title: Agent SDK Go 文档
description: 面向 Agent SDK Go 使用者和维护者的迁移、验证、架构与设计合同。
license: Apache-2.0
---

# Agent SDK Go 文档

## 使用与维护

* [测试指南](testing.md) - Explains how to run offline checks, fixed-recording simulation, and opt-in live CLI checks, and how to read the results.
* [迁移指南](migration.md) - Explains how to migrate standalone Codex and Claude SDK imports and Session calls to the shared Go module.
* [文档维护规则](AGENTS.md) - 规定 SDK 文档的归属、修改权限、源码引用和 OKF 维护方式。
* [开发规范](architecture.md) - Defines repository module and dependency boundaries, code layout, and coding conventions for the native and unified SDKs.
* [测试规范](testing-standards.md) - Defines test layers, isolation requirements, and the evidence that Go tests must provide for SDK acceptance.

## 架构与合同

* [SDK 架构](../client/docs/architecture.md) - 定义原生 SDK 的共同行为、调用方职责、子 Agent 观察及取消、关闭和恢复边界。
* [共同验收](sdk/validation-cases.md) - 说明 SDK 共同验收的用户目标、回放流程、原生适配边界及其与可执行验收矩阵的关系。
* [Session 合同](../client/docs/session.md) - 定义原生 Session 的调用方职责、资源与状态归属、权限、事件交付和子 Agent 关系。
* [统一 Agent 设计](../agent/docs/session.md) - 在原生 Codex 和 Claude SDK 之上定义可选的统一会话与事件层， SDK 保留原生历史查询但不提供历史抽象，MASS 保有历史导入和持久化语义。
* [历史查询合同](../client/docs/history-query.md) - 定义 Codex 与 Claude 独立查询历史时的发现范围、metadata 及主会话与子会话内容。
* [测试工具设计](modules/agent-sdk-testify/design.md) - Defines how the SDK and Testify share responsibility for fixed-recording simulation, strict replay, evidence, and regression gates.
* [Testify 库接入](modules/agent-sdk-testify/library-mode.md) - Defines connection ownership, call order, and acceptance for Go SDK tests that use Testify for in-process replay.

## 架构决策

* [ADR 0001](adr/0001-claude-mixed-user-content.md) - 记录统一 Agent 接口对 Claude 文本与图片混合输入的支持边界。
* [ADR 0002](adr/0002-no-provider-neutral-plan.md) - 记录将 Plan 类型与通知留在原生 SDK 的决定，以及统一层对计划相关工具调用的处理。
* [ADR 0003](adr/0003-unified-start-or-steer.md) - 记录统一输入采用 Start or steer 的决定及其与原生 Session 的职责边界。
* [ADR 0004](adr/0004-environment-and-trace-scopes.md) - 记录 Core 进程环境、Session shell 环境、Claude Session 进程环境和 prompt trace 的独立作用域。
* [ADR 0005](adr/0005-session-permission-presets.md) - 记录 Session 权限预设、默认权限与沙箱映射，以及恢复时的生效检查。
* [ADR 0006](adr/0006-structured-prompt-content.md) - 记录结构化 Prompt 的内容块、原生输入映射和资源链接的默认行为。
* [ADR 0007](adr/0007-sdk-replay-package-ownership.md) - 规定 SDK、Replay 和测试的归属，以及新 provider 的接入顺序。

术语见[仓库词汇表](../GLOSSARY.md)。
