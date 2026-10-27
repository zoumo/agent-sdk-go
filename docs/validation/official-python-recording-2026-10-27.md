# 2026-10-27：独立官方 Python SDK 录制验证

本报告记录 [独立 Python 工具模块](../../tools/recording/README.md)的实现与有界验证，
目标来自 [共同用户故事](../../harness/sdk/user-stories.md)，证据分类遵循
[测试规则](../../harness/testing.md)。此前的 [Go 驱动 38 项报告](story-recording-2026-10-27.md)
仍独立有效；其中的 34 份协议数据不计入本次新路线结果。

## 实现与来源

- Python 工具独立负责隔离环境、官方 SDK 交互和原始 stdio 代理，不依赖仓库 Go SDK。
  没有新增 Go module 或修改 go.mod/go.sum、harness、历史发布 fixture。
- 固定 `openai-codex==0.161.0`、CLI `0.161.0`；固定
  `claude-agent-sdk==0.2.174`、CLI `2.1.199`。完整 Python 依赖版本在模块 lock 文件中。
  官方接口来源：[Codex](https://learn.chatgpt.com/docs/codex-sdk)、
  [Claude](https://code.claude.com/docs/en/agent-sdk/python)。
- 每项使用独立 /tmp HOME/config/project/tmp/raw；只继承明确选择的认证和连接配置。
  不读取已有项目或会话、不继承 hook/MCP/skills。原始协议、诊断、认证来源均不发布。
- 后期准备的数据集保存不可变 Python 驱动源码快照；前期开发验证保存原源码哈希，
  没有补写或改写它们的历史来源。原 CLI/源码身份与新的回放消费者身份分开。
- Go 的 `--story-plan`/`--story-proxy` 新增实时故事入口已退役，旧录制门不放宽。
  历史 story 回放、回答完成屏障和共享捕获/匹配能力保留。

## 新的真实证据

| Provider / story | 所选真实路径 | 结果 | 官方 Python SDK → Go mock |
|---|---|---|---|
| Codex 001 | 同一新根连续两次文本输入 | partial，2 输入/2 终态 | 完整 JSON/EOF 通过 |
| Claude 001 | 同一新会话连续两次文本输入 | partial，2 输入/2 终态 | 完整 JSON/EOF 通过 |
| Codex 004 | 精确 fixture_read/fixture_write | partial，2 原生工具请求/1 终态 | 完整 JSON/EOF 通过 |
| Claude 004 | 精确原生 Read/Write 权限回调 | partial，2 原生工具请求/1 终态 | 完整 JSON/EOF 通过 |
| Codex 011 | 原生 requestUserInput 与原句柄答案 | partial，1 原生问答/1 终态 | 完整 JSON/EOF 通过 |
| Claude 011 | 原生 AskUserQuestion 与原权限答案 | partial，1 原生问答/1 终态 | 完整 JSON/EOF 通过 |

这 6 项是新官方 SDK 路线的新数据集，共 617 帧原生双向 JSON；均通过所选凭据值扫描，
各自 transport 回执确认 owned native 回收与 cleanup 完成。认证和连接配置副本已删除。
`partial` 表示只有选定路径完成，不代表对应 user story 的全部验收分支。

本仓库 Go 公共 Session 另对两端新 001 数据完成离线消费：Go Session → 自执行 Go
mock 进程，验证两次输入/终态和完整 EOF 回执。Codex 使用原始请求选项；Claude 仅
在严格验证源 envelope 后显式添加 `initialize.forwardSubagentText=true`，并将
`session_id="default"` 转为 Go 的空流标记。派生 Claude 请求期望标为 synthetic
adaptation，服务端帧和原 raw 不变。没有忽略任意请求或字段来通过回放。

## 保留的失败与限制

- Codex 首次 001 因官方 `turn_start` 方法参数用法错误而失败，发出 0 条模型输入，
  保留 12 帧初始化证据。修正后在新目录验证，不覆盖原失败。
- Codex 首次 004 的官方生成参数模型丢弃 `dynamicTools`，模型明确返回工具不可用，
  0 工具请求。保留 52 帧及失败报告。改用官方 `JsonObject` 参数重载后，在新目录完成
  精确工具路径；原错误没有 relabel 或伪造请求。
- 两次失败不计为回放通过。本次合计 8 个已尝试项目，6 partial、2 failed。
  原始帧、CLI/driver/source 哈希及诊断均保留；不使用成功样本覆盖失败样本。
- 新路线只对 001/004/011 做了有界真实验证。其他故事的 Python 操作实现或显式 gap
  没有因此取得新路线真实验收。006/007 的递归约束未证明，不启动 native/model；
  Codex 009 不批准缺少精确 patch/path 的文件操作。
- 010 持有权限期间持续 Receive、015 的本地 Receive 取消、全部媒体输入、压力/故障、
  子 Agent 继承与完整历史查询等仍有明确缺口，不能用普通文本输出或初始化 ack 代替。
- 计时、字节、单次启动和 Claude CLI 预算不证明独立 provider 计费、内部重试硬上限。
  未做发布脱敏、远端 CI、外部 pushed-SHA consumer 或完整 19 项故事验收。

## 工程验证

9 项纯 Python 离线测试通过：配置筛选、同源认证配对、symlink 拒绝、独占账本、
原始大整数协议/进程回收、原始高精度小数导出、未知 SDK 提前拒绝、重复回放历史归档和自定义认证变量扫描。两个公共 Go Session 的新私有数据消费检查通过。
默认离线脚本包含新安全测试；官方 SDK 依赖只用于显式录制/回放，不成为默认 Go CI
或 SDK 运行时依赖。完整离线脚本（生成、inventory、依赖边界、sanitizer、全部 Go 测试与七个示例）和
`go vet ./...` 和全仓库 `go test -race -p=1 -count=1 -timeout=300s ./...` 通过；
两端新公共 Session 消费测试与新增 Go 退役入口保护也单独通过 race。
当前 Go 回放二进制另外完整重现历史 34 份协议，Claude 019 仍为
`expected-failure-reproduced`，不改变原始 failed 状态。

重复回放首次发现独占 report 文件冲突，协议对端已完整消费但协调器返回失败。
这些私有失败诊断保留；修复为将先前 replay 文件移入独立 `replays/attempt-*` 后，
最终 6 项全部严格回放通过，未重跑任何模型或修改原始录制。

私有数据索引：`/tmp/agent-sdk-python-story-index.json`。索引包括 8 次尝试，
manifest 哈希复核通过，所选认证/连接副本均不存在。原失败数据和回放失败诊断仍保留。

验证日志 SHA-256：

- 完整 offline：`e7f2ea61103e3bcce824540fb956786bc60bae7e67ab7e4dee802377eec69a33`。
- 完整 race：`178f178ff46bf9722b4f39b970c71ed140126a7f178377430a903a85337d23b4`。
- 新 Go Session 消费 race：`10364c6fa8d59075e40bd2b66395ca453506909bc553d906fcd4db129348cb8a`。
- 9 项 Python 测试：`031d0e7d31e9a377be2c0604b19e8b64922f6c35eb67c6f2e39ebc45a70c22b4`。
