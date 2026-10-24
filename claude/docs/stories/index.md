# claude 故事原生映射

规范与实现证据分开维护：[SDK 共同目标](../../../harness/sdk/user-stories.md)、[场景合同](../../../harness/contracts/stories/index.md)、[实现验收记录](../../../docs/validation/stories/index.md)。

共同故事见[根目录故事](../../../harness/contracts/stories/index.md)。此处只描述各自原生契约；两个包不相互导入。原生说明见[原生 API 比较](../api-comparison.md)、[子代理证据与限制](../subagent-compatibility.md)、[原生事件序列](../event-sequences.md)。

## US-001

Send 表示完整本地写入，Receive 的 result 才是原生任务结果。

自动化：`TestCommonStories/TestNativeAdapters/US-001/claude`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-002

user 工具结果内容与 result 独立。

自动化：`TestCommonStories/TestNativeAdapters/US-002/claude`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-003

根 Agent tool_use 与 task_started 的 tool_use_id 确认父级；原生 agent_id 保留。

自动化：`TestCommonStories/TestNativeAdapters/US-003/claude`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-004

task_started.parent_task_id 确认嵌套；身份不完整时保持诊断与未知来源。Claude task/agent/tool 别名仍各自管理。

自动化：`TestCommonStories/TestNativeAdapters/US-004/claude`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-005

root result 不结束 Receive；后台与子任务继续交付。

自动化：`TestCommonStories/TestNativeAdapters/US-005/claude`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-006

仅 can_use_tool 请求创建 PermissionRequest；原始句柄的 Answer 决定权限。允许与拒绝各自执行一次；第二次回答失败；脱离句柄的 Metadata 仅为数据。CLI 自动批准的操作不会产生此请求，故条件支持。

自动化：`TestCommonStories/TestNativeAdapters/US-006/claude`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-007

AskUserQuestionInput.WithAnswers 使用原始完整题目文本，UpdatedInput 保留原题及未知字段；仅原 PermissionRequest.Answer 可回答。需要真实原生 can_use_tool 请求，故条件支持。

自动化：`TestCommonStories/TestNativeAdapters/US-007/claude`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-008

关闭期间仍待答的句柄终止后不可再答；已取消 context 的预检查不占用句柄，之后原句柄可回答。占用后写入取消与不确定结果继续由 permission_cancellation_test.go 约束。

自动化：`TestCommonStories/TestNativeAdapters/US-008/claude`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-009

慢消费者的事件积压使用动态 FIFO，保持已接纳事件顺序，不阻塞控制回复与权限分发；Limits.EventBuffer 已弃用且忽略。机器内存仍是边界，持续积压可能耗尽内存。帧、控制、权限及关系观察资源限制独立生效。

自动化：`TestCommonStories/TestNativeAdapters/US-009/claude`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-010

未知种类保留 Raw；无 UUID 未知消息不按文本去重。task/run/epoch/UUID 去重继续由 tree_lineage_test.go 覆盖。

自动化：`TestCommonStories/TestNativeAdapters/US-010/claude`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-011

ResumeID 与 Subagents.History 不重发输入/回答；历史只关联且报告 history-gap。不承诺恢复遗漏事件或待答权限。

自动化：`TestCommonStories/TestNativeAdapters/US-011/claude`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-012

Interrupt 仅确认原生 control_response；之后仍可 Receive result。Close 结束客户端传输，不证明模型任务停止。

自动化：`TestCommonStories/TestNativeAdapters/US-012/claude`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。
