# 子 Agent 输出与交付：来源及现状

对应规范：[ADR-0004](../../harness/adr/0004-subagent-event-delivery.md)。
核实日期：2026-10-25；本 Go 仓库基线：`bd557d91b98ab4db5e2b968b125fba6d583f791b`。
本记录是固定源码与官方文档核实，不是新真实运行或完整互操作验证。

## Codex：连接输出与 Session 接收

app-server 一个连接可观察多个 thread；新 thread 创建时，在相应监听条件满足时为已初始化连接自动附着 listener，不保证无缺口。原生事件携带各自 threadId，子事件不会自动改成 root 身份。
来源：[新 thread listener](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/lib.rs#L1291)。

当前 Go `publishSessionsLocked` 使用 Session 已确认 members 集合接收，不是只过滤 root。thread/started 的父证据、已完成 spawn 等受支持证据可增加成员；完整父图不是成员前提。关系证据之前到达的未知 child 通知进入隔离，不在以后补投实时流。
来源：[Session 接收与成员确认](https://github.com/zoumo/agent-sdk-go/blob/bd557d91b98ab4db5e2b968b125fba6d583f791b/codex/session_observation.go#L88)。

远端已有 thread 的重新附着需要区分 resume 的恢复与附着副作用；固定协议没有独立纯 `thread/subscribe`。本 ADR 不新增订阅或 resume，也不把历史读取作为 live 附着。
来源：[固定版 resume 定义](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/v2/thread.rs#L340)、[resume 恢复与 listener 附着](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/thread_processor.rs#L3986)。

## Claude：CLI 流、校验及 SDK 主动设置

当前每个 Session 拥有一个 CLI 进程。`accept` 依据已审阅 envelope 和 root session_id 接收事件；同 root 子事件不要求预先确认成员或完整父路径。foreign session 与未确认 envelope 被隔离，不能描述成“CLI 输出全部无条件传递”。control/cancel 另按自有连接处理真实请求权限。
来源：[Session.accept](https://github.com/zoumo/agent-sdk-go/blob/bd557d91b98ab4db5e2b968b125fba6d583f791b/claude/session.go#L183)。

官方 Python SDK 的 `forward_subagent_text` 默认 false，对应 `forwardSubagentText`：省略时只转发子 tool_use/tool_result，true 增加 text/thinking。当前 Go 初始化显式设置 `forwardSubagentText: true`，属于 SDK 主动扩大输出，并非原生默认。
来源：[官方选项](https://github.com/anthropics/claude-agent-sdk-python/blob/0e89b298e771259378a6cd3734e00d323dff5703/src/claude_agent_sdk/types.py#L2563)、[Go initialize](https://github.com/zoumo/agent-sdk-go/blob/bd557d91b98ab4db5e2b968b125fba6d583f791b/claude/client.go#L187)。

是否移除这一强制 true，是后续实现需要明确处理的已知差异；本次保留代码不变。该原生输出设置不等于未来 SDK Receive 过滤开关，也不能据此声称 Go 已保持官方默认。

已确认的 live 路径是父 CLI 流在本地的分流。官方提供 `listSubagents` / `getSubagentMessages`（Python 对应 snake_case）查询子历史，但本 Go 仓库主历史四票的工作不代表子历史支持；该 Phase 2 工作未并入此 ADR。
来源及版本边界见[历史查询核实](history-query-boundaries.md#已验证claude-code-官方-agent-sdk)。

## 结果回父与应用消费分开

Codex 的 wait handler 读取原生子状态并返回工具结果，内部 completion 通知属于原生编排；应用不需要把 Receive 中的详细子事件再交回父 Agent。
来源：[wait 结果与工具返回](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/tools/handlers/multi_agents/wait.rs#L206)、[内部 completion watcher](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/agent/control.rs#L449)。

Claude 同步 Agent 工具可直接返回子结果；后台工具先返回启动状态，报告经后续原生通知/后续 turn 到达。父 Agent 是否等待取决于原生调度与设置，不能承诺后台立即唤醒，也不能把失败、取消都说成成功结果。真实审批如果无人回答，可能阻止完成。
来源：[官方等待/后台语义及错误边界](https://code.claude.com/docs/en/agent-sdk/subagents#choose-whether-claude-waits-for-subagents)（动态文档，核实于上述日期）。

## 验证边界

本次只有 ADR、术语与导航及本来源记录。未运行真实 Agent、读取真实会话、新增录制或验证后台唤醒；未修改 Go、测试、配置、依赖、生成产物或 Phase 2 分支。可选过滤的 API、配置时机和混合 envelope 处理仍待后续设计与实现。
