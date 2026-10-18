# codex-sdk-go

A native Go client for the Codex app-server protocol.

**Start with [compatibility and the complete official capability inventory](docs/compatibility.md).**
The protocol target is official **0.160.1** at one exact commit. The
[Mac live report](docs/evidence/mac-live-summary.md) records limited real CLI
**0.162.1** stdio/loopback WS and user-input passes. Approvals and interrupt were
untriggered; public WSS/JWT remain uncovered. No supported-version range is claimed.

## Local thread example

Go 1.26.9 is pinned with mise. Install the toolchain using your normal mise setup;
then build with `mise exec go@1.26.9 -- go test ./...`.

```go
package main

import (
    "context"
    "fmt"
    "log"

    codex "github.com/zoumo/codex-sdk-go"
    "github.com/zoumo/codex-sdk-go/protocol"
)

func main() {
    ctx := context.Background()
    client, err := codex.NewStdio(ctx, codex.StdioOptions{
        Executable: "/path/you/selected/codex",
        Args:       []string{"app-server"},
        Initialize: protocol.InitializeParams{
            ClientInfo: protocol.ClientInfo{Name: "my-client", Version: "0.1"},
        },
    })
    if err != nil { log.Fatal(err) }
    defer client.Close()

    started, err := client.StartThread(ctx, protocol.ThreadStartParams{
        Ephemeral: protocol.Some(true),
    })
    if err != nil { log.Fatal(err) }
    read, err := client.ReadThread(ctx, protocol.ThreadReadParams{
        ThreadID: started.Thread.ID,
        IncludeTurns: protocol.Some(true),
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
    Initialize: protocol.InitializeParams{
        ClientInfo: protocol.ClientInfo{Name: "my-client", Version: "0.1"},
    },
})
if err != nil { return err }
defer client.Close()

started, err := client.StartThread(ctx, protocol.ThreadStartParams{
    Ephemeral: protocol.Some(true),
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
resumed, err := client.ResumeThread(ctx, protocol.ThreadResumeParams{
    ThreadID: nativeThreadID,
    // Omit ServiceTier to preserve the server's override.
    // ServiceTier: protocol.Null[string](), // explicitly clear the override
})
if err != nil { return err }
read, err := client.ReadThread(ctx, protocol.ThreadReadParams{
    ThreadID: resumed.Thread.ID,
    IncludeTurns: protocol.Some(true),
})
if err != nil { return err }
fmt.Println(read.Thread.ID)
detached, err := client.UnsubscribeThread(ctx, protocol.ThreadUnsubscribeParams{
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

Models live in the public `protocol` package. `Optional[T]` preserves omission,
explicit null, and present values, including false, zero, and an empty list:

```go
params := protocol.ThreadStartParams{
    Model: protocol.Some("caller-selected-model"),
    ServiceTier: protocol.Null[string](),
    Ephemeral: protocol.Some(false),
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

`StartTurn(ctx, protocol.TurnStartParams)` and `InterruptTurn(ctx,
protocol.TurnInterruptParams)` return native response types. Call `Subscribe(ctx)`
first: a turn can emit events, including completion, before its start response.
The response's turn ID remains authoritative for that response. The SDK does not
filter, merge, accumulate, or reinterpret interleaved native thread/turn/item IDs.

The compilable [observe-turn example](examples/observe-turn/main.go) registers
before starting work, matches both native thread and turn IDs, and checks stream
loss. It is compiled by tests, never run against a model. Running it yourself may
use an authenticated account and consume model resources.

A subscription is local observation, active when returned; it does not replay
notifications received before registration. `Events()` supplies ordered `Event`
values with the original `Method` and raw JSON `Params`, plus optional typed
`Data` and a separate `DecodeError`. Absent parameters remain nil; explicit null
remains raw `null`. Each subscriber independently owns its raw and typed data.
Typed payloads currently cover `turn/started`, `turn/completed`, `item/started`,
`item/completed`, `item/agentMessage/delta`, `error`, and `serverRequest/resolved`.
Other methods remain raw.
Malformed supported payloads remain observable with `*DecodeError`; an invalid
JSON envelope instead terminates the connection.

`Done()` closes immediately on subscription termination, independently of the
bounded event queue. Accepted queued events remain readable after termination.
After `Done`, inspect `Err()`:

- nil: explicit subscription or normal client close
- `context.Canceled` / `context.DeadlineExceeded`: local subscription cancellation
- `*SubscriptionOverflowError` / `ErrSubscriptionOverflow`: the stream lost events
- Another error: the underlying terminal connection failure

A full queue terminates only that subscriber, without waiting for it or preventing
RPC response routing. Check terminal status even when a queued completion is
available; an incomplete stream is not evidence of a complete observation.
`Close()` releases local observation without waiting for a consumer. Subscription
cancellation does not call `thread/unsubscribe` or `turn/interrupt`.

To request execution interruption explicitly, pass the exact native identities:

```go
_, err := client.InterruptTurn(ctx, protocol.TurnInterruptParams{
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
`protocol.CommandExecutionRequestApprovalResponse` and
`protocol.ToolRequestUserInputResponse` can be passed to `Answer` instead of raw
JSON. `Reject(ctx, &RPCError{...})` sends exactly the caller-selected protocol error.
`RPCError.Raw` is inbound diagnostic data and is not sent.

A handle is opaque, bound to its original connection, and shared by copies.
Only one answer or cancellation wins. `Done()` signals termination and `Err()`
reports answered, locally canceled/expired, server-resolved, or connection failure.
A successful `Answer` means its frame was written; it is not a peer acknowledgment
or proof of execution. Competing/duplicate answers and stale handles fail locally.
Once an answer is admitted to the outbound queue, it consumes the handle, including
when a later timeout reports `NotSent`. Validation, pre-canceled context, oversized
frame, and queue-admission failures leave an unanswered handle usable. Partial or
ambiguous writes return `AnswerError` with `OutcomeUnknown` and cannot be replayed.

`Cancel()` and the configured handle expiry abandon a prompt locally. They send
no response or cancellation and do not stop server work. The native
`serverRequest/resolved` notification invalidates an unanswered matching handle
using both its exact `requestId` and native thread identity. It can follow an answer
or interruption and does not establish success. The first admitted answer or
terminal signal wins; resolution does not retract an already admitted response.
There is no invented generic cancellation method.

Slow consumers cannot block ordinary responses or notifications. Queue or active
request exhaustion terminates the connection explicitly with
`ErrRequestOverflow` (also `ErrLimit`), invalidating handles and pending calls;
requests are never silently discarded or automatically denied. Buffered requests
may already be expired/resolved when read. Check handle status before showing a
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

`DefaultLimits()` supplies finite per-connection defaults. Each zero field in
`Limits` selects its default; negative values are rejected:

- Incoming and outgoing JSON payload: 8 MiB, excluding stdio newline or WS framing
- Queued outbound frames: 32; pending calls: 128
- Active local subscriptions: 32; queued events per subscriber: 128
- Active reverse requests: 128; queued reverse requests: 32
- Unanswered handle lifetime: 5 minutes (local expiry, no protocol cancellation)
- Diagnostic tail: 64 KiB; all stderr content is replaced with redaction markers
- Every call, including initialization: at most 30 seconds unless configured
- A blocked write: 10 seconds
- Shutdown: 2 seconds; stdio splits this between graceful EOF and child reaping

Earlier caller deadlines win. Resource admission fails with `ErrLimit` before
sending where possible. An oversized outgoing frame is not sent; malformed or
oversized incoming frames terminate the connection with an explicit cause.
Bounded diagnostic counters record unmatched responses and notifications without
retaining diagnostic payloads. Subscription storage scales with queue capacity,
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

## Development and evidence

```sh
mise exec go@1.26.9 -- go test -v -count=1 ./...
mise exec go@1.26.9 -- go test -race -v -count=1 ./...
mise exec go@1.26.9 -- go vet ./...
python3 docs/protocol/verify_inventory.py
```

Default tests use synthetic helper processes, loopback WS peers, and local WSS
peers with synthetic certificates and dummy tokens. The opt-in `tests/live`
harnesses use a real CLI/account/model only when explicitly enabled. Keep
`RUN_MAC_STDIO_LIVE`, `RUN_MAC_INTERACTIONS_LIVE`, and `RUN_MAC_WEBSOCKET_LIVE`
unset or `0` for offline checks. Each requires the value `1` plus an explicit
`MAC_LIVE_CODEX` executable to opt in. `RUN_REAL_CODEX_TESTS` is not a harness
switch. The completed Mac budget is 9/9; do not rerun live tests within it.

The [CI workflow](.github/workflows/ci.yml) runs uncached normal and race suites,
vet, module verification, inventory, changed-whitespace checks, and reproducible
schema generation on Ubuntu 24.04 and macOS 15, using Go 1.26.9 and Python 3.13.13.
All three live switches are explicitly `0`; verbose output and job summaries
identify real scenarios as **SKIPPED**, not live passes. Pushes to `main`, PRs,
and manual dispatch run with read-only repository permissions and no service
secrets. See [CI policy and validation evidence](docs/evidence/offline-ci.md)
for comparison bases, action maintenance, regeneration limits, and local checks.
Historical live results remain in the [final Mac report](docs/evidence/mac-live-summary.md).
[Ticket 01](docs/evidence/ticket-01.md), [ticket 02](docs/evidence/ticket-02.md),
[ticket 03](docs/evidence/ticket-03.md), [ticket 04](docs/evidence/ticket-04.md), and
[ticket 05 evidence](docs/evidence/ticket-05.md) record scenarios and verification status.
[Official testing patterns](docs/protocol/testing-patterns.md) distinguishes
app-server harnesses from the TypeScript SDK's `codex exec` transport.

Licensed under [Apache-2.0](LICENSE). See [NOTICE](NOTICE) for official protocol
attribution and [dependency provenance](docs/protocol/README.md#licensing) for the
WebSocket transport library. This is an independent SDK, not an OpenAI-maintained
Go SDK.

### Observe a root and its live direct children

Register before starting root work:

```go
stream, err := client.SubscribeTree(ctx, rootThreadID, codex.TreeOptions{})
if err != nil {
    return err
}
defer stream.Close()
// Now start root work through the usual StartTurn API.
for record := range stream.Events() {
    // record.Kind distinguishes Native from SDK-derived relation/state records.
    // record.Native retains Event.Params, Data and DecodeError unchanged.
    // record.Raw retains the native notification envelope bytes.
    _ = record
}
return stream.Err()
```

`SubagentCreated` requires a live successful `spawnAgent` completed item with
receiver identities. Other successful collab calls can establish
`SubagentAssociated`; association alone does not confirm creation. A later spawn
confirmation uses the same relation key. Relations precede child notifications;
all native notifications that establish relations are also delivered. Repeated
relations do not duplicate creation; identical text deltas remain separate events.
Source thread/turn/item IDs, direct parent, provider, connection epoch and local
sequence accompany delivery. Unknown fields, methods, explicit null and absent
parameters remain available. Each observer owns its buffers.

This first slice follows **direct children only** after their collab evidence
arrives. Its initial `ObservationState(incomplete)` explicitly reports this
scope, the pre-relation gap, and unverified best-effort runtime coverage. Known
source IDs outside the root/direct-child set are filtered; this includes child
events arriving before the relation. No pending-event replay, recursive discovery,
history seed, recovery or complete-tree guarantee is provided in this slice.
Notifications without an identifiable source are preserved as `unscoped` and do
not establish membership. `UsageScope` remains `unknown`; child text and usage are
never combined with parent answers or usage.

At pinned Codex 0.160.1, native server source automatically attempts to attach
initialized connections to newly created threads, best-effort. The SDK uses
notifications already on its connection and sends **zero** child resume/start/
subscribe RPCs or additional connections. This is source evidence and offline
fake-peer validation, not real-server subagent validation. Older/unknown runtimes
remain unverified; the SDK does not probe them with execution RPCs. See
[compatibility and evidence](docs/compatibility.md#managed-direct-child-observation-ticket-01).

`TreeOptions.EventQueue` defaults to `Limits.EventQueue`; `MaxNodes` defaults to
4096 including the root. Both require positive values or zero for defaults. Queue
overflow terminates only that observer with `SubscriptionOverflowError`; graph
exhaustion ends it with `ErrLimit`. Each tree uses one shared subscription-admission
slot. `Close` ends local observation without interrupting provider work. Parent
completion does not close it. Permission and question answers still go through
original `Client.Requests()` handles, with no second answer authority in records.
The existing `Subscribe` API remains a connection-wide native broadcast.
