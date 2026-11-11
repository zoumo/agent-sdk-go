# Agent SDK 共享术语

本表定义原生 Agent SDK 的会话、观察、权限、历史查询和验收用语，用于区分相近概念。中英文名称表示同一术语，`Avoid` 列出在该含义下容易混淆的称呼。

## 术语

### 会话与执行

**Session（会话）**:
跨多次输入和执行持续存在的原生交互上下文。一次执行结果是会话内的事实，不是会话本身。
_Avoid_: 任务、单次执行、Turn（指整个 Session 时）

**Execution options（执行选项）**:
Session 级、可由多个 provider 共同解释的模型与推理 effort 配置。它们可以在打开、
恢复或运行时请求修改；请求、被原生接受和实际生效是不同事实。
_Avoid_: 全部 Session 配置、权限设置、跨 provider 思考预算

**Process environment（Provider 进程环境）**:
构造 Provider 时选择的 Codex app-server 或 Claude CLI 基础子进程环境。nil 继承
当前进程，非 nil 整体替换；Claude 的 Session overlay 另由 `ProcessEnv` 表达。
_Avoid_: ShellEnv、全部 Session 配置

**Session process environment（Session 进程环境，`ProcessEnv`）**:
Claude Session 自有 CLI 进程的 additive、set-only 环境 overlay；以 Provider
进程环境为基础，在 `Open`/`Resume` 时合成。它影响该 CLI 及其子进程；Codex
共享 app-server，不支持 Session 级 `ProcessEnv`。
_Avoid_: ShellEnv、per-prompt env、Codex app-server 环境

**Session shell environment（Session shell 环境，`ShellEnv`）**:
Session 创建或恢复时声明的 additive、set-only 环境覆盖，只影响 provider 启动的
shell / unified-exec 类子进程。本合同规定 Codex 支持；Claude 因没有 shell-only
入口而明确不支持。
_Avoid_: `ProcessEnv`、Claude settings `env`、任意子进程环境

**Provider-native behavior（Provider 原生行为）**:
由 Codex/Core 或 Claude CLI 自身定义并执行的行为。SDK 可以映射输入并接收观察
结果，但不拥有、模拟或保证其 span 创建、turn trace 复用、进程环境继承和并发
隔离。
_Avoid_: SDK 合同、adapter 保证、跨 provider 一致语义

**Prompt trace context（Prompt 追踪上下文）**:
通过 Go `context` 与一次 `StartOrSteer` 关联的 W3C trace carrier。它可作为
SDK 本地 span 的 parent；宣告 `promptTrace` 的 Session 还把它映射到 provider
原生提交。SDK 合同包括提取并校验 carrier、映射原生提交入口、创建可选本地
span，以及 best-effort 写入。Codex 映射到 JSON-RPC request envelope 顶层
`trace`；Claude 在同一个常驻 CLI 上，于单条 prompt 提交前尽力通过
`apply_flag_settings.settings.env` 设置 `TRACEPARENT`/`TRACESTATE`，并在下一条
无 trace prompt 恢复启动环境或清空。失败、超时或 CLI 内部排队造成的 trace
归属竞态只影响观测，不阻止 prompt。Claude 的该映射只服务于 W3C prompt trace，
不是通用 per-prompt env API；carrier 不进入 `Input` 或持久状态。它不保证
trace 进入 shell 或 MCP 子进程，也不保证具体 CLI 版本最终导出正确关联的
span。原生 Codex 把输入接纳为 steer 时，不创建新的 turn 执行 span，会复用
原 turn 的 trace；原生 Claude 的 settings env 是进程级状态，写入后当前 turn
的后续新工具、工具启动的子进程或随后 prompt 都可能读取当前值，已经启动的
进程不保证更新。这些是明确接受的 Provider 原生行为，不是 SDK 新增或保证的
语义；SDK 不重定向 provider turn trace，也不提供 prompt/turn 环境隔离。
ACP v2 在 `session/prompt._meta` 预留同类字段，但只有实现并宣告该能力的
adapter 才形成 ACP 传播。per-prompt trace 不要求每个 prompt 启动新进程；同一
CLI 内切换运行时 settings env 也属于 best-effort per-prompt trace。
_Avoid_: 通用 per-prompt env、整个 turn 的 trace、跨 provider trace 隔离

**SDK trace span（SDK 追踪 span）**:
可选的 `Tracer` 为一次 `StartOrSteer` 创建的本地操作 span。它覆盖输入接纳和
原生提交调用，不覆盖 turn 开始、执行完成、审批等待或后续事件；也不表示
provider 为 steer、工具调用或 turn 创建了新的 span。
_Avoid_: 执行 span、turn 完成 span、provider 内部 span

**Requested execution value（已接受请求值）**:
执行选项最近一次获得原生接受回执的调用方字面值。它不表示当前原生状态。
_Avoid_: 生效值、当前值

**Effective execution value（原生生效值）**:
由原生接口明确给出、当前归属于主 Session 的执行选项值。缺少观察时保持未知。
_Avoid_: 请求值、由回执推断的值

**Main conversation（主会话）**:
本次打开或恢复的原生会话，也是普通输入的默认目标。
_Avoid_: 最近发出事件的 Agent（指输入目标时）

**Submission（输入提交）**:
通过原生操作发送输入的动作及其提交结果。提交结果与输入被接受、执行完成是不同事实。
_Avoid_: 任务完成、执行成功（仅有提交结果时）

**Start or steer（启动或并入输入）**:
统一输入操作：把输入交给原生 Session，由适配器按原生语义开始新工作、并入当前工作或返回未应用。它不排队、不等待空闲、不持久化输入，也不自动重试。
_Avoid_: Submit、Steer（指统一输入操作时）、排队提交、等待空闲后发送

**Completion（执行结果）**:
原生会话中一次执行的结果或完成边界。它的范围是该次执行，不包含对全部子任务、后台工作或连接状态的判断。
_Avoid_: 全部结束、Session 结束、全局空闲

**Recovery（观察恢复）**:
出现断线或观察缺口后，通过显式原生操作或应用动作继续观察的过程。
_Avoid_: 自动重连、无缝续接、完整事件回放（泛指观察恢复时）

**Archive（归档）**:
原生会话存储生命周期中的隐藏状态；归档保留 transcript 和保存内容，不等于删除。
原生服务可能为此关闭已加载的执行；它不是 Resume、Close 或观察订阅操作。
_Avoid_: 删除、清空历史、停止 Session

**Unarchive（恢复归档）**:
把已归档的保存会话恢复为未归档状态的显式原生操作。它不 Resume、不建立附着，
也不启动执行。
_Avoid_: 恢复会话、重连、运行历史

**Native observation identity（原生观察身份）**:
原生协议在其自身作用域内标识一次观察上下文的身份。
_Avoid_: 执行身份、跨协议统一周期

**Native execution identity（原生执行身份）**:
原生协议在其自身作用域内标识一次执行的身份。
_Avoid_: 观察身份、跨协议统一 Turn

### 事件与观察证据

**Connection reception（连接接收）**:
连接取得一条原生记录的事实，此时记录所属的 Session 可能尚未确定。
_Avoid_: Session 接收（仅确认连接取得记录时）

**Session reception（Session 接收）**:
基于当时可用的原生成员证据，将记录接纳为某个 Session 观察内容的事实。
_Avoid_: 连接接收、应用已消费

**Isolated diagnostic material（隔离诊断材料）**:
接收时尚未确认 Session 归属、作为诊断保留的连接记录。
_Avoid_: Session 事件历史、可回答请求

**Session observation membership（观察成员关系）**:
原生证据确认某个 Agent 的事件属于一个 Session 观察范围的关系。成员关系与已知父级或完整父链是不同事实。
_Avoid_: 父子关系、根路径可达（泛指成员关系时）

**Observed association（已观察关联）**:
原生证据已确认的成员关系或其他关联事实。发现关联与确认新建 Agent 是不同事实。
_Avoid_: 创建事实、父子关系（未确认相应事实时）

**Created evidence（创建证据）**:
原生材料中明确支持某个 Agent 被创建这一事实的证据。
_Avoid_: 首次看到、发现已有记录

**Root membership（根路径归属）**:
一个身份通过已确认的父级关系连接到选定观察根的事实。它只描述已知父链，不等同于全部观察成员关系。
_Avoid_: 观察成员关系、回答权限

**Unknown parent（父级未知）**:
某个身份的直接父级或其通向观察根的父级关系尚未得到证实的状态。
_Avoid_: 根的直接子级、不属于 Session（仅缺少父级证据时）

**Gap（观察缺口）**:
因原生证据、身份归属、可用观察能力或资源边界而无法确认完整覆盖的区间或范围。
_Avoid_: 没有活动、任务已停止（仅存在缺口时）

### 工具调用

**Tool identity（工具身份）**:
由工具提供方和 provider 原生工具名共同确定的身份，公开 JSON 表示为
`(provider, toolName)`。call 与 result 复制相同的 `provider`、`toolName` 和
`kind`；只有前两者组成身份，`kind` 不是身份的一部分。
_Avoid_: 工具类别、展示标题

**Tool name（工具名）**:
provider 能提供的最具体的原生工具标识。它可以是显式模型工具名，也可以是原生
item type fallback；必须非空，且不承诺跨 provider 相同。
_Avoid_: 统一 leaf name、工具动作类别

**Tool provider（工具提供方）**:
工具由 Agent runtime、动态注册机制还是 MCP 提供的分类。合法形式恰好是
`agent`、`dynamic`、`mcp` 或 `mcp:<server>`；`mcp:` 非法，`<server>` 非空
且允许继续包含 `:`。已知 MCP server 时把 server 放在 `mcp:<server>` 中，
不另设 payload server 字段。
_Avoid_: 事件来源、Session provider

**Tool kind（工具类别）**:
SDK 按 ACP 词汇附加的跨 Agent 动作分类，用于展示、统计和粗粒度路由提示，不表示
权限、资源范围或不同 provider 的行为等价。
_Avoid_: 原生工具名、审批类别

**Tool status（工具状态）**:
工具 call 或 result 上由 provider 原生生命周期投影得到的状态。它不是工具身份
或工具类别，SDK 不从状态推断身份，也不统一跨 provider 的成功语义。
_Avoid_: 工具类别、Session 状态、执行结果

**Tool chunk（工具调用分块）**:
与某个工具调用 ID 关联的部分原生输入内容。分块只表达 `{id, content}`，不单独
承载完整工具身份，并可能早于完整 call 出现。
_Avoid_: 完整工具调用、工具结果

### 子 Agent 输出

**Agent output settings（Agent 输出设置）**:
决定原生运行时输出哪些子 Agent 内容的设置。
_Avoid_: SDK 交付策略、展示过滤

**Child detail events（子详细事件）**:
可归属于子 Agent 的文本、思考和工具进度内容。父工具结果、关系事实、终态通知和实际审批请求是其他类别的事实。
_Avoid_: 全部子事件、子任务结果

**Internal result return（内部结果回父）**:
原生编排把子任务结果提供给父 Agent 的过程，与应用消费观察事件是不同路径。
_Avoid_: SDK 收到子事件、应用转发子事件

### 交互权限

**Approval controller（审批控制方）**:
拥有某个实时原生请求的原始回答权限的一方。
_Avoid_: 观察方、父 Agent（仅凭关系推定控制方时）

**Observer（观察方）**:
能够查看交互事实、但不因观察而取得请求回答权限的一方。
_Avoid_: 审批控制方、权限持有方

**Original answer handle（原始回答句柄）**:
绑定实际原生审批或问答请求、代表其回答权限与有效状态的操作对象。
_Avoid_: 请求文本、工具调用记录、关系记录

### 保存内容与查询

**Metadata（会话元数据）**:
保存会话的身份、标题、预览、时间和可用状态等描述信息。
_Avoid_: 完整消息、正在运行的证明

**Saved content（保存内容）**:
原生存储保留的会话消息、执行条目或其他内容记录。
_Avoid_: 原始事件日志、实时交互请求

**Raw event log（原始事件日志）**:
记录原始协议交互的日志，包括实际捕获的通知、增量、审批和 hook 等记录。它与保存的会话内容或 transcript 原文是不同材料。
_Avoid_: 保存内容、完整事件日志（未确认捕获完整性时）

**Query coverage（查询覆盖）**:
一次查询在所声明范围内实际完成的发现、采样或内容读取范围。
_Avoid_: 全部历史、完整存储快照

### MCP 配置

**External MCP server（外部 MCP server）**:
由原生 CLI 启动或通过 URL 连接的 MCP server，与应用在 SDK 内直接回答
`mcp_message` 的 application-hosted MCP server 不同。
_Avoid_: SDK MCP server（指外部 server 时）、普通工具

**Runtime MCP defaults（Runtime MCP 默认项）**:
调用方在构造 Provider 前从 Runtime 配置解析的外部 MCP 声明。Session 声明可按
name 整体覆盖同名默认项。
_Avoid_: native ambient MCP、用户配置、项目配置

**Native ambient MCP（原生 ambient MCP）**:
原生 CLI 从用户、项目、企业或其他外部设置自行发现的 MCP 配置。Session 或
Runtime 的声明不保证整体替换或删除同名 ambient server。
_Avoid_: Runtime MCP 默认项、Session MCP

### 故事、验证与采集

**User story（用户故事）**:
SDK 使用者的一个真实、完整的使用目标，由一组验证 case 共同验收。
_Avoid_: 单个 case、采集批次

**Validation case（验证 case）**:
归属于一个用户故事、可独立录制和回放的完整使用流程，具有自己的身份、前置条件、输入、操作序列和可观察结果。同一流程中的多条断言共同构成该 case 的验收。
_Avoid_: 单条断言、专项故障测试、一次录制

**Capture attempt（采集尝试）**:
某个 provider 对一个验证 case 的一次实际采集，具有本次采集的版本、批次和结果。
_Avoid_: case 身份、故事身份

**Simulation testing（仿真测试）**:
以真实 Agent 交互的录制为依据，通过 mock 回放这些交互，验证用户故事中真实使用流程的测试方式。仿真测试的 case 表达真实可执行的使用流程；人工构造的错误、边界与竞态由代码集成测试负责。
_Avoid_: 故障注入测试、任意合成协议测试（泛指仿真测试时）
