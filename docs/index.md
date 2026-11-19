---
okf_version: "0.3"
title: Agent SDK Go 文档
description: 面向 Agent SDK Go 使用者和维护者的迁移、验证、架构与设计合同。
license: Apache-2.0
---

# Agent SDK Go 文档

## 使用与维护

- [测试指南](testing.md) — 运行离线检查与仿真。
- [迁移指南](migration.md) — 更新旧版 SDK 接入。
- [文档维护规则](AGENTS.md) — 更新文档与引用。
- [开发规范](architecture.md) — 查阅模块、依赖和代码布局约束。
- [测试规范](testing-standards.md) — 查阅测试层级、隔离与验收要求。

## 架构与合同

- [SDK 架构](sdk/architecture.md) — 查阅原生 SDK 的共同能力与设计取舍。
- [共同验收](sdk/validation-cases.md) — 查阅用户目标与验收流程。
- [Session 合同](modules/native-session/design.md) — 查阅原生 Session 接口、权限、事件和子 Session 关系。
- [统一 Agent 设计](modules/unified-agent/design.md) — 查阅原生 Codex 和 Claude SDK 之上的可选统一会话与事件层，以及 SDK 与 MASS 的历史处理边界。
- [历史查询合同](contracts/history-query.md) — 查阅独立发现、metadata 与主／子历史。
- [测试工具设计](modules/agent-sdk-testify/design.md) — 查阅测试编排、协议录制与回放及原生 SDK 驱动。
- [Testify 库接入](modules/agent-sdk-testify/library-mode.md) — 在 Go 测试与仿真中接入 Testify 库。

## 架构决策

- [ADR 0001](adr/0001-claude-mixed-user-content.md) — Claude mixed user content 的支持范围。
- [ADR 0002](adr/0002-no-provider-neutral-plan.md) — 统一层的 Plan 边界。
- [ADR 0003](adr/0003-unified-start-or-steer.md) — 统一输入采用 Start or steer。
- [ADR 0004](adr/0004-environment-and-trace-scopes.md) — 环境与 prompt trace 按作用域分离。

术语见[仓库词汇表](../GLOSSARY.md)。
