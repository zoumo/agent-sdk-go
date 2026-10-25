---
type: Specification
title: 独立历史查询与 Codex 原生页
status: stable
---

# 独立历史查询与 Codex 原生页

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
