---
type: Agent Instructions
title: 文档维护规则
status: stable
description: "规定 SDK 文档的归属、修改权限、源码引用和 OKF 维护方式。"
tags: [documentation, maintenance, okf]
sources:
  - id: repo-guide
    resource: "../AGENTS.md"
    title: "仓库 Agent 指南"
---

# 文档维护规则

开始工作前读取 [入口](index.md)，按任务读取对应规范与合同。

## 维护规则

- 设计与规范修改须有人类明确要求或事先同意；授权包含必需的引用同步。
- 仓库级开发规范、共同验收和跨领域决策归 `docs/`。原生合同、统一合同行为和工具合同随所属领域维护。术语维护在根 [GLOSSARY.md](../GLOSSARY.md)。使用命令、来源与证据随对应 README 或来源文件维护。
- 使用指南和合同描述当前行为，引用当前维护的文档。架构决策和取舍归 [ADR](adr/)。每项规则只维护一处。
- 公开 Go 声明、默认值和验收矩阵链接到所属源码。正文记录调用顺序、资源归属、设计约束与验收条件。实现历史由 Git 保存，验证结果以检查产物为准。
- `docs/` 按 [OKF v0.3](https://github.com/open-knowledge-format/open-knowledge-format/blob/main/SPEC.md) 组织。维护根索引时保留 YAML 元数据，并为概念页填写 `type` 和 `title`。
- `status` 用于文档生命周期，取值为 `draft`、`stable` 或 `deprecated`。ADR 用 `x-agent-sdk-go-decision-status` 和 `x-agent-sdk-go-decision-date` 记录决策状态与日期。
- 使用相对链接。链接到源码或仓库级文档时，保留从当前文件出发的路径。
