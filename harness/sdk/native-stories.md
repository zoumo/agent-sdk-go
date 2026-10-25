---
type: Specification
title: 原生补充用户故事
status: stable
---

# 原生补充用户故事

本页在[共同的 19 项目标](user-stories.md)之上增加原生验收条件，不替换共同需求，也不证明实现完成。编号独立于 SDK-US、场景 US 和录制 S1/S2/S3；条件不触发、原生版本不支持、尚未实现和缺失证据须分别记录。公开操作边界仍遵循 [Session Interface](../modules/native-session/interfaces.md)与[独立历史查询](../contracts/history-query.md)。

## Codex

| 编号 | 用户目标 | 补充验收 |
|---|---|---|
| CODEX-US-001 | 区分应用 dynamic tool、原生审批和问题 | item/tool/call 的工具结果不算 command/file/permissions 审批；审批保留 decision 及 scope/policy/network 扩展；requestUserInput 按 question ID 回答。只有原始有效请求有回答权；协议 Reject 不冒充用户 Deny。对应 SDK-US-004/009/010/011。 |
| CODEX-US-002 | 按原生规则提交输入与修改配置 | turn/start 与 steer/排队操作分开；执行期 start 的接受或拒绝保留，不自行 steer/重试。创建 model/config 与动态 thread settings 分开；experimental 拒绝、空 ack 与后续 observed settings 分开，不推断当前 turn 或子任务生效。对应 SDK-US-001/002/003/012/013/015。 |
| CODEX-US-003 | 查询原生保存内容并识别恢复副作用 | list/read/turns/items 保留 typed optional、unknown、opaque cursor、summary/full、有效空和原生不支持；local app-server 与 remote server storage 范围不同。元数据查询不恢复或订阅，显式 resume 不能伪装成纯查询；保存 items 不是完整事件日志。对应 SDK-US-008/018/019。 |
| CODEX-US-004 | 按传输所有权清理与恢复观察 | stdio 只终止自有进程；WS/WSS Close 不停止远端服务。保留 TLS、Origin、认证及错误隐私边界，不自动重连或恢复旧句柄。慢消费、失联、排空与清理分别可观察，不宣称公开 WSS 已通过真实部署验证。对应 SDK-US-014/016/017/018。 |

## Claude

| 编号 | 用户目标 | 补充验收 |
|---|---|---|
| CLAUDE-US-001 | 区分输入写入和原生控制确认 | stream-json 完整本地写入不表示模型接受/执行；priority/shouldQuery 保留原生含义，不添加 scheduler、steer 或通用 Turn。control_response 按原 request_id 关联；取消写入的 NotSent/OutcomeUnknown 不互换或重发。对应 SDK-US-001/002/003/004/015。 |
| CLAUDE-US-002 | 用原权限请求回答问题 | 仅实际 can_use_tool 创建权限；AskUserQuestion 使用完整题文键，通过 UpdatedInput 保留 questions 和 unknown；普通 tool_use、tool_result、输入与历史均不授予回答权。自动批准路径不保证发请求，延迟时可继续 Receive，撤销/终止/已答后不再授予权限。对应 SDK-US-009/010/011。 |
| CLAUDE-US-003 | 区分任务失败、result 与 idle | 检查 result.is_error 和原生错误字段，不能只看 subtype:success；后续 idle、initialize 接受和请求转发选项均不证明模型或工具成功；root result 不证明子任务已停止。对应 SDK-US-004/005/007/017。 |
| CLAUDE-US-004 | 保留思考与模型配置的原生维度 | 创建 thinking enabled/adaptive/disabled 与动态 max_thinking_tokens/display 分开；省略、null-clear 和 0 分开。effort 不映射成 budget，ack 不作 observed 或子继承证明，原生 deprecated budget 不保证硬 token 上限。对应 SDK-US-012/013。 |
| CLAUDE-US-005 | 显式读取有界本地主会话记录 | 独立查询要求 ConfigDir，不隐式读取 HOME；metadata 的 head/tail 采样与完整可见消息链分开，保留压缩及并行 tool-result 恢复规则与 unknown。offset/limit 是内容切片，不伪造 cursor、total、原子快照或 running；Session.ListSubagents 的本地子记录发现不等于独立子历史。对应 SDK-US-008/019。 |

新增 child 过滤、独立子消息查询和重新附着能力遵循 [ADR-0004](../adr/0004-subagent-event-delivery.md)，不能用主会话验收代替。原生差异不免除共同目标；未实现和真实证据缺口仍须保留。
