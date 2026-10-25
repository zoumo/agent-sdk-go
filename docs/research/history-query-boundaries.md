# 历史查询核实与 Load 移除边界

核查日期：2026-10-25。目标仓库 `zoumo/agent-sdk-go`，基线
`fc395595d9b5eb95038fdbe1188aeb9578997052`。云端原有 checkout 是
`zoumo/codex-sdk-go`，此次未修改；目标独立取得于 `/workspace/agent-sdk-go`。

本轮实施范围仅为删除 Load。独立历史查询的共同合同与原生适配留待阶段二。
规范入口：[架构](../../harness/architecture.md)、[Session 接口](../../harness/modules/native-session/interfaces.md)。

## 已验证：Codex 官方当前 main

固定源码为 [`970111f6cb62cc02fe36a82ea7d71b37a2889c97`](https://github.com/openai/codex/commit/970111f6cb62cc02fe36a82ea7d71b37a2889c97)，
提交时间 2026-10-23。workspace version 是 `0.0.0` 占位，不能当成发行版本。
本项目绑定仍固定 Codex 0.162.1 / `092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`；
此次没有升级生成代码或宣称当前 main 能力适用于所有旧版本。

| JSON-RPC 方法 | 请求及响应 | 当前默认/限制 |
|---|---|---|
| `thread/list` | cursor、limit、sortKey、sortDirection、cwd/source/provider/archive 等过滤；返回 data:Thread[]、nextCursor、backwardsCursor，可选 readStates | 默认25，1–100；created_at/desc；section_position 默认asc |
| `thread/read` | `{threadId,includeTurns?:bool}` → `{thread,readState?}` | includeTurns默认false；无分页 |
| `thread/turns/list` | `{threadId,cursor?,limit?,sortDirection?,itemsView?}` → `{data:Turn[],nextCursor,backwardsCursor}` | 默认25，1–100；desc、summary |
| `thread/items/list` | `{threadId,turnId?,cursor?,limit?,sortDirection?}` → `{data:ThreadItemEntry[],nextCursor,backwardsCursor}` | 默认25，1–100；asc；省略turnId查整个thread |

定义：[list](https://github.com/openai/codex/blob/970111f6cb62cc02fe36a82ea7d71b37a2889c97/codex-rs/app-server-protocol/src/protocol/v2/thread.rs#L1415)、
[read/turns/items](https://github.com/openai/codex/blob/970111f6cb62cc02fe36a82ea7d71b37a2889c97/codex-rs/app-server-protocol/src/protocol/v2/thread.rs#L1723)、
[分页限制](https://github.com/openai/codex/blob/970111f6cb62cc02fe36a82ea7d71b37a2889c97/codex-rs/app-server/src/request_processors/thread_processor.rs#L6095)。

Thread metadata 包含身份、parent/fork、preview、cwd、时间、model/provider、CLI version、status、historyMode 等；
列表的 turns 为空。sourceKinds省略/空默认interactive，archived默认false，不能把默认列表称为全部历史。
见 [Thread](https://github.com/openai/codex/blob/970111f6cb62cc02fe36a82ea7d71b37a2889c97/codex-rs/app-server-protocol/src/protocol/v2/thread_data.rs#L204)。

read、turns/list、items/list handler直接调用ThreadStore，不要求resume。
查询需要连接app-server，本地可由SDK启动app-server进程，但不需要执行模型。
见 [read handler](https://github.com/openai/codex/blob/970111f6cb62cc02fe36a82ea7d71b37a2889c97/codex-rs/app-server/src/request_processors/thread_processor.rs#L3022)、
[turns handler](https://github.com/openai/codex/blob/970111f6cb62cc02fe36a82ea7d71b37a2889c97/codex-rs/app-server/src/request_processors/thread_processor.rs#L3277)、
[items handler](https://github.com/openai/codex/blob/970111f6cb62cc02fe36a82ea7d71b37a2889c97/codex-rs/app-server/src/request_processors/thread_processor.rs#L3638)。

关键边界：

- itemsView的full表示持久化app-server历史可用的全部ThreadItem，不是所有原始wire通知、delta或审批请求；summary不能当全文。见 [TurnItemsView](https://github.com/openai/codex/blob/970111f6cb62cc02fe36a82ea7d71b37a2889c97/codex-rs/app-server-protocol/src/protocol/v2/thread_data.rs#L386)。
- paginated thread的`thread/read includeTurns:true`全量hydration已弃用，推荐metadata + turns/items分页；turns full是兼容路径。见 [协议注释](https://github.com/openai/codex/blob/970111f6cb62cc02fe36a82ea7d71b37a2889c97/codex-rs/app-server-protocol/src/protocol/v2/thread.rs#L1723)。
- 本地items/list只支持paginated history且有state DB metadata。legacy返回`-32601`、`thread/items/list is not supported yet`。turns/list对legacy有fallback，每次读取重建整个rollout后分页。见 [存储条件](https://github.com/openai/codex/blob/970111f6cb62cc02fe36a82ea7d71b37a2889c97/codex-rs/thread-store/src/local/thread_history/read.rs#L203)、[fallback](https://github.com/openai/codex/blob/970111f6cb62cc02fe36a82ea7d71b37a2889c97/codex-rs/app-server/src/request_processors/thread_processor.rs#L3318)。
- cursor应当opaque；nextCursor:null表示当前无后页。backwardsCursor配合反向排序，turn anchor可重含以捕获更新。items支持`{type:"item",itemId}`排他anchor，但必须有turnId；后续用返回opaque cursor。见 [官方分页说明](https://github.com/openai/codex/blob/970111f6cb62cc02fe36a82ea7d71b37a2889c97/codex-rs/app-server/README.md#L612)。
- thread/list默认可能扫描JSONL修复metadata，不承诺绝对零磁盘写。`useStateDbOnly:true`禁止scan-and-repair；DB不可用返回错误。见 [官方说明](https://github.com/openai/codex/blob/970111f6cb62cc02fe36a82ea7d71b37a2889c97/codex-rs/app-server/README.md#L9)。

示意请求与响应形状（未对真实会话执行）：

```json
{"id":1,"method":"thread/read","params":{"threadId":"<id>","includeTurns":false}}
{"id":2,"method":"thread/turns/list","params":{"threadId":"<id>","limit":25,"sortDirection":"asc","itemsView":"summary"}}
{"id":3,"method":"thread/items/list","params":{"threadId":"<id>","turnId":"<turn>","limit":25,"sortDirection":"asc"}}
{"id":3,"result":{"data":[{"turnId":"<turn>","item":{"type":"agentMessage","id":"<item>","text":"..."},"startedAtMs":null,"completedAtMs":null}],"nextCursor":null,"backwardsCursor":"<opaque>"}}
```

精确字段以 [ThreadItemEntry/Response](https://github.com/openai/codex/blob/970111f6cb62cc02fe36a82ea7d71b37a2889c97/codex-rs/app-server-protocol/src/protocol/v2/thread.rs#L1834)为准。

## 已验证：Claude Code 官方 Agent SDK

固定官方Python main [`0e89b298e771259378a6cd3734e00d323dff5703`](https://github.com/anthropics/claude-agent-sdk-python/tree/0e89b298e771259378a6cd3734e00d323dff5703)，SDK0.2.174，bundled CLI2.1.310；
TS main [`019515d3268e6f94946eb2d8e9edc1ea79c5a6aa`](https://github.com/anthropics/claude-agent-sdk-typescript/tree/019515d3268e6f94946eb2d8e9edc1ea79c5a6aa)，最新CHANGELOG0.3.310。
另外静态检查并运行官方npm `@anthropic-ai/claude-agent-sdk@0.3.310`的合成fixture；
其integrity为`sha512-fo+sr+9Ks73P001f6XKRQlyzfIBAJMgXArLUkH5Tu98NikRDZeGtFQVj23d5/hj2ijpFgzffrr4fHywY9qUkoQ==`。

| 能力 | TS及首次版本 | Python及首次版本 |
|---|---|---|
| 会话列表 | `listSessions(options?):Promise<SDKSessionInfo[]>`，0.2.53 | `list_sessions(directory=None,limit=None,offset=0,include_worktrees=True)`，0.1.46 |
| metadata | `getSessionInfo(sessionId,options?)`，0.2.75 | `get_session_info(session_id,directory=None)`，0.1.50 |
| 消息历史 | `getSessionMessages(sessionId,options?):Promise<SessionMessage[]>`，0.2.59 | `get_session_messages(session_id,directory=None,limit=None,offset=0)`，0.1.46 |
| 子代理历史 | listSubagents/getSubagentMessages，0.2.89 | list_subagents/get_subagent_messages，0.1.60 |

版本证据：[TS CHANGELOG](https://github.com/anthropics/claude-agent-sdk-typescript/blob/019515d3268e6f94946eb2d8e9edc1ea79c5a6aa/CHANGELOG.md)、
[Python CHANGELOG](https://github.com/anthropics/claude-agent-sdk-python/blob/0e89b298e771259378a6cd3734e00d323dff5703/CHANGELOG.md)。
TS仓库无完整实现，类型和实现检查使用官方发布包 [sdk.d.ts](https://unpkg.com/@anthropic-ai/claude-agent-sdk@0.3.310/sdk.d.ts)、[sdk.mjs](https://unpkg.com/@anthropic-ai/claude-agent-sdk@0.3.310/sdk.mjs)。

```ts
type GetSessionMessagesOptions = {
  dir?: string; limit?: number; offset?: number;
  includeSystemMessages?: boolean; // 默认false，自0.2.89
  sessionStore?: SessionStore;     // alpha存储adapter
};
type ListSessionsOptions = {
  dir?: string; limit?: number; offset?: number;
  includeWorktrees?: boolean;      // 默认true
  includeProgrammatic?: boolean;  // 默认true
  sessionStore?: SessionStore;
};
```

列表按lastModified降序；消息沿parentUuid重建所选主链，根到叶排序，然后对可见消息应用limit/offset。
无cursor/nextCursor/total。Python先读取完整JSONL再过滤切片，不是I/O分页。
默认只user/assistant；TS可选system。message保留tool_use/tool_result等内容，外层不是原始JSONL entry，
不保证progress、hook、result、stream delta、所有附件等原始事件。主链过滤sidechain/team/meta；子代理另查。
Python明确不沿compact_boundary.logicalParentUuid跨回压缩前内容。
见 [签名与实现](https://github.com/anthropics/claude-agent-sdk-python/blob/0e89b298e771259378a6cd3734e00d323dff5703/src/claude_agent_sdk/_internal/sessions.py#L1284)、
[链与compaction](https://github.com/anthropics/claude-agent-sdk-python/blob/0e89b298e771259378a6cd3734e00d323dff5703/src/claude_agent_sdk/_internal/sessions.py#L933)、
[过滤](https://github.com/anthropics/claude-agent-sdk-python/blob/0e89b298e771259378a6cd3734e00d323dff5703/src/claude_agent_sdk/_internal/sessions.py#L1249)。

默认读取`CLAUDE_CONFIG_DIR`下本地JSONL，无需resume、不启动Claude agent runtime、不调用模型。
worktree发现可能运行只读git进程，不能说完全无子进程。
TS sessionStore、Python get_session_messages_from_store等路径可直接读adapter；
“仍读JSONL”仅适用于默认后端。见 [git发现](https://github.com/anthropics/claude-agent-sdk-python/blob/0e89b298e771259378a6cd3734e00d323dff5703/src/claude_agent_sdk/_internal/sessions.py#L387)、
[store查询](https://github.com/anthropics/claude-agent-sdk-python/blob/0e89b298e771259378a6cd3734e00d323dff5703/src/claude_agent_sdk/_internal/sessions.py#L2087)、
[官方sessions文档](https://code.claude.com/docs/en/agent-sdk/sessions)。
此处是本地Claude Code Agent SDK，未混用 [Managed Agents](https://code.claude.com/docs/en/agent-sdk/overview)云端session API。

合成实验：只读内存SessionStore，append/delete设置为抛错，显式隔离ConfigDir；
`getSessionMessages`返回u1、a1；offset1/limit1仅a1；system位于所选链中时，includeSystemMessages决定是否返回。
返回形状示例：

```json
{"type":"assistant","uuid":"a1","session_id":"11111111-1111-4111-8111-111111111111","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"fixture answer"}]},"parent_tool_use_id":null,"parent_agent_id":null,"timestamp":"2026-01-01T00:00:01Z"}
```

## 项目对照与阶段边界

删除前Codex Load：固定home/cwd/ID，thread/read → thread/resume → full turns分页，
投影成item/completed/turn/completed送入实时Session FIFO，LoadState控制普通输入门禁；
拥有进程启动、attachment、取消与清理等副作用。
删除前Claude Load：读取并索引JSONL，启动带--resume的进程，初始化后因缺少无输入确认而
ErrResumeUnconfirmed失败。它不是纯历史查询。

阶段一移除两端Load/LoadOptions/LoadState/ReceivedLoad、专属错误/门禁/协程、专属测试和合同。
保留Codex `Client.ResumeThread(ctx,types.ThreadResumeParams)`、UnsubscribeThread、
`ListThreadTurns`/`ListThreadItems`、Call支持的thread/read/list；
保留Claude Open+ResumeID及RequestedResumeID，保留已有有界transcript解析/投影基础及其独立测试。
mock/replay工具的同名Load函数只读取scenario/fixture，与Session Load无关，保留。

项目里的`harness/`实际是架构、术语、合同、共同故事与验收标准文档；不是Go运行时包。
`codex/`与`claude/`是独立原生SDK/agent适配，`tests/stories`是共同验收的原生测试adapter。
不应凭术语新增一个根通用client或统一历史wire协议。

阶段二最小建议（推断，未实施）：在harness定义独立查询的scope、原生返回值、分页/覆盖/错误边界；
由各原生包实现。查询不创建Session、不恢复/订阅、不注入Receive、不生成回答权限。
保留原生metadata、turn/item或message结构和未知字段；不要承诺完整原始事件或统一cursor。
Codex明确版本/historyMode和不支持的RPC；Claude明确JSONL/存储后端与链选择，不猜CLI control method。
多页无snapshot token，应声明可能变化，不能承诺原子历史快照。

## 验证范围与限制

官方接口结论基于固定commit定义、handler、存储实现、官方测试源码，以及Claude官方npm的合成fixture。
未编译运行官方Codex大仓，未对真实用户会话试验，未追溯Codex各查询接口的最早发行版本。
未验证稳定、独立的Claude CLI历史查询wire/control协议；已确认的是官方SDK库函数。
没有读取`/home/agent/.codex/sessions`，没有付费模型调用、新录制、推送或PR。
