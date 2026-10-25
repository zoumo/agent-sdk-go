> Historical evidence for the pre-removal baseline. Load and its contracts have been removed; this report is not a current API guide.

# Claude Session 固定原生证据

日期：2026-10-25。对应 ticket 01「固定原生证据与 Session 接入清单」。本说明是原生来源调查，不是新 Interface 已实现或真实兼容性认证。目标合同见 [Session 设计](../../harness/modules/native-session/design.md)；本说明不能替代合同。

结论：固定 TypeScript SDK 0.3.258 的声明及 SDK 实现支持持续结构输入、动态 model/thinking 控制和只读子记录查询；没有在已检查的本地 stream-json 公开路径中确认无输入、无模型执行的 resume 身份回执。因此后续 Claude Load 不能把 initialize 成功、requested ID、磁盘记录或远端 transport 的身份 getter 当成本地恢复成功，缺少实际确认时必须明确失败。

## 1. 固定来源、许可与核验方式

主要来源是 [官方 npm 0.3.258 archive](https://registry.npmjs.org/@anthropic-ai/claude-agent-sdk/-/claude-agent-sdk-0.3.258.tgz)（下称 T）。该版本 manifest 声明 CLI **2.1.258**，commit `b3cd543a1f6fcdf4d8fabc0f5e5538d2ee7f38e1`，buildDate `2026-09-01T22:02:56Z`。这建立包与 CLI 目标版本的关联，不证明所有声明均被本机 CLI 实现。

辅助来源是 [官方 Python 固定提交](https://github.com/anthropics/claude-agent-sdk-python/tree/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab)（下称 P），其 `_cli_version.py` 目标是 **2.1.302**。P 只能辅助说明 SDK 机械逻辑或较新格式，不能证明 2.1.258 的运行能力。既有来源入口见 [type-sources](../../claude/docs/type-sources.md)。

| 实际读取的固定字节 | SHA256 |
| --- | --- |
| T archive | `656cf237bc567cb172a007a0fd5b3958cf960d154c03ab390a755d2c3bdbb398` |
| T `package/sdk.d.ts` | `b209ed293b63bb92bde59a49fe8a6e3b06f92afe0d1d4987c937a3cfea90b6dc` |
| T `package/sdk.mjs` | `4d9286bd9ca8f802e27c9be2cfa2e0769502dfabb693a6e3d16b62e4fbe3e69a` |
| T `package/manifest.json` | `ec53b2ce3d9f95d2c94c07aacf9202e6df9f79c695b71a1af2b4491081fba60f` |
| T `package/LICENSE.md` | `8ce94b9478bb9868f9641f818e06cd722fbe55d4c22e2d2ed11971b20146173a` |
| T `package/package.json` | `a8fb007bc424b11eca6a4f046f5e6bdf078c9f032a4c6f133f9bc5297e686a14` |
| P `_internal/query.py` | `0562517de949f4431177f204fe84cda3265f495d0eb0a949b9ced96cc5bd8276` |
| P `_internal/sessions.py` | `d5323963daf711c8bcc684057fd48d0cb786ee04d8329be6dec82444ec505662` |
| P `client.py` | `980f956d963338b39f4fd5224e69594eb513bfda37e5b215cf94bb0499ea047c` |
| P `_internal/transport/subprocess_cli.py` | `23c0b35acfcff5426255f1f17433e66fc7f8f6d5b8f339e93e085233c569e11c` |
| P `_cli_version.py` | `2fc8a1aad4b2bb7d598143a6edbe79229173c2a4be6f75acec2909f4bd4a6f4b` |

T **不是 MIT**，其 LICENSE 为 Anthropic 版权所有、保留全部权利，并指向服务法律条款。仅研究公开协议事实；没有把 SDK 实现、声明包或上游 fixtures 加入仓库，也没有将仓库 Apache-2.0 许可证套用于它。P 的 MIT 边界及既有 LICENSE 哈希见 type-sources；本次没有复制其实现。

外部来源用命令行核验：npm **11.19.0** 的 `npm view @anthropic-ai/claude-agent-sdk@0.3.258 dist --json` 返回上述版本的固定 tarball，`npm pack ... --ignore-scripts --pack-destination /tmp/claude-native-evidence --json` 的 archive SHA256 与表中一致。P 文件另经 `gh api repos/anthropics/claude-agent-sdk-python/contents/...?ref=... --jq .content` 解码逐字节回读，与初次下载文件哈希一致。临时上游文件只在 `/tmp`，不进入项目。

为便于在 minified `sdk.mjs` 精确定位，以下 offset 均为 UTF-8 解码文本的零起始字符位置，不是机器地址或字节偏移。请先核对 SHA256，再定位函数；名字只对这个固定包有效。声明行号按该文件 CRLF 分行计数。

## 2. 无模型执行的 resume 身份确认：证据不足

| 候选证据 | 固定来源与实际含义 | 能否确认本地恢复身份 |
| --- | --- | --- |
| launch `--resume=<id>` | T `sdk.mjs` local transport `initialize`，offset 910611 起构造参数；P [subprocess transport](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/transport/subprocess_cli.py) 同样传 resume 选项 | 否，只证明请求目标 |
| `initialize` success | T `sdk.d.ts:3915–4006`；`sdk.mjs:935887` 的 Query initialize 返回控制响应 payload。声明响应有 commands/agents/models/account 等，没有当前 session_id | 否；初始化能力或连接可用不等于指定历史已恢复 |
| `system/init.session_id` | T `sdk.d.ts:5055` 起说明每次执行开始附近发出会话身份等；字段位于 5116 | 这是运行时原生身份来源，但没有证明本地 CLI 会在无输入、无执行阶段先发它；不能为取证注入 prompt |
| `get_session_id` 控制 | 已检查 T sdk.d.ts/sdk.mjs 和 P client/query 的公开路径，没有找到此本地控制方法或 subtype | 不能实现一个猜测的请求，然后把回显、错误或超时当成功 |
| `getSessionInfo` / Python `get_session_info` | T 声明 758–784 明确读取单一文件；T `tZ` offset 1097023、`Ju` offset 1091985 做文件解析，SDKSessionInfo 的 sessionId 来源于查询参数；P [sessions.py:739](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/sessions.py#L739) 也是文件查询 | 否，磁盘或 SessionStore 历史记录不是当前进程身份 |
| `getSessionId()` | T `sdk.mjs` offset 1199856：这是 **DirectConnectTransport** 的 getter，返回 transport 存储的 sessionId；邻近 initialize 经远端会话创建过程设置该值并连接 WebSocket | 否，不是本地 stdio Query 的 resume 确认接口；不得引入另一远端运行模型来填补缺口 |
| P `get_server_info` | P [client.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/client.py#L549) 返回缓存 initialize 结果；[query.py:309–363](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/query.py#L309) 没有加强其身份含义 | 否；且 P 目标 CLI 比基线新 |

限定结论是“**这些固定公开资料没有建立可实现的本地无执行确认路径**”，不是对所有私有或未来 CLI 功能不存在的证明。本次没有执行 Claude，因此无法把缺省无输入行为补成观察事实。

后续实现建议：Load 状态显式从 pending 到 failed，错误分类为恢复身份无法确认（具体公开错误名由 Interface 设计固定），保留 requested ID 与初始化/历史读取诊断但不标记 confirmed。若实际原生身份不匹配，报告身份不匹配；原生拒绝、传输失败和确认超时分别保留原因。确认不足不能完成 Load、交付“已恢复”历史或开放继续 Submit；清理这次拥有的交互资源。磁盘只读查询仍可独立成功。不要启动输入获取身份、静默新建会话、自动重连或返回 requested ID 冒充观测值。

## 3. 原生输入接入范围与提交强度

T [`sdk.d.ts`](https://unpkg.com/@anthropic-ai/claude-agent-sdk@0.3.258/sdk.d.ts) 的 Query 参数（2913–2916）、`streamInput`（2881）、SDKUserMessage（5267–5316）证明同一输入流接收原生 user 消息。`message` 是 Anthropic MessageParam，声明注释明确 content 可为字符串或内容块数组，举例 text、image、document、tool_result。SDKUserMessage 还声明 priority、origin、shouldQuery、timestamp、uuid、可选 session_id 等原生字段。

后续接入应覆盖 **text、image、document、tool_result 及原生内容块扩展数据的保留**；内容块应使用 Claude 原生类型和 Raw 扩展，不制造跨 provider 统一输入类型。普通输入的外层身份由这次 Session 的主目标决定，不能允许任意传入 child session_id / parent_tool_use_id 将它变成子 Agent 操作。原生声明的 priority / shouldQuery 不是 SDK 调度保证，也不构成恢复身份确认操作；本 ticket 没有要求实现调度器。

需要区分 `message.content` 内给模型的 `tool_result` 与外层 `tool_use_result`（5276–5278）的结构化工具输出；两者都不是原生 IncomingRequest 的回答权限。读取普通 `tool_use`、重放历史、提交 user 消息不能授予 can_use_tool 的有效 request handle。

声明给出输入形状，SDK 源码给出写入路径；本次没有真实 CLI 接受图片、文档或各类扩展块的观察。完整本地写成功只证明字节提交到 transport，不证明模型开始、工具成功、CLI 调度时机或子任务停止。

## 4. 配置：wire、ack 与观察值

| 操作 | 固定 T 声明 | T `sdk.mjs` 实际 wire | 成功强度 |
| --- | --- | --- | --- |
| 动态 model | 2619–2625：streaming input 下用于 subsequent responses；省略 model 采用默认值 | offset 938940：`subtype: set_model`，字段 `model`；undefined 序列化时省略 | 等待同 request_id 的 control_response success；返回 void，不返回实际 model |
| 动态 thinking budget/display | 2626–2648：budget number 或 null（清除）；display 可省略、summarized、omitted 或 null（清除） | offset 939022：`subtype: set_max_thinking_tokens`，字段 `max_thinking_tokens`、`thinking_display` | 同一控制响应 ack；不返回实际配置 |
| 创建 thinking | 1741–1767、8454–8472：adaptive/enabled/disabled，enabled 可含 budgetTokens，adaptive/enabled 可含 display | local transport offset 910611：enabled+budget → `--max-thinking-tokens`；enabled 无 budget/adaptive → `--thinking adaptive`；disabled → `--thinking disabled`；非 disabled display → `--thinking-display` | 启动参数映射；不能当作模型最终采用配置的观测 |

T `sdk.mjs` offset 942303 的 Query `request(e,t)` 构造 control_request 和唯一 request_id，路由同 ID 响应；`success` resolve，其他 subtype 用原生 error reject；写失败也 reject。读取控制响应在 offset 929151 独立于普通消息交付。该实现证据比函数名更强，但仍是 SDK 发出的请求与 ack 机械逻辑，不是本次 Go/native CLI 验证。

T 声明指出 maxThinkingTokens 已弃用，Opus 4.6 按 on/off 解释：0 disabled，其他值 adaptive。不能将固定 budget 宣称为所有模型均严格执行的 token 上限。动态控制没有声明一个 `set_thinking` adaptive/enabled/disabled 完整 wire 操作；不能把创建 thinking 结构机械地序列化成猜测的动态请求。

后续必要 Interface 可接受 Claude 专属的动态 budget（含 null-clear）及 display，映射上述已证实控制；若请求无法映射的动态 adaptive 结构、effort 等范围，须明确 unsupported，不能隐式重启或把启动选项冒充动态支持。model 的 clear/default 应保留“字段省略”的 wire 语义，不从 P 的 null 写法跨版本推断等价。

配置结果需区分本地不支持/未发送、原生拒绝、原生 ack、已经收到的实际配置事实和提交后结果未知。ack 不证明正在执行的一次响应已切换，也不证明既有 child 或未来 child 继承；模型身份可由以后收到的原生 init/assistant 字段观察，thinking 的有效值没有在这条 ack 中返回。不要为确认配置额外触发模型响应。

## 5. 子记录查询、布局与 sidecar

T 声明 1037–1061 的 `listSubagents(sessionId,{dir?})` 明确是扫描记录，路径为 `~/.claude/projects/<project>/<sessionId>/subagents/agent-<agentId>.jsonl`。T `WZt`（1506485）在本地路径调用 EZ（1110545）；`yZ`（1107698）从已找到的主 transcript 路径去掉 `.jsonl` 再进入 `subagents`。`bZ`（1108436）递归读目录，只取 `agent-*.jsonl` 普通文件并递归普通子目录，因此嵌套目录也是固定 0.3.258 **实现**证据，不仅是较新 Python 注释。

T `xZ`（1110695）读取所选 transcript 旁的同名 `.meta.json`，提取 `toolUseId` 和 `parentAgentId`；`nR`（998357）投影到历史消息的 parent_tool_use_id / parent_agent_id。sidecar 缺失或解析失败被上游吞掉，因此没有 sidecar 只能报告 parent unknown，不意味着根级或无父级。T 的 list 方法本身只返回 ID，不返回父级；关系来自额外记录证据。

P [sessions.py:1199–1325](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/sessions.py#L1199) 同样递归发现，并举出 `workflows/<runId>/`；它辅助对照，不升级 T/CLI 保证。

原生 SDK 的宽松扫描会把目录访问失败折叠为空或部分结果，不能用于声称完整覆盖。后续 Go 查询应报告检查的根目录、部分结果、访问/解析失败及资源限制；拒绝无效身份和越界路径，固定 ConfigDir/cwd，避免把不同项目同名记录混入。发现只说明本次读取时存在记录，不证明 Agent 活跃、仍属于本次连接、可恢复、可写或拥有实时订阅。目录枚举不增加输入/配置/回答权限，不读取 output_file 冒充实时转发。

## 6. 非链元数据分类与证据缺口

固定 T 主历史 parser `hHe`（offset 994802）仅把类型 user/assistant/progress/system/attachment 且具有 string uuid 的条目加入候选主链。主链构建在 `gHe`，随后根据 parentUuid 与压缩保留片段处理。P [sessions.py:897](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/sessions.py#L897) 使用相同五类候选筛选。这直接证明下面四种顶层 type **不会成为这些 reader 的主链节点**，但不等于公开了完整持久化 schema。

| 顶层 type | 可证实事实 | 仍未知的边界 |
| --- | --- | --- |
| permission-mode | 不在 T 五类主链集合；P 483 行明确把它举作 metadata-only 第一记录的例子 | P 属于较新 CLI；T 未公开其完整载荷与字段校验规范 |
| ai-title | 不在主链集合；T `Ju`（1091985）和 `EKe`（1105632）使用 aiTitle 作为显示标题元数据 | aiTitle 字段不单独证明所有 `type: ai-title` 载荷合法 |
| file-history-delta | 不在主链集合；不能仅由名称断定字段语义 | T/P 已检查公开 reader 没有给出这种精确类型的完整 schema；尤其不能当作主链压缩重写指令 |
| cost-state | 不在主链集合 | 已检查公开 reader 没有给出该精确类型的完整 schema；不能把它当成实时 result/费用观测 |

本次对既有本机 `/Users/jim/bin/claude` 仅做文件哈希与字符串存在性检查，SHA256 仍为 `0d036f0b635d239844e6df6996bc05263fe305df505b073c71f79f83668ea35a`，未执行二进制。上述四个名称存在于字节中，但字符串不是 reader/writer 控制流证据，本说明没有据此确认其 schema、合法载荷或恢复语义。子串 `get_session_id` 的一个命中来自 `target_session_id`，同样不能证明存在控制 API。包 manifest 的 darwin-arm64 checksum 为 `b63136194160791c27cfa7b0403060d85eb0752991625fde8c09f9acacb17c78`，与本机文件不同，不能把两者当成相同构建。

后续兼容策略应把已确认的非链记录与主链完整性校验分开：原文、文件快照、记录偏移/哈希继续保留；允许的非链分类不变成历史原生消息事件，不改变 parentUuid/身份/快照约束。不要照搬上游对损坏 JSON 的静默跳过，也不要让未知 type 无条件绕过可能影响身份或主链的校验。四种类型的完整 writer/schema 是明确未解决证据项；若后续需要校验内部字段，须补固定版本来源，不能借此升级基线。

## 7. 验证边界

本次只做固定公开来源读取、CLI 查询及哈希回读；没有执行 Claude/Codex CLI、Agent、模型、工具任务、新录制、真实 Load 或真实动态配置。没有把上游源码放入仓库，没有修改原生 types、依赖或基线。本文没有新增 SDK 实现，因此没有运行 Go 测试。

既有 [Claude 架构检查](../reviews/2026-10-25-claude-architecture-alignment.md) 与 [compatibility](../../claude/docs/compatibility.md) 中的合成测试、认证失败录制及历史有界运行观察仍保留各自证据层级；它们不能自动补齐本说明的无执行恢复、结构输入实际接受、配置实际生效或子观察覆盖缺口。
