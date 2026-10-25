---
type: Specification
title: 原生 Session 公开 Interface
status: stable
---

# 原生 Session 公开 Interface

本页是[Session 深模块](design.md)的实现目标，不声明现有导出 API 已实现。两个运行时包各自声明具体 Go struct 和方法，不新增根接口、通用客户端或另一套运行模型。`types` 仅放无权限的原生值。下列签名中的配套值由本页定义其必要字段；原生类型名称沿各包 schema 命名，不要求两端类型可互换。

## Session 与交付值

两端分别提供 `Open(ctx, SessionOptions) (*Session, error)`。Codex 的 SessionOptions 选择自有进程或显式已有 Client 连接，并携带原生 ThreadStartParams；Claude 的 SessionOptions 携带原生进程选项，包括创建时 model/thinking。已有连接不再是正常调用必须拼装的订阅对象。所有权在原生派发之前保留，失败明确报告。

两个 `*Session` 都有以下方法；同名配套类型位于各自运行时包：

```go
Receive(ctx context.Context) (Received, error)
Submit(ctx context.Context, input Input) (Submission, error)
SetModel(ctx context.Context, model string) (ConfigurationReceipt, error) // Codex
SetModel(ctx context.Context, change ModelChange) (ConfigurationReceipt, error) // Claude
SetThinking(ctx context.Context, change ThinkingChange) (ConfigurationReceipt, error)
ListSubagents(ctx context.Context, query SubagentQuery) (SubagentPage, error)
Interrupt(ctx context.Context, options InterruptOptions) error
Close() error
Done() <-chan struct{}
Err() error
```

`Received` 是有判别字段的值，只携带一个事实类别：原生 Event、真实 IncomingRequest、SDK 关系/覆盖证据。它保留原生顶层 Raw、Params、未知 envelope 字段与来源身份。其 Event 复用原生类型；不是把两种协议收敛为一个事件模型。

Receive 是唯一消费入口，不另设 Events、Requests channel、审批回调或 History channel。并发调用争用同一 FIFO，不提供广播或独立游标。一次 context 取消只取消等待；与已经取出的记录竞争时可返回记录。Done 表示交互终止，Err 是稳定的首个交互终止原因；都不表示 FIFO 已排空。资源回收结果由 Close 返回，不能覆盖首个终止原因。正常关闭后 Receive 排空再返回 io.EOF，故障终止排空后返回终止错误。Close 幂等，清理不等消费。

Codex 的 `StartResponse() (types.ThreadStartResponse, bool)` 返回 Open 实际收到的原生启动响应副本，保留 model、sandbox 和未知字段。该只读查询不发送协议消息，不产生消费者或回答权限。

主身份由 Session 固定。Codex 输入参数不再要求调用者重复填 ThreadID；Claude 输入不能将 session_id 或 parent_tool_use_id 当作任意目标选择器。主身份的 requested 与 observed 状态分别保留；Claude Open 尚未观察原生根身份时不伪造 observed ID。

## 独立历史查询

历史查询不属于 Session 生命周期，不要求 Open 或同身份空闲。Codex Client 的四个原生查询签名、结果所有权、后端与分页边界见[独立历史查询合同](../../contracts/history-query.md)。查询不注入 Receive，不建立成员关系或回答权限；原生 resume 继续独立表达。

## 原生输入与配置

| 值 | Codex | Claude |
|---|---|---|
| Input | 原生 `[]types.UserInput` 与 turn/start 可用执行选项；不含可改写的主 ThreadID。 | `Input{Text}` 或 `Input{User: *types.UserInput}`，后者携带原生 InputMessage 与关联/扩展；支持 text、image、document、tool_result，其他块明确拒绝。image source 支持 base64/url，document source 支持 base64/url/text/content；工具结果/文档的嵌套内容同样校验支持范围。根 envelope 由 Session 固定，不从输出 decoder 推导发送许可。 |
| Submission | 保留原生 TurnStartResponse（包括 turn 身份）；成功表示 RPC 确认，不表示 turn 完成。 | 成功仅表示完整本地写入，没有虚构 CLI 接受回执或 Turn。 |
| ModelChange | SetModel 直接接受原生模型标识字符串。 | `Model *string`：nil 省略 model，恢复原生默认；非 nil 非空标识逐字发送；不发送 model:null，不将空字符串或跨提供方别名等同默认。 |
| ThinkingChange | 原生 ReasoningEffort 值，用 thread/settings/update 的 effort；不增加跨提供方档位。 | `MaxThinkingTokens *int` 的 nil 发送 null 清除，非 nil 保留 0/整数；`ThinkingDisplay *string` 的 nil 省略，值为 summarized/omitted，`ClearDisplay` 单独发送 null 清除；创建时原生 thinking enabled/adaptive/disabled 独立表达，不将创建模式隐式转换成动态预算。 |
| ConfigurationReceipt | 原生空响应、请求值与“原生接受”证据；settings updated 是另行收到的观察事实。 | `Requested` 为确切原生请求 payload，`Response` 为完整 control_response，`Accepted` 仅表示 success ack；`Observed` 保持未知，实际事实由 Receive 单独交付。 |
| InterruptOptions | 显式原生 TurnID，ThreadID 固定为主会话；不猜最后收到的 turn。 | 原生 interrupt 不要求合成执行 ID；无执行选择字段。 |

Submit 使用原生 start 语义，不依据本地 Busy 推断 start/steer。若保留 Codex steer 扩展，必须显式选择并携带 expectedTurnId；不能自动切换。文本便捷调用只构造 Input 并调用同一 Submit。未知输入变体保留原文不等于确认可发送；已知未接入内容明确拒绝，不能转成提示词或丢字段。

SetModel 与 SetThinking 只改变主会话原生支持范围，不排队等待空闲、不中断、不重建、不递归修改子 Agent。Codex 的 model/effort 影响后续 turns；Claude 原生 ack 不建立跨执行的生效屏障。所有成功返回都不把 requested 写成 observed。原生拒绝保留原错误及关联，已知不支持在发送前明确拒绝。

## 真实 IncomingRequest

两个运行时分别定义 `IncomingRequest`，包含原生 request ID、method/subtype、完整 Raw/Params 及原来的权威回答句柄。提供 `Approve(ctx, nativeOptions)`、`Deny(ctx, nativeOptions)`、`Answer(ctx, nativeAnswer)` 和 `Context()`。nativeOptions/nativeAnswer 是各包按请求种类判别的具体值，不使用通用 map 建立共享回答状态机。不适用于该请求种类的操作在写前返回不支持/无效答案错误。

Codex 的适配覆盖 command execution、file change、permissions、原生 legacy approvals；Answer 对 tool/requestUserInput 保留 question ID 与答案数组。原生 scope、execpolicy/network amendment 等扩展按请求支持情况保留。RPC Reject（协议错误）不能映射成用户 Deny。Claude 的 Approve 保留 updatedInput/updatedPermissions，Deny 保留 message/interrupt，Answer 仅对真实 can_use_tool 请求中的问答工具保留题文键、问题列表和原生 updatedInput。

Context 在原生取消、已回答或交互终止时失效；回答前由同一 registry 再检查，至多一次有效发送。派发结果未知不恢复可重试权限，不自动重答。不增加默认审批倒计时或无人回答决定。ParseEvent、历史、子记录和隔离诊断均不能构造有效 IncomingRequest。

## 子 Agent 查询

Codex SubagentQuery 使用 cursor、limit、sourceKinds、archived 等原生过滤，ancestorThreadId 固定为本 Session 主身份，不同时发送 parentThreadId。SubagentPage 保留原生 Thread 记录、nextCursor 和查询覆盖条件。完整非归档与归档覆盖需要分别查询；source 默认 interactive 不等于子 Agent 全覆盖。实验能力未启用或被拒绝必须显式报告，不能回退为缓存。

Claude SubagentQuery 使用本 Session 固定的 ConfigDir/cwd/main ID 与记录数/字节限制，发现主 transcript 旁的 subagents 记录及有证据的嵌套布局。SubagentPage 保留 agent ID、可用 parent/tool/sidecar 元数据、实际扫描范围和完整/部分/未知覆盖。无原生分页时不伪造原生 cursor；资源限制、访问失败和未支持布局不能作为完整空列表。两端允许部分记录与错误同时返回；调用者以 coverage 和 error 判断，不以长度判断完整。列表不触发 resume/start/subscribe、回放或成员观察范围更新。

## 错误语义

以下分类在两个运行时分别以可判别错误实现，保留 native cause；名称可沿用已有原生错误类型，不要求共享枚举。

| 分类 | 语义与可重试边界 |
|---|---|
| ownership-conflict | 同连接同主身份已有所有者或不确定的附着；本地拒绝，不另建订阅。 |
| unsupported / invalid-input | 已知能力或参数不支持/不合法；未发送。原生未知限制保留原生拒绝，不猜测成功。 |
| request-invalid / already-answered | 原句柄不存在、取消、已回答或已终止；不产生新回答权限。 |
| not-sent / outcome-unknown | 前者确认未派发；后者可能已经写出或执行。输入、配置、回答及释放的后者不自动重试。 |
| native-rejected | 保留原生 RPC/control 错误及关联；不是正常执行失败事件，也不强制结束其他交互。 |
| history-integrity / resource-limit / coverage-gap | 保留 offending raw、游标/路径、已交付及未覆盖范围；不能以截断成功处理。覆盖缺口可作为证据交付，不必全部终止。 |
| transport / protocol / closed | SDK 交互终止与原生执行失败分开；停止新接受、失效权限、排空后暴露原因。 |

关闭规则以[事件交付合同](../../contracts/event-delivery.md)为准。验收使用本页公开方法，不依赖私有父图、回调池或第二消费者。
