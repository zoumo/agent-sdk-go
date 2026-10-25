# 从用户动作看 Codex Session 事件

规范见 [Session 接口](../../harness/modules/native-session/interfaces.md)。
应用通过 Open 取得 Session，以 Submit 提交输入，并持续调用
Receive。原生 Event、真实 IncomingRequest、SDK Observation共用
一个 FIFO。每个事件保留 Raw、Params、来源 thread/turn/item 和未知数据。

## 1. 用户提交普通任务，看到答案并结束本次工作

Open 确定主线程。Submit 保留原生 TurnStartResponse 和 turn ID；早到的原生
通知不能代替调用响应。一个 turn/completed 只结束对应原生 turn，Session
继续接收。应用选择何时 Close；Close 后排空已接受事实或用 Abort 放弃消费。

## 2. 用户要求执行工具，查看输出与工具结果

工具 started、delta、completed 保持原生身份和顺序。未知工具变体保留原文。
历史完整 item 是带 History 来源的快照，不伪造 started 或 delta。

## 3. 用户回答权限或问题，再观察实际结果

Receive 中的真实请求携带唯一原句柄。应用保留它，在等待用户时继续接收。
Approve/Deny/Answer 保留原生选项；RPC Reject 与用户拒绝不同。取消、原生
resolved 和终止使原句柄失效。NotSent 可保留活权限；未知写入结果不可重试。

## 4. 用户委派并行任务，查看子线程与嵌套孙线程

固定原生证据可确认成员，父级知识独立。已确认成员的消息不等祖先图完整，
也不按祖先顺序改排。重复 spawn、delta 和未知 item 原样保留。无归属的早到
消息进入有界诊断隔离区，后续关系不会把它补进实时流。查询记录不创建成员。

## 5. 用户启动耗时命令，持续查看后台输出

持续 Receive 并按原生来源展示工具输出。Interrupt 需要明确 TurnID；确认
中断请求不等于工具回滚，也不推断全部子线程已停止。

## 6. 用户看到父答案，但后台工作尚未结束

父答案、父 idle、子 active、子 turn 结束和后台输出是独立事实。父子 text、
thinking 和 usage 不合并，不从快照缺项或安静推断成功。

## 7. 用户经历交错、重复、缺失与断线后恢复观察

保留每次原生到达和未知字段。诊断隔离区报告容量、字节、期限、丢弃/过期数
及覆盖缺口。断线后已接受事件排空并报告失败；SDK 不自动重连或重发。

## 用户主动停止与关闭的边界

Receive 的 context 只取消这次等待；Close 停止准入并撤销所属请求，按引用释放
共享 attachment，不等待消费者。原生低层连接操作是显式扩展，不能建立第二个
Session owner。实际合成协议回归见 session_native_regression_test.go、
turn_contract_test.go、request_contract_test.go 和 remote_*_test.go；不代表真实
子 Agent 验证。历史录制与源码 pin 仍按原范围解释。

## Pinned source references

- [`092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`](https://github.com/openai/codex/tree/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d)
- [当前 TurnComplete 路径](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/bespoke_event_handling.rs#L185-L202)
- [原生 method 注册](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/common.rs)
- [输出通知映射](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/event_mapping.rs)
- [item 生命周期处理](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/bespoke_event_handling.rs)
- [V2 成功 Started](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/tools/handlers/multi_agents_v2/spawn.rs#L185-L239)
- [首次输入接纳](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/agent/control/spawn.rs#L923-L959)
- [官方无 collab 测试源码](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/turn_start.rs#L4493-L4593)
- [后台与 exit watcher](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/unified_exec/async_watcher.rs)
- [delta 数量上限](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/exec.rs)
- [进程管理与 write_stdin](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/unified_exec/process_manager.rs)
- [完成回传](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/agent/control/completion.rs)
- [mailbox 与 trigger_turn](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/agent/control.rs#L253-L323)
- [durable sleep 例外](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/tasks/mod.rs#L428-L490)
- [输入处理](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/session/handlers.rs)
