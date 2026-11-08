---
type: Decision
title: 统一层不定义 Provider 无关的 Plan
status: accepted
date: 2026-11-08
---

# 统一层不定义 Provider 无关的 Plan

## 背景

Codex 与 Claude 都存在计划相关行为，但原生事实的形态不同：

- Claude 的 `TodoWrite`、`TaskCreate` 和 `TaskUpdate` 是工具调用，原始事实位于
  `tool_use` 的名称和输入中。
- Codex 有独立的 `PlanThreadItem`、`item/plan/delta` 和
  `turn/plan/updated`；这些通知不能由 Claude tool call 等价表达。

把 Codex turn plan、Codex plan item 和 Claude `TodoWrite` 归一成同一 payload，
需要 adapter 解释 provider 专属的条目结构、状态和身份，还无法消除两类协议之间
的语义差异。

## 证据

2026-11-08 对本地最近的 100 个 Codex session 做了只读结构化扫描，共解析
107,082 条 rollout JSONL：

- 41 个 session 中出现 96 次 `update_plan` function call，共携带 495 条计划条目；
- 这 96 次调用的结果全部为 `unsupported call: update_plan`，没有一次成功执行；
- 没有持久化的 `PlanThreadItem`，也没有结构化的 `item/plan/delta` 或
  `turn/plan/updated` 事件；
- 原始文本中的相关字符串只出现在 tool input/output、reasoning、diff 和文档内容，
  不是统一事件或原生 Plan 事件记录。

该结果说明 Codex 历史中存在计划意图，但当前没有可依赖的成功 Plan 状态。
rollout 不保证保存全部瞬时 app-server 通知，因此该扫描不能证明 UI 未收到临时
通知，只能证明保存历史中没有统一层可消费的 Plan 事实。

## 决策

统一 `agent` 层不提供 Provider 无关的 Plan 事件、payload 或状态：

- 不定义 `EventAgentPlan`、`PlanPayload`、`PlanEntry` 及对应 JSON 编码和校验；
- Claude 的 `TodoWrite`、`TaskCreate` 和 `TaskUpdate` 只输出普通 tool call，
  保留 tool-use ID、名称和原始输入，不额外生成 Plan 事件或 Plan 专属缺口；
- Codex 的独立 Plan item、`item/plan/delta` 和 `turn/plan/updated` 暂不转换为
  统一事件，也不作为映射缺口输出；
- 如果 provider 原本通过普通工具调用暴露计划行为，adapter 仍按普通 tool call
  和 tool result 保留其名称、原始输入、输出与状态；
- Codex 的原生 Plan 类型和通知，以及 Claude 的通用 tool-use 类型和消息，继续由
  各自原生 SDK 与协议层保留。

## 后果

统一事件流只承载两类 provider 都能保留原始身份的实时事实，不再声称两个协议
具有相同的 Plan 语义。消费者如果需要读取 Plan，必须使用 provider 原生 API，
或根据普通工具调用的名称和原始输入处理。

Codex Plan 通知不进入统一流，因此不会产生跨 provider 的 Plan 状态，也不会形成
仅因当前不消费而产生的 `session.error` 缺口。代价是统一层暂时不展示 Codex 的
独立 Plan UI 事实；后续若出现稳定的真实消费需求，可以基于新的协议证据和事件
合同重新评估。

## 未采用方案

1. 继续用 `PlanPayload` 统一 Codex 和 Claude。两者的身份、状态和权威性不一致，
   需要持续增加转换规则，却无法形成可靠的共同语义。
2. 仅为 Claude `TodoWrite` 生成统一 Plan 事件。该 tool call 已经完整保留原始
   输入，额外派生事件会重复事实并发明 provider 间不存在的等价关系。
3. 把 Codex Plan 通知输出为 `session.error`。决定是暂不消费该类事实，而不是
   解码失败或调用方可以处理的错误。
4. 为统一层新增一个可配置的 Plan 开关。当前没有成功且跨 provider 的 Plan 消费
   场景，配置会增加公开面，却没有定义稳定的行为合同。

## 相关合同

- [统一 Agent 设计](../modules/unified-agent/design.md)
- Codex adapter 包文档：`agent/codex/doc.go`
- Claude adapter 包文档：`agent/claude/doc.go`
