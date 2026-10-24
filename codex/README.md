# codex-sdk-go

Commands in this native documentation run from `codex/`; see [root validation](../docs/testing.md).

A native Go client for the Codex app-server protocol.

Connections and execution live in the native `codex` package. Events, native models,
optional values and subscription options live in `github.com/zoumo/agent-sdk-go/codex/types`.

**Start with [compatibility and the complete official capability inventory](docs/compatibility.md).**
The protocol target is official **0.162.1** at one exact commit. The
[live coverage summary](docs/compatibility.md#live-validation) records limited real CLI
**0.162.1** stdio/loopback WS and user-input passes. Approvals and interrupt were
untriggered; public WSS/JWT remain uncovered. No supported-version range is claimed.
Those historical runs do not validate the refreshed bindings or managed subagents.
Current-version adaptation uses source inspection and offline tests only.

## Documentation

- [Compatibility, protocol target and live coverage](docs/compatibility.md)
- [Managed subagent API and limitations](docs/subagent-compatibility.md)
- [Event sequences and user stories](docs/event-sequences.md)
- [Testing, offline examples and CI](docs/testing.md)
- [Recorded native events and replay](docs/recorded-events.md)
- [Architecture and official SDK comparison](docs/design/native-sdk-comparison.md)
- [Protocol generation, sources and licensing](docs/protocol/README.md)

## Session model and reasoning settings

Creation uses `SessionOptions.Thread` (`types.ThreadStartParams`). Dynamic
changes use `Session.SetModel(ctx, nativeModelID)` and
`Session.SetThinking(ctx, ThinkingChange{Effort: types.Some[types.ReasoningEffort]("high")})`.
The connection must opt in during initialization with
`Capabilities: types.Some(types.InitializeCapabilities{ExperimentalAPI: types.Some(true)})`.
Without that opt-in, setters return `ErrUnsupportedConfiguration` with `NotSent`.
Empty model/effort selectors return `ErrInvalidInput` with `NotSent`; other native
model identifiers and effort strings go to the server for validation.

`ConfigurationReceipt` preserves the native request and empty settings response;
`NativeAccepted` reports RPC acceptance only. Observe the separate
`types.ThreadSettingsUpdatedNotification` through `Session.Receive` for native
settings facts. Neither request nor ack establishes the currently executing
model. At the pinned version, null/omitted effort both leave the override
unchanged; neither means clear. Changes target the main thread's subsequent
turns, without waiting for idle, changing children explicitly, or substituting
turn/start, steer, resume or interrupt. Submit retains its independent native
turn/start options and target.

Native refusal remains a `CallError` wrapping `RPCError`; possible dispatch
followed by cancellation, timeout or Close remains `OutcomeUnknown` and is never
retried automatically. Validation is synthetic/offline; no real configuration
effect, active-turn switch, or child inheritance is certified.

## Query existing descendants

`Session.ListSubagents(ctx, SubagentQuery{})` reads bounded native pages of stored
descendants of the fixed main identity. Initialize with experimentalApi enabled.
Inspect returned `Pages`, `Coverage` and the error together: partial pages can
accompany a failure. Defaults select five native subAgent source kinds and only
non-archived records; archived records need a separate query. Results do not
change observation membership or prove that an Agent is running or subscribed.
See [pagination, filters and evidence limits](docs/session-discovery.md).

## Local thread example

Go 1.26.9 is pinned with mise. Install the toolchain using your normal mise setup;
then build with `mise exec go@1.26.9 -- go test ./...`.

```go
package main

import (
    "context"
    "fmt"
    "log"

    codex "github.com/zoumo/agent-sdk-go/codex"
    "github.com/zoumo/agent-sdk-go/codex/types"
)

func main() {
    ctx := context.Background()
    client, err := codex.NewStdio(ctx, codex.StdioOptions{
        Executable: "/path/you/selected/codex",
        Args:       []string{"app-server"},
        Initialize: types.InitializeParams{
            ClientInfo: types.ClientInfo{Name: "my-client", Version: "0.1"},
        },
    })
    if err != nil { log.Fatal(err) }
    defer client.Close()

    started, err := client.StartThread(ctx, types.ThreadStartParams{
        Ephemeral: types.Some(true),
    })
    if err != nil { log.Fatal(err) }
    read, err := client.ReadThread(ctx, types.ThreadReadParams{
        ThreadID: started.Thread.ID,
        IncludeTurns: types.Some(true),
    })
    if err != nil { log.Fatal(err) }
    fmt.Println(read.Thread.ID)
}
```

The SDK does not download or discover Codex, log in, look up credentials, launch a
shell, or own ambient stdin/stdout. The explicitly selected executable can itself
read configuration or credentials and perform side effects. `Env == nil` inherits
the parent environment; supply an explicit environment and isolated `CODEX_HOME`
when needed. No executable is launched by importing this package.

The constructor sends `initialize`, awaits its response, then writes `initialized`
before returning. `ServerInfo()` returns the native handshake result. Native
thread, session, turn, and item identifiers are never fabricated.

## Connect to an existing remote app-server

Use the same client methods with an explicitly selected WS/WSS endpoint and a
nonempty caller-supplied bearer token. Supply the token from your application;
the SDK never discovers credentials, signs in, mints a token, or refreshes it.

```go
client, err := codex.NewRemote(ctx, codex.RemoteOptions{
    URL:         "wss://codex.example.com/",
    BearerToken: token, // supplied by your application; never put it in the URL
    Initialize: types.InitializeParams{
        ClientInfo: types.ClientInfo{Name: "my-client", Version: "0.1"},
    },
})
if err != nil { return err }
defer client.Close()

started, err := client.StartThread(ctx, types.ThreadStartParams{
    Ephemeral: types.Some(true),
})
if err != nil { return err }
fmt.Println(started.Thread.ID)
```

`NewRemote` validates its options before dialing, then completes `initialize` and
`initialized` before returning. Pass a deadline on `ctx` to bound startup; the
configured call timeout bounds dialing plus initialization. `Limits` has the same
finite defaults as stdio. Canceling the constructor context after success does not
close the client; call `Close()` explicitly. Each complete WebSocket text message
carries one JSON envelope, with no JSONL delimiter. The transport handles
fragmentation. JSON batches, multiple envelopes in one message, and binary messages
are unsupported; malformed or oversized messages terminate the connection explicitly.

- Authentication uses only `Authorization: Bearer <token>`. URL user information,
  query strings, and fragments are rejected. Redirects are never followed.
  Tokens must have valid bearer-token syntax; whitespace/control characters are
  rejected. No Origin header or environment-selected HTTP proxy is used.
- WSS verifies the certificate and hostname. To trust a private CA, set
  `TLSConfig: &tls.Config{RootCAs: trustedRoots}` using `crypto/tls` and a caller-built
  `*x509.CertPool`. `InsecureSkipVerify` is rejected, and TLS versions below 1.2
  are not enabled.
- Plain `ws://` is allowed for `localhost` and numeric loopback addresses.
  Before sending the bearer header, the connected TCP peer must also be loopback.
  `AllowInsecureWS: true` explicitly permits other hosts, exposing the bearer token
  and protocol traffic to the network. Prefer WSS or a secured loopback tunnel.
- `ErrRemoteTransport` reports sanitized stage/status information, without URL,
  token, response body, or peer-supplied close reason. Only safe underlying causes
  such as context cancellation and EOF are retained. Remote error messages and
  `RPCError.Message`, `Data`, and `Raw` also redact the explicitly supplied bearer
  if reflected by the peer, including supported JSON/URL-encoded echoes.
  Successful results, events, and request payloads remain caller-sensitive data;
  the SDK is not a general logging redactor and cannot control server logs.

There is no automatic reconnect or request replay. After a lost connection,
inspect the operation's dispatch evidence, create a new initialized client, then
explicitly resume the saved native thread ID if appropriate. An unknown outcome
must be reconciled before repeating side-effecting work. Remote `Close` releases
only this connection; it never shuts down or kills the remote service.

The WSS example describes a caller-provided TLS endpoint; source inspection and
synthetic TLS tests do not certify a production TLS proxy, bearer issuer, server
configuration, or live account. See the [canonical evidence](docs/compatibility.md)
and [transport provenance](docs/protocol/README.md). The
[remote example](remote_example_test.go) compiles in tests and is never executed.

## Explicitly resume and detach a native thread

Keep the native thread ID returned by the server. After closing the old client,
construct a new client with `NewStdio` or `NewRemote` for a fresh handshake, then
resume and read that exact ID. The SDK has no saved-session registry or reconnect loop.

```go
resumed, err := client.ResumeThread(ctx, types.ThreadResumeParams{
    ThreadID: nativeThreadID,
    // Omit ServiceTier to preserve the server's override.
    // ServiceTier: types.Null[string](), // explicitly clear the override
})
if err != nil { return err }
read, err := client.ReadThread(ctx, types.ThreadReadParams{
    ThreadID: resumed.Thread.ID,
    IncludeTurns: types.Some(true),
})
if err != nil { return err }
fmt.Println(read.Thread.ID)
detached, err := client.UnsubscribeThread(ctx, types.ThreadUnsubscribeParams{
    ThreadID: resumed.Thread.ID,
})
if err != nil { return err }
fmt.Println(detached.Status)
```

The compilable [resume-thread example](examples/resume-thread/main.go) takes an
explicit executable and an already-known native thread ID. Tests compile it but
do not run it. A real server must still have the thread; the fake-peer tests do
not establish cross-process server persistence or resumability of ephemeral data.

`ResumeThread` sends the supplied ID unchanged and checks `response.thread.id`.
A mismatch returns `ErrProtocol` with `CallError.Dispatch == Responded` and the
matching RPC ID. Missing/expired threads, RPC errors, timeouts, or lost connections
never create a replacement thread or cause automatic replay.

All native resume fields are exposed, including experimental fields. The native
server can select another identity when `history` or `path` overrides `threadId`.
This typed wrapper deliberately requires the returned ID to equal the supplied
ID even in those cases; prefer ID-only resume. It does not undo server-side work
if validation fails. The raw `Call` escape hatch leaves alternative-identity
handling to the caller and does not certify those workflows.

Resume `ServiceTier` distinguishes omission (keep the override), null (clear it),
and a string (set it). `ExcludeTurns` can be omitted, false, or true, but not null.
Other nullable fields preserve wire omission and null without claiming those
states always mean different things to the server. Schema defaults are not
silently inserted by the SDK.

`UnsubscribeThread` returns the native `notLoaded`, `notSubscribed`, or
`unsubscribed` status (and preserves future string values). This is server-side
thread detachment, separate from closing a local `Subscription`. It does not
close local observers, delete the thread, send an interrupt, or establish that an
active turn stopped. Server notifications already in flight may still arrive.
Closing a local subscription never sends `thread/unsubscribe`.

## Native values and extensions

Models live in the public `types` package. `Optional[T]` preserves omission,
explicit null, and present values, including false, zero, and an empty list:

```go
params := types.ThreadStartParams{
    Model: types.Some("caller-selected-model"),
    ServiceTier: types.Null[string](),
    Ephemeral: types.Some(false),
}
```

Required members are checked on decoding. Generated objects retain unrecognized
members in `UnknownFields`; unions expose typed variants and an `Unknown` raw
fallback. Raw JSON values never pass through float64. Unknown wire structure and
values are preserved, although re-encoding may normalize whitespace/key order.
These loss-preserving bindings are not strict JSON Schema validators: native
minimum/maximum and `additionalProperties` constraints are not all enforced, and
unknown enum, union, and extension values can remain representable. The server
remains responsible for accepting a request. See
[generation provenance](docs/protocol/README.md) for the exact native fields.

`Call(ctx, method, params, result)` is a raw extension escape hatch with the same
bounds, exact correlation, and no-replay behavior. `nil` params omits the member;
`json.RawMessage("null")` sends explicit null. A nil result discards the response.
Calling an unwrapped method does not make it an officially supported SDK workflow.
Incoming server requests have a separate bounded pull queue. Unknown methods
remain caller-managed raw extensions; their reachability is not workflow certification.

## Observe and explicitly interrupt a turn

`StartTurn(ctx, types.TurnStartParams)` and `InterruptTurn(ctx,
types.TurnInterruptParams)` return native response types. Call `Subscribe(ctx, rootThreadID, types.SubscriptionOptions{})`
first: a turn can emit events, including completion, before its start response.
The response's turn ID remains authoritative for that response. The SDK does not
merge, accumulate, or reinterpret native thread/turn/item IDs. The explicit root
selects proven descendants; unrelated identities may remain unresolved.

The compilable [observe-turn example](examples/observe-turn/main.go) registers
before starting work, prints native thread/turn/item IDs, and checks stream
loss. Root completion is printed while observation continues until the caller
context ends; applications must choose their own observation stop condition.
It is compiled by tests, never run against a model. Running it yourself may
use an authenticated account and consume model resources.

A subscription is local observation, active when returned; it does not replay
notifications received before registration. `Events()` supplies `types.Event` directly,
with `Method`, raw JSON `Params`, optional typed `Data`, `DecodeError`, and the
exact notification envelope in `Raw`. SDK convenience events use separate concrete
`Data` types: `*types.SubagentCreated`, `*types.SubagentAssociated`, and
`*types.ObservationState`; their Method is empty and Params/Raw are nil. Switch
on Data's concrete type. Native structs and JSON contain no SDK origin flags.
Absent parameters remain nil; explicit null remains raw `null`. Each subscriber
independently owns its raw and typed data.
Typed payloads currently cover `turn/started`, `turn/completed`, `item/started`,
`item/completed`, `item/agentMessage/delta`, `error`, and `serverRequest/resolved`.
Other methods remain raw.
Malformed supported payloads remain observable with `*DecodeError`; an invalid
JSON envelope instead terminates the connection.

`Done()` closes immediately on subscription termination, independently of event
consumption. `Close()` and subscription context cancellation stop local observation;
accepted events remain readable in FIFO order, and `Events()` closes after they
drain. After `Done`, inspect `Err()`:

- nil: explicit subscription or normal client close
- `context.Canceled` / `context.DeadlineExceeded`: local subscription cancellation
- Another error: the underlying terminal connection failure or observation limit

Unread delivery grows dynamically in memory and does not block connection readers,
RPC responses, or server-request routing. `EventQueue` is deprecated and ignored;
negative values remain invalid. This backlog has no item capacity and can exhaust
machine memory if consumers do not keep up. It is not durable across crashes or OOM.
The public channel is an unbuffered bridge with at most one additional in-flight
event, so `len(Events())` does not measure backlog.

`Close()` returns without waiting for consumers. Continue draining `Events()`, or
call `Abort()` to discard unread events and release the delivery goroutine when
abandoning observation. `Abort()` also works after `Close()` and preserves the first
terminal cause. A receive racing `Abort()` may receive the in-flight event. Neither
operation sends `thread/unsubscribe` or `turn/interrupt` or changes answer handles.

To request execution interruption explicitly, pass the exact native identities:

```go
_, err := client.InterruptTurn(ctx, types.TurnInterruptParams{
    ThreadID: threadID,
    TurnID: turnID,
})
```

The acknowledgement is only an RPC response. It neither creates a completion
event nor proves that execution stopped. There is no high-level turn runner,
implicit interruption, or final-text aggregation.

## Answer server requests explicitly

`Requests()` is a single bounded pull queue, available immediately after startup.
Each immutable `Request` exposes `ID()`, `Method()`, `Params()`, `Data()`, and
`Handle()`. Integer `1` and string `"1"` remain different identities. `Params()`
and `Data()` return fresh copies, so UI mutations cannot change an answer target.
`Data()` decodes native command/file/permissions approvals, tool user input, and
legacy command/patch approvals. Unknown methods return `nil, nil`; malformed known
payloads return `*DecodeError` without losing their original raw bytes.

Read requests concurrently with calls that might need an answer. Obtain the
result from your application or user; the SDK supplies no default decision:

```go
request, ok := <-client.Requests()
if !ok { return client.Err() }
fmt.Println(request.ID(), request.Method(), string(request.Params()))

// For example, read a caller-selected native JSON result from the terminal.
var result json.RawMessage
if err := json.NewDecoder(os.Stdin).Decode(&result); err != nil { return err }
if err := request.Handle().Answer(ctx, result); err != nil { return err }
```

The [compiled example](requests_example_test.go) follows this pattern and is never
executed by tests. Native response models such as
`types.CommandExecutionRequestApprovalResponse` and
`types.ToolRequestUserInputResponse` can be passed to `Answer` instead of raw
JSON. `Reject(ctx, &RPCError{...})` sends exactly the caller-selected protocol error.
`RPCError.Raw` is inbound diagnostic data and is not sent.

New code receives `ReceivedRequest` from `Session.Receive`, with an
`IncomingRequest` carrying complete envelope `Raw()`, `Params()`, `Data()` and
`Context()`. `Approve(ctx, ApprovalOptions)` / `Deny(ctx, ApprovalOptions)` choose
native decisions; typed options preserve scope, execpolicy/network amendments
and extensions. Permissions approval requires an explicit grant. `Answer` accepts
the native `ToolRequestUserInputResponse`. `Handle().Reject` remains an explicit
RPC error operation, separate from user denial.

A request enters exactly one consumption surface. A direct main owner takes
precedence; otherwise a unique confirmed member range routes the original handle.
Ambiguous/unknown requests remain in the connection registry and bounded Raw-only
diagnostics; diagnostic eviction/expiry never decides or invalidates them. Later
membership does not replay old diagnostic material. Session Close revokes only
its assigned requests. Closing an owner ends its answer wait without canceling
an already started shared-connection write; actual I/O faults and write deadlines
still fail that connection. Legacy Requests is temporary expand-contract support.

Raw/Params snapshots live with the received Request/FIFO. The authority registry
retains identity fingerprints and request status, without retaining completed
command/diff/question payloads. Keeping an original Request keeps its snapshot
readable after answering or Close; releasing consumed snapshots releases their
payload independently of replay protection.

A handle is opaque, bound to its original connection, and shared by copies.
Only one answer or cancellation wins. `Done()` signals termination and `Err()`
reports answered, locally canceled, server-resolved, or connection failure.
A successful `Answer` means its frame was written; it is not a peer acknowledgment
or proof of execution. Competing/duplicate answers and stale handles fail locally.
Confirmed `NotSent` failures, including cancellation before writing, leave a still
live native handle usable. Validation, pre-canceled context, oversized frames and
queue-admission failures likewise leave it usable. Partial or
ambiguous writes return `AnswerError` with `OutcomeUnknown` and cannot be replayed.

`Cancel()` abandons a prompt locally. It sends no response or cancellation and
does not stop server work. There is no SDK approval countdown; `RequestTimeout`
is deprecated and ignored. The native
`serverRequest/resolved` notification invalidates an unanswered matching handle
using both its exact `requestId` and native thread identity. It can follow an answer
or interruption and does not establish success. Native resolution invalidates the original capability immediately and prevents
a queued, unwritten answer. Once writing starts its outcome may be unknown; no
response is retracted or retried.
There is no invented generic cancellation method.

Slow consumers cannot block ordinary responses or notifications. Queue or active
request exhaustion terminates the connection explicitly with
`ErrRequestOverflow` (also `ErrLimit`), invalidating handles and pending calls;
requests are never silently discarded or automatically denied. Buffered requests
may already be resolved or closed when read. Check handle status before showing a
prompt and use `Done()` to dismiss stale UI. Closing the client closes the request
queue and invalidates every pending handle without waiting for consumer code.

## Cancellation and errors

A context timeout cancels local waiting. It does not stop server execution or send
an invented cancellation notification. `CallError` unwraps its cause and carries:

- `NotSent`: no write began; local admission, encoding, or cancellation failed.
- `OutcomeUnknown`: a write may have begun, without a matching definitive reply.
- `Responded`: the matching server error or an invalid typed result was received.

Use `errors.Is` for context/transport causes and `errors.As` for `*RPCError`.
`RPCError` retains its code, message, and raw optional data, with a remote-security
exception: a reflected bearer token is replaced in diagnostic messages, data,
and raw error JSON. Unaffected raw errors stay byte-for-byte intact; redacted JSON
replaces only affected lexical values, including reflected keys/numbers;
unaffected bytes, duplicate members, and numeric lexemes remain intact. Native error codes and RPC
correlation remain unchanged. Unsafe reflected custom error causes are scrubbed
rather than exposed through `Unwrap`; public SDK sentinel classification remains.
A server error is not proof that no side effect occurred. Nothing is retried automatically. A canceled
blocked write terminates the connection to release the writer; other pending calls
then fail with their own dispatch evidence. Late responses cannot satisfy new calls.

## Bounds and connection ownership

`DefaultLimits()` supplies per-connection resource and operation defaults. Each zero field in
`Limits` selects its default; negative values are rejected:

- Incoming and outgoing JSON payload: 8 MiB, excluding stdio newline or WS framing
- Queued outbound frames: 32; pending calls: 128
- Active local subscriptions: 32; accepted event backlog grows with memory
- Active reverse requests: 128; queued reverse requests: 32
- Unanswered handle lifetime: native resolution/termination only; no SDK deadline
- Request identity registry: 4096 distinct tagged ID/method/params identities per
  connection (RequestIdentities). Terminal entries are retained; exhaustion fails
  with ErrRequestOverflow rather than restoring answer permission. Different
  method/params may reuse a terminal ID. This is an SDK replay-safety boundary,
  not a claim that the native server guarantees globally unique IDs.
- Diagnostic tail: 64 KiB; all stderr content is replaced with redaction markers
- Every call, including initialization: at most 30 seconds unless configured
- A blocked write: 10 seconds
- Shutdown: 2 seconds; stdio splits this between graceful EOF and child reaping

Earlier caller deadlines win. Resource admission fails with `ErrLimit` before
sending where possible. An oversized outgoing frame is not sent; malformed or
oversized incoming frames terminate the connection with an explicit cause.
Bounded diagnostic counters record unmatched responses and notifications without
retaining diagnostic payloads. Subscription storage scales with unread events,
frame size, and subscriber count, including independent typed payload copies; use
smaller limits for large events or many observers. SDK decoding runs on the reader,
but caller callbacks never do. Stderr is drained independently of the reader.

`Close()` is concurrent-safe and idempotent. For stdio it closes stdin, waits for
graceful exit, then kills and reaps only the owned child. For a remote client it
closes only its WebSocket connection. Both settle pending calls, subscriptions,
and reverse-request handles; stdio also closes all owned pipe ends. A child exit is detected even if an inherited output pipe
remains open. It does not kill descendants, process groups, or an existing daemon.
`Done()` signals connection termination; `Err()` gives the terminal cause. A rare
OS failure to reap before the shutdown deadline returns `ErrShutdownTimeout`;
the background `Wait` still owns reaping. User-defined JSON marshal/unmarshal
implementations run in the caller and must cooperate; the SDK cannot interrupt
arbitrary Go code.

## Development and testing

```sh
mise exec go@1.26.9 -- go test -v -count=1 ./...
mise exec go@1.26.9 -- go test -race -v -count=1 ./...
mise exec go@1.26.9 -- go vet ./...
python3 docs/protocol/verify_inventory.py
```

Default tests use synthetic helper processes, loopback WS peers, and local WSS
peers with synthetic certificates and dummy tokens. The opt-in [`tests/e2e`](tests/e2e)
harnesses use a real CLI/account/model only when explicitly enabled. Keep
`CODEX_E2E_STDIO`, `CODEX_E2E_INTERACTIONS`, and `CODEX_E2E_WEBSOCKET`
unset or `0` for offline checks. Each requires the value `1` plus an explicit
`CODEX_E2E_CLI` executable to opt in. `RUN_REAL_CODEX_TESTS` is not a harness
switch. The completed historical validation budget is 9/9; it grants no reruns.
Real E2E tests refuse CI. The WebSocket harness uses Go networking and the
server process handle; Unix process groups remain required for bounded cleanup.
It does not inspect process-tree listener ownership or additional listeners.

The [CI workflow](../.github/workflows/ci.yml) runs uncached normal and race suites,
vet, module verification, inventory, changed-whitespace checks, and reproducible
schema generation on Ubuntu 24.04 and macOS 15, using Go 1.26.9 and Python 3.13.13.
All three E2E switches are explicitly `0`; verbose output and job summaries
identify real scenarios as **SKIPPED**, not live passes. Pushes to `main`, PRs,
and manual dispatch run with read-only repository permissions and no service
secrets. See [testing and CI](docs/testing.md) for all offline commands, comparison
bases, generation limits and action maintenance.
[Official testing patterns](docs/protocol/testing-patterns.md) distinguishes
app-server harnesses from the TypeScript SDK's `codex exec` transport.

Licensed under [Apache-2.0](LICENSE). See [NOTICE](NOTICE) for official protocol
attribution and [dependency provenance](docs/protocol/README.md#licensing) for the
WebSocket transport library. This is an independent SDK, not an OpenAI-maintained
Go SDK.

### Observe a root and its live descendants

Read [事件时序（当前 0.162.1）](docs/event-sequences.md) for native/model/RPC
boundaries, interleaving, lifecycle and gap examples.

See the [per-capability matrix](docs/subagent-compatibility.md) and the
[runnable offline example](examples/observe-subagents-offline/README.md), which starts
only its own synthetic peer. Subagent E3/E4 tests were not run; ACP subagent
proposals remain drafts and are not a stable protocol implemented by this SDK.

Register before starting root work:

```go
stream, err := client.Subscribe(ctx, rootThreadID, types.SubscriptionOptions{})
if err != nil {
    return err
}
defer stream.Abort()
// Now start root work through the usual StartTurn API.
for event := range stream.Events() {
    switch data := event.Data.(type) {
    case *types.AgentMessageDeltaNotification:
        fmt.Print(data.Delta)
    case *types.SubagentCreated:
        fmt.Println(data.ThreadID, data.ParentThreadID, data.Evidence)
    case *types.SubagentAssociated:
        fmt.Println(data.ThreadID, data.ParentThreadID)
    case *types.ObservationState:
        fmt.Println(data.State, data.Reason)
    }
    // Unknown methods and fields remain inspectable in event.Params and event.Raw.
}
return stream.Err()
```

`SubagentCreated` requires a live successful `spawnAgent` completed item with
receiver identities, or a live decoded `item/completed` SubAgentActivity with
`kind=started` at the pinned 0.162.1 source. V2 uses notification `threadId` as
parent and `agentThreadId` as child, without requiring `thread/started`.
`types.SubagentEvidence.Activity` retains the actual native item; it is not collab evidence.
Other activity kinds and `item/started` prove no relation or creation.
Other successful collab calls can establish
`SubagentAssociated`; association alone does not confirm creation. A later spawn
confirmation refers to the same native thread ID. Relations precede child notifications;
all native notifications that establish relations are also delivered. Repeated
relations do not duplicate creation; identical text deltas remain separate events.
Native notifications retain their thread/turn/item IDs. Convenience events carry
the child ID, direct parent and original supporting evidence. Unknown fields, methods, explicit null and absent
parameters remain available. Each observer owns its buffers.

Relations from completed collab items, V2 completed Started activity, native `Thread.parentThreadId`, and
`source.subAgent.thread_spawn.parent_thread_id` recursively establish descendants.
Ancestors are announced before descendants and their buffered native events.
`agentPath` never proves a parent. First confirmed parent wins; conflicting or
cyclic evidence produces an incomplete observation diagnostic with the rejected
evidence, without rebinding. `PreviousEvidence` retains the accepted edge on a
conflict, and `types.SubagentEvidence.ParentThreadID` identifies each proposed parent.
A later live spawn can independently confirm Created
for the same native thread after Associated, once per entity across turns.

Events whose source is not yet related enter bounded pending storage. After a
count, byte or age limit they follow an `unresolved-source` incomplete diagnostic,
whose UnresolvedThreadID identifies the source. This can include unrelated roots;
only relationship evidence establishes descendant membership. Later association does not
replay already delivered native records. Per-source pending order and exact Raw
are retained. The initial observation remains incomplete because native attach is
best-effort and runtime coverage is unverified, including unknown server versions.

For explicit reconstruction on a new Client, pass caller-owned native Thread
snapshots in `types.SubscriptionOptions.History` (`[]json.RawMessage`, each a Thread object,
not a thread/read response envelope). Only parent/source links and successful
completed collab items and V2 Started activities in `turns[].items` establish
relations. Seeded relations
have `Evidence.Provenance="history"` and never emit Created, even if
later live evidence repeats their spawn. History does not replay native events,
seed occurrence deduplication, or establish current execution state. A new live
child can still emit Created. Seed count is bounded by MaxNodes and total snapshot
bytes by PendingBytes; invalid snapshot IDs fail admission. Snapshots are not
retained. Graph/depth limits also apply during seeding.

A seed emits `ObservationState(incomplete, history-recovery-gap)`: new-thread
auto-attach does not prove attachment to old threads on a cold connection. The SDK
never issues read/list/resume/start requests to fill this gap. Silence, lagged
attach and missing history cannot establish completeness. Explicitly reconnecting
requires a new Client and Subscribe, with an empty occurrence cache; identical native IDs can be delivered again. There is no replay
cursor or cross-connection exactly-once guarantee.

Native deduplication is deliberately narrow: successfully decoded known-type `item/completed`
notifications use thread, turn, method and item identity within this connection. The bounded FIFO identity cache can forget older entries. Other native
events, including repeated identical deltas and started/complete snapshots, are
preserved. An item ID alone never identifies an individual delta.

Notifications without an identifiable source are preserved and do not establish
membership. Child text and usage are never combined with parent answers or usage.

At pinned Codex 0.162.1, native server source automatically attempts to attach
initialized connections to newly created threads, best-effort. The SDK uses
notifications already on its connection and sends **zero** child resume/start/
subscribe RPCs or additional connections. This is source evidence and offline
fake-peer validation, not real-server subagent validation. Older/unknown runtimes
remain unverified; the SDK does not probe them with execution RPCs. See
[compatibility and evidence](docs/compatibility.md#managed-descendant-observation).

`types.SubscriptionOptions.EventQueue` and `Limits.EventQueue` are deprecated,
validated as nonnegative, and ignored without preallocating their configured size.
`MaxNodes` defaults to 4096 including the root and unresolved relation nodes;
`MaxDepth` defaults to 32 edges. `PendingEvents`, `PendingBytes`, `PendingAge` and
`DedupEntries` default to 128, 4 MiB (stored envelope plus params), 5 seconds and
8192. Zero selects defaults; negative values are invalid. The age limit only bounds
association waiting. Frame, reverse-request, graph, pending-association, and dedup
limits remain separate from dynamic delivery storage.

Each live notification and its derived associations/resolved pending records enter
the FIFO atomically as one contiguous batch, as do pending-expiry diagnostics and
unresolved native records. A single connection reader preserves notification order;
concurrent producers are ordered at admission, without inventing global protocol
order. `Done`/`Err` never wait for queue space or consumer drain. Graph/depth
exhaustion still ends observation with `ErrLimit`.
Each subscription uses one shared admission slot. `Close` ends local observation without interrupting provider work. Parent
completion does not close it. Parent and child statuses are independent: parent
idle may coexist with child active; child turn/activity completed or interrupted
does not destroy the agent or prevent later turns. The SDK forwards native states
and identities without synthesizing child completion. While a parent waits in
wait_agent, its turn remains active. At pinned 0.162.1, TurnComplete awaits
note_turn_completed (publishing that thread's idle change when applicable) before
handle_turn_complete enqueues turn/completed on the same outbound queue. Status
is per thread, with no child aggregation. This is not a cross-thread ordering or
success-result-first promise.

Unexpected connection termination preserves queued records, then emits
`ObservationState(disconnected, permanent-gap)`. Done closes immediately, Events
closes after drain, and Err retains the original connection cause. This observation remains incomplete; EOF is not
work success. Close/cancel are idempotent, do not
wait for consumers, preserve accepted records and the first terminal cause, and
release only this local observation. The shared server attachment is never
unsubscribed by observation teardown. Permission and question answers still go
through original `Client.Requests()` handles.
`Subscribe(ctx, rootThreadID, types.SubscriptionOptions{})` is the sole subscription entry.
It always manages relations and descendant routing; there is no observation mode switch.
See the [pinned official SDK comparison](docs/design/native-sdk-comparison.md).


### Recorded event replay

Run `go run ./examples/replay-recorded` without Codex, credentials or network.
The embedded real sanitized S1/S2 transcripts cover two bounded dynamic file
tools and one native user question. Default CI validates and replays them offline.
Native shell approvals, subagent creation and background boundaries were not
observed. See [recorded coverage and explicit real-agent maintenance](docs/recorded-events.md).

## Load an existing session and replay history

```go
session, err := codex.Load(ctx, codex.LoadOptions{
    Client: client,
    HistoryOptions: codex.HistoryOptions{
        ThreadID: id, CWD: absoluteCWD, CodexHome: expectedAbsoluteCodexHome,
    },
})
// Check err, then defer session.Close(). Receive is the only consumption entry.
for {
    received, err := session.Receive(receiveCtx)
    if err != nil {
        break // Handle terminal error or EOF after draining a closed Session.
    }
    switch received.Kind {
    case codex.ReceivedEvent:
        handleEvent(*received.Event) // History and live use the same native type.
    case codex.ReceivedRequest:
        handleRequest(received.Request) // Only actual live requests have authority.
    case codex.ReceivedLoad:
        // Inspect received.Load.Status/Err; success permits Submit/configuration.
    }
}
```

Load returns a Session before asynchronous validation and replay finish. It
requires the actual native resume response's ID and cwd to match before history
admission, then pages full main turns oldest-first. Ordinary ResumeThread does
not perform this SDK replay. LoadState is independent of Session.Done/Err and
consumer drain. Submit and configuration reject pending/failed loads; actual
request answers, explicit Interrupt and Close remain available during replay.

History and live traffic can interleave in the same FIFO, preserving each source's
order without claiming the original cross-source timeline. Successful Load does
not terminate the Session. Canceling its startup context after success does not
stop continued interaction. Close releases only owned attachment references;
closing an explicitly loaded child does not unsubscribe an active ancestor's
confirmed observation. Unknown resume/release outcomes remain fenced and visible.
Raw Call is an explicit extension outside coordinated typed ownership guarantees.
See [Session Load and native attachment evidence](docs/session-load.md). The old
Client.Load/LoadedThread API is transitional compatibility pending removal.

The server must support pagination; unsupported RPC errors are not hidden by a
legacy fallback. Active threads and observed metadata changes fail. External
writers must be quiescent because native paging is not an atomic snapshot.
ListThreadTurns and ListThreadItems also expose native pages. See the
[history contract](../harness/contracts/async-history.md) for limits and evidence.

History uses the normal `types.Event` and notification payloads: one
`item/completed` snapshot per retained item followed by `turn/completed` for a
known terminal turn. Item status is preserved, including failed or interrupted
work; this is not a replay of execution. There are no invented starts, deltas,
answer handles or live subagent-created events. An unknown future turn status
is retained as an event with empty Method and `*types.Turn` Data.

`event.History` is non-nil only for replay. Its Raw is the exact item JSON for
item events and the exact full-turn JSON for terminal/unknown turn events.
Projected events have no received wire envelope in `event.Raw`. Their Params
omit fields not retained by the source, listed in `History.MissingFields`.
Full turns lack item completion timestamps: typed `CompletedAtMs` is zero and
unknown for replay, not a measured timestamp. Do not serialize projected Params
as a captured, complete native notification. Session.Receive delivers history,
live records, real requests and SDK boundaries through one entry point.
