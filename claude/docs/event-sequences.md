# 按用户故事理解 Claude Session 事件

固定原生基线为 CLI 2.1.258 / SDK 0.3.258。Open 返回 Session；Submit 与单一
Receive 配合，区分原生 Event、真实请求、SDK Observation。
原生字段与 Raw 不加入 SDK 推断。源码、合成测试和真实录制分别记录。

## 故事 1：我输入一个普通任务，等待答案

Submit 的 Written 只证明完整本地写入。原生 assistant/result 仍是事件；result
不关闭 Session。应用可以继续输入，并自行选择 Close 与排空策略。

## 故事 2：我让它读取文件或调用工具，再等待解释

工具输入、结果、text/thinking、未知块和 partial/complete 快照保持原生数据。
不合并父答案或费用，也不把工具名字当成成功创建子任务的证明。

## 故事 3：我批准、拒绝工具，或回答问题

只有同流真实 can_use_tool 请求携带原回答句柄。应用保留句柄并继续接收，
然后显式 Approve/Deny/Answer。没有默认 deny、回调工作池或 SDK 审批计时器。
问题答案键是完整问题文本，值是原生字符串；原问题与未知输入字段保留。
取消/关闭撤销权限。NotSent 保留仍活权限；未知写入结果不可重试。

## 故事 4：我派前台 Agent，并查看子任务的工作

已知同 Session envelope 直接交付。固定版本 local_agent task/tool 证据可确认
别名；父级未知不阻塞交付。agent_id 与 task_id 不凭同字符串合并。

## 故事 5：我让多个子任务并行，其中一个再派孙任务

按接收顺序交付已确认成员的原生事实，父级由已观察工具所有者或调用者已验证
历史知识补充。环、冲突或深度拒绝只报告知识缺口，不改排原生消息。

## 故事 6：我启动后台 Agent，让父模型先回答

父 result 只描述父输出。继续 Receive 观察子 text/thinking、usage 和任务通知。
不从父结束、重复消息、缺少 run ID 或 run ID 字典序构造子运行生命周期。

## 故事 7：我启动后台命令，前台暂时安静但后台仍在运行

原生 idle、background_tasks_changed 和 task terminal 各自保留。空快照与安静
不能证明成功或删除。Interrupt 的匹配响应与后续原生结果独立。

## 故事 8：我看到交错、无法归属、重复或缺失事件

保留每次原生到达。未知 envelope、身份未确认和不同 Session 原文进入有界
Unattributed 诊断区；报告丢弃/过期缺口，不回填实时流，不产生回答句柄。

## 故事 9：我断线后显式恢复，并保留缺口

普通 Open 的 ResumeID 逐字传给新进程，不重放旧输入；requested 与 observed
身份分开，初始化成功不证明恢复。断线期间的缺失不能声称已补齐。

## 可执行离线故事与后续真实验证

[离线示例](../examples/subagents-offline/main.go) 和 Session/权限/生命周期测试
使用隔离的合成进程。20 个新故事与 12 个旧故事分别运行，见
[共同验收映射](../../docs/validation/stories/session-targets.md)。默认不运行真实
CLI/model，也不采集新录制。Close 停止准入并清理，已接受事实仍可排空；Abort
显式放弃未读 FIFO。

## 有限真实尝试的补充证据

后续[录制样本](record-replay.md)只取得一次 CLI 2.1.258 认证失败样本，未取得成功模型/工具或子流证据。实际 result 的 `subtype=success` 与 `is_error=true` 同时出现，且随后发出原生 idle。因此判断成功还必须检查错误字段；初始化接受、result subtype 和 idle 都不能单独证明模型工作成功。以上九故事的成功路径、工具与子任务交错仍不因该失败样本升级为真实验证通过。
