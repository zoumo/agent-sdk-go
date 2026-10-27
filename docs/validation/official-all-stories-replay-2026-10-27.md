# 2026-10-27：全部故事的新流程回放矩阵

本报告对应用户明确要求“用新的流程把全部 user story 回放一下”。新流程指官方
Python SDK 驱动真实交互、保存双向原生 JSON，再由官方 Python SDK 对 Go mock 进程
逐项严格回放。不是把此前 Go 驱动的 34 份数据改成新来源，也不是完整能力验收声明。
相关实现入口见 [Python 模块](../../tools/recording/README.md)，共同目标见
[19 项故事](../../harness/sdk/user-stories.md)，证据分类见[测试规则](../../harness/testing.md)。

## 最终结果

最终选定两端各 SDK-US-001–019，共 **38 份有真实原生协议的数据集**：
**36 项严格回放 passed、2 项原失败精确重现**，全部消费完整 EOF，并匹配原官方 SDK
计数、回执与实际适用的 case/flow 标识。录制状态为 35 partial、2 failed、1 blocked；
这个 blocked 是 Codex 009，仅初始化材料可回放，审批仍未执行。

后来明确授权的四个单子固定读取 case 已替换最终选定矩阵中的旧 006/007 阻塞项。
原四份 blocked 记录完整保留，不改身份；两端 006 实际观察到单子读取，但不覆盖全部
嵌套/并行能力。两端 007 的“根先结束、子随后完成”完整分支仍未证明，不能记成该
用户故事验收通过。没有为强行触发顺序重跑模型或扩大任务。

## 主批次结果

两端各 19 项全部处理。复用上一轮新路线 001/004/011 共 6 项；其余 32 项按独立
HOME、config、合成 project、tmp、raw 和不可变 Python 源码快照准备。每项最多一次
native launch、两条固定输入、110 秒活动预算与既有字节上限；失败不重试，后续只处理
不同的未执行故事。没有访问用户已有项目、继承未列出的配置，或把 SDK 对象重序列化
冒充原生 wire。临时认证/连接副本均已删除。

- 38 项录制状态：31 partial、5 blocked、2 failed。
- 34 项有原生协议：32 严格回放 passed、2 expected-failure-reproduced；全部完整消费 EOF。
- 4 项主批次无协议：两端 006/007，明确不可回放，不计为通过；用户随后授权的新子 case
  已使用另外的 source/目录完成有限录制和回放，原 blocked 不改写。
- Codex 009 的 replay passed 只证明初始化协议重现，原录制状态仍为 blocked，不是审批通过。

回放先验证 capture/driver/保存源 hash，再启动选定的 Go mock，不运行 native CLI、
读取认证或回落到真实模型。除原生 RPC/control envelope ID 关联外，完整 JSON 匹配。
此外匹配原 SDK 的 status、inputs、results、requests 与 native receipts；失败重现还要求
相同私有诊断。额外请求、未消费 EOF、改动回执或失败诊断均不计为通过。
`partial` 只表示本次选定分支的真实材料，不代表共同用户故事验收全部完成。

## 最终选定 38 项逐项矩阵

`P` = partial，`B` = blocked，`F` = failed；`通过` = SDK→Go mock 严格样本重现。

| Provider | Story | 录制 | Go 回放 | 实际范围或未完成原因 |
|---|---|---|---|---|
| codex | SDK-US-001 | P | 通过 | 同一身份连续两次输入；重复所有权等分支仍有独立离线合同。 |
| codex | SDK-US-002 | P | 通过 | 原生文本内容数组/结构；媒体组合未完整覆盖。 |
| codex | SDK-US-003 | P | 通过 | 执行进度期间原生补充输入，接受/拒绝回执保留；没有替换成 steer。 |
| codex | SDK-US-004 | P | 通过 | 工具结果与执行终态分开，固定合成读写路径。 |
| codex | SDK-US-005 | P | 通过 | 保留真实原生 JSON；未知/重复主动注入仍为合成测试。 |
| codex | SDK-US-006 | P | 通过 | 新单子 case：真实创建一个固定 reader 并读取唯一合成文件；嵌套/并行范围未覆盖。 |
| codex | SDK-US-007 | P | 通过 | 根终态后子读取成功，查询仍 inProgress；缺少子随后完成证据。 |
| codex | SDK-US-008 | F | 原失败精确重现 | 没有既有子记录正例；Codex 临时 thread 不支持 includeTurns，失败精确重现。 |
| codex | SDK-US-009 | B | 通过 | Claude 精确允许/拒绝 Write；Codex 仅初始化，未授予无法精确限定的审批。 |
| codex | SDK-US-010 | P | 通过 | Claude 原 Read 权限持有期间控制 ack；额外事件分支未完整证明。Codex 问答样本不冒充延迟审批。 |
| codex | SDK-US-011 | P | 通过 | 原生问答与原始句柄答案；无普通输入替代回答。 |
| codex | SDK-US-012 | P | 通过 | 原生配置控制/后续输入；ack 不冒充所有生效时机。 |
| codex | SDK-US-013 | P | 通过 | 原生可用配置回执与连续结果；子继承/全部思考控制仍缺证据。 |
| codex | SDK-US-014 | P | 通过 | 消费事件之前执行原生控制；不宣称无界压力覆盖。 |
| codex | SDK-US-015 | P | 通过 | 官方 SDK 无 Go Receive 取消等价操作，文本样本不能证明本地取消合同。 |
| codex | SDK-US-016 | P | 通过 | 独立 interrupt 和 Close；Claude 中断后 is_error=true 原 failed 保留。 |
| codex | SDK-US-017 | P | 通过 | 正常结果/关闭样本；故障排空仍需合成检查。 |
| codex | SDK-US-018 | P | 通过 | 关闭后调用拒绝；不冒充意外失联、远端停止或遗漏补齐证明。 |
| codex | SDK-US-019 | P | 通过 | 仅新建合成源查询；Claude 关闭后独立 local SDK 查询，Codex 仍使用现有 app-server。 |
| claude | SDK-US-001 | P | 通过 | 同一身份连续两次输入；重复所有权等分支仍有独立离线合同。 |
| claude | SDK-US-002 | P | 通过 | 原生文本内容数组/结构；媒体组合未完整覆盖。 |
| claude | SDK-US-003 | P | 通过 | 执行进度期间原生补充输入，接受/拒绝回执保留；没有替换成 steer。 |
| claude | SDK-US-004 | P | 通过 | 工具结果与执行终态分开，固定合成读写路径。 |
| claude | SDK-US-005 | P | 通过 | 保留真实原生 JSON；未知/重复主动注入仍为合成测试。 |
| claude | SDK-US-006 | P | 通过 | 新单子 case：真实创建一个固定 reader 并读取唯一合成文件；嵌套/并行范围未覆盖。 |
| claude | SDK-US-007 | P | 通过 | 原生子完成早于根终态；根先结束的后台顺序分支未触发。 |
| claude | SDK-US-008 | P | 通过 | 没有既有子记录正例；Codex 临时 thread 不支持 includeTurns，失败精确重现。 |
| claude | SDK-US-009 | P | 通过 | Claude 精确允许/拒绝 Write；Codex 仅初始化，未授予无法精确限定的审批。 |
| claude | SDK-US-010 | P | 通过 | Claude 原 Read 权限持有期间控制 ack；额外事件分支未完整证明。Codex 问答样本不冒充延迟审批。 |
| claude | SDK-US-011 | P | 通过 | 原生问答与原始句柄答案；无普通输入替代回答。 |
| claude | SDK-US-012 | P | 通过 | 原生配置控制/后续输入；ack 不冒充所有生效时机。 |
| claude | SDK-US-013 | P | 通过 | 原生可用配置回执与连续结果；子继承/全部思考控制仍缺证据。 |
| claude | SDK-US-014 | P | 通过 | 消费事件之前执行原生控制；不宣称无界压力覆盖。 |
| claude | SDK-US-015 | P | 通过 | 官方 SDK 无 Go Receive 取消等价操作，文本样本不能证明本地取消合同。 |
| claude | SDK-US-016 | F | 原失败精确重现 | 独立 interrupt 和 Close；Claude 中断后 is_error=true 原 failed 保留。 |
| claude | SDK-US-017 | P | 通过 | 正常结果/关闭样本；故障排空仍需合成检查。 |
| claude | SDK-US-018 | P | 通过 | 关闭后调用拒绝；不冒充意外失联、远端停止或遗漏补齐证明。 |
| claude | SDK-US-019 | P | 通过 | 仅新建合成源查询；Claude 关闭后独立 local SDK 查询，Codex 仍使用现有 app-server。 |

## Go 公共 Session 消费范围

最终 38 项是“官方 Python SDK → Go mock”验证；最终协议对端均为本仓库原生 Go
mock。另有两端新 001 数据通过本仓库 Go 公共 Session → Go mock 测试及 race，包含
两次提交/终态与完整 EOF。没有把这两项公共 Session 验证推广成其余 36 项公共 Go
Session 的新数据消费通过。

Codex 公共 Session 使用录制原生参数；Claude 只在严格核对源 envelope 后显式增加
初始化 `forwardSubagentText` 能力及转换固定 stream marker，派生请求期望标为
synthetic adaptation，服务端原帧和原始来源不改写。

## 失败和实现限制

Codex 008 是原生 JSON-RPC `-32600`：ephemeral threads do not support includeTurns，
在 0 条模型输入后失败。Claude 016 在 interrupt 后收到原生 `is_error=true`，保留为
failed。两者均以相同 SDK 计数、回执、私有诊断及完整 Go EOF 精确重现，不改写原失败。
前一轮开发过程的两次 Codex 失败也保留，未作为本矩阵选定样本重录或覆盖。

010/015 等官方 SDK 不具备 Go 公开接口的全部控制语义；普通文本、创建参数或关闭回执
不能替代这些分支。006 的单子读取也不能证明嵌套/并行全范围；007 必须实际看到根先
结束、原生后台子后完成，前台子返回不算。

共享工作区在本批次后出现并发新增的 version3 recipe/Flow 实现。为避免覆盖未知归属
改动，新授权 child case 的兼容验证在独立 /tmp 副本进行，保留真实覆盖源码 hash、
上游 HEAD 与显式新 case 校验。共享文件的集成不包含在已完成主批次的来源中。

主批次原始索引：`/tmp/agent-sdk-python-all-stories-index.json`。最终选定索引：
`/tmp/agent-sdk-python-all-stories-final-index.json`。索引包括每项 root、
CLI/官方 SDK 版本、原源码 hash、manifest/raw hash、SDK 计数、清理和凭据扫描结果、
Go 回放状态及限制。最终 38 份 raw hash、完整 EOF、SDK 回执、cleanup 与凭据扫描均已复核。没有提交、推送、发布原始材料或执行远端写入。

## 单子 case 的实际证据和限制

两个 provider 各 006/007 的新 case 均使用独立 /tmp HOME、唯一
`project/child-fixture.txt`，内容为固定合成 token，没有用户项目材料。
CLI/官方 SDK 版本沿用受审版本，实际 overlay 源码 hash、显式 child recipe hash、
新 manifest/raw hash 与原上游 HEAD 分开记录。原始采集和原 SDK report 不改写；
额外的 `native-evidence-v1.json` 只从 hash 关联的原始帧提取已观察事实。

| Case | 原生事实 | 后台验收限制 |
|---|---|---|
| Codex 006 | `subAgentActivity` 标明一个 fixture_reader thread；该 thread 的 fixture_read 成功；原生 child completed 可见 | 没有声称嵌套/并行完整覆盖 |
| Claude 006 | 一次原 Agent 权限和带 native agent_id 的固定 Read；local_agent task 与 completed 通知可见 | 没有声称嵌套/并行完整覆盖 |
| Codex 007 | 根完成 ordinal 38，子固定工具完成 43，query 48 中子仍 inProgress | 证明根结束后子仍可执行读取；没有观察到后续子终态，不能称后台完成通过 |
| Claude 007 | 子 completed 在 ordinal 121/122，根 result 在 131 | 实际子先完成；要求的根先结束顺序未触发 |

Codex 006 的原 helper 只检查 `collab.receiverThreadIds`，漏认此次 CLI 实际发出的
`subAgentActivity.agentThreadId/agentPath`，所以原 report 含未确认子身份的 gap。
原报告保留；独立原生事实文件核对了活动、非 root 动态工具调用和完成事实。
Go 回放仍使用该样本自己的冻结 driver，以匹配原回执，没有用修正后的解释改原数据。

原生限制与提示词分别说明：Claude 子定义只列 Read 并显式去掉 Agent/Bash/写/网络
工具，原权限回调检查一次 Agent 及固定 Read 路径，观察到额外/越界工具即拒绝并终止。
Codex 关闭 shell/外部工具，使用固定 reader 角色及精确 fixture_read 回调，并配置原生
并发 1、深度 1；仍不把配置被接受当作所有版本/分支递归总次数硬保证。
两端“只创建一个子、不创建后代、只做固定任务”也有 prompt 约束。
所有 case 保留时限、字节预算和 owned process-group 清理，不宣称 OS 全文件/网络隔离。
Claude CLI 2.1.199 早于官方文档明确描述的新递归环境限额版本，未假称这些新限额已强制。

该 case 的源码兼容验证在 `/tmp/agent-sdk-child-case-work` 独立副本完成，21 项针对性
Python 测试通过。共享工作区有另一组新增 recipe/Flow 实现，归属尚未协调，故兼容
patch **尚未集成共享目录**，也未覆盖它们。可审阅 patch 为
`/tmp/agent-sdk-child-compat.patch`；这项源码集成限制和本次已取得的真实取证/回放结果
分别记录。此轮没有修改 Go 运行时或共享 Go 库，也没有为凑顺序重录子 case。

最终索引 SHA-256：
`a70452844e81ee0cf47f7dece31d7ce1d9f8eae92c70e8b2a4222214bc352fb7`。
