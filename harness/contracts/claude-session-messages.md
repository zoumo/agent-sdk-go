---
type: Specification
title: Claude 独立本地消息查询
status: stable
---

# Claude 独立本地消息查询

Claude 原生包提供 `GetSessionMessages(ctx, sessionID, SessionMessagesOptions)
(SessionMessagesPage, error)`。这是 query-only 存储读取，不需要 Client、Session
或可执行 runtime，不调用 Open、resume、start、subscribe、submit 或审批，不更新
Session 成员、归属和权限，不投影或注入 Receive。与 live Session 并存不增加消费者。

## Scope 与值

`SessionQueryScope{ConfigDir, ProjectDir, MaxEntries}` 是可复用的无权限查询范围。
ConfigDir 必须显式绝对路径，不读取用户 HOME、环境默认目录或客户端历史。
ProjectDir 可选且必须绝对，解析现有项目目录的 symlink 后使用原生非字母数字替换
为 `-` 的项目 key。超过 200 字符的截断 key 不支持。未指定项目时只发现
ConfigDir/projects 的直接项目子目录，扫描所有项目后才确认唯一匹配；同一 ID
多处匹配报 ambiguous。只覆盖该布局下主会话 `<UUID>.jsonl`，不展开 worktree、
不运行 git，不搜索其他配置或旧布局。programmatic 会话仅在同一布局下可读。
不包含子代理消息、system inclusion、SessionStore 或云后端。

`SessionMessage` 携带 Type、UUID、SessionID、可用 ParentUUID/Timestamp、原生
Message JSON 和整个原始 Raw。Raw 保留未知 envelope 字段、omitted/null/value
和大整数；Message 保留未知内容块与数字，不经过 float64。
值与源和后续调用 detached，无 IncomingRequest 或任何回答方法。Raw 是源检查
材料，不构成完整 wire 通知、delta、审批或 hook 回放 API。

## 选择与切片

先对完整有界源建索引，找 terminal 的最近 user/assistant，优先选择非
sidechain/team/meta 的最后文件位置主叶；无主叶时沿最后候选叶回溯并过滤。
仅沿 parentUuid 重建根到叶链，不沿 compact_boundary.logicalParentUuid
重复压缩前内容；保留 isCompactSummary。相关并行 tool_use/tool_result 按原生
response message.id、直接 parent、sourceToolAssistantUUID 和同 writer 的工具
调用身份恢复；工具调用跨 response 歧义时不用于匹配 stray，已回答调用不恢复
额外 stray。恢复内容遵循原生稳定 timestamp 排序及 tie 次序，父链不另排序。
最终仅返回非 sidechain/team/meta 的 user/assistant，再应用 Offset/Limit。

Limit/Offset 非负，Limit=0 使用 SDK 有界默认 1000；MaxMessages 默认 10000，
请求 Limit 超过此上限报 resource-limit。超页尾和有效空源成功返回空 Messages。
无 native cursor、nextCursor 或 total；切片不是 I/O 分页，每次可能扫描整个有界源。

## 资源、错误和一致性

零值默认 MaxBytes=64 MiB、MaxLines=100000、MaxRecords=100000、
MaxLineBytes=8 MiB、MaxEntries=10000。MaxLines 包括空行和 metadata，MaxRecords
包括全部索引链条（含被过滤分支）。所有上限和切片参数可配置，负数拒绝。
每条 JSONL 必须换行结束。配置根被显式选定并固定，树内目录和文件必须是普通
目录/regular file，不接受 symlink；os.Root 约束树内路径替换不能逃逸配置根。
文件以非阻塞/no-follow（平台支持时）打开，避免 FIFO 阻塞；不读取私有外部路径。

`SessionQueryError{Kind, Path, Reason, Raw, Cause}` 可判别 invalid-input、missing、
inaccessible、corrupt、unsupported、ambiguous、resource-limit、changed；Cause
保留 OS 错误。取消保留 context 错误。覆盖 Status 为 unknown、partial 或 complete，
返回实际范围、字节、行、记录、发现数量与有效 Limits。任何错误都不返回部分消息，
不把 partial 截断为成功。duplicate key/UUID、cycle、missing-parent、无效 UTF/JSON、
冲突身份等报 corrupt；未来未知 entry type 报 unsupported。已确认的非消息记录
api-request-shape、api-request-blob、api-request、cost-state 在独立查询中不进入
消息链、无 UUID/parentUuid 要求；仍先验证完整 JSON 和可用 sessionId/session_id
身份，并计入源字节、行及单行上限。此分类不扩大旧 replay 的 frozen-tail 许可。

读取结束前重读并比较整个源哈希、长度、mtime 和原根相对路径文件身份；观察到
append、rewrite、文件/目录 replacement 报 changed。Atomic 始终 false：文件系统
没有事务锁，校验之后的写入、两次校验间完全恢复的更改、配置根句柄外部改名不保证
检测。各页/各调用没有共同快照，调用者应自行决定重试策略。文件存在不证明 running。
