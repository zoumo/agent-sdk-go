---
okf_version: "0.2"
---

# Harness

只维护开发规范与核心设计合同。设计目标不代表已实现或已验证。

| 阅读目的 | 文档 |
|---|---|
| 文档维护 | [工作规则](AGENTS.md) |
| 模块、依赖、代码布局与编码 | [开发规范](architecture.md) |
| 测试层级、隔离与验收 | [测试规范](testing.md) |
| 共同能力与设计取舍 | [SDK 架构](sdk/architecture.md) |
| 用户目标与 36 条验收流程 | [共同验收](sdk/validation-cases.md) |
| Session 接口、权限、事件、关系与子详细交付 | [Session 合同](modules/native-session/design.md) |
| 官方 SDK 之上的可选统一 Provider、Session 与事件流 | [统一 Agent 设计](modules/unified-agent/design.md) |
| Claude mixed user content 的决定 | [ADR 0001](adr/0001-claude-mixed-user-content.md) |
| Provider 无关 Plan 的决定 | [ADR 0002](adr/0002-no-provider-neutral-plan.md) |
| 独立发现、metadata、主／子历史 | [历史查询合同](contracts/history-query.md) |
| 测试编排、协议录制／回放与原生 SDK 驱动 | [测试工具设计](modules/agent-sdk-testify/design.md) |
| 以 Go 库而非 testify CLI 接入测试与仿真 | [testify 库模式](modules/agent-sdk-testify/library-mode.md) |
| 术语 | [GLOSSARY](../GLOSSARY.md) |
