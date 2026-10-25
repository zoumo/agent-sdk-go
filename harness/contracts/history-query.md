---
type: Specification
title: 独立历史查询
status: stable
---

# 独立历史查询

历史查询可以不创建 Session。区分 metadata（身份、标题、预览、时间和可用状态）、保存的内容（原生 messages/turns/items）和原始事件日志。前两者不构成完整 wire 通知、delta、审批或 hook 回放；保留 raw source 也不建立全事件日志 API。

查询不能 Open、start、resume、subscribe、submit 或回答审批，不运行模型、不将保存的内容投影为历史完成事件、不注入 Receive、不建立成员关系或回答权限。已有同身份 Session 时，查询不套用 idle/stable 门禁；其真实 live 通知仍正常交付。查询 metadata 或 turns 可能包含已有 live 状态。结果保持原生顺序、重复、unknown 数据及调用方所有权，不补造缺失事实。

两个原生包独立实现，没有根统一 Client、统一历史 wire 协议、通用 Turn 或第二套 Session 流。共同验收见 [US-014](stories/US-014.md)；接口及具体后端的支持状态由外部实施证据记录，不从本合同推定已完成共同能力。

## Codex 原生接口

`codex.Client` 提供以下独立方法，参数和结果是 `codex/types` 的原生绑定：

```go
ListThreads(context.Context, types.ThreadListParams) (types.ThreadListResponse, error)
ReadThread(context.Context, types.ThreadReadParams) (types.ThreadReadResponse, error)
ListThreadTurns(context.Context, types.ThreadTurnsListParams) (types.ThreadTurnsListResponse, error)
ListThreadItems(context.Context, types.ThreadItemsListParams) (types.ThreadItemsListResponse, error)
```

每次调用只返回一页或一次 read，不自动取全。`Optional` 区分 omitted/null/value；保留字段、unknown 变体及新服务端 readState/readStates 扩展，不用旧绑定猜测新字段含义。推荐 `ReadThread` 显式 `IncludeTurns: types.Some(false)` 读取 metadata。summary、full items、完整原始日志是三种不同覆盖，full 仍仅表示保存的 ThreadItem。

turns 原生默认 desc/summary，items 默认 asc。页大小、过滤和排序由固定服务端版本决定，不建立 SDK 的统一分页默认。cursor opaque；保留 nextCursor 和 backwardsCursor，显式 null 表示当前无后页，省略不能改写为 null。反向 turns anchor 可再次包含同一 turn；item anchor 排他，必须提供非空 turnId，后续继续使用原生 opaque cursor。SDK 不静默排序、去重、clamp 或补字段；无 snapshot token，不承诺跨页或与 live 状态一致的原子快照。

list 默认 interactive、non-archived 范围，不能称全部历史。保留 source/provider/archive、cwd、project/section、parent/ancestor、originator、sort 和 useStateDbOnly 等原生选项；本地与 hosted 的支持条件可以不同，原生错误不能改为空列表。版本、legacy history、DB/metadata 条件可能使 items/list 不支持，turns legacy fallback 可能逐页重建完整 rollout；SDK 不自动 resume 或改成事件回放。

查询经初始化后的 app-server 连接。构造 stdio 连接可启动进程，原生 list 可扫描及修复 metadata；不承诺零进程或零磁盘写，useStateDbOnly 保持原生含义。远端查询使用服务器存储及路径语义，不读取客户端 transcript，也不假定远端一定使用 LocalThreadStore。保留原生拒绝、协议/传输错误及 context 取消和已有 dispatch 证据，不自动重试。

## Claude 本地主会话查询

原生 package-level ListSessions、GetSessionInfo、GetSessionMessages 接受 context
和显式绝对 ConfigDir 的 query-only scope，不需要进程或 Session。其返回值分别是
metadata 及选出的 user/assistant 消息，不能强制成 Codex 的 Thread/Turn 或 cursor。
具体签名见[公开接口](../modules/native-session/interfaces.md)，范围及默认资源上限见
[metadata 合同](claude-session-metadata.md)和[消息合同](claude-session-messages.md)。

先发现身份与采样 metadata，再按所选身份读取消息。预览可来自压缩前提示词，不能
用它推定当前主链内容；消息选择保留压缩摘要并恢复相关并行工具 siblings，再按可见
消息切片。Limit/Offset 不代表 I/O 分页，没有 native cursor、total 或共享 snapshot。
metadata 的 complete 只认证本次发现和采样，不能认证未读中段或完整消息有效性。
消息失败不返回部分消息；metadata 可同时返回部分值和 error，必须检查二者。

## 扩展边界

本合同的主会话历史能力不承诺子 Agent 历史或独立订阅。子 Agent 发现、live
成员归属及原始回答权限继续服从[Session 合同](../modules/native-session/interfaces.md)。
独立父子消费流、子代理消息后端与新的 live 路由需要单独设计；查询不改变现有路由。

Claude 独立 child 发现和消息目标由[子历史合同](claude-subagent-history.md)单独定义；主历史算法和 Session 绑定发现均不能替代它。
