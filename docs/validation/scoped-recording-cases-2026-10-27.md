# 2026-10-27：缩小录制用例范围

本次按[场景合同](../../harness/sdk/recording-scenarios.md)缩小用例范围，
不修改共同 US 的验收要求，也不改写历史录制。

## 实现

- 19 项[场景配置](../../tools/recording/scenarios/README.md)升级为 v2。
  每个用例声明 `caseId`、具体 `scope` 和 `outOfScope`，并随计划和源码冻结。
- 新报告和 manifest 包含 `case`。用例必需证据齐全时为 `passed`；
  范围外事项不再形成通用 gap，缺少必需证据仍为 `untriggered`。
- 009 只拒绝一次固定文件写入；012 只请求模型配置；013 仅记录配置响应与
  后续输入的顺序；016 只验证执行中断，不合并关闭分支。
- 新 child profile 也冻结单独的范围。资源限制放入 `limitations`，
  子身份、授权读取和关联的根后完成记录仍是必需证据。
- 回放除原生协议完整消费外，还核对用例范围、gap、限制和分支结果。
  旧 plan 和旧驱动快照继续使用原有状态合同。

## 验证

- 35 项 Python 离线测试通过，包含范围外事项不阻止通过、必需证据缺失不能
  通过、单次拒绝审批和回放范围被篡改时拒绝通过。
- 19 项配置及两个 provider 绑定通过 Draft 2020-12 JSON Schema 校验。
- 完整 `scripts/check-offline.sh` 使用 mise 固定的 Go 1.26.9 通过，
  包含全部 Go 测试、协议生成检查、fixture 校验、依赖边界和离线示例。
- 原始 23 次录制的 manifest、原始字节和驱动快照校验通过。
  保存的 v1 驱动仍解析其原始 v1 配置，未改写旧报告。
- 用旧 Codex 001 的原始协议做派生离线评估，新范围得到 `passed`、空 gap，
  Go mock 完整消费协议；用旧 Codex 006 的原始协议评估，因缺少原生子身份
  得到 `untriggered`，同样完整消费协议，没有将缺失证据移出范围。

本次没有新增模型调用或真实录制。派生离线评估不是新录制，也不替代历史报告。
新的输入、配置或范围需重新 prepare 后才能用于后续真实录制。

本机私有派生评估目录：`/tmp/agent-sdk-scoped-cases-assessment-20261027`。
完整验证日志：`/tmp/agent-sdk-scoped-cases-offline-unsandboxed.log`。
沙箱内首次全量检查因本地回环监听被禁止而失败；同一离线检查在沙箱外通过。
