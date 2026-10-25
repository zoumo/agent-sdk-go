> Historical evidence for the pre-removal baseline. Load and its contracts have been removed; this report is not a current API guide.

# 固定原生 Session 接入清单

日期：2026-10-25。SDK 检查固定点 `77abc43d29eac8786f317e3e8ac6b354160a346b`；目标为 [Session Interface](../../harness/modules/native-session/interfaces.md) 和 [20 项共同故事](../../harness/sdk/user-stories.md)。本文是 ticket 01 的证据与后续接入清单，不是实现报告或兼容性通过声明。未修改原生 types、版本、生成输入、录制或来源表。

## 证据等级与固定来源

- **I**：当前 Go 实现的静态检查，部分基础不等于目标 Session 已实现。
- **D**：固定原生声明；**S**：固定拥有源码路径；都不证明真实运行。
- **Y**：现有合成/离线测试；**R**：保留的真实录制；**V**：对应目标公开路径的真实验证。下面没有任何新 V；Y 通过也不升级为 R/V。

### Codex C 基线

CLI/schema **0.162.1**，源码 `092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`，Apache-2.0。完整原有版本、schema 选择闭包、精确字节哈希及许可见 [sources.json](../../codex/docs/protocol/sources.json)、[LICENSE](../../codex/LICENSE) 与 [NOTICE](../../codex/NOTICE)。NOTICE 中旧 Source revision 是历史值，与 sources.json 当前 pin 不一致；本次不修改来源元数据，也不以 NOTICE 旧值替代实际 pin。后续交付须核对该差异，不能声称来源文本已全部同步。

本次用已安装 gh 2.92.0（github.com/zoumo）读取固定官方 contents API；协议 thread.rs/turn.rs 最初通过固定 raw URL 取得。原生 CLI 未执行。以下精确字节 SHA256 与先前报告一致；上游源码未复制到仓库。

| 编号 | 官方拥有源（同一 commit） | SHA256 与结论 |
|---|---|---|
| C1 | [v2/thread.rs](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/v2/thread.rs) | `553d99d01eed0c7a9073f481938f7dd68ef633879be0e1ffd8373778c937b158`；236–332 settings/model/effort/notification，1394–1465 ancestor/source/archive/page，1551 response cursor。 |
| C2 | [v2/turn.rs](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/v2/turn.rs) | `eb1a99cc72a65af84f933073881bfc0440288fb629de7ec92b7c1b50d3530779`；turn/start 与带 expectedTurnId 的 steer 是不同请求。 |
| C3 | [turn_processor.rs](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/turn_processor.rs#L931) | `50bc2dd50ba86c569a81abbe527000735ae90280dcaf8502e9e8c1641864f7b7`；settings 提交 Op::ThreadSettings 后空响应；不能证明当前执行已切换。 |
| C4 | [thread_processor.rs](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/thread_processor.rs#L2524) | `2c40a6db590c9823a6775b28c2e125544583246318ce038166d28fb57a10ab29`；DirectChildrenOf/DescendantsOf、互斥过滤和存储错误；3607 起 best-effort auto-attach 不是完整观察证明。 |
| C5 | [thread_lifecycle.rs](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/thread_lifecycle.rs) | `967cbda6493c971232d5434af690e0c86a318589492d3c14344ee249a7267457`；resume 返回 thread，warm resume 可重发 pending requests，不能作为无副作用自动订阅。 |

原生 query/settings 的生成根尚未接入：当前 [capabilities.json](../../codex/docs/protocol/capabilities.json) 的 unsupported 不是原生不存在；后续新增类型必须通过同一 pin 的原始 schema 和生成器，不手写替代生成来源。

### Claude A 基线

CLI **2.1.258**；TS npm **0.3.258** 的声明/实现用于该版本研究。Python `f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab` 目标 CLI **2.1.302**，仅作明确标注的辅助源码，不能证明旧 CLI 接受。详见本次 [Claude 固定来源调查](claude-session-native-evidence.md)（下列 A 编号）及原有 [来源与许可表](../../claude/docs/type-sources.md)。TS 包非 MIT，不分发其实现、声明包或二进制；Python MIT 只读研究，无代码复制。

本文编号：A1 = 固定 T sdk.d.ts 声明；A2 = 固定 T sdk.mjs 输入/控制实现；A3 = 固定 T 子记录递归发现与 sidecar 实现（调查第 5 节）；A4 = 固定 T 主链筛选及明确标注的新 Python 对照（第 6 节）；A5 = 第 2 节恢复身份候选调查。每项精确哈希见调查来源表。

关键结论：固定本地 stream-json 路径未找到有充分证据的无输入恢复身份确认机制。initialize 确认控制握手；getSessionInfo 读取存储；另一传输的 getSessionId 不能证明本地 --resume。未证明不可能存在私有机制，但当前证据不能支持 Load 成功。实现须以 resume-unconfirmed 明确失败，不等待提示词或伪造 observed ID。

## 输入、配置与查询的接入边界

| 项目 | 固定依据与当前 I | 后续操作、参数与成功含义 | 未证明/明确不支持范围 |
|---|---|---|---|
| Codex 输入 | C2；`types.UserInput` 当前已生成 text/image/localImage/audio/localAudio/skill/mention，unknown raw 保留。 | Submit 接收原生内容与 turn/start 选项，Session 固定 thread；返回原生 turn receipt。保留 text_elements、图片 url/fileId/detail、本地 path 等。 | 类型可编码不证明模型支持每种媒体；未知 union 不自动算已接入。steer 需显式 expectedTurnId，不由 Busy 自动选择。 |
| Claude 输入 | A1/A2：SDKUserMessage 的 string 或原生 user 内容块；Go 当前 Send(string)。 | Submit 写原生 user envelope；文本、图片和文档按发送声明接入，保留未知字段与支持的关联，完整本地写入即返回。 | 输出 tool_use/thinking/tool_result decoder 的存在不证明所有块均可作为新用户输入；仅供工具答复的结构不等于普通提示输入；无额外 CLI 接受回执。 |
| Codex 配置 | C1/C3：model/effort，后续 turns；当前只有创建/turn 参数，缺公开 setter。 | SetModel(string) / SetThinking(native effort) → settings/update，保留空 ack；settings updated 从 Receive 单独观察。 | ack 非活跃 turn 切换；门槛/原生拒绝原样保留，不用新 turn 模拟设置。 |
| Claude 配置 | A1/A2：创建 model/thinking；动态 set_model/model 与 set_max_thinking_tokens/max_thinking_tokens、thinking_display。当前 Go 无 setter。 | 创建 enabled/adaptive/disabled thinking 与动态可空预算分开；保留原生 control_response 和请求值。 | deprecated token 名称不证明所有模型硬预算；不推断当前执行及子 Agent 生效，不将 adaptive 隐式映射成整数。 |
| Codex 子查询 | C1/C4；当前无正式 wrapper。 | ListSubagents 查询 ancestorThreadId=主 ID，cursor/limit/sourceKinds/archived；保留 nextCursor、原生身份与可用父级。 | 声明中默认/空 sources 偏 interactive；拥有实现 C4:5504–5509 对 relation_filter 存在且 source_kinds=None（缺省/null）特殊处理为 unrestricted，显式空数组仍调用 compute_source_filters；archived=true 只归档，false/null 只非归档；全范围须分别查询。关系过滤实验门槛、存储索引、非原子分页、访问失败明确报告。列表不启动/恢复/订阅，不保证记录仍运行。 |
| Claude 子查询 | A1/A3：主记录旁 subagents、嵌套目录与 sidecar；Go 只有观察缓存/seed。 | ListSubagents 独立扫描显式 ConfigDir/cwd/main ID，保留 agent/parent/tool 元数据和扫描范围；限数/字节/访问失败返回部分覆盖。 | 固定 T 实现也递归发现嵌套目录；这不证明 CLI 实际产生全部布局。没有原生 cursor 不伪造。sidecar 缺失保留未知父级，目录可发现不证明实时活跃/转发完整。 |
| Claude 元数据 | A3/A4：Python reader 主链筛选；permission-mode 明确元数据；TS 白名单排除其他非链类型。 | 四种 permission-mode/ai-title/file-history-delta/cost-state 与链 user/assistant 分开调查；合法非链记录不投影为会话消息。 | 排除主链不证明追加元数据可安全接受。未知/破损/身份不符仍失败；四种完整 schema 与旧 CLI 追加语义证据不足，不能扩成“所有未知类型忽略”。 |

## SDK-US-001～020 逐项检查

“部分”均指现有基础，**本 ticket 没有实现新的 Session API**。原生依据列与验证列相互独立。R 仅限 [录制索引](../recordings.md)：Codex S1 动态工具，S2 原始问答；Claude S1 为认证失败，均零真实子 Agent。旧 `tests/stories` 只有旧 US-001～012，不能等同新 20 项。

| 故事与目标操作 | Codex 当前 I；D/S 依据 | Claude 当前 I；D/S 依据 | 现有 Y/R/V 与后续公开 seam |
|---|---|---|---|
| 001 Open/Submit/Receive/Close | 部分：thread/turn/subscription 分散，缺单 owner；C2/C5。 | 部分：长期 Client 可多输入，缺目标 Session 组织；A1/A2。 | Y lifecycle；无新 V。测同主重复拥有、两次输入、结果不关流。 |
| 002 Submit 原生结构/主目标 | 部分：UserInput 有结构，无固定主目标；C2。 | 部分：仅文本 Send；A1 user message。 | Y codec/文本，R C-S1；测每个接入内容保真、未知拒绝。 |
| 003 执行期间 Submit | 部分：无 Busy gate，start/steer 不同；C2。 | 部分：持续 writer，不证明执行中接受；A2。 | Y 旧 peer 非新验收；测原生接受/拒绝，不新增队列调度。 |
| 004 Submission/结果区分 | 基础已有 RPC receipt/item/turn 分离；C2。 | 基础已有本地写与 result 分离；A1/A2。 | Y turn contract/result，R C-S1；测工具结束非执行结束、结果非全部停止。 |
| 005 Receive 原文/重复 | 部分：Raw 保留，但 terminal 抑制、请求顶层缺失。 | 部分：Raw 保留，但 UUID 抑制和 held 重排。 | Y codecs/unknown；R 仅样本；测同 ID 不同 Raw/完全重复均保留，六类证据不混。 |
| 006 嵌套/并行成员输出 | 部分：完整父图 gate，通信可能伪父级；C4/C5。 | 部分：父图 gate，未知 run 排序无版本依据；A1 转发。 | Y nested，无 R/V；测已知成员未知父级立即交付及其他 Session 隔离。 |
| 007 根结果后观察 | 部分：持续流、auto-attach best-effort；C4。 | 部分：result 后持续流、forwardSubagentText；A1/A2。 | Y parent-idle-child-active；无 R/V；测后续事件与覆盖缺口，不 resume 子级。 |
| 008 ListSubagents | 缺公开查询；C1/C4 有原生分页。 | 缺独立发现；A1/A3。 | 无目标查询 Y/R/V；测分页循环/空页/来源/归档/部分失败、零执行与订阅。 |
| 009 IncomingRequest 批准拒绝 | 部分：原句柄在独立 Requests；各原生 response types。 | 部分：permissionState 在回调且默认 deny；A2 can_use_tool。 | Y one-shot/原生选项，R C-S2 是问答非审批；测同入口、扩展与无权拒绝。 |
| 010 延迟审批/失效 | 部分：默认本地 5 分钟超时。 | 部分：默认 deny/回调交付；A2 cancel。 | Y 旧超时不是目标通过；测超过旧期限、cancel/answer/Close 竞争。 |
| 011 Answer 实际问答 | 原生 ToolRequestUserInput/原句柄已具基础。 | can_use_tool 的 AskUserQuestion 原结构有基础；A2。 | Y 问题键/答案；R C-S2；测历史/普通输入/tool观察不能回答。 |
| 012 SetModel/SetThinking | 部分：创建/turn 有参数，setter 缺失；C1/C3。 | 部分：创建 model，thinking/setter 缺失；A1/A2。 | Y 类型≠操作；无 R/V；测创建/动态、已知 unsupported、原生拒绝。 |
| 013 ConfigurationReceipt/观察 | 缺正式 setter/观察合同；C1/C3。 | 缺正式 setter；A1/A2 ack。 | 仅 D/S，无配置效果 R/V；测 requested 与 observed、后续 turn范围。 |
| 014 慢 Receive 仍控制 | 部分：ChannX/独立 RPC，但 pending 重排清空。 | 部分：backlog/独立 control，但 held/默认决定。 | Y queue/backpressure；测单 FIFO、慢历史期间控制、明确内存/隔离限制。 |
| 015 取消等待/结果未知 | 已有 NotSent/OutcomeUnknown 基础。 | 已有写入歧义基础；A2 writer。 | Y timeout/race；无新 V；测 Receive ctx 局部取消，输入/答案不重发。 |
| 016 Interrupt/Close | 部分：InterruptTurn 与 owner 清理分散；C2/C5。 | 已有 interrupt/进程清理基础；A2。 | Y owned child/remote close；无真实中断 V；测共享连接隔离、失效与回收。 |
| 017 故障与终止排空 | 部分：native failure 分开，但 pending 丢弃。 | 部分：基本 drain，但 held/抑制影响完整性。 | Y delivery/fault；R Claude 只 auth失败；测故障排空、清理不等消费。 |
| 018 断线不自动恢复 | 已有永久 gap/Done/Err，无重连。 | 已有 process failure/Err，无重连。 | Y transport faults；无新 V；测失联不等于执行停止、无自动重发。 |
| 019 LoadState/同身份恢复 | 部分：read/resume/pages，未直验 resume response ID；C1/C5。 | 不满足：initialize-only 无确认；A1/A2/A5。 | Y 旧 history 非新成功合同；无 Load R/V；测错身份、未确认失败、单入口/live/门禁/清理。Claude先实现明确缺口失败。 |
| 020 历史来源/比较 | 基础已有 HistorySource/Raw/MissingFields。 | 基础已有主链、Raw、快照，metadata过严；A3/A4。 | Y history_events/snapshot；无真实完整Load V；测未知保留、部分失败不宣称完整、无历史权限。 |

## 本 ticket 验证与后续限制

文档改动不需要新增镜像实现的测试；没有运行时实现，因此不编造 TDD red/green。实施 ticket 在已批准公开 seam 上开展 TDD。运行 pinned mise Go 的全仓离线检查（含全测试、生成、inventory、fixture hash/脱敏、依赖边界和离线例子）；结果在 ticket 执行记录填写。未运行真实 CLI、模型、新录制、上游源码测试或独立消费者拉取；未 push/release。全仓离线通过不能填平上表证据缺口。


实际检查结果（本 ticket，文档变更）：

- Go `1.26.9 darwin/arm64`，由 mise 固定；`scripts/check-offline.sh` 全部通过，包含 `go test -p=1 -count=1 -timeout 180s ./...`、离线消费者及四个例子。真实 e2e 由脚本显式禁用。
- 边界、`go mod verify`、6 个 CI gate 测试、协议 inventory（173/11/86/1，controls 24）、408 types / 767 合成案例生成一致性、7 个 sanitizer 测试、录制及 transcript 哈希检查通过。
- `mise exec -- go vet ./...`、文档相对链接、harness 内部引用边界、OKF 元数据及 `git diff --check` 通过。
- 首两次脚本运行因 PATH 的系统 Python 3.9 缺少 tomllib 停止；使用 `/tmp/session-ticket01-bin/python3` 指向现有 `/opt/homebrew/bin/python3.13`，以 `PATH=/tmp/session-ticket01-bin:$PATH mise exec -- bash scripts/check-offline.sh` 完整重跑通过；没有修改依赖或项目工具链。未执行 race；文档变更未改变并发实现。
- 按 code-review 技能，从固定 base 到本 ticket 文档提交并行独立审查：Standards **0** 项，Spec **0** 项。后续仅补充本段实际检查记录，无设计或运行时改动。

## Ticket 08 来源条件补正

固定 C4 `thread_processor.rs:5504–5509` 明确分支 `relation_filter.is_some() && source_kinds.is_none()` → `(Vec::new(), None)`，因此 ancestor 查询下缺省或 null 不限制来源；`Some([])` 进入 `compute_source_filters` 的 interactive 默认。这里是拥有实现的特殊分支，不由 nullable schema 推断。SDK 默认显式选择五类固定子 Agent 来源；调用者 null/空数组逐字传递并在 coverage 区分。归档语义不变。当前公开查询实现、合成证据及覆盖限制见 [Codex discovery](../../codex/docs/session-discovery.md)，不改写原架构报告的历史检查结论。
