# Codex app-server 连接断开与 Session 存续调研

日期：2026-10-24。状态：研究证据，不是已采纳的 SDK 合同。

当前 SDK 设计决定见[断线报告与显式 Load](../../harness/adr/0002-disconnect-reporting-and-explicit-load.md)。本文件记录原生行为证据；末尾研究问题不替代当前规范。

## 范围与方法

检查官方 `openai/codex` 固定提交 `092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`（本仓库 Codex 0.162.1 协议基线）。通过 GitHub 官方源码接口读取 owning source 与官方测试；没有运行真实 Agent、模型请求、录制或上游测试。以下“测试覆盖”仅表示已阅读测试代码。

## 结论

### WebSocket 断开不等于 thread 停止

连接关闭时，app-server 移除该连接及其 thread 订阅；不会因为最后一个订阅者离开而直接关闭已有 thread。thread processor 只在 thread 本来已经不存在时清理残留 bookkeeping。[连接处理](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/thread_processor.rs#L3588-L3602)、[移除订阅](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/thread_state.rs#L622-L647)

卸载条件是“没有订阅者”且“thread 非 Active”，两个条件持续达到配置延迟；有 Active turn 时不设置卸载截止时间。thread 活动会刷新非活动计时。因此，在 app-server 及其依赖保持正常的前提下，断开客户端连接不会单独中断运行中的 turn。[卸载状态机](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/thread_lifecycle.rs#L20-L104)

固定提交默认延迟为 **60 秒**，可通过 `thread_unload_delay_secs` 配置。不要将它写成跨版本固定承诺。[默认配置](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/config/mod.rs#L3930-L3938)

官方测试直接覆盖 WebSocket 最后订阅连接断开后 thread 暂时仍 loaded、随后超时卸载；另一个测试覆盖显式取消最后订阅后运行中的 turn 继续，完成后才开始非活动倒计时。两项测试加上共享卸载状态机，支持上述结论；本次没有执行这些测试。[WS 断开测试](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/connection_handling_websocket.rs#L453-L497)、[运行中取消订阅测试](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/thread_unsubscribe.rs#L196-L360)

### stdio 的生命周期不同

运行时把 Stdio 标为 single-client mode。该连接关闭会退出 processor 主循环；正常退出路径会 drain 后台任务并 shutdown threads。WebSocket 某个连接关闭不触发此分支。因此 stdio EOF 与远程 WS 断开不能共享“远端执行已经停止”的解释；即便 stdio 也应分别观察连接关闭、进程退出与清理完成。[模式判断](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/lib.rs#L794-L796)、[关闭分支](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/lib.rs#L1163-L1187)、[退出清理](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/lib.rs#L1322-L1343)

### 重新连接与重新附着不同

新连接有自己的 ConnectionState 和初始化状态。官方 WS 测试在第二个连接执行 initialize。对已运行 thread 执行 `thread/resume` 的 warm-resume 路径会把新 connection 加入订阅，返回 live thread 状态/按参数提供的历史，并重发仍未回答的普通 server requests。[新连接状态](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/lib.rs#L1130-L1162)、[warm resume 附着](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/thread_lifecycle.rs#L714-L836)

`thread/resume` 也可能涉及已卸载 thread 的重新加载，不能把它一概称为“原执行从未中断”。恢复前后是否仍是同一原生活跃执行，应依据响应和原生事实判断。[resume 参数与 live-resume 描述](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/v2/thread.rs#L350-L430)

### 历史分页不等于原始事件流补齐

resume 可返回历史或初始 turns 页面；`thread/read` 可请求 rollout turns/items；`thread/turns/list`、`thread/items/list` 有历史分页游标。这些游标指向历史 turn/item，不是本次检查中可见的“最后收到的通知序号”。不能据此承诺重连后完整重放所有 missed notifications、增量 token、工具进度及原始接收顺序。[协议参数](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/v2/thread.rs#L1687-L1819)

warm resume 明确重放的是部分状态快照和 pending server requests；对普通 thread 通知，空订阅者列表直接返回。本次没有找到具有事件 cursor/ack 的通用通知重放协议。因此“能重新观察”与“事件连续无缺口”需要分开承诺。[空订阅者的通知发送](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/outgoing_message.rs#L194-L205)、[resume 的补发范围](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/thread_lifecycle.rs#L803-L836)

### 审批请求不能全部按断线失效

普通 thread-scoped server requests 保存在进程级 callback map；connection_closed 只删除 incoming request contexts，并处理特殊 user verification。warm resume 会向新连接重发尚待回答的普通 thread requests，保持原请求 ID。官方测试覆盖 command execution approval 与 file change approval 重放并接受回答。[断线处理](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/outgoing_message.rs#L275-L280)、[请求重放](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/outgoing_message.rs#L446-L467)、[请求筛选](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/outgoing_message.rs#L582-L599)、[command approval 测试](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/thread_resume.rs#L5466-L5596)、[file approval 测试](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/thread_resume.rs#L5603-L5777)

user verification 是例外：它有 connection owner，源码明确指出 reconnect 或其他订阅者不能回答；pending replay 排除此类请求。一般审批回答按 callback ID 取出一次，特殊 verification 额外核对 owner 与认证版本。[owner 约束](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/outgoing_message.rs#L350-L403)、[回答校验](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/outgoing_message.rs#L562-L599)

这些是服务端原生请求语义，不自动决定 Go SDK 旧回答句柄是否跨连接继续有效。SDK 应单独设计如何在重连、请求重发、其他客户端已回答、thread 卸载等情况下更新句柄权威性。也不能自动重发结果未知的审批回答。

## 当前在线文档与固定基线的差异

本轮主调研读取的当前官方 [app-server 文档](https://learn.chatgpt.com/docs/app-server) 将最后订阅取消后的卸载阈值描述为 30 分钟；固定提交源码默认是 60 秒。本文件针对固定提交得出结论，在线文档用于提醒存在版本漂移，不能混为同一兼容性证据。

## 待讨论的 SDK 合同

- Session 的逻辑身份、当前连接状态、原生执行状态是否分别表达？远程断线时不能仅凭连接状态声称执行已结束。
- 是否允许同一进程内显式 reconnect/reattach，还是提供可配置的自动重连？两者都不能默认重发非幂等输入。
- 重连成功应至少区分连接初始化完成、thread 附着完成、可用状态/历史获取完成，以及不能补齐的事件区间。
- 断线期间审批如何标记为暂不可回答；重发请求如何关联、恢复或替换回答句柄？
- 根 thread 重新附着后，已知子 Agent 的订阅如何恢复？本次未核验所有多 Agent 重连路径，不能承诺自动完整恢复。

这些问题暂未作为架构决定采纳。
