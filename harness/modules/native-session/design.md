---
type: Architecture
title: 原生 Session 深模块设计
status: stable
---

# 原生 Session 深模块设计

## 调用者与职责

应用需要打开一个长期主会话，持续消费原生事实，随时提交输入、回答请求及控制执行，最后释放自己拥有的资源。Session Module 隐藏连接初始化、身份固定、事件分流、请求关联与清理的协调。调用方无需拼装订阅、独立审批回调。

行为目标来自[通用架构](../../sdk/architecture.md)和[共同故事](../../sdk/validation-cases.md#stories-sdk-用户故事)。本页规定项目内的逻辑 Module 与 Interface，不要求每一项成为一个包或公开接口。两个原生 SDK 独立实现；一致的操作概念不建立根通用客户端或跨原生协议的回答状态机。

## 内部 Module 与状态所有权

| Module | 隐藏的复杂度与唯一拥有状态 | Seam 与依赖 |
|---|---|---|
| 原生协议 | 编解码、未知字段与变体、原始数据和固定版本语义 | 纯计算；types 生产依赖仅标准库。 |
| 原生交互 | 读写、请求关联、已发送或结果未知、连接故障、自有进程或 socket 回收 | 原生 I/O 的私有 seam；生产传输和离线对端是实际 Adapter。控制处理不依赖 Receive。 |
| Session | 主身份、唯一 FIFO、会话范围注册、事件与请求交付、交互终止 | 外部 seam 是公开 Interface；内部组合原生交互和记录读取。 |
| 请求权限 | 原 request ID、交互身份、有效性、取消与单次回答 | 由原生交互持有权威 registry；Session 交付同一句柄，关闭按所属 Session 失效；观察范围重叠不产生第二份回答权。 |
| 观察证据 | 成员集合、可选父级、已验证别名、覆盖缺口 | 原生 Adapter 解释证据；纯父级校验可用 internal/lineage，不能决定全部成员资格。 |
| 原生记录读取 | 主链选择、分页、快照校验、子记录发现、访问和资源限制 | 本地文件和原生查询分别使用必要的私有 seam；不拥有恢复或回答权限。 |

这些职责可在同一个原生 SDK 内组合，不为单个函数建立通用插件框架。共享机械 Module 不认识原生协议、Session 身份或权限。连接拥有 Session 注册及未归属隔离材料；Session 拥有自己的交付状态。外部显式共享连接时，关闭一个 Session 不关闭其他 Session 或整个连接。自有进程的 Session 则承担进程回收。

## 验收与测试 seam

真实使用流程见[共同验收](../../sdk/validation-cases.md#流程语义)。

采用公开 Interface 验证上述行为；必要的私有 seam 只控制原生对端、存储、时钟和故障。原生进程与外部运行时属于真实外部依赖，离线 Adapter 必须依据固定原生协议；不能用测试自创语义证明兼容。原生编解码、写入歧义、并发权限及快照完整性回归仍单独保留。替换只断言旧组织方式的测试，不叠加另一套长期故事框架，遵循[测试规则](../../testing.md)。

<a id="interface-原生-session-公开-interface"></a>

## 原生 Session 公开 Interface

以下为公开接口目标，不声明现有导出 API 已全部实现。两个运行时包各自声明具体 Go struct 和方法，不新增根接口、通用客户端或另一套运行模型。`types` 仅放无权限的原生值。下列签名中的配套值由本节定义其必要字段；原生类型名称沿各包 schema 命名，不要求两端类型可互换。

<a id="interface-session-与交付值"></a>

### Session 与交付值

两端分别提供 `Open(ctx, SessionOptions) (*Session, error)`。Codex 的 SessionOptions 选择自有进程或显式已有 Client 连接，并携带原生 ThreadStartParams，或以 Resume 指针选择原生 ThreadResumeParams（两者互斥）；它还可以携带仅影响 shell/unified-exec 子进程的 `ShellEnv`。Claude 的 SessionOptions 携带原生进程选项，包括创建时 model/thinking/effort、完整进程 `Env`，以及显式声明的 SDK MCP server 名称。已有连接不再是正常调用必须拼装的订阅对象。所有权在原生派发之前保留，失败明确报告。

Claude 的 `Initialize` 及其中的调用方原生 `Hooks` 保留原有 JSON 语义；恢复时
SDK 只追加自己的 `SessionStart` 身份探针，不改写或替换调用方其他 hook。新建
Session 可通过 `SessionOptions.SessionID` 选择官方 `--session-id` 指定 UUID，
该选项与 `ResumeID` 互斥。initialize 成功后 Open 即可固定该主身份，不等待
`system/init`。initialize ACK 不回显该 ID。后续原生主消息必须匹配指定身份；
`system/init` 必须非 replay、只出现一次，且 cwd 与请求目录为同一目录。
未指定 `SessionID` 的调用仍从原生消息观察主身份。

Codex `SessionOptions.Resume *types.ThreadResumeParams` 显式选择已有 thread，要求非空 ThreadID 和空 Thread 创建参数。Open 在派发前保留该身份的所有权及观察队列，接纳恢复应答前后的实时原生事实；仅在恢复应答确认同一身份后返回 Session。旧隔离记录不重新入队。恢复失败不重试、不创建替代 thread；已可能派发的恢复执行一次有界解除附着，失败保留不确定释放屏障，自有连接同时关闭回收。共享连接的其他 Session 不被关闭。

`Session.ResumeResponse()` 独立返回原生恢复应答及其中的保存内容；新建时无恢复应答，恢复时无 StartResponse。保存的 turns 不转换为实时 Receive 事件，不建立子成员或旧审批权限。恢复后的 Submit、Receive、ListSubagents 使用同一个主身份；ListSubagents 仅发现保存关联。恢复是显式原生操作，保留其附着与执行副作用，不承诺纯订阅、自动重连或补齐断线期间事件。

两个 `*Session` 提供以下公开方法；同名配套类型位于各自运行时包。Codex Session
另有 `Archive`/`Unarchive`，Claude Session 不提供这两个方法。

```go
Receive(ctx context.Context) (Received, error)
Submit(ctx context.Context, input Input) (Submission, error)
SetModel(ctx context.Context, model string) (ConfigurationReceipt, error) // Codex
SetModel(ctx context.Context, change ModelChange) (ConfigurationReceipt, error) // Claude
SetThinking(ctx context.Context, change ThinkingChange) (ConfigurationReceipt, error)
SetEffort(ctx context.Context, change EffortChange) (ConfigurationReceipt, error) // Claude
ListSubagents(ctx context.Context, query SubagentQuery) (SubagentPage, error)
Interrupt(ctx context.Context, options InterruptOptions) error
Archive(ctx context.Context) (types.ThreadArchiveResponse, error) // Codex
Unarchive(ctx context.Context) (types.ThreadUnarchiveResponse, error) // Codex
Close() error
Abort() error
Done() <-chan struct{}
Err() error
```

### 归档与恢复

Codex `Session.Archive`/`Unarchive` 以固定主身份调用原生
`thread/archive`/`thread/unarchive`，不接受调用者另选 thread ID。成功仅表示原生
RPC 接受；`thread/archived`/`thread/unarchived` 是经 `Receive` 交付的独立原生
事实。SDK 不删除 transcript、不关闭 Session；原生服务可能级联归档该主身份派生的
子线程。原生归档也可能先关闭已加载的主/子线程，因此不保证正在进行的执行继续。
`Unarchive` 保留原生 `Thread` 响应与未知字段，并拒绝返回其他 thread ID 的响应；
该操作只恢复保存状态，不 resume、不建立附着，也不启动执行。

Claude 当前没有对应的原生归档契约，因此 Claude Session 不声明这两个方法，也不以
移动本地 transcript 文件模拟归档。

`Received` 是有判别字段的值，只携带一个事实类别：原生 Event、真实 IncomingRequest、SDK 关系/覆盖证据。它保留原生顶层 Raw、Params、未知 envelope 字段与来源身份。其 Event 复用原生类型；不是把两种协议收敛为一个事件模型。

Receive 是唯一消费入口，不另设 Events、Requests channel、审批回调或 History channel。
Done 表示交互终止，Err 保留首个终止原因；Close 返回资源回收结果。
并发等待、取消、排空与 Abort 见[事件交付](#delivery-event-delivery)。

Codex 的 `StartResponse() (types.ThreadStartResponse, bool)` 返回 Open 实际收到的原生启动响应副本，保留 model、sandbox 和未知字段。该只读查询不发送协议消息，不产生消费者或回答权限。

主身份由 Session 固定。Codex 输入参数不再要求调用者重复填 ThreadID；Claude 输入不能将 session_id 或 parent_tool_use_id 当作任意目标选择器。Claude 新建时可由官方 `--session-id` 指定 UUID，并在 initialize 成功后固定；后续原生主消息与 init 会核对指定身份。恢复时由 live init 或匹配的私有 `SessionStart` callback 确认身份；单独 hook lifecycle frame 不充分；尚未确认原生根身份时不伪造 observed ID。

<a id="interface-独立历史查询"></a>

### 独立历史查询

历史查询不属于 Session 生命周期，不要求 Open 或同身份空闲。Codex Client 的四个原生查询及 Claude package-level 本地发现、metadata、主/子消息查询相互独立；结果所有权、后端与分页边界见[独立历史查询合同](../../contracts/history-query.md)。完整查询签名与值归各查询合同维护；Claude package-level ListSubagents 不替代 Session.ListSubagents。

<a id="interface-原生输入与配置"></a>

### 原生输入与配置

| 值 | Codex | Claude |
|---|---|---|
| Input | 原生 `[]types.UserInput` 与 turn/start 可用执行选项；不含可改写的主 ThreadID。 | `Input{Text}` 或 `Input{User: *types.UserInput}`，后者携带原生 InputMessage 与关联/扩展；支持 text、image、document、tool_result，其他块明确拒绝。image source 支持 base64/url，document source 支持 base64/url/text/content；工具结果/文档的嵌套内容同样校验支持范围。根 envelope 由 Session 固定，不从输出 decoder 推导发送许可。 |
| Trace context | `Session.Submit` 从 context 读取 `types.W3cTraceContext`，有效时只写入 JSON-RPC request envelope 顶层 `trace`，不写入 `turn/start.params`。 | `Session.Submit` 从 context 读取 `claude.TraceContext`，尽力在写入 user frame 前通过同一常驻 CLI 的 `apply_flag_settings.settings.env` 切换 `TRACEPARENT`/`TRACESTATE`；不重启进程，control response 拒绝或超时不影响健康 transport 上的输入接纳。 |
| Submission | 保留原生 TurnStartResponse（包括 turn 身份）；成功表示 RPC 确认，不表示 turn 完成。 | 成功表示 user frame 本地写入完成；trace control 的 transport 写入失败遵循既有写入错误合同，完整 control 写入后的拒绝或等待超时不参与成功判定。没有虚构 CLI turn 接受回执或 Turn。 |
| ModelChange | SetModel 直接接受原生模型标识字符串。 | `Model *string`：nil 省略 model，恢复原生默认；非 nil 非空标识逐字发送；不发送 model:null，不将空字符串或跨提供方别名等同默认。 |
| ThinkingChange | 原生 ReasoningEffort 与 ReasoningSummary 值，分别用 thread/settings/update 的 effort 和 summary；两字段独立保留省略/null，不增加跨提供方档位或将展示配置转换成预算。 | `MaxThinkingTokens *int` 的 nil 发送 null 清除，非 nil 保留 0/整数；`ThinkingDisplay *string` 的 nil 省略，值为 summarized/omitted，`ClearDisplay` 单独发送 null 清除；创建时原生 thinking enabled/adaptive/disabled 独立表达，不将创建模式隐式转换成动态预算。 |
| EffortChange | — | `Level *string` 通过 apply_flag_settings 的 `effortLevel` 设置；nil 发送 null 清除 flag-layer override，非 nil 非空值逐字发送，空值拒绝。请求必须保留不相关且仍有效的 flag settings；无法确认时在写入前拒绝。该请求不改变 max_thinking_tokens，也不转换成 thinking budget。 |
| ConfigurationReceipt | 原生空响应、请求值与“原生接受”证据；settings updated 是另行收到的观察事实。 | `Requested` 为确切原生请求 payload，`Response` 为完整 control_response，`Accepted` 仅表示 success ack；`Observed` 保持未知，实际事实由 Receive 单独交付。 |
| InterruptOptions | 显式原生 TurnID，ThreadID 固定为主会话；不猜最后收到的 turn。 | 原生 interrupt 不要求合成执行 ID；无执行选择字段。 |

Submit 使用原生 start 语义，不依据本地 Busy 推断 start/steer。若保留 Codex steer 扩展，必须显式选择并携带 expectedTurnId；不能自动切换。文本便捷调用只构造 Input 并调用同一 Submit。未知输入变体保留原文不等于确认可发送；已知未接入内容明确拒绝，不能转成提示词或丢字段。

Codex 原生包提供 context carrier helper：

```go
func WithTraceContext(ctx context.Context, trace types.W3cTraceContext) context.Context
func TraceContextFromContext(ctx context.Context) (types.W3cTraceContext, bool)
```

`WithTraceContext` 使用私有 context key 保存值；重复调用以后一次为准。
`TraceContextFromContext` 只读取当前值，不把读取成功解释为 carrier 已通过
校验。carrier 不作为 `Input` 字段，也不进入原生用户消息或请求 params。

`Session.Submit` 在写入前读取并复制该 context value，尽力校验 `Traceparent`
和可选的 `Tracestate`。无效或不完整的 carrier 视为没有 prompt trace，省略
envelope `trace` 并继续提交；trace 是观测元数据，不改变输入结果。有效
carrier 仅用于该次 request/envelope；不修改 Session 主身份、后续输入或进程
环境。`Client.Call`、`StartThread` 和 `ResumeThread` 不读取该 context value，
也不新增 trace 参数。

Claude 原生包提供独立于 `agent` 的同类 helper：

```go
type TraceContext struct {
    Traceparent string
    Tracestate  string
}

func WithTraceContext(ctx context.Context, trace TraceContext) context.Context
func TraceContextFromContext(ctx context.Context) (TraceContext, bool)
```

`Session.Submit` 在原生写入前读取并复制 carrier，按
[prompt trace 合同](../unified-agent/design.md#prompt-trace)尽力校验 W3C 值。
无效 carrier 视为没有 prompt trace，不返回错误，也不阻止 user frame。存在
有效 prompt trace，或此前 prompt 留下未恢复的 trace override 时，它先取得与
`SetEffort` 共用的 settings/input 串行 gate；目标值已确定且与当前状态相同时
可跳过 control request，否则发送：

```json
{
  "type": "control_request",
  "request_id": "go_7",
  "request": {
    "subtype": "apply_flag_settings",
    "settings": {
      "env": {
        "TRACEPARENT": "00-...-...-01",
        "TRACESTATE": "vendor=value"
      }
    }
  }
}
```

两个变量总是同时写；缺失值使用空字符串清除旧值。无 prompt trace 但此前覆盖
过时，恢复 `Open` 时从有效进程环境捕获的 startup baseline；baseline 缺失时
写两个空字符串。无 prompt trace 且无旧覆盖时不发额外 control request。该请求
不能清除 `effortLevel`、`ultracode` 或其他无关 flag settings。

`apply_flag_settings` 是提交入口的一次 best-effort 尝试。等待 control response
不是 prompt 出队确认。完整写出 control frame 后，control response 的拒绝、
等待超时或未知响应仅使 trace 状态未知；只要 transport 和调用 context 仍可用，
`Session.Submit` 就在同一 gate 内继续写 user frame，下一次 prompt 重写完整目标值。
control 写入阻塞时使用既有取消机制关闭 transport，释放写入和 settings gate；
部分写、短写或其他 transport 写入失败终止交互并报告原生 write 错误和未知结果，
不再追加 user frame，也不返回 `Written=true`。apply 成功而 input 写失败时沿用
既有写入错误语义，不自动重发。

`Submit` 和 `SetEffort` 获取 settings/input gate 时使用调用 context；等待期间
被取消的调用在写入前返回该取消错误，不进入 pending 队列。nil context 在取得
gate 前返回既有 nil-context 错误，不改变 trace 或写入状态；统一 adapter 的
nil `StartOrSteer` 在 admission 和 span 创建前返回同一错误类别。

SDK 不维护 prompt 队列，不等待 prompt 开始或整个 turn，也不因同一 Session
另有 prompt 正在执行而拒绝带 trace 的提交。Claude CLI 可以把已写入的输入留在
自己的内部队列中，并在稍后取走 prompt 时读取进程级 trace env；因此后一次写入
的 env 可能作用于更早尚未取走的 prompt。该归属误差明确接受，不需要 pending
ID、replay 或 dequeue acknowledgment。设置 env 成功也不证明具体 CLI 版本已
导出正确关联的 provider span。

SetModel、SetThinking 与 SetEffort 只改变主会话原生支持范围，不排队等待空闲、不中断、不重建、不递归修改子 Agent。Codex 的 model/effort 影响后续 turns；Claude 原生 ack 不建立跨执行的生效屏障。所有成功返回都不把 requested 写成 observed。原生拒绝保留原错误及关联，已知不支持在发送前明确拒绝。

Claude `Session.GetSettings` 是原生 `get_settings` 的只读查询，逐字返回 response
body，不缓存或解释为统一配置状态。查询成功不表示执行 idle，也不确认模型已消费
此前输入；查询错误保留原生 control 与派发证据。

<a id="interface-codex-会话配置"></a>

### Codex 会话配置

Codex `SessionOptions.Features`、`Tools`、`MCPServers` 和 `ShellEnv` 只在
`Open` 固定，分别生成 `features.*`、`tools.*.enabled`、
`mcp_servers.<name>` 和 `shell_environment_policy.set` native config，并合并到
本次 thread/start 或 thread/resume。未声明的 Feature/Tool key 不覆盖原配置；
`MCPServers []mcp.Server` 使用共享配置类型，并且 Transport 必须显式为
`stdio` 或 `http`；Codex 在 thread RPC 前拒绝 SSE。MCP server 必须具有非空、
UTF-8 合法、无点号且唯一的 name。stdio 与 HTTP 只接受各自字段组，不接受
未声明的 tool filter、OAuth、timeout 或 approval 字段。

新增的 `SessionOptions.ShellEnv` 只提供 additive set，不支持删除或空值
unset。key 必须非空、合法 UTF-8、不含 NUL 或 `=`，并按 ASCII 大小写不敏感
方式唯一；value 必须合法 UTF-8 且不含 NUL。错误不包含 value。
空 value 表示设置空值，不表示删除。
校验和冲突报告按排序后的 key 遍历，保证同一 map 的错误选择与消息稳定。
`ShellEnv` 不是 secret store；value 会进入 native config 并由 app-server 处理。
SDK 不把它写入错误、事件或 SDK 日志，但不能阻止 provider 自身的配置诊断或
日志记录该值。
它不修改调用方 Thread/Resume config，也不代表 app-server、Codex 保留控制变量、
MCP server 或其他任意子进程继承。`shell_environment_policy` 的 ambient
`include_only` 仍可在 `set` 后过滤变量；SDK 不改写调用方 policy。Codex 会在
构造 shell 环境后继续注入身份、应用 `include_only` 并清理受限变量，因此以下
key 必须在 RPC 前拒绝，不能报告一个实际不会生效的值。该列表是固定版本与
已知后续版本身份/控制名称的并集，避免升级后把调用方值静默覆盖或清理：

```text
CODEX_SESSION_ID
CODEX_THREAD_ID
CODEX_TOOL_CALL_ID
CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN
NODE_REPL_AUTH_TOKEN
CODEX_GUARDIAN_DECISIONS_API_KEY
OPENAI_FEDERATION_RULE_ID
OPENAI_IDENTITY_TOKEN_FILE
OPENAI_WORKLOAD_IDENTITY_CONTEXT
```

保留名称按 ASCII 大小写不敏感匹配，与 Codex 在 Windows 环境和受限变量清理中
的比较方式一致。

身份/控制变量由 Codex 拥有或写入，受限启动上下文由 Codex 清理；它们均不由
Session 调用方覆盖。native config 使用扁平 dotted path：

```json
{"shell_environment_policy.set": {"FOO": "bar"}}
```

合并不得修改调用方配置。生成的 raw path 与调用方 `Config` 的精确、祖先或后代
路径冲突时，必须在 thread RPC 前返回包装本包 `ErrInvalidInput` 的错误；错误
可包含字段和路径名，不得包含 header、token、env 或其他配置值。低层
`StartThread`/`ResumeThread` 不隐式应用这些便利字段，也不写用户 config。
Codex 没有 Claude 等价的 strict MCP 选项；thread config 会与 ambient 配置按
字段递归 merge。本次声明只保证添加或覆盖所声明的字段，不保证整体替换同名
ambient server，也不能删除其未声明字段。同名项可能保留不同 transport 的字段
并由 Codex 拒绝；需要确定 server 集合时，调用方必须使用隔离的 `CODEX_HOME`
或外层执行环境。

`thread/resume` 对仍被观察或运行的 loaded thread 可能忽略 config overrides；
恢复成功不表示 MCP tool set 已重建，SDK 不承诺运行中热替换。

<a id="interface-claude-external-mcp"></a>

### Claude 外部 MCP 配置

Claude `SessionOptions.MCPServers []mcp.Server` 支持 native `stdio`、
Streamable HTTP 和旧式 HTTP+SSE，且 Transport 必须显式指定。stdio 接受
command、args 和 env；HTTP/SSE 接受 URL 和 headers。Claude 拒绝共享配置中的
stdio cwd、env header 和 bearer token 环境变量字段。`StrictMCPConfig` 向原生
CLI 传递 `--strict-mcp-config`，但其企业策略兼容性、同名优先级和 ambient
覆盖属于原生 CLI 行为。

这些是原生 Claude 能力。统一 Agent 层只采用 stdio 与 Streamable HTTP，不暴露
旧式 SSE 或 `StrictMCPConfig`；其 Runtime/Session 合成规则见
[统一 Agent MCP 配置](../unified-agent/design.md#mcp-configuration)。

<a id="interface-进程环境与-prompt-trace"></a>

### 进程环境与 prompt trace

原生进程环境、Session `ShellEnv` 与 prompt trace 是三个独立合同：

| 合同 | Codex | Claude |
|---|---|---|
| 子进程完整环境 | app-server `StdioOptions.Env`；nil 继承，非 nil 整体替换 | Session `SessionOptions.Env`；nil 继承，非 nil 整体替换 |
| Session shell-only 环境 | `SessionOptions.ShellEnv` 映射 `shell_environment_policy.set` | 不支持；没有稳定 shell-only 入口，非空值必须拒绝 |
| Prompt trace | `Session.Submit` 从 context 读取 carrier，映射 request envelope 顶层 `trace` | `Session.Submit` 从 context 读取 carrier，在同一常驻 CLI 上于 user frame 前切换 settings `env`；启动 baseline 用于恢复 |

表中 Prompt trace 只描述 SDK 的映射入口。原生 Codex 的 steer trace 复用、
原生 Claude settings env 的进程级扩散，以及 provider 最终如何创建或关联 span，
都属于原生 Agent 行为，不是本包新增或保证的语义。Synthetic 测试只验证
carrier 编码、调用顺序和 best-effort 边界，不伪造这些原生结果。

`Env` 是完整环境声明而不是 overlay。调用方只追加变量时必须显式复制
`os.Environ()` 后加入目标值；SDK 不在 nil 时注入 `os.Environ()`，也不在非 nil
时自动合并 ambient 变量。`Env` 可含认证秘密，SDK 不记录其值。process-free 的
`StreamOptions.Env` / `OpenStreams` 只保存协议快照，不启动或修改子进程环境，
不提供 env 注入。

Codex app-server 是一个共享进程，其 `Env` 对所有 Session 相同；Session
`ShellEnv` 才能在共享 app-server 上为不同 thread 提供 shell 级覆盖。Claude
每个 Session 拥有自己的 CLI 进程，原生 `claude.SessionOptions.Env` 因此可在
每个 Session 选择不同完整进程环境。

统一层可以为 Claude Session 提供 additive `ProcessEnv`，但原生接口不需要新增
同名或 `EnvOverlay` 字段：adapter 以 `Config.Env` 或 `os.Environ()` 为基底，
合成完整 `[]string` 后写入现有 `SessionOptions.Env`。统一层仍把 Claude
`ShellEnv` 视为 unsupported，不能因为 Session 恰好自有进程就把更宽作用域改名
为 shell-only。详细 overlay 规则见
[统一 Agent 环境配置](../unified-agent/design.md#environment-configuration)。

Codex 原生包新增手写的 `types.W3cTraceContext`，保留 pinned `rpc.rs` 的 W3C
原文和 native optional 语义。该 envelope 类型不属于当前生成 schema 闭包：

```go
type W3cTraceContext struct {
    Traceparent Optional[string] `json:"traceparent" protocol:"nullable"`
    Tracestate  Optional[string] `json:"tracestate" protocol:"nullable"`
}
```

`Session.Submit` 只把合法 carrier 编码成 envelope `trace`：`Traceparent` 必须是
非空且合法的 `Some`；`Tracestate` 的 absent 或 null 规范化为不发送，非空
`Some` 必须是合法值。无效或不完整的 carrier 整个省略，不发送空对象、空字符串
或非法值，也不影响 prompt 提交。

`Session.Submit` 通过 `codex.WithTraceContext` 接收 context carrier，并把它
写到 JSON-RPC request envelope：

```json
{
  "id": 7,
  "method": "turn/start",
  "params": {"threadId": "...", "input": []},
  "trace": {"traceparent": "00-...-...-01", "tracestate": "vendor=value"}
}
```

`trace` 不能放进 params、user content 或 turn/item identity。app-server 用 envelope
trace 建立 request span 并让该提交的内部工作继承；没有 envelope trace 时可以
按 provider 行为回退到进程启动时的 `TRACEPARENT`。Codex SDK 不替调用方设置
该进程变量，也不保证一次 prompt 的 trace 自动进入 Codex Bash/MCP 子进程。

本包对 Codex 的 trace 合同止于把单次 `Session.Submit` 的 carrier 写入 request
envelope；之后的 span 归属由原生 app-server/Core 决定。原生 Codex 把
`turn/start` 判为 steer 时不创建新的 turn 执行 span，而是继续使用原 turn 的
trace；新 carrier 最多父化本次 request 和输入接纳路径。服务端可把既有
`turn.id` 记录到该 request span，但不会替换 active turn 的 trace，也不会回溯
重挂已经产生的 span。`TurnInput` 入队后不携带 carrier，后续 turn 工作保持
turn 建立时的 trace。这些是原生 Agent 行为，不是本包新增或保证的语义；
SDK 不模拟该状态机，也不重定向 active turn 的 trace。因此 prompt trace 不是
turn-level retarget；需要相同 trace 的多次输入必须由调用方复用 carrier。显式
`turn/steer` 请求不承诺该传播，也不属于 `Session.Submit` 的 envelope trace
合同。

`types.W3cTraceContext` 的 W3C 语法规则以
[prompt trace 合同](../unified-agent/design.md#prompt-trace)为准。native 层在
提交前提取并复制有效 carrier；低层 `StartThread`、`ResumeThread` 和
`Client.Call` 等通用 request 不新增公开 trace 参数，也不能隐式传播 prompt
trace。只有 `Session.Submit` 通过私有的 trace-aware call path 写 request
envelope。

Claude 原生包使用 CLI stream-json/control 协议，不使用 ACP。本包的 Claude
支持直接来自原生长连接 control 协议，和 ACP adapter 状态相互独立。

固定 trace 可在启动前通过 `SessionOptions.Env`（或统一层的 `ProcessEnv`）
提供；prompt trace 使用同一常驻进程的 settings env，并在下一条无 trace prompt
恢复该启动 baseline。两者都不重启 CLI。SDK 的合同止于尽力设置 settings env
并保持本地 apply/user frame 顺序；CLI 之后如何使用该环境属于原生 Agent
行为：trace env 写入后，当前 turn 的后续新工具、工具启动的新子进程或随后
prompt 都可能读取当前值；已经启动的进程不保证更新。SDK 不按工具调用快照或
恢复，也不保证 prompt/turn 隔离。原生包只承诺 best-effort control response
和本地写入顺序；transport 写入失败遵循正常失败合同，不保证具体 CLI 版本
已导出正确关联的 provider span。

<a id="interface-真实-incomingrequest"></a>

### 真实 IncomingRequest

两个运行时分别定义 `IncomingRequest`，包含原生 request ID、method/subtype、完整 Raw/Params 及原来的权威回答句柄。提供 `Approve(ctx, nativeOptions)`、`Deny(ctx, nativeOptions)`、`Answer(ctx, nativeAnswer)` 和 `Context()`。nativeOptions/nativeAnswer 是各包按请求种类判别的具体值，不使用通用 map 建立共享回答状态机。不适用于该请求种类的操作在写前返回不支持/无效答案错误。

Codex 的适配覆盖 command execution、file change、permissions、原生 legacy approvals；Answer 对 tool/requestUserInput 保留 question ID 与答案数组。原生 scope、execpolicy/network amendment 等扩展按请求支持情况保留。RPC Reject（协议错误）不能映射成用户 Deny。Claude 的 Approve 保留 updatedInput/updatedPermissions，Deny 保留 message/interrupt，Answer 仅对真实 can_use_tool 请求中的问答工具保留题文键、问题列表和原生 updatedInput。

Context 在原生取消、已回答或交互终止时失效；回答前由同一 registry 再检查，至多一次有效发送。派发结果未知不恢复可重试权限，不自动重答。不增加默认审批倒计时或无人回答决定。ParseEvent、历史、子记录和隔离诊断均不能构造有效 IncomingRequest。

Claude 的 `Options/SessionOptions.Effort` 是创建时逐字传递的原生 `--effort`，空值省略；创建字段与运行时 `EffortChange` 分开，两者都不转换成思考预算。

Claude 的 `SDKMCPServers []string` 只声明由应用处理的原生 SDK MCP server 名称。实际 `mcp_message` 只有在 server 已声明时通过同一个 Receive FIFO 交付为 IncomingRequest；`MCPRequest()` 返回原生 server_name/message 的分离副本，`ReplyMCP(ctx, json.RawMessage)` 通过原始控制句柄回答 JSON-RPC。回复保留原 ID 和未知字段；notifications 仍确认外层 control 请求。它不授予工具审批权限，也不能使用 Approve/Deny/问答 Answer 代替。取消、单次回答、Close 和 OutcomeUnknown 沿用该原生连接的控制权威；未声明或其他不支持的控制请求仍明确拒绝。SDK 不添加公开的通用回调 API 或内部 MCP 服务实现；下节的 Claude 私有 `SessionStart` 身份探针只由本包应答，不进入该公开面。

<a id="claude-sessionstart-probe"></a>

### Claude SessionStart 身份探针

`SessionStart` 是 Claude Code 的原生 hook 事件，不是 SDK 新造的事件；原生
`source` 为 `startup`、`resume`、`clear`、`compact`。Claude 依据初始化时发送的
`hooks` 生成 `hook_started`、`hook_progress`、`hook_response`，并在需要
callback 时发送 `control_request.request.subtype=hook_callback`。

`hook_name` 由 CLI 在运行时生成，固定为 `<hook_event>:<source>`，例如
`SessionStart:resume`；`matcher` 只筛选 `source`，调用方不能给 hook 指定私有
名称或附加标签。应用自己的同类 hook 与 SDK 探针因此可能交错，且
`hook_started`、`hook_progress`、`hook_response` 都不携带 callback ID，不能
用于探针归属或身份判断。

SDK 的探针归属只使用 `callback_id`。SDK 在调用方 `Initialize.Hooks` 中追加一个
私有 `SessionStart` matcher，其 `hookCallbackIds` 只包含本次恢复的 ID，并为
每次 Resume 生成新的、不复用的
`agent-sdk-go:claude:resume-probe:<uuid>`。该前缀由 SDK 保留；调用方 Hooks
已包含同前缀 ID、形状无效或无法无损追加时，在进程启动前拒绝，不覆盖调用方
hook。注册必须在原生进程启动前完成。

以下为 `control_request.request` 体，省略外层 `type`、`request_id` 和 `request`
包裹：

```json
{
  "subtype": "hook_callback",
  "callback_id": "agent-sdk-go:claude:resume-probe:<uuid>",
  "input": {
    "hook_event_name": "SessionStart",
    "source": "resume",
    "session_id": "...",
    "cwd": "...",
    "transcript_path": "..."
  }
}
```

私有 responder 只应答本操作拥有的 `callback_id`，返回正常 `control_response`
和空 `HookJSONOutput` `{}`，至多一次。该请求不作为公开 `IncomingRequest`、
`PermissionRequest` 或回答句柄交付，也不允许应用 Approve/Deny/Answer；其他
callback ID 仍按现有 unsupported 控制规则处理。探针输入必须同时匹配预先固定
的事件、source、目标 Session ID、cwd 和 transcript 文件身份；仅通用
lifecycle frame、错误 session/cwd/transcript、重复 callback 或未知 callback
ID 都不构成成功身份证据。恢复身份的顺序、超时和创建后漂移由统一 Agent 的
[Claude 恢复身份合同](../unified-agent/design.md#claude-resume-identity)维护；
本包只提供原生注册、应答和输入值。

该探针只为 `Resume` 注册，且只对已审阅的 Claude CLI 2.1.258 定义。hook input
不携带 CLI 版本，不能从 `source` 或恢复结果推断版本；运行时版本未确认时不得
把该 fallback 推广到其他版本。新建 Session 不使用该探针。

<a id="interface-子-agent-查询"></a>

### 子 Agent 查询

Codex SubagentQuery 使用 cursor、limit、sourceKinds、archived 等原生过滤，ancestorThreadId 固定为本 Session 主身份，不同时发送 parentThreadId。SubagentPage 保留原生 Thread 记录、nextCursor 和查询覆盖条件。完整非归档与归档覆盖需要分别查询；source 默认 interactive 不等于子 Agent 全覆盖。实验能力未启用或被拒绝必须显式报告，不能回退为缓存。

Claude SubagentQuery 使用本 Session 固定的 ConfigDir/cwd/main ID 与记录数/字节限制，发现主 transcript 旁的 subagents 记录及有证据的嵌套布局。SubagentPage 保留 agent ID、可用 parent/tool/sidecar 元数据、实际扫描范围和完整/部分/未知覆盖。无原生分页时不伪造原生 cursor；资源限制、访问失败和未支持布局不能作为完整空列表。两端允许部分记录与错误同时返回；调用者以 coverage 和 error 判断，不以长度判断完整。列表不触发 resume/start/subscribe、回放或成员观察范围更新。

<a id="interface-错误语义"></a>

### 错误语义

以下分类在两个运行时分别以可判别错误实现，保留 native cause；名称可沿用已有原生错误类型，不要求共享枚举。

| 分类 | 语义与可重试边界 |
|---|---|
| ownership-conflict | 同连接同主身份已有所有者或不确定的附着；本地拒绝，不另建订阅。 |
| unsupported / invalid-input | 已知能力或参数不支持/不合法；未发送。原生未知限制保留原生拒绝，不猜测成功。 |
| request-invalid / already-answered | 原句柄不存在、取消、已回答或已终止；不产生新回答权限。 |
| not-sent / outcome-unknown | 前者确认未派发；后者可能已经写出或执行。输入、配置、回答及释放的后者不自动重试。 |
| native-rejected | 保留原生 RPC/control 错误及关联；不是正常执行失败事件，也不强制结束其他交互。 |
| history-integrity / resource-limit / coverage-gap | 保留 offending raw、游标/路径、已交付及未覆盖范围；不能以截断成功处理。覆盖缺口可作为证据交付，不必全部终止。 |
| transport / protocol / closed | SDK 交互终止与原生执行失败分开；停止新接受、失效权限、排空后暴露原因。 |

关闭规则以[事件交付合同](#delivery-event-delivery)为准。验收使用本页公开方法，不依赖私有父图、回调池或第二消费者。

<a id="delivery-event-delivery"></a>

## Event delivery

所有权取舍见[通用架构](../../sdk/architecture.md#设计取舍)。

<a id="delivery-接收范围与顺序"></a>

### 接收范围与顺序

连接接收是协议 reader 取得原生记录；Session 接收是根据当时可用原生证据，确认该记录属于其观察范围并接受交付。不能把全部连接记录算作每个 Session 的已接收事件。成员资格不要求已知直接父级或完整根路径。

每个 Session 拥有一个消费入口和一个 FIFO，并发 Receive 争用该队列，不广播、不提供独立游标。通知、真实交互请求及其原句柄、SDK 关联证据从该入口交付。已确认属于同一 Session 的实时原生事实按接收顺序接受，不等待父级、创建公告或运行账本。后续关系只增补知识，不重排旧事实。SDK 派生证据与原生记录区分，不能阻止原生相对顺序或隐去原始数据。

不依据实体 ID、UUID、内容相同或未知 run ID 压掉原生事实；只有固定版本明确规定的原生重复规则可以限定处理。关系公告的幂等与原请求单次回答防护不等于原生消息去重。不同 Session、历史与实时之间不声称原始全局发生顺序。

批量接受须先验证大小，连续且全有或全无。控制回复、权限取消及故障处理不等应用消费。相同真实请求只能归属一个可确认的权限所有者；多个观察范围重叠不复制回答权，归属不明确时保持连接级待处理状态并报告缺口。一次等待取消只结束该等待，不删事件或结束 Session；取消与成功接收竞争时允许返回已取出的事件。

<a id="delivery-未归属连接材料"></a>

### 未归属连接材料

未确认成员的记录留在连接级隔离诊断中，不作为任何 Session 的原生成员事件，也不因等待超时广播到全部 Session。隔离设置明确的记录数、字节数及保留期限制，超限、到期与材料可用范围可观察；不得为每个 Session 复制隔离区。

诊断读取保留仍可用的原始材料与接收证据，并标记“未纳入 Session 实时流”。后续成员确认不把旧材料补插进实时流；归属未知区间及未保留部分仍是缺口。隔离材料不能创建回答权；真实未归属请求由连接权威 registry 持有并按原生规则失效，未知归属不自动批准、拒绝或授予某个 Session 权限。

隔离容量与到期策略只约束诊断材料；不得充当真实请求有效期或 Session FIFO 丢弃策略。已知成员但未知父级的事件不能被放入隔离区等待完整树。

<a id="delivery-关闭与排空"></a>

### 关闭与排空

关闭或终止故障停止新接受，失效本 Session 请求权限，唤醒挂起控制并释放所拥有资源；已接受记录仍可排空，包括交付步骤中已取出但尚未返回调用方的记录。正常关闭排空后返回 io.EOF，故障终止排空后返回稳定的终止错误。交互终止、清理完成与事件读尽独立表达，根执行结果不终止流。

Close 幂等，清理不等待消费，清理错误不能覆盖首个交互终止原因。共享连接只释放该 Session 的注册与原生附着；自有进程按其有界回收规则处理。不得把关闭统一描述为递归停止所有 Agent。显式放弃消费的 Abort 可以丢弃 backlog、释放引用；与已经完成的接收竞争时允许该记录返回。普通 Close 不等于 Abort。

<a id="delivery-fifo-机械合同与资源"></a>

### FIFO 机械合同与资源

internal/queue 只共享 FIFO 机械逻辑，直接使用根模块固定的 ChannX，不认识原生类型、关系或权限。每个 Session 一份实例，不按 Agent 分区，无第二份增长的派生事件 FIFO。命令握手及有界单记录 staging 不是独立 backlog。wrapper 的单一输入拥有者负责入队与关闭，保持取消和批量接受语义。

内部 queue 的零值可用，nil 接收者和 nil 等待 context 返回明确错误。Close 拒绝新入队并允许排空；Abort 清除保留引用、唤醒等待者并收拢输入拥有者，可随 backlog 大小产生线性清理成本。首个终止原因保持稳定，清理诊断另行保留。

队列没有固定事件数或字节上限；可用内存是限制，持续慢消费可能耗尽内存。不承诺崩溃、OOM 或进程重启后交付，也不承诺一定能在 OOM 前报告错误。已消费和显式丢弃的引用须释放。停止消费时应排空或明确 Abort，避免 backlog 留存。

帧、请求、观察索引、隔离材料及历史读取各自有显式资源限制；错误不能静默截断已接受事实。父级图深度限制只约束父级知识，不是 Session 的预设观察层数。Call/Write 的单次操作超时与审批等待有效期分开，无默认 SDK 审批倒计时。

内部 snapshot 的 pending 数量和调用者估算字节不是 heap 测量，且不包含已取出的 staging；ChannX 不提供的最旧记录时间保持不可用。不能以诊断估计声称总内存受限，也不因此新增公共 metrics Interface。

<a id="lineage-shared-relationship-rules"></a>

## Shared relationship rules

`internal/lineage` validates confirmed optional parent relationships. Each Session owns its parent graph separately from its membership evidence. The package imports only the standard library and has no event, protocol, creation, permission, transport or SDK type dependencies. Graph reachability proves only a known parent path, not the full extent of Session observation membership.

Native adapters establish membership, identities, aliases and parent edges using version-supported evidence. An identity can be a confirmed member while its parent is unknown. Communication targets alone do not establish parentage; only evidence for the particular native operation may do so. No missing root edge or creation fact is invented. A conflict preserves the accepted parent; self-links and cycles are rejected even in components that have not reached a root. Ancestor queries include confirmed parents, nearest first.

`Confirm` validates candidate vertices, edges, cycles and the maximum depth of
every affected descendant chain before committing. Rejected candidates retain
no vertex or relationship.
`ConfirmRoot` also rolls back a newly observed root or root designation when its
child edge is rejected. A newly
arriving ancestor can exceed an existing descendant's depth, so admission checks
the child's complete observed descendant subtree; maintained subtree heights
cover unchanged chains. `Merge` atomically contracts two caller-confirmed equivalent
identities, rewrites every descendant edge and removes the discarded vertex. It
rejects conflicting parents, ancestor contraction and excessive resulting depth.
Identity equivalence is established by the native adapter, never by the graph.

The graph's integer limits are literal: zero relations permits no edge; zero depth permits only depth-zero vertices. Public native options document their own default mapping. Admission validates all affected chains atomically; a rejected orphan edge cannot reserve vertices.

Graph, member, alias and request limits are distinct resources. Node accounting includes parent-only identities. Native adapters document count/byte limits and report rejected relationship evidence as a coverage/resource gap. A bounded parent index is not a preset maximum Session observation depth and cannot suppress already confirmed member events. It must not fabricate a root edge to fit a bound. If safe continued observation is impossible, termination is explicit and accepted events remain drainable.

The native adapter owns identity equivalence, source provenance, creation interpretation, aliases and observation gaps. Unknown run identifiers remain opaque without fixed native comparison semantics. SDK relationship announcements may avoid duplicates; this does not authorize filtering native events by UUID or entity identity, or keeping an unbounded occurrence ledger on every root message.

Answers require the original live native request handle. Relationship records carry context only; they do not grant answer, input or configuration authority. No TreeRecord or TreeEvent Interface is introduced. See [event delivery](#delivery-event-delivery) for acceptance, isolation and drain semantics.

<a id="child-detail-可选子详细事件交付"></a>

## 可选子详细事件交付

本页规定 [子输出边界](../../sdk/architecture.md#子-agent-输出边界) 的确定性扩展合同，
是实现目标，不声明 API 已提供。两原生包各自实现，不能新增根 Client/config wrapper。
详细验收见下文，历史另见[子历史](../../contracts/history-query.md#children-claude-独立本地子历史)。

<a id="child-detail-原生公开选项"></a>

### 原生公开选项

两包各自在运行时包声明以下值（不是共享类型，不进入 native wire types）：

```go
type ChildDetailPolicy string
const (
    ChildDetailPreserve ChildDetailPolicy = "preserve"
    ChildDetailSuppress ChildDetailPolicy = "suppress"
)
// 新字段：SessionOptions.ChildDetailPolicy ChildDetailPolicy
```

空字符串和 preserve 完全等价；suppress 仅抑制下面白名单中的已确认 child detail。
其他字符串（包括大小写变体）在进程启动、连接注册或 RPC 写入前返回包装本包
ErrInvalidInput 的错误，不忽略、不 clamp、不回退。选项在 Open 前复制并固定，
对该 Session 全部后续 Submit 和子成员生效，无 setter、自动切换或全连接策略。
默认与现有帧交付及原生字段逐字兼容；调用者修改原 options 不改变 Session。

| 原生入口 | 精确范围 |
|---|---|
| codex.Open(ctx, codex.SessionOptions{ChildDetailPolicy: …}) | 新建或显式 Resume 的 Session；Client 和 Stdio 两种连接所有权一致。共享 Client 上各 Session 独立。 |
| codex.Client.ResumeThread(ctx, types.ThreadResumeParams) | 原生低层显式 resume 保持原签名，无 ChildDetailPolicy wire 字段，不新建 Session、不重设现有策略；其附着/执行副作用保留。Session 级恢复由 Open 的 Resume 参数选择，不通过此低层方法先恢复再附着。 |
| claude.Open(ctx, claude.SessionOptions{ChildDetailPolicy: …}) | claude.Options 同名字段，经现有 SessionOptions 定义传递。零值创建和显式 ResumeID 均在 Open 固定策略；ResumeID 仍是原生恢复请求，不等于历史/纯订阅。 |
| 两端 Client 原生读取、独立历史及诊断 | 不受该策略影响；无额外 child 输出参数。 |

Claude 默认 initialize 显式发送 forwardSubagentText:true；调用方可用 Initialize 原生选项
显式选择该字段的值或省略。ChildDetailPolicy 不改写这些初始化选项，不能因 suppress 改成
false/省略。过滤不是源输出控制，不修改提示词、工具、原生内部结果回父或模型配置。
省略详细交付不能承诺降低上游 token/运行费用、源帧大小或全部内存使用。

<a id="child-detail-归属处理顺序及保护规则"></a>

### 归属、处理顺序及保护规则

先按[事件交付](#delivery-event-delivery)完成原生校验、所有权判断、控制 registry 更新和
成员/关系/工具 alias/生命周期处理，再决定本帧是否进入唯一 FIFO。已确认 child
指当时已确认 Session 成员且不是根；不要求完整父图。Codex 使用确认的 threadId；
Claude 仅使用审阅版本 task/tool alias 确认的 AssociationID，单独 agent_id、任意
parent_tool_use_id、文本或路径猜测都不充分。不得扩大版本的身份确认规则。

未知源/未知版本/解码错误/未知方法或变体均不因过滤丢弃；原已归属的保留，未归属的
仍按原容量/到期规则隔离，不广播、不补投。foreign 诊断也不能当 child 删除。
后续确认不重评旧帧、不把隔离材料回填 live。共享连接的回答权仍只有原 controller。

以下事实始终保护，优先于所有白名单：

- 所有真实审批、问题、工具调用请求的原始句柄、Raw/Params、取消/撤销/解决和控制响应；
  单次回答、原 context 及 OutcomeUnknown 规则不变，不产生自动回答或 observer 权限。
- 根/父原生工具结果，尤其 spawn/wait/Agent 子任务结果及失败；不能从结果文本猜成 child。
- 原生身份/关系/创建或关联、任务开始、后台状态、完成、失败、取消、错误和 SDK 覆盖证据。
  根结果、子工具完成、子任务 terminal 和后台停止仍是不同事实。
- 一个帧同时携带上述事实与 detail，或携带未知语义时，保留整个帧。

保留帧的 Raw、Params、原生 payload/unknown/omitted/null 和相对次序不变；不拆分、
删字段、重新 marshal 或制造一个看似原始的帧。不制造 filtered 占位事件或第二队列。
未知扩展字段默认视为 mixed，保留整帧；白名单只允许固定 schema 的已知字段集合。
普通关联用 threadId/turnId/itemId/UUID 等引用字段不单独构成新关系事实，但原生
身份扩展/任务 parent/终态字段构成保护事实。过滤发生在入队前，不能妨碍控制进展；
Close 排空、Abort 放弃 backlog、取消等待、资源限制和原生处理顺序保持原合同。

<a id="child-detail-codex-逐方法分类"></a>

### Codex 逐方法分类

下面是封闭白名单，要求已确认 child、成功通过该方法的固定 schema 形状校验（不要求已有 typed binding）、仅固定 schema 字段、无保护事实。
未列方法一律保留，包括未来 delta。

| 方法或 envelope | suppress 行为 |
|---|---|
| item/agentMessage/delta | 可抑制纯 delta。 |
| item/reasoning/textDelta、item/reasoning/summaryTextDelta、item/reasoning/summaryPartAdded | 可抑制纯思考进度。 |
| item/commandExecution/outputDelta、item/fileChange/outputDelta、item/mcpToolCall/progress | 可抑制纯工具进度；请求审批与结果不是进度。 |
| item/started、item/completed（所有 item 变体） | 整帧保留：item 生命周期/工具结果；其中的全文可能仍可见。这是明确的过滤局限。 |
| thread/*、turn/*、error、serverRequest/resolved、SDK Observation、IncomingRequest | 全保留；thread/started 与 collabAgentToolCall/subAgentActivity 关系处理照常。 |
| 其他方法、未知 item/字段、DecodeError | 按现有归属保留/隔离，不推断为白名单。 |

<a id="child-detail-claude-逐-envelope-分类"></a>

### Claude 逐 envelope 分类

同样只对已确认 child 且成功解析的封闭已知形状抑制。已知字段集以审阅的
stream-json 版本为界，unknown content block/扩展一律 mixed。

| envelope/内容 | suppress 行为 |
|---|---|
| assistant.message.content 全为 text/thinking/redacted_thinking；无 error、tool_use_result 或工具块 | 可抑制纯详细消息；已知 usage、stop_reason 是消息元数据，不等于任务 terminal。 |
| stream_event.event.type=content_block_delta；delta.type 为 text_delta/thinking_delta/signature_delta/input_json_delta | 可抑制纯增量；input_json_delta 仅工具参数进度，不含控制请求。 |
| stream_event 的 message_start/message_delta/message_stop/content_block_start/content_block_stop/ping 及未知 API 类型 | 保留整帧；边界/usage 等不会被误作任务完成，也不维护额外配对状态。 |
| assistant 含 tool_use/server_tool_use 或未知块；user 含 tool_result；任何其他 user | 全保留，保留工具归属/结果和 mixed 内容。 |
| system（包括 task_started/task_progress/task_notification/background_tasks_changed）、result、control_request/control_response/control_cancel_request、rate_limit_event、conversation_reset | 全保留。task_progress 可混有任务身份/状态，不做纯文本猜测。 |
| 其他 envelope/字段、错误、unknown source | 按现有归属保留/隔离。 |

本策略是尽力减少已审阅的纯 detail，并非 child 内容安全擦除接口；完整 item、mixed
assistant、系统通知和未知帧仍可能含文字/思考/工具内容。调用者不得将其当脱敏保证。

<a id="child-detail-专项验收"></a>

### 专项验收

合成检查通过两端公开 Receive 与原始 peer trace 验证过滤，不计为仿真 case 通过。

| 场景 | 公开操作与独立预期 |
|---|---|
| 默认与固定策略 | 两原生 Open 未填/空/preserve 的输出逐帧相同；交付策略无新 wire 设置；Claude 默认 initialize 的 forwardSubagentText 为 true，显式 Initialize 配置独立于交付策略。非法值在 fake peer 收到启动/RPC 前拒绝。Claude ResumeID + 两策略仍固定本次策略；Codex 低层 ResumeThread 不接受 SDK 策略；Open 的 Resume 固定本次 Session 策略。 |
| 纯详细帧 | 经原生关系确认后发送矩阵每个白名单方法/shape；preserve 交付，suppress 不交付。根同类帧照常。比较保留帧原始 bytes、来源、重复和 FIFO 相对次序，不能只统计条数。 |
| 未知与混合帧 | child-before-confirmation 隔离且不回填；已入 Session 但来源不充分的帧保留。加入 unknown 字段/块、decode error、未列方法、known member unknown parent、sibling 和 foreign。mixed 详细+tool_result/关系/terminal/error 整帧与 Raw 不变；身份不从字符串猜。 |
| 原请求权限 | 过滤开启时真实 child 原审批仍单次 Approve/Deny/Answer，保留取消 context、已答/撤销/终止和 OutcomeUnknown；观察及历史无回答权。普通 tool_use 不变成权限请求；未知来源请求保持原 registry 规则。 |
| 生命周期 | 父 spawn/wait/Agent 结果、关系/身份及子成功/失败/取消保留。root terminal 后仍可 Receive 子 terminal；背景未触发不能断言后台完成。慢消费者不阻碍控制回复/取消；Close 排空，Abort 丢 backlog，无额外队列。 |

三个证据 seam 分开：公开 Receive 判定过滤效果；原始 peer trace/录制原 bytes
判定源输出及未改写；独立本地文件 API 判定 child saved content。合成 mock 检查不证明
真实 child emitted/received/parent consumed。相同原始 capture
分别跑默认/过滤可比较保留 ordinal 子序列，不重写原录制以伪装过滤输入。

<a id="child-detail-真实拓扑与取证"></a>

### 真实拓扑与取证

以下拓扑规定需要观察的证据；执行与发布规则统一遵循[录制模块设计](../agent-sdk-testify/design.md#story-官方-sdk-仿真测试)。

- direct：root 请求 A 返回固定标记 ALPHA；记录创建证据、子文本、父工具结果和 terminal。
- nested/parallel：root 请求 A、B，A 请求 C，分别只返回 ALPHA/BRAVO/CHARLIE；
  记录每条实际父边、累计 attempts 和深度，不能从输出词推断成功创建。
- parent-first/background：root 返回 ROOT，背景 A 后续返回 ALPHA；分别观察根结果、
  父工具结果、子 terminal/失败和实际背景状态。按已冻结场景等待后续证据；等待结束或显式限制触发时报告缺口，不追加输入或重试强造。
- Claude storage：对每次运行实际新建 child 文件、sidecar 与 wire trace 分别取证，
  文件 agentID/父 UUID 用类型化映射关联；文件不存在/身份未知是缺口。

能力已实现、原生版本不支持、条件未触发、认证或原生策略拒绝、显式限制触发、仅有合成证据与保留的真实片段分别报告。未观察到完成不是成功背景验证，未实际运行不是 live pass。
