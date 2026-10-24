---
type: Specification
title: 原生 Session 公开 Interface
status: stable
---

# 原生 Session 公开 Interface

本页是[Session 深模块](design.md)的实现目标，不声明现有导出 API 已实现。两个运行时包各自声明具体 Go struct 和方法，不新增根接口、通用客户端或另一套运行模型。`types` 仅放无权限的原生值。下列签名中的配套值由本页定义其必要字段；原生类型名称沿各包 schema 命名，不要求两端类型可互换。

## Session 与交付值

两端分别提供 `Open(ctx, SessionOptions) (*Session, error)`、`Load(ctx, LoadOptions) (*Session, error)`。Codex 的 SessionOptions 选择自有进程或显式已有 Client 连接，并携带原生 ThreadStartParams；Claude 的 SessionOptions 携带原生进程选项，包括创建时 model/thinking。已有连接不再是正常调用必须拼装的订阅对象。LoadOptions 固定主身份、绝对 cwd、CodexHome 或 ConfigDir，以及有界历史读取选项。所有权在原生派发之前保留，失败不静默改为 Open。

两个 `*Session` 都有以下方法；同名配套类型位于各自运行时包：

```go
Receive(ctx context.Context) (Received, error)
Submit(ctx context.Context, input Input) (Submission, error)
SetModel(ctx context.Context, model string) (ConfigurationReceipt, error)
SetThinking(ctx context.Context, change ThinkingChange) (ConfigurationReceipt, error)
ListSubagents(ctx context.Context, query SubagentQuery) (SubagentPage, error)
Interrupt(ctx context.Context, options InterruptOptions) error
Close() error
Done() <-chan struct{}
Err() error
LoadState() LoadState
```

`Received` 是有判别字段的值，只携带一个事实类别：原生 Event、真实 IncomingRequest、SDK 关系/覆盖证据或 Load 边界。它保留原生顶层 Raw、Params、未知 envelope 字段、来源身份与 live/history 来源；历史含原始记录及可用关联字段。不能为历史重新构造“实时 Raw”。其 Event 复用原生类型；不是把两种协议收敛为一个事件模型。

Receive 是唯一消费入口，不另设 Events、Requests channel、审批回调或 History channel。并发调用争用同一 FIFO，不提供广播或独立游标。一次 context 取消只取消等待；与已经取出的记录竞争时可返回记录。Done 表示交互终止，Err 是稳定的首个交互终止原因；都不表示 FIFO 已排空。资源回收结果由 Close 返回，不能覆盖首个终止原因。正常关闭后 Receive 排空再返回 io.EOF，故障终止排空后返回终止错误。Close 幂等，清理不等消费。

主身份由 Session 固定。Codex 输入参数不再要求调用者重复填 ThreadID；Claude 输入不能将 session_id 或 parent_tool_use_id 当作任意目标选择器。主身份的 requested 与 observed 状态分别保留；Claude Open 尚未观察原生根身份时不伪造 observed ID。

## 原生输入与配置

| 值 | Codex | Claude |
|---|---|---|
| Input | 原生 `[]types.UserInput` 与 turn/start 可用执行选项；不含可改写的主 ThreadID。 | 原生 user message 的字符串或内容块及受支持关联/扩展；根 envelope 由 Session 固定，不从输出 decoder 推导发送许可。 |
| Submission | 保留原生 TurnStartResponse（包括 turn 身份）；成功表示 RPC 确认，不表示 turn 完成。 | 成功仅表示完整本地写入，没有虚构 CLI 接受回执或 Turn。 |
| ThinkingChange | 原生 ReasoningEffort 值，用 thread/settings/update 的 effort；不增加跨提供方档位。 | 动态 max_thinking_tokens 的可空整数及 thinking_display 的省略、summarized、omitted、null 清除；创建时原生 thinking enabled/adaptive/disabled 独立表达，不将创建模式隐式转换成动态预算。 |
| ConfigurationReceipt | 原生空响应、请求值与“原生接受”证据；settings updated 是另行收到的观察事实。 | 原始 control_response、请求值与“原生接受”证据；没有当前执行切换证明时 observed 保持未知。 |
| InterruptOptions | 显式原生 TurnID，ThreadID 固定为主会话；不猜最后收到的 turn。 | 原生 interrupt 不要求合成执行 ID；无执行选择字段。 |

Submit 使用原生 start 语义，不依据本地 Busy 推断 start/steer。若保留 Codex steer 扩展，必须显式选择并携带 expectedTurnId；不能自动切换。文本便捷调用只构造 Input 并调用同一 Submit。未知输入变体保留原文不等于确认可发送；已知未接入内容明确拒绝，不能转成提示词或丢字段。

SetModel 与 SetThinking 只改变主会话原生支持范围，不排队等待空闲、不中断、不重建、不递归修改子 Agent。Codex 的 model/effort 影响后续 turns；Claude 原生 ack 不建立跨执行的生效屏障。所有成功返回都不把 requested 写成 observed。原生拒绝保留原错误及关联，已知不支持在发送前明确拒绝。

## 真实 IncomingRequest

两个运行时分别定义 `IncomingRequest`，包含原生 request ID、method/subtype、完整 Raw/Params 及原来的权威回答句柄。提供 `Approve(ctx, nativeOptions)`、`Deny(ctx, nativeOptions)`、`Answer(ctx, nativeAnswer)` 和 `Context()`。nativeOptions/nativeAnswer 是各包按请求种类判别的具体值，不使用通用 map 建立共享回答状态机。不适用于该请求种类的操作在写前返回不支持/无效答案错误。

Codex 的适配覆盖 command execution、file change、permissions、原生 legacy approvals；Answer 对 tool/requestUserInput 保留 question ID 与答案数组。原生 scope、execpolicy/network amendment 等扩展按请求支持情况保留。RPC Reject（协议错误）不能映射成用户 Deny。Claude 的 Approve 保留 updatedInput/updatedPermissions，Deny 保留 message/interrupt，Answer 仅对真实 can_use_tool 请求中的问答工具保留题文键、问题列表和原生 updatedInput。

Context 在原生取消、已回答或交互终止时失效；回答前由同一 registry 再检查，至多一次有效发送。派发结果未知不恢复可重试权限，不自动重答。不增加默认审批倒计时或无人回答决定。ParseEvent、历史、子记录和隔离诊断均不能构造有效 IncomingRequest。

## 查询与 Load 状态

Codex SubagentQuery 使用 cursor、limit、sourceKinds、archived 等原生过滤，ancestorThreadId 固定为本 Session 主身份，不同时发送 parentThreadId。SubagentPage 保留原生 Thread 记录、nextCursor 和查询覆盖条件。完整非归档与归档覆盖需要分别查询；source 默认 interactive 不等于子 Agent 全覆盖。实验能力未启用或被拒绝必须显式报告，不能回退为缓存。

Claude SubagentQuery 使用本 Session 固定的 ConfigDir/cwd/main ID 与记录数/字节限制，发现主 transcript 旁的 subagents 记录及有证据的嵌套布局。SubagentPage 保留 agent ID、可用 parent/tool/sidecar 元数据、实际扫描范围和完整/部分/未知覆盖。无原生分页时不伪造原生 cursor；资源限制、访问失败和未支持布局不能作为完整空列表。两端允许部分记录与错误同时返回；调用者以 coverage 和 error 判断，不以长度判断完整。列表不触发 resume/start/subscribe、回放或成员观察范围更新。

LoadState 是 pending/succeeded/failed 的可查询快照，包含 requested identity、原生确认的 observed identity（可缺）、确认来源及失败原因。Open 的状态为 not-requested。Load 的 pending 状态不允许 Submit/配置；真实请求回答、Interrupt、Close 保持可用。Load 成功须同时具备原生同身份恢复确认及主历史完整接受，通过同一 Receive 交付完成边界；独立状态不需要消费到边界才能查询。失败即终止该 Session、清理所属资源并保留已接受事件排空，不开放普通输入。

当固定原生基线没有已确认的无执行恢复机制时，Load 返回明确的恢复未确认错误，或返回 Session 后将 LoadState 置 failed；不能无限等待应用输入，也不能在失败前交付历史为已恢复回放。超时不是恢复证明，initialize、指定 ID 和磁盘 ID 不是恢复确认。

## 错误语义

以下分类在两个运行时分别以可判别错误实现，保留 native cause；名称可沿用已有原生错误类型，不要求共享枚举。

| 分类 | 语义与可重试边界 |
|---|---|
| ownership-conflict | 同连接同主身份已有所有者或不确定的附着；本地拒绝，不另建订阅。 |
| unsupported / invalid-input | 已知能力或参数不支持/不合法；未发送。原生未知限制保留原生拒绝，不猜测成功。 |
| load-pending / load-failed | 成功门禁尚未满足或已经失败；普通输入及配置未发送。 |
| resume-unconfirmed / identity-mismatch | 缺少同身份原生确认或实际响应身份不同；Load 失败，前者不冒充不存在的会话，后者不继续回放。 |
| request-invalid / already-answered | 原句柄不存在、取消、已回答或已终止；不产生新回答权限。 |
| not-sent / outcome-unknown | 前者确认未派发；后者可能已经写出或执行。输入、配置、回答及释放的后者不自动重试。 |
| native-rejected | 保留原生 RPC/control 错误及关联；不是正常执行失败事件，也不强制结束其他交互。 |
| history-integrity / resource-limit / coverage-gap | 保留 offending raw、游标/路径、已交付及未覆盖范围；不能以截断成功处理。覆盖缺口可作为证据交付，不必全部终止。 |
| transport / protocol / closed | SDK 交互终止与原生执行失败分开；停止新接受、失效权限、排空后暴露原因。 |

具体历史完整性与关闭规则以[异步历史合同](../../contracts/async-history.md)及[事件交付合同](../../contracts/event-delivery.md)为准。验收使用本页公开方法，不依赖私有父图、回调池或第二消费者。
