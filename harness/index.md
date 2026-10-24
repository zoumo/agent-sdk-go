---
okf_version: "0.2"
---

# Harness

本目录是架构、设计、术语、合同和共同验收的规范入口。内部文档只依赖本目录的文件；实现说明、来源、验证结果、检查报告和操作材料从外部反向引用规范。

## 工作规则与工程规范

- [工作与文档管理规则](AGENTS.md)
- [工作原则索引](principles/index.md)
- [项目架构总览](overview.md)
- [架构与依赖边界](architecture.md)
- [代码布局](code-layout.md)
- [项目测试规则](testing.md)

## SDK 通用设计

- [SDK 接入规范入口](sdk/index.md)
- [通用架构](sdk/architecture.md)
- [共同用户故事](sdk/user-stories.md)
- [新 Agent 接入流程](sdk/onboarding.md)
- [共享术语](glossary.md)
- [设计决定](adr/index.md)

## 具体合同与验收场景

- [具体合同索引](contracts/index.md)
- [已确认关系与资源合同](contracts/lineage.md)
- [事件交付](contracts/event-delivery.md)
- [异步历史加载](contracts/async-history.md)
- [原生历史事件](contracts/history-events.md)
- [共同验收场景](contracts/stories/index.md)
- [Session 深模块设计](modules/native-session/design.md)
- [Session 原生公开 Interface](modules/native-session/interfaces.md)
