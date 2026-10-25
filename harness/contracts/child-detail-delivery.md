---
type: Specification
title: 可选子详细事件交付
status: stable
---

# 可选子详细事件交付

本页规定 [ADR-0004](../adr/0004-subagent-event-delivery.md) 的确定性扩展合同，
是实现目标，不声明 API 已提供。两原生包各自实现，不能新增根 Client/config wrapper。
共同场景见 [US-003](stories/US-003.md)，历史另见[子历史](claude-subagent-history.md)。

## 原生公开选项

两包各自在运行时包声明以下值（不是共享类型，不进入 native wire types）：

```go
type ChildDetailPolicy string
const (
    ChildDetailPreserve ChildDetailPolicy = "preserve"
    ChildDetailSuppress ChildDetailPolicy = "suppress"
)
// 新字段：SessionOptions.ChildDetailPolicy ChildDetailPolicy
```

空字符串和 preserve 完全等价；suppress 仅抑制下面白名单中的已确认 child detail。
其他字符串（包括大小写变体）在进程启动、连接注册或 RPC 写入前返回包装本包
ErrInvalidInput 的错误，不忽略、不 clamp、不回退。选项在 Open 前复制并固定，
对该 Session 全部后续 Submit 和子成员生效，无 setter、自动切换或全连接策略。
默认与现有帧交付及原生字段逐字兼容；调用者修改原 options 不改变 Session。

| 原生入口 | 精确范围 |
|---|---|
| codex.Open(ctx, codex.SessionOptions{ChildDetailPolicy: …}) | 仅新建的 Session；Client 和 Stdio 两种连接所有权一致。共享 Client 上各 Session 独立。 |
| codex.Client.ResumeThread(ctx, types.ThreadResumeParams) | 原生低层显式 resume 保持原签名，无 ChildDetailPolicy wire 字段，不新建 Session、不重设现有策略；其附着/执行副作用保留。当前没有 Session 级 resume 构造，本扩展不新增它。 |
| claude.Open(ctx, claude.SessionOptions{ChildDetailPolicy: …}) | claude.Options 同名字段，经现有 SessionOptions 定义传递。零值创建和显式 ResumeID 均在 Open 固定策略；ResumeID 仍是原生恢复请求，不等于历史/纯订阅。 |
| 两端 Client 原生读取、独立历史及诊断 | 不受该策略影响；无额外 child 输出参数。 |

Claude initialize 必须继续显式发送 forwardSubagentText:true，不能因 suppress 改成
false/省略。过滤不是源输出控制，不修改提示词、工具、原生内部结果回父或模型配置。
省略详细交付不能承诺降低上游 token/运行费用、源帧大小或全部内存使用。

## 归属、处理顺序及保护规则

先按[事件交付](event-delivery.md)完成原生校验、所有权判断、控制 registry 更新和
成员/关系/工具 alias/生命周期处理，再决定本帧是否进入唯一 FIFO。已确认 child
指当时已确认 Session 成员且不是根；不要求完整父图。Codex 使用确认的 threadId；
Claude 仅使用审阅版本 task/tool alias 确认的 AssociationID，单独 agent_id、任意
parent_tool_use_id、文本或路径猜测都不充分。不得扩大版本的身份确认规则。

未知源/未知版本/解码错误/未知方法或变体均不因过滤丢弃；原已归属的保留，未归属的
仍按原容量/到期规则隔离，不广播、不补投。foreign 诊断也不能当 child 删除。
后续确认不重评旧帧、不把隔离材料回填 live。共享连接的回答权仍只有原 controller。

以下事实始终保护，优先于所有白名单：

- 所有真实审批、问题、工具调用请求的原始句柄、Raw/Params、取消/撤销/解决和控制响应；
  单次回答、原 context 及 OutcomeUnknown 规则不变，不产生自动回答或 observer 权限。
- 根/父原生工具结果，尤其 spawn/wait/Agent 子任务结果及失败；不能从结果文本猜成 child。
- 原生身份/关系/创建或关联、任务开始、后台状态、完成、失败、取消、错误和 SDK 覆盖证据。
  根结果、子工具完成、子任务 terminal 和后台停止仍是不同事实。
- 一个帧同时携带上述事实与 detail，或携带未知语义时，保留整个帧。

保留帧的 Raw、Params、原生 payload/unknown/omitted/null 和相对次序不变；不拆分、
删字段、重新 marshal 或制造一个看似原始的帧。不制造 filtered 占位事件或第二队列。
未知扩展字段默认视为 mixed，保留整帧；白名单只允许固定 schema 的已知字段集合。
普通关联用 threadId/turnId/itemId/UUID 等引用字段不单独构成新关系事实，但原生
身份扩展/任务 parent/终态字段构成保护事实。过滤发生在入队前，不能妨碍控制进展；
Close 排空、Abort 放弃 backlog、取消等待、资源限制和原生处理顺序保持原合同。

## Codex 逐方法分类

下面是封闭白名单，要求已确认 child、成功通过该方法的固定 schema 形状校验（不要求已有 typed binding）、仅固定 schema 字段、无保护事实。
未列方法一律保留，包括未来 delta。

| 方法或 envelope | suppress 行为 |
|---|---|
| item/agentMessage/delta | 可抑制纯 delta。 |
| item/reasoning/textDelta、item/reasoning/summaryTextDelta、item/reasoning/summaryPartAdded | 可抑制纯思考进度。 |
| item/commandExecution/outputDelta、item/fileChange/outputDelta、item/mcpToolCall/progress | 可抑制纯工具进度；请求审批与结果不是进度。 |
| item/started、item/completed（所有 item 变体） | 整帧保留：item 生命周期/工具结果；其中的全文可能仍可见。这是明确的过滤局限。 |
| thread/*、turn/*、error、serverRequest/resolved、SDK Observation、IncomingRequest | 全保留；thread/started 与 collabAgentToolCall/subAgentActivity 关系处理照常。 |
| 其他方法、未知 item/字段、DecodeError | 按现有归属保留/隔离，不推断为白名单。 |

## Claude 逐 envelope 分类

同样只对已确认 child 且成功解析的封闭已知形状抑制。已知字段集以审阅的
stream-json 版本为界，unknown content block/扩展一律 mixed。

| envelope/内容 | suppress 行为 |
|---|---|
| assistant.message.content 全为 text/thinking/redacted_thinking；无 error、tool_use_result 或工具块 | 可抑制纯详细消息；已知 usage、stop_reason 是消息元数据，不等于任务 terminal。 |
| stream_event.event.type=content_block_delta；delta.type 为 text_delta/thinking_delta/signature_delta/input_json_delta | 可抑制纯增量；input_json_delta 仅工具参数进度，不含控制请求。 |
| stream_event 的 message_start/message_delta/message_stop/content_block_start/content_block_stop/ping 及未知 API 类型 | 保留整帧；边界/usage 等不会被误作任务完成，也不维护额外配对状态。 |
| assistant 含 tool_use/server_tool_use 或未知块；user 含 tool_result；任何其他 user | 全保留，保留工具归属/结果和 mixed 内容。 |
| system（包括 task_started/task_progress/task_notification/background_tasks_changed）、result、control_request/control_response/control_cancel_request、rate_limit_event、conversation_reset | 全保留。task_progress 可混有任务身份/状态，不做纯文本猜测。 |
| 其他 envelope/字段、错误、unknown source | 按现有归属保留/隔离。 |

本策略是尽力减少已审阅的纯 detail，并非 child 内容安全擦除接口；完整 item、mixed
assistant、系统通知和未知帧仍可能含文字/思考/工具内容。调用者不得将其当脱敏保证。
