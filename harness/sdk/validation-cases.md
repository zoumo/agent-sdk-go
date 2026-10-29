---
type: Specification
title: SDK 共同验收
status: stable
---

# SDK 共同验收

<a id="stories-sdk-用户故事"></a>

## SDK 用户故事

本目录定义 8 个真实用户目标。仿真测试录制真实 Agent 交互，再通过 mock 回放验证使用流程，术语见[共享词汇表](../../GLOSSARY.md)。人工构造的错误、协议边界、安全条件与精确竞态由代码集成测试负责，不进入故事目录。

User story 表达完整使用目标，使用 `SDK-US-NNN`；验证 case 表达其中一条完整、可独立录制和回放的使用流程，使用独立的 `SDK-CASE-NNN`。不同输入类型、操作序列或原生行为可以形成不同 case；同一流程中的多条断言共同归属该 case。不能从 ID 的数字推断归属或驱动行为。

一个 story 的全部必需 case 在指定 provider 和证据层完成后，才能声明故事验收。planned、unsupported、未触发、失败与缺证据分别报告；驱动已注册或其他 case 通过不能替代它们。目录不提供旧 ID 或历史录制兼容。

| Story ID | 完整用户目标 | 必需 case |
|---|---|---|
| SDK-US-001 | 在长期会话中提交输入、追加输入并观察结果 | SDK-CASE-001～SDK-CASE-005 |
| SDK-US-002 | 观察工具执行并处理应用工具调用 | SDK-CASE-006～SDK-CASE-007 |
| SDK-US-003 | 接收、等待并批准或拒绝权限请求 | SDK-CASE-008～SDK-CASE-012 |
| SDK-US-004 | 回答 Agent 提出的选择题或文本题 | SDK-CASE-013～SDK-CASE-015 |
| SDK-US-005 | 观察直接、嵌套、并行和后台子 Agent | SDK-CASE-016～SDK-CASE-020 |
| SDK-US-006 | 设置模型与思考配置并观察生效情况 | SDK-CASE-021～SDK-CASE-029 |
| SDK-US-007 | 中断执行并关闭会话或连接 | SDK-CASE-030～SDK-CASE-032 |
| SDK-US-008 | 独立发现并读取已有会话及子 Agent 保存内容 | SDK-CASE-033～SDK-CASE-036 |

配置及 provider 绑定见[仿真录制配置](../modules/agent-mock/design.md#story-场景配置)。

<a id="stories-共同边界"></a>

### 共同边界

仿真测试必须使用原生真实交互证据。未触发的工具、审批、问答或子 Agent 行为保持未触发，不用伪造事件或循环重试凑齐通过。审批拒绝和主动中断是实际用户操作，可以成为仿真流程。

共同能力遵循[SDK 架构](architecture.md)，事件接收与排空遵循[事件交付合同](../modules/native-session/design.md#delivery-event-delivery)，独立查询遵循[查询合同](../contracts/history-query.md)。原生差异见[适配说明](#native-用户故事的原生适配说明)。这些合同中的专项测试不因此成为新的 story 或 case。

真实录制、官方 SDK 对 mock 的回放、Go SDK 验收分别报告，不能跨证据层抵扣。默认质量检查离线运行；测试工具所用的合成进程不是仿真录制证据。

## 验证 case

以下 36 条完整流程都是所属故事的必需验收目标，待实现或原生不支持保留显式状态。

录制目录中的“可执行”只表示驱动提供该路径，不表示已经录制、通过或每次必然触发。原生能力还受实际 CLI/SDK 版本影响。录制冻结版本、配置、原生数据及流程结果；回放只能断言捕获材料实际支持的行为。

| Case ID | Story ID | 完整流程 |
|---|---|---|
| SDK-CASE-001 | SDK-US-001 | 连续提交文本输入并在结果后继续观察 |
| SDK-CASE-002 | SDK-US-001 | 提交图片并读取结果 |
| SDK-CASE-003 | SDK-US-001 | 提交文档并读取结果 |
| SDK-CASE-004 | SDK-US-001 | 提交混合结构化内容并读取结果 |
| SDK-CASE-005 | SDK-US-001 | 执行中追加输入并保留原生调度结果 |
| SDK-CASE-006 | SDK-US-002 | 观察文件工具的执行与任务完成 |
| SDK-CASE-007 | SDK-US-002 | 处理应用工具请求并等待任务完成 |
| SDK-CASE-008 | SDK-US-003 | 拒绝文件写入审批并观察后续结果 |
| SDK-CASE-009 | SDK-US-003 | 批准文件写入审批并观察后续结果 |
| SDK-CASE-010 | SDK-US-003 | 批准命令审批并观察后续结果 |
| SDK-CASE-011 | SDK-US-003 | 拒绝命令审批并观察后续结果 |
| SDK-CASE-012 | SDK-US-003 | 持有审批期间观察事件与控制回执 |
| SDK-CASE-013 | SDK-US-004 | 回答选择题并继续任务 |
| SDK-CASE-014 | SDK-US-004 | 提供自由文本答案并继续任务 |
| SDK-CASE-015 | SDK-US-004 | 回答一个请求中的多道问题 |
| SDK-CASE-016 | SDK-US-005 | 观察直接子 Agent 的身份、输出与结果 |
| SDK-CASE-017 | SDK-US-005 | 观察嵌套子 Agent 的实际父子关系 |
| SDK-CASE-018 | SDK-US-005 | 观察并行子 Agent 的独立输出 |
| SDK-CASE-019 | SDK-US-005 | 根任务结束后继续观察后台子 Agent |
| SDK-CASE-020 | SDK-US-005 | 选择子详细交付策略并观察实际输出 |
| SDK-CASE-021 | SDK-US-006 | 创建会话时选择模型并执行任务 |
| SDK-CASE-022 | SDK-US-006 | 首次输入前修改模型并核对确认、生效顺序与可观察配置 |
| SDK-CASE-023 | SDK-US-006 | 两次执行之间修改模型并继续输入 |
| SDK-CASE-024 | SDK-US-006 | 执行期间请求修改模型并观察结果 |
| SDK-CASE-025 | SDK-US-006 | 创建会话时选择思考模式 |
| SDK-CASE-026 | SDK-US-006 | 修改原生思考预算并执行任务 |
| SDK-CASE-027 | SDK-US-006 | 选择原生 effort 并执行任务 |
| SDK-CASE-028 | SDK-US-006 | 修改思考展示配置并观察输出 |
| SDK-CASE-029 | SDK-US-006 | 配置根会话并观察子 Agent 的实际继承情况 |
| SDK-CASE-030 | SDK-US-007 | 在执行进度后中断并读取确认及终态 |
| SDK-CASE-031 | SDK-US-007 | 关闭拥有的会话并确认交互与进程结束 |
| SDK-CASE-032 | SDK-US-007 | 关闭远端连接并保持远端服务可用 |
| SDK-CASE-033 | SDK-US-008 | 独立发现会话、读取元数据和主会话内容 |
| SDK-CASE-034 | SDK-US-008 | 独立发现并读取保存的子 Agent 内容 |
| SDK-CASE-035 | SDK-US-008 | 发现当前 Session 观察前已有的子 Agent |
| SDK-CASE-036 | SDK-US-008 | 按原生分页或切片浏览保存的记录与内容 |

## 流程语义

- SDK-CASE-001：同一会话连续完成两次文本输入。区分提交结果与执行结果，第一次结果后保持观察入口可用，再提交第二次输入并继续接收其事件和结果；第一次结果不能关闭会话，不要求两次输入之间产生新事件，也不要求 idle 晚于执行结果。SDK-CASE-002～004 分别使用图片、文档和混合输入，必须发送真实原生结构化内容并得到执行结果。
- SDK-CASE-005：收到实际执行进度后补充一次输入，保留原生接受、拒绝或调度结果，不自动重发。补充输入不凭空增加统一执行边界。
- SDK-CASE-006～007：完成实际工具任务。普通工具需保留工具事件和最终执行结果；应用工具需收到实际原生调用，按原请求返回结果，再观察任务完成。工具结束不是任务结束。
- SDK-CASE-008～011：分别拒绝文件、批准文件、批准命令、拒绝命令。必须实际收到对应审批并沿原始句柄回答，然后观察决策后续行为与执行结果。SDK-CASE-012 另需实际持有审批、继续观察或控制，再回答并完成；未发生审批不算通过。
- SDK-CASE-013～015：实际收到单选、自由文本或多题请求，按原生题目标识回答并观察后续执行；不能用提示词文本代替原生问答。
- SDK-CASE-016～019：分别发起直接、嵌套、并行和后台委派，观察原生子身份、已证实的父边、输出和完成。后台流程必须覆盖根结果之后的子活动；不能从根结果推断全部子任务停止。SDK-CASE-020 覆盖用户选择子详细交付策略并观察实际输出的流程。
- SDK-CASE-021～028：按各 case 时机设置模型或思考参数，执行任务并观察结果。配置请求、确认与实际 observed 配置分别记录；未知生效范围保留未知。SDK-CASE-022 合并首次输入前配置请求、回执、调用顺序与生效范围断言。SDK-CASE-029 覆盖设置根配置、委派子任务并观察父子配置继承。
- SDK-CASE-030：观察真实执行进度，发送中断，再读取原生确认与终态。SDK-CASE-031：完成输入后显式关闭本地拥有的会话与进程，观察关闭结果及后续交互拒绝，不能仅以普通输入结果判定关闭通过。SDK-CASE-032 覆盖关闭远端连接后远端服务仍可用的流程，不能用本地进程关闭替代。
- SDK-CASE-033：先产生保存记录，结束交互后通过官方查询实际执行发现 → 元数据 → 主内容读取。SDK-CASE-034 需产生并保存真实子 Agent，独立列举、选择子身份并读取其内容。已知身份的单次读取或普通提示词结果不能替代独立发现。SDK-CASE-035 覆盖发现观察前已有子 Agent；SDK-CASE-036 覆盖按原生分页/切片浏览多个保存记录或内容片段。

## 待实现与不支持

配置中的 `planned` 表示真实目标尚无完整录制路径；`unsupported` 表示已知原生能力不支持。两者都不发送替代提示词，不调用代码专项测试，不生成成功录制或回放证据。

每个 provider 的适用能力和当前驱动状态由录制目录显式声明。查询、配置和子 Agent 能力必须依据原生接口及实际执行证据，不从普通任务结果推断。

协议容错、审批句柄失效、未知字段、背压、取消竞态、安全与存储损坏等专项行为仍遵循 SDK 合同，由代码集成测试覆盖；它们不增加仿真 case 或填补实录证据缺口。

<a id="native-用户故事的原生适配说明"></a>

## 用户故事的原生适配说明

下表只补充原生差异，SDK-US 列省略共同前缀。

原生不支持、条件未触发、尚未实现和缺证据分别报告。共同目录中存在目标不代表每个原生版本支持它；原生参数或接入范围的拒绝须明确记录。详细边界见[Session Interface](../modules/native-session/design.md#interface-原生-session-公开-interface)、[历史查询](../contracts/history-query.md)、[子详细交付](../modules/native-session/design.md#child-detail-可选子详细事件交付)和[子历史](../contracts/history-query.md#children-claude-独立本地子历史)。

| 原生适配主题 | 对应 SDK-US | 原生协议与参数说明 |
|---|---|---|
| Codex 请求与审批 | 002、003、004 | item/tool/call 的工具结果不算 command/file/permissions 审批；审批保留 decision 及 scope/policy/network 扩展；requestUserInput 按 question ID 回答。只有原始有效请求有回答权；协议 Reject 不冒充用户 Deny。 |
| Codex 输入与配置 | 001、006 | turn/start 与 steer/排队操作分开；执行期 start 的接受或拒绝保留，不自行 steer/重试。创建 model/config 与动态 thread settings 分开；experimental 拒绝、空 ack 与后续 observed settings 分开，不推断当前 turn 或子任务生效。 |
| Codex 查询与恢复 | 008 | list/read/turns/items 保留 typed optional、unknown、opaque cursor、summary/full、有效空和原生不支持；local app-server 与 remote server storage 范围不同。元数据查询不恢复或订阅，显式 resume 不能伪装成纯查询；保存 items 不是完整事件日志。 |
| Codex 连接与资源 | 007 | stdio 只终止自有进程；WS/WSS Close 不停止远端服务。保留 TLS、Origin、认证及错误隐私边界，不自动重连或恢复旧句柄。慢消费、失联、排空与清理分别可观察，公开 WSS 的部署验证须单独提供证据。 |
| Codex 子详细交付 | 005 | SessionOptions.ChildDetailPolicy 默认 preserve，suppress 仅封闭方法白名单；低层 ResumeThread 不接受该策略。保留 Raw、父工具结果、审批原权限、关系及 terminal，unknown/mixed 整帧保留。 |
| Claude 输入与回执 | 001、002 | stream-json 完整本地写入不表示模型接受/执行；priority/shouldQuery 保留原生含义，不添加 scheduler、steer 或通用 Turn。control_response 按原 request_id 关联；取消写入的 NotSent/OutcomeUnknown 不互换或重发。 |
| Claude 权限与问答 | 003、004 | 仅实际 can_use_tool 创建权限；AskUserQuestion 使用完整题文键，通过 UpdatedInput 保留 questions 和 unknown；普通 tool_use、tool_result、输入与历史均不授予回答权。自动批准路径不保证发请求，延迟时可继续 Receive，撤销/终止/已答后不再授予权限。 |
| Claude 结果与后台状态 | 002、005 | 检查 result.is_error 和原生错误字段，不能只看 subtype:success；后续 idle、initialize 接受和请求转发选项均不证明模型或工具成功；root result 不证明子任务已停止。 |
| Claude 思考配置 | 006 | 创建 thinking enabled/adaptive/disabled 与动态 max_thinking_tokens/display 分开；省略、null-clear 和 0 分开。effort 不映射成 budget，ack 不作 observed 或子继承证明，原生 deprecated budget 不保证硬 token 上限。 |
| Claude 主历史与发现 | 008 | 独立查询要求 ConfigDir，不隐式读取 HOME；metadata 的 head/tail 采样与完整可见消息链分开，保留压缩及并行 tool-result 恢复规则与 unknown。offset/limit 是内容切片，不伪造 cursor、total、原子快照或 running；Session.ListSubagents 的本地子记录发现不等于独立子历史。 |
| Claude 子详细交付 | 005 | Options/SessionOptions.ChildDetailPolicy 包括显式 ResumeID，initialize 仍 forwardSubagentText:true；仅已确认 task/tool alias 的纯详细形状可过滤，mixed/unknown/system/结果/权限保留。 |
| Claude 独立子历史 | 008 | 显式 ConfigDir、父 UUID 与安全非 UUID agentID，独立 list/get；nested 布局、sidecar unknown、子 parentUuid 链、可见切片、资源/一致性/错误及 stricter-than-upstream 边界均明确。不用主链恢复，不建立 live 权限。 |
