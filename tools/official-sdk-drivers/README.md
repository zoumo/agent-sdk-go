# Agent SDK drivers

<a id="repository-recording"></a>

`codex/` and `claude/` contain the respective native official-SDK cases for the Go
[agent-sdk-testify runner](../agent-sdk-testify/README.md). Python owns case execution and business
assertions. Go owns environments, peer processes and result collection.

Install into a private Python 3.11+ environment:

```sh
python3.11 -m pip install './tools/official-sdk-drivers[recording]'
agent-sdk-testify-codex --request /absolute/request.json
```

Installed commands are `agent-sdk-testify-codex` and `agent-sdk-testify-claude`.
They execute one supplied request; Go prepares the environment and recording or
mock peer. Shared material helpers live under `common/`. The Python import remains
`record_user_story` for the existing recording-source readers. Older
preparation and schema readers remain for the existing datasets and offline
regression tests; the Go runner is the operational entry point.

SDK extras select dependencies without exact runtime-version gates.
`requirements.lock` is a known installation snapshot, not a runtime allowlist.
The driver writes `result.json` with `status` and `message`; detailed native
receipts and diagnostics remain driver-owned files in the same output directory.

For offline package and driver checks:

```sh
mise exec -- bash scripts/with-agent-sdk-testify.sh env PYTHONPATH=tools/official-sdk-drivers/common:tools/official-sdk-drivers/codex:tools/official-sdk-drivers/claude python3.11 -m unittest discover -s tools/official-sdk-drivers/tests -v
mise exec -- bash scripts/with-agent-sdk-testify.sh python3.11 -m unittest discover -s tools/official-sdk-drivers/tests/scenarios -v
```

The story/case table below describes available native flows, not successful
recordings or current Go SDK simulation coverage. Actual results belong to each
private attempt. See the [design](../../harness/modules/agent-sdk-testify/design.md).

<a id="fixtures-仿真录制与测试输入"></a>

## 仿真录制与测试输入

仿真测试的来源是实际 Agent 交互。当前仓库不附带已录制的仿真数据集，
也不以目录可用性或代码测试结果声称真实流程通过。[操作说明](#repository-recording)描述新录制与回放。

<a id="fixtures-私有仿真数据"></a>

### 私有仿真数据

私有数据按 `<story-id>/<case-id>/` 保存；文件清单见[数据合同](../../harness/modules/agent-sdk-testify/design.md#story-数据格式与存储)。

配置和认证副本按录制流程清理。原始材料不会因录制或回放通过自动公开。
数据来源、哈希与遗漏以该次 manifest 为准；发布任何新数据须单独审核。
规范见[仿真工具](../../harness/modules/agent-sdk-testify/design.md#story-官方-sdk-仿真测试)和[布局](../../harness/architecture.md#layout-code-layout-standards)。

<a id="fixtures-代码集成测试输入"></a>

### 代码集成测试输入

这些材料均为人工构造的协议，不是仿真录制，也不计入故事验收。

| 输入 | 代码归属与来源 |
|---|---|
| Codex 生命周期与故障、原生问题 | [生命周期](../../codex/tests/testdata/integration/lifecycle.jsonl)、[问题](../../codex/tests/testdata/integration/question.jsonl) |
| Claude 控制协议与任务失败 | [控制](../../claude/tests/testdata/integration/controls.jsonl)、[任务失败](../../claude/tests/testdata/integration/task-failure.jsonl) |
| 共享原生对端 | [tests/integration](../../tests/integration/peer_test.go)、[会话对端](../../tests/integration/session_peer_test.go) |
| Claude 子任务示例 | [合成事件](../../claude/examples/subagents-offline/testdata/events.ndjson) |

Go fixture 适配器读取同一份嵌入数据，协议匹配由 `tools/agent-sdk-testify/protocol/` 实现。
专项测试直接验证 SDK 公开接口；不生成按 story/case 编号组织的合成回执。

<a id="catalog-仿真测试流程目录"></a>

## 仿真测试流程目录

[仓库场景目录](../../tests/simulation/scenarios)包含 8 个真实用户故事、36 条完整流程。主规范见[用户故事](../../harness/sdk/validation-cases.md#stories-sdk-用户故事)及[case 语义](../../harness/sdk/validation-cases.md)。

`live-selected-path` 表示驱动提供该真实流程；`planned` 表示尚未实现；`unsupported` 表示已知原生能力不支持。
这些状态不代表录制或回放已通过。实际结果由每次私有数据集中的来源、报告与回放回执证明。
待实现和不支持的理由保存在 provider 绑定及 catalog 输出中；它们不执行替代流程。

| Case ID | Story ID | 完整流程 | Codex | Claude | 来源 |
|---|---|---|---|---|---|
| SDK-CASE-001 | SDK-US-001 | 连续提交文本输入并在结果后继续观察 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-001.json) |
| SDK-CASE-002 | SDK-US-001 | 提交图片并读取结果 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-001.json) |
| SDK-CASE-003 | SDK-US-001 | 提交文档并读取结果 | unsupported | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-001.json) |
| SDK-CASE-004 | SDK-US-001 | 提交混合结构化内容并读取结果 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-001.json) |
| SDK-CASE-005 | SDK-US-001 | 执行中追加输入并保留原生调度结果 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-001.json) |
| SDK-CASE-006 | SDK-US-002 | 观察文件工具的执行与任务完成 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-002.json) |
| SDK-CASE-007 | SDK-US-002 | 处理应用工具请求并等待任务完成 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-002.json) |
| SDK-CASE-008 | SDK-US-003 | 拒绝文件写入审批并观察后续结果 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-003.json) |
| SDK-CASE-009 | SDK-US-003 | 批准文件写入审批并观察后续结果 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-003.json) |
| SDK-CASE-010 | SDK-US-003 | 批准命令审批并观察后续结果 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-003.json) |
| SDK-CASE-011 | SDK-US-003 | 拒绝命令审批并观察后续结果 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-003.json) |
| SDK-CASE-012 | SDK-US-003 | 持有审批期间观察事件与控制回执 | planned | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-003.json) |
| SDK-CASE-013 | SDK-US-004 | 回答选择题并继续任务 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-004.json) |
| SDK-CASE-014 | SDK-US-004 | 提供自由文本答案并继续任务 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-004.json) |
| SDK-CASE-015 | SDK-US-004 | 回答一个请求中的多道问题 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-004.json) |
| SDK-CASE-016 | SDK-US-005 | 观察直接子 Agent 的身份、输出与结果 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-005.json) |
| SDK-CASE-017 | SDK-US-005 | 观察嵌套子 Agent 的实际父子关系 | live-selected-path | unsupported | [配置](../../tests/simulation/scenarios/SDK-US-005.json) |
| SDK-CASE-018 | SDK-US-005 | 观察并行子 Agent 的独立输出 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-005.json) |
| SDK-CASE-019 | SDK-US-005 | 根任务结束后继续观察后台子 Agent | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-005.json) |
| SDK-CASE-020 | SDK-US-005 | 选择子详细交付策略并观察实际输出 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-005.json) |
| SDK-CASE-021 | SDK-US-006 | 创建会话时选择模型并执行任务 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-006.json) |
| SDK-CASE-022 | SDK-US-006 | 首次输入前修改模型并核对确认、生效顺序与可观察配置 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-006.json) |
| SDK-CASE-023 | SDK-US-006 | 两次执行之间修改模型并继续输入 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-006.json) |
| SDK-CASE-024 | SDK-US-006 | 执行期间请求修改模型并观察结果 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-006.json) |
| SDK-CASE-025 | SDK-US-006 | 创建会话时选择思考模式 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-006.json) |
| SDK-CASE-026 | SDK-US-006 | 修改原生思考预算并执行任务 | unsupported | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-006.json) |
| SDK-CASE-027 | SDK-US-006 | 选择原生 effort 并执行任务 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-006.json) |
| SDK-CASE-028 | SDK-US-006 | 修改思考展示配置并观察输出 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-006.json) |
| SDK-CASE-029 | SDK-US-006 | 配置根会话并观察子 Agent 的实际继承情况 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-006.json) |
| SDK-CASE-030 | SDK-US-007 | 在执行进度后中断并读取确认及终态 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-007.json) |
| SDK-CASE-031 | SDK-US-007 | 关闭拥有的会话并确认交互与进程结束 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-007.json) |
| SDK-CASE-032 | SDK-US-007 | 关闭远端连接并保持远端服务可用 | live-selected-path | planned | [配置](../../tests/simulation/scenarios/SDK-US-007.json) |
| SDK-CASE-033 | SDK-US-008 | 独立发现会话、读取元数据和主会话内容 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-008.json) |
| SDK-CASE-034 | SDK-US-008 | 独立发现并读取保存的子 Agent 内容 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-008.json) |
| SDK-CASE-035 | SDK-US-008 | 发现当前 Session 观察前已有的子 Agent | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-008.json) |
| SDK-CASE-036 | SDK-US-008 | 按原生分页或切片浏览保存的记录与内容 | live-selected-path | live-selected-path | [配置](../../tests/simulation/scenarios/SDK-US-008.json) |

专项测试直接归属代码集成测试，不出现在本表。
