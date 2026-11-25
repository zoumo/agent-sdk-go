---
type: Specification
title: Claude adapter 会话合同
status: stable
description: "规定 Claude adapter 的恢复身份和工具事件映射。"
tags: [claude, session, unified-agent]
---

# Claude adapter 会话合同

<a id="claude-resume-identity"></a>

## 恢复身份

Claude 的进程启动要求稳定 CLI >= 2.1.293。Core 使用与 Session
相同的 `CLIPath` 和由 `Config.Env` 与本次 `ProcessEnv` 合成的有效进程环境，
执行无会话副作用的版本查询；版本不能确认时，`Resume` 在启动 Session 进程前
失败关闭。原生 Session 启动同样执行版本门禁，因此新建 `Open` 也在启动前拒绝
旧版本、预发布版本和无法确认的版本。不能从 `SessionStart` lifecycle frame
或 init 是否出现推断 CLI 版本。新建 `Open` 不使用恢复探针，按
[创建规则](../../docs/session.md#core-接口)通过 claim 协议确认新身份。

Claude Core 的 `Resume` 要求构造配置显式提供绝对 `ConfigDir`；恢复预检和
复核使用该值，不从 `HOME`、环境变量或当前目录推断。原生只读探针及接收投影由
[Claude 只读身份探针](../../../client/docs/session.md#claude-status-identity-probe)维护。

`Resume` 在启动原生进程前完成 metadata 预检：

1. 用显式绝对 `ConfigDir` 和请求的绝对 `Cwd` 调用
   `claude.GetSessionInfo`。错误、`Info == nil`、coverage 非 `complete`、
   Session ID 不符、stored cwd 与请求目录不是同一目录，或 transcript 不是
   `ConfigDir` 下的普通文件时，返回确定的本地拒绝，原生 Session 进程不得启动。
2. 保存 transcript 的绝对 `Path` 和平台文件身份（与 Go `os.SameFile` 等价）。
   metadata 查询只证明保存材料，不证明 Session 存活、未被占用或可恢复。
3. 原样保留调用方 `Initialize.Hooks`；恢复流程不追加 SDK 私有 hook。

initialize 成功后，Core 在同一个原生连接上调用 `Session.ProbeIdentity(ctx)`：

- 探针通过现有控制请求相关机制发送唯一 `request_id` 的 `get_status`，只接受该
  请求的正常关联响应；不发送 user frame、上下文注入或模型请求，不重试恢复。
- 原生层从 `sections[].rows[]` 投影身份：全响应必须恰有一个非空 `Session ID`
  行和一个绝对路径 `cwd` 行。重复 JSON 成员、缺失/重复/类型错误的身份行或
  无效路径均失败关闭；不猜测标签、不截取 UUID、不把初始化 ACK 当作身份。
- 投影结果通过同一个 Receive FIFO 交付，先前已接纳的通知不会被跳过。Core
  要求 Session ID 精确等于预检目标，cwd 与请求目录为同一目录。
- status 的账户、provider、配置及其他行不进入投影、统一 Event、Meta 或日志。
  探针失败只保留错误类别，不回显响应正文。
- 已到达的 non-replay live `system/init` 必须匹配目标 ID 和 cwd；它不代替
  `get_status`。重复、回放或矛盾 init 在返回前拒绝，返回后通过 `Done`/`Err`
  终止 Session。status 先到时允许迟到的第一个匹配 init，并刷新原生元数据；
  不重置交付、关系或权限权威。
- `SessionStart` callback 及通用 `hook_started`、`hook_progress`、`hook_response`
  都不构成统一恢复的身份证据；统一层不注册或应答私有恢复 callback。

身份确认后、返回 Session 前，Core 必须再次用相同显式 `ConfigDir` 和请求 cwd
调用 `GetSessionInfo`，核对同一 Session ID、同一 transcript `Path` 和平台文件
身份、同一 cwd 与完整 coverage。比较文件身份，不要求文件大小、mtime 或 bytes
不变；正常恢复可以追加 transcript。metadata 缺失、coverage 不完整、文件身份
漂移或 cwd 不符时，关闭进程且不返回 Session。

错误分类固定为：预检无法建立目标或目录/文件前置条件时为
`failed_precondition`；版本查询无法执行时为 `unavailable`，版本不受支持时为
`unsupported`；探针超时、请求失败或断线时为 `unavailable`；已收到但形状无效、
歧义或身份矛盾的 status、init 或 metadata 为 `native_rejected`。关闭失败是附加
cause 与清理诊断，不覆盖主错误类别。任何失败均不新建替代 Session，也不自动
重试恢复或重发业务输入。

## 恢复身份验收

以下使用 fake peer、合成 metadata 和 wire 断言，不把合成通过计为真实 Agent
验证。

| 场景 | 公开操作与独立预期 |
|---|---|
| 预检拒绝 | missing、partial、ambiguous、cwd 不符和非普通 transcript 分别在启动前返回 `failed_precondition`；fake peer 未收到原生 Session 启动或 RPC。 |
| 匹配 status | 关联的 `get_status` 响应含唯一精确 ID 和同目录 cwd；无需 init/callback/user frame，metadata 复核后返回同一 Session。 |
| 仅 init 或 lifecycle | status 没有有效响应时，单独 init、callback 或 hook lifecycle 都不能完成身份；超时以 `unavailable` 关闭。 |
| 错误 status | ID/cwd 错误、缺失或重复身份行、重复 JSON 成员、相对 cwd 均以 `native_rejected` 拒绝；无关 request_id 不能完成探针。 |
| init 交错 | 早到和迟到 init 均核对 ID/cwd；回放、重复或矛盾均失败。status 先确认后，第一个匹配 init 可以刷新能力元数据。 |
| 私有数据 | 账户/配置等非身份 status 行不得出现在统一事件、Meta、错误或日志。 |
| 恢复后漂移 | 身份确认后 transcript 被替换、Path 或文件身份变化、coverage 不完整或 cwd 变化时，以 `native_rejected` 关闭进程且不返回 Session。 |
| 版本门禁 | 版本查询无法执行时返回 `unavailable`，结果不是稳定 CLI >= 2.1.293 时返回 `unsupported`；新建和恢复均在启动 Session 进程前失败。 |
| 调用方 Hooks | 原样保留调用方 hooks JSON，不追加 SDK 私有 matcher；原生可选 SessionStartProbe 的独立合同不变。 |

## 工具事件映射

Claude 的 `ToolName` 直接使用 `tool_use.name` 原文，不剥离 `mcp__`，不重新
拼接 server 或 leaf。

| Claude 工具 | `Kind` | `Provider` |
|---|---|---|
| `Read` | `read` | `agent` |
| `Edit`、`Write`、`MultiEdit`、`NotebookEdit` | `edit` | `agent` |
| `Bash` | `execute` | `agent` |
| `Glob`、`Grep`、`WebSearch` | `search` | `agent` |
| `WebFetch` | `fetch` | `agent` |
| `TodoWrite`、`TaskCreate`、`TaskUpdate` | `think` | `agent` |
| `ExitPlanMode` | `switch_mode` | `agent` |
| `Agent`、`Task` | `other` | `agent` |
| MCP `mcp__...` | `other` | 能可靠确定 server 时为 `mcp:<server>`，否则为 `mcp` |
| 其他未知非 MCP 工具 | `other` | `agent` |

Claude MCP 名称只有去掉 `mcp__` 后恰好存在一个 `__` 分隔符、两侧都非空时，
才能据名称确定 server；多个 `__`、空 segment 或畸形前缀一律保留完整
`ToolName` 并使用 `mcp`。如果有权威 server 列表能够确认边界，可以使用
`mcp:<server>`，但仍不得改写 `ToolName`。plugin/connector server alias 按原生
token 原样处理。
