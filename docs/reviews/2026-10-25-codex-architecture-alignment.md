> Historical evidence for the pre-removal baseline. Load and its contracts have been removed; this report is not a current API guide.

# Codex SDK 架构检查：以长期 Session 为中心重新设计

日期：2026-10-25。检查基准：`de8647df17e73d824561a7c4f94b949d82ab7f42`，并读取当前共享工作目录中未提交的 `harness/sdk/{architecture,user-stories,onboarding}.md`、ADR-0001/0002 及相关规则。本文是独立设计与检查，只检查 Codex；没有读取另一实现的检查报告，也没有修改 SDK、测试或合同。

## 结论与从零设计的目标

**建议替换对外的操作与观察组织方式，保留底层原生协议、传输、回答权限与历史证据机制。** 当前实现是一个相当完整的原生 app-server 连接客户端，加上可独立取消的递归观察器和另一条服务器请求通道；它还不是新合同要求的长期 Session 接口。问题不在缺少一个 `Session` 类型名，而在于主会话目标、事件顺序、成员资格、回答权限和资源释放被分配给了不同的公开对象，应用必须自行拼装它们。

先不考虑现有文件，一个应用需要的最小闭环是：建立或显式恢复一个主会话，持续读取其原生事件，随时提交原生输入，回答真实交互请求，修改原生配置，列举与观察成员，最后中断或关闭。执行结果只是这个长流中的原生事实。由此推导出三个独立的状态维度：

1. **交互状态**：连接可用、终止原因、清理是否完成。失联不等于执行结束。
2. **原生执行事实**：thread、turn、item、原生状态与结果，原样保留；不另造跨 Agent Busy/Idle 或通用 Turn。
3. **证据状态**：成员已确认、父级已知或未知、观察缺口、历史回放是否完成。列举记录、已订阅、活跃状态彼此不能替代。

第一天设计会让一个 Session 持有固定主 thread 身份和一个事件消费入口；连接是其可注入的内部资源，必要时可由显式的外部连接拥有者共享。应用只需知道一种正常使用路径，而不是先创建 thread、抢在执行前建立 Subscription、另起 Requests 消费循环、再另行管理 LoadedThread。

### 公共 API 概念草图

以下仅表达操作语义，不是兼容现有接口的 Go 实现提案。

```text
OpenSession(connection-or-owned-process, native-create-options) -> Session
LoadSession(connection-or-owned-process, native-identity, history-options) -> Session

Session.NativeIdentity() -> native main thread identity
Session.Receive(local-wait-context) -> Envelope or terminal error
Session.Submit(context, native input/options) -> native receipt
Session.SetModel(context, native model identifier) -> native change evidence
Session.SetThinking(context, native reasoning parameters) -> native change evidence
Session.ListSubagents(context, native query/page options) -> records + coverage + cursor
Session.Interrupt(context, native execution selector) -> native acknowledgement
Session.Close() -> owned-resource cleanup result
Session.Done()/Err() -> interaction termination, independent of unread backlog

Envelope = native notification | native interactive request + original handle
         | explicitly SDK-owned observation/connection/load evidence
ApprovalHandle.Approve(native supported options) / Deny(native supported options)
QuestionHandle.Answer(native answer structure)
Handle validity and ambiguous dispatch outcome remain inspectable
```

`Submit` 将主 thread ID 注入原生参数，不能从最后一个事件推导目标。原生 `turn/start` 与显式 `turn/steer` 保持不同操作语义；不以本地 Busy 标志选择、排队或重发。Codex 的 turn 标识保留在返回值、事件和 Interrupt 的原生选择器中，不必另建 Turn 生命周期对象。`SetModel` / `SetThinking` 是共同操作概念，参数仍为 Codex 原生值；同一命名不意味着引入根 generic client 或让两 SDK 相互依赖。

`Receive(ctx)` 的 ctx 只取消这次等待。channel 也能用 select 实现这个语义，但不应把 Session 的生命周期 ctx 当成读取 ctx。一个 Session 的审批与通知共用入口；请求是原生服务器请求，不能伪装成 notification。通用审批句柄是原始回答状态的原生适配，不是共享跨提供方的回答状态机。RPC `Reject` 不是用户拒绝审批。

Load 完成信息必须独立于 Session 的交互结束。推荐在同一消费入口标注 HistorySource 和 Load 进度/完成证据；保留历史和实时之间的证据差异，不宣称合并后的历史有原始实时顺序。若保留独立的有限回放对象，它只能是显式加载阶段，不能迫使应用长期消费两条执行流。实时到达与回放期间如何交错，应先按后文合同问题定案。

### 深模块与所有权

| 模块 | 隐藏的复杂度与拥有状态 | 依赖与测试 seam |
|---|---|---|
| 原生协议模块 | schema 版本、编码、未知字段/变体、原始 envelope、原生身份 | 标准库 types；schema 派生 round-trip 与真实录制回放分别验证 |
| 连接模块 | 初始化、读写、RPC correlation、dispatch 状态、进程或 socket、清理 | 注入字节传输/离线 peer；不含主会话调度策略 |
| Session 模块 | 固定主身份、单事件 FIFO、交互终止、所属请求与成员范围 | 依赖连接和原生解释；公开 Session 路径做行为验收 |
| 原生交互请求模块 | 原始 request ID、所属连接、原生取消/解决、唯一有效回答 | 连接保有权威 registry；Session 只交付同一个句柄；不从关系记录创建权限 |
| 观察证据模块 | membership 与可选 parent 分开存储、证据来源、覆盖缺口 | 不阻塞原生事件入队；可复用纯关系算法，但不能让树决定全部成员资格 |
| 历史读取模块 | 同身份 resume、分页、资源界限、历史来源、部分失败 | 只向 Session 交付历史证据；不执行工具、不回答历史请求 |

若 Session 自己创建 stdio 进程，它拥有关闭 stdin、限时退出/kill/reap 的责任；远程只拥有 socket，不拥有服务器及后代进程。若连接由外部显式共享，Session.Close 只释放自身注册/原生附着与句柄权限，不能顺便关闭其他 Session。共享连接不是默认必须公开的复杂性。

值得存在的抽象是：长期 Session、原生 envelope、原始回答句柄、纯 FIFO、可选父级的观察成员记录、有限历史读取。没有共同故事要求独立订阅者回放系统、完整 Agent 树、通用 Turn、自动重连、调度器、持久恢复、通用能力发现服务；不要为本次改造加入它们。

### 关键数据流及不变量

- **输入**：Session 固定主 ID → 原生参数校验/编码 → 连接写入 → native receipt；事件可早于 receipt，不能由事件倒推调用成功。执行中输入是否接受由原生决定。
- **控制**：模型/思考修改映射到原生 settings 操作，保留请求与通知；请求接受不等于活跃 turn 已切换，不遍历子 Agent。
- **事件**：连接按接收顺序解析 → 原生 source 与成员证据解释 → Session 唯一 FIFO。先交付当时可确认的原始事实，后续补充关系，不用关系等待队列重排事实。
- **审批/问答**：真实 reverse request → 原生 registry 创建句柄 → 同一 Session 流交付 → 应用稍后调用 → 按原结构回写一次。控制回复处理不依赖应用读流。无人回答不启动 SDK 审批倒计时。
- **子 Agent**：原生自动转发直接接收；有独立成员证据时可入观察范围，即使直接父级未知。只有 spawn/明确 parent 等证据建立父级。原生不支持纯观察补附着时报告缺口，不用 resume 模拟自动订阅。
- **列举**：显式原生查询所有相关页面，返回原生身份、可选父级与覆盖限制；不启动、恢复或订阅。列举结果不冒充实时活跃证据。
- **Load**：验证存储/身份 → resume 同一 thread → 完整保留的主历史分页 → 带来源的快照投影 → 明确成功或部分失败 → 继续正常 Session。没有新会话 fallback，没有历史回答权限。
- **结束**：原生工具/turn 失败继续作为事件；连接故障结束交互但保留已接收 FIFO；关闭资源不等待读完。消费端排空后才取得流结束。断线无自动 reconnect，也不能写成所有 Agent 已停止。

## 原生事实、实现选择与合同问题

### 固定原生依据

本次通过已安装的 `gh 2.92.0`（github.com 账户 zoumo）执行官方 GitHub contents API 的只读读取，全部固定为 Codex 0.162.1 源码提交 `092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`。没有执行 codex CLI、模型或上游测试。首轮猜测的两个旧文件路径返回 404，随后按实际目录及已有 provenance 定位到 `request_processors`；404 不作为能力缺失证据。

- [thread 原生参数](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/v2/thread.rs#L236)：`thread/settings/update` 的 model/effort 作用于后续 turns（L260–278），响应为空结构；L330 有 settings updated notification。不是只能借下一次输入或 resume 才能改模型。
- [设置拥有实现](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/turn_processor.rs#L931)：L948–982 将请求构造成 `Op::ThreadSettings`，提交后返回空响应。此结果不能证明活跃执行已切换；实验能力及字段限制仍需原生拒绝/通知证据。
- [后代查询参数](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/v2/thread.rs#L1394)：cursor、sourceKinds、archived 与 parentThreadId/ancestorThreadId；ancestor 明确任意深度、排除祖先本身，两个关系过滤互斥。关系字段有实验门槛。默认 source 过滤偏向 interactive，不能直接使用默认值声称列全子 Agent。
- [查询实现](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/thread_processor.rs#L2524)：L2574–2588 映射到存储 DirectChildrenOf/DescendantsOf；确有查询路径，不只是 schema 宣告。存储、归档与来源覆盖仍需在新操作中表达。
- [原生输入与 steer](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/v2/turn.rs#L167)：原生 turn/start 携带 input/model/effort；steer 是带 expectedTurnId 的独立操作。不要在共同层隐藏切换策略。
- [新 thread 自动附着](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/lib.rs#L1291) 与 [best-effort 实现](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/thread_processor.rs#L3607)：新建 thread 可向已初始化连接附着，lag 明确跳过 resync。不能承诺全量或冷历史 thread 覆盖；Go 不另发子 Agent RPC 本身不是 bug。
- [warm resume](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/thread_lifecycle.rs#L728)：新连接加入 thread，L830 重发 pending requests；这属于原生 resume，而非无副作用的通用订阅操作。现阶段不据此新增自动审批句柄恢复。
- [send_input 原生实现](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/tools/handlers/multi_agents/send_input.rs#L49)：receiver 是明确消息目标，L61 恢复检查传 `parent=None`，L113–127 的 sender/receiver 描述通信而非 spawn 父级。`CollabAgentTool` 还含 wait/close/sendMessage/listAgents 等，不能一律当父子边。

本次读取的重要源字节 SHA-256：thread.rs `553d99d01eed0c7a9073f481938f7dd68ef633879be0e1ffd8373778c937b158`；turn.rs `eb1a99cc72a65af84f933073881bfc0440288fb629de7ec92b7c1b50d3530779`；thread_processor.rs `2c40a6db590c9823a6775b28c2e125544583246318ce038166d28fb57a10ab29`；turn_processor.rs `50bc2dd50ba86c569a81abbe527000735ae90280dcaf8502e9e8c1641864f7b7`；thread_lifecycle.rs `967cbda6493c971232d5434af690e0c86a318589492d3c14344ee249a7267457`；lib.rs `b4c6b019d3ed6e5511abc997a725f466a0377c055ea09d2db8535f9275697c47`。仓库既有的 sources.json 继续是其他生成来源、许可证与哈希索引，未修改它。

### 尚需明确的共同合同或旧规范冲突

1. 新合同要求接收顺序、成员不依赖完整树；旧 `harness/contracts/event-delivery.md` 明确保留 unresolved 等待与既有重排，旧 subagent 文档承认无关事件进入流。不能同时把两种语义当成同一个验收目标，应以后续明确采纳的新行为更新旧规范，而非测试时选方便的一份。
2. `harness/architecture.md` 仍按每个 Codex subscription 一个队列/graph 描述资源，通用合同则排除独立订阅者系统。逻辑上可以保留底层连接调试能力，但正常 Session 路径是否继续公开多订阅，属于需要明确的交付面。
3. “单入口”是否覆盖 Load 阶段、允许如何交错实时与历史、Load 未完成能否控制原生正在执行的工作，需要补充验收。现有历史规范限定稳定主历史；新断线合同不要求活跃运行的无损恢复，因此 active Load 拒绝应先列为明确限制，而非直接定为错误。
4. 对已接收但尚未确认成员的外来连接事件，不能既要求它一定进入本 Session，又要求绝不混入无关事件。应区分连接接收与 Session 接收：未归属原始数据可保留为显式隔离诊断/原生连接扩展，不能充当成员事件。具体诊断承载方式须明确。
5. 成员证据能否由“同一会话内成功通信的目标”证明、证明多大观察范围，需要按各原生工具分别定义；即使确认 membership，也不能直接填 sender 为 parent。列表 ancestor 关系是成员证据，但 SDK 不应为缺失 parent 编造根边。
6. 动态内存 FIFO 已明确以可用内存为限，不保证 OOM 交付。这是当前已记录的取舍，不能擅自加固定丢弃/超时策略。若合同要求每次内存超限都可回报错误，则与 OOM 的物理限制冲突，需明确可承诺的范围。

## 影响架构的发现

### F1：缺少 Session 所有者，审批也没有进入单事件入口（合同不一致，高）

**证据**：`codex/client.go:47–75,196–220` 的 Client 同时管理多 thread RPC、loads 和 subscriptions；`codex/subscription_management.go:68–150` 以调用方提供 root 创建独立观察；`codex/requests.go:98,288–323` 把请求送入 Client.Requests；`codex/wire.go:112–124` 在 request 分支返回，根本不进入通知流。

**触发/后果**：应用只消费 Subscription.Events，会错过待审批请求；多个 Session 共用 Client 时，审批需要应用另做归属和分发。调用方每次 StartTurn 都要提供目标 ID，接口没有“本 Session 主会话”约束。Subscription.Close 只是本地观察关闭，也不终止该范围内原始回答权限（有专门测试保证旧语义）。

**根因/建议**：连接与应用 Session 的所有权没有形成深模块。新增 Session 时应合并正常事件/请求交付与权限失效范围，而不是在旧接口上再加两个用户需要协调的 callback。保留连接级 RPC 扩展，但明确它绕过哪些 Session 协调。请求原始 envelope 也应保留；当前 routeFrame 只把 ID/method/params 交给 receiveRequest，未知顶层字段没有原始副本。

### F2：额外审批超时使原生仍等待时失去回答能力（合同不一致，高）

**证据**：`codex/options.go:27–40,58–68` 默认 RequestTimeout 五分钟，零值仍归一化为该值；`codex/requests.go:316–323` 无人回答即本地 finish(ErrRequestExpired)，不发送原生取消。

**触发/后果**：审批交给人工超过五分钟，即使原生还有效，原始句柄也不能回答。没有“沿用原生等待”的默认/禁用路径；这不是原生拒绝，也不是应用明确策略。

**根因/建议**：把资源等待策略错误放进了 SDK 权限有效性。保留原生 resolved、连接终止与一次回答状态，移除默认本地审批倒计时；明确应用可自行放弃句柄。CallTimeout/WriteTimeout 控制本地一次操作仍可独立保留。现有 controlled-time 测试证明旧过期行为，应替换验收而不是把通过当作满足新合同。

### F3：树成为成员资格前提，通信边被写成父级（合同不一致/复杂度，高）

**证据**：`codex/subscription_management.go:369–413` 用 `lineage.Reaches(id, root)` 判成员；relationLocked 要求 child 和 parent 都非空；`:418–451` 递归先公告祖先；`:550–560` 对任意 completed collab 的 receiver 都调用 relationLocked(child, sender)，只有 created 标志区分 spawn；默认 MaxDepth=32（`:93–94`）。

**触发/后果**：独立证据确认属于主会话、但直接父级缺失时，没有记录成员的途径；深层事件必须等祖先链闭合。子 Agent 向已有兄弟/其他成员发消息时，会尝试把 sender 写成其父级，产生伪父级、冲突或错误可达性。原生通信目标不能证明直接父级。

**根因/建议**：把 membership、parentage、通信关系压成一张单父树。拆成有证据的成员集合和可选父级记录；只有相应原生证据写父级，通信事实保持原样。资源上限可以存在并报告，但不应把预设层数当共同模型。shared lineage 可保留为可选父级校验工具，不再是事件交付门卫。

### F4：关联等待改变接收顺序、泄漏其他 root 事件，结束时还会丢弃 pending（合同不一致，高）

**证据**：`codex/subscription_management.go:330–366` 将未知 source 暂存；后来的已知 root 事件直接交付；`:590–617` 超时后发 unresolved diagnostic 并无条件 deliver 原始事件；`:169–187` finish 直接清空 pending；`:196–201` disconnect 先入 disconnected 后 finish。

**触发/后果**：先收到 child delta A、再收到 root B、最后收到关联证据 C，会把 B 放到 A 前面；有 root X/Y 共用连接时，Y 的未知事件超时后进入 X 的流。若在 pending 超时前 Close 或断线，已被连接读入 pending 的 A 被清空，不会排空。诊断没有改变原始事件仍混入 Session 流的事实。

**根因/建议**：为“关系先于事件”建立的第二段暂存和重排流程，与单入口原生证据合同相冲突。先明确 Session 接收范围，再在首次交付时标注可确认来源与缺口，关系更新作为后来事实；不为完整树阻塞已属于 Session 的事件。关闭必须定义并保全所有已接受的 Session 原生记录，而不仅是已经进入 ChannX 的记录。

### F5：按实体键压掉 terminal 原生通知（合同不一致，中高）

**证据**：`codex/subscription_management.go:563–584` 对已知类型 item/completed，以 thread/turn/method/item 为键直接 return；不比较原始 payload、未知字段或原生 occurrence 标记。`codex/tree_nested_test.go:42–45` 主动把 terminal replay 去重当期望。

**触发/后果**：同 item ID 的第二次完成通知带新增字段、重复交付证据或不同 envelope 时，原始消息整体消失。原生实体身份相同并不能独立证明接收记录可丢弃。

**根因/建议**：将派生关系“只公告一次”与原生事件去重混在一条路径。只去重 SDK 派生关系公告；保留每个原生到达。若某方法原生协议确实要求 occurrence 去重，必须给出对应原生规则与验收，不能单凭 item ID 推导。

### F6：已有子 Agent 列举缺少正式操作（能力缺口，高）

**证据**：所有公开方法中没有 ListSubagents/ListThreads；`codex/history.go:148–156` 只有 thread turns/items 查询；`codex/docs/protocol/capabilities.json:1234` 将 thread/list 标为 unsupported。Subscribe.History 仅解析调用方提供的快照，不发现观察之前未知的子 Agent。

**触发/后果**：应用晚接入时无法通过 SDK 的正式操作发现当前流未见过的后代。raw Call 可以手写请求，但没有为查询覆盖、分页与原生门槛提供共同故事所需的已支持合同。

**根因/建议**：不是原生没有能力；固定源码已提供 ancestor 查询和存储实现。增加原生分页参数/响应与 Session 级 ListSubagents，显式纳入来源、归档/存储限制，不能只返回缓存或列举时偷偷 resume。

### F7：模型与思考修改被留给 raw Call/下一次输入（能力缺口，中高）

**证据**：`codex/client.go:196–234` 没有配置修改操作；`codex/types/types_generated.go` 有输入/创建时 model/effort 的选定类型闭包，未形成 settings update 的公共操作；capabilities.json:907 将 thread/settings/update 标为 unsupported。StartTurn 参数的透传不等于独立设置。

**触发/后果**：应用不能按共同控制概念在长期 Session 中修改模型或思考设置；只能手写实验 RPC 或借提交输入表达变更。缺乏明确的“后续 turns”作用范围和原生 settings 更新观察合同。

**根因/建议**：原生 RPC 客户端的选择性 wrapper 未围绕共同故事成闭环。增加原生参数、共同命名操作与原生通知证据。返回 requested/accepted/observed 必须可区分；不声称空 RPC response 表示当前 turn 已改变，不递归对子 Agent 发设置。

### F8：Load 的证据设计值得保留，但与长期入口/附着所有权分裂（设计复杂度/证据缺口，中）

**证据**：`codex/history.go:35–53` 是独立 LoadedThread/Events；`:77–109` Close 释放原生 attachment；`:112–145` typed StartTurn 与 Load 有协调门；`:213–229` resume 后回放；`:248–259,287–371` 检查身份、稳定性、分页与元数据变化；`:378–419` 带 HistorySource 投影，无历史回答权限。`codex/history_events_test.go:14–51` 明确同时维护 sub 与 h 两个流。

**触发/后果**：Load 结束不意味着 LoadedThread 可随手 Close：它还拥有恢复附着，Close 会 unsubscribe，随后交互的观察由另一个 Subscription 负责。generic Call 又不经过 beginThreadOperation，应用可能绕过 typed Load 协调。读历史与继续交互的使用规则分散在三个对象之间。

**建议**：保留分页完整性、同身份前置 read、HistorySource.Raw/MissingFields、未知 status 不合成 completion、部分错误与显式释放；把恢复附着与后续 Session 的所有权接起来。额外防御点：`:217–227` 没有直接核对 resume response 的 Thread.ID 等于请求 ID，虽然前后 read 检查了目标；应增加该公开路径拒绝错身份的离线验收，不能由后续对另一个目标的 read 代替 resume 身份确认。active/ephemeral 拒绝和非原子分页是已声明限制，尚无真实 Load 兼容性通过证据，不能据此宣称当前原生无法恢复活跃 thread。

### F9：验证套件与新共同合同脱节（测试证据不足，高）

**证据**：`tests/stories/acceptance_test.go:17` 只有旧 US-001～US-012；旧 US-003 检查子关系，旧 US-006 检查审批，与新 SDK-US-003“执行中补充输入”、SDK-US-006“子 Agent 输出”并不对应。`docs/recordings.md` 明确真实子 Agent 录制缺失；Codex S1 是 dynamic tools，S2 是用户问答，不是 command/file/permissions approval。

**后果/建议**：不能用“共同故事通过”证明本次 20 项已验收。建立新的 20 项逐条 public-path 验收与原生来源映射，保留旧协议回归中仍有价值的场景，替换与新合同相反的超时、重排、混流和去重断言。真实兼容性另行授权后验证，不能把合成子树或历史录制重新标成真实。

## SDK-US-001～020 逐项映射

“满足”表示在所列范围代码行为与合同一致且有离线证据，不表示真实兼容性已通过。“部分”表示必要基础存在但完整故事不成立。“缺失”表示没有正式受支持操作。原生事实和证据等级不合并计分。

| 故事 | 现状 | 公共接口与代码行为 | 原生能力/限制 | 验证证据与差距 |
|---|---|---|---|---|
| SDK-US-001 长 Session 多次输入 | 部分 | StartThread/StartTurn/Subscribe 可持续，turn/completed 不关流；无 Session 所有者（F1） | thread 与 turn 独立，保留 turn 正确 | 生命周期离线测试；历史两 turn live 是旧 SDK/schema，非当前新接口通过 |
| SDK-US-002 原生结构化输入/主目标 | 部分 | UserInput 原生 union；StartTurn 每次要求 ThreadID，无固定主目标 | 原生支持 text/image 等，不应提示词化 | 类型 round-trip/输入字段测试；主目标约束需新公开路径验收 |
| SDK-US-003 执行中补充输入 | 部分 | StartTurn 无本地 Busy gate；steer 只有 raw Call，不能声称正式支持 | 原生 start/steer 不同，保留其拒绝/调度 | 未找到新故事执行中多输入完整验收；类型/函数存在不证明原生接受 |
| SDK-US-004 提交/执行/工具结果 | 满足 | StartTurn 返回 RPC receipt；工具和 turn 通知独立，不拿 early completion 改响应 | 原生 turn/item 明确分开 | turn_contract_test、旧故事工具/结果分离；录制 S1 工具但不覆盖全后台语义 |
| SDK-US-005 未知/重复/原生证据 | 部分 | 通知 Raw/Params/Unknown 保留良好；terminal 压缩、重排、请求顶层 Raw 丢失（F1/F4/F5） | 不应由实体 ID 推断 occurrence 重复 | schema 750 合成案例与未知通知测试不能证明允许去重 |
| SDK-US-006 嵌套/并行子输出 | 部分 | graph 可处理有完整链场景；membership/parent 混用、无关事件可进入（F3/F4） | V1/V2 原生创建/父级事实不同，通信不等于父级 | nested/v2 合成测试；零真实子 Agent 录制 |
| SDK-US-007 根结束后继续子观察 | 部分 | 根 completion 不结束 Subscription；初始 incomplete/gap 已暴露 | native 新 thread best-effort auto-attach；冷成员无保证 | TestTreeParentIdleChildActive 合成；没有真实后台验证，不应伪造 resume 订阅 |
| SDK-US-008 查询已存在子 Agent | 缺失 | 无正式列举，History seed 不是查询（F6） | thread/list ancestor/source/archive/page 有能力和门槛 | inventory unsupported；未有 SDK 查询验收或真实验证 |
| SDK-US-009 统一审批入口/批准拒绝 | 部分 | 原始 Request/AnswerHandle 好；另一路 Requests，Answer(any) 与 RPC Reject 不是统一审批（F1） | 原生 decision/scoped options 按请求类型不同 | reverse request 合成、one-shot 测试；真实 S2 只问答，非审批 |
| SDK-US-010 延迟审批/有效性 | 部分 | reader 不等回答、resolved 精确 ID/thread；默认五分钟过期违约（F2） | 原生等待/解决应决定有效性，不应额外本地期限 | controlled-time 证明旧行为；需要超过旧超时时仍可回答的新验收 |
| SDK-US-011 原生交互问答 | 满足 | ToolRequestUserInput 参数+原句柄回答；普通输入不创建权限 | 问题 ID/答案结构属于原请求 | 真实 S2 录制相关问答；合成回答与权限隔离，不证明所有交互类型 |
| SDK-US-012 模型/思考操作 | 部分 | 创建/输入有原生字段，无共同独立修改操作（F7） | thread/settings/update 已有后续 turns 支持，实验门槛 | 参数 round-trip；无 settings workflow 正式 wrapper/验收 |
| SDK-US-013 生效时间/范围 | 部分 | 不递归改子 Agent、不伪造 observed 值；缺设置结果与通知合同 | settings update 接受不等于活跃 turn 切换 | 目前只有原生源码可支撑设计；无当前公开路径配置生效验证 |
| SDK-US-014 慢消费仍控制 | 部分 | ChannX 非固定队列，RPC 不等消费者；Requests 另有有限 admission（溢出显式失败） | available memory 上限，非持久队列 | backpressure/queue tests 离线通过；pending 重排/清空仍不满足完整保留（F4） |
| SDK-US-015 本地取消/未知结果 | 满足 | select 取消 Events 等待不取消订阅；调用/回答有 NotSent/OutcomeUnknown，无重发 | local wait 不等于中断，写入结果可能未知 | client timeout、answer race/dispatch 测试；Subscribe ctx 是观察生命周期，必须明确区别 |
| SDK-US-016 中断/关闭 | 部分 | InterruptTurn 独立；Client 只清理所拥有资源；Subscription.Close 仅观察，缺 Session 统一所有者 | WS/owned stdio 不同，不保证递归停止 | 离线 owned-child/remote close；历史 live interrupt 未触发，不能写成已验证中断 |
| SDK-US-017 失败/结束后排空 | 部分 | 原生失败不终止连接；队列能 drain，清理不等消费者；pending finish 清空（F4） | transport failure 与 native turn failed 分开 | delivery lifecycle/remote fault/历史失败合成；完整“已接收”需明确 admission 范围 |
| SDK-US-018 断线由应用决定 | 满足 | fail→disconnected/permanent-gap→Done/Err；无 reconnect loop/请求重放 | 远程断线不证明执行结束，原生 server 保持活动 thread | remote fault/epoch 测试与固定源码；无当前版本真实断线恢复 pass |
| SDK-US-019 显式 Load 同会话 | 部分 | read→resume→全主历史分页→Err/Done；继续 typed StartTurn 可用，但入口/所有权分裂与 response ID 检查需补（F8） | 有限保留快照，非实时日志；稳定历史限制已声明 | history/resume/events 离线通过；无当前真实 Load 完整回放证据 |
| SDK-US-020 回放与已存内容比较 | 满足 | HistorySource IDs/Raw/MissingFields、unknown status 保留，不创建原始实时 Raw/回答权限 | 不含未保留 token/progress/全部子历史；应用处理重复/缺口 | history_events_test 的未知字段、工具失败、interrupted/unknown status；未验证真实保留历史覆盖 |

## 现状到目标的推荐路径

按依赖排序实施，不以最小 diff 或旧 API 兼容为默认目标。每步都只在未来获得实现授权后执行。

| 顺序 | 保留/简化/移除/新增 | 交付行为与验证 | API 迁移影响 |
|---|---|---|---|
| 1 | 明确合同与验收编号 | 确认上述 6 个合同问题；建立新 20 项矩阵，分别指向 native/public-path/证据 | 文档应明确旧故事不是新编号别名；本报告不改合同 |
| 2 | 保留原生 types、transport、dispatch/原句柄；新增 Session 所有者 | 固定主 ID；open/close/done/drain 所有权测试；同连接多 Session 隔离；请求 raw 保留 | 应用从 StartThread+Subscribe+Requests 转到 Session；连接级 raw API 标为显式逃生口 |
| 3 | 替换交付/观察组织，简化 lineage | 单 FIFO 通知与请求；去掉关系等待重排与原生 terminal 去重；成员与父级分开；未知范围明确诊断 | 不再支持每个应用消费者独立 subscription；应用自行 fanout；依赖旧顺序/去重的代码需迁移 |
| 4 | 新增统一审批适配，移除默认审批倒计时 | approve/deny 保留原生 scope/扩展；无回答继续等待；cancel/resolved/close 和 ambiguous write 的竞争测试 | Reject 的 RPC 错误语义不改名冒充 deny；旧 RequestTimeout 默认行为有明确破坏性迁移 |
| 5 | 新增 ListSubagents 与配置操作 | ancestor 分页/空页/来源/归档/门槛/部分失败；查询零 resume/start；settings 接受与 observed 区分 | 用正式原生参数替换手写 raw RPC；model/effort 原生标识不映射成跨提供方档位 |
| 6 | 接入现有 Load 模块并简化所有权 | identity response mismatch、稳定历史/游标失败、并发控制、历史/live 交错与 close drain 公共验收 | LoadedThread.Close 不再是使用者必须猜测的附着释放点；旧双流消费需迁移 |
| 7 | 重新验证交付证据 | 当前 pin 离线/故障/race、fixture 哈希、生成/依赖边界、外部消费者；另行授权后真实多输入/审批/子 Agent/Load | 不将旧 API 历史 live 结果平移到新交付；更新能力表与示例后再声明支持 |

明确延期：自动重连、旧对象跨连接续接、句柄/订阅恢复、应用重启持久恢复、完整 Agent 树、通用 Turn、调度器及递归停止。这些不是消除上述缺口的必要条件。对于 native best-effort attach、分页非原子和已丢失通知，继续暴露缺口，不通过自动 resume 或补造事件填平。

## 本次实际验证与不确定性

执行命令（mise 的 Go 固定为 1.26.9）：

```sh
rtk proxy mise exec -- go test ./codex ./codex/types ./internal/queue ./internal/history ./internal/lineage ./tests/stories
```

结果：Codex 通过（30.377s）、types 通过（1.886s）、queue 通过（2.766s）、lineage 通过（0.679s）、共同故事通过（3.396s）；internal/history 无测试文件。仅运行这些有针对性的离线测试，没有运行 race、完整仓库 gates、外部消费者、真实 CLI/Agent/模型、上游 Rust 测试或新录制。命令通过证明这些包当前离线回归成功，不证明新 20 项通过。

未新增测试来复现 F1–F9：任务授权只写报告，各发现由具体控制流/状态归属和现有测试断言支持。F8 的 resume response ID 防御缺口是静态检查结论，未另写 hostile peer。原生版本事实固定在 0.162.1，不扩展为最新版本或支持区间。

真实证据严格限于仓库已有录制与已记录历史运行：S1/S2 不覆盖真实子 Agent、原生审批或 background complete；历史 live lifecycle 使用旧 schema/SDK 基础，不能证明当前绑定全部兼容。源实现确认有列举与配置处理路径，但实验门槛、权限、存储索引及实际部署行为仍需后续验证。当前共享目录有其他未提交设计文件，本次仅创建此报告，未 commit/push。
