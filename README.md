# Claude Agent SDK for Go

An experimental, independent native Go client for a locally installed Claude Code CLI. No other Go SDK or language runtime is required by the client. Original code is licensed under Apache-2.0.

**Implementation is in progress. This is not a P0 release or a production-ready SDK.** Live Claude CLI compatibility is unverified. The protocol baseline is the official Python SDK at [`f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab`](https://github.com/anthropics/claude-agent-sdk-python/tree/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab), which references CLI 2.1.302. Synthetic tests do not establish real runtime interoperability or official endorsement.

## Implemented so far

- Local process startup with literal argv, explicit CLI path/workdir/model, bounded and correlated initialization
- Synchronous complete-frame `Send` and single-consumer `Receive`
- Explicit native resume, separate requested/observed session identity, and correlated `Interrupt` acknowledgments
- Typed system, assistant, user, result, stream-delta, tool-use and tool-result views, with lossless raw JSON for unknown types, fields and blocks
- Fail-closed `can_use_tool` replies, observable incoming control requests, and explicit error replies for unsupported controls
- Bounded event/control queues, frame sizes and stderr tails; explicit overflow and protocol errors
- Independent `Done` and `Err`, retained accepted events, idempotent concurrent `Close`, and owned child reaping with bounded escalation

Deferred permission handles, per-request cancellation guards and deeper overload/lifecycle hardening are the remaining P0 tickets. All five tickets and independent review are required before the first experimental release.

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

A successful `Send` means only that the full NDJSON frame was written. It is not proof of acceptance, execution, or exactly-once delivery. `errors.Is(err, claude.ErrUnknownOutcome)` marks ambiguous writes/control waits; the SDK never automatically retries them. Canceling an in-flight write closes the connection to unblock that write. Canceling a `Receive` wait leaves the session running.

A result ends one turn, not the process. Always close the client. `Done` is independent of event-buffer capacity; drain `Receive` to obtain accepted events before its terminal error. `Err` exposes the latest known failure, while `Close` waits for cleanup and final diagnostics. `Error.Stderr` contains a bounded diagnostic tail that may be sensitive; the SDK never logs it automatically.

Incoming `can_use_tool` requests are denied in this initial implementation. This is not a sandbox: CLI settings, previously granted permissions and operations that do not prompt still apply. An assistant `AskUserQuestion` content block is observable data and causes no synthetic reply.

`Options.Env == nil` inherits the current environment; a non-nil slice replaces it. Defaults are 60 seconds for initialization, 3 seconds for shutdown escalation, 128 queued events, 4 MiB per frame, 32 pending outbound controls, 32 queued incoming controls, and an 8 KiB stderr tail. Zero-valued fields select defaults. Overflow terminates the connection visibly, retaining only already accepted events. POSIX process-group signaling is implemented; descendant teardown and other platform behavior are not yet comprehensively verified.

## Native resume and interrupt

`client.SessionID()` returns `(id, true)` only after a nonempty native ID arrives in a system or result frame. Before that it returns `("", false)`. It reports the most recently observed ID and remains available after closing. `client.RequestedResumeID()` separately returns the original request, which is never treated as observed identity.

To request resume, open a fresh process with `claude.Options{CLIPath: "claude", ResumeID: id}`. The ID is bound literally as `--resume=<value>`, including dash-leading values. Native history belongs to the CLI: the SDK does not replay earlier user messages, restore pending permission requests, adopt an old process, or provide durable recovery. Resume requires whatever persisted history and configuration the CLI itself needs. The fixture tests verify argv and observed IDs, not live persisted-history continuation.

`client.Interrupt(ctx)` sends an interrupt control request and waits for that request's acknowledgment. Concurrent interrupts have independent IDs; unknown, duplicate, and stale response IDs cannot complete another waiter. An explicit rejection returns `*claude.ControlError`. Success acknowledges the control only: it does not promise tool rollback, close the process, or consume a later turn result. Continue receiving events to observe the turn's outcome.

Cancellation before a write starts emits no request. Cancellation while waiting after a completed write returns an unknown-outcome error and leaves the connection available; cancellation during the write can terminate the connection to unblock it. Disconnect and write failure wake pending controls. The SDK never retries an interrupt automatically.

## Development and verification

Go is pinned to 1.26.9 in `mise.toml`. This module is standard-library-only.

```sh
mise exec -- go version
mise exec -- go test -timeout 60s ./...
mise exec -- go test -race -timeout 120s ./...
mise exec -- go vet ./...
```

Tests exercise exported APIs against a scripted subprocess with a replacement environment and isolated temporary HOME/workdir. They cover exact initialization/user/interrupt envelopes, literal resume without replay, requested versus observed native IDs, immediate and out-of-order control replies, unknown/stale IDs, timeout/response/close races, startup failures, preserved unknown data, Unicode/chunk framing, errors after accepted events, queue overflow, fail-closed permissions, direct write errors, cancellation, concurrent sends/close, and POSIX child reaping. The public examples are compiled, not executed.

Protocol and behavioral references: [official Agent SDK overview](https://code.claude.com/docs/en/agent-sdk/overview), pinned Python SDK `query.py`, `subprocess_cli.py`, `types.py`, and its `test_query.py`, `test_message_parser.py`, `test_subprocess_buffering.py`, `test_close_cancellation.py`, `test_session_resume.py`, `test_streaming_client.py`, and `test_transport.py`. The implementation and Go tests are original, not copied from those sources or a rival Go SDK.

See [the design](docs/design/sdk-design.md) for the full planned P0 contract and deferred scope.
