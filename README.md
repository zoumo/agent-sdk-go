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
This slice counts and discards notifications; incoming server requests fail the
connection explicitly. See the canonical compatibility record before enabling
capabilities that require callbacks or event handling.

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
- Diagnostic tail: 64 KiB; all stderr content is replaced with redaction markers
- Every call, including initialization: at most 30 seconds unless configured
- A blocked write: 10 seconds
- Shutdown: 2 seconds, split between graceful stdin EOF and forced-child reaping

Earlier caller deadlines win. Resource admission fails with `ErrLimit` before
sending where possible. An oversized outgoing frame is not sent; malformed or
oversized incoming frames terminate the connection with an explicit cause.
Bounded diagnostic counters record unmatched responses and notifications without
retaining payloads. Stderr is drained independently of the protocol reader.

`Close()` is concurrent-safe and idempotent. It closes stdin, waits for graceful
exit, then kills and reaps only the owned child. It closes all owned pipe ends and
settles pending calls. A child exit is detected even if an inherited output pipe
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
a model. [Ticket 01 evidence](docs/evidence/ticket-01.md) records tested scenarios.
[Official testing patterns](docs/protocol/testing-patterns.md) distinguishes
app-server harnesses from the TypeScript SDK's `codex exec` transport.

Licensed under [Apache-2.0](LICENSE). See [NOTICE](NOTICE) for official protocol
attribution. This is an independent SDK, not an OpenAI-maintained Go SDK.
