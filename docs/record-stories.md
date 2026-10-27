# 按 user story 录制和回放

新的真实录制入口是仓库中的独立 Python 工具模块
[`tools/recording`](../tools/recording/README.md)。它使用锁版的官方 Python SDK
驱动两个原生 CLI；准备、SDK 交互和双向原始 stdio 捕获都不调用本仓库 Go SDK。
数据分别导出为现有 Codex/Claude Go mock 的原生 scenario，最终协议对端是 Go 进程。
本仓库 Go 公共 Session 另有真实新数据的离线消费检查。

目标来自 [19 项共同用户故事](../harness/sdk/user-stories.md)，依赖边界与证据规则遵循
[架构](../harness/architecture.md)、[测试规则](../harness/testing.md)和
[布局](../harness/code-layout.md)。这里记录实现入口和实际证据，不改变 harness 规范。
一次样本只证明实际发生的选定路径，不代表整个 user story 验收通过。

## 使用

Python 3.11 以上；在 Git 外安装锁定依赖。完整命令、目录、限制和可选继承名单见
[模块说明](../tools/recording/README.md)。以下命令的 `record` 会调用真实模型，
默认测试及 CI 不运行它。

```sh
python3 -m venv /tmp/agent-recording-venv
/tmp/agent-recording-venv/bin/pip install -r tools/recording/requirements.lock
/tmp/agent-recording-venv/bin/python tools/recording/agent_recording/bootstrap.py prepare \
  --provider codex --output /tmp/new-codex-stories --stories SDK-US-001
/tmp/agent-recording-venv/bin/python tools/recording/agent_recording/bootstrap.py record \
  --output /tmp/new-codex-stories
mise exec -- go build -o /tmp/codex-mock ./codex/cmd/mock
/tmp/agent-recording-venv/bin/python tools/recording/agent_recording/bootstrap.py replay \
  --output /tmp/new-codex-stories --peer /tmp/codex-mock
```

Claude 选择 `--provider claude`，回放使用 `./claude/cmd/mock`。不混用两个原生协议。
每个可录制 SDK-US 独立 HOME/config/project/tmp/raw；项目只含新建的合成文件。
`prepare` 只查询版本、筛选明确配置、复制必需认证并保存来源哈希，不调用模型。
CLI 与官方 SDK 的审核版本严格固定；未知版本拒绝启动。

`record` 为每项独占创建账本，最多两条固定输入；活动上限 110 秒、传输上限
120 秒、双向协议总量 2 MiB、stderr 256 KiB。失败停止批次，不自动重试，原失败不覆盖。
CLI 报告的美元预算不是独立 provider 请求、内部重试或计费硬上限。
完成后扫描所选凭据值，删除本次复制的认证与连接配置，保留私有原始证据。
数据不自动发布、脱敏或加入 Git。

新准备的数据集保存不可变 Python 源码快照；运行和回放使用该快照。manifest 保留原始
CLI、官方 SDK、源码/二进制身份及原始文件哈希。回放核对文件哈希，使用无认证环境，
无 native/model fallback，要求完整 JSON 匹配和完整 EOF。只有原生请求 envelope ID
可以按现有 Go matcher 的严格关联规则重绑定，不忽略任意字段或额外请求。

## 当前证据

新官方 Python 路线与前阶段 Go 驱动路线分别记载于
[官方 SDK 验证报告](validation/official-python-recording-2026-10-27.md)和
[历史逐 story 报告](validation/story-recording-2026-10-27.md)。后者的 34 份原生数据
不能算作新 Python 驱动的取证。两者均未达到完整 19 项共同验收。

新准备的用例按冻结范围判定：必需证据齐全时记为 `passed`，范围外事项写入
`case.outOfScope`，不计为本用例 gap。历史 `partial` 数据及源码快照保持原样。
默认 006/007 的递归子任务
资源和工具控制尚未证明，不启动 native/model。Codex 009 不批准缺少精确 patch/path
的原生文件审批。未知 hook/child 输出终止；这个防御不证明管理员 hook 在事件出现前
从未运行。010 的持有权限期间消费其他事件、015 的本地 Receive 取消等 Go 专属合同
没有被官方驱动的普通文本调用替代证明。

## 历史 Go 路线兼容

`codex/cmd/record`、`claude/cmd/record` 的旧 S1/S2 录制门保持原状。
它们的 `--story-plan`、`--story-proxy` 实时故事入口现已退役，返回明确错误；
保留 `--story-template` 及历史 `--story-replay`/`--story-peer`、Go SDK 操作和
私有回答完成屏障，继续消费前阶段真实录制。

旧协调器 `scripts/record-stories.py` 只允许 `replay`，不再准备或启动 Go 驱动录制：

```sh
python3 scripts/record-stories.py replay --output /tmp/old-private-suite \
  --driver /tmp/current-provider-record-binary
```

历史 plan 仍使用原始绝对路径和原 driver/source 哈希。指定新回放二进制不改写它们。
历史失败只有在同一私有诊断、输入/结果计数和完整协议消费都匹配时，才记作
`expected-failure-reproduced`；原 `failed` 不变。旧录制、来源、CLI pin 与发布 fixture
不迁移、不覆盖，不将新身份套到历史记录上。

## 确定性场景入口

设计见[录制与回放场景](../harness/sdk/recording-scenarios.md)，实现与命令见
[场景使用说明](../tools/recording/README.md#versioned-deterministic-scenarios)。
`catalog` 无需官方 SDK 或用户配置，即可查看两个 provider 的全部 19 项场景定义；
`offline` 运行选定的合成公开验收，并逐项检查实际通过的测试名称。

新计划采用 v3，冻结场景、prompt、初始文件和驱动源码，并绑定 plan 与场景哈希。
事件屏障触发追加输入或中断；缺屏障时不补发。报告区分选定分支通过、未触发、
仅离线和受阻。真实选定分支通过时用例记为 passed，只证明该用例范围，不宣称整个 story 通过。
回放额外比较场景哈希、语义轨迹和分支结果。历史 v2 数据继续使用原快照。

005/008/014/015/017/018/019 的新计划走明确离线入口，不再用普通文本生成充当
相应故障、子发现或独立历史分支的证据。默认 006/007、Codex 009 保留受阻原因；
Codex 010 使用离线入口。具体原生差异和可执行范围以 catalog 为准。

并行新增的显式 `--child-case` 保留专用 v2 计划与原生驱动，不纳入 v3 分支通过声明；
默认 006/007 的受阻状态不被该独立 profile 的存在替代。新 child 计划冻结单独的
case 范围，必须取得原生子身份与读取证据，或根结束后的关联完成记录；缺少证据记为
untriggered。历史 child 报告仍按原快照解释。

通用场景和 Agent 绑定已拆为[语言无关 JSON 配置](../tools/recording/scenarios/README.md)。
Python 只负责加载、校验和解释；配置、schema 和源码一同快照并校验哈希。

## 一个用例只验证一个场景

场景配置 v2 为每个 US 的当前用例声明 `caseId`、具体 `scope` 和 `outOfScope`。
这些声明与输入、步骤一起快照并绑定哈希。US 的完整验收条件不删除；本次录制的
状态只描述选定用例。其他分支按范围外事项管理，不再混入运行时 gap。

009 缩成一次固定写入拒绝；012 只请求模型配置；016 只验收执行中断。
子身份、请求关联、实际读取、进度屏障和终态仍是相应用例的必需证据，不能排除后
宣称通过。`catalog` 可直接查看两端全部用例范围，不需要模型调用。
