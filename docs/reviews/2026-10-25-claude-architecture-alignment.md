> Historical evidence for the pre-removal baseline. Load and its contracts have been removed; this report is not a current API guide.

# Claude SDK 架构检查与从零设计

日期：2026-10-25。检查基准：`de8647df17e73d824561a7c4f94b949d82ab7f42`，同时读取当前共享工作目录中尚未提交的 `harness/sdk/{architecture,user-stories,onboarding}.md`。本文独立检查 Claude，未读取另一套实现的检查报告。仅新增本报告；未修改 SDK、测试或合同，未执行真实 Claude CLI、Agent、模型或录制。

## 结论与目标架构

现有实现有值得保留的核心：一个长期存续的本地进程、持续输入、单消费者入口、原生 JSON 保留、独立控制路由、原请求审批权限、可排空的关闭和显式历史加载。它并不是必须整体重写的传输实现，但也不能称为已经满足新共同合同。最需要替换的是**把子 Agent 归因、原生事件去重、创建识别和运行账本串在所有事件交付之前的观察设计**，以及**以回调作为唯一回答权限入口的审批设计**。

从第一天设计，最小充分结构应当是：一个 Claude Session 拥有一个原生交互进程、一套原生请求关联状态和一个接收顺序事件入口；原生事件是事实源，成员及父级是可增补的证据；历史读取是独立的只读能力，显式 Load 再组合恢复与回放。应用拥有审批决定、输入时机、显示分发和任务完成判断。不存在公共通用 Turn，不需要以完整树、创建证明、最大 run_id 或统一 Busy/Idle 驱动 Session 生命周期。

当前阻碍共同验收的事项包括：审批句柄不随事件交付且默认自动拒绝、归因等待改变原生顺序、UUID 消息被静默抑制、子 Agent 列举缺失、动态模型与思考配置缺失，以及 Load 的“恢复确认”强度与新合同不一致。部分是实现缺口，部分需要先明确共同合同；不能通过把现有测试改名为 SDK-US-001～020 来解决。

## 一、由用户故事与原生事实推导设计

### 1. 必须维持的不变量

1. Session 存续由交互生命周期决定；一次 result、工具结束、根任务结束或 idle 均不关闭它。输入写成功仅证明本地完整写入。
2. 每个已接收的原生事件保留原始数据、原生身份和接收次序；缺少父级只影响关系知识，不阻止原生交付。SDK 诊断、关联和回答权限不能伪装成原生事实。
3. 普通输入始终送给这次打开或恢复的主会话。观察成员不获得输入、配置、恢复或回答权限。
4. 审批权限来源是实际请求及本次交互身份。它只能有效回答一次；取消、回答和终止影响同一个权限状态。无人回答时 SDK 不替应用作决定。
5. 读事件慢不阻止读取控制响应、撤销请求或释放进程。终止后可以排空，排空不是释放进程的前提。
6. 断线报告并终止本次交互，不自动重连、重发、恢复句柄或填补事件。
7. Load 只恢复指定原生身份并表达保留主链历史；历史不是重新执行，也没有实时回答权限。完成强度不能超过原生恢复证据。

### 2. 哪些由协议决定，哪些只是当前选择

| 事项 | 原生事实或限制 | 当前 Go 选择及评价 |
|---|---|---|
| 执行边界 | stream-json 有 result；后台任务可能在其后继续 | 结果不关闭 Client，正确；不需要额外公共 Turn |
| 输入 | 原生 user 内容可以是字符串或内容块；调度由 CLI 决定 | `Send(string)` 只保留文本是 SDK 范围选择，不能说原生只支持文本 |
| 主目标 | 一个本地交互进程对应其根会话；子事件含 parent/tool 等关联 | `Send` 固定根级封装正确，不应跟随最近观察成员 |
| 审批 | `control_request(can_use_tool)` 与 request_id 相关联，取消走 `control_cancel_request` | 回调池、默认拒绝和另一套交付渠道都是 SDK 选择 |
| 配置 | 固定官方资料有 set_model、动态 thinking 控制及创建时 thinking 类型 | 仅启动 Model、没有控制 setter 是实现缺口；具体时机与子级继承仍需版本证据 |
| 子事件 | initialize 可请求 `forwardSubagentText`；请求成功不是完整转发证明 | 启用转发、报告 unverified 值得保留；无需主动启动子任务来补观察 |
| 子列举 | TS 0.3.258 有 `listSubagents` 的本地文件发现；固定 Python reader 有嵌套目录及侧文件关系读取 | 现有事件缓存与调用者 History seed 均不能替代独立记录查询 |
| 未知数据 | JSON 消息有扩展字段及变体 | Raw/字段状态保留值得保留；将未知 run_id 字符串作大小排序不是原生事实 |
| 生命周期 | stdin/stdout/子进程是本地资源，平台进程组不等于原生 Agent 树 | 所有权与有界回收值得保留；不要承诺全部后台 Agent 被递归停止 |
| 历史 | transcript 是保留状态，不是实时流；初始化通常不提供已恢复身份 | 严格文件快照可作为 SDK 合同；“初始化成功即恢复已确认”需要另行说明 |

### 3. 从零设计的公共操作概念草图

以下是概念，不是现有签名的兼容性方案，也不要求新增通用根客户端：

```text
claude.Open(ctx, NativeSessionOptions) -> Session
claude.Load(ctx, SessionIdentity + StoreLocation + NativeSessionOptions) -> LoadHandle

Session:
  Send(ctx, NativeUserInput) -> SubmissionEvidence
  Receive(ctx) -> NativeEvent | IncomingRequest | RelationEvidence | ObservationGap
  SetModel(ctx, NativeModelID) -> NativeControlEvidence
  SetThinking(ctx, NativeThinkingChange) -> NativeControlEvidence 或明确 Unsupported
  ListSubagents(ctx, QueryScope) -> Records + Coverage
  Interrupt(ctx) -> NativeControlEvidence
  Close(), Done(), Err()

IncomingRequest:
  原始 ControlRequest + 绑定本次交互的回答句柄
  Approve/Reject 的共同操作概念 + Claude 原生 Answer 参数
  Context/有效性及终止原因

LoadHandle:
  同类型历史事件 + 完成/失败 + 恢复证据
  只有成功满足声明的恢复条件后才允许继续输入
```

`NativeUserInput` 必须保留原生内容块、调用者可用关联字段及扩展，不把图片转成提示词；默认主目标应由 Session 固定，不能开放一个未说明能力的“任意 agent_id”参数。无需为写入结果发明 CLI 接受回执：可以返回 local-write-complete 证据，或保留明确文档化的 error-only 操作。

统一的是操作概念与命名，不是在共享包里建立 Claude/Codex 共用回答状态机。原生 types 仍只依赖标准库；包含活权限句柄的 `IncomingRequest` 放在运行时包，且同时保留原生请求。`ParseEvent` 不能制造它，历史也不能制造它。AskUserQuestion 仅在真实可回答请求中使用原题结构、完整题文键及原生 UpdatedInput；assistant tool_use 本身没有回答权。

Load 是否继续保持独立历史 channel 和稍后取得 Client，可保留为两阶段接口，但必须把它与“每个 Session 一个实时入口”解释清楚。新设计可让 LoadHandle 以单个接收入口先提供历史、再提供明确加载边界和实时消息；这不是补回原始跨连接流。无论选择哪种，都不能让应用同时争抢两个实时消费者。

### 4. 深模块、依赖与状态所有权

| 深模块 | 必要职责与所拥有状态 | 不拥有的策略 |
|---|---|---|
| 原生编解码 | 原生消息、内容、未知字段、Raw、历史原文引用；纯函数 | 进程、审批有效性、成员树 |
| 原生交互 | 一个进程及管道、序列化 writer、控制 request/reply 关联、请求 epoch 和有效状态、终止原因及回收 | 展示、自动批准、重连、任务完成判断 |
| Session 交付与观察 | 一个 FIFO、原生接收次序、必要成员/别名/已知父级、覆盖缺口；关系变更作为独立证据 | 等齐父树后再放行消息、根结果推断全部停止 |
| 原生记录读取 | 指定 ConfigDir/cwd/session 的主历史快照和子 Agent 记录发现；路径、数量、大小及覆盖限制 | 恢复、订阅、执行、生成活句柄 |
| 显式 Load 协调 | 上述记录读取与一次明确恢复的组合、阶段门禁、历史完成及失败清理 | 自动断线补偿、持久恢复、旧请求复活 |

依赖方向为 Session/Load → 原生交互、记录读取、编解码；交互 → 编解码/FIFO；关系观察仅处理原生事实，可使用现有 `internal/lineage`。共享机械模块不认识 Claude 协议。测试 seam 是公开 Session 操作与可控的原生对端；不必建立任意传输插件框架，一个私有进程启动 seam 已能覆盖本地实际变项。

最小必要状态是进程生命周期、已发控制请求、活审批请求与有限重复请求防护、观察成员/父级证据和历史快照。主链图是历史选择所必需的，实时完整 Agent 树不是。`SubagentCreated` 是有条件的原生扩展便利，不是共同故事的交付前置条件；创建证据账本、跨版本猜测的 current-run 账本和 native UUID 去重账本都不应天然进入所有消息的必经路径。

### 5. 关键数据路径

- **输入：** 应用原生输入 → 主 Session writer → 完整本地写或已知未发送/结果未知；reader 继续交付所有后续原生结果。没有 SDK 自建排队、抢占或前台忙检查。
- **控制：** 注册本地请求 → writer → reader 直接相关联回复 → 控制调用返回原生证据；不等待 `Receive`。超时后不重发，迟到回复按原生关联规则处理。
- **事件：** reader 解析并保留 Raw → 同次接收附带可确认关联/缺口 → FIFO → 唯一 `Receive`。父级以后才明确时另发关系证据，不移动已收到的原生消息。
- **审批与问答：** reader 建立原生请求权限 → 同入口交付 `IncomingRequest` → 应用继续读、稍后回答 → writer 前重新核查有效性 → 一次原生 response。取消由 reader 立即失效，不等消费者。
- **子 Agent：** initialize 请求原生转发 → 接收已转发消息及真实成员依据 → 保留可用父级、未知部分及缺口；根 result 后路径继续。记录列举独立读取存储，不触发订阅、恢复或运行。
- **Load：** 验证指定本地主历史与资源范围 → 明确恢复一次 → 达到声明的恢复证据门槛 → 按主链回放并标记 History → 检查快照完整性 → 发布成功/失败。live reader 可继续工作；其审批不能由历史投影产生。恢复期间实时事件的保留与交付必须明确，不能被历史慢消费丢弃。
- **结束：** 传输故障/Close → 活权限失效、挂起控制醒来、回收自有资源 → 已接收事件继续排空 → EOF 或终止错误。应用若放弃排空，显式 Abort 释放 backlog；它是额外放弃操作。

## 二、影响架构的发现

### F1 · P1 · 合同不一致：审批权限在回调渠道，默认自动拒绝

证据：`claude/client.go:546` 注册请求后依据 PermissionHandler 分流；`claude/client.go:648` 的无 handler 路径直接发送 deny；`claude/types/events.go:206` 明确原生事件不是回答权限；`claude/permissions.go:378` 独立派发回调。

触发：应用只消费 Session 事件并打算稍后批准，或暂时没有人处理审批。后果：事件本身不能回答；无回调时 SDK 立即拒绝；有回调则需应用管理第二渠道，且可能在读取原始事件前已被回调回答。与 SDK-US-009/010 的事件流句柄和无人审批原生等待要求直接冲突。原生 request/reply 关联并不强制这种回调交付，这是 SDK 设计选择。

推荐：保留 `permissionState` 的单次权限、epoch、取消与写前检查，将其句柄随运行时请求事件交付；移除核心回调池和默认拒绝。旧回调如需短期迁移，应是应用侧消费适配器，不能与唯一事件消费者争用或形成第二份回答权。原生回答扩展也有缺口：`claude/permissions.go:242` 只实现 updatedInput 与 deny message，未提供原生 updatedPermissions、deny interrupt 等选项；不能声称完整保留原生授权语义。

### F2 · P1 · 合同不一致与复杂度：归因等待及 UUID 抑制支配原生交付

证据：`claude/tree.go:161` 为 `Receive` 必经路径；`:240` 对 duplicate 直接 continue；`:270` 暂存 unresolved；`:297` 遍历首个可释放记录；`:343` 建立 UUID 去重键，`:387` 只按该键抑制，不比较原文；`:878` 把“祖先已连到根”作为 child scope 条件。

触发 A：先收到缺少父级证据的 A，随后收到根事件 B。A 被 held，B 可先返回，最多等默认 5 秒才返回 A；后续关系消息也可先于 A。即使最终都交付，原生接收顺序已改变。成员事实与完整根路径被绑在一起，违背 SDK-US-005/006/014。`harness/contracts/event-delivery.md` 明文保留旧归因顺序，而新 `harness/sdk/architecture.md` 要求接收顺序，这是待裁决的文档冲突，不能称所有合同同时满足。

触发 B：两条相同 Session/来源/类型/UUID 的原生消息，第二条即使携带不同内容或未知字段，仍被抑制。固定声明给出 UUID 字段不等于赋予 Go 丢弃消息的权限；本次未发现足以支撑所有被去重类型的重复语义依据。当前合成测试固化了该选择，不能当原生证明。

附加后果：即便没有任何子 Agent，带 UUID 的根 assistant/user/result/stream_event 也进入同一 seen 账本；默认 8192 个不同键后经 `claude/tree.go:390`、`:944` 终止整个进程。资源失败是可见的，不能归类为隐藏 OOM，但一个额外消息去重便利功能成为长期 Session 的硬寿命上限，属于不必要的架构耦合。

推荐：保留原生消息，关系识别只增补证据；不等待完整树、不对事实流擅自去重。关系通知本身可按已发布状态避免重复，原生重复请求的回答权限防护仍必须保留。若特定版本确有必须去重的协议，按该原生规则明确限定，而非让观察图决定消息是否存在。

### F3 · P1 · 能力缺口：没有独立子 Agent 列举

证据：`claude/client.go:229` 起公开操作没有查询子 Agent 的入口；`claude/tree.go:34` 的 History 是调用者 seed；`claude/derived_events.go` 的 SubagentHistory 只有已验证 task/parent 关系；`claude/history.go:101` 仅读取一个主 transcript。

触发：恢复 Session 后查询本次观察前已存在的子 Agent。后果：只能依赖新事件或外部预先知道的 seed，无法完成 SDK-US-008。不是原生不支持：TS 0.3.258 `sdk.d.ts:1037`～1058 声明目录发现，固定 Python `sessions.py:1286` 起还涵盖嵌套目录。列表可发现身份，但不能证明活跃、转发已建立或完整父级。

推荐：新增只读记录查询，明确 ConfigDir/cwd/session、目录及版本范围、上限/分页或显式部分结果；可读取原生可用 sidecar 作为元数据证据。不要直接把观察缓存公开为“完整列表”，也不要为查询恢复子 Agent。

### F4 · P1 · 能力缺口：共同配置操作尚未存在

证据：`claude/client.go:30` 只有启动 Model，`:144` 仅加 `--model`；公开控制只有 `Interrupt`；`claude/options.go:10`/`:26` 也不携带 thinking。

触发：运行中修改模型或创建/修改思考设置。后果：SDK-US-012 缺失，013 也没有可确认控制结果与范围的公开表达。原生资料存在相应能力，不能归为 Go 已正确拒绝了所有不支持参数：目前这些操作根本不存在。

推荐：先按固定版本实现 SetModel 及思考操作的原生参数映射；已知不可动态映射的模式明确 Unsupported。保留 native ack，而不是把请求值写成当前生效值。TS 的 `setMaxThinkingTokens` 已被标记 deprecated，且部分模型上是开关语义，不能凭名称承诺通用 token 预算，也不能为了统一而映射到 Codex 档位。ack 的生效执行边界和子 Agent 继承范围仍须证据，不用中断、重建或遍历子 Agent 模拟。

### F5 · P1 · 合同冲突与证据缺口：Load 成功不包含已观察身份确认

证据：`claude/load.go:123` 的 before 回调在 `open` 返回后即放行；`claude/client.go:204` 的 initialize 回复只相关联，不验证 session ID；`:502` 只在后续有非空 root system/result ID 时校验；`claude/history_test.go:274` 后明确断言 Load 完成时 `SessionID()` 仍 unknown。

触发：一个对端初始化成功，但尚未发任何根身份事实；主历史回放即可全部完成并暴露 Client。后果：磁盘身份、请求 ResumeID 和初始化成功被组合为恢复成功，但尚无“实际恢复同一个原生会话”的身份观察。错误身份在后续到达时才失败，可能晚于宣布加载完成。

这里必须区分合同层级：旧 `harness/contracts/async-history.md` 明确允许初始化不暴露 ID，只要求握手与后续严格身份校验；新共同架构要求先确认恢复同一身份。这是既有规范与新目标的冲突，不能直接写成 CLI 本应提供不存在的初始化字段。

推荐：先确定恢复成功所需证据。如果固定 CLI 可在不提交新输入、不执行模型的条件下提供恢复确认，则等该证据再回放；否则把“已请求 resume、握手成功但身份未观察”明确表达为缺口，且不能宣称完整满足 SDK-US-019。禁止通过无意义 prompt 逼出 init 或伪造 SessionID。此次静态读取没有证明可用的无执行确认操作，仍需版本固定的进一步调查。

### F6 · P2 · 设计复杂度与证据不足：未知 run 字段被提升为有序执行语义

证据：`claude/tree.go:455`～468 用字符串大小判断 stale；`:1097`～1108 取最大 run_id 为 current；`claude/docs/type-sources.md` 明确 CLI 2.1.258 声明没有 run_id、parent_task_id、message agent_id；`claude/docs/subagent-events.md:153` 的“固定协议字符串顺序”在当前原生基线中没有对应依据。

触发：未知/后续版本输出不具备字典序时间意义的 run ID，例如先 `z` 后 `a`。后果：第二个运行被视为 stale，其父级和别名更新可被拒绝；这不只是 Raw 透传，而是用未知扩展控制关系状态。新字段编解码测试只证明可解析，不证明可排序。

推荐：没有固定协议排序证据时仅保留原始 run ID 与逐条原生状态，不定义 newest/stale；如果另一固定版本确有比较规则，再做版本专属解释。共同合同没有要求运行账本，可整体移除此部分并保留原生事件。不能为统一引入通用 Turn。

### F7 · P2 · 范围选择与接入缺口：结构化输入只读不写

证据：`claude/client.go:229`～238 的 Send 只接字符串并自行构造 user；`claude/types/events.go:245` 起及内容类型可读取结构化内容。TS 0.3.258 的 `SDKUserMessage` 明确允许 string/content blocks。

触发：应用需要图片、文档或已有原生结构输入。后果：无法通过公开输入操作保留它们。SDK-US-002 的“接入范围内”使文本子集本身不能简单判作数据损坏，但必须明确图片等未接入；目标架构不应让未来支持只能靠另造一个不同生命周期入口。主输入目标当前正确，`:238` 不随子事件变化。

推荐：用一个原生输入入口取代 text-only 核心，文本作为便捷构造。输入扩展由版本限定，不顺带承诺 steer/queue/priority 的调度效果。

### F8 · P2 · 历史兼容范围及验收证据不足

证据：`claude/history.go:147`～156 对非白名单 transcript 类型失败；`claude/history_snapshot.go:29` 的元数据白名单没有 `permission-mode`、`ai-title`、`file-history-delta`、`cost-state`；固定 Python reader `sessions.py:897` 起只保留已知链类型，其他记录不进入主链，并在 `:483` 明确提及 permission-mode 元数据。

触发：同一主历史文件包含合法非链元数据。后果：Go 在 resume 前失败，或追加此类元数据后失败；没有篡改主链仍可能无法 Load。它遵守旧 async-history 规范“未知类型失败”的严格选择，不应称为擅自违背旧规范；但新目标“完整保留主历史”的可用范围因此较窄。

推荐：区分主链完整性证据与非链元数据兼容策略。在固定版本格式基础上扩充已知分类，保留错误原文、文件快照、身份与链完整性校验。不要照搬官方跳过损坏行的宽松行为，也不要为了通过未知元数据而忽略可能影响链归属的条目。

证据体系还有独立缺口：`tests/stories/acceptance_test.go:17` 只有旧 `US-001`～`US-012`，编号语义与新 20 项不同，例如旧 US-003 是子关系，而新 SDK-US-003 是执行期间输入。这些通过不能直接映射成新故事通过。真实录制只有认证失败样本；文档另记录八个历史有界运行观察，其私有材料不属于可重放夹具，子 Agent 真实行为仍未验证。

## 三、SDK-US-001～020 映射

“满足”表示所检查公开行为与该目标相符，不表示完整真实版本认证。“部分”包括核心可用但关键验收缺口；“缺失”表示必要操作没有实现；原生资料与合成测试分开列出。

| 故事 | 现状 | 公共接口与代码行为 | 原生能力、差距与验证证据 |
|---|---|---|---|
| SDK-US-001 长 Session 多输入 | 部分 | Open/Send/Receive，result 不关闭；没有通用 Turn。必经 UUID 账本形成默认 8192 键上限（F2） | 原生流支持持续输入；resume_interrupt、Client、story 合成测试和历史有界普通回复观察支持基础行为；缺新故事公开路径持续验收及长期账本测试 |
| SDK-US-002 原生输入与主目标 | 部分 | Send 只收文本；固定主目标正确，不把最近子成员作为目标（F7） | 原生支持内容块；codec 测试只证明输出/类型保留，不证明结构输入公开路径；图片等未接入范围需明确 |
| SDK-US-003 执行中补充输入 | 部分 | Send 没有 busy gate，仅 writer 串行；未自建调度 | 只有本地写证据，不能推断 CLI 已接受或 steer；需多次输入在 result 前到达的公开合成路径及版本原生调度依据，旧同号 story 不对应本目标 |
| SDK-US-004 提交/工具/结果 | 满足 | Send 注释明确 local write；Interrupt ack 与 result 分离；原生 tool/result 各自保留 | 合成控制与结果测试；真实录制 result subtype success 仍 is_error:true，可验证失败不能冒充成功；后台停止不能由 result 推断 |
| SDK-US-005 原生、未知、重复 | 部分 | types.Raw、未知变体、字段状态保留强；tree 丢重复键、可能改序（F2） | schema/codec/receive 测试支持字节与语义保留的样本范围；去重语义缺原生证明，不满足所有已接收事实保留 |
| SDK-US-006 嵌套并行子输出 | 部分 | alias、tool owner、parent_task、lineage 处理丰富；缺根路径可 held；未知 run 排序影响关系（F2/F6） | 原生转发请求有依据；现代字段不都属于 2.1.258。tree_lineage/tree_bounds 等为合成，真实子 Agent 录制缺失 |
| SDK-US-007 根结束后子观察 | 部分 | result/idle 不结束，forwardSubagentText=true，初始 unverified、结束 incomplete 诊断 | 原生已经转发路径无需额外恢复；没有完整覆盖或独立追加订阅证据，不应发明订阅 API。父先结束合成通过，真实覆盖未知 |
| SDK-US-008 已存在子 Agent 列举 | 缺失 | 没有 ListSubagents；History seed 不是查询（F3） | 固定 TS/Python 提供文件发现依据；分页、目录覆盖、元数据/未知状态尚未设计和验证 |
| SDK-US-009 统一审批 | 部分 | 原生单次 Answer 可靠，但只能回调获得；事件无句柄、扩展参数不全（F1） | can_use_tool 原生依据充分；permissions 合成及历史有界 allow/deny 观察支持子集，不支持新的单事件入口验收 |
| SDK-US-010 延迟/无人审批 | 部分 | 配置 handler 可保留句柄且继续读；取消/终止/重复回答有状态防护；无 handler 自动 deny（F1） | 原生等待不应由 SDK 默认拒绝替代；延迟、取消、单次并发合成测试充分，公开事件流延迟回答路径缺失 |
| SDK-US-011 原生问答权限 | 部分 | AskUserQuestionInput.WithAnswers 保留问题与未知字段；只有真实 PermissionRequest 可 Answer，普通 tool_use 不授予权力 | 类型/权限问题测试与历史有界单选/自由文本观察支持映射；事件入口及更广原生交互子类未实现，不能以普通输入代答 |
| SDK-US-012 模型/思考修改 | 缺失 | 仅 Options.Model 创建时选择；无 SetModel/SetThinking（F4） | 原生有对应声明/操作；动态参数支持范围需固定版本解释，不能声称已明确拒绝所有不可用模式 |
| SDK-US-013 配置结果和范围 | 缺失 | 没有动态配置结果界面；启动请求未冒充观测 Model 值是正确的 | 原生 ack 不自动证明当前执行切换或子级继承；生效边界证据未建立，不能自建调度补齐 |
| SDK-US-014 慢消费及控制 | 部分 | 一个 ChannX backlog，控制响应/权限撤销不依赖消费，资源及 OOM 边界文档明确；tree 后处理改序（F2） | overload、queue、tree_bounds 合成覆盖；资源数量上限不是总内存上限；无真实慢消费完整性认证 |
| SDK-US-015 本地取消/提交超时 | 满足 | Receive 取消只退出等待；写前取消不发送，开始写后取消关闭管道并报告 OutcomeUnknown；不重发 | client.go:401、control:462、权限写前授权及取消合成覆盖；未知结果不等于未执行，真实模糊断线场景未验证 |
| SDK-US-016 中断/关闭 | 满足 | Interrupt 原生请求；Close 回收自有进程且不依赖消费；Abort 是明确放弃 backlog | Unix 进程组与其他平台不同；不保证全部原生子 Agent 停止或回滚。lifecycle/resume_interrupt 合成，历史 pending-question interrupt 观察仅证明该范围 |
| SDK-US-017 故障后排空 | 部分 | 原生失败不 fail Client；Close/传输失败保留 queue 并报终止；tree 会抑制部分已收到事件（F2） | recorded_replay 的 auth failure 有助区分原生失败；终止/排空合成覆盖强，受排序与去重缺陷限制 |
| SDK-US-018 断线交给应用 | 满足 | EOF/协议/进程故障 fail，唤醒控制，回收自有资源；没有主动重连 | 当前只拥有本地子进程，失联后的 cleanup 可能影响自有执行，不能把它说成外部远程断线绝不停止。无自动补齐/重发；故障测试支持 |
| SDK-US-019 显式 Load 同会话 | 部分 | resume 参数、ConfigDir/cwd 固定；主链回放与继续 Send 门禁、失败清理；完成时允许原生身份未知（F5） | history 测试明确覆盖当前较弱握手合同；新“先确认身份”未满足且原生确认能力待查；严格元数据限制（F8）；无真实 Load 完整认证 |
| SDK-US-020 历史对比与缺失 | 满足 | HistorySource 保留原 transcript、UUID/parent/time；Event.Raw 是投影；不通过实时 reducers，不产生句柄 | history_events/branch/snapshot 合成支持；只承诺保留主链，未恢复 delta/全部子历史；原始实时重复与缺失由应用判断 |

本表没有把“原生资料有声明”升级为“Go 路径可用”，也没有把四个离线包通过升级为 20 项验收全部完成。001/003/007/013/019 等的真实调度、覆盖或恢复强度仍有独立证据需求。

## 四、合同中需先明确的事项

1. **Load 的确认门槛。** 新共同合同的同身份恢复确认，与旧规范允许 SessionID unknown 的握手语义不能同时作为无条件成功定义。先确定原生可取证门槛；没有证据就保留缺口，不注入 prompt。
2. **接收顺序。** 新共同合同与 `harness/contracts/event-delivery.md` 的旧 unresolved 归因排序存在实际冲突。推荐明确以原生接收顺序为准，让关系后续增补，不自行改合同。
3. **输入接入范围。** SDK-US-002 的“接入范围内”允许文本起步，但应列出必需的内容类型；否则文本-only 可无限被解释成完成，无法验收新增 Agent 的结构输入能力。
4. **配置共同操作。** 应固定共同操作命名及创建/动态修改、unsupported、native ack、已观察实际值的区别；参数仍保留 Claude 原生类型，不引入统一 reasoning 档位。
5. **请求交付形式。** 如何随流携带句柄而不污染 `types.Event` 原生 JSON，需要运行时事件合同；是否保留旧 callback 作为兼容层应明确其唯一消费者及权限所有权。
6. **列举覆盖。** 新合同允许只读记录发现，不能反向要求它证明存活或自动建立可重放订阅。目录扫描不完整、权限失败及资源上限必须显式表示。

## 五、推荐迁移路径

### 第 1 步：固定验收与证据语义

先处理上述合同冲突并建立独立的 SDK-US-001～020 映射，保留旧 US 编号的原测试含义。为每项列固定原生版本、公开操作、成功含义和未验证范围。报告本身不修改这些合同。

验证：文档间的 Load、顺序、审批、结构输入范围逐项核对；验收测试调用公共 seam，不允许从私有树状态反推出“公开用户已看到什么”。这一阶段不需要改变 SDK API。

### 第 2 步：替换事件必经归因设计

保留 native codec、一个 FIFO 和本地生命周期；简化观察状态为成员、别名、已知父级及覆盖缺口。移除对 native 事实的 UUID 过滤和按归因重新排序。创建识别可隔离为有版本约束的扩展；未知 run ledger 应移除或停用解释。

验证：先收到无父级 A 再收到根 B，公开 Receive 仍 A→B；后续关系只丰富知识；同 UUID 不同 Raw 以及完全相同的原生重复均按原生规则保留；无子 Agent 的长会话不因观察去重键耗尽而被杀；关系超限应按约定报告并保留已接收事件；Close/错误后完整排空。当前固化去重与关联先行的测试需要按裁决后的合同重写，不能只调预期使旧实现通过。

迁移影响：应用将能看到原来被过滤的消息，且不能依赖“关联通知一定先于全部子消息”；需以原生事件身份和后续关系证据做展示。`SubagentCreated` 和 current-run convenience 字段如移除属于公开破坏性变更，应集中升级，不保留两套长期观察机制。

### 第 3 步：迁移审批到唯一事件入口

保留原请求状态机和单 writer 授权检查；新增携带原生请求与同一权限句柄的运行时事件；移除核心默认 deny 与 handler workers。扩充 Claude 原生支持的 Answer 选项及显式不支持校验。

验证：不配置回调仍可从流中得到句柄；持续读并延迟回答；长时间无人回答不产生 SDK 决定；原生取消即时失效；回答、取消、关闭并发及模糊写出结果不重试；历史和 ParseEvent 不能生成权限；工具观察和普通输入不能代答。用原生协议约束的合成对端贯穿公开操作，现有权限状态测试继续保留。

迁移影响：应用由 handler 改为事件类型分支，可自己派发 UI/工作队列。不能让旧 handler 与新流独立创建两个有效句柄。无需跨 provider 共用回答状态机。

### 第 4 步：补输入与配置共同能力

新增原生结构输入；文本便捷方法复用同一 writer。按固定 CLI 映射创建与动态模型/思考配置，返回原生确认强度，明确不支持项。不要把一般 settings 管理、MCP 管理等顺带纳入当前任务。

验证：内容块和未知输入扩展往返、主目标不变、执行期间提交不被 SDK busy gate 拒绝；配置成功/原生拒绝/本地不支持/超时未知；设置调用不隐式中断或重建；请求值与观测值分离。真实生效时机与子级范围列独立未验证项，后续须另获授权执行。

### 第 5 步：新增只读子记录发现，整理 Load 协调

将主历史和子记录发现共享必要的路径/身份校验机械逻辑，但保持两种查询的证据语义。先补 ListSubagents；再按已裁决的恢复门槛调整 Load。保留主链选择、逐记录哈希、冻结前缀、资源上限、异步错误与 Close 清理；针对原生合法非链元数据做有依据的兼容。

验证：查询能发现本次观察前记录、嵌套目录、未知父级、访问受限和部分结果，且零恢复/订阅/输入；Load 错误身份、初始化但未确认身份、主链完整、未知元数据策略、历史/live 区分、无历史工具重放/活权限、慢读期间实时控制、取消和失败清理。输出 API 可能新增覆盖和恢复证据；旧 Done 的成功含义若增强，应明确迁移。

### 明确延期

不新增通用 Turn、完整 Agent 树、自动重连、同一对象跨连接续接、自动补洞、旧句柄恢复、调度器、持久恢复或通用实时能力发现。后台输出若原生不转发，报告覆盖缺口；不能读取输出文件、恢复子任务或重跑来冒充实时观察。真实 Agent 验证及新录制不属于本次授权。

## 六、实际验证与原生来源

### 本次执行

使用 mise 固定 Go 1.26.9，执行：

```sh
rtk proxy sh -c 'GOMAXPROCS=2 CLAUDE_E2E=0 SDK_REAL_AGENT_E2E=0 mise exec -- go test -p=1 -count=1 -timeout 180s ./claude ./claude/types ./internal/queue ./tests/stories'
```

结果：全部通过。`claude` 9.711s，`claude/types` 0.609s，`internal/queue` 0.705s，`tests/stories` 0.985s。执行的是既有离线单元、合成子进程及录制回放路径，不是真实 Agent 兼容性认证。未运行完整仓库、race、vet、外部已推送消费者或真实 e2e；此次只新增报告，没有 SDK 变更需要验证发布产物。

只读检查了当前 HEAD、dirty 文件列表、公共操作、路由/权限/关系/生命周期/历史实现及相关测试。未读取个人历史或认证配置。原生资料通过命令行 `curl 8.7.1` 按固定提交/包版本读取，并在内存计算 SHA256；没有启动 Claude、使用浏览器或把上游实现写入仓库。

### 本次实际读取的原生一手资料

| 来源 | 版本、哈希与用途 |
|---|---|
| [官方 Python query.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/query.py) | commit `f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab`；SHA256 `0562517de949f4431177f204fe84cda3265f495d0eb0a949b9ced96cc5bd8276`，与仓库来源表一致。initialize 转发请求、原生控制/撤销、can_use_tool 回答、set_model，以及 result 后后台工作说明。此 Python 源码的目标 CLI 是 2.1.302，不能当成 2.1.258 全能力运行证明 |
| [官方 TS sdk.d.ts 0.3.258](https://unpkg.com/@anthropic-ai/claude-agent-sdk@0.3.258/sdk.d.ts) | SHA256 `b209ed293b63bb92bde59a49fe8a6e3b06f92afe0d1d4987c937a3cfea90b6dc`，与来源表一致；包 manifest 的 CLI 目标为 2.1.258（来源记录，未重新核验二进制）。结构输入、setModel/setMaxThinkingTokens、thinking 类型、listSubagents 及历史关系字段。声明证明表面合同，非 Go 实测 |
| [官方 Python sessions.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/sessions.py) | 同一 Python commit；本次 SHA256 `d5323963daf711c8bcc684057fd48d0cb786ee04d8329be6dec82444ec505662`。链条目筛选、permission-mode 元数据、嵌套子目录发现、parentAgentId/toolUseId 侧文件证据；不声称与 TS/CLI 2.1.258 每个细节相同 |

TypeScript 包不是 MIT；本次仅引用协议事实与行号，未复制其实现。许可证和原始取证范围继续以 `claude/docs/type-sources.md` 为准。官方 SDK 的回调默认行为不自动成为本项目共同行为合同。

### 真实证据和不确定性

- checked-in `claude/tests/testdata/recordings/real-sanitized/s1-auth-blocked/manifest.json` 固定 CLI 2.1.258 与哈希，明确 authentication-blocked、零 child、零 permission callback、无成功模型完成。它只证明保存样本的失败/交付路径，不证明子 Agent 或审批成功。
- `claude/docs/compatibility.md` 的 Bounded runtime coverage 另记 2026-10-18 八个输入的普通回复、一次历史 continuation、问题/权限与中断观察。必须承认这些历史观察存在，但本次未重跑、无法把未公开私有捕获提升成可重放证据，也不能延伸到所有平台或最新版本。
- nested/parallel、背景子观察、完整覆盖、run 关系、长会话去重边界等现有主要证据是源码与合成测试。没有真实子 Agent 捕获；不能以 `provenance: live` 把合成流标为真实运行。
- Load 恢复确认、动态配置生效范围、执行期间输入实际调度，以及未转发后台内容的可观测能力仍有原生证据缺口。它们应进入固定版本能力清单，不能靠跨版本字段、函数名或测试整体通过推断。

推荐先收敛“事实流、权限、关系知识”三者的责任，再补共同操作。这样可以保留已经有价值的传输与编解码实现，同时消除当前将便利账本变成 Session 前置条件的主要复杂度。
