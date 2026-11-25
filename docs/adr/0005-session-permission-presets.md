---
type: Decision
title: 打开和恢复 Session 时应用权限预设
status: stable
x-agent-sdk-go-decision-status: accepted
x-agent-sdk-go-decision-date: "2026-11-20"
description: "记录统一 Agent 权限预设、默认审批与沙箱映射，以及恢复时的生效检查。"
tags: [session, permissions, approvals, sandbox]
---

# 打开和恢复 Session 时应用权限预设

调用方需要用同一套选项打开和恢复 Codex、Claude Session。审批由谁处理和工具能在哪个范围执行是两个决定。若恢复只改审批人，原生 Session 仍可能保留先前的完全访问权限。因此统一入口须同时应用本次选择的审批和沙箱配置。

## 决策

在 `agent.OpenOptions` 增加顶层 `PermissionPreset`。`ResumeOptions` 通过内嵌的 `OpenOptions` 使用同一字段。权限预设在本次 `Open` 或 `Resume` 时解析，审批请求仍由现有交互接口交付。`Features` 用于能力请求，Claude 原生权限模式继续使用 `PermissionMode`。

| `PermissionPreset` | Codex `thread/start`、`thread/resume` | Claude CLI |
| --- | --- | --- |
| `manualReview` | `approvalPolicy=on-request`，`approvalsReviewer=user`，`sandbox=workspace-write` | `--permission-mode=default`，启用 Bash 沙箱 |
| `autoReview` | `approvalPolicy=on-request`，`approvalsReviewer=auto_review`，`sandbox=workspace-write` | `--permission-mode=auto`，启用同一 Bash 沙箱配置 |
| `fullAccess` | `approvalPolicy=never`，`sandbox=danger-full-access` | `--permission-mode=bypassPermissions`，关闭 Bash 沙箱 |

统一 `Core.Open` 和 `Core.Resume` 在预设为空时使用 [默认权限预设](../../agent/permissions.go)，每次传入对应的原生配置。在 `Resume` 中继续请求 `manualReview` 或 `autoReview` 时，调用方须显式重传。SDK 对 `manualReview` 和 `autoReview` 使用相同沙箱基线。选择这两个审批预设时，Claude 传 `sandbox.enabled=true`、`sandbox.failIfUnavailable=true` 和 `sandbox.allowUnsandboxedCommands=false`。沙箱不可用时由 CLI 按 `failIfUnavailable` 拒绝启动。

预设选择默认审批路径。`autoReview` 保留原生端要求人工确认的交互，调用方继续用 `Session.Answer` 回答。`fullAccess` 放开预设控制的内置限制，执行范围仍受宿主隔离、管理策略和原生工具规则约束。

## 恢复与原生覆盖

- 从 `fullAccess` 恢复到任一审批预设时，同时发送新的审批和沙箱配置。Codex 在交付 Session 前校验原生响应的沙箱、审批策略和 reviewer。若已加载的 thread 仍返回 `danger-full-access`，或原生响应与本次配置不符或缺少所需证据，恢复返回错误并释放本次取得的资源。
- Claude 在新进程中应用本次 CLI 参数。Adapter 根据 `initialize` 响应的 `current_permission_mode` 核对启动模式，再通过 `get_settings` 核对有效沙箱配置。`auto` 启动回落、CLI 未报告可识别模式或明确因沙箱不可用退出时，Adapter 返回 `unsupported`。其余模式或沙箱配置不一致时返回 `native_rejected`。启动检查确认 CLI 报告的配置。Bash 执行依赖 CLI 建立的系统沙箱，运行中的模式变化由原生状态事件报告。
- 原生 SDK 入口保留 provider 的默认和覆盖规则。具体 Adapter 的 `OpenWithPermissions` 和 `ResumeWithPermissions` 在预设为空时接收原生配置，在选择审批预设时允许调用方覆盖沙箱。Claude 只覆盖显式传入的沙箱键，其余键使用预设基线。`fullAccess` 生成完整配置，与额外原生权限字段同时传入时报 `invalid_argument`。

## 取舍

由 SDK 显式设置两个审批预设的沙箱，调用方可以在恢复时选择可检查的权限基线。代价是统一入口的零值会主动设置审批和沙箱。需要沿用 provider 配置的调用方可以使用原生入口，或调用具体 Adapter 的原生权限方法。

统一预设定义启动和恢复配置的意图。Codex 沙箱和 Claude Bash 沙箱的覆盖范围由各自原生实现决定。未来 Adapter 只有能兑现对应预设时才声明支持。Pi 的人工和自动审批需要执行前拦截能力，Pi 的进程隔离由宿主配置。

[预设定义](../../agent/permissions.go) · [Codex 映射](../../agent/codex/permissions.go) · [Claude 映射](../../agent/claude/permissions.go) · [统一 Agent 设计](../modules/unified-agent/design.md) · [原生 Session 合同](../modules/native-session/design.md)
