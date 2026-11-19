---
type: Specification
title: SDK 共同验收
status: stable
description: "说明 SDK 共同验收的用户目标、回放流程、原生适配边界及其与可执行验收矩阵的关系。"
tags: [acceptance, cases, simulation]
sources:
  - id: simulation-design
    resource: "../modules/agent-sdk-testify/design.md"
    title: "仿真设计与证据合同"
---

# SDK 共同验收

<a id="stories-sdk-用户故事"></a>

## SDK 用户故事

`SDK-US-NNN` 标识完整使用目标。`SDK-CASE-NNN` 标识可独立录制和回放的流程。同一流程可以包含多条断言。按场景的 `storyId` 和 `id` 查找，ID 数字不表示归属或驱动行为。场景、provider binding 和官方录制由固定版本的 Testify 提供。[Testify 场景目录](https://github.com/zoumo/agent-sdk-testify/tree/main/data/scenarios) · [仿真配置](../modules/agent-sdk-testify/design.md#story-场景配置)

<a id="stories-共同边界"></a>

### 共同边界

分别报告真实录制、官方 SDK 回放和 Go SDK 仿真。仿真验收使用原生交互材料。工具、审批、问答或子 Agent 未触发时，保留原始结果并记录未触发。协议容错、无效句柄、未知字段、背压、取消竞态和存储异常由合成对端与代码集成测试验证。[测试规范](../testing-standards.md)

## 验证 case

SDK 的门禁代码定义每个 provider 和 case 的必需性。指定 provider 和证据层的全部必需 case 通过后，才能声明故事通过。必需 case 需要合格的录制、通过的当前 Go 业务断言、完整的严格回放与清理证据。可选 case 的业务缺口仍须报告，材料损坏或清理失败仍阻断门禁。[SDK 验收矩阵](../../tests/simulation/simulate/coverage.go) · [固定录制门禁](../testing.md#run-the-fixed-recording-gate)

分别报告 `planned`、`unsupported`、`blocked`、未触发、失败与缺证据。已注册的录制路径只表明驱动能尝试该流程。Codex 持有审批的录制限制属于官方驱动。Go SDK 的独立接收与回答能力仍可用合成测试验证。[官方驱动限制](../../codex/docs/rust-client-approval.md)

## 流程语义

### 证据与通过条件

分别取证提交、原生接受、实际执行和业务目标。严格回放证明 SDK 的协议交互与录制匹配。原生拒绝只有在目标允许拒绝时才可构成通过。验收要求收紧后，用原录制重新核对所需证据，并单独记录复核结论。

配置取证区分请求值、原生确认、原生观察值与生效范围。只有确认回执时可报告已接受。需要实际生效证据的目标在缺少原生观察值时记为 `unverified`。任务文本、耗时、token 数量和模型自述无法证明配置生效。

### 输入、工具、审批与问答

- 连续输入在第一次结果后复用同一 Session，第二次输入须有对应事件和结果。两次输入之间可能没有事件，`idle` 与结果的顺序取原生观察值。执行中追加输入须先观察实际进度，再分别记录原生接受、拒绝和调度结果。
- 图片、文档和混合输入须通过原生内容类型发送。校验的答案须依赖读取所发的非文本内容。混合输入须验证声明的各内容部分。工具调用、工具结果和任务结果分别取证。应用工具须沿原生请求返回结果，再观察任务继续并完成。
- 文件与命令审批使用原始有效句柄。用户拒绝后，任务可以以无法完成结束。持有审批时，须在回答前收到独立原生事件和关联的控制回执，随后回答并观察执行终态。MCP 工具审批的子 Thread 路径还须证明请求归属到已确认的子 Thread。额外沙箱权限分别核对本轮、本会话与拒绝三个决定。[原生请求合同](../modules/native-session/design.md#interface-真实-incomingrequest) · [统一回答权限](../modules/unified-agent/design.md#交互与回答权限)
- 按原生题目标识完整回答单选、自由文本或同一次请求中的多题。独立 MCP 表单和 URL 请求各自覆盖接受、拒绝与取消。

### 子 Agent 与详细交付

- 子 Agent 输出与完成结果须关联原生子身份。嵌套须证明根到子、子到孙两条父边。并行须证明原生活动区间重叠。后台须在根执行结束后观察到已确认的子 Agent 的活动及最终结果。
- 在打开 Session 前选择交付策略，用同一材料比较 `preserve` 和 `suppress`。`suppress` 须在确认子归属后至少省略一条允许过滤的纯详情，其余事件的内容和顺序保持一致。过滤范围与保护规则以原生合同为准。[子详细交付](../modules/native-session/design.md#child-detail-可选子详细事件交付)

### 模型与思考配置

- 区分创建时选择、首次输入前切换、两次执行之间切换和执行中更新。切换使用不同的模型值，并用原生观察证据核对生效范围。执行中更新被拒绝时保留原生原因和当前执行终态。
- 思考模式、预算、effort 与展示分别取证。创建和运行时修改 effort 分阶段核对请求、回执及原生观察值。更新前的状态只作为预检。展示配置须正确透传、关联原生成功响应并完成后续执行，展示效果只作补充观察。验证子 Agent 的配置时只比较模型，未暴露子模型时保持未知。[原生输入与配置](../modules/native-session/design.md#interface-原生输入与配置)

### 中断、关闭与独立查询

- 中断须在观察到实际进度后发出，并取得原生确认及执行终态。执行先正常结束时，按竞争结果记录。关闭自有进程须确认进程回收和交互终止。远端断连须另用连接或健康检查证明同一服务仍可用。公开 WSS 部署单独取证。
- 独立查询在显式存储范围内分别验证发现、元数据和完整主内容。保存的子历史须保留父子关联和原生顺序。发现已有子 Agent 时，先结束创建与保存阶段，再打开独立观察连接。正式观察阶段零输入，通过发现接口取得已有子身份及关系。分页或切片至少读取两个原生窗口，核对顺序、边界和终点。[历史查询合同](../contracts/history-query.md)

### 上下文注入

- 后续查询须核对仅通过原生 context-only 接口注入的随机事实。空闲注入后先经过无模型调用的控制屏障，再发起查询。原始协议须证明注入期间没有额外执行或 assistant 输出。执行中注入须发生在实际执行进度之后，分别记录原生接受或拒绝、当前执行是否消费、后续查询能否读取。[统一注入合同](../modules/unified-agent/design.md#context-inject)

<a id="native-用户故事的原生适配说明"></a>

## 用户故事的原生适配说明

调用及适配规则查各自的合同。原生参数拒绝、功能未触发和缺少观察证据各自报告。[原生 Session](../modules/native-session/design.md) · [统一 Agent](../modules/unified-agent/design.md) · [历史查询](../contracts/history-query.md)
