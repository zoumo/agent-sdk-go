# SDK comparison — CLI 0.162.1

Official source pin: [`092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`](https://github.com/openai/codex/tree/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d). Go baseline: [`885bf4de79c9bde75c342c807a9e3e45f098187c`](https://github.com/zoumo/codex-sdk-go/tree/885bf4de79c9bde75c342c807a9e3e45f098187c). All seven linked official implementation files were fetched without credentials at that exact pin and matched the local sources byte-for-byte. Examples describe source contracts; no live model calls were made. The source pin identifies the official SDK code; this does not assert an npm release version.

| Operation | Go baseline | Implemented Go | Official TypeScript exec SDK | Official Rust app-server client |
|---|---|---|---|---|
| Configuration | NewStdio(StdioOptions), NewRemote(RemoteOptions) | Same | new Codex(CodexOptions) | RemoteAppServerClient::connect(RemoteAppServerConnectArgs), or InProcessAppServerClient::start |
| Create / resume | StartThread / ResumeThread, native types | Same | startThread / resumeThread; execution starts lazily during run | request_typed with ClientRequest::ThreadStart / ThreadResume |
| Send | StartTurn(native TurnStartParams) | Same | thread.run / runStreamed | request_typed with ClientRequest::TurnStart |
| Receive | Subscribe(ctx, id, TreeOptions), TreeEvent.Native | Subscribe(ctx, id, types.SubscriptionOptions), types.Event directly | AsyncGenerator of exec ThreadEvent, not app-server notifications | next_event → AppServerEvent |
| Approvals / user questions | Requests → Data → Handle().Answer / Reject | Same | No public bidirectional server-request channel; approvalPolicy is configuration | ServerRequest; resolve_server_request / reject_server_request |
| Children | Automatic bounded discovery and forwarding | Internal bookkeeping; typed convenience events in Data | Collab exec items; no recursive subscription or per-child app-server stream API | Native notification stream; native server auto-attach is best-effort, no Go convenience event equivalents |
| Close | Subscription.Close, Client.Close; UnsubscribeThread is separate | Same | No client/thread close method; AbortSignal on run options | shutdown(self); native ThreadUnsubscribe is separate |

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

## Go signatures

Baseline receive:

```go
sub, err := client.Subscribe(ctx, rootID, codex.TreeOptions{})
// handle err
for record := range sub.Events() {
    if record.Native != nil { consume(record.Native.Method, record.Native.Data) }
}
```

Implemented receive:

```go
sub, err := client.Subscribe(ctx, rootID, types.SubscriptionOptions{})
// handle err
for event := range sub.Events() {
    switch data := event.Data.(type) {
    case *types.AgentMessageDeltaNotification:
        fmt.Print(data.Delta)
    case *types.SubagentCreated:
        fmt.Println(data.ThreadID, data.Evidence)
    }
}
```

Go ResumeThread enforces that the returned native thread ID matches the requested ID. Generic Rust ThreadResume can carry native history/path selectors; that broader selector behavior is not certified by the Go strict-ID helper.

The proposal was adopted: there is no second proposed mode or wrapper. Go examples are compiled by `go test ./...`; [complete configuration, create/resume, send/receive, request and close code](../../examples/apiusage/usage.go) is part of that check. The TypeScript and Rust fragments are verified against pinned source, but were not compiled by the Go checks.

### Go configuration, create/resume, send and close

```go
// Package apiusage contains compile-checked examples. Nothing is run by tests.
package apiusage

import (
	"context"
	"fmt"

	codex "github.com/zoumo/codex-sdk-go"
	"github.com/zoumo/codex-sdk-go/types"
)

func Configure(ctx context.Context, executable string) (*codex.Client, error) {
	return codex.NewStdio(ctx, codex.StdioOptions{
		Executable: executable, Args: []string{"app-server"},
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "example", Version: "1"}},
	})
}

// Observe creates a thread when id is empty, or resumes that native ID. The
// caller owns the client and independently services Requests while this runs.
func Observe(ctx context.Context, client *codex.Client, id, prompt string) error {
	if id == "" {
		started, err := client.StartThread(ctx, types.ThreadStartParams{})
		if err != nil {
			return err
		}
		id = started.Thread.ID
	} else {
		resumed, err := client.ResumeThread(ctx, types.ThreadResumeParams{ThreadID: id})
		if err != nil {
			return err
		}
		id = resumed.Thread.ID
	}
	sub, err := client.Subscribe(ctx, id, types.SubscriptionOptions{})
	if err != nil {
		return err
	}
	defer sub.Close()
	_, err = client.StartTurn(ctx, types.TurnStartParams{
		ThreadID: id, Input: []types.UserInput{{Text: types.Ptr(types.TextUserInput{Text: prompt})}},
	})
	if err != nil {
		return err
	}
	for event := range sub.Events() {
		if event.DecodeError != nil {
			return event.DecodeError
		}
		switch data := event.Data.(type) {
		case *types.AgentMessageDeltaNotification:
			fmt.Printf("[%s] %s", data.ThreadID, data.Delta)
		case *types.SubagentCreated:
			fmt.Println("created", data.ThreadID, data.ParentThreadID, data.Evidence)
		case *types.SubagentAssociated:
			fmt.Println("associated", data.ThreadID, data.ParentThreadID)
		case *types.ObservationState:
			fmt.Println(data.State, data.Reason, data.UnresolvedThreadID)
		}
		// Root turn/completed does not imply child or background work has stopped.
	}
	return sub.Err()
}

// Reply uses caller-selected native response shapes and the original handle.
// The UI supplies the decision or answers; this function does not choose them.
func Reply(ctx context.Context, request codex.Request,
	command types.CommandExecutionRequestApprovalResponse,
	answers types.ToolRequestUserInputResponse) error {
	data, err := request.Data()
	if err != nil {
		return err
	}
	switch data.(type) {
	case *types.CommandExecutionRequestApprovalParams:
		return request.Handle().Answer(ctx, command)
	case *types.ToolRequestUserInputParams:
		return request.Handle().Answer(ctx, answers)
	default:
		return request.Handle().Reject(ctx, &codex.RPCError{Code: -32601, Message: "unsupported request"})
	}
}

// Close ends local client ownership. Subscription.Close only ends that local
// observation. UnsubscribeThread is an explicit native detachment RPC.
func Close(client *codex.Client) error { return client.Close() }
```

For interactive handling, run the Requests consumer independently from observation, obtain a caller-selected approval decision or answer map, then call Reply with the original Request. Do not wait for the turn-start RPC to finish before servicing requests; it may depend on a reply. File changes, permissions and legacy approval methods use their respective native response types; the short Reply example handles only command approvals and user questions and explicitly rejects other methods.

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

`native_response_json` is a serialized native CommandExecutionRequestApprovalResponse, ToolRequestUserInputResponse, or the actual response for the received method. Receive with next_event as shown above; preserve the received ID and service requests concurrently with pending RPCs. Child activity remains native ServerNotification data on that connection. Rust does not emit the Go convenience types. Its shutdown consumes the client and does not assert provider work completion.
