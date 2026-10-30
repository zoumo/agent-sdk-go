# record-user-story

Run stories through official Agent SDKs and retain case recordings and evidence.
Python owns SDK drivers, scenario preparation, orchestration and acceptance.
The separately installed Go `agent-mock` owns stdio recording and protocol mock.
Python 3.11+ is required; the import package is `record_user_story` and the only
installed command is `record-user-story`.

## Install and configure

Run shell commands from the repository root, using a private Python environment.

```sh
python3.11 -m pip install ./tools/record-user-story
record-user-story catalog --provider codex
```

The base wheel has no runtime dependencies. Install the official SDK extra needed
for capture or SDK replay: `[codex]`, `[claude]`, or `[recording]` for both.
`requirements.lock` retains the complete default SDK environment. Catalog does not
need Go, SDKs or authentication. Go mock itself does not need Python or an SDK.

Build the Go binary from the current repository root with its pinned compiler.
Keep this binary for the lifetime of the prepared dataset:

```sh
export AGENT_MOCK_BIN=/absolute/persistent/bin/agent-mock
mise exec -- go version  # go1.26.9
mise exec -- go build -o "$AGENT_MOCK_BIN" ./tools/agent-mock
"$AGENT_MOCK_BIN" version
```

There is no implicit sibling-directory discovery, automatic download, Go module
runtime dependency or Python mock fallback. `--peer /absolute/binary` explicitly
selects the executable at prepare; record/replay may relocate that exact binary
with `--peer`, but its SHA-256 and version must match the frozen identity.
Missing or changed binaries fail before executing the story.

## Portable contracts and adapters

The bundled `conversation/two-inputs` example is independent of the SDK repository.
Use `--scenarios /absolute/catalog` or `AGENT_MOCK_SCENARIOS` for another catalog.
The frozen catalog takes precedence
over the environment. `--source-revision` optionally attaches a caller revision;
the module source hash is always recorded.

The [frame schema](record_user_story/schemas/wire.schema.json) describes the
`agent-mock-jsonl` v1 envelopes: `ordinal`, `direction`, `channel`, `epoch`,
`elapsedMs` and original `wire`. Go preserves unknown fields, key order and number
lexemes; line endings may be normalized. Python delegates protocol matching and
ID state to the Go executable.

[Scenario](record_user_story/schemas/scenario.schema.json),
[index](record_user_story/schemas/index.schema.json) and
[binding](record_user_story/schemas/bindings.schema.json) schemas describe flows
without executable expressions. New SDK drivers belong here; new protocol
matching and capture transports belong to `tools/agent-mock/`.

## Offline validation

```sh
bash scripts/with-agent-mock.sh \
  env PYTHONPATH=tools/record-user-story python3.11 -m unittest discover -s tools/record-user-story/tests -v
AGENT_MOCK_BIN=/absolute/path/to/agent-mock python3.11 -m unittest discover -s tests/recording -v
python3.11 -m pip wheel --no-build-isolation --no-deps --no-index --wheel-dir /absolute/wheels ./tools/record-user-story
AGENT_MOCK_BIN=/absolute/path/to/agent-mock python3.11 scripts/check-agent-mock-package.py /absolute/wheels/record_user_story-0.1.0-py3-none-any.whl
```

The helper's temporary binary lasts only for the wrapped command; use a persistent
binary for prepare followed by later record/replay. Tests use synthetic SDK/process
substitutes and real Go subprocesses. The wheel check installs into a fresh venv
outside the checkout and runs capture/mock for both protocols without SDKs.
Build wheels from a clean source tree: remove generated `build/` and stale
`*.egg-info` left by an earlier package name before building.

CI builds `tools/agent-mock` from the current checkout and exports
`AGENT_MOCK_BIN` for every offline gate. No external tool repository, revision
variable or separate tool publication is required.

Repository design: [agent-mock](../../harness/modules/agent-mock/design.md).

## Repository recording

Commands run from the repository root. Select the SDK catalog explicitly; the
standalone default contains only conversation/two-inputs:

```sh
export AGENT_MOCK_SCENARIOS="$PWD/tests/recording/scenarios"
record-user-story catalog --provider codex --stories SDK-US-001
record-user-story prepare --provider codex --cases SDK-CASE-001
# Use the absolute directory printed by prepare:
record-user-story record --output /absolute/prepared-dataset --cases SDK-CASE-001
record-user-story replay --output /absolute/prepared-dataset --cases SDK-CASE-001
```

--stories expands a story's cases; --cases selects independent case IDs. Together
they intersect; an empty intersection fails. Story containers use v4, index v2,
provider bindings v3; only current v4 plans execute. Explicit profiles select behavior independently of IDs.
planned/unsupported routes require reasons and never execute substitute flows.

Codex `SDK-CASE-012` is deferred at low priority (`planned`, recording disabled).
The official Python SDK 0.161.0 and 0.163.0 run approval callbacks on the sole
reader thread: retaining an approval blocks delivery of control acknowledgements
and new events. Sending controls on another thread does not unblock delivery;
answering first would change this case's acceptance target. We keep the Python
driver and do not add Rust dependencies or a side observer. Re-enable this route
when the Python SDK supports deferred approval with continued response/event
delivery, then validate a fresh capture, strict replay and Go SDK simulation.
This is a recorder limitation, not an unsupported native capability. Only this
Codex case is deferred; the other `SDK-US-003` cases and Claude 012 retain their
routes. The required acceptance target and Go synthetic held-approval test remain;
the story still reports missing recording evidence rather than passing by skip.
See the [client evaluation](../../codex/docs/rust-client-approval.md).

prepare only probes CLI versions and freezes driver/catalog/configuration/peer
identities. It creates a new private directory under the real OS temp path, outside
Git, with separate HOME/config/project/tmp per case. --output must not exist.
Only record executes the real CLI/model. Go creates raw/ and retains native frames,
stderr, transport result and manifest even on failure. Python stores SDK receipts,
case outcomes and hook/child observations separately. Actual SDK/CLI versions are recorded;
installation pins are defaults, not a capture allowlist. Use a private venv with
requirements.lock or the appropriate SDK extra, and retain the frozen Go binary.

--cli selects the native executable; --source-config selects connection config
without modifying its source; --model selects the model. --timeout,
--max-protocol-bytes and --max-stderr-bytes default to 0 (disabled).
--child-case selects the explicit child adapter and requires a child profile.
New legal events and missing case evidence do not interrupt capture. API, protocol,
process, filesystem and cleanup failures still fail. Capture complete, case passed,
story accepted and replay reproduced are separate results.

The Claude interrupt driver uses a frozen `stdinEOFAction=interrupt` close policy.
After the acknowledged cancellation result and same-session idle, client EOF tells
Go record to send SIGINT to the direct native child while keeping its stdin open
and draining output. The original cancellation result remains in the recording;
the native process must actually exit 0 and be reaped. Other profiles retain the
default `close` policy. Generic Go record exposes these policies through
`--stdin-eof-action=close|interrupt`; it does not classify provider results or
accept nonzero exits. Transport receipts retain the actual EOF action and signal.

Review report.json, manifest.json and raw/transport.json, including cleanup.
replay runs the saved official-SDK workflow against Go mock, requiring the SDK extra
but no native executable or authentication. It checks source/driver/plan/peer hashes,
SDK outcomes, full consumption and client EOF, archiving old replay output.
Failed/untriggered outcomes keep their meaning. Re-record into a new prepare directory;
changed plans/drivers cannot reuse frozen sources. Publication rules are [below](#fixtures-私有仿真数据).

History drivers separate seed execution from independent discovery and reading.
Codex detaches the seed thread before list/read calls. Pagination follows opaque
native cursors and compares the collected ordered content with a complete read;
Claude checks offset/limit slices and the empty tail against saved messages.
The model-inheritance case checks observed parent and child models separately
from the requested model; it makes no claim about inherited reasoning settings.

Existing-child observation requires a verified child-history seed from the same provider:

```sh
record-user-story prepare --provider claude --cases SDK-CASE-035 \
  --history-seed /absolute/passed-034-case --output /absolute/new-dataset
```

Preparation copies only verified saved records and freezes their source hashes.
Recording resumes the parent, observes its native identity, and discovers the
already-saved children without submitting a new input. Replay uses the frozen
copy and preserves the recorded project identity as explicit `historyProject`;
that synthetic project directory must remain available. It does not rewrite a
transcript's saved cwd to fit a new directory. Neither saved content nor requested
resume identity becomes a live event. Codex uses `--provider codex --model
gpt-6.1-terra` with a fresh Codex child-history seed whose manifest hashes the
parent and child rollout files. It keeps immutable seed bytes separately from
the writable native home and rebuilds indexes without copying source SQLite
paths. The Go adapter restores `Open` with `Resume`, queries `ListSubagents`,
and checks live delivery without projecting stored turns or granting authority.

Codex remote-close records two connections to one isolated, authenticated native
WebSocket service. Closing the first client must leave that service alive and
able to answer the second connection. `remote-probe/raw/` preserves the second
capture independently; the case manifest covers both phases. Cleanup then stops
the owned service and requires exit 0. Offline replay consumes both captures and
does not launch the service.

For Go SDK acceptance, build the explicit offline adapter and supply a successful
private case with its strict official-SDK replay. The command fails on unsupported
profiles or missing evidence; it has no native fallback. Keep output separate
from the immutable recording:

```sh
mise exec -- go build -o /absolute/bin/sdk-acceptance ./tests/recording/sdk
python3.11 scripts/replay-sdk-recording.py --case /absolute/passed-case \
  --adapter /absolute/bin/sdk-acceptance --output /absolute/new-acceptance
```

The adapter supports the executable local input, tool, approval, question, child,
configuration, interrupt, close and history profiles, plus Codex remote-close.
It calls public Go SDK operations and answers original native request handles;
the Go mock retains strict matching against the unchanged recording.
Claude continuation runs independently of local-history queries. Creation options
and initialize fields are selected from the frozen source, including the official
SDK's `default` main-stream input alias and initialization-time SDK MCP requests.

To validate a private set, provide a JSON list of
`{"provider":"claude","caseId":"SDK-CASE-001","case":"/absolute/passed-case"}`
objects to `--cases-file` instead of `--case`. Each entry must have a complete real
capture, successful case and strict official-SDK replay. The command checks all
entries, keeps separate results and logs, writes `batch-result.json`, and fails if
any entry fails. It neither records new data nor launches a native Agent.
Keep this path list and its outputs outside Git alongside the private datasets.

This entry exercises public Go SDK interfaces and verifies the mock's source hash,
full consumption and client EOF. Child-detail acceptance compares preserve and
suppress delivery against the same native capture, retaining protected and mixed
events. An official-SDK replay or protocol self-replay alone is not Go SDK acceptance.

<a id="mock-go-离线协议对端"></a>

## Go 离线协议对端

设计见[录制模块](../../harness/modules/agent-mock/design.md)，录制和回放编排见[操作说明](#repository-recording)。
`tools/agent-mock/` 构建的 Go `agent-mock` 只通过 stdio 扮演指定录制的协议对端，不需要 Python、官方 SDK、认证或真实 CLI。
构建方法见[安装](#install-and-configure)。

<a id="mock-启动方式"></a>

### 启动方式

```sh
export AGENT_MOCK_BIN=/absolute/path/to/agent-mock
"$AGENT_MOCK_BIN" version
"$AGENT_MOCK_BIN" mock --provider codex --scenario /path/to/scenario.json app-server
"$AGENT_MOCK_BIN" mock --provider claude --scenario /path/to/raw.jsonl --receipt /path/to/new-receipt.json
```

provider、scenario 和 receipt 均由参数指定，不自动读取旧环境变量或回退到真实 Agent。
SDK 配置通过原有公开进程 Interface 传递 Go 命令和参数；具体示例见
[Codex 测试](../../codex/mock_cli_test.go)和[Claude 测试](../../claude/mock_cli_test.go)。

<a id="mock-直接读取原始录制"></a>

### 直接读取原始录制

统一 `raw/raw.jsonl` 可以直接输入，两端都使用录制代理的原封装：

```json
{"ordinal":1,"direction":"s2c","channel":"protocol","epoch":1,"elapsedMs":0,"wire":{"type":"notice","opaque":true}}
```

真实回放协调器先核对原始文件、计划和保存源码哈希；单独 mock 命令仅执行指定文件，
不能替代来源验证。直接读取不创建第二份数据，不导出 `go-mock-scenario.json`。
仿真 CLI 只执行当前格式的冻结计划，不提供历史计划或 Python mock fallback。

<a id="mock-版本化-scenario-封装"></a>

### 版本化 scenario 封装

手写或派生场景使用 version 1，显式声明 `provenance=synthetic` 或 `recording`。
recording 还需 `source`，并保留实际源 manifest。合成延迟、断连或修改后的客户端预期
必须标为 synthetic，不能借原样本身份冒充未修改的真实协议。

Codex 封装：

```json
{"version":1,"provenance":"synthetic","frames":[
  {"ordinal":1,"direction":"c2s","channel":"protocol","epoch":1,"wire":{"id":91,"method":"fixture/hello"}},
  {"ordinal":2,"direction":"s2c","channel":"protocol","epoch":1,"wire":{"id":91,"result":{"text":"hello"}}}
]}
```

Claude 封装：

```json
{"version":1,"provenance":"synthetic","frames":[
  {"ordinal":1,"direction":"client","channel":"protocol","data":{"type":"user","message":{"content":"hello"}}},
  {"ordinal":2,"direction":"server","channel":"protocol","data":{"type":"result","result":"hello"}}
]}
```

Codex 允许递增但不连续的 ordinal，以保留输入材料的原始序号；epoch 为 1。
Claude ordinal 从 1 连续递增。channel 为 protocol/control。未知原生字段完整参与匹配，
但未知 scenario/frame 元数据、重复 JSON 成员、尾随 JSON 和无效帧会拒绝。
JSON 数字按原始字面量比较和输出，不转成浮点数；布尔值、字符串和数字不能混淆。

`delayMs`（Codex）或 `delay_ms`（Claude）是显式合成延迟，不从 elapsedMs 隐式等待。
Codex 最后一帧可用 `disconnect=true` 且不带 wire；Claude 使用最后一帧
`direction=disconnect` 且不带 data。只有 synthetic 场景允许这些动作。

<a id="mock-请求匹配与完成"></a>

### 请求匹配与完成

完整 JSON 必须匹配，数组顺序和未知字段不能忽略。允许动态变化的仅是原生客户端
RPC/control envelope ID；响应重绑定到该次实际 ID，业务字段和嵌套 request_id 不改写。
反向请求使用保存的原 ID，拒绝错误、重复或已取消的回答。

Codex 的 `serverRequest/resolved` 只有请求 ID 与所属 thread 同时匹配才解除未回答状态。
Claude 的 `control_cancel_request` 解除对应原生请求；相同取消不能凭空解除其他请求。
数字与字符串 ID 区分，正反向 ID 命名空间分别维护。

所有帧结束后，未完成请求必须为空，随后必须取得客户端 EOF。提前关闭、额外数据、
错配请求或未完成请求都不写成功回执。只重写必要 envelope ID，其余服务端 JSON 内容按原字节输出。

<a id="mock-资源退出与测试适配"></a>

### 资源、退出与测试适配

mock 的离线限制独立于真实录制：Codex 默认总时限 30 秒、场景和流量 8 MiB；
Claude 默认 10 秒、16 MiB。均最多 10,000 帧，默认单帧 payload 必须小于 2 MiB（不含行尾）。
Codex 合成延迟上限 300 秒，Claude 30 秒。`timeoutMs` 与 `--timeout` 可选择 mock 时限，
场景 timeoutMs 最大 300,000。更短的 SDK/调用方上下文仍可提前结束进程。

| 退出码 | 含义 |
| --- | --- |
| 0 | 帧和请求完整消费并取得 EOF |
| 1 | 请求错配、提前结束、额外输入或 I/O 错误 |
| 2 | 参数、来源封装或场景结构无效 |
| 3 | 超时或取消 |
| 4 | 场景明确指定的合成断连，不是成功 EOF |

stdout 只输出协议 JSON，诊断写 stderr。回执只有在正常完整结束后写入，
包括 `peer="Go native mock"`、`fullConsumption=true`、`nativeLaunched=false`、`clientEOF=true`、provider、帧数及 `recordingSHA256`。

SDK fixture 适配器只读取数据、桥接进程并断言结果，布局见[开发规范](../../harness/architecture.md#layout-code-layout-standards)。
测试专用的回答/完成屏障走独立通道，不进入真实录制或普通 mock 模式。
离线验证命令见[上文](#offline-validation)。

<a id="fixtures-仿真录制与测试输入"></a>

## 仿真录制与测试输入

仿真测试的来源是实际 Agent 交互。当前仓库不附带已录制的仿真数据集，
也不以目录可用性或代码测试结果声称真实流程通过。[操作说明](#repository-recording)描述新录制与回放。

<a id="fixtures-私有仿真数据"></a>

### 私有仿真数据

私有数据按 `<story-id>/<case-id>/` 保存；文件清单见[数据合同](../../harness/modules/agent-mock/design.md#story-数据格式与存储)。

配置和认证副本按录制流程清理。原始材料不会因录制或回放通过自动公开。
数据来源、哈希与遗漏以该次 manifest 为准；发布任何新数据须单独审核。
规范见[仿真工具](../../harness/modules/agent-mock/design.md#story-官方-sdk-仿真测试)和[布局](../../harness/architecture.md#layout-code-layout-standards)。

<a id="fixtures-代码集成测试输入"></a>

### 代码集成测试输入

这些材料均为人工构造的协议，不是仿真录制，也不计入故事验收。

| 输入 | 代码归属与来源 |
|---|---|
| Codex 生命周期与故障、原生问题 | [生命周期](../../codex/tests/testdata/integration/lifecycle.jsonl)、[问题](../../codex/tests/testdata/integration/question.jsonl) |
| Claude 控制协议与任务失败 | [控制](../../claude/tests/testdata/integration/controls.jsonl)、[任务失败](../../claude/tests/testdata/integration/task-failure.jsonl) |
| 共享原生对端 | [tests/integration](../../tests/integration/peer_test.go)、[会话对端](../../tests/integration/session_peer_test.go) |
| Claude 子任务示例 | [合成事件](../../claude/examples/subagents-offline/testdata/events.ndjson) |

Go fixture 适配器读取同一份嵌入数据，协议匹配由 `tools/agent-mock/` 实现。
专项测试直接验证 SDK 公开接口；不生成按 story/case 编号组织的合成回执。

<a id="catalog-仿真测试流程目录"></a>

## 仿真测试流程目录

[仓库场景目录](../../tests/recording/scenarios)包含 8 个真实用户故事、36 条完整流程。主规范见[用户故事](../../harness/sdk/validation-cases.md#stories-sdk-用户故事)及[case 语义](../../harness/sdk/validation-cases.md)。

`live-selected-path` 表示驱动提供该真实流程；`planned` 表示尚未实现；`unsupported` 表示已知原生能力不支持。
这些状态不代表录制或回放已通过。实际结果由每次私有数据集中的来源、报告与回放回执证明。
待实现和不支持的理由保存在 provider 绑定及 catalog 输出中；它们不执行替代流程。

| Case ID | Story ID | 完整流程 | Codex | Claude | 来源 |
|---|---|---|---|---|---|
| SDK-CASE-001 | SDK-US-001 | 连续提交文本输入并在结果后继续观察 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-001.json) |
| SDK-CASE-002 | SDK-US-001 | 提交图片并读取结果 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-001.json) |
| SDK-CASE-003 | SDK-US-001 | 提交文档并读取结果 | unsupported | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-001.json) |
| SDK-CASE-004 | SDK-US-001 | 提交混合结构化内容并读取结果 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-001.json) |
| SDK-CASE-005 | SDK-US-001 | 执行中追加输入并保留原生调度结果 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-001.json) |
| SDK-CASE-006 | SDK-US-002 | 观察文件工具的执行与任务完成 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-002.json) |
| SDK-CASE-007 | SDK-US-002 | 处理应用工具请求并等待任务完成 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-002.json) |
| SDK-CASE-008 | SDK-US-003 | 拒绝文件写入审批并观察后续结果 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-003.json) |
| SDK-CASE-009 | SDK-US-003 | 批准文件写入审批并观察后续结果 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-003.json) |
| SDK-CASE-010 | SDK-US-003 | 批准命令审批并观察后续结果 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-003.json) |
| SDK-CASE-011 | SDK-US-003 | 拒绝命令审批并观察后续结果 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-003.json) |
| SDK-CASE-012 | SDK-US-003 | 持有审批期间观察事件与控制回执 | planned | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-003.json) |
| SDK-CASE-013 | SDK-US-004 | 回答选择题并继续任务 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-004.json) |
| SDK-CASE-014 | SDK-US-004 | 提供自由文本答案并继续任务 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-004.json) |
| SDK-CASE-015 | SDK-US-004 | 回答一个请求中的多道问题 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-004.json) |
| SDK-CASE-016 | SDK-US-005 | 观察直接子 Agent 的身份、输出与结果 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-005.json) |
| SDK-CASE-017 | SDK-US-005 | 观察嵌套子 Agent 的实际父子关系 | live-selected-path | unsupported | [配置](../../tests/recording/scenarios/SDK-US-005.json) |
| SDK-CASE-018 | SDK-US-005 | 观察并行子 Agent 的独立输出 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-005.json) |
| SDK-CASE-019 | SDK-US-005 | 根任务结束后继续观察后台子 Agent | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-005.json) |
| SDK-CASE-020 | SDK-US-005 | 选择子详细交付策略并观察实际输出 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-005.json) |
| SDK-CASE-021 | SDK-US-006 | 创建会话时选择模型并执行任务 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-006.json) |
| SDK-CASE-022 | SDK-US-006 | 首次输入前修改模型并核对确认、生效顺序与可观察配置 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-006.json) |
| SDK-CASE-023 | SDK-US-006 | 两次执行之间修改模型并继续输入 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-006.json) |
| SDK-CASE-024 | SDK-US-006 | 执行期间请求修改模型并观察结果 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-006.json) |
| SDK-CASE-025 | SDK-US-006 | 创建会话时选择思考模式 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-006.json) |
| SDK-CASE-026 | SDK-US-006 | 修改原生思考预算并执行任务 | unsupported | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-006.json) |
| SDK-CASE-027 | SDK-US-006 | 选择原生 effort 并执行任务 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-006.json) |
| SDK-CASE-028 | SDK-US-006 | 修改思考展示配置并观察输出 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-006.json) |
| SDK-CASE-029 | SDK-US-006 | 配置根会话并观察子 Agent 的实际继承情况 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-006.json) |
| SDK-CASE-030 | SDK-US-007 | 在执行进度后中断并读取确认及终态 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-007.json) |
| SDK-CASE-031 | SDK-US-007 | 关闭拥有的会话并确认交互与进程结束 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-007.json) |
| SDK-CASE-032 | SDK-US-007 | 关闭远端连接并保持远端服务可用 | live-selected-path | planned | [配置](../../tests/recording/scenarios/SDK-US-007.json) |
| SDK-CASE-033 | SDK-US-008 | 独立发现会话、读取元数据和主会话内容 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-008.json) |
| SDK-CASE-034 | SDK-US-008 | 独立发现并读取保存的子 Agent 内容 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-008.json) |
| SDK-CASE-035 | SDK-US-008 | 发现当前 Session 观察前已有的子 Agent | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-008.json) |
| SDK-CASE-036 | SDK-US-008 | 按原生分页或切片浏览保存的记录与内容 | live-selected-path | live-selected-path | [配置](../../tests/recording/scenarios/SDK-US-008.json) |

专项测试直接归属代码集成测试，不出现在本表。
