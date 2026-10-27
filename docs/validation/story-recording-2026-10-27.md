# 2026-10-27：逐 story 私有真实取证

本次落实 [逐 story 录制流程](../record-stories.md)，目标来自
[19 项共同用户故事](../../harness/sdk/user-stories.md)。不是完整能力通过声明。

## 数据与来源

- Codex CLI **0.161.0**，Claude CLI **2.1.199**。两个原生协议和旧录制的 pin 未升级。
- 两端各有 SDK-US-001–019 共 19 个独立记录项。合计 **38 项、34 份有真实原生协议的数据集**。
  SDK-US-006/007 两端都没有启动 native/model，因此只有阻塞记录，没有伪造协议。
- 14 项完成所选路径，18 项部分覆盖，5 项阻塞，1 项保留失败。这里的 `captured`
  仅表示录制脚本选定的那条路径完成，不表示整个 user story 全部验收条件通过。
- 每项独立 HOME、config、project、tmp 和 raw；只继承必要连接/认证。机器原配置不修改。
  临时复制的认证与连接配置已删除；新建的合成持久化源保留。协议凭据扫描均通过。
- 每项 manifest 记录实际 CLI、二进制 SHA-256、准备时 Git HEAD、源码内容哈希和原始协议哈希。
  录制使用本任务开发过程中的独立二进制；最终代码另做旧数据回放，不改写原录制身份。
- 原始协议、stderr、原始权限、SDK 回执和失败诊断仍是私有资料，未加入 Git 或推送。
  历史 real-sanitized 样本没有修改。本报告不把新私有数据称作已发布 fixture。

## 逐项状态

| Story | Codex | Claude |
|---|---|---|
| SDK-US-001 | captured | captured |
| SDK-US-002 | partial | partial |
| SDK-US-003 | partial | partial |
| SDK-US-004 | captured | captured |
| SDK-US-005 | partial | partial |
| SDK-US-006 | blocked | blocked |
| SDK-US-007 | blocked | blocked |
| SDK-US-008 | partial | partial |
| SDK-US-009 | blocked | captured |
| SDK-US-010 | partial | partial |
| SDK-US-011 | captured | captured |
| SDK-US-012 | captured | captured |
| SDK-US-013 | captured | captured |
| SDK-US-014 | partial | partial |
| SDK-US-015 | partial | partial |
| SDK-US-016 | captured | captured |
| SDK-US-017 | partial | partial |
| SDK-US-018 | partial | partial |
| SDK-US-019 | captured | failed |

## 实际覆盖和限制

- 001：同一原生身份上连续两次输入；同连接重复所有权等本地保护仍依赖独立离线测试。
- 002：原生结构化文本数组/内容块。没有把 image/document 等媒体输入写成通过。
- 003：实际执行进度之后追加原生输入，保留接受/拒绝及写入含义，不自动 steer。
  没有把一次样本推广为所有执行期调度结果。
- 004：Codex 两个精确动态 fixture 工具，Claude 原生 Read/Write；工具和执行终态分别保留。
  Codex 动态工具不是原生审批。
- 005：原始原生 JSON 和顺序保留。未知字段/重复事件的主动注入仍是清楚标识的合成测试。
- 006/007：递归创建总次数、深度、工具策略及后台清理未证明，未启动模型。
- 008：查询仅针对本次新建根；没有既有子记录的正例，不能宣称完成既有子 Agent 发现验收。
- 009：Claude 实际允许一次精确 Write、拒绝一次精确 Write。Codex 只取得初始化材料；
  其原生文件审批 envelope 没有 patch/path，当前工具不执行无法精确约束的批准。
- 010：持有原始请求期间完成控制操作。Codex 使用原生问答请求，不冒充文件审批；
  未证明整个持有区间始终有额外 Receive 事件可读。
- 011：原始原生问答请求与对应答案，保留原权限，不用普通输入替代回答。
- 012/013：原生模型/思考控制回执、后续执行及可用的原生观察分别保留。
  ack 不冒充所有生效时机、当前执行切换或子 Agent 继承证明。
- 014：提交后先控制、再消费 Receive，取得有限样本；不宣称无界压力验证。
- 015：取消本地 Receive 后继续交互。已发送结果未知仍通过确定性的合成屏障验证，
  不用真实调用的偶然时序制造这类证据。
- 016：在实际执行进度后发送 Interrupt，保留终态，再单独 Close；不声称回滚或递归停止。
- 017/018：关闭、排空与后续提交拒绝。没有把正常关闭冒充全部任务/传输故障场景。
- 019：Codex 独立读本次保存的根，取得 metadata、turn/item 页及页尾信息。
  Claude 在新生成存储上完成 list/metadata，但消息查询遇到 `ai-title` 返回明确 unsupported。
  没有放宽 SDK 对未知记录类型的合同来把这次结果改成通过。

## 回放证据

34 份真实协议通过最终命令的严格离线处理：正常/部分路径如实重现；Claude 019
以相同诊断、输入/结果计数和完整 EOF 消费，记作 `expected-failure-reproduced`，
其原始 `failed` 状态不变。4 个无协议的子 Agent 阻塞项不算回放通过。

真实 CLI、账户和凭据不参与回放。认证副本删除后仍能回放。动态工具回放使用新的
临时 fixture 项目；修复前重用 live 的 summary.txt 导致 `file exists`，该失败保留在
私有回放记录中，没有重跑模型、改原始数据或放宽请求匹配。JSON 大整数误匹配的
回归测试先失败后修复；改动后的请求和额外请求均被拒绝。

## 工程验证

已通过完整离线脚本（生成、inventory、依赖边界、sanitizer、全部测试和七个示例）、
新增 Python 配置/权限测试及录制/回放工具的 focused race。21 次交互数据集的
race 二进制回放全部通过，覆盖两端的工具、持有请求与原始问答；没有调用 native/model。
最终全仓库 race（`-race -p=1 -count=1 -timeout=300s ./...`）与 `go vet ./...` 均通过。
新增 Python 配置测试共 5 项通过；文档链接、story inventory 与 `git diff --check` 通过。
原生协议、旧 fixture、harness、go.mod/go.sum 的 diff 为空。首次完整离线运行受执行沙箱禁止回环
socket 影响，不能记为通过；允许本机 socket 的检查仍强制所有真实 agent 开关为 0。
新样本没有发布版本，未运行新发布 SHA 的外部 consumer 或远端六项 CI，也未创建
release/tag、PR、issue 评论或更改仓库设置。

最终离线日志 SHA-256：`8e648a66aca9f10760099b659ec2859e0acdb713f3a0c01edefbb46e72f08405`。
全仓库 race 日志 SHA-256：`c1c3e7cd112099c1506d776da568b6bc9b0364634766209b2851c080c46ee909`。
