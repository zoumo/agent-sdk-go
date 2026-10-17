# codex-sdk-go

A native Go client for the Codex app-server protocol.

**Start with [compatibility and the complete official capability inventory](docs/compatibility.md).**
The protocol source is pinned to one exact official commit. Mock tests are not
live certification, and no supported-version range is claimed.

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

## Explicitly resume and detach a native thread

Keep the native thread ID returned by the server. After closing the old client,
construct a new client with `NewStdio` to perform a fresh handshake, then resume
and read that exact ID. The SDK has no saved-session registry or reconnect loop.

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
See [generation provenance](docs/protocol/README.md) for the exact native fields.

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
`RPCError` retains its code, message, and raw optional data. A server error is not
proof that no side effect occurred. Nothing is retried automatically. A canceled
blocked write terminates the connection to release the writer; other pending calls
then fail with their own dispatch evidence. Late responses cannot satisfy new calls.

## Bounds and process ownership

`DefaultLimits()` supplies finite per-connection defaults. Each zero field in
`Limits` selects its default; negative values are rejected:

- Incoming and outgoing frame payload: 8 MiB, excluding newline
- Queued outbound frames: 32; pending calls: 128
- Active local subscriptions: 32; queued events per subscriber: 128
- Active reverse requests: 128; queued reverse requests: 32
- Unanswered handle lifetime: 5 minutes (local expiry, no protocol cancellation)
- Diagnostic tail: 64 KiB; all stderr content is replaced with redaction markers
- Every call, including initialization: at most 30 seconds unless configured
- A blocked write: 10 seconds
- Shutdown: 2 seconds, split between graceful stdin EOF and forced-child reaping

Earlier caller deadlines win. Resource admission fails with `ErrLimit` before
sending where possible. An oversized outgoing frame is not sent; malformed or
oversized incoming frames terminate the connection with an explicit cause.
Bounded diagnostic counters record unmatched responses and notifications without
retaining diagnostic payloads. Subscription storage scales with queue capacity,
frame size, and subscriber count, including independent typed payload copies; use
smaller limits for large events or many observers. SDK decoding runs on the reader,
but caller callbacks never do. Stderr is drained independently of the reader.

`Close()` is concurrent-safe and idempotent. It closes stdin, waits for graceful
exit, then kills and reaps only the owned child. It closes all owned pipe ends and
settles pending calls and reverse-request handles. A child exit is detected even if an inherited output pipe
remains open. It does not kill descendants, process groups, or an existing daemon.
`Done()` signals connection termination; `Err()` gives the terminal cause. A rare
OS failure to reap before the shutdown deadline returns `ErrShutdownTimeout`;
the background `Wait` still owns reaping. User-defined JSON marshal/unmarshal
implementations run in the caller and must cooperate; the SDK cannot interrupt
arbitrary Go code.

## Development and evidence

```sh
mise exec go@1.26.9 -- go test ./...
mise exec go@1.26.9 -- go test -race ./...
mise exec go@1.26.9 -- go vet ./...
python3 docs/protocol/verify_inventory.py
```

Tests launch only synthetic helper processes and never require an account or call
a model. [Ticket 01](docs/evidence/ticket-01.md), [ticket 02](docs/evidence/ticket-02.md),
[ticket 03](docs/evidence/ticket-03.md), and [ticket 04 evidence](docs/evidence/ticket-04.md)
record tested scenarios.
[Official testing patterns](docs/protocol/testing-patterns.md) distinguishes
app-server harnesses from the TypeScript SDK's `codex exec` transport.

Licensed under [Apache-2.0](LICENSE). See [NOTICE](NOTICE) for official protocol
attribution. This is an independent SDK, not an OpenAI-maintained Go SDK.
