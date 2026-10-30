# Codex Rust client 的审批与并发观察

调查日期：2026-10-30。对照当前录制使用的原生版本 `rust-v0.163.0`（`83086188ae0614bd882818ff1aa23f3194962012`），并检查调查时 upstream HEAD `c9750d7f50052b29f53def02b5cf2160ae49a3d9`。以下是官方源码与现有测试的审阅结论；没有执行 Rust 测试、真实模型调用或新的 012 录制。

## 结论与客户端范围

生产路径使用的 `codex-app-server-client` 没有“必须先返回审批回调，才能继续分发控制响应和事件”的结构性限制。审批以 `AppServerEvent::ServerRequest` 交给调用者；调用者可以保留原请求的答复能力，同时通过独立请求句柄发送控制，并继续读取事件。这个结论在已录制的原生版本中已经成立，不依赖升级到最新源码。[事件和客户端定义](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-client/src/lib.rs#L101-L109)、[公共客户端与请求句柄](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-client/src/lib.rs#L334-L354)、[保留答复句柄](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-client/src/server_request.rs#L12-L63)。

“Rust client”需要区分三个对象：

| 对象 | 用途 | 与本问题的关系 |
|---|---|---|
| `codex-app-server-client` | TUI/exec 使用的 app-server 客户端，含 in-process 与 remote 路径 | 具有独立事件分发和保留答复能力，适合作为候选驱动 |
| `codex-client` | HTTP 请求重试、SSE 与遥测基础设施 | 不处理 app-server 审批，不能据此判断 012 |
| `codex-app-server-test-client` | 调试/测试命令，支持 stdio 与 WebSocket | 使用串行读循环，在读循环内处理服务端请求；不是上面的生产客户端 |

来源：[生产客户端说明](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-client/README.md)、[HTTP 客户端说明](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/codex-client/README.md)、[调试客户端串行分发](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-test-client/src/lib.rs#L1927-L1974)。调试客户端直接接受命令/文件审批，用户输入请求则在处理分支中调用交互询问；它不能作为“所有 Rust 客户端都支持延迟审批”的证明。[请求处理](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-test-client/src/lib.rs#L1999-L2125)。

## 外部连接如何避免堵塞

`RemoteAppServerClient` 在后台任务中用 `tokio::select!` 同时处理客户端命令和 WebSocket 入站消息。发送普通请求时，worker 注册该请求的 oneshot 等待者并写出消息；它不等待服务端响应后才读取下一条消息。响应按 ID 交给等待者；服务端请求和通知送入独立事件队列。服务端审批入队后，worker 回到读取循环，不调用需要先返回审批决定的应用回调。[worker 与分发](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-client/src/remote.rs#L253-L422)。

事件队列使用 `unbounded_channel`，投递事件不会等待应用消费。调用者通过 `next_event(&mut self)` 消费事件，通过可克隆的 `request_handle()` 发控制。先取得请求句柄，再持有 `next_event` 的可变借用，就不会因借用整个 client 而无法并发发送控制。[公共接口](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-client/src/remote.rs#L530-L597)、[请求句柄](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-client/src/remote.rs#L681-L722)、[事件投递](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-client/src/remote.rs#L996-L1006)。

in-process 路径也采用独立 worker 与无界消费队列。源码明确解释：有界消费队列曾使未读取的事件阻塞前台请求响应；现在请求等待通过独立任务完成，worker 继续处理事件和命令。[实现与注释](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-client/src/lib.rs#L360-L458)。

这不是对任意阻塞的保证。远端 worker 仍会等待 WebSocket 写入，传输背压可能阻塞；无界事件队列需要调用者及时消费以控制内存。如果应用自己拿整客户端互斥锁等待审批，仍可能重新引入应用层阻塞。

## 保留原审批的公共接口

统一 `AppServerClient` 可包装 remote 客户端，并公开 `request_handle`、`next_event` 和 `resolve_server_request`。`AppServerRequestHandle::server_request_reply_handle(request_id)` 创建保留答复句柄。该句柄绑定原连接及请求 ID；克隆共享单次提交状态；丢弃不发送答复；原连接关闭后不会换连接或自动重试。[统一接口](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-client/src/lib.rs#L768-L833)、[答复能力实现](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-client/src/server_request.rs#L12-L63)。

下列仅示意调用顺序，未编译；省略类型构造、事件过滤和错误处理：

```rust
let mut client = AppServerClient::Remote(remote);
let controls = client.request_handle();
let request = receive_original_approval(&mut client).await?;
let reply = controls.server_request_reply_handle(request.id().clone());

// 原审批仍未答复，worker 可独立接收控制响应和通知。
controls.request_typed::<ThreadSetNameResponse>(rename_request).await?;
receive_matching_name_event(&mut client).await?;

reply.resolve(original_approval_response).await?;
```

TUI 本身也采用这个模型：`BufferedServerRequest` 同时保存 `ServerRequest` 和 `AppServerReplyHandle`，在事件进入时绑定原连接；pending registry 存储不同类型的未决请求。[TUI 使用方式](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/tui/src/app/app_server_requests.rs#L60-L81)。

句柄的单次提交状态在写出前取得，因此失败可能是结果未知，不能自动重试。这个原语也不承诺对同一 ID 独立创建的多个句柄统一去重，或者自动验证服务端撤回；驱动仍须保留并关联原审批及其生命周期。[claim 与 resolve 顺序](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-client/src/server_request.rs#L27-L63)。

## 原生控制与现有测试证据

`thread/name/set` 成功路径先发送原请求的响应，再发送 `ThreadNameUpdated`。官方 WebSocket 测试验证同一线程名称变更的响应和通知，并验证另一连接收到广播。它适合作为 012 的控制/事件候选；该测试本身没有持有审批，不能把它称为完整 012 已通过。[原生处理](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server/src/request_processors/thread_processor.rs#L671-L690)、[WebSocket 测试](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server/tests/suite/v2/thread_name_websocket.rs#L32-L95)。

官方源码还包含以下相关测试，本次只审阅，未执行：

- 未读通知不阻塞 in-process 请求：容量设为 1，积累通知后仍能完成控制请求，随后按序读取通知。[测试](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-client/src/lib.rs#L1320-L1396)
- remote 服务端请求可以经公共接口完成响应。[测试](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-client/src/lib.rs#L1893-L1947)
- 保留的 reply handle 指向原连接，克隆最多提交一次，旧连接不重连。[in-process 测试](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-client/src/server_request_tests.rs)、[remote 测试](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-client/src/tests/reply_tests.rs)

这些证据支持接口可行性。完整验收仍要验证：同一个原审批未答复期间，驱动确实收到控制成功响应与匹配的会话事件；之后只回答该原审批一次，任务完成，连接正常关闭，严格回放和 Go SDK 仿真消费通过。

## 接入成本和版本边界

remote 公开 endpoint 是 WebSocket 或 Unix socket 上的 WebSocket，没有 stdio endpoint。[endpoint 定义](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-client/src/remote.rs#L65-L92)。因此它不能直接替换现有 stdio 驱动。保持现有录制协议的一条可行路线是：Rust remote 客户端连接本机透明 WS 服务端桥，桥把 JSON-RPC 帧送入现有 Go record，再连接真实 CLI stdio；离线时只把 record/CLI 替换为 Go mock。桥只转换传输边界，不处理审批或修改消息内容；不需要另起原生 WebSocket 服务再做第二层桥。这是待实现方案，尚未完成录制验证。

该 crate 是官方仓库内的 workspace crate，依赖 app-server、core、protocol、config 等内部 crate；README 将其定位为 TUI/exec 共享客户端。这些事实不能推出稳定的对外 Rust SDK 承诺。此次 crates.io 查询返回 HTTP 403，未确认发布状态。接入时应固定官方源码 commit 和锁文件，记录构建/驱动身份，并评估整个依赖图的构建成本。[Cargo 配置](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-client/Cargo.toml#L1-L34)、[定位](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-client/README.md#L1-L16)。

调查时最新 HEAD 的 remote worker 和保留答复方式仍保留上述结构；`server_request.rs` 与固定版本逐字相同。最新版本还有连接能力、传输实现等变化，不建议仅为解决本问题升级 CLI。[最新 remote 源码](https://github.com/openai/codex/blob/c9750d7f50052b29f53def02b5cf2160ae49a3d9/codex-rs/app-server-client/src/remote.rs)、[最新答复句柄](https://github.com/openai/codex/blob/c9750d7f50052b29f53def02b5cf2160ae49a3d9/codex-rs/app-server-client/src/server_request.rs)。

本次保留了两版官方源码快照、差异和哈希；私有证据目录为 `/private/var/folders/v7/hcm12_5x49lf7szbrsz4p_p80000gp/T/codex-rust-approval-research-vtc009o0/rust`。原始录制证据未改动。没有宣称 Rust 构建、012 实录、严格回放或 Go SDK 仿真已通过。

## 适配录制流程的评估

012 的业务验收目标是：原审批保持未答时，同一会话仍能取得控制回执和观察事件，最后沿原请求回答并完成原执行。它验证本仓库 Go SDK 的独立接收和回答能力，不要求复制某个官方 SDK 的同步回调 API。[验收目标](../../harness/sdk/validation-cases.md)、[Session 合同](../../harness/modules/native-session/design.md)

这与 001 的修正不同：001 的 idle 必须晚于 completed 是附加的错误顺序假设；012 的“审批未答期间”是需要验证的行为本身。

| 调整方式 | 可行性与验收影响 |
|---|---|
| Python 回调里等待更久、改用线程发送控制 | 可以保持未答或发送请求，但控制回执与新事件仍由被占用的读线程分发；不能完成 012。 |
| Python 先回答审批，再等待控制回执和事件 | 符合同步 SDK 的串行使用方式；可以成为另一个流程，但不证明未答期间可观察与控制，不能替代 012。 |
| 在审批前发送控制，持有时读取旧队列事件 | 可能读到缓存，但不能证明持有期间的新交互能力。原始帧与 SDK 消费时机都须区分。 |
| 改用官方仓库的 Rust app-server-client 驱动 | 可以使用原请求句柄、独立命令通道和事件接收循环，保留 012 目标。需接入传输与依赖冻结，仍须实际验证。 |
| Python 只生产原生协议样本，Go SDK 独立验证持有期间的消费 | 是有条件的架构选择。原生 raw 必须真实包含未答期间的控制应答与事件，Go 必须在回答前实际消费它们；Python 自身只能声明稍后消费。必须明确改变录制报告的证据边界，不能直接把旧 held-event 断言挪到答案之后算通过。 |

最后一种方案需要解决可重复调度：若审批回调仍等待同客户端的控制结果，会继续死锁；若改成固定等待时间，不能保证服务端在期限内完成控制。只有实际 raw 满足条件才构成样本，否则必须未触发。也不能把原始录制文件的内容当成回放期间新观察到的事件。将录制器或旁路观察者加入协调还会改变驱动的证据来源，因此它并非只调整步骤顺序即可完成的小修复。

## 当前决策：保留 Python，暂缓 Codex 012 录制

暂不增加 Rust 依赖。Python 的同步审批回调无法通过延长等待、并发发送控制或调整消费顺序，满足原审批未答期间收到控制回执和新事件的要求。先回答再消费仅验证串行使用；旁路协调会改变证据来源，本轮不引入。

Codex 012 保持 `planned`，明确为低优先级、暂缓录制；既有可用性门禁拒绝仅选择该 case 的 prepare，并在批量流程中报告缺口。不是禁用整个 SDK-US-003，也不改变 Claude 012。保留共同验收、Go SDK 持有审批的合成专项测试和旧录制证据；不把暂缓当作通过。待 Python SDK 公开接口支持延后答复及持续分发后再启用并重新完成三层验证。

## Rust 备选方向（暂不实施）

若以后重新考虑 Rust，可为 Codex 012 增加一个使用官方仓库 Rust `app-server-client` 的驱动，其他已通过路径继续使用原驱动。适配语言、原生审批类型与非执行控制，不改变未答期间观察和控制的验收目标。

1. 固定与 CLI 0.163.0 对应的上游提交、Cargo.lock、构建产物哈希，准确标注“官方仓库 Rust 客户端”，不沿用 Python 包版本作为来源身份。
2. 复用已验证的 Codex 原生命令审批触发方式，避免把 Claude 的 Read 回调形状强加给 Codex。收到实际审批后保留原 request ID 和答复句柄，继续事件循环。
3. 同一连接、同一 thread 发出 `thread/name/set`；取得关联 RPC 应答和匹配名称的 `thread/name/updated`。这是非模型执行控制，不新增 turn。源代码和已有原生测试支持该控制与事件，但其在持有审批期间的真实执行仍须验证。
4. 两项证据都得到之后，再回答原审批一次，等待原 thread/turn 的成功终态。断言绑定原始 request ID、控制 RPC ID、thread ID、名称与 raw 次序；不额外要求控制应答和事件之间存在通用固定顺序。
5. 新 prepare、真实 CLI 录制、同一 Rust 驱动严格回放、Go 公共 Session 仿真分别报告；模型保持 `gpt-6.1-terra`，保留旧证据。

