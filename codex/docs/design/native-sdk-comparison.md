# SDK comparison — CLI 0.162.1

Official source pin: [`092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`](https://github.com/openai/codex/tree/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d). Examples describe the pinned source contracts; this does not assert an npm release version or live workflow coverage.

| Operation | Implemented Go | Official TypeScript exec SDK | Official Rust app-server client |
|---|---|---|---|
| Configuration | NewStdio(StdioOptions), NewRemote(RemoteOptions) | new Codex(CodexOptions) | RemoteAppServerClient::connect(RemoteAppServerConnectArgs), or InProcessAppServerClient::start |
| Create / resume | Open; native ResumeThread retains ordinary no-replay semantics | startThread / resumeThread; execution starts lazily during run | request_typed with ClientRequest::ThreadStart / ThreadResume |
| Send | Session.Submit with native Input options | thread.run / runStreamed | request_typed with ClientRequest::TurnStart |
| Receive | Session.Receive → Received with native Event/request/observation/load | AsyncGenerator of exec ThreadEvent, not app-server notifications | next_event → AppServerEvent |
| Approvals / user questions | Receive → actual IncomingRequest → native Handle/Approve/Deny/Answer | No public bidirectional server-request channel; approvalPolicy is configuration | ServerRequest; resolve_server_request / reject_server_request |
| Children | Session membership, optional parent knowledge, explicit coverage | Collab exec items; no recursive subscription or per-child app-server stream API | Native notification stream; native server auto-attach is best-effort, no Go convenience event equivalents |
| Close | Session.Close/Abort; shared attachments release after final observer | No client/thread close method; AbortSignal on run options | shutdown(self); native ThreadUnsubscribe is separate |

## Architecture

The codex connection owns framing, handshake, RPC correlation and the original
request registry. Each Session owns one main identity, one FIFO and bounded
membership/parent knowledge. Native models stay in types. The reader does not
wait for event consumers; actual request handles share Session.Receive with
notifications. No independent subscription/request/history queue remains.
Unknown scope is bounded diagnostic material. Application code owns UI decisions
and reconciles unknown outcomes. No automatic replay/reconnect or aggregate
parent/child result is inferred. See [Session migration](../../../docs/session-migration.md).

## Official TypeScript: actual exec surface

[Codex](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/sdk/typescript/src/codex.ts), [Thread](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/sdk/typescript/src/thread.ts), [options](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/sdk/typescript/src/codexOptions.ts), [exec transport](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/sdk/typescript/src/exec.ts).

```ts
import { Codex } from "@openai/codex-sdk";
const codex = new Codex({ codexPathOverride: "/path/to/codex" });
const thread = codex.startThread({ workingDirectory: "/repo" });
// For an existing thread instead: const thread = codex.resumeThread(savedId);
const abort = new AbortController();
const { events } = await thread.runStreamed("Explain the repository", {
  signal: abort.signal,
});
for await (const event of events) console.log(event);
// abort.abort() cancels execution; it is not a close-session RPC.
```

This is `codex exec --experimental-json`, not an app-server client. startThread creates a local Thread object; its id stays null until the first run yields thread.started. Go StartThread and Rust ThreadStart issue app-server requests and return a native Thread response. Its dotted event names and item union are not interchangeable with slash-named app-server notifications. No native approval/question reply, child subscription, or close example exists in this public surface.

## Official Rust: actual app-server surface

[Client and event API](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-client/src/lib.rs), [remote connection and shutdown](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-client/src/remote.rs), [native request variants](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/common.rs).

Source-level excerpts, not a standalone compiled Cargo application:

```rust
let mut client = RemoteAppServerClient::connect(args).await?;
let response: ThreadStartResponse = client.request_typed(
    ClientRequest::ThreadStart {
        request_id: RequestId::Integer(1),
        params: ThreadStartParams::default(),
    },
).await?;
// Resume uses ClientRequest::ThreadResume with native ThreadResumeParams.
// Send uses ClientRequest::TurnStart with native TurnStartParams.
while let Some(event) = client.next_event().await {
    match event {
        AppServerEvent::ServerNotification(notification) => { /* native data */ }
        AppServerEvent::ServerRequest(request) => { /* retain ID, choose reply */ }
        AppServerEvent::Lagged { skipped } => { /* report gap */ }
        AppServerEvent::Disconnected { message } => break,
    }
}
client.shutdown().await?;
```

The caller supplies `RemoteAppServerConnectArgs` with endpoint, client name/version, experimental capability, form elicitation capability, opt-out methods, and channel capacity. Replies use `resolve_server_request(request_id, serde_json::Value)` or `reject_server_request(request_id, JSONRPCErrorError)`. Approval and question payloads require their respective native response shapes; an arbitrary JSON object is not an approval. The in-process alternative needs resolved Codex runtime configuration and dependencies, not just a path to a CLI executable.

## Go Session signatures

```go
session, err := codex.Open(ctx, codex.SessionOptions{Client: client})
if err != nil { return err }
defer session.Abort()
for {
    received, err := session.Receive(ctx)
    if err != nil { return err }
    if received.Event != nil { fmt.Println(received.Event.Method, string(received.Raw)) }
    if received.Request != nil { /* retain original handle; caller chooses answer */ }
}
```

[Compile-checked native examples](../../examples/apiusage/usage.go) show creation,
ordinary native resume, input and original responses.
Native Codex turns remain visible in Input/Submission/Interrupt selectors.
The TypeScript and Rust excerpts are source-checked references, not compiled by
the Go gate. Raw connection operations remain explicit extensions outside
coordinated Session ownership guarantees.

### Rust connection configuration and native operations

Additional source-level fragments using the actual pinned fields and variants. The caller supplies native resume/turn parameter values; these are not TypeScript ThreadOptions.

```rust
let args = RemoteAppServerConnectArgs {
    endpoint: RemoteAppServerEndpoint::WebSocket {
        websocket_url: endpoint_url,
        auth_token: supplied_token,
    },
    client_name: "example".into(),
    client_version: "1".into(),
    experimental_api: false,
    mcp_server_openai_form_elicitation: false,
    opt_out_notification_methods: vec![],
    channel_capacity: 128,
};
let mut client = RemoteAppServerClient::connect(args).await?;
// Create was shown above. Resume instead:
let resumed: ThreadResumeResponse = client.request_typed(
    ClientRequest::ThreadResume {
        request_id: RequestId::Integer(2),
        params: native_resume_params,
    },
).await?;
let started: TurnStartResponse = client.request_typed(
    ClientRequest::TurnStart {
        request_id: RequestId::Integer(3),
        params: native_turn_params,
    },
).await?;
// In the ServerRequest handler, after a caller selects the native response:
client.resolve_server_request(received_request_id, native_response_json).await?;
// Or reject with the received ID and an actual JSONRPCErrorError:
// client.reject_server_request(received_request_id, rpc_error).await?;
```

`native_response_json` is a serialized native CommandExecutionRequestApprovalResponse, ToolRequestUserInputResponse, or the actual response for the received method. Receive with next_event as shown above; preserve the received ID and service requests concurrently with pending RPCs. Child activity remains native ServerNotification data on that connection. Rust does not emit Go SDK observations. Its shutdown consumes the client and does not assert provider work completion.
