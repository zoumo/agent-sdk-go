---
type: Decision
title: 环境与 prompt trace 按作用域分离
status: accepted
date: 2026-11-11
---

# 环境与 prompt trace 按作用域分离

## 背景

调用方需要为 Codex app-server、Claude CLI、Session 内 shell 命令和单次 prompt
提供环境或追踪信息。这些需求都容易被简写成“传一个 env”，但它们的生命周期
和影响范围不同：

- Provider 进程环境决定 Codex app-server 或 Claude CLI 的基础启动环境。Codex
  的所有 Session 共享 app-server；Claude 每个 Session 自有 CLI 进程，可在
  Provider 基底上再应用自己的 Session overlay。
- Codex 的 thread config 可以为 shell/unified-exec 子进程增加环境变量，但不
  修改 app-server 自身环境。
- 原生 `SessionOptions.Env` 可以为 Claude Session 自有 CLI 进程提供独立环境；
  它会影响 CLI 及其更多子进程，不能称为 shell-only。
- Claude 的 `Env` 和 settings `env` 进入 CLI 进程环境，没有等价的 shell-only
  入口。
- Prompt trace 是单次输入的调用元数据。Go `context.Context` 已随每次
  `StartOrSteer` 传递，适合承载 parent carrier；Codex 可把它编码到 JSON-RPC
  request envelope。Claude CLI 在取走 prompt 时读取进程环境；SDK 可以在同一个
  常驻 CLI 上，于每条 prompt 提交前用 `apply_flag_settings.settings.env` 切换
  `TRACEPARENT`/`TRACESTATE`，下一条无 trace prompt 再恢复启动环境。该能力
  不要求重启 CLI。
  Trace 是观测信息，不值得为了精确归属增加 acknowledgment、等待或拒绝提交；
  已经由 CLI 排队但尚未取走的 prompt 可能继承后一次写入的 env，这是接受的
  限制；SDK 不维护另一层 prompt 队列。
  ACP 兼容与该原生 control 映射是两条路径。

Codex 在 steer 时复用原 turn trace、Claude settings env 在进程内扩散，都是
Provider 原生行为。它们解释 SDK 为什么只能做 best-effort 映射而不能承诺统一
trace 生命周期，但不是 SDK 新增或保证的语义。

如果统一层用一个 `Env` 字段同时表示这些能力，调用方会在 Codex 与 Claude 上
得到不同作用域却看到相同名称，无法判断变量是否会影响认证、CLI 配置、MCP、
hook、shell 或 trace。

## 证据

- Codex `thread/start`/`thread/resume` 的 `config` 接受
  `shell_environment_policy.set`；该 policy 只用于构造 Codex 启动的 shell /
  unified-exec 环境。它不修改 app-server 进程，也不保证 MCP 或其他任意子进程
  继承。
- Codex 的 JSON-RPC request envelope 有独立于 params 的 `trace` 字段，
  `turn/start` 可把该 carrier 关联到 request/内部提交 span。没有证据表明
  prompt trace 会自动变成 Bash 子进程的 `TRACEPARENT`。
- 在 pinned Codex commit
  `092d3acd6bec3e3a14bdc7e7a2810ab628ab759d` 中，`turn_start_inner` 通过
  `start_or_steer_turn(...with_trace(...))` 传递 carrier，并可在 busy turn 上
  被 Core 判为 `Steered`。该提交的 dispatch span 使用本次 carrier，
  `codex.turn_input` span 记录既有 `turn.id`，但 `steer_input` 只把不带 trace
  的 `TurnInput` 放入 active turn 队列；active turn task 及其后续模型/工具
  工作继续使用原 trace。显式
  `turn/steer` handler 在该版本没有把 envelope carrier 放入
  `TurnInputRequest`，因此不属于 prompt trace 合同。
- Claude CLI 在 stream-json 模式顺序处理输入，并可在自身内部队列中暂存尚未
  取走的 prompt；env 是 CLI 进程级可变状态，直到 prompt 被取走时才读取。这
  是 CLI 的输入队列，不是 SDK 新增的队列。多条输入快速写入时，后写入的 trace
  env 可能作用到更早尚未取走的 prompt；要消除该竞态需要额外 acknowledgment
  和等待。本设计不采用该复杂度，因为 trace 关联错误只影响观测，不应阻塞
  Agent 输入。
Claude/ACP/CLI 的来源、版本与回归报告归原生 SDK 来源文档维护；本 ADR 只确定
作用域和映射合同，不把来源审阅或 settings acknowledgment 当作 provider span
关联证明。

## 决策

公开四个不同作用域，不建立自动等价：

1. Provider 进程环境继续使用具体 adapter 配置：
    `agent/codex.Config.Stdio.Env` 与 `agent/claude.Config.Env`。nil 继承当前
    进程，非 nil 整体替换。
2. 统一 `OpenOptions.ShellEnv map[string]string` 只表示 Session 级、
    additive、set-only 的 shell 环境覆盖。Codex 映射到 thread config
    `shell_environment_policy.set`；Claude 收到非空值时返回 `unsupported`。
3. 统一 `OpenOptions.ProcessEnv map[string]string` 只表示 Claude Session CLI
    进程的 additive、set-only 覆盖。adapter 以非 nil `Config.Env` 或一次
    `os.Environ()` 快照为基底，按排序 key 追加 overlay，再合成为原生
    `claude.SessionOptions.Env`。它会影响该 CLI 及其子进程；Codex 共用
    app-server，收到非空值时返回 `unsupported`。
4. Prompt trace 只通过 Go `context` 传递，不进入 `Input`、统一事件或持久化
    Session 状态；provider 原生请求中的编码属于 adapter 映射。
    `agent.WithTraceContext` 写入 `agent.TraceContext`，
    `agent.TraceContextFromContext` 读取；可选 `agent.Tracer` 从同一个 context
    创建子 span。Codex 把有效 carrier 放到 JSON-RPC request envelope 顶层
    `trace`；原生层由 `codex.WithTraceContext` 交给 `Session.Submit`，低层
    `Client.Call` 不增加 trace 参数。Claude 原生包提供对应的
    `claude.TraceContext`、`claude.WithTraceContext` 和
    `claude.TraceContextFromContext`；`Session.Submit` 在写入 user frame 前，
    在同一 CLI 上通过 `apply_flag_settings.settings.env` 设置
    `TRACEPARENT`/`TRACESTATE`。两个变量总是同时写入，缺省值为空字符串，以便
    清除上一条 prompt 留下的值。无 prompt trace 时，如果此前没有覆盖，不发
    control request；如果此前覆盖过，则恢复 `Open` 时从有效进程环境捕获的
    startup trace 值，没有值就写空字符串。

    SDK 的义务到原生入口为止：Codex 逐次写入 envelope carrier；Claude 在 user
    frame 前尽力应用 settings env。完整 control frame 后的拒绝、等待超时或未知
    响应只使 trace 状态未知；transport 和调用 context 仍可用时继续写 user frame，
    下一次提交重写完整目标值。control 写入阻塞时使用既有取消机制关闭 transport
    并释放 gate；部分写、短写或其他 transport 写入失败终止交互并报告既有写入
    错误和未知结果，不追加 user frame，也不返回 `Written=true`。所有
    `Submit` 写入、trace control 与 `SetEffort` 共用串行 gate，只保证本地
    apply/user frame 写入顺序；等待 control response 或调用 context 结束不是
    prompt 出队确认。SDK 不维护 pending 队列、replay 状态或 dequeue
    acknowledgment，也不因同一 Session 另有 prompt 正在执行而拒绝带 trace 的
    提交。Claude 路径不重启 CLI，也不把 ACP `_meta` 当作实现依赖；ACP 接入仍
    属于独立 adapter 能力。

    以下是 Provider 原生行为，不是 SDK 新增或保证的语义：

    - 原生 Codex 把 busy turn 上的 `turn/start` 接纳为 steer 时，不创建新的
      turn 执行 span，继续使用原 turn 的 trace。新 carrier 最多父化该次 request
      和输入接纳路径；`turn.id` 是关联字段，不是 trace retarget 或 W3C link。
      输入进入 Core 队列后不再携带 carrier，后续 turn 工作仍使用 turn 建立时的
      trace。显式 `turn/steer` 不在本合同内。
    - 原生 Claude 的 settings env 是 CLI 进程级状态，不提供 prompt/turn 隔离。
      写入后，当前 turn 的后续新工具、工具启动的子进程或随后 prompt 都可能读取
      当前值，已经启动的进程不保证更新。CLI 已排队但尚未取走的 prompt 也可能
      读取后一次写入的 env；这是明确接受的观测误差。

    SDK 不模拟原生 steer 状态机，不重定向 active turn trace，不按工具调用快照
    隔离 env，也不把 settings 成功解释为 provider span 已关联。
5. SDK span 只覆盖一次输入提交，不覆盖整个 turn。Tracer 是 best-effort；
    telemetry 失败不改变 Agent 业务结果。
6. 不增加 per-prompt `Input.Env`。Codex 没有稳定的 per-turn shell env 字段，
    Claude 原生 user message/control request 也没有稳定的 per-prompt env 入口；
    ACP 的 `session/prompt._meta` 只预留 trace/baggage，不是通用 env，社区 PR
    的运行时 settings 注入也只服务于 trace。Claude 的 trace-only settings env
    映射不能扩展成任意环境变量 API；trace carrier 不能代替环境变量。

每个 Session 通过 `shellEnv`、`processEnv`、`promptTrace` capability 宣告实际
支持面。非空 `ShellEnv` 或 `ProcessEnv` 在不支持的 Session 上必须在原生副作用
前失败，不能静默忽略。`promptTrace` 表示 SDK 是否会尝试把有效 context
carrier 送入 provider native 提交；它是 best-effort 传播能力，不保证最终
provider span 归属。为 false 时 context carrier 不返回 `unsupported`，SDK
仍可使用同一 context 创建本地 span，只是不写入 provider request。carrier
无效 carrier、完整 control 写入后的拒绝或等待超时，以及归属竞态不改变健康
transport 上的输入结果；实际 transport 写入失败或挂起遵循正常失败和取消合同。

## 外部依赖评估

不直接依赖 OpenTelemetry 或其他 tracing SDK：

- W3C `traceparent`/`tracestate` 的校验、context carrier 和 span seam 都可用
  标准库实现，不需要引入完整的 propagation、resource、exporter 和 SDK。
- 仓库只有一个 Go module；核心或 adapter 一旦导入 OpenTelemetry，整个 module
  的依赖图和版本兼容面都会扩大，即使部分调用方完全不使用 tracing。
- `context` + `Tracer` 已保留互操作点。OpenTelemetry 用户可以在调用方实现
  `Tracer`，从同一个 `context.Context` 读取 active span，创建子 span，再用
  `propagation.TraceContext` 生成 `TraceContext`；不需要 SDK 核心导入 OTel。
- 只为省掉这层 adapter 而把 OTel API 带进 `agent`、`codex`、`claude` 或原生
  `types` 不划算。若未来多个上层应用都需要相同 bridge，应在 SDK 外部复用，
  或在依赖政策变化后单独评估新增包/module。

## 后果

- 同一个 Codex Provider/app-server 可以用不同 `ShellEnv` 服务多个 thread；
  进程环境和 `CODEX_HOME` 仍由 Provider 选择。
- Codex `Resume` 对仍 loaded 或运行中的 thread 可能忽略 config override；SDK
  只把 `ShellEnv` 作为请求传递，不证明其已热更新。需要确定 shell 环境时必须
  新建 thread，而不是依赖运行中恢复。
- Claude 的 Provider `Config.Env` 基底不同时仍需要不同 Provider；同一基底上
  的 Session 差异可以由各自 `ProcessEnv` 表达，不能用统一 `ShellEnv` 冒充。
- 同一 Claude Provider 的多个 Session 可以用不同 `ProcessEnv`，因为每个
  Session 自有 CLI 进程；该覆盖会影响 CLI 及其子进程。
- Codex 的 Session 级进程环境没有独立入口；`ProcessEnv` 在 Codex Session 上
  明确不支持，进程隔离仍由 Provider 决定。
- 原生 Codex 在 steer 时不创建新的 turn 执行 span，新 carrier 只可能标记该次
  提交，active turn 继续使用原 trace；后续 turn 模型/工具工作仍属于 turn
  建立时的 trace。这是 Provider 原生行为，不是 SDK 合同；SDK 不补造或重定向
  provider span。
- Claude 可在启动时通过 `Config.Env` 或该 Session 的 `ProcessEnv` 提供固定
  trace，也可用 `WithTraceContext` 让 SDK 为每条 prompt 尝试切换 settings
  env。后写入的 env 可能作用于更早已排队但尚未取走的 prompt，也可能被当前
  turn 的后续新工具或新建子进程读取；已经启动的进程不保证更新。上述现象属于
  Provider 原生行为，不是 SDK 保证；SDK 不为避免观测误差阻塞提交，也不声称
  prompt/turn 隔离。具体 CLI 版本是否导出 provider span 仍受 telemetry 配置
  和公开回归影响。
- Context trace 可用于所有 provider 的本地 span parent；两个 provider 都会将
  有效 carrier 送入各自的原生提交路径。本地 span 可以存在，但不能因此宣告
  `promptTrace`，也不能把本地 trace 冒充为 provider 接受的 trace。
- 调用方得到一个可测试的边界：环境作用域不再依靠 provider 文档猜测；
  `promptTrace` 只表示 SDK 会尝试把有效 carrier 送入 provider 原生入口。它是
  best-effort 传播能力，不证明 provider 已消费、创建 span 或建立正确归属。

## 未采用方案

1. 用一个共享 `Env map[string]string` 同时表示所有作用域。它会隐藏 Codex
   app-server、Codex shell、Claude CLI 和 Claude 子进程之间的差异。
2. 把 Claude 进程 `Env` 映射为 `ShellEnv`。名称只承诺 shell，但实际影响整个
   CLI 及其更多子进程，会让安全与配置判断失真。
3. 对 Codex 每个 prompt 重写 shell env 或重启 thread。Codex 没有 per-turn
   shell env 入口，重启会改变执行身份并伪造已支持能力。
4. 把 context trace 写进 `TRACEPARENT` shell env。该动作会混淆 prompt trace
   与 shell 环境，且无法在不重启 thread 的情况下逐 prompt 更新。
5. 把未合并的 ACP PR 或 `session/prompt._meta` 当成当前 Claude ACP adapter
   合同。本 SDK 的 Claude 支持来自原生长连接 control 实现；ACP adapter 是否
   读取并转发 `_meta` 仍是独立事实。只在 SDK 事件或日志中补 trace 也不能
   证明 provider 真正关联。
6. 为 prompt trace 增加 replay acknowledgment、pending 队列和 waiting barrier。
   这些机制能提高 trace 归属精度，但会让纯观测信息影响输入接纳与并发，成本
   高于收益。只保留一次本地串行写入和 best-effort settings 更新。
7. 在 `Input` 上增加 `Trace` 字段。它把调用元数据变成可持久化/可回放 wire
   数据，也会绕开 Go 既有的 context 组合；context carrier 已覆盖同一场景。
8. 在 SDK 核心依赖 OpenTelemetry。它扩大 module 的生产依赖与版本兼容面；
   context + `Tracer` seam 已足以由调用方桥接。

## 相关合同

- [统一 Agent 设计](../modules/unified-agent/design.md#environment-configuration)
- [Prompt trace](../modules/unified-agent/design.md#prompt-trace)
- [原生 Session 设计](../modules/native-session/design.md#interface-进程环境与-prompt-trace)
- [共享术语](../../GLOSSARY.md)
