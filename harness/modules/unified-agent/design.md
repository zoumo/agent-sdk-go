---
type: Module Design
title: 统一 Agent Provider、Session 与事件流
description: >-
  在原生 Codex 和 Claude SDK 之上定义可选的统一会话与事件层，
  SDK 保留原生历史查询但不提供历史抽象，MASS 保有历史导入和持久化语义。
status: stable
---

# 统一 Agent Provider、Session 与事件流

本页定义 `agent-sdk-go` 在现有原生 `codex`、`claude` 及其 `types` 包之上增加
一个可选的统一实时层，同时明确 MASS 继续保留的 Runtime 和历史合同。统一层
只覆盖单个 Agent Session 的常用操作和实时观察，不提供统一历史查询、历史导入、
历史 replay 或持久化。

本设计同时包含一项原生 SDK 清理：完整删除尚未发布的内部 history replay 投影
及相关类型，不保留 deprecated 兼容层。原生 history query API、协议字段、查询
覆盖和 raw 数据保留。删除依据见
[独立历史查询](../../contracts/history-query.md)：保存内容不能投影成 live
事件、历史完成事件或完整 wire 回放。

MASS 是统一层的首个消费者，不是统一层的定义者。MASS 的 `Address`、资源身份、
恢复材料、历史导入、`SessionEvent` wire、EventStore、Broadcast、SSE 和 Service
编排继续由 MASS 拥有。MASS 的设计依据是 `mass/harness/modules/runtime/` 下的
Runtime、SessionEvent 和交互合同，以及 `mass-go/pkg/runtime` 的现有接口。

本页描述稳定的目标合同，不把目标等同于验证结果；当前使用面与离线示例由
README 和包文档说明。现有原生 Session 的语义以
[Session 合同](../native-session/design.md)为准，仓库当前依赖边界见
[开发规范](../../architecture.md)，共同能力目标见
[SDK 架构](../../sdk/architecture.md)，原生历史查询边界见
[独立历史查询](../../contracts/history-query.md)。

## 结论

1. 在 `agent-sdk-go` 增加可选的 `agent` 统一核心，以及
   `agent/codex`、`agent/claude` 两个原生适配层。
2. 只迁移 MASS Runtime 中与具体 provider 无关的单 Session 能力：
   Provider/Session 生命周期、原生事件转换、单 Session 工作归属、状态投影、
   交互引用和回答句柄。统一事件语义以 MASS 现有 `SessionEvent` 的 payload
   为基础，但去掉 MASS 专属信封字段。
3. 不整体搬迁 MASS Runtime。MASS 的 `RuntimeDriver`、多 Session 汇聚、
   `Address`、运行来源、恢复、显式历史 load、持久化分类和 Service 边界保留。
4. MASS 的两个 Runtime adapter 变成薄的编排层：调用 SDK Provider/Session，
   把统一事件补齐为 `SessionEvent`，继续实现现有 `RuntimeDriver`。
5. 原生 `codex`、`claude`、`codex/types` 和 `claude/types` 仍是第一等公开
   接口。统一层不修改原生会话、原生查询或原生协议字段。
6. 删除 SDK 中未公开的 history replay 实现、只服务 replay 的测试，以及
   `Event.History` / `HistorySource`。不保留 deprecated 包装，因为该能力没有
   公开启动入口，仓库也未发布稳定兼容面。
7. 历史不进入统一层。MASS 对外的历史查询只读 EventStore；原生历史导入继续由
   MASS `LoadSession` 编排。MASS 的具体 Runtime adapter 直接使用原生查询 API：
   Codex 通过 adapter 的 `NativeClient()` 逃生口复用同一条连接，Claude 直接
   调用 package-level 查询。
8. 统一 `agent` 层不提供 `NativeHistory`、`HistoryReader`、`HistoryEvent`、
   `LoadSession` 或 replay。历史转换、replay、持久化、提交和失败释放全部由
   MASS 拥有。
9. 统一 MCP 只配置 external stdio 与 Streamable HTTP；Runtime 默认项和 Session
   声明按 name 合成，Session 同名项整体覆盖。统一层不提供 SSE、strict 或
   config mode，也不保证清除 native ambient MCP。

这里的取舍是：把“一个 Agent Session 如何操作和观察”收敛到 SDK，把“MASS
如何命名、恢复、持久和发布一个 Session”留在 MASS。这样普通使用方只处理一套
常用操作和事件，而 MASS 仍能保有资源模型和运行语义。

## 最终形态

| 层 | 最终公开面 | 负责什么 |
|---|---|---|
| `agent` | `Provider`、`Session`、`EventStream`、`Event`/payload、`Input`、`Answer`、`MCPServer`、错误值 | provider 无关的单 Session 实时合同 |
| `agent/codex` | `New(Config)` 返回具体 `*Provider`，它实现 `agent.Provider` 并提供 `NativeClient()` | Codex 原生连接、Session、事件和交互适配 |
| `agent/claude` | `New(Config)` 返回具体 `*Provider`，它实现 `agent.Provider` | Claude 原生进程、Session、事件和交互适配 |
| 原生 `codex`、`claude` | 现有公开 API、类型和 `types` 包 | 原生协议、查询、原始数据和 provider 语义 |
| MASS Runtime | `RuntimeDriver`、`DriverSession`、`SessionEvent`、`LoadSession` | 资源身份、多 Session 汇聚、恢复、历史导入、持久化和发布 |
| MASS Service | Session、History、EventStore、Broadcast、SSE、HTTP/CLI | 面向 MASS 使用方的业务合同 |

最终只有三种调用方式：

1. 普通 SDK 使用方只依赖 `agent.Provider` 和 `agent.Session`。它看到统一的
   打开/恢复/提交/回答/中断/关闭操作和统一实时事件，不接触原生 handle。
2. MASS 的正常运行路径通过 `RuntimeDriver` 编排 `agent.Provider` 和
   `agent.Session`，MASS Service 仍只消费 `RuntimeDriver.Events()`。
3. MASS 的原生历史导入通过 provider-specific 原生查询读取保存记录，再调用
   `Provider.Resume` 恢复同一执行连接；转换出的仍是 MASS `SessionEvent`，
   不是新的 SDK 历史类型。

不存在第四种“SDK 统一历史流”。MASS 的持久历史查询只读 EventStore；原生
保存内容的查询和导入由 MASS 自己编排。

## 范围与非目标

统一层负责：

- 同一套 Provider 启停、Session 打开、恢复、提交、回答、中断和关闭操作。
- 将 Codex 与 Claude 的原生观察转换为同一套可判别事件。
- 在 SDK 适配器内部保留原生工作归属、交互句柄和单次回答权限。
- 透传由上层合成的最终 external MCP 列表，并明确 provider 字段支持差异。
- 诚实表达能力与功能差异，不在 provider 之间伪造等价。

统一层不负责：

- 修改或替换原生协议、原生 `Received`、原生 Session 或原生 `types`。
- 统一 Codex 与 Claude 的全部创建参数、模型别名、思考预算和子 Agent 查询。
- 统一原生历史查询、历史分页、保存记录格式或历史 wire 模型。
- 将保存历史投影为统一 live 事件、历史完成事件，或可重放、完整的 wire 流。
- MASS 的 namespace、Session 名称、当前运行映射和旧运行隔离。
- EventStore、事件持久化分类、历史分页、Broadcast、SSE、HTTP 或 CLI。
- 显式历史 load、历史 replay、跨连接恢复、自动重连或补历史。
- 原生事件去重、跨运行复用回答权，或从统一事件重建原生控制句柄。
- 并列的 `Submit`/`Steer` 操作、显式 Codex `turn/steer`、SDK 输入队列、
  等待空闲、输入持久化和自动重试。
- MCP 连接、OAuth、健康检查、tool discovery、tool 调用或
  application-hosted MCP 的 JSON-RPC 实现。

## 分层与依赖

```text
codex/types <-- codex          claude/types <-- claude
                   ^                              ^
                   |                              |
             agent/codex                    agent/claude
                   \                              /
                    \                            /
                          agent
                             ^
                             |
             MASS runtime/codex  runtime/claude
                             ^
                             |
          MASS Service / EventStore / Broadcast / transport
```

目录、import 方向和依赖门禁由[开发规范](../../architecture.md)维护。上图只
说明本设计的调用方向：统一 `agent` 是上层实时合同，原生 SDK 不反向依赖它；
两个 adapter 只适配各自 provider；MASS Runtime adapter 可以消费统一层，而
MASS Service、Store 和 transport 仍只依赖 MASS 自己的合同。MASS 的配置、
资源身份和恢复解释在构造 Provider 前完成，不进入 SDK adapter。

## 为什么不整体搬迁 MASS Runtime

MASS Runtime 中有两层复杂度，不应一起搬进通用 SDK。

| MASS 逻辑 | 目标归属 | 原因 |
|---|---|---|
| 原生协议、未知字段、原生身份 | 原生 SDK | 已由各协议拥有，不能再复制一层解析 |
| Provider 启停与共享连接所有权 | 统一 SDK | 与具体 Agent 相关，但与 MASS 无关；Claude 的每 Session 进程随 Session 释放 |
| 单 Session 打开、恢复、提交、回答、中断、关闭 | 统一 SDK | 两个 provider 可以共享操作概念 |
| Turn/响应/block 的工作归属 | provider adapter | 必须依赖各自原生证据，不能放入通用协商层 |
| 原生事件到统一事件 | provider adapter | 这是本次要迁移的主要重复代码 |
| 原生交互句柄与单次回答权限 | provider adapter | 权限必须绑定原连接、原 Session、原工作 |
| 多 Session 汇聚与公平交付 | MASS RuntimeDriver | 这是 Runtime 对 Service 的单流合同 |
| `Address`、namespace/sessionName | MASS | MASS 资源身份，不属于 Agent |
| 运行来源、旧运行隔离 | MASS Runtime adapter | 绑定 MASS 当前运行，不进入 SDK 信封 |
| 恢复材料和恢复身份校验 | MASS Runtime adapter | 涉及 MASS 目录、配置和资源生命周期 |
| 显式历史 load 与 replay | MASS | 包含 MASS load 生命周期、提交和发布顺序 |
| `SessionEvent` wire、持久化分类 | MASS | 已发布的外部合同，不能由 SDK 改写 |
| EventStore、Broadcast、SSE、Service | MASS | 应用域职责，与通用 Agent Session 无关 |

如果整体迁走 `RuntimeDriver`，SDK 会被迫导入 MASS 的资源身份、恢复目录、
事件游标和 Service 生命周期，通用层将不再是通用层。反过来，只迁走单 Session
适配和转换，可以删掉两个 Runtime adapter 中真正重复的部分，同时不改变 MASS
对 Service 的接口。

## SDK 构造与 Provider

统一核心不提供跨 provider 的万能构造函数。具体 adapter 接受各自的显式原生
配置，避免共享接口携带 `any` 或伪造共同字段：

```text
agent/codex.New(Config)   -> *agentcodex.Provider, implements agent.Provider
agent/claude.New(Config)  -> *agentclaude.Provider, implements agent.Provider
```

`Config` 由具体 adapter 定义，可承载可执行文件、环境、初始化参数和原生连接
选项。MASS 负责把 RuntimeConfig 解析成这些显式值，包括 Runtime MCP 默认项、
Session 覆盖、secret 引用解析和环境继承策略；SDK 不解析 MASS 配置。

### Provider 接口

```go
package agent

type Provider interface {
    // Start 取得本 Provider 拥有的运行资源并进入 started 状态。
    // 重复调用返回同一能力集合，不创建第二个资源所有者。
    Start(ctx context.Context) (Capabilities, error)

    // Open 创建新的原生主 Session，并在真实身份确认后返回。
    Open(ctx context.Context, options OpenOptions) (Session, error)

    // Resume 恢复已存在的原生主 Session，不以新建代替恢复。
    Resume(ctx context.Context, options ResumeOptions) (Session, error)

    // Stop 只释放本 Provider 拥有的资源。
    // 仍有未关闭 Session 时必须拒绝停止。
    Stop(ctx context.Context) error
}
```

Provider 可以跨 Session 共享原生连接（Codex），也可以只管理 Session 生命周期
（Claude）；后者由每个 Session 拥有并在 `Close` 中释放自己的原生进程。

`Open` 与 `Resume` 的公共选项使用同一结构；provider 只能部分解释的字段由
adapter 在原生副作用前校验，不由共享接口伪造默认值：

```go
type OpenOptions struct {
    Cwd            string
    SystemPrompt   string
    AdditionalDirs []string
    Features       Features
    MCPServers     []MCPServer
}

type ResumeOptions struct {
    SessionID string
    OpenOptions
}

type Features struct {
    // nil 使用该 provider/schema 的默认值；当前默认 true。
    AgentQuestions *bool
}

type EffectiveFeatures struct {
    AgentQuestions bool
}
```

规则：

- `Cwd` 必须由调用方明确给出且为绝对路径；provider 不接受隐式工作目录。
- `Resume` 必须定向并确认原原生身份；恢复响应中的保存历史不转换为实时事件，
  也不重建旧交互权限。普通恢复只交付恢复后的新观察。
- 原生 resume 支持排除历史时，adapter 必须显式关闭保存内容的回放；如果原生
  连接仍收到保存记录，adapter 只能把它当作恢复/查询材料，不能生成统一 live
  `Event`、当前工作状态或回答权限。
- 已知不支持的系统提示、附加目录或其他选项，在原生副作用前明确拒绝。
- 请求关闭某项功能必须得到 false；请求开启但 adapter 不支持时，允许降级为
  false，并通过 `Session.EffectiveFeatures()` 返回。
- 创建时的原生专用选项不进入共享 `OpenOptions`。具体 adapter 可以提供额外
  的创建方法，但必须返回同一个 `agent.Session`，不能形成第二套通用接口。
- `OpenOptions.MCPServers` 是 Runtime 默认项与本次 Session 声明合成后的最终
  external MCP 列表；adapter 不再解释来源或执行第二种合并策略。
- Provider 配置在构造时复制并固定，调用方后续修改原对象不影响运行。
- `Open` 和 `Resume` 要求 Provider 已启动；停止成功后不能重新 `Start`，也不能
  继续 `Open` 或 `Resume`。没有未关闭 Session 时，重复 `Stop` 返回成功。

### MCP 配置

<a id="mcp-configuration"></a>

统一层只声明由原生 Codex 或 Claude 启动或连接的 external MCP server，不实现
MCP 连接、OAuth、健康检查、tool discovery、tool 调用或 application-hosted SDK
MCP 的 JSON-RPC 回答。后者的原始 `mcp_message` 控制仍由原生 Claude Session
和应用处理，不进入统一 MCP 配置。

```go
type MCPTransport string

const (
    MCPTransportStdio MCPTransport = "stdio"
    // MCPTransportHTTP 是 MCP Streamable HTTP，不是旧式 HTTP+SSE。
    MCPTransportHTTP MCPTransport = "http"
)

type MCPServer struct {
    Name              string
    Transport         MCPTransport
    Command           string
    Args              []string
    Env               map[string]string
    Cwd               string
    URL               string
    Headers           map[string]string
    EnvHeaders        map[string]string
    BearerTokenEnvVar string
}
```

统一层不定义 SSE transport。Claude 原生包仍可声明旧式 SSE server，但统一
Provider 只支持两个 provider 共同具备的 stdio 与 Streamable HTTP；需要 SSE
的调用方直接使用原生 `claude` Session。统一层也不定义
`MCPConfigMode`、`inherit`/`replace`、`StrictMCPConfig` 或任何清空 native
ambient MCP 的开关。

<a id="mcp-runtime-overlay"></a>

#### Runtime 与 Session 合成

Runtime 默认 MCP 属于构造 Provider 之前的 Runtime 配置解释，不进入 Provider
或 Session 状态。负责解析 RuntimeConfig 的上层必须在每次 `Open`、`Resume`
以及通过 `Provider.Resume` 完成的显式 load 之前合成最终列表：

- 不使用 Runtime 默认项的调用方直接把完整列表放入
  `OpenOptions.MCPServers`，Provider 不自行补充默认 MCP。
- 合成键是区分大小写的 server `Name`。
- Session 声明与 Runtime 默认项同名时，Session 项整体替换 Runtime 项，不做字段
  级合并，也不继承 Runtime 项未声明的 Args、Env、Headers 或其他字段。
- 只在 Runtime 中出现的 server 继续保留；只在 Session 中出现的 server 追加。
- Runtime 列表和 Session 列表分别要求 name 唯一；跨层同名合法，并由 Session
  项确定最终值。
- Session 列表为空表示不覆盖 Runtime，不能表示“删除全部 Runtime MCP”。需要
  空集合时由 Runtime 配置本身提供空集合。
- Session 项也不能删除单个 Runtime 默认项；统一层不定义 tombstone、禁用标记
  或按 name 删除操作。需要减少默认项时修改 Runtime 配置。
- Provider 只接收合成后的 `OpenOptions.MCPServers`，不知道某一项来自 Runtime
  还是 Session，也不再次覆盖、去重或继承。

该规则只合成调用方拥有的 Runtime 配置。Codex 或 Claude 自己从用户、项目、
企业设置发现的 [native ambient MCP](../../../GLOSSARY.md)仍由原生 CLI 管理。
集中声明的最终列表不能保证整体替换或删除 ambient 同名项：Codex 会按字段递归
merge，Claude 的同名优先级属于目标 CLI 的实现行为且可能受企业策略拒绝。
需要确定性 server 集合时，调用方必须隔离原生配置来源或外层执行环境。

#### Provider 映射

| 统一字段或能力 | 约束 | Codex | Claude |
|---|---|---|---|
| `Name` | 非空、合法 UTF-8、无 `.`、列表内唯一 | `mcp_servers.<name>` key | `mcpServers.<name>` key |
| `Transport` | 必须显式提供 | `stdio` 选择 command，`http` 选择 URL | 生成 `type:"stdio"` 或 `type:"http"` |
| `Command` / `Args` / `Env` | 只用于 stdio | 同名字段 | 同名字段 |
| `Cwd` | 只用于 stdio | `cwd` | 不支持 |
| `URL` | 只用于 HTTP | `url` | `url` |
| `Headers` | 只用于 HTTP | `http_headers` | `headers` |
| `EnvHeaders` | 只用于 HTTP；值是环境变量名 | `env_http_headers` | 不支持 |
| `BearerTokenEnvVar` | 只用于 HTTP；值是环境变量名 | `bearer_token_env_var` | 不支持 |
| 旧式 HTTP+SSE | 不属于统一 transport | 不支持 | 原生包支持，统一层不暴露 |

adapter 必须拒绝无法表达的字段，不能静默丢弃或改写成其他字段。`Cwd` 与
`EnvHeaders`、`BearerTokenEnvVar` 存在于统一结构是为了保留 Codex 的原生安全
引用能力；Claude adapter 在启动进程前返回 `unsupported`。

#### 校验、生命周期与敏感值

- `Transport` 不能由 `Command`/`URL` 推断；未知值返回 `invalid_argument`。
- stdio 必须有非空 `Command`，禁止 `URL`、`Headers`、`EnvHeaders` 和
  `BearerTokenEnvVar`。
- HTTP 必须有非空 `URL`，禁止 `Command`、`Args`、`Env` 和 `Cwd`。
- 文本、名称、map key 与 value 必须是合法 UTF-8。结构校验和 provider 字段
  支持检查都在原生进程启动或 thread RPC 前完成。
- slice 和 map 在进入 Provider 时复制；调用方后续 mutation 不影响已经开始的
  Open/Resume。
- SDK 不解析、展开或持久化 Env/Header secret。错误只包含 server 序号、字段名、
  provider 和 reason，不包含配置值；MCP 配置不进入统一 Event、Meta 或日志。
- Claude 的 `--mcp-config` 位于进程 argv，调用方应使用原生环境引用；SDK 不能
  承诺 argv 保密。Codex literal `Env`/`Headers` 会成为 native config，优先使用
  `EnvHeaders` 或 `BearerTokenEnvVar`。
- `Open`/`Resume` 成功不等于 MCP server 已连接、认证成功或暴露了工具，也不
  新增统一 Capability 或 tool discovery 结果。
- Resume 必须重新提供完整最终列表；SDK 不保存上次 Session 的 MCP 配置。
  Codex 对仍被观察或运行的 warm loaded thread 可能忽略 resume config，统一层
  不承诺运行中热替换 MCP tool set。

### 可显式带入初始输入

初始输入是操作，不是配置字段。需要先提交输入才能确认原生身份的 provider
通过可选接口提供：

```go
type InitialInputProvider interface {
    // 只校验，不打开 Session、不写原生协议、不读取恢复材料。
    ValidateInitialInput(input Input) error

    OpenWithInput(
        ctx context.Context,
        options OpenOptions,
        input Input,
    ) (Session, error)

    ResumeWithInput(
        ctx context.Context,
        options ResumeOptions,
        input Input,
    ) (Session, error)
}
```

输入必须在副作用前校验。提交后即使身份确认或清理失败，也不能声称输入未发送，
不能自动重发；失败结果使用 `outcome_unknown` 表达。恢复不能用隐藏 prompt 或
重新提交旧输入代替。

### 能力与功能

```go
type Capabilities map[string]bool

const (
    CapabilityTextInput          = "textInput"
    CapabilityImageInput         = "imageInput"
    CapabilityAudioInput         = "audioInput"
    CapabilityResourceInput      = "resourceInput"
    CapabilityResourceLinkInput  = "resourceLinkInput"
    CapabilityPermission         = "permission"
    CapabilityForm               = "form"
    CapabilityURL                = "url"
)
```

- `Start` 返回 Provider 的基础支持面；`Session.Capabilities()` 返回该 Session
  实际可用的支持面，可以等于或窄于 Provider。
- 缺失或 false 都表示调用方不能假定支持。能力存在也不绕过当前 Session 状态。
- `EffectiveFeatures` 表达本次请求最终是否启用，不能仅从 `Capabilities`
  推断。
- 当前两个 provider 首版只宣告 `textInput`、`permission` 和 `form`；没有
  经 adapter 证明的图片、音频、资源、URL 或其他能力不宣告。

## Session

```go
type Session interface {
    // ID 是已经确认的原生主 Session 身份。
    ID() string

    Capabilities() Capabilities
    EffectiveFeatures() EffectiveFeatures

    // Events 每个 Session 只能成功取得一次。
    Events() (EventStream, error)

    StartOrSteer(ctx context.Context, input Input) (InputReceipt, error)
    Answer(
        ctx context.Context,
        interaction InteractionRef,
        answer Answer,
    ) (InteractionReceipt, error)
    Interrupt(
        ctx context.Context,
        options InterruptOptions,
    ) (InterruptReceipt, error)

    // Close 释放本 Session 的原生附着或自有资源。
    // 它不停止 Provider，也不递归停止全部子 Agent。
    Close(ctx context.Context) error

    // Done 表示 Session 交互终止；Err 是稳定的首个终止原因。
    Done() <-chan struct{}
    Err() error
}
```

### 生命周期与状态

- `ID` 只能在原生真实身份确认后非空。Codex 以启动/恢复响应确认身份，Claude
  以原生 init 确认身份；返回 Session 之前不能伪造 ID。
- 每个 Session 只允许一个本地 `StartOrSteer` 写入在进行。并发调用返回
  `not_applied` 和空 `Submission`，不等待、不写第二帧，也不形成本地输入队列。
- `StartOrSteer` 先尝试加入可接受的当前工作；没有当前工作或原生调度可接收时，
  启动新工作或交给原生 best-effort 调度。没有当前工作时启动新工作是正确结果，
  不是错误。
- `StartOrSteer` 成功只表示输入已按该原生语义提交，不表示工作开始或完成。
  当前工作、新工作、best effort 和未应用是不同事实；收到消息 delta 和观察到
  工作开始仍是独立观察。
- 原生结果未知时返回 `outcome_unknown`，不自动重试。在 adapter 再次取得原生
  工作状态证据前，后续输入返回 `not_applied`，不写原生帧。
- `Interrupt` 只使用本 Session 已经确认的当前工作，不用最后收到的事件猜目标。
  Claude 的 best-effort 输入不证明特定当前工作已开始，但 adapter 会把该提交
  保留为交互归属与中断的候选目标，直到原生 idle 清除；没有候选或已确认工作
  时返回 `no_active_work`，不发出原生中断。
- `Close` 停止新操作并使未结算交互失效，不等待外部消费者。已经接纳的事件仍
  可以排空；原生清理结果与业务执行结果分别表达。重复 `Close` 返回同一清理
  结果。
- `Done`/`Err` 描述 Session 交互终止，不表示根任务成功。

### 回执

```go
type Acceptance struct {
    Sent           *bool
    NativeAccepted *bool
    NativeRecorded *bool
}

type InputReceipt struct {
    Submission string
    Result     InputResult
    Acceptance Acceptance
}

type InputResult string

const (
    InputAppliedCurrentWork InputResult = "applied_current_work"
    InputAppliedNewWork     InputResult = "applied_new_work"
    InputAppliedBestEffort  InputResult = "applied_best_effort"
    InputNotApplied         InputResult = "not_applied"
)

type InteractionReceipt struct {
    Interaction InteractionRef
    Acceptance  Acceptance
}

type InterruptOptions struct{}

type InterruptResult string

const (
    InterruptAcknowledged InterruptResult = "control_acknowledged"
    InterruptNoActiveWork InterruptResult = "no_active_work"
)

type InterruptReceipt struct {
    Target string
    Result InterruptResult
}
```

- `Sent`、`NativeAccepted`、`NativeRecorded` 是三种独立观察；nil 表示未观察，
  false 表示有明确否定证据。
- 不得把 `Sent=true` 提升为原生接受，也不得用后续观察补造此前阶段。
- Codex 能确认原生 turn 接受时填写 `NativeAccepted`；Claude 写入成功但不能
  确认 CLI 接受时，只填写能够证明的阶段。
- `InputNotApplied` 的 `Submission` 为空；调用方保留输入并自行决定何时重试。
  本地并发或未知状态拒绝只填 `Sent=false`；原生明确拒绝填 `Sent=true`、
  `NativeAccepted=false`，不补 `NativeRecorded`。
- `InputAppliedCurrentWork` 只在响应工作身份与已观察的活动工作一致时使用。
  已观察工作与响应身份不同，或此前没有当前工作时，使用
  `InputAppliedNewWork`。
- `InputAppliedBestEffort` 只证明本地写入完成。Claude 不因写入成功而声称当前
  turn 或下一 turn 已被原生调度；adapter 可以在内部保留该提交作为交互归属与
  `Interrupt` 的候选目标，该内部事实不改变回执。
- 取消确认不证明前台执行已经终止。原生结果未知时返回 `outcome_unknown`，
  不自动重试。

### 输入操作的 provider 实现

- Codex 的 `StartOrSteer` 只调用原生 `turn/start`，不依据本地 Busy 推断是否
  切换方法，也不自动改用其他原生调用。原生 `turn/start` 在可接受的当前 turn
  上按 start-or-steer 语义接收输入；响应 turn identity 与操作前已观察的活动
  turn 一致时为 `applied_current_work`，否则为 `applied_new_work`。
- Codex 能识别的原生明确拒绝（review/compact turn 不可并入、预期 turn 不匹配、
  输出 schema 不一致或当前无法接收输入）返回 `not_applied`，保留 `Sent=true`、
  `NativeAccepted=false`；响应缺少 turn identity 或写入结果未知时返回
  `outcome_unknown`，不重试。
- Claude 的 `StartOrSteer` 写入原生 user input，使用 `priority="next"` 和
  `shouldQuery=true`，由 CLI 决定并入当前工作还是排入下一轮。写入成功返回
  `applied_best_effort`；adapter 不伪造当前或下一 turn 身份，也不填写无法
  证明的 `NativeAccepted`。内部保留该提交作为交互归属与中断的候选目标，
  直到原生 idle 清除。
- 两个 adapter 共用同一组四个结果值，不增加 provider 专属结果。原生 state
  观察、消息 delta 和工作完成仍是输入回执之外的独立事实。

输入操作的取舍和未采用方案见
[ADR 0003](../../adr/0003-unified-start-or-steer.md)。

## 输入与内容块

```go
type Input struct {
    Content []ContentBlock
}

type ContentBlock interface {
    contentBlock()
}

type TextBlock struct {
    Text        string
    Annotations *Annotations
}

type ImageBlock struct {
    Data        string
    MIMEType    string
    URI         *string
    Annotations *Annotations
}

type AudioBlock struct {
    Data        string
    MIMEType    string
    Annotations *Annotations
}

type EmbeddedResource struct {
    URI      string
    MIMEType *string
    Text     *string
    Blob     *string
}

type ResourceBlock struct {
    Resource    EmbeddedResource
    Annotations *Annotations
}

type ResourceLinkBlock struct {
    URI         string
    Name        string
    MIMEType    *string
    Title       *string
    Description *string
    Size        *int64
    Annotations *Annotations
}

type Annotations struct {
    Audience     []string
    Priority     *float64
    LastModified *time.Time
}
```

内容块采用与 MASS 共享合同相同的 ACP 五种类型，不增加任意 JSON 块。数组顺序
是内容顺序；`image`、`audio` 的 `Data` 和资源的 `Blob` 是不带 data URI 前缀的
Base64 原文。`resource` 必须且只能在 `Text`、`Blob` 中提供一种正文。

`Input.Content` 不能为空。调用时会复制并校验内容；已知不支持的类型在原生写入
前拒绝，不能静默删块、改写为文本或把限制交给下游猜测。当前 MASS 迁移首版
沿用两个 Runtime 的文本输入能力，因此 adapter 只宣告 `textInput`。

## 交互与回答权限

```go
type InteractionRef string

type InteractionKind string

const (
    InteractionPermission InteractionKind = "permission"
    InteractionForm       InteractionKind = "form"
    InteractionURL        InteractionKind = "url"
)

type PermissionOption struct {
    OptionID string
    Name     string
    Kind     PermissionOptionKind
}

type PermissionOptionKind string

const (
    PermissionAllowOnce    PermissionOptionKind = "allow_once"
    PermissionAllowAlways  PermissionOptionKind = "allow_always"
    PermissionRejectOnce   PermissionOptionKind = "reject_once"
    PermissionRejectAlways PermissionOptionKind = "reject_always"
)

type InteractionRequest struct {
    Kind            InteractionKind
    ToolCallID      string
    Title           string
    Options         []PermissionOption
    Content         []ContentBlock
    Message         string
    URL             string
    RequestedSchema map[string]any
}

type Answer interface {
    answerKind() InteractionKind
}

type PermissionOutcome string

const (
    PermissionSelected  PermissionOutcome = "selected"
    PermissionCancelled PermissionOutcome = "cancelled"
)

type PermissionAnswer struct {
    Outcome  PermissionOutcome
    OptionID string
}

type FormOutcome string

const (
    FormAccept  FormOutcome = "accept"
    FormDecline FormOutcome = "decline"
    FormCancel  FormOutcome = "cancel"
)

type FormAnswer struct {
    Outcome FormOutcome
    Content map[string]any
}

type URLOutcome string

const (
    URLAccept  URLOutcome = "accept"
    URLDecline URLOutcome = "decline"
    URLCancel  URLOutcome = "cancel"
)

type URLAnswer struct {
    Outcome URLOutcome
}

type InteractionOutcome string

const (
    InteractionAnswered  InteractionOutcome = "answered"
    InteractionCompleted InteractionOutcome = "completed"
    InteractionCancelled InteractionOutcome = "cancelled"
    InteractionExpired   InteractionOutcome = "expired"
    InteractionFailed    InteractionOutcome = "failed"
)
```

`InteractionRequest` 按 `Kind` 校验字段组合：

- `permission` 必须有 `ToolCallID` 和至少一个原生命令选项；只接受原始请求中
  实际提供的 `optionId`，不能凭 kind 推断授权。
- `form` 必须有 `Message` 和 `RequestedSchema`；adapter 只校验自己确能表达的
  schema 子集，不宣称实现任意 JSON Schema。多选问题可接受字符串或字符串数组；
  adapter 将字符串数组以 `", "` 连接成字符串后再写回原生。原生数组答案仍拒绝，
  不能把统一层的兼容编码当成原生数组支持。
- `url` 只表达外部流程入口；URL 的完成仍由原生事实结算。

原生 `IncomingRequest`、`AnswerHandle` 和 permission handle 只存在于 adapter
私有 registry。统一事件只交付 `InteractionRef`。该引用绑定 Session、adapter
实例、当前运行和具体工作；回答前必须再次检查原句柄仍有效。跨 Session、旧运行、
过期请求、已回答请求和伪造引用全部拒绝。

并发回答在单个交互内串行接纳，至多一个回答进入原生提交。事件可以先于 answer
回执到达。发送结果未知时不恢复回答权限，不自动重答。当前 adapter 不宣告
`url`，除非以后有可验证的原生实现。

## 事件流与统一事件

```go
type EventStream interface {
    // io.EOF 表示正常结束；其他 error 表示稳定的异常终止原因。
    // 同一个流不得并发 Next。
    Next(ctx context.Context) (Event, error)
}
```

- 每个 Session 只有一条事件流和一个消费者。重复调用 `Events()` 返回错误，
  不创建第二条队列、第二个原生订阅或第二个回答权。
- `Events()` 不能启动 Agent，也不能改变原生执行。Session 打开后、调用
  `Events()` 前到达的早期事实必须按原顺序保留在有界原生/身份暂存中，之后从
  同一流交付，不能丢弃或补造。
- 读取等待取消只结束本次等待，不取消 Agent 工作，也不关闭 Session。
- 正常关闭在已接纳事件排空后返回 `io.EOF`；异常终止返回稳定错误，并可继续
  通过 `Done`/`Err` 读取。终止后不自动重连或补历史。
- 统一层只有单 Session 流。多个 Session 的公平汇聚由 MASS Runtime 完成，
  SDK 不提供广播。

### 事件信封

```go
type Event struct {
    EventID   string
    SessionID string
    Time      time.Time
    ItemID    string
    Payload   Payload
    Meta      map[string]any
}

func (e Event) Type() EventType
func (e Event) Validate() error
```

规则：

- `EventID` 在 adapter 创建统一事件时生成一次，格式为 UUID 字符串。它只标识
  唯一性，不表示顺序，也不能替代 MASS 的历史游标。
- `Time` 优先使用原生事件时间；缺失时由 adapter 生成 UTC 时间。MASS 接收后
  不得替换。
- `SessionID` 是已确认的原生主身份。适配器不能返回无法与事件核对身份的 Session。
- `ItemID` 用于消息、思考和工具等条目事件；会话级通知不虚构 itemId。
- `Payload` 的类型唯一决定 `Event.Type()`，不能任意组合 type 与 payload。
- `Meta` 是可 JSON 编码的 `map[string]any`，保留 adapter 来源、原生 raw、父子
  身份和诊断扩展；不得放凭据、恢复材料或不可编码值。
- 同一 Session 内的已接纳事件保持原生接收顺序。不同 Session 不定义全局顺序。
- 统一层不提供 `Persistent()`、replay、seq 或 Store 规则。

评测和迁移比较时可以忽略随机 `EventID`，但必须比较类型、payload、身份、来源、
未知数据和顺序；不能忽略影响历史或回答权限的字段。

### 事件与 payload

```go
type EventType string

const (
    EventUserMessage                EventType = "user.message"
    EventAgentMessage               EventType = "agent.message"
    EventAgentThinking              EventType = "agent.thinking"
    EventAgentToolCall              EventType = "agent.tool_call"
    EventAgentToolResult            EventType = "agent.tool_result"
    EventAgentMessageChunk          EventType = "agent.message_chunk"
    EventAgentThinkingChunk         EventType = "agent.thinking_chunk"
    EventAgentToolCallChunk         EventType = "agent.tool_call_chunk"
    EventSessionStateUpdate         EventType = "session.state_update"
    EventSessionError               EventType = "session.error"
    EventSessionInfoUpdate          EventType = "session.info_update"
    EventSessionConfigOptionUpdate  EventType = "session.config_option_update"
    EventSessionCommandsUpdate      EventType = "session.available_commands_update"
    EventSessionUsageUpdate         EventType = "session.usage_update"
    EventInteractionRequested       EventType = "session.interaction_requested"
    EventInteractionClosed          EventType = "session.interaction_closed"
)

type Payload interface {
    eventType() EventType
}
```

payload 与 MASS `SessionEvent` 中的同名 payload 保持字段语义一致，但不包含
MASS 信封字段：

```go
type MessagePayload struct {
    MessageID string
    Content   []ContentBlock
    Phase     string
}

type UserMessage MessagePayload
type AgentMessage MessagePayload
type AgentThinking MessagePayload
type MessageChunk MessagePayload
type ThinkingChunk MessagePayload

type ToolOutput struct {
    Availability string
    Completeness string
    Truncation   *Truncation
}

type Truncation struct {
    RetainedBytes int64
    OmittedBytes  *int64
}

type ToolKind string

const (
    ToolKindRead       ToolKind = "read"
    ToolKindEdit       ToolKind = "edit"
    ToolKindDelete     ToolKind = "delete"
    ToolKindMove       ToolKind = "move"
    ToolKindSearch     ToolKind = "search"
    ToolKindExecute    ToolKind = "execute"
    ToolKindThink      ToolKind = "think"
    ToolKindFetch      ToolKind = "fetch"
    ToolKindSwitchMode ToolKind = "switch_mode"
    ToolKindOther      ToolKind = "other"
)

type ToolProvider string

const (
    ToolProviderAgent   ToolProvider = "agent"
    ToolProviderDynamic ToolProvider = "dynamic"
    ToolProviderMCP     ToolProvider = "mcp"
)

type ToolCallPayload struct {
    ID                  string
    Kind                ToolKind
    ToolName            string
    Provider            ToolProvider
    Title               string
    Phase               string
    InputRepresentation string
    RawInput            any
    RawOutput           *any
    Content             []ContentBlock
    ExitCode            *int32
    DurationMs          *float64
    Output              *ToolOutput
    Status              string
}

type ToolResultPayload struct {
    ID                  string
    Kind                ToolKind
    ToolName            string
    Provider            ToolProvider
    Title               string
    Phase               string
    InputRepresentation string
    RawInput            *any
    RawOutput           *any
    Content             []ContentBlock
    ExitCode            *int32
    DurationMs          *float64
    Output              *ToolOutput
    Status              string
}

type ToolChunkPayload struct {
    ID      string
    Content []ContentBlock
}

type SessionState string

const (
    StateIdle           SessionState = "idle"
    StateRunning        SessionState = "running"
    StateRequiresAction SessionState = "requires_action"
)

type ObservedError struct {
    Code    any
    Message string
}

type StateUpdatePayload struct {
    PreviousState *SessionState
    State         *SessionState
    StopReason    string
    Error         *ObservedError
}

type SessionErrorPayload struct {
    Message string
    Reason  string
}

type InfoUpdatePayload struct {
    Title     *string
    UpdatedAt *time.Time
}

type ConfigOptionValue struct {
    Value       string
    Name        string
    Description string
}

type ConfigOptionGroup struct {
    Group   string
    Name    string
    Options []ConfigOptionValue
}

type ConfigOption struct {
    ID           string
    Name         string
    Description  string
    Category     string
    Type         string
    CurrentValue any
    Options      any
}

type ConfigOptionUpdatePayload struct {
    ConfigOptions []ConfigOption
}

type AvailableCommand struct {
    Name        string
    Description string
}

type AvailableCommandsUpdatePayload struct {
    AvailableCommands []AvailableCommand
}

type UsageCost struct {
    Amount   json.Number
    Currency string
}

type UsageUpdatePayload struct {
    Used int64
    // Size 为 0 表示 provider 未报告 context-window 大小，不表示测得窗口为零。
    Size int64
    Cost *UsageCost
}

type InteractionRequestedPayload struct {
    Interaction InteractionRef
    Request     InteractionRequest
    Blocking    bool
}

type InteractionClosedPayload struct {
    Interaction InteractionRef
    Outcome     InteractionOutcome
    Reason      string
}
```

每个具体 payload 在所属包内实现固定的 `eventType()`；外部包不能扩展 payload
联合。`user.message`、`agent.message`、`agent.thinking`、`agent.tool_call` 和
`agent.tool_result` 是 MASS 可能持久化的完整事实；SDK 只提供事实，不决定
MASS 是否持久化。

`*_chunk` 只追加，不是累计快照。adapter 不跨事件拼接完整历史，也不用结束后
补造完整消息。工具调用和结果共享 `itemId`，但事件身份不同。完整消息与 chunk
使用相同的 item/message 身份。工具分块不携带完整工具身份，具体例外见
[工具身份、Provider 与 Kind](#工具身份provider-与-kind)。

### 工具身份、Provider 与 Kind

公开 JSON 的工具身份由 `(provider, toolName)` 组成；`kind` 是 SDK 附加的
跨 Agent 动作分类，只用于展示、统计和粗粒度路由提示，不参与权限判断，也不
证明不同 provider 的工具执行行为等价。对应的 Go 字段是 `Provider`、
`ToolName` 和 `Kind`。

`toolName` 必须非空，表示 provider 最具体的原生工具标识，不是跨 provider
规范化的 leaf name。选择顺序固定为：

1. provider 原生显式 `tool` 或 `name` 字段；
2. 可确定的一一对应原生枚举映射；
3. 原生 item type fallback。

只有第 3 种情况使用 fallback。fallback 必须保留原生大小写和拼写，不能把它
描述成模型工具名。Claude 的 `mcp__...` 名称原样保留，不从字符串中剥离 server。

`provider` 表示工具提供方，接受且只接受以下四种形式：

| 值 | 语义 |
|---|---|
| `agent` | Agent runtime 自带的工具 |
| `dynamic` | 应用侧或 runtime 动态注册的工具 |
| `mcp` | 确定是 MCP，但 server 无法可靠确定 |
| `mcp:<server>` | MCP 且 server 已知 |

`mcp:` 非法；`<server>` 必须非空，并保留 `mcp:` 后的全部内容，允许继续
包含 `:`，不得截断或重建。`agent`、`dynamic` 和 `mcp` 是完整值，不能带后缀。
其他 provider 值均非法。新事件必须有非空 `provider`；不增加独立的
`server`、`tool`、`action` 或 `type` payload 字段。MCP 的结构化 server
定位放在 `provider`，而 `toolName` 仍保留 provider 原文。

`kind` 使用 ACP 的取值：

| `kind` | 语义 |
|---|---|
| `read` | 读取文件或数据 |
| `edit` | 修改文件或内容 |
| `delete` | 删除文件或数据 |
| `move` | 移动或重命名 |
| `search` | 搜索信息 |
| `execute` | 运行命令或代码 |
| `think` | 内部推理或规划 |
| `fetch` | 获取外部数据 |
| `switch_mode` | 切换 Session 的 mode 配置 |
| `other` | 其他或无法分类的工具，作为默认值 |

`kind` 只描述动作，不描述资源范围。当前不把 `read`/`edit` 改成
`fs.read`/`fs.write`，也不把 `search` 拆成本地检索和网络检索：

| 工具 | `kind` | 资源范围（仅用于说明，不进入 payload） |
|---|---|---|
| `Grep`、`Glob` | `search` | 本地 |
| `WebSearch` | `search` | 网络 |
| `WebFetch` | `fetch` | 网络 |

`read` 和 `edit` 沿用 ACP 的广义定义：前者表示读取文件或数据，后者表示修改
文件或内容；`fs.*` 会把这些动作错误地限制为文件系统操作。

`search` 表示搜索信息；本地和网络的区别由 `toolName` 或未来单独的 scope
维度表达，不改变 `kind`。`fetch` 表示获取已知外部数据，不能和 `search`
混用。当前没有 scope 字段；如果以后需要做本地/网络权限或路由，再设计独立
的正交字段。

`delete` 和 `move` 当前没有稳定的 Codex/Claude 工具映射；不得从 patch 内容、
命令文本或工具参数推断。`switch_mode` 只用于明确切换 session mode 的工具。

`kind` 按工具本身分类，不解析命令的 `commandActions`、MCP 参数或工具输出。
call 和 result 各自独立要求完整的 `(provider, toolName)` 身份和 `kind`；核心
不从前一事件补造缺失字段。adapter 在观察到 call 时保存身份和分类，result
复制 `provider`、`toolName`、`kind` 而不重新解析原生输出。adapter 测试必须
证明 call/result 的这三个字段相等；其中只有前两个组成工具身份。

#### 工具分块例外

`agent.tool_call_chunk` 是工具事件的身份例外。它的 payload 恰好是
`ToolChunkPayload{ID, Content}`，JSON 只有 `id` 和 `content`，不携带
`kind`、`toolName` 或 `provider`。分块可能早于完整 call 身份出现；调用方按
`id` 关联，不得从部分输入猜测身份。

#### 工具状态边界

工具 `status` 是 provider 原生生命周期的投影，不是工具身份，也不参与
call/result 身份相等判断。支持的值为 `pending`、`in_progress`、`completed`、
`failed`、`declined` 和 `unknown`；call 可以省略 status，result 必须有非空
status。

该枚举不建立跨 provider 的成功语义。Codex 继续使用现有
`in_progress`/`completed`/`failed`/`declined`/`unknown` 投影；Claude 的 call
省略 status，result 只使用 `completed`/`failed`。identity 或 kind 的变化不能
改写 status，status 的变化也不能触发重新分类。

#### JSON schema 与兼容性

`agent.tool_call` 和 `agent.tool_result` 的 JSON payload 都要求 `kind`、
`toolName` 和 `provider`。严格解码拒绝缺失或 null 的身份字段、空
`toolName`、未知 kind、非法 provider、`mcp:` 空后缀，以及旧的 `name` 成员。
旧任意字符串 kind 没有迁移或 alias。

这是一次有意的 breaking schema 变更：新的统一 Agent JSON 不读取旧 `name`，
也不提供 schema version 或兼容 shim。统一 Agent 面仍是未发布的候选接口，
因此不保留双读或自动重写。

#### Codex 映射

| Codex item / 条件 | `ToolName` | `Kind` | `Provider` |
|---|---|---|---|
| `commandExecution`，`source=unifiedExecStartup` | `exec_command` | `execute` | `agent` |
| `commandExecution`，`source=unifiedExecInteraction` | `write_stdin` | `execute` | `agent` |
| `commandExecution`，`source=agent`、`userShell`、缺失或未知 | `commandExecution`（fallback；没有唯一模型工具名） | `execute` | `agent` |
| `imageView` | `view_image` | `read` | `agent` |
| `fileChange` | `fileChange`（fallback） | `edit` | `agent` |
| `webSearch` | `webSearch`（fallback） | `search` | `agent` |
| `sleep` | `sleep`（fallback） | `other` | `agent` |
| `imageGeneration` | `imageGeneration`（fallback） | `other` | `agent` |
| `mcpToolCall` | `item.Tool` | `other` | `mcp:<server>`；server 为空时为 `mcp` |
| `dynamicToolCall` | `namespace + tool` | `other` | `dynamic` |
| `functionCallOutput` | `namespace + name` | `other` | `dynamic` |
| `collabAgentToolCall` | `string(item.Tool)` | `other` | `agent` |

`dynamicToolCall` 和 `functionCallOutput` 的 namespace 与名称直接拼接，不插入
`.`；namespace 为空时分别使用 `tool` 或 `name`。这是 ACP 的 Codex flatten
规则，不是 SDK 自造格式。`commandExecution` 的 `source` 是 Codex 原生字段；
只有 `unifiedExecStartup` 和 `unifiedExecInteraction` 能确定模型工具名。

#### Claude 映射

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

#### 与 ACP adapter 的差异

这里只对齐 ACP 的 `Kind` 枚举，不照搬 ACP adapter 的分类结果。以下是 SDK
有意的差异：

- Codex `commandExecution` 固定为 `execute`；ACP 可能根据 `commandActions`
  分为 `read`、`search` 或 `execute`。
- Codex `mcpToolCall`、`dynamicToolCall`、`functionCallOutput` 固定为
  `other`；ACP 的 Codex adapter 使用 `execute`。
- Claude `WebSearch` 固定为 `search`；ACP 的 Claude adapter 使用 `fetch`。
- Claude `Agent`、`Task` 固定为 `other`；ACP 的 Claude adapter 使用 `think`。

这样可以保持 SDK 分类由工具身份决定，而不随单次参数或命令内容变化。

Codex shell 示例（`source=unifiedExecStartup`）：

```json
{
  "id": "cmd-01",
  "kind": "execute",
  "toolName": "exec_command",
  "provider": "agent",
  "title": "go test ./agent/...",
  "rawInput": {
    "command": "go test ./agent/...",
    "cwd": "/Users/jim/code/zoumo/agent-sdk-go"
  },
  "status": "in_progress"
}
```

若同一 `commandExecution` 的 `source` 为 `agent`、`userShell` 或缺失，则只能
使用 `toolName: "commandExecution"` 这个 fallback，不能伪造
`exec_command`。

统一层暂不定义 Provider 无关的计划语义。Claude 的 `TodoWrite`、
`TaskCreate` 和 `TaskUpdate` 原本就是工具调用，只保留带原生输入的
tool call，不额外生成计划事件或计划缺口。Codex 的独立 plan item、
`item/plan/delta` 和 `turn/plan/updated` 暂不转换为统一事件，也不作为
映射缺口输出。两类原生 Plan 类型和通知继续由各自原生 SDK 保留。依据与后果见
[ADR 0002](../../adr/0002-no-provider-neutral-plan.md)。

会话状态只报告 `idle`、`running`、`requires_action` 或未知（nil pointer），
不报告 MASS Service 的 opening、closing、archived。只有原生工作开始/结束证据
才能改变工作状态；接受回执和 delta 本身不能作为开始/结束证据。

未知原生通知不能被伪装为另一种正常事件。当前统一层只交付上述封闭类别；不能
准确映射的 provider 专用值保留在 `Meta` 诊断材料中，或作为明确的 Session
错误/能力缺口报告，不能静默丢失已知完整事实。

## 错误模型

统一层使用稳定的分类值，不通过解析原生错误文本推断类别：

```go
type ErrorCode string

const (
    ErrorInvalidArgument     ErrorCode = "invalid_argument"
    ErrorFailedPrecondition  ErrorCode = "failed_precondition"
    ErrorUnsupported         ErrorCode = "unsupported"
    ErrorUnavailable         ErrorCode = "unavailable"
    ErrorResourceExhausted   ErrorCode = "resource_exhausted"
    ErrorOutcomeUnknown      ErrorCode = "outcome_unknown"
    ErrorNativeRejected      ErrorCode = "native_rejected"
    ErrorClosed              ErrorCode = "closed"
)

type Error struct {
    Code    ErrorCode
    Reason  string
    Message string
    Cause   error
}

func (e *Error) Error() string
func (e *Error) Unwrap() error
func CodeOf(err error) ErrorCode
```

已知不支持或参数非法必须在原生写入前返回确定错误。`outcome_unknown` 表示可能
已经产生原生副作用，不是确定失败；自动重试会破坏回答、输入或资源释放语义。
`Cause` 保留原生错误，但 `Message` 必须脱敏，不包含凭据或 MASS 恢复材料。

## 能力接口

基础 Session 只声明两个 provider 都能稳定提供的操作。差异化能力使用可选接口：

```go
type ActionReceipt struct {
    Acceptance Acceptance
}

type Archiver interface {
    Archive(ctx context.Context) (ActionReceipt, error)
    Unarchive(ctx context.Context) (ActionReceipt, error)
}

type ConfigurationReceipt struct {
    Model      string
    Acceptance Acceptance
}

type ModelController interface {
    // model 必须是非空原生模型标识；不做跨 provider 别名映射。
    SetModel(ctx context.Context, model string) (ConfigurationReceipt, error)
}
```

不支持的能力不实现接口，或返回 `unsupported`。不得用静默 no-op、空成功或伪造
默认值掩盖差异。归档、模型、思考和子 Agent 查询的 provider 专属语义不进入
基础接口；高级调用方可以使用 adapter 具体类型及其原生入口，但不能绕过基础
Session 的权限和关闭规则。

第一版不定义统一的 `SubagentQuery` 或 `SubagentPage`。需要原生子 Agent 查询、
关系证据或分页覆盖的使用方直接使用对应的原生 SDK；统一层不在两个不同查询
模型之间猜测等价。

### 历史查询不进入统一能力面

统一层不定义 `NativeHistory`、`HistoryReader`、`HistoryEvent`、
`HistoryQuery` 或任何等价的跨 provider 历史抽象。原生历史查询继续由
`codex`、`claude` 的既有公开 API 拥有，调用方必须理解各自的 metadata、
分页、覆盖、错误和身份语义。

不统一的理由是：

- Codex 暴露 thread/turn/item 原生分页，Claude 暴露 transcript message、
  parent 链、项目 scope 和 coverage；强行共用一套值会丢失来源、覆盖或错误语义。
- 历史查询返回保存内容，不是 live 通知。把它映射成统一 `Event` 会诱导调用方
  把历史当成当前执行、当前状态或回答权限。
- MASS 需要 replay 来源、加载状态、提交顺序、取消和失败释放；这些是 MASS
  `LoadSession` 的业务语义，不属于通用 Agent 会话。
- 统一层可以把两个 provider 的实时常用操作做得很好，但无法在不损失语义的
  前提下把两套原生历史查询变成同一个 API。

Codex 的具体 adapter 只为 MASS 提供 native escape hatch：

```go
package agentcodex

// NativeClient 返回 Provider 自有的同一条已初始化连接。
// 只在 Start 成功之后、Stop 之前有效；调用方不得关闭或替换该连接。
func (*Provider) NativeClient() (*codex.Client, error)
```

该方法不在 `agent.Provider` 中定义，也不是历史接口。MASS 可以在自己的
provider-specific Runtime 包内声明只含查询方法的私有接口，并把
`NativeClient()` 的结果绑定到该接口，以限制实际使用面。

Claude 没有对应 client，MASS 直接调用 package-level
`claude.ListSessions`、`GetSessionInfo`、`GetSessionMessages` 和子历史查询，
显式传入 RuntimeConfig 与绝对 cwd 产生的 scope。

`NativeClient()` 的规则：

- 只允许 `ServerInfo`、`ListThreads`、`ReadThread`、`ListThreadTurns` 和
  `ListThreadItems` 这些只读方法。不得调用 `Call` 绕过限制，也不得调用
  `ResumeThread`、`StartThread`、`StartTurn`、`InterruptTurn` 或其他会建立、
  附着、控制 Session 的方法。
- 查询不能创建 `agent.Session`、增加 `Session.Events()` 消费者、发布实时
  事件或授予回答权限。
- MASS 不得另建第二个 `codex.Client` 来读取历史；必须复用 Provider 的生命周期
  和数据域。
- 未启动或已停止时返回错误，不得按需创建第二条连接。
- MASS 只在 provider-specific Runtime 内使用它，不把原生 client、原生查询值或
  查询错误类型暴露到 MASS Service、HTTP 或 SSE。
- 返回值和错误的持有、取消、raw 数据及覆盖边界仍由原生查询合同定义。

## MASS 权威 Runtime 合同

MASS 的 `Driver`、`DriverSession`、`Address`、回执、事件和恢复合同不在本
仓库复制；实现与验收以 MASS 仓库为准：

- [Runtime 设计](https://github.com/zoumo/mass/blob/main/harness/modules/runtime/design.md)
- [Runtime driver 合同](https://github.com/zoumo/mass/blob/main/harness/modules/runtime/runtime-driver.tsp)
- [Runtime events](https://github.com/zoumo/mass/blob/main/harness/modules/runtime/events.md)

本设计只定义 MASS Runtime adapter 如何消费 `agent.Provider` 和
`agent.Session`。`RuntimeDriver`、多 Session 汇聚、`Address`、运行来源、
恢复、显式历史 load、持久化分类和 Service 边界仍由 MASS 拥有。统一层不提供
MASS `Address`、`Source`、回执或恢复文件类型，也不通过类型别名耦合两边。

### MASS 事件模型不变

实时、存储和历史读取仍共用 MASS `SessionEvent`：

```text
agent.Event 的通用信封与 payload
    + namespace
    + sessionName
    + replay
    + MASS 持久化分类
```

MASS adapter 必须逐项保留：

| `agent.Event` | MASS `SessionEvent` |
|---|---|
| `EventID` | 原值，不重新生成 |
| `SessionID` | 原值，并核对当前 Session 绑定 |
| `Time` | 原值，不替换为接收/保存时间 |
| `ItemID` | 原值，缺失时不虚构 |
| `Type` 与 payload | 同一语义与字段 |
| `Meta` | 转成 `_meta`，不得丢字段 |
| 不存在的 `replay` | 仅显式历史 load 由 MASS 设置 |
| 不存在的持久化标记 | 仍由 MASS `Persistent()`/EventStore 规则决定 |

该映射只适用于 `agent.Event` 的 live relay。原生历史导入没有 `agent.Event`；
MASS 按 provider-specific 查询结果和现有 load 规则直接构造 `SessionEvent`。

`namespace`、`sessionName` 和 `replay` 由 MASS Runtime 在事件进入 Driver 流前
补齐。在 live relay 路径，MASS 不为统一事件重新解析原生协议，不重写 payload，
不修改已确认的时间或事件身份。原生历史导入仍使用 MASS 自己的
provider-specific 转换，但不生成 `agent.Event`。
`session.history_replay` 是 MASS 专属的控制事件，不属于 SDK 实时事件联合。

现有 MASS `SessionEvent` JSON、字段语义、历史格式和 SSE 分支不得变化。实现上
可以复制或内嵌通用 payload，但不能为了复用 SDK 类型而改变公开 wire。

## SDK 与 MASS 的交互

一次正常交互的顺序是：

1. Service 从尚未启动的 Driver 取得唯一 `Events()` 流并开始接收。
2. Service 调用 `Driver.Start`，MASS Driver 调用 `Provider.Start`。
3. Service 请求打开或恢复；MASS Driver 调用 SDK Provider，成功后生成新的
   `events.Source` 并返回 `DriverSession`。
4. MASS 为 Session 启动唯一 relay，从 `SDK Session.Events()` 读取并补齐
   `SessionEvent`；Service 仍只读取 Driver 的公共事件流。
5. MASS 把 `SendPrompt`、`AnswerInteraction` 和 `Cancel` 分别映射到 SDK
   `StartOrSteer`、`Answer` 和 `Interrupt`，同时保留 MASS 的准入与运行校验。
6. 关闭 Session 时 MASS 先确认自己的工作已结算，再关闭 SDK Session 并排空
   该 Session 已接纳的事实；Driver 流继续服务其他 Session。
7. Service 排空并关闭全部 Session 后调用 `Driver.Stop`，再调用
   `Provider.Stop` 并正常结束 Driver 事件流。

### RuntimeDriver 到 Provider

| MASS 操作 | SDK 调用 | MASS 保留的职责 |
|---|---|---|
| `Driver.Start` | `Provider.Start` | Runtime 配置、运行绑定、共享资源责任 |
| `OpenSession` | `Provider.Open` 或 `OpenWithInput` | Address、Source、恢复材料、事件队列 |
| `ResumeSession` | `Provider.Resume` 或 `ResumeWithInput` | 恢复身份/cwd 校验、旧运行隔离 |
| `SendPrompt` | `Session.StartOrSteer` | Session/运行归属、Service 回执和持久化 |
| `AnswerInteraction` | `Session.Answer` | 当前运行和 Session 校验；原生句柄仍只在 adapter |
| `Cancel` | `Session.Interrupt` | 是否允许关闭/归档、Service 取消回执 |
| `CloseSession` | `Session.Close` | 工作是否已结算、恢复材料保留、Driver 绑定移除 |
| `Driver.Stop` | 关闭全部 Session 后 `Provider.Stop` | 事件排空、EventPipe 终止、当前运行解除 |
| `LoadSession` | `Provider.Resume` 只恢复同一原生会话；历史由 MASS Runtime 直接调用 Codex/Claude 原生查询 API | 显式恢复、历史读取、转换、提交、replay 与发布顺序 |

`OpenSession`、`ResumeSession` 和通过 `Provider.Resume` 完成的 `LoadSession`
都必须在调用 SDK 前按 [Runtime 与 Session 合成](#mcp-runtime-overlay)生成最终
`MCPServers`。MASS 保留 RuntimeConfig 默认项、Session 覆盖和 secret 引用的解释；
SDK 只校验并透传最终列表。Runtime 默认项与 native ambient 是不同来源，不能把
后者枚举进 SDK 列表来伪造清除效果。

### 单 Session 中继

```text
agent.Session.Events()
    -> MASS per-Session relay（唯一消费者）
    -> 补齐 namespace/sessionName/source
    -> SessionEvent
    -> RuntimeDriver.EventPipe（唯一 Driver 流）
    -> Service / EventStore / Broadcast / SSE

MASS history-load conversion（仅显式 load）
    -> 原生查询记录转成 SessionEvent，补 replay
    -> 同一 RuntimeDriver.EventPipe
```

中继规则：

- MASS 每次成功打开/恢复 Session 后只取得一次 `SDK Session.Events()`。
- MASS 为每个成功打开/恢复的 Session 生成一个新的 `events.Source`，在
  `DriverSession` 中返回，并用同一 Source 标记该 Session 的中继事件和交互。
  SDK 事件本身不携带 MASS Source；重开同名原生 Session 也不能复用旧运行 Source。
- 身份确认后、`Driver.OpenSession` 返回前已经产生的事件可以在返回前交给既有
  Driver 流，并按 MASS opening 逻辑暂存；不能因为 Service 尚未登记绑定而丢弃，
  也不能通过第二次 `Events()` 补读。
- 中继不能创建第二条 SDK 事件流，也不能让 Service 或外部订阅者直接读取。
- 历史 load 的 SessionEvent 由 MASS provider-specific 转换产生，不是
  `agent.Session.Events()` 的成员；它只复用同一 Driver EventPipe 和发布链。
- 中继对每个事件保留原顺序，补齐 MASS 字段后写入现有有界、公平的 Driver
  EventPipe；不能增加第二套无界转发队列。
- EventPipe 满时，中继按既有背压等待；不得在公共读取循环内等待某个 Session
  的 Store、Broadcast 或外部订阅者。
- 一个 Session 的慢消费或关闭不能终止其他健康 Session；单个 SDK 事件流异常
  终止时，MASS 标记该 Session 的观察失败并按现有释放规则结算。
- 正常关闭单个 Session 复用 Driver 流；Driver 停止时先关闭并排空所有 Session，
  再结束 EventPipe。

### 交互权限

SDK adapter 生成 `InteractionRef` 并保存原生 handle。MASS 只在
`SessionEvent` 中转发该字符串，回答时原样传回。MASS 仍校验
`SessionRef`、当前运行和 DriverSession 归属；adapter 再校验引用仍绑定原
Session/原工作/原句柄。两层校验都不能被复制值或历史事件绕过。

MASS 当前自己维护的原生句柄级 `PendingInteractions` 在迁移完成后应删除；
MASS 只保留回答准入、运行隔离和 Service 状态，不保留第二份回答权。迁移期间
可以暂时保留旧实现，但同一请求不能同时注册到两套可回答 registry。

### 能力、功能和回执

- SDK `Capabilities` 映射到 MASS `CapabilitySet`，不能擅自补 true。
- SDK `EffectiveFeatures` 映射到 `DriverSession.EffectiveFeatures`，保留请求
  开启但降级为 false 的事实。
- `InputReceipt` 的三态接受证据原样映射，不重命名或补值。
- `InteractionReceipt` 映射到 MASS `InteractionReceipt`；MASS 不把 sent 当作
  native accepted。
- MASS `AnswerInteraction` 收到现有动态答案后，先按原交互类型校验并转换为
  `agent.PermissionAnswer`、`agent.FormAnswer` 或 `agent.URLAnswer`，再调用
  `Session.Answer`；不能把任意对象直接强转成成功回答。
- SDK `InterruptReceipt` 映射到 MASS `CancelReceipt`；`no_active_work` 与
  `control_acknowledged` 保持区分。
- SDK `Error` 映射为 MASS `OperationError` 的稳定 code/reason/message，
  `outcome_unknown` 保留且不自动重试。

## 历史：两条路径

MASS 有两条不会互相替代的历史路径。统一 SDK 不在这两条路径之上再定义一套
跨 provider 的历史类型。

| 调用方 | 使用的入口 | 得到什么 |
|---|---|---|
| 普通 SDK 使用方 | `codex` / `claude` 的原生历史查询 | 原生 thread/turn/item 或 message/coverage 值 |
| MASS 使用方读取已提交历史 | `SessionService.listHistoryEvents` | EventStore 中的 `HistoryPage<SessionEvent>` |
| MASS 使用方导入原生会话 | `Service.LoadSession`，随后查询历史或订阅 SSE | `SessionLoadAccepted`；异步提交后的 `SessionEvent` |
| MASS Runtime 实现者 | Codex `Provider.NativeClient()` 或 Claude package-level 查询 | 仅用于 `LoadSession` 的原生只读材料 |

### 读取 MASS 已持久化历史

调用方读取已经提交到 MASS 的历史时，不经过 Runtime 或 SDK：

```text
SessionService.listHistoryEvents
    -> EventStore.List(HistoryQuery)
    -> MASS HistoryPage
```

这条路径只校验 Session 归属、查询过滤和游标，返回 EventStore 中的持久事实。
它不启动 Runtime、不创建 `agent.Session`、不读取原生历史，也不补发实时事件。
因此普通历史查询在引入统一层前后都没有行为变化。

### 导入 Codex/Claude 原生历史

显式导入仍由 MASS `RuntimeDriver.LoadSession` 拥有：

```text
Service.LoadSession
    -> 固定 Runtime、Address、loadId、原生 SessionID 和绝对 cwd
    -> RuntimeDriver.LoadSession
        -> Codex: Provider.NativeClient().ReadThread/ListThreadTurns/ListThreadItems
        -> Claude: claude.GetSessionMessages
        -> 读取 metadata/baseline，校验身份、cwd、来源
        -> Provider.Resume 恢复同一原生会话
        -> 重新读取原生历史并复核未漂移
        -> 原生记录转换为 MASS SessionEvent，加 replay
        -> 唯一 Driver EventPipe
        -> Service/EventStore/Broadcast/SSE
        -> history_replay: started/native_completed/failed/canceled
```

provider-specific 查询职责：

| Provider | metadata / 身份 | 保存内容 | 恢复后复核 |
|---|---|---|---|
| Codex | `ReadThread(IncludeTurns=false)` | `ListThreadTurns`（按原生 item view，必要时 `ListThreadItems`） | 再次 `ReadThread` 并重新分页核对元数据、顺序、cursor 穷尽和内容摘要 |
| Claude | `GetSessionInfo`，以及恢复后首个 native init | `GetSessionMessages` | 再次读取完整有界消息链，并核对 coverage、原始 bytes、UUID/parent 顺序 |

`ListThreads` 和 `ListSessions` 只用于发现候选，不证明指定 native Session
存在、可恢复或内容完整。导入不能只凭列表结果创建本地 Session。

顺序约束：

1. Service 先建立唯一 Driver 流接收，再调用 `LoadSession`；历史不另开第二条
   Driver 流、历史总线或 Broadcast 注入入口。
2. Codex 的原生查询通过 `NativeClient()` 复用 Provider 连接；Claude 调用显式
   scope 的 package-level 查询。恢复前的 metadata 查询不能创建 Session，也不能
   把请求中的原生 ID 当成观察身份。
3. `Provider.Resume` 只恢复同一原生执行连接，不自动导入或回放保存历史。恢复
   后对原生身份的确认仍属于统一 Provider 的恢复合同。
4. MASS 通过原生查询 API 读取历史，不通过 `Session.Events()` 读取历史。
   `Session.Events()` 只接收恢复成功后产生的实时观察；两者可以进入同一
   Driver EventPipe，但历史和实时事件的来源、顺序及 replay 标记仍按 MASS
   现有合同结算。
5. 历史记录到 `SessionEvent` 的转换仍由 MASS Runtime adapter 拥有。保留
   Codex/Claude 各自的原生转换、分页上限、漂移复核、工具关系和失败语义。
   统一 SDK 不提供历史转换、历史事件或 replay，不为该路径增加通用历史值。
6. `native_completed` 只表示原生历史读取穷尽。Service 还要确认前序 EventStore
   追加和 publish 已结算，才能保存 `completed`、开放该 Session 的普通输入。
   成功 load 保留恢复的 `agent.Session` 供后续正常 `StartOrSteer` 和实时观察，
   不因历史读取上下文结束而关闭。
7. 失败或取消仍由 MASS 释放本次恢复资源、保留已提交的部分历史，并维持
   `outcome_unknown` 和禁止重试的原合同。

统一实时层不提供 `NativeHistory`、`HistoryReader`、`LoadSession`、
`HistoryQuery` 或 `HistoryEvent`，也不得：

- 让普通 `Provider.Resume` 自动回放历史；
- 通过第二次 `Session.Events()` 补读历史；
- 把 MASS `session.history_replay` 加入统一实时事件联合；
- 从统一实时事件或历史记录重建旧回答权限；
- 改动 MASS `LoadSession` 的所有权、提交顺序和失败释放语义。

历史导入、转换和 replay 不在本次统一层范围内。若以后确需复用转换代码，必须
另行设计 provider-specific 的纯转换能力，并保持 MASS 对 load 生命周期、replay
信封、覆盖判断、提交和发布的全部所有权；不能因此恢复 SDK history replay 或
引入通用历史查询接口。

### 采用与放弃的方案

| 方案 | 结论 | 原因 |
|---|---|---|
| SDK 提供统一 `HistoryReader` / `HistoryEvent` | 放弃 | 两套原生分页、身份、覆盖和错误无法无损合并；历史值会诱导调用方当作 live 事件 |
| `LoadSession` 返回统一 SDK 历史事件 | 放弃 | load 还是 MASS 的异步资源生命周期；需要 MASS replay 信封、提交、SSE 和失败释放 |
| 普通 `Resume` 自动回放历史 | 放弃 | 恢复执行连接和导入历史是不同操作；自动回放会污染实时观察和当前状态 |
| Codex 通过 `NativeClient()` 复用连接做只读查询 | 采用 | 保留原生接口和同一数据域，同时不要求 SDK 设计第二套查询模型 |
| Claude 直接使用 package-level 查询 | 采用 | Claude 查询本身不需要 client；显式 `ConfigDir` 和 cwd 已足够固定 scope |
| MASS 持久历史继续由 EventStore 提供 | 保持 | 调用方已有稳定 `listHistoryEvents` 合同，不依赖 Runtime 是否启动 |

## 原生 SDK 内部 replay 清理

这项清理与统一层解耦，但必须在 SDK 发布前完成。SDK 保留原生历史查询，
删除的是“把保存记录伪装成 live 通知”的包内 replay 实现。

### 删除范围

- Claude：`HistoryOptions`、`HistoryError`、`prepareHistory`、
  `replayTranscript`、`transcriptEvent`、replay 快照校验，以及只为 replay
  存在的 `Event.History` / `HistorySource`。
- Codex：`HistoryOptions`、`HistoryError`、`historyThreadOwned`、
  `replayHistoryOwned`、`replayTurn`、分页 replay helper，以及只为 replay
  存在的 `Event.History` / `HistorySource`。
- 只验证 replay 投影、快照冻结或历史完成事件合成的测试。

### 保留范围

- 原生 history query API，包括 Claude `GetSessionMessages`、`ListSessions`、
  `GetSessionInfo` 和子历史查询，以及 Codex `ListThreads`、`ReadThread`、
  `ListThreadTurns`、`ListThreadItems`。
- 查询实现共用的解析、校验、分页、覆盖和文件访问原语。
- Claude 原生协议字段 `Event.IsReplay` 和 Codex 原生 resume
  `ThreadResumeParams.History`；它们属于原生协议，不是 SDK replay。
- MASS 的 `HistoryQuery`、`HistoryPage`、EventStore、`SessionEvent`、
  `history_replay` 和 `LoadSession`。
- 协议录制回放、simulation replay、`tests/replay` 和 MASS load 测试；这些
  replay 与 SDK history replay 无关。

### 清理判据

- replay 入口没有公开方法；唯一调用点在包内测试。
- `Event.History` 不参与 JSON 编码，删除不改变原生 wire 或录制格式。
- 保存内容是查询结果，不是 live 通知；伪造 `item/completed`、
  `turn/completed` 或完整 delta/approval 链会违反历史查询合同。
- 当前版本仍是未发布候选版，不承担稳定 API 兼容义务；保留空壳会继续制造
  “查询结果等于 live 事件”的错误语义。

清理后，SDK 只回答“原生保存了什么”；MASS 回答“这些记录如何成为可持久、
可发布、可恢复的会话事实”。

## 职责边界

| 能力 | `agent-sdk-go` 统一层 | MASS |
|---|---|---|
| 原生协议、原生身份、原始数据 | 通过原生 SDK 保留 | 不解析 |
| 共享连接或自有进程 | Provider 拥有和释放 | 决定 Runtime 何时启停 |
| 单 Session 打开/恢复/提交/回答/中断/关闭 | 统一接口 | 通过 RuntimeDriver 编排 |
| Provider 专属创建参数 | adapter 构造配置或具体类型 | 负责从 RuntimeConfig 映射 |
| 原生工作归属与状态投影 | provider adapter | 消费结果，不重做转换 |
| 原生事件到统一事件 | provider adapter | 不重新解析 |
| 交互句柄和单次回答权 | adapter 私有 registry | 校验运行并转发引用 |
| namespace/sessionName | 不拥有 | Runtime/Service 拥有 |
| 运行来源、旧运行隔离 | 不拥有 | RuntimeDriver 拥有 |
| 多 Session 汇聚、公平和背压 | 不拥有 | RuntimeDriver/EventPipe 拥有 |
| 原生历史查询 | 保留原生入口，不定义跨 provider 查询或事件模型 | 在显式 load 中只读调用并校验 |
| 原生 history replay 投影 | 删除包内实现 | 不依赖 |
| 显式历史导入和 replay | 不拥有；不提供 `LoadSession` | Service/Runtime 拥有转换、提交和发布 |
| EventStore、Broadcast、SSE、HTTP/CLI | 不拥有 | MASS 拥有 |
| 持久化分类 | 不拥有 | MASS `SessionEvent` 合同拥有 |

## 迁移顺序

### 1. 冻结兼容面

在 MASS 中为现有 Codex/Claude 合成输入建立统一输出基线。固定
`SessionEvent` JSON、历史格式、SSE 分支、交互回答和恢复行为。基线比较统一
事件的 type、payload、Session/item 身份、来源、顺序和未知数据；随机
`EventID` 只在明确不影响身份的测试中忽略。

此阶段不改变公共行为、原生包或既有录制材料。

退出条件：旧实现能通过现有 MASS 合同测试；比较器能区分“语义相同”和
“接线错误”；尚未修改生产适配路径。

### 2. 建立 SDK 统一核心

新增 `agent` 包的 Provider、Session、EventStream、事件、内容块、交互、回执和
错误值。该包只依赖标准库，不导入原生 SDK 或 MASS，不启动 Agent。

同时修订当前仓库中“没有 root generic client / 没有共享公共协议”的表述，明确
新增的是独立、可选的上层统一层；原生协议仍保持独立。README 和开发规范只声明
已实现的公开面，不把 MASS 接入或真实 Agent 验证包含在 SDK 层完成条件中。

退出条件：接口编译测试、类型校验、JSON 往返和依赖门禁通过；原生包无改动；
尚未宣称任何 provider 已接入。

### 3. 接入 Codex adapter

在 `agent/codex` 包装 `codex.Client` 和 `codex.Session`，迁移：

- provider 共享连接与 Session 打开/恢复；
- turn/item 身份和工作归属；
- 消息、思考、工具、状态、错误和 usage 等事件；
- command/file change 权限和 Codex user-input form；
- Codex 能证明的接受证据、归档和显式模型设置。

旧 `mass-go/pkg/runtime/codex` 保留，作为逐事件对比基线。

退出条件：现有 Codex 合成输入的转换结果等价；原始身份、原始时间、未知数据
范围和事件顺序保持；原生 Codex 测试不因统一层变化。

### 4. 接入 Claude adapter

在 `agent/claude` 包装 Claude Session，迁移：

- init 身份确认和恢复身份/cwd 校验；
- text/thinking 的 message、block 和 linkage 身份；
- work attribution、工具调用/结果和状态；
- permission 和 AskUserQuestion form；
- Claude 能证明的本地写入与原生观察边界。

Claude block 粒度继续满足现有 MASS 身份规则；无法证明归属和身份的 frame 仍
失败，不使用猜测身份。Claude 的原生 user content 类型允许 text 与
`tool_result` 同帧，但 adapter 不支持这种 mixed frame：整帧生成一个
`user_message_mixed_content_unsupported` 缺口，不输出部分消息或工具结果，也不
构造 segment 身份。纯 text 数组作为一个 `UserMessage.Content` 数组输出，
tool-result-only 数组按工具结果输出。依据与后果见
[ADR 0001](../../adr/0001-claude-mixed-user-content.md)。旧
`mass-go/pkg/runtime/claude` 保留为基线。

退出条件：实时转换的合成结果等价；无法证明身份的样本仍失败；原生 Claude
测试不因统一层变化。

### 5. 清理 SDK 内部 history replay

删除只服务包内 replay 的投影代码、类型和测试，保留全部原生历史查询、协议
字段、原始数据及录制回放。该阶段不改变 MASS `LoadSession`、历史转换、replay
信封或 EventStore 行为。

退出条件：`prepareHistory`、`replayTranscript`、`replayHistoryOwned` 等内部
入口及其专用类型被删除；`GetSessionMessages`、`ReadThread`、
`ListThreadTurns`、`ListThreadItems` 等查询接口仍通过原生测试；仓库中没有
生产调用点依赖被删实现。

### 6. 发布 SDK 统一层

在完成 history replay 清理后，发布 `agent-sdk-go` 的可选统一层。完成离线、
race、依赖门禁和公开导入验证后，再由 MASS 升级依赖。MASS 不直接依赖未发布
的本地源码作为切换前提。

发布证据必须分开记录：SDK 单元/合成、SDK adapter 对旧 MASS 基线的逐事件比较、
MASS SDK 驱动、以及真实 Agent。前者通过不证明真实 Agent 已验证。

### 7. MASS 双路径接入

在 MASS Runtime 内新增基于 SDK Provider 的新实现，与旧转换实现并存。先接入
Codex，再接入 Claude。新实现只保留：

- RuntimeConfig → 具体 Provider 配置；
- Address、Source、恢复材料和运行绑定；
- per-Session relay 和唯一 Driver EventPipe；
- 现有历史 load、Store、Service 和传输，不新增 SDK history 抽象。

双路径比较期间，两个实现不能共享同一条原生连接、同一回答句柄或同一个事件
消费者。每个测试场景只选择一条完整路径。

退出条件：新路径通过 MASS 现有单测、合成集成和 SDK 驱动测试；旧实现仍可
回退；选择器本身不改变公开 Runtime 行为。

### 8. 切换 Codex Runtime

先把 MASS Codex Runtime 切换到 SDK Provider。每次切换保留以下行为：

- 一个 Driver 一条 Service 消费的事件流；
- 单 Session 顺序、背压、关闭排空和旧运行隔离；
- namespace/sessionName 补齐、Source 和回答引用所有权；
- 恢复身份、cwd 和配置一致性验证；
- 历史事件和 MASS wire JSON 不变。

退出条件：Codex 全链路使用新路径通过；仍可切回旧路径。

### 9. 切换 Claude Runtime

按 Codex 相同的退出条件切换 Claude。Claude 额外要求恢复前身份、init 观察、
block/message linkage 和无法归属的交互行为保持不变。

退出条件：两个 provider 的实时与历史兼容测试均使用新路径通过；无法证明的
身份和归属仍明确失败；旧路径保留到回退窗口结束。

### 10. 删除 MASS 重复的实时转换

仅在两个 provider 都通过各自记录的离线门禁且回退窗口结束后，删除 MASS 中
重复的纯实时事件转换、block 身份、状态投影和交互句柄 registry。历史记录到
`SessionEvent` 的 provider-specific 转换不删除，也不迁移到 SDK。

以下代码不删除：

- `RuntimeDriver`、Runtime Registry 和运行绑定；
- `Address`、`Source`、旧运行隔离；
- 恢复材料、显式历史 load、EventStore 和 Service；
- MASS `SessionEvent` wire、持久化分类、Broadcast、SSE 和 transport。

## 验收

- 统一核心只依赖标准库，且不导入 `codex`、`claude` 或 MASS。
- 两个 adapter 互不导入，原生包不导入统一层。
- Provider、Session、EventStream、Input、Answer 和可选能力有公开接口编译测试。
- Codex 与 Claude 的合成协议输入逐事件比较统一输出；比较类型、payload、身份、
  Meta、顺序和未知数据范围，忽略随机 EventID。
- 每个 Session 只允许一个消费者；重复取流失败；慢消费者不重排事件；关闭一个
  Session 不关闭其他 Session 或 Provider。
- 真实原生交互句柄不能被事件、历史、复制值或伪造引用重建；跨 Session、旧
  运行、已回答和过期引用均拒绝。
- MASS 的 wire JSON、历史、SSE、恢复、acceptance 和回答行为保持兼容。
- MASS 普通历史查询只调用 EventStore；原生 load 通过 Codex `NativeClient()`
  的只读查询或 Claude package-level 查询读取保存内容，由 MASS 转换并标记
  replay；`Provider.Resume` 不自动回放历史，`Session.Events` 不承担历史补读。
- 统一 `agent` 层没有 `NativeHistory`、`HistoryReader`、`HistoryEvent` 或
  等价历史抽象，也没有 `LoadSession` 或 replay 方法。
- `agent.Event` 包装为 `SessionEvent` 时 EventID、Time、ItemID、payload 和
  Meta 不被改写。
- SDK 内部 history replay 投影已删除；原生历史查询、`Event.IsReplay`、
  `ThreadResumeParams.History`、录制回放和 simulation replay 仍可用。
- 原生 `codex`、`claude` 及其 `types` 的公开接口和默认测试不因统一层变化。
- 统一层只存在 stdio 和 Streamable HTTP MCP；没有 SSE transport 或
  `MCPConfigMode`。Runtime 默认项与 Session 同名项由上层合成，Session 项整体
  覆盖，且 Provider 不再二次合并。
- MCP synthetic 测试固定 Codex thread config 与 Claude argv 的精确映射；无法
  表达的字段在原生副作用前失败，secret sentinel 不出现在错误、事件或日志。
- 默认验证保持离线，不使用真实 Agent 或新增录制作为完成证据。

## 合同影响

本设计增加一个可选的公开统一层，但不替换两个原生 SDK：

- 原生 `Received` 仍不是统一事件模型；统一转换只发生在 `agent/codex` 和
  `agent/claude`。
- 原生协议层保持独立；统一层是独立、可选的上层，不替代原生包，也不是新的
  root generic client 或共享历史协议。
- MASS 的公开 `RuntimeDriver` 和 `SessionEvent` 仍是其 Service 边界；SDK
  统一层不成为第二个 MASS 公开协议。
- 统一层不暴露 Claude strict 或旧式 SSE；这两个原生能力继续由 `claude` 包
  提供。Runtime 同名覆盖只作用于调用方拥有的最终列表，不清空 native ambient。
- README 和包文档说明统一层的使用边界与离线示例；MASS 接入、发布和真实
  Agent 验证须分别记录，不从该合同推定完成。
