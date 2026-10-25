---
type: Specification
title: 子详细交付与独立子历史验收
status: stable
---

# 子详细交付与独立子历史验收

本页是 [US-003](US-003.md) 的 optional-child-detail 共同子场景及
[US-014](US-014.md) 的 Claude independent-child-content 原生子场景，不重编号
既有目标。关联 SDK-US-006（子事件）、007（父先结束）、008（发现）、019（独立查询）。
[过滤合同](../child-detail-delivery.md)与[子历史合同](../claude-subagent-history.md)
规定预期，不以验收设计声明实现完成。

## 公开 seam 与必须断言

| 子场景 | 公开操作与独立预期 |
|---|---|
| F1/default-options | 两原生 Open 未填/空/preserve 的输出逐帧相同；无新 wire 设置；Claude initialize 的 forwardSubagentText 始终 true。非法值在 fake peer 收到启动/RPC 前拒绝。Claude ResumeID + 两策略仍固定本次策略；Codex 低层 ResumeThread 不接受 SDK 策略、不生成新的 Session resume API。 |
| F2/pure-detail | 经原生关系确认后发送矩阵每个白名单方法/shape；preserve 交付，suppress 不交付。根同类帧照常。比较保留帧原始 bytes、来源、重复和 FIFO 相对次序，不能只统计条数。 |
| F3/unknown-and-mixed | child-before-confirmation 隔离且不回填；已入 Session 但来源不充分的帧保留。加入 unknown 字段/块、decode error、未列方法、known member unknown parent、sibling 和 foreign。mixed 详细+tool_result/关系/terminal/error 整帧与 Raw 不变；身份不从字符串猜。 |
| F4/authority | 过滤开启时真实 child 原审批仍单次 Approve/Deny/Answer，保留取消 context、已答/撤销/终止和 OutcomeUnknown；观察及历史无回答权。普通 tool_use 不变成权限请求；未知来源请求保持原 registry 规则。 |
| F5/lifecycle | 父 spawn/wait/Agent 结果、关系/身份及子成功/失败/取消保留。root terminal 后仍可 Receive 子 terminal；背景未触发不能断言后台完成。慢消费者不阻碍控制回复/取消；Close 排空，Abort 丢 backlog，无额外队列。 |
| H1/no-session | 临时显式 ConfigDir，经 package ListSubagents → 选 agent → GetSubagentMessages，CLIPath 不存在且所有 live flag=0；无 Session/进程/模型 fallback。已有同身份 Session 时以 peer barrier 证明无 resume/control/Receive 注入。 |
| H2/layout-identity | direct、workflows/run 及多级目录；非 UUID agentID；缺 sidecar 保持 nil，存在则精确保留 toolUseId/parentAgentId/unknown/null。跨项目重复父 ID、跨路径重复 agentID、身份冲突分别失败，路径目录不能制造父边。 |
| H3/child-chain | 合成分叉 fixture 的最后 user/assistant 为叶；根到叶再切片，isSidechain/isMeta/team 不过滤。故意放 compaction logicalParentUuid 与同 response 的 stray tool_result，断言两者不被主历史恢复算法接入。重复 UUID、cycle、missing parent 为 corrupt；message unknown、大整数和 detached 所有权保留。 |
| H4/bounds-errors | nil/cancel context、负数/非法 ID、显式项目缺失、空源/无叶/超页尾、无 child 容器、孤立/坏/无权限 sidecar、partial 最后一行、坏 JSON/UTF、未知类型、目录/文件/行/byte/index/depth/result 上限，逐项检查 Kind/Cause/Coverage，错误无部分值。 |
| H5/containment-change | symlink/FIFO/device/文件及目录 replacement 不逃 root；测试 barrier 注入 append、rewrite、库存增删、sidecar 出现/消失返回 changed。不能靠 sleep 证明一致性；固定 Atomic=false 和跨调用限制。 |

CommonStoriesSuite 使用命名子场景 `TestOptionalChildDetail/{codex,claude}/F1…F5`，
断言放既有 acceptance_test.go，native peer 不进入生产依赖。Claude 原生套件使用
`TestIndependentSubagentHistory/H1…H5`，fault/时间/文件替换测试可在原生包测试中
用确定性屏障补充。上述是后续实现测试目标名称，不是假称当前函数已存在。
现有 SDK-US-001–018 循环、US-014 browser 和历史证据继续保留。

三个证据 seam 分开：公开 Receive 判定过滤效果；原始 peer trace/录制原 bytes
判定源输出及未改写；独立本地文件 API 判定 child saved content。mock 成功只证明
合成行为，不证明真实 child emitted/received/parent consumed。相同原始 capture
分别跑默认/过滤可比较保留 ordinal 子序列，不重写原录制以伪装过滤输入。

## 真实拓扑与安全前置

以下为待单独批准的中性测试设计，不能从本页取得调用或公开许可：

- direct：root 请求 A 返回固定标记 ALPHA；记录创建证据、子文本、父工具结果和 terminal。
- nested/parallel：root 请求 A、B，A 请求 C，分别只返回 ALPHA/BRAVO/CHARLIE；
  记录每条实际父边、累计 attempts 和深度，不能从输出词推断成功创建。
- parent-first/background：root 返回 ROOT，背景 A 后续返回 ALPHA；分别观察根结果、
  父工具结果、子 terminal/失败和实际背景状态。仅设独立有界 wait，不追加输入或重试强造。
- Claude storage：对每次运行实际新建 child 文件、sidecar 与 wire trace 分别取证，
  文件 agentID/父 UUID 用类型化映射关联；文件不存在/身份未知是缺口。

launch 前必须全部通过；任何无法执行的 gate 都是 blocked，不能用 prompt 替代：

1. 操作者批准具体 provider/model、CLI 绝对路径/version/digest、隔离环境、精确中性
   prompt/input hashes；全新合成项目，不读取旧会话/私有日志/工作区/外部集成。
2. 批准每场景及总 root sessions、submitted turns、provider/native retries、累计
   child attempts（含失败/重试）和最大深度；递归拦截在创建前扣预算。并发上限不等于
   累计上限，prompt 不是限制。重启不可重置 durable ledger，旧预算不续期。
3. 批准每场景/活动总 wall time、独立 background deadline、wire/stderr/storage bytes、
   usage accounting、明确可执行的金融 stop。--max-budget-usd 不作独立账单证明；
   无法强制所需美元上限就不运行。根 turn 不等于一次 provider 请求。
4. 每层 child 均验证 hook/policy/tool 隔离和允许清单，禁止任意 shell/network/文件访问。
   启动后看到 hook 再拒绝不能证明 hook 未运行。operator 提供已获准的 opaque auth
   transport；不查、复制或修复平台凭据，不修改权限来绕过拒绝。
5. 先用合成 peer 证明递归拒绝、budget/timeout/byte 停止、错误及 cleanup。实际 CLI
   pin/flag 支持另核验，不静默升级旧 CLI pin，不以 offline pass 宣称真实运行通过。
6. 原始 wire/control/stderr/storage 留 Git 外，0700/0600、受限拥有者 cleanup；不覆盖
   旧 bytes/hash。公开另经逐版本 allowlist、unknown quarantine、secret/path/header/
   email canaries、ID 类型与相等关系、timestamp 类型/顺序及来源 manifest 审查授权。
   保留 exclusions/coverage，合成故障不标 real。任何拒绝停止对应 launch/publication。

所有 live flags 默认 0，CI 不录制。能力 implemented、native/version unsupported、
condition untriggered、auth/policy/budget blocked、synthetic-only、retained-real-fragment
分别报告；未观察到完成不是成功背景验证，未实际运行不是 live pass。
