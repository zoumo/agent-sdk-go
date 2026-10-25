> Historical evidence for the pre-removal baseline. Load and its contracts have been removed; this report is not a current API guide.

# Session SDK-US-001～020 公开验收

本页保留 T12 的检查结果与当时的未解决问题。T14 的最终集成修复、全仓验证及剩余能力缺口见[集成验证报告](../../reviews/ticket14-integration-verification.md)；以下历史失败记录不改写为当时通过。

本记录对应 [ticket 12 的共同目标](../../../harness/sdk/user-stories.md)、[公开 Session Interface](../../../harness/modules/native-session/interfaces.md)、[事件交付](../../../harness/contracts/event-delivery.md)及 Load 合同 (removed Load design)。实现支持与证据强度分列：**合成通过不等于真实兼容性认证**。Claude 的 SDK-US-019/020 成功部分仍未满足，两端不能据此宣称完整满足全部共同目标。

## 运行入口与断言

`CommonStoriesSuite.TestSessionTargets` 在同一 suite 中直接运行全部 20 个新编号，每项下有 `codex` 和 `claude` 子测试。具体 Session、原生 Received/Event、原 IncomingRequest/Handle 留在各自原生 adapter 中；公开观察和验收在 [acceptance_test.go](../../../tests/stories/acceptance_test.go)。[session_peer_test.go](../../../tests/stories/session_peer_test.go) 是自执行测试二进制的离线 native peer，不调用真正的 CLI、模型或工具，不是录制。没有新增 production generic client、统一 native event、共享回答状态机、testkit 或第二生命周期框架。

peer trace 只记录实际到达原生对端的输入字节和启动参数。输出断言来自公开 `Receive`，不从内部成员树、队列长度或请求 registry 推断应用已经看到事实。Codex Thread fixture 使用完整 required 字段；受支持消息还检查 typed decode 错误，问答请求通过原句柄的 `Data` 解码。Claude 正常 envelope 补齐固定 0.3.258 的 result usage/cost/UUID、status、init、task_started 与 message required 字段；缺字段不作为正常能力证据。未知变体和协议故障明确分开。

每个新编号的测试路径为 `TestCommonStories/TestSessionTargets/SDK-US-NNN/{codex,claude}`。表中“合成”均指本次公开端到端路径；固定声明/源码只说明协议依据，不能代替运行证据。

## 逐目标支持与证据矩阵

| 目标 | Codex implementation support | Claude implementation support | Codex evidence strength / 精确检查 | Claude evidence strength / 精确检查 |
|---|---|---|---|---|
| SDK-US-001 多输入 | 支持长期主 Session、同连接身份唯一 owner | 支持单进程主 Session；不虚构 CLI Turn 或跨进程共享连接 | 合成：重复 Open 冲突；两次 Submit/原生 turn/result，结果不关流 | 合成：两次完整本地写入及 result，结果不关流；无接受回执宣称 |
| SDK-US-002 结构输入 | 原生 input/执行参数，固定 ThreadID；未知输入拒绝 | text/image/document/tool_result 接入范围；固定 envelope | 合成：peer wire 的 image、model、OutputSchema/null 精确比对，未知输入未派发 | 合成：实际 wire 的四类块、image/document source、future:null/大整数/priority、固定 session_id/parent，子目标拒绝未派发 |
| SDK-US-003 执行期输入 | 沿原生 turn/start 接受/拒绝，不自动 steer | 持续写入；CLI 的接受/调度没有 submit ack | 合成：前次未完成时继续 start，两次接受、第三次 RPC 拒绝，连接仍可用 | 合成：无结果期间两次写入，随后原生失败 result，写成功与原生结果分开 |
| SDK-US-004 结果区分 | RPC 接受、commandExecution、turn 终态分别保留 | 写入、原生 tool_result、result 分别保留 | 合成：读到工具原生 ID/输出/状态后，peer 尚未释放执行结果；显式控制后收到终态 | 合成：tool_use_id/内容和 result.is_error 检查；工具阶段无执行结果，控制后释放结果 |
| SDK-US-005 Raw/重复 | native 类型、Raw、未知 envelope/params 与重复保留 | 已知 envelope 中未知字段保留；未知归属 envelope 隔离 | 合成：相同 item ID、相同 Raw 的两次事实均可读，大整数/null不丢 | 合成：相同 UUID/Raw 两次均可读；未知 envelope 的原文诊断及缺口，不借 session ID 冒充根消息 |
| SDK-US-006 子观察 | 有成员证据即可交付；父知识限制不限制成员 | 固定 task/tool 证据可关联，缺父与身份扩展保持未知 | 合成：嵌套/并行原生输出在公开流；MaxParentDepth=1 故意拒绝父边仍交付；通信 external 不造父/成员 | 合成：先见 task 无父及其输出，后见 root tool；嵌套 tool owner 与并行 sibling；agent_id 不当 task alias，foreign session 隔离 |
| SDK-US-007 根先结束 | 继续持有确认子身份的 attachment；覆盖有缺口 | 转发内容可接收，初始化请求不能保证完整转发 | 合成：根 turn/completed 后 grand 原生 delta；隔离早期/外部材料有缺口；无 resume/read 重跑 | 合成：根 result 后 grand assistant；观察/身份缺口保留；无额外执行 |
| SDK-US-008 查询存量 | 原生 descendant 查询，分页/归档/来源过滤明确 | 固定 config/cwd/main 的记录扫描；无原生 cursor | 合成：分页一页限额部分结果、完整两页、独立归档查询；实际 ancestor 参数、无 parent 过滤；查询不恢复/提交/订阅或入流 | 合成：旧/嵌套文件、限额部分结果、fresh 新文件、unknown running/非原子覆盖；查询无输入/回答/新事件 |
| SDK-US-009 审批扩展 | command approval 操作与原 execpolicy 扩展 | can_use_tool 的 updatedInput/updatedPermissions、deny message/interrupt | 合成：同流真实请求，未答时控制可用；deny/approve 各一次，peer 原 ID、policy amendment、重复拒绝 | 合成：同流真实请求，peer 原 ID、更新输入/权限及 deny 扩展，重复拒绝；无人时零自动答案 |
| SDK-US-010 延迟/取消 | 原生 registry 撤销与单次权威 | 原生 cancel_request 撤销与单次权威 | 合成：延迟时继续收控制，撤销 first 后拒绝、预取消 second 不消耗句柄，最终仅 second 一次原 ID 回答 | 合成：同上，保留 Claude NotSent/cancel 语义，无默认拒绝 |
| SDK-US-011 真问答 | question ID → answer 数组 | 完整题文键 → updatedInput 答案 | 合成：两个真实 requestUserInput，选择与中文自由文本原结构精确回写；不能 Approve 问题或重复回答；普通输入仅一次 | 合成：真实 AskUserQuestion，选择与中文文本回写题文键，questions/future 保留；输入不替代回答 |
| SDK-US-012 创建/动态配置 | 创建参数与 experimental thread settings 分开 | 创建 model/thinking 与动态 budget/display 分开；不支持的 display 明确拒绝 | 合成：实际 thread/start model/config 与独立 settings model/effort wire，native rejection/本地 invalid，不重建/无子递归修改 | 合成：实际 launch model/budget/display flags；动态 model、0、null-clear、omitted model 精确 wire；不重启，unsupported/native rejection |
| SDK-US-013 ack/observed | 空 RPC ack 不表示当前 turn 已生效 | control success 不建立执行切换屏障，Observed 未知 | 合成：requested-model 回执与以后 observed-model 原生 settings 分开 | 合成：Accepted/Observed=nil 与以后原生 init 的 observed-model 分开；不推断 child 继承 |
| SDK-US-014 慢消费 | 单 FIFO 完整交付，控制不等消费；仅内存可用量限制 | 同左，原生控制独立处理 | 合成：256 条 unread burst 后仍能 Interrupt/Close/Done，完整逐序号排空，无静默丢弃 | 合成：同左；不是 heap 上限/OOM 保证 |
| SDK-US-015 本地取消/未知 | Receive 本地取消不终止；已发送 RPC 超时不重发 | Receive 本地取消不终止；开始写入后未知结果可终止交互 | 合成：预取消 NotSent、peer 确认收到后取消 OutcomeUnknown、后续 Interrupt 可用且只有一次 start | 合成：预取消 NotSent/Receive 可续；对端读取大帧第一个字节后取消，OutcomeUnknown/ErrUnknownOutcome，Close 保留原因、不重发 |
| SDK-US-016 Interrupt/Close | 显式主 turn，关闭仅释放本 owner，外部连接共享 | 无合成执行 ID；关闭自有进程，不承诺递归停止 | 合成：实际 interrupt 主 ThreadID/显式 TurnID；Close 后另一 Session 可提交，只有本 attachment detach | 合成：Interrupt 后仍可写/读；Close 幂等、Done 独立、closed control 不发送；只有一个 owned launch |
| SDK-US-017 失败/排空 | 原生 failed turn 不等于 SDK 故障；终止保留首因 | result.is_error 不等于 SDK 故障；后续失联终止 | 合成：原生失败后仍可交互；失联失效真实句柄；32 条已接收事件排空后故障，Close 不改首因 | 合成：同上，原生 result/is_error 与交互原因分开；清理不等消费 |
| SDK-US-018 断线 | 报错，不重连，不证明 native execution 停止 | 同左 | 合成：Done 后排空 marker 再故障，后续提交拒绝；只初始化/启动一次 | 合成：同左，只 launch 一次，不补事件或重试输入 |
| SDK-US-019 Load | 支持稳定持久主历史：确认同身份、单 FIFO、pending gates/控制/成功后继续；非原子/非active限制 | **部分/原生能力缺口**：pending 交互和明确失败；固定基线没有无输入同身份恢复确认，不能成功 Load | 合成：confirmed/mismatch/late/source/active/cancel/close；历史-live交错及真实审批/Interrupt；成功后取消 startup ctx 仍提交；overlapping-owners 验证 child Close 不撤销 ancestor attachment/原权限 | 合成：interrupt/cancel/close/mismatch；实际 pending 句柄批准、失效、门禁、仅一次 failed boundary、drain；initialize 后 ErrResumeUnconfirmed，无历史/普通输入。成功/交错/成功后ctx未满足，**不是 skip 或成功认证** |
| SDK-US-020 比较回放 | 保留历史 native ID/Raw/unknown、显式来源及 MissingFields；不保证原始实时序列/缺失补齐 | **部分/原生能力缺口**：public Load 不回放；独立离线历史投影支持不等于 live Load 成功 | 合成：原始 user/unknown/tool item 字节、tool失败/exitCode/输出、item/turn ID、缺 completedAtMs、unknown futureStatus，Raw 不冒充 live；迟到失败/源变更不报完整，无历史回答权限 | 合成：固定失败路径无历史、无历史活权限或重执行；已保留的单包 offline history projection/integrity 回归单独验证，不能填充这里成功路径 |

## 固定原生与证据边界

原生来源仍是 [固定接入清单](../../research/session-native-integration-checklist.md)、[Claude 原生证据](../../research/claude-session-native-evidence.md)：Codex CLI/schema 0.162.1、源 `092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`；Claude TS SDK 0.3.258 / CLI 2.1.258。来源、哈希、许可证和 fixture 字节没有改变。没有新 upstream 包、baseline 升级、test-only Load success、额外 prompt 或 guessed resume control。

[真实录制](../../recordings.md)的原 scope 不升级：Codex S1 仅成功根任务与两个 dynamic tool calls，S2 仅真实 user-question/原回答；Claude S1 仅认证失败，没有成功模型/工具执行。所有真实录制都没有子 Agent。它们不是本轮 Session/config/discovery/Load/child forwarding 的真实认证。本轮没有真实 CLI/Agent/模型执行或新录制。

旧 `US-001～012` 仍单独运行在 `TestNativeAdapters`，已迁移至公开 Session/Receive/Submit 和原句柄；它们与新 `SDK-US-001～020` 含义不同。旧 US-011 的 Codex caller-seeded Subscribe snapshot 改为公开只读记录查询及其非归档/实时覆盖限制，保留“不虚构创建/执行、零输入/回答副作用”的有效保证；Claude 的原生固定 tool/task 关系替换未证实的 parent_task_id 推断。旧 US-011 在初始化阶段主动接收记录证据和对端计数，零 user 输入；不发送一个被 peer 特判为“不计数”的 snapshot 提示词。旧 US-013 仍不在旧 storyIDs 中；新 SDK-US-008 独立覆盖查询，不能说旧 US-013 已通过枚举。

原生编解码、queue/lineage、并发、资源、历史、写入歧义及录制回归继续保留。本票只迁移共同故事所需消费者，不执行 T13 的全仓旧模型删除。验证命令与实际结果见下方；已知原生/证据缺口不会因 green gate 消失。

## 本地验证

- `rtk mise exec -- env GOMAXPROCS=2 GOPROXY=off GOSUMDB=off go test -p=1 ./tests/stories -count=1`：通过（全部 20 新目标与 12 旧故事，非真实 Agent）。
- 最终公开 stories race 通过：`rtk mise exec -- env GORACE=atexit_sleep_ms=0 GOMAXPROCS=2 GOPROXY=off GOSUMDB=off go test -race -p=1 -count=1 -timeout 300s ./tests/stories`（4.348s）。
- 最终 `rtk mise exec -- go vet ./...`、`rtk git diff --check` 通过。
- 最终全仓 offline gate 通过：`rtk mise exec -- sh -c 'export PATH=/tmp/session-ticket01-bin:$PATH; bash scripts/check-offline.sh'`；覆盖依赖边界、模块、schema/inventory/generation、脱敏/manifest/hash/transcript、所有 Go offline tests（Claude 11.219s、Codex 32.945s、stories 2.548s）、本地公开 consumer 和四个离线示例。Go 1.26.9；真实 E2E 保持 opt-in/skipped，不记真实通过。
- 相关原生 race 命令 `go test -race -p=1 -count=1 -timeout 300s ./codex ./claude ./internal/queue -run 'TestSession|TestLoad|TestHistory|TestQueue|TestStartupFailureReleasesUnconsumedNativeQueue'` 的 Codex（9.840s）和 queue（2.547s）通过，Claude writer_prefix 时序失败；诊断见下节，不能声称该命令整体通过。
- 两个独立 code-review subagent 按固定 base `fb30415781a5d52ac59a9bed7fcd5d439712f3ae` 审查；最终代码 `61b1cf92353595907203d4797a62f127c75a305d`：Standards 0 剩余发现、Spec 0 剩余发现。初审指出的关系无回答权回归、Claude 必填字段及其遗漏 emitter、旧 US-011 隐藏实际输入均已修复并独立复查。本次完成记录只补文档，不再改变受审代码。
- 外部精确 pushed-revision consumer gate 不运行：没有 push 授权。没有 push/release。

## Race 时序诊断与后续范围

本票相关原生 race 首次运行：Codex/queue 通过；Claude `TestSessionPermissionQueuedCallerCancelPreservesLiveAuthority` 在 `session_requests_unix_test.go:64` 等待 `writer_prefix` 的 1 秒预算内失败，没有 data race。隔离 `-count=10` 复现一次，带临时失败诊断的 `-count=30` 复现两次：Session cause=nil、对端文件仅 391 字节（前置协议）、只有 writer_ready、无 Expired/Dropped，Submit 仍未返回。说明未到真实写入阶段，不是权限取消错误或隔离过期。

临时阶段计时测得 2.5 MB 输入在 race 下多次 JSON 编码/解码到开始写入约 0.86 秒，已逼近该 1 秒预算。仅用于诊断把同一输入从 2.5 MB 增至 3 MB（不改 deadline）后 `-count=3` 两次失败，失败时 goroutine 堆栈直接位于 `encoding/json.appendCompact` → `Client.writeJSONChecked`，另一次位于 `encoding/json.Unmarshal` → `sendInput`，尚未到管道 Write。原因是该 readiness 时限包含了大帧的多次编码/校验成本，race 下编码余量不足。临时计时/堆栈/输入改动已全部撤销；`git diff <fixed-base> -- claude` 为空，没有放宽 deadline 或降低测试规模。该既有写路径性能/屏障时序问题交 T14，不能把重跑通过描述成从未失败。

之前 T10 已证实的 `TestWriteFailuresAreReturnedDirectly/{closed_stdin,blocked_write}` race 失败仍是 T14 项。本票不声称 full-repository race 通过；普通 offline gate 与公开共同故事 race 分别报告，保留失败证据与范围。

## Red → green 与范围

先在批准的公开 seam 增加故事再补齐原生 peer：SDK-US-001 起初缺少具体 adapter 编译失败；输入/执行期故事在 peer 不拒绝第三个 Codex start、Claude 缺原生失败结果时失败；审批故事缺真实请求时超时；配置故事缺原生控制回复时失败；Load 故事缺 read/resume/paging 对端时失败。之后分别补固定原生形状及故障脚本，公开 Session 既有实现满足检查，没有发现本票必须修改的 runtime 缺陷。迁移旧 US-011 最初因未启用 Codex experimental discovery 失败，明确初始化能力后通过。新增历史工具值断言曾因把 int32 exitCode 写成 int64 的断言类型错误失败，按原生类型修正；没有放宽语义或 deadline。

首次 stories race 的 late-history peer 先发送分页失败再返回 Interrupt 回执，使 owner 合法结束后控制返回 OutcomeUnknown；改为明确先回控制 RPC，再释放故意分页失败，保证测试实际表达“控制在加载期间可用，随后 Load 失败”的因果顺序。该修正只作用于合成 peer，不修改原生错误分类、取消或清理实现。
