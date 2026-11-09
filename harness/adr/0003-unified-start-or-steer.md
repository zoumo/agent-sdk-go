---
type: Decision
title: 统一输入操作采用 Start or steer
status: accepted
date: 2026-11-09
---

# 统一输入操作采用 Start or steer

## 背景

调用方需要向一个 live Session 交付输入，但不应先读取本地 Busy 状态再选择
“开始新工作”还是“并入当前工作”。Codex 与 Claude 都由原生协议和执行器决定
输入是开始新工作、并入当前工作还是暂时无法接收；SDK 只能在原生事实范围内
报告结果。

同时存在两种容易混淆的替代方案：公开并列的 `Submit` 与 `Steer` 两个操作，
或在 Codex adapter 中显式使用 provider 专属的 `turn/steer` 与
`expectedTurnId`。两者都会把 provider 的调度知识转移到调用方，并制造两套
并发、错误和接受语义。

## 证据

Codex 原生 `turn/start` 返回实际 turn identity；原生 schema 说明
`activeTurnNotSteerable` 错误可能由 `turn/start` 或 `turn/steer` 在当前活动
turn 不能接受同 turn 输入时返回，例如 review 或 compact turn。该原生语义已经
覆盖“可并入则并入、不可并入则明确拒绝”，不需要统一层再暴露 provider 专属的
steer 控制面。

Claude 原生 user input 协议包含 `priority` 与 `shouldQuery`，由原生 CLI 决定
当前或后续调度；原生 `Session.Submit` 的写入成功只证明完整本地写入，不证明
CLI 已接受或建立了特定当前工作。

## 决策

统一 `agent.Session` 提供一个输入操作：

```go
StartOrSteer(ctx context.Context, input Input) (InputReceipt, error)
```

`InputReceipt.Result` 使用四个稳定值：

- `applied_current_work`：原生响应与操作前已观察的当前工作一致；
- `applied_new_work`：原生响应建立了新的工作身份，或此前没有当前工作；
- `applied_best_effort`：本地写入完成，但原生没有证明调度位置；
- `not_applied`：输入明确未应用，`Submission` 为空，输入仍由调用方保留。

具体约束：

- Codex adapter 只调用原生 `turn/start`，不新增统一层 steer 方法，不自动改用
  其他原生调用，也不要求调用方提供 `expectedTurnId`。
- Claude adapter 写入原生 user input，使用 `priority="next"` 和
  `shouldQuery=true`；成功写入只返回 `applied_best_effort`，不伪造当前或下一
  turn 身份，也不填写无法证明的 `NativeAccepted`。adapter 可以在内部保留该
  提交作为交互归属与中断的候选目标，直到原生 idle 清除；该候选不改变回执。
- 本地并发写或尚未取得原生状态证据的未知写不再写原生帧，返回 `not_applied`
  和 nil error；原生明确拒绝也返回 `not_applied`，并保留 `Sent=true`、
  `NativeAccepted=false`。
- 原生写入结果未知时返回 `outcome_unknown`，不自动重试；在 adapter 再次取得
  原生工作状态证据前，后续输入返回 `not_applied`。

## 后果

调用方不需要了解 Session 的内部 Busy 状态，也不需要为两个 provider 选择不同
操作。回执只陈述原生能够证明的事实：Codex 可以区分当前工作、新工作和明确
未应用；Claude 在写入成功后只声称 best effort。调用方在 `not_applied` 时仍
拥有输入并自行决定是否重试，在 `outcome_unknown` 时不得自动重试。

统一层不排队、不等待空闲、不持久化输入，也不在失败后回退到另一种原生调用。
代价是繁忙或不可并入的工作可能直接返回 `not_applied`，调用方需要显式处理；
这比把输入留在 SDK 队列中并伪装成已接受更诚实。该决定不改变原生 Session、
原生协议或 MASS 的输入准入和持久化职责。

## 未采用方案

1. 并列公开 `Submit` 与 `Steer`。调用方要先猜测 Session 内部状态，两个操作
   还会产生不同的并发、未知结果和错误合同。
2. 显式使用 Codex `turn/steer` 和 `expectedTurnId`。调用方必须持有并匹配
   provider 专属 turn 身份；原生 `turn/start` 已按 start-or-steer 语义处理
   输入，额外控制面不增加可证明性。
3. SDK 本地输入队列或等待空闲。队列会把未应用伪装成已接受，输入可能过期；
   两个 provider 的原生调度不同，SDK 不拥有执行调度。
4. SDK 持久化输入或自动重试。未知结果下重试可能重复执行；持久化会把 Agent
   调度变成 SDK 的资源生命周期。
5. 增加 provider 专属结果值或为 Claude 补造原生接受。两者都会破坏统一四态
   事实，并把本地写入冒充原生调度。

## 相关合同

- [统一 Agent 设计](../modules/unified-agent/design.md)
- [原生 Session 设计](../modules/native-session/design.md)
- Codex adapter 包文档：`agent/codex/doc.go`
- Claude adapter 包文档：`agent/claude/doc.go`
