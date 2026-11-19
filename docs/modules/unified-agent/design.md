---
type: Module Design
title: 统一 Agent Core、Session 与事件流
description: >-
  在原生 Codex 和 Claude SDK 之上定义可选的统一会话与事件层，
  SDK 保留原生历史查询但不提供历史抽象，MASS 保有历史导入和持久化语义。
status: stable
---

# 统一 Agent Core、Session 与事件流

`agent` 为单个 Codex 或 Claude Session 提供统一的操作和实时事件。具体 adapter
管理原生资源、工作归属和回答权限。原生 SDK 提供各自的协议与历史查询。MASS 负责
应用侧的 Session 身份、多 Session 汇聚、历史导入和持久化。
[原生 Session 合同](../native-session/design.md) · [独立历史查询](../../contracts/history-query.md)

从 [SDK 接入示例](../../../README.md) 开始使用统一接口。依赖边界见[开发规范](../../architecture.md)。

## 分层与依赖

```text
MASS Runtime adapters -> agent.Core / agent.Session
                           ^             ^
                           | implements  |
                    agent/codex     agent/claude
                        |                 |
                        v                 v
                      codex             claude
                        |                 |
                        v                 v
                    codex/types      claude/types

agent/codex, agent/claude
    -> agent/internal/corelease         -> agent
    -> agent/internal/eventstream       -> agent, internal/queue
    -> agent/internal/interactionledger -> agent, internal/uuidv4
    -> agent/internal/eventenvelope     -> agent, internal/uuidv4
    -> agent/internal/submissiontrace   -> agent

agent, codex, claude, claude/types -> mcp
agent          -> internal/tracecontext
internal/queue -> github.com/zoumo/golib/channx
```

目录、import 方向和依赖门禁由[开发规范](../../architecture.md)维护。上图展示
公开合同、实现关系与生产依赖；原生 SDK 不反向依赖统一 `agent`。
两个 adapter 只适配各自原生 SDK，共同使用 `corelease` 管理 Core 准入、共享资源
和 Session lease，使用 `eventstream` 管理每个 Session 的事件队列、取流与清理
顺序。两者都不拥有原生观察、转换或交互状态机。`interactionledger` 另行保存
统一回答准入状态；adapter 私有 registry 独占原生回答句柄与结算逻辑。
MASS Runtime adapter 可以消费统一层，而 MASS Service、Store 和 transport 仍
只依赖 MASS 自己的合同。MASS 的配置、资源身份和恢复解释在构造 Core 前完成，
不进入 SDK adapter。

## SDK 构造与 Core

调用方通过具体 adapter 构造 Core，并显式提供原生配置。上层应用负责解析环境继承策略、
secret 引用及最终的 MCP 列表。Core 在构造时固定 `Tracer`。实现须支持并发调用并及时返回。
SDK 隔离回调 panic，也不会配置 exporter 或等待导出。
[Codex Core 源码](../../../agent/codex/core.go) · [Claude Core 源码](../../../agent/claude/core.go)

### Core 接口

`Open` 完成初始化并确认原生身份后返回。`Resume` 要求恢复到原有的原生身份。
[Core 接口](../../../agent/core.go)

Core 可以跨 Session 共享原生连接（Codex），也可以只管理 Session 生命周期
（Claude）；后者由每个 Session 拥有并在 `Close` 中释放自己的原生进程。

Agent Core 是 Agent 级 adapter owner，与工具来源 `ToolProvider`、工具 payload
的 `provider` 字段和 MCP provider 不同。Core 错误使用 `core_stopped`、
`core_not_started`、`core_has_open_sessions`；Codex 资源获取失败使用
`core_start_failed`。

`Open` 与 `Resume` 共用配置字段。Adapter 在原生副作用前校验 provider 对这些字段的支持范围。
[创建和恢复选项](../../../agent/options.go) · [会话配置](../../../agent/session_config.go)

- `Cwd` 必须由调用方明确给出且为绝对路径；provider 不接受隐式工作目录。
- Claude `Open` 默认通过 `--await-claim` 启动，initialize 后发送
  `claim_session` 绑定工作目录，并从响应取得 CLI 分配的原生 session ID；
  SDK 不生成 session ID。claim 成功后返回，不等待 `system/init`，不发送用户输入。
  原生 claim 与显式 `SessionID` 的互斥和身份确认见
  [原生创建合同](../native-session/design.md#interface-session-与交付值)。
  后续原生主消息必须匹配固定 ID，init 必须非
  replay、只出现一次，且 cwd 与请求目录为同一目录；冲突以 `native_rejected`
  终止 Session。init 的命令列表在到达后交付。该流程不依赖本地身份文件或刷新 API。
- `Resume` 必须定向并确认原原生身份；Claude 还必须在启动前预检保存材料，
  以只读 `get_status` 控制探针确认身份，并在返回前复核
  同一文件身份（见 [Claude 恢复身份](#claude-resume-identity)）。恢复响应中的
  保存历史不转换为实时事件，也不重建旧交互权限。普通恢复只交付恢复后的新观察。
- 原生 resume 支持排除历史时，adapter 必须显式关闭保存内容的回放；如果原生
  连接仍收到保存记录，adapter 只能把它当作恢复/查询材料，不能生成统一 live
  `Event`、当前工作状态或回答权限。
- `AdditionalSystemPrompt` 按下述追加合同解释；其他已知不支持或无法解释的
  字段，在原生副作用前明确拒绝。
- `ProcessEnv` 按[环境配置](#environment-configuration)解释；只有 Session
  自有 CLI 进程的 provider 可以接受非空值，其他 provider 必须返回
  `unsupported`，不能映射到 app-server 或 shell 环境。
- `ShellEnv` 按[环境配置](#environment-configuration)解释；provider 不支持
  非空值时必须返回 `unsupported`，不能静默忽略或映射到更宽的作用域。
- `AgentQuestions` 请求关闭必须得到 false；请求开启但 adapter 不支持时，
  允许降级为 false。`RestrictLocalTools` 使用下述 best-effort 合同。
  两者均通过 `Session.EffectiveFeatures()` 返回有效映射。
- 创建时的原生专用选项不进入共享 `OpenOptions`。具体 adapter 可以提供额外
  的创建方法，但必须返回同一个 `agent.Session`，不能形成第二套通用接口。
- `OpenOptions.MCPServers` 是 Runtime 默认项与本次 Session 声明合成后的最终
  external MCP 列表；adapter 不再解释来源或执行第二种合并策略。
- Core 配置在构造时复制并固定，调用方后续修改原对象不影响运行。
- Core 的基础 `Capabilities` 由 adapter 在构造时供给，`corelease` 复制并
  冻结；`Start` 每次返回副本。Session 的有效能力和功能降级仍由 adapter 按
  Session 选项计算，不更新 Core 基础能力。
- `Start` 的单次原生资源获取与 `Stop` 串行；获取失败后 Core 保持未启动并可
  重试，Stop 被接纳后不能重新 `Start`。
- 进行中的 `Open` 和 `Resume` 计入 in-flight 操作，`Stop` 必须拒绝而不是等待。
  Open 的 callback 失败时，adapter 必须先完成原生清理，再由共享生命周期自动
  撤销 opening；之后 Core 仍可 Stop，也可继续使用到 Stop。
- Core 的 Session registry 持有活跃或正在清理的 lease。没有显式 `Close`
  时，原生观察终止必须先完成 native cleanup 并恰好释放一次 lease，之后才关闭
  `Done`；已经接纳的事件仍可排空，不再阻止 `Core.Stop`。显式 `Close` 已
  开始时，`Stop` 仍拒绝，直到该清理路径释放 lease。自然终止后的 `Session.Close`
  只复用同一 cleanup 结果，不重复 native cleanup 或 Core release。
- 首次被接纳的 `Stop` 是终止性的：release 至多执行一次，release 错误被缓存，
  后续 `Stop` 返回同一结果；即使 release 失败，`Start`、`Open` 和 `Resume`
  也不能再运行。没有未关闭 Session 时重复 Stop 返回缓存结果。

### Restrict local tools

<a id="restrict-local-tools"></a>

`Features.RestrictLocalTools` 是单个 `bool` 开关，要求 adapter 对 provider
内置的本地文件读写与命令执行工具应用可用的原生限制。`true` 请求限制，
不承诺关闭全部本地工具；默认 `false` 沿用原生配置，不主动开启工具。
该选项按 `Open`／`Resume` 操作传递，恢复时调用方必须重新声明。
它与 `AgentQuestions` 独立，不主动关闭提问、计划或其他类别工具。

| Provider | `true` 的原生映射 | `EffectiveFeatures.RestrictLocalTools` |
|---|---|---|
| Claude | `--disallowedTools` 加入 `Bash`、`PowerShell`、`Read`、`Glob`、`Grep`、`Write`、`Edit`、`NotebookEdit`、`EnterWorktree`、`ExitWorktree` | true |
| Codex | thread config 设置 `features.shell_tool=false`、`features.view_image=false`、`features.image_generation=false` | true，表示配置了上述限制 |

Claude 的 `AskUserQuestion` 仅由 `AgentQuestions` 控制。Codex 的公开配置没有
完整文件工具 deny list，也没有关闭全部工具的公开入口；`apply_patch` 仍可能
注册。因此 Codex 接受该请求并应用可用限制，不能把它解释为完整关闭。

`EffectiveFeatures.RestrictLocalTools` 在本次请求为 true 且 adapter 配置了
原生限制时为 true，默认 false。它描述 adapter 配置映射，允许 provider 之间
存在 gap，不是全部工具已关闭的证明、运行时工具发现结果或未来工具名覆盖承诺。
本选项不控制 MCP、hook、其他类别工具、子 Agent 的继承行为或 CLI 自身读取配置与
写入会话材料；不构成操作系统安全隔离，也不修改 sandbox 或 approval policy。

### Additional system prompt

<a id="additional-system-prompt"></a>

`OpenOptions.AdditionalSystemPrompt` 是唯一统一入口，字段名直接表达“追加”而不是
“替换”。它只接受 inline 文本：空字符串表示本次不请求追加；非空文本表示在
provider 内置或默认指令之后增加一段调用方指令。统一层不暴露替换内置 prompt 的
能力，也不保证追加文本相对 provider 动态段落、工具说明或子 Agent 的精确顺序和
继承关系。

原未发布的 `SystemPrompt` 字段直接改名为 `AdditionalSystemPrompt`，不保留兼容
别名、deprecated shim 或双重字段。仓库仍处于
`v0.1.0-rc.1 planned, unreleased`，因此这里不承担已发布 API 的兼容义务。

| 统一输入 | Codex | Claude |
|---|---|---|
| 空 `AdditionalSystemPrompt` | 省略 `developerInstructions` | 省略 `--append-system-prompt` |
| 非空 `AdditionalSystemPrompt` | `thread/start` 与 `thread/resume` 的 `developerInstructions` | `AppendSystemPrompt` 映射为 `--append-system-prompt=<text>` |
| 替换内置系统提示 | 不提供；不设置 `baseInstructions` | 不提供；不设置 `--system-prompt` |

Claude 原生包在 `claude.Options` 与 `claude/types.Options` 增加
`AppendSystemPrompt string`，并由 `OptionsFromTypes` 和 `Types()` 原样往返。
该字段只接受 inline 文本；非空时生成一个 `--append-system-prompt=<text>` argv
元素，空串省略。使用 `=` 绑定值，避免以 `-` 开头的文本被当成另一个 CLI 选项。
该选项不经过 shell。统一 `agent/claude` adapter 只把
`AdditionalSystemPrompt` 填入这个原生字段。

Codex 原生字段已存在：统一 adapter 在非空时填写 `thread/start` 和
`thread/resume` 的 `developerInstructions`，空串保持省略。不得改用
`baseInstructions`，因为那是替换而不是追加合同。

首版只支持这段 inline string，不解释文件、路径、数组、模板、动态 boundary 或
secret 引用。`AGENTS.md`、`CLAUDE.md`、原生 agent 定义、配置文件和 provider
自有动态指令仍由原生 CLI/SDK 管理，统一层不合并、不读取也不改写。

`Resume` 必须由调用方重新提供完整文本；SDK 不保存上次 `Open`/`Resume` 的
prompt。空字符串表示本次调用不追加，不应被解释为清除原生会话可能保存的旧
指令；需要确定的初始指令集时，调用方应新建 Session，或恢复后显式传入完整目标
文本并接受 provider 原生恢复语义。

校验必须在原生副作用前完成。非空文本必须是合法 UTF-8，且不能包含 NUL
（U+0000）；换行、Unicode 和其他合法文本原样传递。统一入口在
`OpenOptions.Validate` 中返回 `invalid_argument`，不能先创建 thread、启动
Claude 进程或发送原生请求。直接使用原生 `claude.Options` 时，同一文本约束也在
进程启动前校验。

统一层不把 prompt 当作保密存储。Claude 会把文本放入 CLI argv，Codex 会把
文本放入 app-server 请求；统一错误、事件和日志不得回显 prompt 内容。调用方
不得依赖该字段传递密钥或恢复材料。

首版不定义 `Session.SetAdditionalSystemPrompt`、
`Session.AppendAdditionalSystemPrompt` 或等价的运行时热更新接口：

- Codex 的稳定 `thread/settings/update` 合同没有 `developerInstructions`；
  `turn/start.collaborationMode.settings.developer_instructions` 是实验性的
  collaboration mode 配置，会覆盖该模式下的 developer instructions，不是统一
  层的 live append 合同。
- Claude 的 `--append-system-prompt` 是进程启动配置；公开控制协议没有可依赖的
  稳定“修改追加提示”操作，CLI 内部设置命令不能作为公开合同。
- 要改变 prompt，调用方应结束当前 Session 后重新 `Open`，或重新 `Resume` 并
  提供完整新选项。不能假定同一 Session 的后续 turn 自动采用新文本。

`AdditionalSystemPrompt` 不增加统一 `Capabilities`、`Features`、`Event`、`Meta`
或持久化 Session 状态。两个 adapter 首版都必须实现上述映射；如果未来某一
provider 不能表达该字段，必须在原生副作用前返回 `unsupported`，不能静默忽略或
伪造已应用。

### 环境配置

<a id="environment-configuration"></a>

环境有四个不同作用域，不能用一个 `Env` 字段混合表达：

| 作用域 | 配置位置 | 影响范围 | 生命周期 |
|---|---|---|---|
| Core 进程环境 | `agent/codex.Config.Stdio.Env`、`agent/claude.Config.Env` | Codex app-server；Claude Session CLI 的基础环境 | Core 构造时固定；nil 继承在进程启动时解析 |
| Session 进程环境 | `OpenOptions.ProcessEnv` | Claude Session 自有 CLI 进程及其子进程 | `Open`/`Resume` 时固定 |
| Session shell 环境 | `OpenOptions.ShellEnv` | 仅 provider 启动的 shell / unified-exec 类子进程 | `Open`/`Resume` 时固定 |
| Prompt trace | `agent.WithTraceContext` 与 `Tracer` | SDK 将 carrier 映射到 Codex JSON-RPC request envelope 或 Claude settings `env`，并可创建本地提交 span | carrier 属于一次 `StartOrSteer`；SDK 逐次映射，不拥有 provider span/trace 生命周期 |

本设计不提供 `Input.Env` 或等价的 per-prompt 环境变量注入。Codex 没有稳定的
per-turn shell env 字段，Claude 的 user message/control request 也没有
通用 per-prompt env 入口；Claude 的 trace-only settings env 映射不能被扩展成
任意环境变量 API。trace carrier 不能用于补造该能力。

#### Core 进程环境

进程环境不是新 Session API。Codex 使用现有 `StdioOptions.Env`（统一层为
`agent/codex.Config.Stdio.Env`），Claude 使用现有 `Options.Env` /
`SessionOptions.Env`（统一层为 `agent/claude.Config.Env`）。两者对真正启动的
CLI 进程保持 Go `os/exec` 语义：nil 继承当前进程环境，非 nil 的完整 slice
替换子进程环境。只追加变量时，调用方应显式以 `os.Environ()` 为基底构造完整
环境；SDK 不隐式合并调用方 map。process-free 的 `StreamOptions.Env` /
`OpenStreams` 只保存协议快照，不启动或修改任何子进程环境，不能用于 env 注入。

Codex Core 只有一个 app-server 进程，所有 Session 共享该进程环境。
不同 Codex thread 需要不同 `ShellEnv` 时仍可共享一个 Core；不同
app-server 进程环境或隔离的 `CODEX_HOME` 必须使用不同 Core。

Claude 的 `agent/claude.Config.Env` 是 Core 提供的默认完整环境，不是每个
Session 的最终值。Claude 每个 Session 自有 CLI 进程，所以
`OpenOptions.ProcessEnv` 可以只覆盖该 Session 的进程环境；不同 Session 可以
使用不同 overlay，而不为每个 Session 重建 Core。

进程环境可能含认证秘密，统一错误、事件、Meta、日志和诊断不得包含其值。
SDK 不解析 secret 引用、不读取 dotenv，也不改写调用方 slice。

#### Session `ProcessEnv`

`OpenOptions.ProcessEnv` 只增加或覆盖 Session 所拥有 CLI 进程的环境变量。

- nil 或空 map 不改变 Core 的进程环境基底；SDK 协议默认值仍按下述规则补齐。
- 非空 map 只增加或覆盖所列变量；不提供删除、空值即 unset 或恢复 ambient
  变量的语义。空字符串是合法 value，表示把变量设置为空值。
- Claude adapter 在 `Open`/`Resume` 中生成该 Session 的完整原生
  `SessionOptions.Env`。合成规则是：

1. `agent/claude.Config.Env` 非 nil 时复制它作为基底；包括非 nil 空 slice。
2. `Config.Env` 为 nil 时调用一次 `os.Environ()` 并复制结果作为基底。
3. 按排序后的 key 将 `ProcessEnv` 追加到基底，因此同名 overlay 值在该原生
   进程环境中覆盖基底值；其他基底项及其顺序不变。
4. 若结果中没有 `CLAUDE_CODE_SDK_READS_SESSION_STATE`（变量名大小写不敏感），
   追加该变量并设为 `1`，请求原生 Session 状态通知。已有变量保留原值，包括
   空字符串；非 nil 空基底不隐式继承 ambient 环境，但仍补齐该协议默认值。

合成后的环境同时用于版本查询、Claude 配置范围发现和 Session 进程启动，不得
在一次 `Open`/`Resume` 内从不同环境快照读取。恢复身份仍由显式 `ConfigDir`
固定，不从有效环境推断；调用方应让原生 `CLAUDE_CONFIG_DIR` 与该值指向同一
配置树。
- 调用时复制并校验。key 必须非空、合法 UTF-8、不含 NUL 或 `=`；value 必须
  合法 UTF-8 且不含 NUL。key 还必须按 ASCII 大小写不敏感方式唯一，避免
  Windows 环境语义下互相覆盖；错误只包含 key 或字段名，不包含 value。
- 校验、冲突报告和追加顺序按排序后的 key 处理。非法值在版本查询、隔离检查、
  原生进程启动或 RPC 前返回 `invalid_argument`。
- `ProcessEnv` 不是 secret store。值会进入 Claude CLI 进程环境并可影响认证、
  配置、MCP、hook 及其启动的更多子进程；SDK 错误、事件和日志不包含 value，
  但不能阻止 provider 自身诊断或日志可见该值。
- Codex app-server 由同一 Core 的所有 Session 共享，没有 Session 级自有
  CLI 进程。Codex `Open`/`Resume` 收到非空 `ProcessEnv` 时，必须在本次调用
  产生原生副作用前返回 `unsupported`；若 Core 尚未启动，这包括
  app-server 进程启动前。不能改写到 `shell_environment_policy` 或 app-server
  所在进程。

支持该合同的 Claude Session 宣告 `processEnv` capability；Codex 不宣告。
capability 说明 adapter 接受该 Session 的进程环境 overlay，不说明 provider
自身的 settings `env`、认证逻辑或其他配置不会进一步改变最终行为。

#### Session `ShellEnv`

`OpenOptions.ShellEnv` 只增加或覆盖 shell 环境变量。

- nil 或空 map 不生成原生配置，不改变现有行为。
- 非空 map 只增加或覆盖所列变量；不提供删除、空值即 unset 或恢复 ambient
  变量的语义。
- 空字符串是合法 value，表示把变量设置为空值，不表示删除；删除不受支持。
- 调用时复制并校验。key 必须非空、合法 UTF-8、不含 NUL 或 `=`；value 必须
  合法 UTF-8 且不含 NUL。key 还必须按 ASCII 大小写不敏感方式唯一，避免
  Windows 环境语义下互相覆盖；错误只包含 key 或字段名，不包含 value。
- 校验和冲突报告按排序后的 key 遍历，保证同一 map 的错误选择与消息稳定。
- `ShellEnv` 不是 secret store。值会进入 native thread config 并由 provider
  处理；SDK 的错误、事件和日志不包含 value，但不能阻止 provider 自身的配置
  诊断或日志可见该值。
- Codex adapter 在创建或恢复的 raw config 中生成一个扁平 dotted path：

  ```json
  {"shell_environment_policy.set": {"FOO": "bar"}}
  ```

  它与调用方 raw config 的精确、祖先或后代路径冲突时，在
  `thread/start`/`thread/resume` 前返回 `invalid_argument`。调用方仍可用不
  相冲突的 `inherit`、`exclude`、`include_only` 等 policy 字段。
- Codex 的 `populate_env` 在应用 `set` 后还会应用 ambient `include_only`，
  并覆盖或清理保留变量。SDK 不读取或改写这些 policy 字段，因此 `ShellEnv`
  不是“最终环境必含”保证。Codex 身份/控制变量不得由该字段设置；SDK 拒绝的
  保留名称是固定版本与已知后续版本的并集，精确列表和 native merge 规则见
  [Codex 会话配置](../native-session/design.md#interface-codex-会话配置)。
- Codex 的该策略只影响它启动的 shell / unified-exec 类子进程，不修改
  app-server 自身环境，也不保证 MCP server、hook 或其他任意子进程继承。
- `ShellEnv` 只在 `Open`/`Resume` 时生效。Codex 已加载或运行中的 thread 可能
  忽略 `thread/resume.config` override，且没有 per-turn shell env 更新入口；
  SDK 不重启 thread、不伪造热更新成功。
- Claude 没有稳定的 shell-only 环境入口。settings `env`、Core
  `Config.Env` 与 Session `ProcessEnv` 都会进入 Claude CLI 进程环境并可影响
  更多子进程，不能冒充 `ShellEnv`。因此 Claude `Open`/`Resume` 收到非空
  `ShellEnv` 时，必须在进程启动前返回 `unsupported`。

支持该合同的 Codex Session 宣告 `shellEnv` capability；Claude 不宣告。
capability 只说明输入被按 shell-only 语义解释，不说明某个变量最终可见于每条
命令。

<a id="claude-resume-identity"></a>

### Claude 恢复身份

Claude 的进程启动要求稳定 CLI >= 2.1.293。Core 使用与 Session
相同的 `CLIPath` 和由 `Config.Env` 与本次 `ProcessEnv` 合成的有效进程环境，
执行无会话副作用的版本查询；版本不能确认时，`Resume` 在启动 Session 进程前
失败关闭。原生 Session 启动同样执行版本门禁，因此新建 `Open` 也在启动前拒绝
旧版本、预发布版本和无法确认的版本。不能从 `SessionStart` lifecycle frame
或 init 是否出现推断 CLI 版本。新建 `Open` 不使用恢复探针，按
[创建规则](#core-接口)通过 claim 协议确认新身份。

Claude Core 的 `Resume` 要求构造配置显式提供绝对 `ConfigDir`；恢复预检和
复核使用该值，不从 `HOME`、环境变量或当前目录推断。原生只读探针及接收投影由
[Claude 只读身份探针](../native-session/design.md#claude-status-identity-probe)维护。

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

### MCP 配置

<a id="mcp-configuration"></a>

统一层只声明由原生 Codex 或 Claude 启动或连接的 external MCP server，不实现
MCP 连接、OAuth、健康检查、tool discovery、tool 调用或 application-hosted SDK
MCP 的 JSON-RPC 回答。后者的原始 `mcp_message` 控制仍由原生 Claude Session
和应用处理，不进入统一 MCP 配置。

Codex、Claude 和统一层都使用 `[]mcp.Server`。调用方必须显式设置 `Transport`。
[共享 MCP 配置](../../../mcp/server.go)

`mcp` 包定义 `TransportStdio`、`TransportHTTP` 和 `TransportSSE`。
`TransportHTTP` 是 MCP Streamable HTTP；`TransportSSE` 是 Claude 原生包仍可
声明的旧式 HTTP+SSE。统一 Core 只接受两个 adapter 共同具备的 stdio 与
Streamable HTTP，并明确拒绝 SSE；需要 SSE 的调用方直接使用原生 `claude`
Session。统一层也不定义
`MCPConfigMode`、`inherit`/`replace`、`StrictMCPConfig` 或任何清空 native
ambient MCP 的开关。

<a id="mcp-runtime-overlay"></a>

#### Runtime 与 Session 合成

Runtime 默认 MCP 属于构造 Core 之前的 Runtime 配置解释，不进入 Core
或 Session 状态。负责解析 RuntimeConfig 的上层必须在每次 `Open`、`Resume`
以及通过 `Core.Resume` 完成的显式 load 之前合成最终列表：

- 不使用 Runtime 默认项的调用方直接把完整列表放入
  `OpenOptions.MCPServers`，Core 不自行补充默认 MCP。
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
- Core 只接收合成后的 `OpenOptions.MCPServers`，不知道某一项来自 Runtime
  还是 Session，也不再次覆盖、去重或继承。

该规则只合成调用方拥有的 Runtime 配置。Codex 或 Claude 自己从用户、项目、
企业设置发现的 [native ambient MCP](../../../GLOSSARY.md)仍由原生 CLI 管理。
集中声明的最终列表不能保证整体替换或删除 ambient 同名项：Codex 会按字段递归
merge，Claude 的同名优先级属于目标 CLI 的实现行为且可能受企业策略拒绝。
需要确定性 server 集合时，调用方必须隔离原生配置来源或外层执行环境。

#### Adapter 映射

| 统一字段或能力 | 约束 | Codex | Claude |
|---|---|---|---|
| `Name` | 非空、合法 UTF-8、无 `.`、列表内唯一 | `mcp_servers.<name>` key | `mcpServers.<name>` key |
| `Transport` | 必须显式提供；统一层拒绝 `sse` | 只接受 `stdio` 或 `http` | 接受 `stdio`、`http` 或 `sse` |
| `Command` / `Args` / `Env` | 只用于 stdio | 同名字段 | 同名字段 |
| `Cwd` | 只用于 stdio | `cwd` | 不支持 |
| `URL` | 只用于 HTTP | `url` | `url` |
| `Headers` | 只用于 HTTP | `http_headers` | `headers` |
| `EnvHeaders` | 只用于 HTTP；值是环境变量名 | `env_http_headers` | 不支持 |
| `BearerTokenEnvVar` | 只用于 HTTP；值是环境变量名 | `bearer_token_env_var` | 不支持 |
| 旧式 HTTP+SSE | `mcp.TransportSSE` 只供原生 Claude | 不支持 | 原生包支持，统一层不暴露 |

adapter 必须拒绝无法表达的字段，不能静默丢弃或改写成其他字段。`Cwd` 与
`EnvHeaders`、`BearerTokenEnvVar` 存在于统一结构是为了保留 Codex 的原生安全
引用能力；Claude adapter 在启动进程前返回 `unsupported`。

#### 校验、生命周期与敏感值

- `Transport` 不能由 `Command`/`URL` 推断；缺失、未知或统一层不支持的
  transport 返回 `invalid_argument`。
- stdio 必须有非空 `Command`，禁止 `URL`、`Headers`、`EnvHeaders` 和
  `BearerTokenEnvVar`。
- HTTP 必须有非空 `URL`，禁止 `Command`、`Args`、`Env` 和 `Cwd`。
- 文本、名称、map key 与 value 必须是合法 UTF-8。结构校验和 provider 字段
  支持检查都在原生进程启动或 thread RPC 前完成。
- slice 和 map 在进入 Core 时复制；调用方后续 mutation 不影响已经开始的
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

### 会话级模型与 effort

`SessionConfig` 的全部字段只有 `Model` 和 `Effort`。两者都使用
provider 原生字面值，不在统一层定义模型别名、模型目录或 effort 枚举。

#### 启动与恢复

- 空字符串表示不传递该选项，由 provider 使用自己的默认值或恢复语义；适配器
  不得填造跨 provider 默认值。没有“清空为默认值”的独立请求。
- 非空值原样传递，不 trim、不大小写转换、不做别名映射，也不把一种 provider
  的档位翻译成另一种 provider 的档位。值是否有效由原生接口判定。
- 选项在 `Open` 或 `Resume` 时用于建立主 Session，不是逐 turn 选项，也不
  自动作用于子 Agent。
- 所有 adapter 必须先在原生进程或协议写入前完成自身可判定性检查。某项无法
  被该 provider 执行时返回 `unsupported`，不能忽略该字段后继续打开 Session。
- 原生明确拒绝模型或 effort 时保留原生原因并返回 `native_rejected`；只有原生
  派发结果本身无法确定时才返回 `outcome_unknown`。不得因配置失败自动换成默认
  模型、降低 effort 或重试。

`Resume` 携带非空值时请求原生恢复路径应用这些值。Codex 的原生 resume 接受
模型和配置覆盖；Claude 以带 `--resume` 的新进程启动，因此创建参数可用于该次
恢复。若以后某个 provider 不能接受恢复覆盖，它必须在副作用前拒绝，不能悄悄
沿用旧值或忽略请求。

#### 运行时更新

支持运行时更新的 Session 实现 `ConfigController`。调用 `SetConfig` 时每次使用一个
配置 ID 和一个值。支持的 ID 为 `model` 和 `effort`。[配置接口和回执](../../../agent/session_config.go)

该接口沿用以一个配置 ID 和一个值更新 Session 的请求形状，但不复制任意配置
目录、布尔选项或 provider 自定义配置项。常量值是统一层 ID，不是任一
provider 的原生 config ID；adapter 负责映射到对应原生字段。

- 只接受本节定义的两个 `ConfigID`。未知 ID、空 ID、空 `Value` 和无法编码的值
  在原生写入前返回 `invalid_argument`；运行时空字符串不是“省略”或“恢复默认”。
- 不定义跨 provider 的 `"default"`、清空或别名语义。任何非空值都按原生字面值
  传递；provider 是否接受由原生接口决定，不得由适配器自行替换成默认值。
- 已知但该 Session 不可用的 provider 能力在原生写入前返回 `unsupported`。
- 每次只修改一个字段，不提供“模型与 effort 同时原子更新”的请求。统一层
  不定义多个并发 `SetConfig` 的全局顺序；调用方需要顺序时必须自行串行调用。
- 适配器不合并请求，也不与 `StartOrSteer`、`Interrupt` 或 `Close` 建立隐式
  调度。
- 更新只承诺影响后续 turn。若当前仍有执行，是否立即影响该执行由原生行为
  决定；适配器不等待 idle、不中断、不 steer、不重建进程，也不自动重试。
- 成功回执的 `Acceptance.NativeAccepted` 只表示原生接受了控制请求。
  统一层不缓存当前配置，也不从回执、请求值、后续 turn 文本或耗时推断配置
  是否生效。原生拒绝返回 `native_rejected`；派发结果未知返回
  `outcome_unknown`。
- 原生写入前失败时 `Sent` 为 false；原生拒绝时 `Sent` 为 true 且
  `NativeAccepted` 为 false；结果未知时 `Sent` 为 true 且 `NativeAccepted`
  保持 nil。调用方必须按错误分类处理，不能把失败或未知结果当成已生效。
- 重复设置同一值不是幂等成功证明，也不保证产生可观察的变更。适配器不因本地
  相等判断跳过原生更新。

运行时更新不自动作用于子 Agent，也不把成功回执转换成统一的“已生效”事件。
本版不新增配置更新事件，`SetConfig` 的结果只通过 `ConfigReceipt` 交付。
`EventSessionConfigOptionUpdate` 保持既有 ACP-shaped 合同；当前 provider 不能
提供完整 option catalog，因此 adapter 不合成完整快照。`thread/settings/updated`
等原生通知无法形成完整 catalog 时继续按既有 `config_options_unsupported` gap
交付。

#### Adapter 映射

provider 映射如下：

| 配置 ID | Codex `Open` / `Resume` | Codex 运行时 | Claude `Open` / `Resume` | Claude 运行时 |
|---|---|---|---|---|
| `model` | `SessionConfig.Model` 映射到顶层 `model` 参数 | 原生 `SetModel` 映射到 `thread/settings/update` 的 `model` | `SessionConfig.Model` 映射到 `--model=<value>` | 原生 `SetModel` 映射到 `set_model` control request |
| `effort` | `SessionConfig.Effort` 映射到配置键 `model_reasoning_effort` | 原生 `SetThinking` 映射到 `thread/settings/update` 的 `effort` | `SessionConfig.Effort` 映射到 `--effort=<value>` | 原生 `SetEffort` 映射到 `get_settings` 预检后的 `apply_flag_settings.effortLevel` |

Codex 恢复继续显式使用 `excludeTurns: true`，配置传递不得改变恢复身份或把
保存历史转换成 live 事件。Claude 恢复按只读 `get_status` 核对 Session ID 和 cwd，并校验后续原生 init；
统一层不从恢复响应或 init 保存当前配置状态。

Codex 的运行时 thread settings 需要 experimental API；初始化时未启用该能力，
必须在写入前返回 `unsupported`。`thread/settings/update` 的接受回执不是配置
已生效的证据。

Claude 的运行时 effort 必须通过原生 `claude` 包提供的
`apply_flag_settings` control request 实现，不能映射到
`set_max_thinking_tokens`，也不能通过重启进程应用。模型与 effort control ack
只表示原生接受，不建立当前配置快照。

`apply_flag_settings` 会合并 flag layer。Claude 在改变 `effortLevel` 时还会
依据当前 `ultracode` 值保持或关闭该设置；原生 `SetEffort` 必须保留不相关且
仍然有效的 flag settings，或在无法确认当前值时在写入前返回 `unsupported`。
统一层不暴露、推导或修改这些额外的原生设置。

#### 不支持的共享配置

本版明确不支持 `sandbox`、`approvalPolicy` 和 `permissionsProfile`：
它们既不加入 `SessionConfig`，也不加入 `ConfigID`。统一 adapter 不做同名
映射，也不依据模型或 effort 推导其值。调用方需要这些原生能力时使用对应原生
SDK，不能把它们塞入统一会话配置。

### 能力与功能

能力常量及 `Capabilities.Supports` 的识别范围见[能力声明源码](../../../agent/capabilities.go)。

- `Start` 返回 Core 的基础支持面；`Session.Capabilities()` 返回该 Session
  实际可用的支持面，可以等于或窄于 Core。
- 缺失或 false 都表示调用方不能假定支持。能力存在也不绕过当前 Session 状态。
- `EffectiveFeatures` 表达本次请求的有效 adapter 映射，不能仅从 `Capabilities`
  推断；它不是运行时工具清单或安全隔离证明。`RestrictLocalTools` 的部分支持
  按[限制本地工具](#restrict-local-tools)解释。
- `AdditionalSystemPrompt` 是 `Open`/`Resume` 输入，不是运行时能力或 feature；
  它不进入 `Capabilities`，也不能通过 `EffectiveFeatures` 表达。
- `processEnv` 表示非空 `OpenOptions.ProcessEnv` 能被该 Session 解释为自有
  CLI 进程环境的 additive overlay；`shellEnv` 表示非空 `ShellEnv` 能被按
  shell-only 语义解释。两者不互相蕴含，也不表示 MCP、hook 或其他任意子进程
  必然继承。
- `promptTrace` 表示来自 context 的有效 carrier 或 tracer-created child
  carrier 会被尝试送入 provider native 提交路径。传播是 best-effort：
  carrier 无效、Claude 完整 control 后的拒绝或等待超时、并发覆盖只影响 trace
  归属，不改变健康 transport 上的输入接纳与业务错误；实际 transport 写入失败
  或挂起遵循正常失败和取消合同。它不表示本地 span 存在；配置了 `Tracer` 的 Session 总
  可使用同一 context 创建本地 span，即使 `promptTrace` 为 false。该能力为
  false 时 context carrier 不返回 `unsupported`，只是不进入 provider request。
  它也不保证每次调用产生新的 provider span，或隔离 provider turn、进程环境
  和后续工具；这些 provider 差异以 [Prompt trace](#prompt-trace) 为准。
- Codex 与 Claude 都支持文本输入、权限、表单、URL 和 `promptTrace`。`AgentQuestions`
  只控制原生模型提问工具。独立 MCP 表单与 URL 请求有各自的交互入口。
  [Codex 包合同](../../../agent/codex/doc.go) · [Claude 包合同](../../../agent/claude/doc.go)
- Codex 支持 `shellEnv`，Claude 支持 `processEnv`。调用方依据当前 Session 的能力判断
  输入类型及其余可选操作的可用性。

## Session

`Session` 和回执类型由[公开接口源码](../../../agent/session.go)定义。

### 生命周期与状态

- `ID` 在 `Open`/`Resume` 返回时必须非空，且在整个附着生命周期内保持不变。
  Codex 以启动/恢复响应确认身份；Claude 新建时按创建规则取得原生身份，
  并核对后续原生主消息。恢复时以关联的只读
  `get_status` 探针确认身份，init 与 hook lifecycle frame 不代替该探针。
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

回执描述调用返回时可以证明的事实。

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

<a id="context-inject"></a>

### 仅注入上下文

[上下文注入接口和回执](../../../agent/inject.go)

- `Injector` 是 Session 的可选接口。它把用户上下文追加到本 Session，不请求
  新的模型响应；不调用启动、steer 或 interrupt，不建立平台输入队列，不等待
  idle，也不自动重试。不支持时返回 `unsupported`，不得降级成普通 prompt。
- 输入在原生写入前完成校验和复制。初版沿用各 adapter 的文本限制：Codex
  接受有序文本块，Claude 接受单个文本块；均拒绝 annotations 及非文本内容。
  注入内容是 user context，不提升为 system 或 developer 指令。
- `Inject` 与 `StartOrSteer` 共用本地写入准入；已有输入正在写入，或前次
  `StartOrSteer` 结果未知时，`Inject` 返回 `failed_precondition` 和
  `Sent=false`，不写入。`Inject` 在途时的 `StartOrSteer` 仍返回
  `not_applied`。需要确定顺序的调用方须串行调用。
- closing/closed Session 拒绝新注入。成功注入不创建 work identity，不更新
  active work、交互归属、中断候选或 Session state；已经运行的工作保持原生语义。
- 回执只表达当次观察到的接受阶段，不表示消费时机、持久性、幂等性或恢复后仍可见。
  本地拒绝填 `Sent=false`；Codex 原生明确拒绝填 `Sent=true`、
  `NativeAccepted=false`；未知写入返回 `outcome_unknown`，调用方不可假定未应用
  并直接重试。注入失败不创建运行候选，也不把未知消费状态伪装成工作状态。

| Provider | 原生操作与版本边界 | 成功回执与消费边界 |
|---|---|---|
| Codex | `thread/inject_items`，将文本编码为 Responses API 的 user message / `input_text`。固定 0.162.1 schema 已声明该方法；0.166.0 来源说明追加历史且不启动 user turn。以实际 RPC 为准，method-not-found 映射为 `unsupported`，不猜测 server user-agent 版本。 | `Sent=true`、`NativeAccepted=true`；不填 `NativeRecorded`。idle 时留待后续模型请求；running 时由原生 pending input 机制决定消费时机，不保证某个 tool call 或模型步骤之前可见。 |
| Claude | user envelope 的 `shouldQuery=false`，不附加 query priority。依据官方 TypeScript 的该字段合同，要求原生 live init 确认符合 SDK 最低 CLI 版本要求；缺少版本、旧版本和预发布版均返回 `unsupported`，不另设功能级版本白名单。仅以 callback 确认身份且尚无 live init 的恢复 Session 也不启用。 | 成功只填 `Sent=true`；官方类型描述为 transcript append，并入后续会 query 的 user message。写入成功不证明 CLI 接受、记录或消费，也不保证运行中当前 turn 消费。 |

原生来源、具体测试命令与录制证据分别维护在对应 SDK 的来源说明和测试入口；
来源合同、合成协议测试、真实录制仿真与 Go SDK 真实 E2E 是独立证据。

## 输入与内容块

[输入与内容块源码](../../../agent/input.go) · [内容块 JSON 编码](../../../agent/content.go)

内容块采用与 MASS 共享合同相同的 ACP 五种类型，不增加任意 JSON 块。
`ContentBlock` 的五个指针分支必须且只能有一个非 nil；JSON 继续使用原有 `type`
判别字段和对应分支字段。数组顺序
是内容顺序；`image`、`audio` 的 `Data` 和资源的 `Blob` 是不带 data URI 前缀的
Base64 原文。`resource` 必须且只能在 `Text`、`Blob` 中提供一种正文。

`Input.Content` 不能为空。调用时会复制并校验内容；已知不支持的类型在原生写入
前拒绝。两个 adapter 的统一输入当前支持文本。

Claude 观察到同时含文本和 `tool_result` 的原生 user frame 时，整帧报告
`user_message_mixed_content_unsupported`。纯文本数组作为一个 `UserMessage.Content`
交付，纯工具结果数组按工具结果交付。[Claude 混合内容 ADR](../../adr/0001-claude-mixed-user-content.md)

## Prompt trace

<a id="prompt-trace"></a>

Prompt trace 是单次输入调用元数据，不是环境变量，也不是 Session 持久状态。
它通过 `context.Context` 与 `StartOrSteer` 关联；统一层只使用 W3C Trace
Context 原文，不解析 trace 后重建 ID，不依赖某个 OpenTelemetry SDK。

SDK 合同与原生 Agent 行为分开：SDK 只提取并校验 carrier、创建可选本地
span、把它映射到原生提交入口，并保持 best-effort。provider 是否创建 span、
steer 是否复用原 turn trace、settings env 是否被后续工具或排队 prompt 读取，
都由 Codex/Core 或 Claude CLI 决定；这些差异被接受，但不是 SDK 新增或保证的
语义。

[TraceContext、Tracer 和 Span 源码](../../../agent/trace.go)

`OpenOptions` 不增加 tracer。具体 adapter 的 `Config` 增加可选
`Tracer agent.Tracer`；Core 构造时固定，调用方可实现按租户路由的 tracer，
但该路由不进入统一 Session 合同。

`Tracer.StartSpan` 同时收到调用 context 和可选的 W3C parent。`parent` 非 nil
时，实现必须把它作为新建 span 的父级；nil 时才可从 `ctx` 读取自己的 active
span。`ctx` 仍用于 tracer 自己的取消、采样和导出编排。

调用方在每次提交前设置 context carrier：

```go
ctx = agent.WithTraceContext(ctx, traceCtx)
receipt, err := session.StartOrSteer(ctx, input)
```

`TraceContext` 只作为 Go 调用元数据存在，不进入 `Input`、统一 `Event`、`Meta`
或持久化 Session 状态。`StartOrSteer` 在调用 tracer 或写入原生帧前读取并复制
当前 carrier，之后不再读取可变状态。

- `Traceparent` 的合法形式遵循 W3C：当前接受 version `00` 的固定
  `00-<32 lowercase hex>-<16 lowercase hex>-<2 lowercase hex>` 形式，以及
  非 `ff` 未来版本的同一基础字段和规范允许的扩展后缀；trace ID 和 parent ID
  不得全零。
- `Tracestate` 为空表示省略；非空时必须是合法 W3C `tracestate`，只与有效
  `traceparent` 一起发送，原样保留而不重排或截断。
- 校验按字节进行；CR、LF、NUL、非 ASCII、错误分隔符、错误长度或全零 ID
  视为无效 carrier。

`WithTraceContext` 不执行 I/O，也不返回 error；调用方若需提前检查，可调用
`Validate`。无效 carrier 在 SDK 中按“无 provider carrier”处理，不能写入原生
request，也不能导致输入失败。Tracer 的显式 parent 同样只在载体有效时设置；
tracer 仍可从原 context 读取自己的 active parent。

每次被 Session 接纳的 `StartOrSteer` 按以下顺序处理：

1. 输入接纳阶段读取 `TraceContextFromContext` 并尝试校验。无效 carrier 视为
   没有 context parent，不改变输入接纳，也不省略 SDK 本地 span。
2. adapter 完成 adapter-specific 输入校验，并完成本地并发、关闭和未知状态
   接纳。未接纳的调用不启动 span。
3. 配置了 tracer 时，在本地接纳输入后、原生写入前创建一次
   `agent.start_or_steer` span。context carrier 存在时作为显式 parent；不存在
   时 tracer 可从同一个 `ctx` 取得自身可识别的 active parent。tracer 返回 nil
   表示没有 SDK span。
4. span carrier 有效时优先作为 provider trace carrier；span 缺失或 carrier
   无效时回退到有效 context carrier。两者都不存在时用零原生 carrier 覆盖传入
   context 中可能残留的原生 helper 值，再跳过 provider 传播。
5. Session 宣告 `promptTrace` 时，adapter 尝试把选定的有效 carrier 送入原生
   提交路径；未宣告时跳过 provider 传播，但上面的本地 span 仍正常结束。
   provider control response 是 best-effort，拒绝或等待超时不能改变健康
   transport 上的输入接纳；transport 写入失败或挂起遵循既有失败和取消合同。
6. 对每个非 nil span，`StartOrSteer` 返回前恰好调用一次 `Span.End(err)`。
   span 只覆盖本地接纳、可能的原生写入和调用返回，不覆盖 turn 开始、执行
   完成、审批等待或后续事件。

实现不得从 tracer/span 回调阻塞网络、等待导出或 panic；正常 telemetry 失败
必须退化为无 span，不能改变 `StartOrSteer` 的业务结果、取消或关闭。SDK 不
自行生成 trace/span ID；没有 tracer 时只有 context 中的 W3C carrier 会被
传播。SDK 对 tracer/span 的调用做 panic 隔离，panic 与普通 telemetry 失败
一样退化为无 span。

不引入 OpenTelemetry 或其他 tracing SDK 作为生产依赖。`TraceContext`、
标准库 context helper 和 `Tracer`/`Span` seam 已覆盖 W3C parent propagation
与一次提交 span；OpenTelemetry 调用方可在仓库外部实现 adapter，从同一个
`context.Context` 读取 active span、开始子 span，并用 W3C propagator 生成
`TraceContext`。把 OTel 引入唯一 module 会扩大所有使用方的依赖图和版本兼容
面，也会把 SDK 合同绑定到一种 telemetry 实现。多个上层应用若需要相同 bridge，
应在 SDK 外部共享 adapter，而不是进入 `agent`、`codex`、`claude` 或原生
`types` 的生产依赖。

SDK 对 provider 的映射义务：

| 项 | Codex | Claude |
|---|---|---|
| context carrier | 作为本地 span parent；Session 宣告 `promptTrace`，有效时写入原生请求 | 作为本地 span parent；Session 宣告 `promptTrace`，有效时映射到原生 `claude.TraceContext` |
| SDK span | 配置 tracer 时为每次已接纳的 `StartOrSteer` 创建 | 同样创建，只覆盖 SDK 调用 |
| 原生提交入口 | JSON-RPC request envelope 顶层 `trace`；不在 `turn/start.params` 内 | 同一常驻 CLI 上，在写入 user frame 前通过 `apply_flag_settings.settings.env` 设置 `TRACEPARENT`/`TRACESTATE`；不重启进程 |

以下是原生 Agent 行为，只用于说明两个运行时的差异，不是 SDK 合同：

| 项 | 原生 Codex | 原生 Claude |
|---|---|---|
| busy turn / steer | 原生 Core 把 `turn/start` 接纳为 steer 时不创建新的 turn 执行 span，继续使用原 turn 的 trace；新 carrier 不替换 active turn trace | 不适用 |
| trace 状态范围 | 每次 request 的 envelope carrier 独立；入队输入不再携带 carrier，后续模型/工具使用 turn 建立时的 trace | settings env 是 CLI 进程级状态；快速排队的前序 prompt、当前 turn 的后续新工具或新建子进程都可能看到后一次 env；已有进程不保证更新 |
| shell/MCP 可见性 | prompt trace 不自动变成 Bash/MCP 子进程的 trace env | 新启动的 Bash/PowerShell 或其他工具子进程可能继承当前 `TRACEPARENT`/`TRACESTATE`，是否读取由 CLI 和工具决定 |
| provider span | SDK 不保证原生实现是否创建、复用或关联 span | 是否导出关联 span 受 CLI 版本、telemetry 配置和工具行为影响；SDK 不保证 |

这些原生结果不会改变 SDK 的输入接纳或业务错误，也不能由 SDK synthetic 测试
证明。SDK 不模拟原生 steer 状态机、不重定向 turn trace、不按工具调用快照
隔离 env，也不把 settings 更新成功解释为 provider span 已关联。配置 `Tracer`
时，SDK 仍为每次已接纳的 `StartOrSteer` 创建自己的
`agent.start_or_steer` span；它不是 provider span，也不改变上述原生 trace
归属。

统一 Codex adapter 把 `agent.TraceContext` 转换为原生
`types.W3cTraceContext`，再通过 `codex.WithTraceContext` 把 carrier 放入传给
`codex.Session.Submit` 的 context。原生 helper 只由 `Session.Submit` 消费；
低层 `Client.Call`、`StartThread` 和 `ResumeThread` 不增加 trace 参数，也不
隐式读取该 context value。

Codex 的 envelope `trace` 使用：

```json
{
  "id": 7,
  "method": "turn/start",
  "params": {"threadId": "thread-1", "input": []},
  "trace": {
    "traceparent": "00-...-...-01",
    "tracestate": "vendor=value"
  }
}
```

`trace` 与 `params` 平级，params 内不新增同名字段。envelope 有有效 carrier 时
provider 使用它；没有时 app-server 可以按自身行为回退到进程 `TRACEPARENT`，
SDK 不为每个 prompt 改写进程环境，也不把该回退作为 SDK 行为。SDK 只写入本次
request 的 envelope carrier；不承诺它变成 Bash/MCP 子进程的 trace env。需要
固定 shell trace 时只能另行设置 `ShellEnv`，不能得到 per-prompt 变化。

原生 Codex 可能把 busy turn 上的 `turn/start` 判为 `Steered`。这是 pinned
Codex/Core 原生行为，不是 SDK 新增或保证的语义：本次 RPC 的 envelope `trace`
最多作为该次 request 和输入接纳路径的 parent；原生实现可把既有 `turn.id`
记录到该 request span，但不会改写 active turn 的 trace context，也不会回溯
重挂已经产生的 span。输入被接纳后，Core 队列中的 `TurnInput` 不带 trace
carrier，因此该 turn 后续的模型、工具和执行工作仍使用 turn 建立时的 trace。
SDK 不模拟这套状态机，也不承诺“新 carrier 会替换或延续 active turn trace”。
若调用方希望同一 turn 的多条输入在观测上可关联，应复用同一 carrier，并在
自己的观测系统中按 `turn.id` 或其他业务身份建立关联。显式低层 `turn/steer`
不属于本设计路径；统一 adapter 只使用 `turn/start` 的原生 start-or-steer
语义。

统一 Claude adapter 把 `agent.TraceContext` 转换为原生 `claude.TraceContext`，
再通过 `claude.WithTraceContext` 放入传给 `claude.Session.Submit` 的 context。
原生 helper 使用私有 context key。[Claude trace 源码](../../../claude/trace.go)

`Session.Submit` 在原生写入前提取并复制 carrier，按
[prompt trace](#prompt-trace) 的 W3C 规则尝试校验。它复用 Claude CLI 已经
用于运行时 effort 的 `apply_flag_settings` 路径，在同一个常驻 CLI 上逐 prompt
切换 trace；不得因 prompt trace 重启进程。`Session.Submit`、trace settings
更新和 `SetEffort` 共用一个本地串行 gate，保证 apply 与 user frame 两个写入
不被其他 SDK 调用插入。只有需要改变 trace 状态时，才发送 control request：

1. 有效 prompt trace 的目标值是 carrier；无 prompt trace 但此前应用过覆盖时，
   目标值是 `Open` 时从有效进程环境捕获的 startup baseline，没有 baseline
   则两个变量都为空；无 prompt trace 且没有旧覆盖时跳过 settings 更新。
2. 发送 `apply_flag_settings`，`settings` 只增加 `env` 字段：

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

   两个变量总是同时出现。没有 `tracestate` 时写空字符串，不能用省略字段的
   方式清除旧值。该请求不得清除不相关的 flag settings，尤其不能改写
   `effortLevel` 或 `ultracode`。
3. 完整写出 control frame 后的 settings response 是 best-effort。成功时记录
   目标状态；拒绝、等待超时或未知响应时记录状态未知，只要 transport 和调用
   context 仍可用，就继续写入 user frame。control 写入阻塞时使用既有取消机制
   关闭 transport 并释放 gate；部分写、短写或其他 transport 写入失败终止交互，
   返回既有 transport 错误和未知结果，不追加 user frame 或返回 `Written=true`。

下一次提交在状态未知时重新写完整目标值；这是唯一的重试机制。SDK 不等待
prompt 开始信号，不维护 pending UUID 或 dequeue acknowledgment，也不等待
整个 turn。等待 control response 或调用 context 结束仅用于确定 apply 结果，
不是 prompt 出队确认。若前一条 prompt 仍由 CLI 内部队列暂存且尚未取走，后
写入的 env 可能作用于它；该 trace 归属误差被明确接受。无 prompt trace、且
此前没有 trace override 时，不产生额外 control roundtrip。

以下现象来自 Claude CLI 原生行为，不是 SDK 新增或保证的语义：settings env 是
CLI 进程级状态，不提供 prompt/turn 隔离。trace env 写入后，当前 turn 的后续
新工具调用、工具启动的新子进程或随后 prompt 都可能读取到当前值；已经启动的
进程不保证更新。SDK 的合同止于在 user frame 前尽力执行 settings 写入并保持
本地顺序；它不按工具调用快照或重写环境，也不把下一 prompt 的 baseline 恢复
解释为收回已经传播的 trace。该跨 prompt、跨工具的影响是明确接受的原生 Agent
行为；需要严格归属时，调用方必须使用原生 Agent 自己保证隔离的机制，不能
仅靠本 SDK 的 context carrier。

上述能力属于 Claude 原生 stream-json/control 合同，不是 ACP 兼容层。
ACP adapter 若接入 `_meta`，应把字段映射到同一原生 helper。SDK 不依赖
ACP 社区实现，也不复制其严格归属状态机。设置 trace env 成功只证明 CLI 接受了
settings 更新，不证明目标 CLI 版本导出正确关联的 provider span。

## 交互与回答权限

[交互请求、回答和校验源码](../../../agent/interaction.go) · [交互请求 JSON 编码](../../../agent/interaction_json.go)

- `permission` 必须提供至少一个原生选项。`ToolCallID` 是可选的工具调用关联字段。
  Codex 将带 MCP 工具审批标记的 elicitation 映射为 `permission`，此时 `ToolCallID` 为空。
  回答使用 `InteractionRef`，并从原始请求实际提供的 `optionId` 中选择。
  [Codex MCP elicitation 映射](../../../agent/codex/mcp_elicitation.go)
- `form` 提供 `Message` 和 `RequestedSchema`。adapter 只校验自己确能表达的 schema 子集。
  多选问题可接受字符串或字符串数组；
  adapter 将字符串数组以 `", "` 连接成字符串后再写回原生。原生数组答案仍拒绝，
  不能把统一层的兼容编码当成原生数组支持。
- `url` 只表达外部流程入口；URL 的完成仍由原生事实结算。

`MatchAnswer` 和 `ValidateAnswer` 只作本地校验。Adapter 在取得回答权限前继续核对
原生选项及表单 schema。校验失败保留原回答权限和具体错误原因。
[回答校验源码](../../../agent/interaction.go)

原生 `IncomingRequest`、`AnswerHandle` 和 permission handle 只存在于 adapter
私有 registry。统一事件只交付 `InteractionRef`。该引用绑定 Session、adapter
实例、当前运行和具体工作；回答前必须再次检查原句柄仍有效。跨 Session、旧运行、
过期请求、已回答请求和伪造引用全部拒绝。

并发回答在单个交互内串行接纳，至多一个回答进入原生提交。事件可以先于 answer
回执到达。发送结果未知时保持已消耗的回答权限，避免重复提交。

共享 `interactionledger` 每 Session 一份，只分配不透明 `InteractionRef`、单调
注册顺序并保存 pending、answering、sent、settled 等统一权威阶段。它负责一次
claim、answer 完成等待、确定 `NotSent` 后的回滚，以及 terminal result、sent
outcome 与 fallback 的结算顺序。等待不得持有 adapter 锁。slot 不保存原生
request、raw frame、request ID、handle、reply callback 或 ref→adapter 查询；
adapter 的私有 registry 仍是唯一能从引用取得原生句柄并构造回复的位置。因此
共享 slot 只协调回答准入，不转移原生回答权限，也不形成第二套句柄 registry。

## 事件流与统一事件

`EventStream.Next` 由唯一消费者串行调用。[事件流接口](../../../agent/event.go)

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

### Session 附着、关闭与原因稳定性

每个 adapter Session 构造一个 `eventstream.Stream`。原生观察循环由 adapter
启动并负责接收、转换、事件信封和错误映射；转换结果通过 `Publish` 入队，
循环退出时调用 `ObservationEnded(cause)`。共享模块不解析原生数据，也不查询
或结算交互引用。

`Claim` 只允许一次，且不启动原生观察。自然终止发生在首次取流之前时，已经
接纳的事实仍可通过该次 claim 排空；显式 `Close` 一旦开始，后续 claim 返回
`session_closed`。已取得的 reader 保持排空能力。

显式 `Close` 与自然 `ObservationEnded` 竞争同一条清理路径：

1. 首次关闭入口标记 closing，并同步调用一次 `BeginClose`。adapter 在该 hook
   中禁止新的操作并快照未结算交互；此时不发布这些交互的关闭事件。
2. 清理路径调用一次 `CloseNative`，缓存其返回值。adapter 负责停止原生附着或
   Session 自有进程，使观察循环可以退出；不关闭 Core 的共享连接或其他 Session。
3. 等待 `ObservationEnded`，保证观察循环不再调用 `Publish`。自然终止先到时，
   该信号可以早于 `BeginClose` 和 `CloseNative`；显式关闭先到时，清理路径等待
   adapter 报告观察结束，不能由调用方 context 取消跳过此等待。
4. 保留第一次 `ObservationEnded` 提供的非 nil cause。该 cause 为 nil 时，
   才调用 `FinalCause` 从已完成清理的原生状态取得 fallback，并在生成关闭事件
   前固定最终 cause。后续 Close 或 ObservationEnded 不能覆盖它。
5. 调用一次 `CloseEvents`。adapter 按自身原生权威规则结算步骤 1 的快照并生成
   有序关闭事件；共享模块通过 `Publish` 将该批事件追加到已接纳事实之后。
6. 用最终 cause 关闭 event queue。reader 排空后，nil cause 对应 `io.EOF`，
   非 nil cause 对应同一稳定错误；queue close 后不再接受事件。
7. 调用一次 `Release`，从 `corelease` 移除该 Session lease。
8. 关闭 `Done`。观察到 Done 后，`Err` 与 queue 的最终 cause 一致，且该 Session
   不再阻止 `Core.Stop`。事件消费者是否已经排空不影响 lease 释放。

`Session.Close` 等待上述路径完成，重复调用返回同一个 `CloseNative` 清理结果；
该结果与观察终止 cause 分开保存。清理错误不覆盖已确定的观察原因。
`Err` 在 Done 之前可能尚未完成 fallback，调用方须在 Done 后读取稳定最终值。
即使显式关闭与自然终止同时到达，native cleanup、关闭事件、queue close、
lease release 和 Done 都只执行一次。

### 事件信封

[事件信封与校验](../../../agent/event.go)

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

[事件类型](../../../agent/event.go) · [Payload 源码](../../../agent/payloads.go) · [工具分类源码](../../../agent/tool.go)

`agent.Event` 使用 SDK 自己的信封。MASS 消费统一 payload 时为其补充应用侧字段。

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

统一层暂不定义 adapter 无关的计划语义。Claude 的 `TodoWrite`、
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

`CodeOf` 使用稳定的分类值。分类取自原生证据和 SDK 状态。[错误类型和分类](../../../agent/errors.go)

已知不支持或参数非法必须在原生写入前返回确定错误。`outcome_unknown` 表示可能
已经产生原生副作用，不是确定失败；自动重试会破坏回答、输入或资源释放语义。
`Cause` 保留原生错误，但 `Message` 必须脱敏，不包含凭据或 MASS 恢复材料。

## 能力接口

归档通过可选的 `Archiver` 接口提供。[归档接口](../../../agent/capabilities.go)

不支持的能力不实现接口，或返回 `unsupported`。不得用静默 no-op、空成功或伪造
默认值掩盖差异。归档、模型、思考和子 Agent 查询的 provider 专属语义不进入
基础接口；高级调用方可以使用 adapter 具体类型及其原生入口，但不能绕过基础
Session 的权限和关闭规则。

`ConfigController` 是唯一统一的运行时配置能力；基础 Session 不要求实现。
统一层不提供仅模型 controller 或仅模型 setter。原生 `codex`、`claude` 包中的
`SetModel`、`SetThinking`、`SetEffort` 及其原生 `ConfigurationReceipt` 保持
各自合同。

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

Codex 的 `NativeClient()` 暴露 Core 自有的同一条已初始化连接。它只在 `Start`
成功后、`Stop` 前可用。调用方借用此连接进行只读查询，关闭与替换由 Core 负责。
调用方可以将其绑定到只查询接口。[Codex Core 源码](../../../agent/codex/core.go)

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
- MASS 不得另建第二个 `codex.Client` 来读取历史；必须复用 Core 的生命周期
  和数据域。
- 未启动或已停止时返回错误，不得按需创建第二条连接。
- MASS 只在 adapter-specific Runtime 内使用它，不把原生 client、原生查询值或
  查询错误类型暴露到 MASS Service、HTTP 或 SSE。
- 返回值和错误的持有、取消、raw 数据及覆盖边界仍由原生查询合同定义。

## MASS 与 SDK 的职责边界

MASS 拥有 `RuntimeDriver`、`Address`、运行来源、恢复材料、事件汇聚、`SessionEvent`
wire、EventStore 及发布。SDK adapter 拥有单 Session 的原生工作归属、实时转换、原生交互
句柄和一次回答权。MASS 在调用 `Session.Answer` 前核对当前运行，再将原 `InteractionRef`
传给 adapter，由 adapter 核对原工作与原句柄。
[MASS Runtime 设计](https://github.com/zoumo/mass/blob/main/harness/modules/runtime/design.md) ·
[Driver 合同](https://github.com/zoumo/mass/blob/main/harness/modules/runtime/runtime-driver.tsp) ·
[事件合同](https://github.com/zoumo/mass/blob/main/harness/modules/runtime/events.md)

### 实时观察与关闭

1. MASS 先接收 Driver 的唯一事件流，再通过 Driver 调用 `Core.Start`。
2. 打开或恢复前，上层解析配置和凭据引用，合成最终 MCP 列表。
   [Runtime 与 Session 合成](#mcp-runtime-overlay)
3. `Core.Open` 或 `Core.Resume` 成功后，MASS 固定新的运行来源。每个 Session 只有一个
   relay 获取 `Session.Events()`。relay 保留已接纳事件的顺序，将 SDK 事件的 ID、时间、
   item 身份、payload 和 Meta 原样带入 `SessionEvent`，再补充 MASS 身份。早到事件由
   MASS 的 opening 状态归属。relay 按固定的 Driver EventPipe 背压规则写入，保持其他
   Session 的观察和生命周期独立。
4. MASS 调用 `Session.Close` 后继续排空已经接纳的事实。所有 Session 关闭并排空后，
   Driver 调用 `Core.Stop` 并结束 Driver 事件流。

MASS 映射 `Capabilities` 和 `EffectiveFeatures` 时保留 SDK 提供的范围及降级结果。
输入与回答回执保留各阶段证据，取消回执区分无活动工作和原生控制确认。操作结果未知时
保留 `outcome_unknown` 并交由调用方处理。

### 原生历史导入

已经提交的历史由 MASS 直接从 EventStore 查询。导入原生会话由 MASS `LoadSession`
编排，沿用已启动的 Driver 流：

1. 固定 Runtime、Address、loadId、原生 Session ID 和绝对 cwd。Codex 通过借用的
   `NativeClient()` 查询 metadata 和逐页保存内容。Claude 使用显式 scope 调用
   `GetSessionInfo` 与 `GetSessionMessages`。发现接口只提供候选。
2. 调用 `Core.Resume` 恢复原会话，再用原生查询复核身份、范围、覆盖、记录顺序和内容。
   Claude 还核对原生只读 status、同一目录和同一个 transcript 文件身份。
   [原生查询边界](../../contracts/history-query.md)
3. MASS 将原生保存记录转成带 replay 标记的 `SessionEvent` 并写入 Driver EventPipe。
   `Session.Events()` 交付恢复后的实时事实。历史记录不会建立当前工作或回答权。
4. MASS 在读完历史时记录 `native_completed`，待 EventStore 追加和发布都结算后才
   保存 `completed` 并开放输入。成功时继续使用该 SDK Session。失败或取消由 MASS
   释放本次恢复的资源并保留已提交的部分历史。

原生 `Event.IsReplay` 和 `ThreadResumeParams.History` 属于原生协议。普通 SDK 恢复继续
使用实时事件，原生保存内容继续由独立查询交付。协议录制回放和固定录制仿真沿用各自的
材料及测试入口。

## 验收

- 统一核心遵循[生产依赖规则](../../architecture.md)，不导入 `codex`、`claude` 或 MASS。
- 统一核心没有 OpenTelemetry 或其他 telemetry 生产依赖；`Tracer` 的第三方
  adapter 可在仓库外部实现，不能进入 SDK 生产依赖树。
- 两个 adapter 互不导入，原生包不导入统一层。
- `agent/internal/corelease` 的依赖门禁只允许标准库和公开 `agent` 合同；两个
  adapter 都通过该模块覆盖 Start 单次获取、in-flight Open/Resume 拒绝 Stop、
  失败 Open 自动结算、终止性 Stop 与 release 错误缓存，且不保留重复生命周期
  状态、计数器、Session 集合或私有 reserve/complete helper。
- `agent/internal/eventstream` 的直接依赖只允许标准库、公开 `agent` 与
  `internal/queue`；传递依赖仅增加已审核 carrier validator 与 golib/channx。
  两个共享模块均不导入原生 SDK、adapter 或 interaction state machine。
  离线测试覆盖一次 claim、自然终止前的事件保留、显式关闭前拒绝取流、
  关闭事件排序、稳定 cause、清理错误缓存，以及先 release 后 Done。
- `agent/internal/interactionledger` 只保存统一权威状态；其测试覆盖唯一引用、
  单调注册顺序、一次 claim、NotSent 回滚、完成等待和结算优先级。adapter 的
  私有 registry 仍独占原生 request、handle、reply construction 和查询。
- Core 基础 Capabilities 由 adapter 供给并由生命周期冻结；Session 级能力
  只能窄化，不修改 Core 级快照。Claude Start 不启动进程，每个 Session
  仍独占并关闭自己的原生 CLI 进程。
- `Capabilities.Supports` 识别 `processEnv`、`shellEnv` 与 `promptTrace`；
  未知能力仍返回 false，不能仅凭 map key 透传。
- Core、Session、EventStream、Input、Answer 和可选能力有公开接口编译测试。
- Core 进程环境测试固定 Codex 与 Claude 的 nil 继承、非 nil 整体替换和
  slice 复制语义，并覆盖非 nil 空 slice 与 nil 的区分；进程启动前修改调用方
  slice 不影响实际环境。进程环境 sentinel 不出现在错误、事件或日志。
- Claude `ProcessEnv` synthetic 测试固定 `Config.Env` 为 nil 时以一次
  `os.Environ()` 快照为基底、非 nil 时以完整 slice（包括非 nil 空 slice）为
  基底，并按排序 key 追加 overlay；同名项由 overlay 覆盖，空字符串保留为
  空值，其他基底项顺序不变。两个 Session 可使用不同 overlay，调用方 map 和
  base slice 不被修改。
- `ProcessEnv` 的非法 key/value、非空 key 的 ASCII 大小写不敏感重复项在版本
  查询、Claude 进程启动、Codex app-server 启动或 thread RPC 前失败；env
  value sentinel 不出现在错误或事件中。Codex 非空值返回 `unsupported`，不能
  落到 app-server 或 shell 配置；Claude 非空值必须出现在原生
  `SessionOptions.Env` 中。
- Codex `ShellEnv` synthetic 测试固定 `thread/start` 与 `thread/resume` 的精确
  `shell_environment_policy.set`，复核空值省略、additive set、调用方 config
  和 `ShellEnv` map 不被修改、以及路径冲突在原生 RPC 前失败；非法 key/value、
  大小写不敏感重复 key 与列明的 Codex 保留变量被拒绝，env value sentinel
  不出现在错误、事件或日志。
- Claude 非空 `ShellEnv` 在 CLI 启动前返回 `unsupported`，fake peer 未收到
  Session 启动或 RPC；不接受用进程环境或 settings `env` 冒充 shell-only。
- Codex prompt trace synthetic 测试固定 envelope 顶层 `trace` 与
  `turn/start.params` 的分离、无 trace 时字段省略、`agent.WithTraceContext`
  到原生 `codex.WithTraceContext` 的转换、tracer-created child context 的
  优先级，以及非法 W3C carrier 省略 envelope `trace` 但 prompt 仍照常提交。
- Codex 连续提交使用不同 carrier 时，每次 `turn/start` 只编码当次 carrier；
  SDK 不缓存前一次 carrier，也不把 steer 解释成替换 active turn trace。
  该测试只证明 SDK 的 per-call wire 与无状态性；active turn 继续使用原 trace
  属于 pinned app-server/Core 行为，不在 synthetic SDK 测试中伪造。
- 原生 `codex.WithTraceContext` 只由 `Session.Submit` 消费；低层
  `Client.Call`、`StartThread`、`ResumeThread` 不增加 trace 参数，也不读取该
  context value。原生 `types.W3cTraceContext` 按 pinned `rpc.rs` 手写并有独立
  往返测试，协议来源和 capability 记录把 `request-trace-context` 的已支持
  范围限定到这一 Session prompt 路径，不声称其他 raw call 自动支持。
- `Input` JSON 合同没有 `trace` 字段；JSON 往返不携带 context carrier，carrier
  也不进入统一 `Event`、`Meta` 或 Session 状态。
- Trace seam 测试使用 fake tracer 验证每次已接纳的 `StartOrSteer` 至多一个
  `agent.start_or_steer` span、`End(err)` 恰好一次、span 在调用返回前结束；
  tracer 返回 nil 时不调用 `End`；非 nil span 的零值 carrier 不改变输入结果
  且仍恰好 `End` 一次；未接纳调用不创建 span。context carrier 是显式
  parent，非法 carrier 按缺少 provider parent 处理且不改变输入结果；
  `TraceContextFromContext` 只返回当前值，不暗含校验；派生 context 继承该
  value，重复 `WithTraceContext` 以后一次为准。
- Claude prompt trace synthetic 测试固定输入前的精确
  `apply_flag_settings.settings.env`，两个 trace 变量总是同时出现，缺失值为
  空字符串；tracer-created child context 优先于 context carrier，非法 W3C
  carrier 只跳过 provider 传播，user frame 仍照常写入。无 prompt trace 且无
  旧覆盖时不发送额外 control；有旧覆盖时下一条无 trace prompt 恢复 `Open`
  时捕获的 startup baseline，baseline 缺失则写空值。该 request 不清除
  `effortLevel`、`ultracode` 或其他无关 flag settings，也不重启 CLI。
- Claude 完整 control 写入后的拒绝、等待超时或未知响应只使状态未知，健康
  transport 和有效调用 context 上的 `Submit` 仍写 user frame，下一条 prompt
  重写完整目标值。control 写入阻塞须通过取消关闭 transport；部分写、短写或
  其他 transport 写入失败终止交互，不追加 user frame，不报告成功输入。
  快速连续提交不同 trace 时，SDK 不等待 dequeue acknowledgment、
  不新增 pending 队列，也不因同一 Session 有未结束 prompt 而拒绝提交；测试
  固定本地 apply/user frame 顺序并明确允许前序排队 prompt 的 trace 竞态。
  有效 context carrier 不返回 `unsupported`，Session 宣告 `promptTrace`。
  显式 `Config.Env` 或 `ProcessEnv` 的 startup trace 只作为 baseline；设置
  成功不证明特定 CLI 版本已导出关联的 provider span。测试只证明 settings 与
  user frame 的本地顺序，不声称按 prompt/turn 隔离环境，也不证明或禁止后续
  新工具调用继承当前 trace env；该 provider 行为由 live 验证另行取证。
- ACP v2 `session/prompt._meta` 的预留字段不属于本包的 Claude 原生 wire
  合同；synthetic 测试不让 `_meta` 取代原生长连接控制协议。
- `SessionConfig` 的空值省略、非空字面值传递和 `ResumeOptions` 继承规则有
  公开合同测试；不实现可选 `ConfigController` 的自定义 Session 仍可满足
  基础接口。
- `ConfigController` 只有一个 setter，配置 ID 恰好是 `model` 和 `effort`；
  未知 ID、空值、非法编码、已关闭 Session 和不可动态配置的 provider 都在
  原生写入前返回确定错误。
- Codex 合成测试核对 `thread/start`、`thread/resume` 的精确 `model` 与
  `model_reasoning_effort`，且恢复不改变身份或回放保存历史。
- Codex 运行时测试核对 `thread/settings/update` 每次只携带一个目标字段；关闭
  experimental API 时不写入，空 ack 只产生 native acceptance。执行期间更新
  不得隐含等待 idle、中断、重建或重试。
- Claude 合成测试核对 `--model=`、`--effort=` 的精确命令行参数及空值省略，
  恢复还须核对 `--resume=`。
- Claude 运行时测试核对 `set_model` 与
  `apply_flag_settings.settings.effortLevel` 的精确请求，确认 effort 不映射为
  `set_max_thinking_tokens`；不相关且仍有效的 flag settings 不被静默清除，
  且更新不触发进程重启。
- `ConfigReceipt` 只包含精确 `ConfigChange` 和 acceptance，不包含当前配置
  快照；原生写入前失败、原生拒绝和结果未知分别保留 `not-sent`、`native_rejected`
  与 `outcome_unknown` 的可区分语义；重复值仍派发并保留实际 acceptance，不因本地
  相等判断跳过原生更新，不从当前执行结果反推配置生效。
- `EventSessionConfigOptionUpdate` 的 ACP-shaped 合同不变；provider 无法提供
  完整 option catalog 时不合成完整快照。
- Codex 与 Claude 的合成协议输入逐事件比较统一输出；比较类型、payload、身份、
  Meta、顺序和未知数据范围，忽略随机 EventID。
- 每个 Session 只允许一个消费者；重复取流失败；慢消费者不重排事件；关闭一个
  Session 不关闭其他 Session 或 Core。
- 真实原生交互句柄的测试覆盖跨 Session、旧运行、已回答、过期及伪造引用拒绝，
  并验证事件与历史无法重建句柄。Codex MCP 工具审批测试覆盖缺省 `ToolCallID` 时
  仍通过原引用和选项回答。Codex 与 Claude 测试分别覆盖独立表单与 URL 的回答结算。
  [Codex 测试](../../../agent/codex/mcp_elicitation_test.go) · [Claude 测试](../../../agent/claude/elicitation_test.go)
- MASS 的 wire JSON、历史、SSE、恢复、acceptance 和回答行为保持兼容。
- MASS 普通历史查询只调用 EventStore；原生 load 通过 Codex `NativeClient()`
  的只读查询或 Claude package-level 查询读取保存内容，由 MASS 转换并标记
  replay；`Core.Resume` 不自动回放历史，`Session.Events` 不承担历史补读。
- 统一 `agent` 层没有 `NativeHistory`、`HistoryReader`、`HistoryEvent` 或
  等价历史抽象，也没有 `LoadSession` 或 replay 方法。
- `agent.Event` 包装为 `SessionEvent` 时 EventID、Time、ItemID、payload 和
  Meta 不被改写。
- SDK 内部 history replay 投影已删除；原生历史查询、`Event.IsReplay`、
  `ThreadResumeParams.History`、录制回放和 simulation replay 仍可用。
- 除 Claude 原生 `AppendSystemPrompt`、trace context/settings 映射和 Codex
  原生 `ShellEnv`、prompt trace envelope 的增量字段外，原生 `codex`、
  `claude` 及其 `types` 的公开接口和默认测试不因统一层变化；Claude
  `AppendSystemPrompt` 由 `OptionsFromTypes`/`Types()` 往返，并由进程启动
  测试固定 argv。
- 统一层只存在 stdio 和 Streamable HTTP MCP；没有 SSE transport 或
  `MCPConfigMode`。Runtime 默认项与 Session 同名项由上层合成，Session 项整体
  覆盖，且 Core 不再二次合并。
- MCP synthetic 测试固定 Codex thread config 与 Claude argv 的精确映射；无法
  表达的字段在原生副作用前失败，secret sentinel 不出现在错误、事件或日志。
- Additional system prompt synthetic 测试固定空值省略、Unicode/换行原样传递、
  Codex `thread/start` 与 `thread/resume` JSON、Claude
  `--append-system-prompt=` argv、`Resume` 重新提供完整文本，以及非法
  UTF-8/NUL 在原生副作用前失败；prompt sentinel 不出现在错误、事件或日志。
- 本版统一选项中不存在 `sandbox`、`approvalPolicy` 或 `permissionsProfile`
  映射，也不把这些原生值从其他请求字段反推出来。
- 默认验证离线运行。分别记录公开接口与合成协议、固定录制仿真及真实 Agent 的验证结论。

### Claude 恢复身份验收

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
