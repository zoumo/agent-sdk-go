---
type: Specification
title: Claude 独立本地发现与 metadata
status: stable
---

# Claude 独立本地发现与 metadata

Claude 原生 package-level `ListSessions(ctx, ListSessionsOptions) (SessionsPage,
error)` 和 `GetSessionInfo(ctx, sessionID, SessionInfoOptions) (SessionInfoResult,
error)` 不需要 Client、Session 或 runtime。两者复用[消息查询](claude-session-messages.md)
的 `SessionQueryScope`、主源定位、普通目录/文件约束及 `SessionQueryError`。
不执行 Open、resume、control、订阅、模型、写入或 git，不改变 live 交付及回答权限。
应用按 list → info → GetSessionMessages 选择保存内容；metadata 与消息分别查询。

## 范围与值

必须显式指定绝对 ConfigDir。可选绝对 ProjectDir 使用消息查询的原生项目 key；
未指定时仅扫描 projects 的直接项目目录及其直接主源 `<UUID>.jsonl`。树内 symlink
拒绝。最多两层发现，不递归展开 worktree、子代理、旧布局或 programmatic 专用
存储；同一主源布局下的 programmatic/daemon 会话包括在内，不按入口过滤。
已知 `<UUID>/` 子代理容器与 regular `sessions-index.json` 不作为主源读取，未知
项目条目报告 unsupported，不以未知布局假装完整空列表。显式项目不存在报 missing。
本地后端不读取远端或其他用户配置；不提供 alpha SessionStore 或云后端。

`SessionInfo` 保留 SessionID、Path、FileSize、文件 LastModified（Go time.Time）、
可用 CustomTitle/AITitle、FirstPrompt、GitBranch、Cwd、Tag、Timestamp/CreatedAt。
Summary 是有界预览，优先最后非空 customTitle、aiTitle、lastPrompt、summary，
最后回退首条有效 user 提示词。FirstPrompt 只来自 head；排除 tool_result、
meta/compact summary/sidechain/team 与原生自动生成前缀，command 仅作后备。
仅首个有完整来源证据的记录将整个文件标为 sidechain/team 时排除该源；后续隐藏
分支不排除主源。有效空和没有已知字段的 metadata 仍返回身份/stat；无可用摘要
时 Summary 为空，不补造标题、cwd、时间或主链事实。Running 始终 unknown。

Head/Tail 是调用者所有的原始采样 bytes，可能含边界半行；Records 是完整采样
JSON object 原文，保留 unknown、null/省略、大整数和未知类型。所有返回值 detached，
不构造 Event 或 IncomingRequest。采样原文不等于完整消息或原始 wire 日志。

## 资源、排序及切片

ListSessionsOptions 含 Scope、Limit/Offset、MaxPage/MaxSessions、SampleBytes/
MaxBytes/MaxLines；SessionInfoOptions 只含 Scope 与采样字节/行上限。所有参数
非负，零值默认：Limit=100、MaxPage=1000、MaxSessions=10000、MaxEntries=10000、
SampleBytes=64 KiB（各 head/tail）、MaxBytes=16 MiB（每次调用累计初读字节）、
MaxLines=10000（完整采样行，包括空行）。发现范围/中间排序库存先受上限约束，
再按 LastModified 降序排序并应用 Offset/Limit；同时间以 Path 升序为 SDK tie
规则，与官方平台目录迭代 tie 不保证相同。保留重复身份并报 ambiguous，不去重。
无 native cursor/nextCursor/total。超页尾及完整空发现成功返回空 slice。

info 使用同一定位器，只采样所选文件；不会因单源 metadata 而解析全历史。
小于两倍 SampleBytes 的源只读一次连续内容；更大源跳过中段并设 Sampled=true。
分离尾窗额外读取其前 1 字节来判断首行是否完整；该探测计入 MaxBytes/Bytes，
随采样一起重读验证，不加入原始 Tail，也不提供 head 来源事实。首尾不完整
边界行不解析，Head/Tail 仍保留其 bytes；源末行缺少换行报 corrupt。
默认的采样窗口不构成完整链、内容可用性或整个文件有效性的认证。

## 错误、覆盖与一致性

Coverage 暴露 Status=unknown/partial/complete、实际 ConfigDir/ProjectDir、Entries、
Files、Lines、Bytes、有效 Limits 和 Atomic=false。complete 仅表示本次声明发现
及采样完成，Sampled=true 时依然有未读中段。列举可以同时返回部分身份/元数据和
error；单源采样失败也保留已得到的 Info。调用者必须同时检查 error/Coverage。
完整空、被明确排除（Info=nil 且无错）、missing、访问失败、corrupt、unsupported、
ambiguous、resource-limit、changed 与 context 取消分别可辨认。多故障以 errors.Join
保留原始 SessionQueryError/OS cause，不把 partial 截断为成功。

文件必须 regular，读取使用受限 root 和平台 no-follow/nonblocking 打开。采样后
重读同一首尾，比较内容、大小、mtime 及原根相对路径身份；观察到 append、采样
rewrite、文件/目录 replacement 报 changed。校验 I/O 至多初读字节的两倍；Bytes
只计初读，不计验证。未读中段同大小且恢复 mtime 的 rewrite、两次检查间完全恢复
的变更、最后检查后的写入、显式配置根自身外部改名可能无法检测。发现集合本身
不加锁，不承诺跨文件或跨页原子快照；调用者负责重试和范围选择。
