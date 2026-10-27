# 确定性场景流程离线验证

设计合同：[录制与回放场景](../../harness/sdk/recording-scenarios.md)。
运行入口：[工具说明](../../tools/recording/README.md#versioned-deterministic-scenarios)。

本次只实现并验证场景编排，不启动真实 Agent、不调用模型、不产生新录制。
没有修改既有真实录制或历史来源身份。

## 实现

- `scenarios/SDK-US-*.json` 为全部 19 个目标定义语言无关场景；`scenarios/bindings/*.json` 保存 Agent 差异，JSON Schema 定义配置合同。`agent_recording/scenarios.py` 加载、校验和解释这些配置，提供规范化执行计划。
- 标准 v3 计划冻结完整场景、SHA256 和驱动源码快照；manifest 额外绑定 plan。非录制路径不复制配置或认证。
- 两种原生驱动共享录制流程控制器；原生协议、配置、句柄和答案仍由各自适配处理。进度屏障缺失时不会顺序补发第二条输入或虚构中断。
- 语义轨迹、场景哈希及分支结果参加严格回放比较。保留完整 EOF、原生 receipts、失败诊断和数字精度检查。
- `catalog` 无需官方 SDK 或配置。`offline` 使用 mise 固定版本、关闭 live flags 与依赖下载，并核对每个选定测试实际通过，防止空选择或跳过被算作通过。
- 显式单子 Agent profile 保留独立 v2 计划与适配，不授予 v3 分支通过结论。

## 实际验证

`PYTHONPATH=tools/recording python3 -m unittest discover -s tools/recording -p 'test_*.py' -v`
通过 28 项测试，包括既有安全检查及新增的两原生驱动屏障顺序、缺屏障、重复/越序请求、预算、场景/plan 篡改、回放轨迹差异、准备阶段隔离与 v2 profile 兼容检查。
原生驱动检查使用测试内的官方 SDK 接口替身，不是官方 SDK 或真实模型兼容性证明。

`python3 tools/recording/agent_recording/bootstrap.py offline`
通过两 provider 共 38 个选定公开合成验收：SDK-US-001～018 和独立历史 SDK-US-019/US-014。
使用已有 `/tmp` mise 工具缓存中的 Go 1.26.9，临时 mise 配置隔离全局 Java/Rust；
未修改全局配置或项目 mise.toml。Claude 015 通过本机 TCP 屏障同步，需允许本机 socket；
首次沙箱内执行因禁止 socket 失败，允许本机 socket 后完整通过。

`python3 scripts/check-story-evidence.py` 通过 19 目标、14 场景、12 原生补充的链接与公开测试库存检查。

## 证据边界

标准 live-selected-path 只有目录明示的选定分支。005/008/014/015/017/018/019 的
故障、存储与独立查询使用合成验收入口，不能代替真实录制。默认子 Agent、Codex
原生审批和延迟审批缺口保持可见。Claude 延迟审批必须实际观察到持有期间事件；
仅控制确认不能使该分支通过。选择题录制不证明自由文本题覆盖。

未执行新的真实录制、真实回放、单子 profile 或外部消费者发布验证。历史数据的
快照兼容由离线合同测试检查，不宣称所有私有历史数据均重新执行过回放。

旧录制协调器的 5 项 Python 测试、证据库存的 3 项 Python 测试和 mise 固定编译器下的依赖边界检查均通过。

配置解耦补充验证：只改 JSON 即可调整 prompt 并将屏障追加流程改为顺序提交；未知操作、非法引用/顺序、重复 JSON key、路径逃逸及超预算均被拒绝。保存的驱动在 checkout 外可独立加载 JSON；修改快照 JSON 会改变快照哈希。未执行新增真实录制。

另以隔离配置副本新增测试场景，验证已支持动作的新场景可只通过 JSON 接入，不增加 Python story 分支。该测试场景不加入正式 19 项目标。
