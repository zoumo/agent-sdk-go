---
type: Specification
title: Claude 独立本地子历史
status: stable
---

# Claude 独立本地子历史

这是 query-only 的实现目标，与 [Session 发现](../modules/native-session/interfaces.md)
和[主消息查询](claude-session-messages.md)分开。无需 Client/Session/CLI，不读环境或
HOME，不运行 git/model，不写存储，不 resume/subscribe/Submit，不改变 live 成员、
FIFO、alias 或审批权限。已有同身份 Session 不要求 idle，查询不增加消费者。

## 固定公开 API 与值

以下声明位于 claude 运行时包；既有 Session.ListSubagents/SubagentQuery 不改名，
不使用它的冻结进程环境冒充显式独立查询 scope：

```go
func ListSubagents(context.Context, string, ListSubagentsOptions) (SubagentsPage, error)
func GetSubagentMessages(context.Context, string, string, SubagentMessagesOptions) (SubagentMessagesPage, error)
// string 参数依次为 parentSessionID、agentID；List 只有 parentSessionID。
type SubagentHistoryLimits struct {
    MaxBytes int64
    MaxLines, MaxRecords, MaxLineBytes int
    MaxFiles, MaxDirectories, MaxDepth int
    MaxSidecarBytes int64
}
type ListSubagentsOptions struct {
    Scope SessionQueryScope
    Limits SubagentHistoryLimits
    Limit, Offset, MaxAgents int
}
type SubagentMessagesOptions struct {
    Scope SessionQueryScope
    Limits SubagentHistoryLimits
    Limit, Offset, MaxMessages int
}
type SubagentInfo struct {
    SessionID, AgentID, Path string
    ParentAgentID, ParentToolUseID *string
    Sidecar json.RawMessage
    Running string // 始终 "unknown"
}
type SubagentMessage struct {
    SessionMessage // 原 transcript 的 Type/UUID/SessionID/ParentUUID/Timestamp/Message/Raw
    AgentID string // 所选文件身份，不从 message text 推断
    ParentAgentID, ParentToolUseID *string // 仅来自 sidecar
}
type SubagentHistoryCoverage struct {
    Status string // unknown、partial、complete
    ConfigDir, ProjectDir, SessionID, AgentID, MainPath, Root, Path string
    Directories []string
    Entries, Files, Lines, Records int
    Bytes int64
    Atomic bool // 始终 false
    Limits SubagentHistoryLimits
    MaxEntries, Limit, Offset, MaxResults int // 生效值；MaxResults 对应 MaxAgents/MaxMessages
    SidecarStatus string // list 为 "per-record"；get 为 absent/present
}
type SubagentsPage struct {
    Records []SubagentInfo
    Coverage SubagentHistoryCoverage
}
type SubagentMessagesPage struct {
    Info SubagentInfo
    Messages []SubagentMessage
    Coverage SubagentHistoryCoverage
}
```

指针 nil 表示未提供或显式 null，差异保留于原始 Sidecar/Raw；不把缺 parentAgentId
推成根的直接 child。所有 JSON/bytes/slices/pointers detached，修改结果不影响下次
查询或 Session。JSON 保留未知字段/块、大小写、大整数及 null/省略，不经过 float64。
Sidecar 原文和查询错误 Raw 可能私密，错误字符串不自动打印它们。

## 定位、布局及身份

复用 SessionQueryScope：ConfigDir 必须显式绝对；可选 ProjectDir 必须绝对，使用
现有 realpath 项目 key（超过 200 字符 key 不支持），不扩展 worktree/cloud/alpha
SessionStore/旧布局。不提供任意 transcriptPath 输入。父 ID 必须匹配既有 native
session UUID 校验；agentID 必须有效 UTF-8、1–255 bytes，拒绝 .、..、/、反斜线、
NUL、控制字符及驱动器分隔冒号。agentID 不是 UUID，保持大小写和 Unicode 原文，不做 NFC 或大小写归一；不同 ID 按原始 bytes 比较，实际文件别名按下述冲突规则处理。

先在声明项目范围内定位唯一 `<parentUUID>.jsonl`，跨项目冲突报 ambiguous。
要求该主源存在（空源允许）；有界完整语法/身份校验但不重建主链，源内可用
sessionId/session_id 必须等于所选父 UUID。随后仅扫描其旁
`<parentUUID>/subagents/`，包括任意普通嵌套子目录如 workflows/<runID>/。
目录位置不证明 parentAgentID，nested path 不等于 agent tree。

只识别 regular `agent-<agentID>.jsonl` 及同目录 `agent-<agentID>.meta.json`。
其余普通文件/特殊节点/不支持布局报 unsupported；不静默忽略。孤立 sidecar 报
corrupt。空 subagents/ 或父存在且容器缺失时 list 成功为空，get 为 missing。
同一 agentID 在任意两个相对路径、或两个候选由文件身份检查确认是同一个文件时报
ambiguous，绝不第一项获胜/去重。list 按相对 Path 的 UTF-8 bytes 升序，再切片；
get 即使已找到目标也完成有界库存扫描，证明唯一性，不只查顶层。

list 读取全部候选及 sidecar 并验证语法/身份（不重建每个 child 的链）；get 扫描
库存后只读主源、所选 child 和其 sidecar。内容中可用 sessionId/session_id 必须
匹配 parentSessionID，可用 agentId/agent_id 必须匹配文件 agentID；冲突为 corrupt。
不要求消息 UUID 是 native session UUID，仅要求非空字符串且唯一。

sidecar 是整个 JSON object；toolUseId、parentAgentId 允许省略/null/非空 string。
parentAgentId 若出现须满足 agentID 校验且不得等于自身；它不要求父文件存在。
sidecar 中 sessionId/session_id、agentId/agent_id 若出现，必须分别为匹配父 UUID/文件 agentID 的非空 string（null 为 corrupt）；其他未知字段原样保留。缺 sidecar 成功，两个 parent 值
unknown；损坏、权限失败、字段类型错误、来源身份冲突均返回错误，不降级成缺失。
SidecarStatus 和 Sidecar 原文区分 absent 与空 object。不会把 sidecar 历史关系
注册为 live 成员、创建事件或回答权。

## 子链选择与切片

单独的 child 算法：索引 user/assistant/progress/system/attachment（均需非空 uuid），
选择文件中最后一个 user/assistant 作为叶，沿 parentUuid 回到 null/省略，反转成
根到叶，仅返回该链 user/assistant，再 Offset/Limit。没有叶的有效源成功为空。
不按 timestamp 重排，不按 message.id 去重，不过滤 isSidechain/isMeta/teamName，
保留原文；不沿 compact_boundary.logicalParentUuid，不恢复主历史 parallel-tool
siblings/stray。重复 UUID、cycle、非空 parentUuid 找不到节点均为 corrupt（即使
在未选分支）；所有链记录先校验，再选择。未知 entry type 为 unsupported；既有
主查询已审阅的 metadata 及 api-request-shape/api-request-blob/api-request/cost-state
只做完整 JSON/可用身份校验，不进子链。未知 native message/content 字段仍保留。

Offset/Limit 非负；list 的零 Limit=100、MaxAgents=10000；get 的零 Limit=1000、
MaxMessages=10000。请求 Limit 超过对应 MaxResults 返回 resource-limit；库存
agent 数超过 MaxAgents 同样失败，get 索引与可见返回分别受 MaxRecords/MaxMessages
约束。Offset 超页尾及有效空源成功为空。无 cursor/nextCursor/total，不是 I/O 分页，
不会自动补齐/重试。saved content/Raw 不等于 wire delta/控制事件日志。

## 资源、错误与一致性

所有限制负数 invalid-input；零默认 MaxBytes=64 MiB、MaxLines=100000、
MaxRecords=100000、MaxLineBytes=8 MiB、MaxFiles=10000、MaxDirectories=10000、
MaxDepth=64、MaxSidecarBytes=64 KiB、Scope.MaxEntries=10000。限制按整次调用累计，
先限制再分配/读取/排序；深度以 subagents 根为 0。Files 包括主源、被读取 child
和存在的 sidecar；Entries 包括项目定位及递归库存中的全部目录项；Lines 包括
空行/metadata/sidecar 的物理行（sidecar 可无末换行），Records 包括全部已索引主/子
链条而非仅返回消息；Bytes 包括全部初读（主源+child+sidecar）。目录数含项目定位
中打开的目录及 subagents 根/嵌套目录，MaxDepth 只限制 child 库存遍历。
JSONL 每行完整 UTF-8 object 且末尾换行；重复 JSON key、无效 JSON/UTF、缺链身份
为 corrupt。sidecar 同样完整 JSON/无重复 key，并同时受 MaxLineBytes/MaxSidecarBytes。

复用 SessionQueryError.Kind：invalid-input、missing、inaccessible、corrupt、
unsupported、ambiguous、resource-limit、changed；保留 Path/Reason/Raw/Cause，
errors.Is 可见 OS cause，取消保留 context 错误。两 API 任意错误均清空 Records/
Messages（Info 也清空），仅保留实际 Coverage，绝不部分成功。unknown 是未定位，
partial 是已定位但未完成，complete 仅证明本次声明布局/读集合完成；不证明 running
或所有平台布局支持。MaxResults 只限制结果，不隐藏扫描超限。

显式配置根可解析其自身 symlink 后固定句柄；树内目录及文件拒绝 symlink，使用
os.Root containment、regular-file、no-follow/nonblocking 打开，不读 FIFO/device/
外部路径。检测目录/文件替换不能逃出 root。支持平台能力不足报 unsupported。

返回前重新验证发现目录的完整条目集合/类型/身份以及每个读文件的身份、size、mtime
和完整内容哈希；不存在的可选 sidecar/容器也重查。观察到 append/rewrite/replacement/
新增/删除/sidecar 变化均为 changed。验证读取至多初读字节的两倍（包括初读），
Bytes 只计初读；无锁，Atomic=false。最后验证之后写入、两次检查间完全恢复的
变更、根句柄外部改名不保证检测；跨文件/页/调用没有共同快照。无内部自动重试。
