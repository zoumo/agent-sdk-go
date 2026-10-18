# codex 故事原生映射

共同故事见[根目录故事](../../../docs/stories/index.md)。此处只描述各自原生契约；两个包不相互导入。原生说明见[兼容范围](../compatibility.md)、[子代理证据与限制](../subagent-compatibility.md)、[原生事件序列](../event-sequences.md)。

## US-001

StartTurn 的响应与 turn/completed 分离；原生 turn_contract_test.go。

自动化：`TestCommonStories/US-001/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-002

item/completed 与 turn/completed 分离。

自动化：`TestCommonStories/US-002/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-003

thread/started 的 parentThreadId 建立关联；item/agentMessage/delta 保留 threadId。

自动化：`TestCommonStories/US-003/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-004

父子线程关系自动接入；缺少父级证据的来源保留 unresolved-source 诊断，不猜归属。

自动化：`TestCommonStories/US-004/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-005

根 turn/completed 不关闭 Subscription。

自动化：`TestCommonStories/US-005/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-006

Requests 返回原 Request；仅 Request.Handle().Answer 响应，允许与拒绝各自执行一次，第二次失败；拒绝答案沿原请求 ID 返回。

自动化：`TestCommonStories/US-006/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-007

ToolRequestUserInputParams 使用原生题目 ID q，响应 answers[q] 保留文本。

自动化：`TestCommonStories/US-007/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-008

关闭期间仍待答的句柄终止后不可再答；已取消 context 的回答未入队，原句柄随后仍可回答。写入期间竞态与不确定结果保留在 request_contract_test.go。

自动化：`TestCommonStories/US-008/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-009

Subscription EventQueue 超出终止该观察并报告 ErrSubscriptionOverflow；连接生命周期独立。

自动化：`TestCommonStories/US-009/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-010

未知方法保留原始 JSON；无 ID 未知消息不按文本去重。原生关系去重范围由 tree_current_contract_test.go 等测试约束。

自动化：`TestCommonStories/US-010/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-011

Subscription History 仅关联，不发读取、恢复、输入或回答 RPC；报告 history-recovery-gap。不承诺补齐断线期间事件。

自动化：`TestCommonStories/US-011/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。

## US-012

InterruptTurn 响应不是完成；Subscription.Close 本地结束观察后连接仍可调用。

自动化：`TestCommonStories/US-012/codex`；源码证据为以上公开 API 和本包协议测试，离线证据为实际运行结果，真实运行均未验证。
