# codex-sdk-go

Shell commands below run from the repository root; see [root validation](../docs/testing.md).

A native Go client for the Codex app-server protocol.

Connections and execution live in the native `codex` package. Events, native models,
and optional values live in `github.com/zoumo/agent-sdk-go/codex/types`.

## Compatibility

The source/schema target is Codex **0.162.1** at
`092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`; no supported-version range is claimed.
Current bindings and descendants have source/offline evidence only. Historical
CLI 0.162.1 stdio/loopback WS and user-input checks used older SDK/schema bases;
approvals and interrupt were untriggered, public WSS/JWT and current descendants
remain unverified. Exact scopes stay in [evidence.json](docs/protocol/evidence.json).

Use [capabilities.json](docs/protocol/capabilities.json) for the method inventory
and [protocol provenance](docs/protocol/README.md) for source hashes and licensing.
Raw Call/types support is not workflow support. Dedicated steering/work queues,
thread administration, hooks/plugins, MCP administration, realtime, credential
management and durable recovery are not provided. Child observation does not
provide a spawn controller, complete forwarding or combined completion.

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

## Optional child detail delivery

To omit reviewed pure child text, reasoning and tool progress from `Receive`, set
`SessionOptions.ChildDetailPolicy: codex.ChildDetailSuppress` when calling `Open`.
The zero value and `codex.ChildDetailPreserve` retain default delivery. Other
values wrap `ErrInvalidInput` before process startup or RPC. The policy is fixed
per Session, including subsequent submissions and confirmed child members;
shared Client sessions select independently.

Filtering leaves native source output, parent tool results, relationships,
approvals and lifecycle facts intact. Complete items and mixed/unknown frames
remain visible, with their original bytes. It is not content redaction or a
token-cost control. The policy also applies to `Open` with explicit `Resume`
parameters. Low-level `Client.ResumeThread` is unchanged. See the exact
[method and shape contract](../harness/modules/native-session/design.md#child-detail-可选子详细事件交付).
`NativeIntegrationSuite/TestOptionalChildDetail/codex/F1`–`F5` validates synthetic
public API delivery and original peer bytes; real child interoperability remains
unverified.

## Query existing descendants

`Session.ListSubagents(ctx, SubagentQuery{})` reads bounded native pages of stored
descendants of the fixed main identity. Initialize with experimentalApi enabled.
Inspect returned `Pages`, `Coverage` and the error together: partial pages can
accompany a failure. Defaults select five native subAgent source kinds and only
non-archived records; archived records need a separate query. Results do not
change observation membership or prove that an Agent is running or subscribed.

Discovery walks pages with defaults of 128 pages, 10000 records and 16 MiB;
MaxPages/MaxRecords/MaxBytes override these bounds. Failed calls retain accepted
whole pages plus error/Coverage. EndReached requires explicit terminal null;
EnumerationComplete also requires starting without a cursor. Missing/repeated/empty continuation cursors fail
explicitly. No automatic retry or cache fallback runs.

## Open, submit and receive

Go 1.26.9 is pinned with mise. This example is compiled by external-consumer
validation; default tests do not launch the selected real executable.

```go
package main

import (
 "context"
 "fmt"
 "log"
 codex "github.com/zoumo/agent-sdk-go/codex"
 "github.com/zoumo/agent-sdk-go/codex/types"
)

func main(){
 ctx:=context.Background()
 session,err:=codex.Open(ctx,codex.SessionOptions{
  Stdio:&codex.StdioOptions{Executable:"/explicit/path/to/codex",Args:[]string{"app-server"},Initialize:types.InitializeParams{ClientInfo:types.ClientInfo{Name:"example",Version:"1"}}},
  Thread:types.ThreadStartParams{Ephemeral:types.Some(true)},
 })
 if err!=nil{log.Print(err);return}
 defer session.Abort()
 submission,err:=session.Submit(ctx,codex.Input{Input:[]types.UserInput{{Text:types.Ptr(types.TextUserInput{Text:"Say hello briefly."})}}})
 if err!=nil{log.Print(err);return}
 fmt.Println("native turn",submission.Response.Turn.ID)
 for{
  received,err:=session.Receive(ctx);if err!=nil{log.Print(err);return}
  if received.Request!=nil{
   // Hand this original request to the application UI. It must supply the
   // native answer or approval decision; the SDK makes no default decision.
   fmt.Println("decision required",received.Request.ID(),received.Request.Method())
  }
  if received.Event!=nil{fmt.Println(received.Origin,received.Event.Method,string(received.Event.Params))}
  if received.Observation!=nil{fmt.Println(received.Observation.Reason)}
 }
}
```

`Open` initializes the connection and fixes the server's main identity before
returning. `StartResponse` retains a copy of the actual native startup model,
sandbox and unknown fields.
Each Session owns one FIFO; `Receive` selects native events, real
`IncomingRequest` values and SDK observation evidence. Concurrent
receivers compete for that same FIFO. Applications own UI/logging fan-out.
A native result does not close the Session. `Submit` uses native turn/start,
retains the actual native turn response, and never guesses start versus steer
from local Busy state. A successful response is not turn completion.

Use `IncomingRequest.Approve` / `Deny` with native `ApprovalOptions`, or `Answer`
with `types.ToolRequestUserInputResponse` keyed by original question IDs. Native
scope, execpolicy and network amendment options survive. The original
`Handle().Answer` supports explicit raw extensions; `Handle().Reject` is an RPC
error, separate from a user denial. Copies retain the same single-use authority.
Cancellation, native resolution or Session termination invalidates that authority.
No answer timeout, default deny or automatic retry is imposed by Session.

Native `types.Event` retains exact `Raw`, `Params`, unknown fields/variants and
native thread/turn/item IDs. Malformed known payloads retain bytes with a decode
error; malformed envelopes terminate interaction. Raw duplicates remain facts.
Confirmed members deliver immediately even when parent knowledge is missing.
Later parent evidence never reorders earlier facts. Unconfirmed connection
records are bounded diagnostic material in `Unattributed`, never live authority;
later membership does not replay them into the Session FIFO.

`Receive` cancellation affects only that wait. `Interrupt` requires an explicit
native `TurnID`; its acknowledgment does not prove execution stopped. `Close`
invalidates owned handles and cleans up without waiting for consumers. `Done`
reports interaction termination independently of unread events. Drain to EOF or
the stable terminal error, or explicitly `Abort` to discard abandoned backlog.

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

session, err := codex.Open(ctx, codex.SessionOptions{Client: client,
    Thread: types.ThreadStartParams{Ephemeral: types.Some(true)},
})
if err != nil { return err }
defer session.Close()
fmt.Println(session.ThreadID())
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
  such as context cancellation and EOF are retained. Reflected bearer redaction follows the [error rules](#cancellation-and-errors).
  Successful results, events, and request payloads remain caller-sensitive data;
  the SDK is not a general logging redactor and cannot control server logs.

There is no automatic reconnect or request replay. After a lost connection,
inspect the operation's dispatch evidence, create a new initialized client, then
explicitly resume the saved native thread ID if appropriate. An unknown outcome
must be reconciled before repeating side-effecting work. Remote `Close` releases
only this connection; it never shuts down or kills the remote service.

The WSS example describes a caller-provided TLS endpoint; source inspection and
synthetic TLS tests do not certify a production TLS proxy, bearer issuer, server
configuration, or live account. See the [canonical evidence](#compatibility)
and [transport provenance](docs/protocol/README.md). The
[remote example](remote_example_test.go) compiles in tests and is never executed.

## Native values and raw extensions

Native `types` production code uses only the standard library. Generated
`Optional[T]` preserves omission, explicit null, false, zero and empty collections.
Unknown fields retain their JSON without float64 conversion. Representability
is not permission to submit an unsupported workflow. The server still validates
native constraints. See [generation provenance](docs/protocol/README.md).

An explicit `Client` remains useful for a shared native I/O resource, handshake
metadata and `Call` extensions. Supply it in `SessionOptions.Client`. Closing one
Session leaves other owners on that connection usable. A main identity has one owner. Overlapping child/ancestor observation
ranges share physical attachments; only the final observer releases them.
Typed detachment/resume operations respect ownership and uncertain-release
fences. Raw calls are outside coordinated Session guarantees and never create a
second consumer or answer registry.

The SDK does not discover executables, log in, look up credentials, reconnect or
retry. `Env=nil` inherits the parent environment; an explicit environment can
select an isolated `CODEX_HOME`. Stdio owns its chosen child; remote Close owns
only the socket. The selected native executable can read its own configuration.

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
- Session owners: 32; accepted event backlog grows with memory
- Active reverse requests: 128; delivery uses the Session FIFO
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
retaining diagnostic payloads. Session storage scales with unread events and frame size; a slow consumer can
exhaust available memory. This is neither durable delivery nor an OOM guarantee. SDK decoding runs on the reader,
but caller callbacks never do. Stderr is drained independently of the reader.

`Close()` is concurrent-safe and idempotent. For stdio it closes stdin, waits for
graceful exit, then kills and reaps only the owned child. For a remote client it
closes only its WebSocket connection. Both settle pending calls, Sessions,
and reverse-request handles; stdio also closes all owned pipe ends. A child exit is detected even if an inherited output pipe
remains open. It does not kill descendants, process groups, or an existing daemon.
`Done()` signals connection termination; `Err()` gives the terminal cause. A rare
OS failure to reap before the shutdown deadline returns `ErrShutdownTimeout`;
the background `Wait` still owns reaping. User-defined JSON marshal/unmarshal
implementations run in the caller and must cooperate; the SDK cannot interrupt
arbitrary Go code.

## Native resume and history queries

Resume a public Session on a shared client or an owned stdio process:

```go
session, err := codex.Open(ctx, codex.SessionOptions{
    Client: client,
    Resume: &types.ThreadResumeParams{ThreadID: savedID},
})
```

`Resume` requires a nonempty native ID and empty `Thread` start options. It
reserves ownership before dispatch and returns only after the native response
confirms the same identity. Live notifications around that response retain their
arrival order. `ResumeResponse()` returns a detached copy of the actual native
response; `StartResponse()` is absent. Saved turns do not enter `Receive`, grant
answer authority, or establish live child membership. Use `ListSubagents` to
query already saved descendants without submitting another turn.

Recovery is explicit and preserves native resume side effects. It does not
retry, create a replacement thread, or promise to recover missed events. Failed
or uncertain dispatch performs bounded detachment; failed cleanup preserves an
ownership fence. Shared clients remain open; owned processes are reaped.

`Client.ResumeThread(ctx, types.ThreadResumeParams{ThreadID: id})` explicitly
resumes a native thread. It does not create a Session or replay history through
Session.Receive. `Client.UnsubscribeThread` releases that native attachment.

`Client.ListThreads`, `Client.ReadThread`, `Client.ListThreadTurns` and
`Client.ListThreadItems` query native metadata and saved content without Open or
resume. Prefer `ReadThread` with `IncludeTurns: types.Some(false)`. Each call
returns one native result; keep opaque next/backwards cursors, sort direction,
item-view selection and omitted/null/value fields. Query availability depends on
the server version/backend and stored history format. Metadata and saved items
are not original notification/delta/approval logs or atomic snapshots. Remote
queries use server storage and paths; local list may repair metadata unless
`UseStateDbOnly` selects the native DB-only path. See [query boundaries](../harness/contracts/history-query.md#codex-原生接口)
and the [offline example](examples/history-query-offline).

## Offline examples and validation

Run the root offline gate with the mise-managed compiler. The offline
examples use explicit synthetic peers and code-owned protocol inputs, never an
installed agent. [Migration details](../docs/migration.md#session) distinguish
native limitations, resource options and breaking lifecycle changes.

Run from the repository root:

```sh
mise exec -- go run ./codex/examples/history-query-offline
mise exec -- go run ./codex/examples/observe-subagents-offline
```
