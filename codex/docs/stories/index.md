# codex 故事原生映射

当前入口：[19/14 交叉索引、固定原生补充与逐项证据](../../../docs/validation/story-coverage.md)。以下 US 编号为场景，不是 SDK-US 或 recorder S1/S2/S3。

规范与实现证据分开维护：[SDK 共同目标](../../../harness/sdk/user-stories.md)、[场景合同](../../../harness/contracts/stories/index.md)、[实现验收记录](../../../docs/validation/stories/index.md)。

共同故事见[根目录故事](../../../harness/contracts/stories/index.md)。此处只描述各自原生契约；两个包不相互导入。原生说明见[兼容范围](../compatibility.md)、[子代理证据与限制](../subagent-compatibility.md)、[原生事件序列](../event-sequences.md)。

## US-001

StartTurn 的响应与 turn/completed 分离；原生 turn_contract_test.go。

自动化：`TestCommonStories/TestNativeAdapters/US-001/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-002

item/completed 与 turn/completed 分离。

自动化：`TestCommonStories/TestNativeAdapters/US-002/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-003

thread/started 的 parentThreadId 建立关联；item/agentMessage/delta 保留 threadId。

自动化：`TestCommonStories/TestNativeAdapters/US-003/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-004

父子线程关系自动接入；缺少父级证据的来源保留 unresolved-source 诊断，不猜归属。

自动化：`TestCommonStories/TestNativeAdapters/US-004/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-005

根 turn/completed 不关闭 Session.Receive。

自动化：`TestCommonStories/TestNativeAdapters/US-005/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-006

Requests 返回原 Request；仅 Request.Handle().Answer 响应，允许与拒绝各自执行一次，第二次失败；拒绝答案沿原请求 ID 返回。

自动化：`TestCommonStories/TestNativeAdapters/US-006/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-007

ToolRequestUserInputParams 使用原生题目 ID q，响应 answers[q] 保留文本。

自动化：`TestCommonStories/TestNativeAdapters/US-007/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-008

关闭期间仍待答的句柄终止后不可再答；已取消 context 的回答未入队，原句柄随后仍可回答。写入期间竞态与不确定结果保留在 request_contract_test.go。

自动化：`TestCommonStories/TestNativeAdapters/US-008/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-009

Session 接纳的事件进入动态 FIFO，慢消费不会因 EventQueue 容量终止观察或阻塞 RPC；EventQueue 已弃用并忽略。Close 保留事件供排空，放弃消费时用 Abort 释放积压。连接生命周期独立，积压仍受机器内存限制。

自动化：`TestCommonStories/TestNativeAdapters/US-009/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-010

未知数据在确认归属的 Session 或诊断隔离区保留原文；原生重复不按实体/UUID 抑制。Session 与原请求回归见当前验收映射。

自动化：`TestCommonStories/TestNativeAdapters/US-010/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-011

Session.ListSubagents 原生查询只读取关联记录，不恢复、提交、订阅或回答；分页/归档及观察覆盖缺口明确报告。不承诺补齐断线期间事件。

自动化：`TestCommonStories/TestNativeAdapters/US-011/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-012

Session.Interrupt 响应不是完成；Session.Close 结束本 owner 后共享 Client 仍可调用。

自动化：`TestCommonStories/TestNativeAdapters/US-012/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。
