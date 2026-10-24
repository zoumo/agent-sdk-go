> The new owned-process `Open(ctx, SessionOptions)` API and its current migration
> limits are documented in [Session migration](docs/session-migration.md). The
> examples below retain the temporary `NewClient` compatibility entry point.

# Claude Agent SDK for Go

Commands in this native documentation run from `claude/`; see [root validation](../docs/testing.md).

An experimental, independent native Go client for a locally installed Claude Code CLI. No other Go SDK or language runtime is required by the client. Original code is licensed under Apache-2.0.

**Experimental core implemented; not production-validated. Live Claude CLI compatibility: UNKNOWN.** See the [canonical compatibility matrix and complete official feature inventory](docs/compatibility.md) for implemented APIs, synthetic/race evidence, and unsupported features. The current CLI target is **2.1.258**, with official TypeScript contract **0.3.258**. The broader schema reference is the official Python SDK at [`f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab`](https://github.com/anthropics/claude-agent-sdk-python/tree/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab), which references CLI 2.1.302. Synthetic tests do not establish real runtime interoperability or official endorsement.

## Documentation

- [API comparison and examples](docs/api-comparison.md), [message JSON](docs/message-json.md)
- [Compatibility and limitations](docs/compatibility.md), [type/version provenance](docs/type-sources.md)
- [Subagent API and bounds](docs/subagent-events.md), [capability matrix](docs/subagent-compatibility.md)
- [Event sequences / 用户故事](docs/event-sequences.md), [architecture](docs/design/sdk-design.md)
- [Testing and CI](docs/ci.md), [offline recorded replay](docs/record-replay.md), [manual live checklist](docs/live-compatibility.md)

## Implemented core

- Local process startup with literal argv, explicit CLI path/workdir/model, bounded and correlated initialization
- Synchronous complete-frame `Send` and single-consumer `Receive`
- Explicit native resume, separate requested/observed session identity, and correlated `Interrupt` acknowledgments
- Typed system, assistant, user, result, stream-delta, tool-use and tool-result views, with lossless raw JSON for unknown types, fields and blocks
- Deferred, single-use permission handles with original IDs/input/context, bounded asynchronous handlers, cancellation guards, and default deny
- Typed cancellation events, including unknown/completed IDs, and request-specific terminal answer errors
- Observable incoming control requests and explicit error replies for unsupported controls
- Dynamic FIFO event delivery; bounded control queues, frame sizes and stderr tails; explicit resource and protocol errors
- Independent `Done` and `Err`, retained accepted events, idempotent concurrent `Close`, and owned child reaping with bounded escalation

The typed Go views are handwritten, with raw JSON preserving unknown exposed-event data; no automatic upstream type synchronization/code generation exists. Hooks, SDK-hosted MCP, full configuration parity, session-store helpers and dynamic model/permission controls are unsupported. A separately authorized [live compatibility checklist](docs/live-compatibility.md) is provided. Default tests and CI skip the opt-in live initialization probe; the full checklist remains manual.

## Usage

You must install and configure Claude Code separately. Running this example can use your configured account and incur model costs. Default offline tests never invoke the real CLI.

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    claude "github.com/zoumo/agent-sdk-go/claude"
)

func main() {
    ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
    defer cancel()

    client, err := claude.NewClient(ctx, claude.Options{CLIPath: "claude"})
    if err != nil { log.Fatal(err) }
    defer client.Abort()

    if err := client.Send(ctx, "Explain this directory."); err != nil {
        log.Print(err)
        return
    }
    for {
        received, err := client.Receive(ctx)
        if err != nil { log.Print(err); return }
        event, ok := received.(claude.Event)
        if !ok {
            // Handle relationship and observation records.
            continue
        }
        if event.Assistant != nil {
            for _, block := range event.Assistant.Content.Blocks {
                if block.Text != nil { fmt.Println(block.Text.Text) }
            }
        }
        if event.ParentToolUseID == nil && event.Result != nil { return } // App stop policy; children may still run.
    }
}
```

A successful `Send` means only that the full NDJSON frame was written. It is not proof of acceptance, execution, or exactly-once delivery. `errors.Is(err, claude.ErrUnknownOutcome)` marks ambiguous writes/control waits; the SDK never automatically retries them. Canceling an in-flight write closes the connection to unblock that write. Canceling a `Receive` wait leaves the session running. Queued writes rejected before starting return a caller-context error or `ErrClosed`, without inheriting another write’s unknown outcome; inspect `Client.Err` for the connection’s terminal cause.

A result ends one turn, not the process. Always close the client. Use `Close` to retain accepted events for draining, or `Abort` to shut down and discard the unconsumed native backlog. After `Abort`, future `Receive` calls return the terminal error or `ErrClosed`; an already-running Receive may still complete. Bounded relationship state stays with the client until garbage collection. `Done` is independent of consumer progress; drain `Receive` to obtain accepted events before its terminal error. `Err` exposes the latest known failure, while `Close` waits for cleanup and final diagnostics. `Error.Stderr` contains a bounded diagnostic tail that may be sensitive; the SDK never logs it automatically.

Incoming `can_use_tool` requests are denied when no permission handler is configured. This is not a sandbox: CLI settings, previously granted permissions and operations that do not prompt still apply. An assistant `AskUserQuestion` content block is observable data and causes no synthetic reply.

`Options.Env == nil` inherits the current environment; a non-nil slice replaces it. Defaults are 60 seconds for initialization, 3 seconds for shutdown escalation, dynamic event buffering, 4 MiB per frame, 32 pending outbound controls, 32 queued automatic responses, 32 queued permission-handler jobs, 128 unanswered incoming controls, 4 concurrent permission handlers, 4,096 distinct incoming control/cancellation IDs per connection, and an 8 KiB stderr tail. Request-ID history is never evicted; exhaustion terminates the connection rather than reviving stale requests. Zero-valued fields select defaults. Slow Receive consumers accumulate accepted native events in a dynamic FIFO without blocking control-response routing or permission dispatch. The backlog has no configured count ceiling and is limited by available machine memory; sustained producer/consumer imbalance can exhaust memory. Draining releases event references; shutdown does not wait for draining, and process crashes or OOM do not guarantee delivery. `Limits.EventBuffer` is deprecated and ignored; its existing 0..65,536 validation remains, with no allocation based on its value. Frame, control, permission and relationship bounds still fail visibly when exceeded. Oversized local sends and excess pending outbound controls are rejected before writing and do not close the connection. Values above documented [validated resource ceilings](docs/compatibility.md#resource-and-shutdown-contract) are rejected before process startup. Linux/macOS offline subprocess/race tests verify direct-child reaping and process-group teardown, including descendants holding stdout and descendants that close their pipes. Other platform runtimes are unverified; non-POSIX builds kill only the direct child, and escaped process groups are outside the guarantee. Close never waits for arbitrary user callbacks that ignore cancellation.

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

A real `control_cancel_request` immediately cancels the matching handle context and prevents a response not yet authorized for writing. The cancellation event has a typed `event.Cancel.RequestID` and lossless `event.Cancel.Raw`, including for unknown, repeated, or already-completed IDs. A late cancel does not overwrite an already-completed answer. If bounded permission/history capacity prevents delivery, `Done` and a terminal `ResourceLimitError` expose the loss and invalidate remaining handles. `ErrPermissionCancelled` identifies rejected late answers; `context.Cause(request.Context())` identifies cancellation, completed use, or shutdown/failure. Cancellation concurrent with an already-authorized write cannot retract bytes and may return `ErrUnknownOutcome`. Closing or disconnecting invalidates pending handles with `ErrClosed`; a new client has a new epoch, including when resuming a native session. Handles remain bound to their original client, so an old handle cannot answer an identically numbered request on a fresh client. `ErrPermissionStale` is the defensive cross-epoch guard; the handle-only API cannot redirect a handle to another client. These are connection-local safety guarantees, not durable exactly-once delivery.

A real `can_use_tool` for `AskUserQuestion` uses this same API with caller-supplied updated input. There is no automatic question-schema mapping. An assistant `tool_use` named `AskUserQuestion` remains only an event and never creates a responder or synthetic `tool_result`. These semantics are tested against synthetic source-derived frames; live CLI question behavior remains unverified.

## Data types and compatible imports

Pure data and JSON decoders live in
`github.com/zoumo/agent-sdk-go/claude/types`. Import that package alone to inspect
frames with `types.ParseEvent`, build content/permission answers, or store CLI
configuration without depending on the process client. It uses only the Go
standard library. Usage and unknown data remain raw JSON.

The native `claude` package aliases the event/content/result/control types,
`PermissionBehavior`, `PermissionAnswer`, and `Limits`, including the existing
permission constants. Literals, method sets and assignments work across both imports. The canonical
named types belong to
`types`: reflection `PkgPath` and type-name formatting (including `%T`) can
change. Those strings are not a compatibility guarantee.

`claude.Options` contains callback-free startup fields, runtime callbacks and
`Subagents` bounds/history. `PermissionRequest` is a connection-bound answer handle, and
`PermissionHandler` accepts that handle; both remain in the native `claude` package.
`types.Options` contains only the callback-free startup fields. Adapt it
explicitly, then attach a handler if needed:

```go
config := types.Options{CLIPath: "claude", Limits: types.Limits{FrameBytes: 4 << 20}}
options := claude.OptionsFromTypes(config)
options.PermissionHandler = func(request *claude.PermissionRequest) {
    // Retain or answer the real request using the existing permission API.
}
// Pass options to claude.NewClient(ctx, options).
// options.Types() returns the callback-free configuration again.
```

Conversions preserve fields, including nil versus empty `Env`, and share its
backing slice just like an ordinary struct copy. Defaults and validation still
run in `Open`. Runtime errors, process ownership, locks, permission registries,
queues, and callback execution stay in the native `claude` implementation.

## Development and verification

Go is pinned to 1.26.9 in the root `mise.toml`. The native Claude packages are standard-library-only.

```sh
mise exec go@1.26.9 -- go version
mise exec go@1.26.9 -- go test -count=1 -timeout 180s ./...
mise exec go@1.26.9 -- go test -race -count=1 -timeout 300s ./...
mise exec go@1.26.9 -- go vet ./...
mise exec go@1.26.9 -- go list -deps ./types
```

See [CI checks and live gate](docs/ci.md) for the Linux/macOS matrix, committed-change checks, and explicit live skip status.

Offline tests exercise exported APIs against a scripted subprocess with a replacement environment and isolated temporary HOME/workdir. They cover exact initialization/user/interrupt envelopes, literal resume without replay, requested versus observed native IDs, immediate and out-of-order control replies, unknown/stale IDs, timeout/response/close races, startup failures, preserved unknown data, Unicode/chunk framing, errors after accepted events, slow-consumer FIFO bursts, fail-closed permissions, direct write errors, cancellation, concurrent sends/close, POSIX child reaping, process-group descendants, ignored SIGTERM, event backlogs with pending interrupts/permissions, response-work overload and integer-limit rejection. Linux FIFO-barrier tests additionally check queued cancellation/close, interrupted partial response writes, preserved failure classification and no retry using actual subprocess stdin transcripts. The real-CLI examples are compiled, not executed. The subagents-offline example is executed against its own synthetic peer.

Protocol and behavioral references: [official Agent SDK overview](https://code.claude.com/docs/en/agent-sdk/overview), pinned Python SDK `query.py`, `subprocess_cli.py`, `types.py`, and its `test_query.py`, `test_message_parser.py`, `test_subprocess_buffering.py`, `test_close_cancellation.py`, `test_session_resume.py`, `test_streaming_client.py`, `test_tool_callbacks.py`, and `test_transport.py`. The implementation and Go tests are original, not copied from those sources or a rival Go SDK.

See the [compatibility inventory](docs/compatibility.md) for official runtime exports, query methods, options, input/output variants and callback/control boundaries, and [the design](docs/design/sdk-design.md) for the implemented core contract and deferred scope. Source/test provenance is recorded there and in [NOTICE](NOTICE).

Message construction, JSON round trips, optional values and Raw/typed editing
rules are documented in [the message JSON contract](docs/message-json.md).
See [type source and license review](docs/type-sources.md) for pinned references.

### Subagent forwarding

`Open` owns one native stream and enables `forwardSubagentText` during
initialization. It exposes lossless native frames and explicitly marked SDK
association/observation records. Forwarding support remains unverified until
there is runtime evidence. **SubagentCreated is conditionally implemented for
exact CLI 2.1.258**, the locally installed version checked with `--version`.
Creation requires the fixed foreground or corroborated successful Agent result
criterion; ordinary task start, tool input and SubagentStart alone do not prove
creation. Associated remains separate. See the [creation rules](docs/subagent-events.md#creation-evidence).
See the [per-capability matrix](docs/subagent-compatibility.md) for pinned versions,
E1/E2 evidence and foreground/background/nested/legacy limits, plus the
[API and current limits](docs/subagent-events.md). No E3/E4 subagent checks ran.

A self-contained example uses only a local synthetic peer (no installed CLI or
model):

```sh
mise exec go@1.26.9 -- env GOPROXY=off GOSUMDB=off CLAUDE_E2E=0 go run ./examples/subagents-offline
```

It prints source/parent/run records through a single Open/Receive consumer, continues
after the parent result, observes patch-only terminals and explicitly closes.
Session-state events need a native opt-in on applicable CLIs; background task
snapshots replace a process-local set and do not prove termination. See the [offline checks](docs/ci.md).

中文事件时序说明：[前台/后台任务、子流、审批和结束边界](docs/event-sequences.md)。

### Native event stream

`Open` returns `*Client`; `Receive` returns `ReceivedEvent`. Type-switch on
`Event`, `SubagentAssociated`, `SubagentCreated`, or `ObservationState`.
Native values arrive directly, with unchanged fields, JSON and `Raw` bytes.
`Event.Origin` remains the official `*MessageOrigin` object. Relationship tracking
and child forwarding are automatic. `Options.Subagents` sets bounds and verified
history; `Options.Types()` omits this runtime configuration. See the
[API comparison with pinned official examples](docs/api-comparison.md) and
[derived definitions](derived_events.go).

[Recorded wire replay](docs/record-replay.md) provides an offline real CLI authentication-failure sample. Successful model/tool recording remains blocked; it is not live E2E coverage.

## Load an existing session and replay history

```go
loaded, err := claude.Load(ctx, claude.LoadOptions{
    Options: claude.Options{CLIPath: claudePath},
    History: claude.HistoryOptions{
        SessionID: id, CWD: absoluteCWD, ConfigDir: absoluteClaudeConfigDir,
    },
})
// Check err, then defer loaded.Close().
for event := range loaded.Events() {
    // Use the same func(claude.ReceivedEvent) handler as Client.Receive().
    handleEvent(event)
}
<-loaded.Done()
// Check loaded.Err(), then obtain the ordinary resumed Client:
client, err := loaded.Client()
// Check err; client.Send(newContext, text) and client.Receive continue the session.
```

Load validates the stored main chain and starts the normal CLI resume process
before delivering events. It never sends historic content back as prompts.
Client returns ErrLoadPending until replay finishes successfully. Open with
Options.ResumeID remains ordinary resume without SDK history replay. The load
context controls startup and replay; canceling it after success does not close
the Client. LoadedSession.Close cancels replay and closes the owned child.

Each replayed value is the normal native `claude.Event`, satisfying
`claude.ReceivedEvent`. User/assistant content, thinking, tool blocks and retained
system/progress/attachment data use the ordinary decoder and preserve unknown
data. `event.History` marks replay and retains the exact original transcript
bytes, parent UUID and recorded timestamp. `event.Raw` contains the projected
native-shaped JSON with validated `session_id`; it is not an original live wire
capture. SDK history metadata is excluded from native JSON serialization.
History never passes through live permission or execution-state reducers and
does not manufacture permission handles, tool execution or subagent creation.
The old `Records` snapshot interface has been replaced by `Events`.

The selected retained main chain includes compaction summaries, not abandoned
branches, standalone subagent files or deleted content. Unknown blocks remain
raw; corrupt/unsupported records fail explicitly. Local transcript loading
supports Unix, exact project keys up to 200 characters and quiescent regular
files. ConfigDir is also fixed in the child environment. RequestedResumeID stays
separate from observed SessionID: native initialize does not itself report the
session ID, and the SDK does not invent an observation from a request. Later
known root system/result identity mismatches terminate the loaded Client.
See the [history contract](../harness/contracts/async-history.md).
