# Claude Agent SDK for Go

An experimental, independent native Go client for a locally installed Claude Code CLI. No other Go SDK or language runtime is required by the client. Original code is licensed under Apache-2.0.

**Experimental core implemented; not production-validated. Live Claude CLI compatibility: UNKNOWN.** See the [canonical compatibility matrix and complete official feature inventory](docs/compatibility.md) for implemented APIs, synthetic/race evidence, and unsupported features. The protocol baseline is the official Python SDK at [`f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab`](https://github.com/anthropics/claude-agent-sdk-python/tree/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab), which references CLI 2.1.302. Synthetic tests do not establish real runtime interoperability or official endorsement.

## Implemented core

- Local process startup with literal argv, explicit CLI path/workdir/model, bounded and correlated initialization
- Synchronous complete-frame `Send` and single-consumer `Receive`
- Explicit native resume, separate requested/observed session identity, and correlated `Interrupt` acknowledgments
- Typed system, assistant, user, result, stream-delta, tool-use and tool-result views, with lossless raw JSON for unknown types, fields and blocks
- Deferred, single-use permission handles with original IDs/input/context, bounded asynchronous handlers, cancellation guards, and default deny
- Typed cancellation events, including unknown/completed IDs, and request-specific terminal answer errors
- Observable incoming control requests and explicit error replies for unsupported controls
- Bounded event/control queues, frame sizes and stderr tails; explicit overflow and protocol errors
- Independent `Done` and `Err`, retained accepted events, idempotent concurrent `Close`, and owned child reaping with bounded escalation

The typed Go views are handwritten, with raw JSON preserving unknown exposed-event data; no automatic upstream type synchronization/code generation exists. Hooks, SDK-hosted MCP, full configuration parity, session-store helpers and dynamic model/permission controls are unsupported. A separately authorized [live compatibility checklist](docs/live-compatibility.md) is provided. Default tests and CI skip the opt-in live initialization probe; the full checklist remains manual.

## Usage

You must install and configure Claude Code separately. Running this example can use your configured account and incur model costs. The SDK's own tests never invoke the real CLI.

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    claude "github.com/zoumo/claude-agent-sdk-go"
)

func main() {
    ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
    defer cancel()

    client, err := claude.Open(ctx, claude.Options{CLIPath: "claude"})
    if err != nil { log.Fatal(err) }
    defer client.Close()

    if err := client.Send(ctx, "Explain this directory."); err != nil {
        log.Print(err)
        return
    }
    for {
        event, err := client.Receive(ctx)
        if err != nil { log.Print(err); return }
        if event.Assistant != nil {
            for _, block := range event.Assistant.Content.Blocks {
                if block.Text != nil { fmt.Println(block.Text.Text) }
            }
        }
        if event.Result != nil { return }
    }
}
```

A successful `Send` means only that the full NDJSON frame was written. It is not proof of acceptance, execution, or exactly-once delivery. `errors.Is(err, claude.ErrUnknownOutcome)` marks ambiguous writes/control waits; the SDK never automatically retries them. Canceling an in-flight write closes the connection to unblock that write. Canceling a `Receive` wait leaves the session running. Queued writes rejected before starting return a caller-context error or `ErrClosed`, without inheriting another write’s unknown outcome; inspect `Client.Err` for the connection’s terminal cause.

A result ends one turn, not the process. Always close the client. `Done` is independent of event-buffer capacity; drain `Receive` to obtain accepted events before its terminal error. `Err` exposes the latest known failure, while `Close` waits for cleanup and final diagnostics. `Error.Stderr` contains a bounded diagnostic tail that may be sensitive; the SDK never logs it automatically.

Incoming `can_use_tool` requests are denied when no permission handler is configured. This is not a sandbox: CLI settings, previously granted permissions and operations that do not prompt still apply. An assistant `AskUserQuestion` content block is observable data and causes no synthetic reply.

`Options.Env == nil` inherits the current environment; a non-nil slice replaces it. Defaults are 60 seconds for initialization, 3 seconds for shutdown escalation, 128 queued events, 4 MiB per frame, 32 pending outbound controls, 32 queued automatic responses, 32 queued permission-handler jobs, 128 unanswered incoming controls, 4 concurrent permission handlers, 4,096 distinct incoming control/cancellation IDs per connection, and an 8 KiB stderr tail. Request-ID history is never evicted; exhaustion terminates the connection rather than reviving stale requests. Zero-valued fields select defaults. Incoming overflow terminates the connection visibly, retaining only already accepted events. Oversized local sends and excess pending outbound controls are rejected before writing and do not close the connection. Values above documented [validated resource ceilings](docs/compatibility.md#resource-and-shutdown-contract) are rejected before process startup. Linux subprocess/race tests verify direct-child reaping and process-group teardown, including descendants holding stdout and descendants that close their pipes. Other platform runtimes are unverified; non-POSIX builds kill only the direct child, and escaped process groups are outside the guarantee. Close never waits for arbitrary user callbacks that ignore cancellation.

## Native resume and interrupt

`client.SessionID()` returns `(id, true)` only after a nonempty native ID arrives in a system or result frame. Before that it returns `("", false)`. It reports the most recently observed ID and remains available after closing. `client.RequestedResumeID()` separately returns the original request, which is never treated as observed identity.

To request resume, open a fresh process with `claude.Options{CLIPath: "claude", ResumeID: id}`. The ID is bound literally as `--resume=<value>`, including dash-leading values. Native history belongs to the CLI: the SDK does not replay earlier user messages, restore pending permission requests, adopt an old process, or provide durable recovery. Resume requires whatever persisted history and configuration the CLI itself needs. The fixture tests verify argv and observed IDs, not live persisted-history continuation.

`client.Interrupt(ctx)` sends an interrupt control request and waits for that request's acknowledgment. Concurrent interrupts have independent IDs; unknown, duplicate, and stale response IDs cannot complete another waiter. An explicit rejection returns `*claude.ControlError`. Success acknowledges the control only: it does not promise tool rollback, close the process, or consume a later turn result. Continue receiving events to observe the turn's outcome.

Cancellation before a write starts emits no request. Cancellation while waiting after a completed write returns an unknown-outcome error and leaves the connection available; cancellation during the write can terminate the connection to unblock it. Disconnect and write failure wake pending controls. The SDK never retries an interrupt automatically.

## Deferred permissions

Configure `Options.PermissionHandler` to receive a `*PermissionRequest` for each real `can_use_tool` control. The handler can retain the handle and return, or wait for a human while unrelated events and controls keep moving. Handler return never answers the request. Calls run on a bounded worker pool; exhausted queues fail visibly. Handlers should watch `request.Context().Done()`. Close does not wait for callbacks that ignore cancellation.

`RequestID()`, `ToolUseID()`, `ToolName()`, and `Epoch()` expose immutable identifiers. `Input()` returns a copy of the original input. `Raw()` returns a copy of the entire nested request, preserving permission suggestions, agent/display context and unknown fields. An epoch identifies a client instance within the current Go process; it is not a native session ID.

Answer exactly once:

```go
err := request.Answer(ctx, claude.PermissionAnswer{
    Behavior: claude.PermissionAllow, // nil UpdatedInput preserves original input
})
// Or provide UpdatedInput: json.RawMessage(`{"path":"approved.txt"}`).
// An explicit {} replaces the input with an empty object.
// To deny, use Behavior: claude.PermissionDeny, Message: "Not approved".
```

Only this handle can send its permission response, correlated to the original control request ID. Copying it does not create another authority. A second attempt after success, or while another attempt is running, returns `ErrPermissionUsed`. A failed response attempt returns `ErrPermissionWriteFailed`, preserving its original cause even after the connection closes. Rejected/failed attempts are `*PermissionError` values with the original request ID; use `errors.Is` for their classification. An already-canceled caller context consumes nothing; once an answer attempt is reserved, it cannot be retried. Failed reserved answers terminate the connection unless the CLI already canceled the request or the connection is closed. A handler panic or invalid answer produces an observable `PermissionError` and terminates the connection without granting permission. An earlier completed answer cannot be undone.

A real `control_cancel_request` immediately cancels the matching handle context and prevents a response not yet authorized for writing. The cancellation event has a typed `event.Cancel.RequestID` and lossless `event.Cancel.Raw`, including for unknown, repeated, or already-completed IDs. A late cancel does not overwrite an already-completed answer. If bounded event/history capacity prevents delivery, `Done` and a terminal `ResourceLimitError` expose the loss and invalidate remaining handles. `ErrPermissionCancelled` identifies rejected late answers; `context.Cause(request.Context())` identifies cancellation, completed use, or shutdown/failure. Cancellation concurrent with an already-authorized write cannot retract bytes and may return `ErrUnknownOutcome`. Closing or disconnecting invalidates pending handles with `ErrClosed`; a new client has a new epoch, including when resuming a native session. Handles remain bound to their original client, so an old handle cannot answer an identically numbered request on a fresh client. `ErrPermissionStale` is the defensive cross-epoch guard; the handle-only API cannot redirect a handle to another client. These are connection-local safety guarantees, not durable exactly-once delivery.

A real `can_use_tool` for `AskUserQuestion` uses this same API with caller-supplied updated input. There is no automatic question-schema mapping. An assistant `tool_use` named `AskUserQuestion` remains only an event and never creates a responder or synthetic `tool_result`. These semantics are tested against synthetic source-derived frames; live CLI question behavior remains unverified.

## Data types and compatible imports

Pure data and the existing JSON decoders now live in
`github.com/zoumo/claude-agent-sdk-go/types`. Import that package alone to inspect
frames with `types.ParseEvent`, build content/permission answers, or store CLI
configuration without depending on the process client. It uses only the Go
standard library. Message fields and JSON behavior are unchanged by this move;
usage and unknown data remain raw JSON.

The root package aliases the moved event/content/result/control types,
`PermissionBehavior`, `PermissionAnswer`, and `Limits`, including the existing
permission constants. Old literals, method sets, and assignments to/from the
new import continue to compile. The canonical named types now belong to
`types`: reflection `PkgPath` and type-name formatting (including `%T`) can
change. Those strings are not a compatibility guarantee.

`claude.Options` retains its original fields, so existing keyed literals still
work. `PermissionRequest` is a connection-bound answer handle, and
`PermissionHandler` accepts that handle; both remain in the root package.
`types.Options` contains only the callback-free startup fields. Adapt it
explicitly, then attach a handler if needed:

```go
config := types.Options{CLIPath: "claude", Limits: types.Limits{EventBuffer: 64}}
options := claude.OptionsFromTypes(config)
options.PermissionHandler = func(request *claude.PermissionRequest) {
    // Retain or answer the real request using the existing permission API.
}
// Pass options to claude.Open(ctx, options).
// options.Types() returns the callback-free configuration again.
```

Conversions preserve fields, including nil versus empty `Env`, and share its
backing slice just like an ordinary struct copy. Defaults and validation still
run in `Open`. Runtime errors, process ownership, locks, permission registries,
queues, and callback execution stay in the root implementation.

## Development and verification

Go is pinned to 1.26.9 in `mise.toml`. This module is standard-library-only.

```sh
mise exec go@1.26.9 -- go version
mise exec go@1.26.9 -- go test -count=1 -timeout 180s ./...
mise exec go@1.26.9 -- go test -race -count=1 -timeout 300s ./...
mise exec go@1.26.9 -- go vet ./...
mise exec go@1.26.9 -- go list -deps ./types
```

See [CI checks and live gate](docs/ci.md) for the Linux/macOS matrix, committed-change checks, and explicit live skip status.

Offline tests exercise exported APIs against a scripted subprocess with a replacement environment and isolated temporary HOME/workdir. They cover exact initialization/user/interrupt envelopes, literal resume without replay, requested versus observed native IDs, immediate and out-of-order control replies, unknown/stale IDs, timeout/response/close races, startup failures, preserved unknown data, Unicode/chunk framing, errors after accepted events, queue overflow, fail-closed permissions, direct write errors, cancellation, concurrent sends/close, POSIX child reaping, process-group descendants, ignored SIGTERM, full event queues with pending interrupts/permissions, response-work overload and integer-limit rejection. Linux FIFO-barrier tests additionally check queued cancellation/close, interrupted partial response writes, preserved failure classification and no retry using actual subprocess stdin transcripts. The real-CLI examples are compiled, not executed. The subagents-offline example is executed against its own synthetic peer.

Protocol and behavioral references: [official Agent SDK overview](https://code.claude.com/docs/en/agent-sdk/overview), pinned Python SDK `query.py`, `subprocess_cli.py`, `types.py`, and its `test_query.py`, `test_message_parser.py`, `test_subprocess_buffering.py`, `test_close_cancellation.py`, `test_session_resume.py`, `test_streaming_client.py`, `test_tool_callbacks.py`, and `test_transport.py`. The implementation and Go tests are original, not copied from those sources or a rival Go SDK.

See the [compatibility inventory](docs/compatibility.md) for every official export, client method, option and wire-control boundary, and [the design](docs/design/sdk-design.md) for the implemented core contract and deferred scope. Source/test provenance is recorded there and in [NOTICE](NOTICE).

Message construction, JSON round trips, optional values and Raw/typed editing
rules are documented in [the message JSON contract](docs/message-json.md).
See [type source and license review](docs/type-sources.md) for pinned references.

### Subagent forwarding

`OpenTree` owns one native stream and enables `forwardSubagentText` during
initialization. It exposes lossless native frames and explicitly marked SDK
association/observation records. Forwarding support remains unverified until
there is runtime evidence; new entity creation is not inferred from task start.
Created remains **conditional and never emitted**: task_started and
SubagentStart cannot alone exclude resume; Associated does not prove creation.
See the [per-capability matrix](docs/subagent-compatibility.md) for pinned versions,
E1/E2 evidence and foreground/background/nested/legacy limits, plus the
[API and current limits](docs/subagent-events.md). No E3/E4 subagent checks ran.

A self-contained example uses only a local synthetic peer (no installed CLI or
model):

```sh
mise exec go@1.26.9 -- env GOPROXY=off GOSUMDB=off CLAUDE_GO_LIVE=0 go run ./examples/subagents-offline
```

It prints source/parent/run records through a single OpenTree consumer, continues
after the parent result, observes patch-only terminals and explicitly closes.
Session-state events need a native opt-in on applicable CLIs; background task
snapshots replace a process-local set and do not prove termination. See the
[offline validation evidence](docs/evidence/subagent-ticket05.md).
