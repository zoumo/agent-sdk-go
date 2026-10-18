# Offline CLI peers

Build a deterministic protocol peer, select it through the real SDK's executable
option, and test your application without an installed agent, account or model.
The SDK, process transport, request handles and permission handling remain real.
The peer executes an explicit conversation; it never reasons about prompts,
executes tools, starts another agent, or falls back to a real executable.

Install either command from a published version or a pushed full commit SHA:

```sh
go get github.com/zoumo/agent-sdk-go@FULL_PUSHED_COMMIT_SHA
go build -o mock-codex github.com/zoumo/agent-sdk-go/codex/cmd/mock
go build -o mock-claude github.com/zoumo/agent-sdk-go/claude/cmd/mock
```

Run those commands inside a consumer module that requires
`github.com/zoumo/agent-sdk-go@<version-or-SHA>`. Building explicit output paths
keeps both commands available; their source directories share the basename `mock`.
No repository source path, `replace` directive or `go.work` file is needed.

## Select a scenario with the normal SDK options

The following fragments assume a caller context `ctx` and absolute executable
and scenario paths. Handle the returned errors and close each client:

```go
client, err := codex.NewStdio(ctx, codex.StdioOptions{
    Executable: "/tmp/bin/mock-codex",
    Args: []string{"app-server"},
    Env: []string{"CODEX_MOCK_SCENARIO=/tmp/codex-scenario.json"},
    Initialize: codextypes.InitializeParams{
        ClientInfo: codextypes.ClientInfo{Name: "consumer", Version: "1"},
    },
})
```

```go
client, err := claude.Open(ctx, claude.Options{
    CLIPath: "/tmp/bin/mock-claude",
    Env: []string{"CLAUDE_MOCK_SCENARIO=/tmp/claude-scenario.json"},
    PermissionHandler: func(request *claude.PermissionRequest) {
        // Hand the original handle to your application/UI. Answer it explicitly.
    },
})
```

`Env` replaces the child environment. These peers require only the scenario
selection variable. They do not read a home directory or account configuration.
Use the native SDK imports from the [root README](../README.md).

Codex accepts `app-server`, `--listen stdio://` and `--scenario PATH` (also
`--scenario=PATH`). An explicit scenario option overrides `CODEX_MOCK_SCENARIO`.
The first release supports stdio only. It does not implement a remote WebSocket
listener, authentication, reconnects or network fault injection; `NewRemote`
cannot connect to this CLI. Unsupported listener values and startup arguments
fail before protocol output.

Claude accepts the SDK's `--input-format stream-json`, `--output-format
stream-json`, `--permission-prompt-tool stdio`, `--verbose` and
`--include-partial-messages` flags, plus `--model=value` and `--resume=value`.
These options validate startup compatibility; the script still determines the
conversation and does not restore a real session. Unknown or duplicate flags
fail. Neither command runs a version probe.

## Scenario format and matching

Both tools expose `tools/mock.Scenario`, `Load(io.Reader)` and
`Run(context.Context, io.ReadCloser, io.WriteCloser, Scenario)`. A JSON document
contains `version: 1`, `provenance` and an ordered `frames` array. Unknown
wrapper/frame fields, unsupported versions and trailing JSON are rejected.
Native message payloads retain their native fields, including unknown fields.

Codex example containing SDK initialization only:

```json
{
  "version": 1,
  "provenance": "synthetic",
  "frames": [
    {"ordinal": 1, "direction": "c2s", "channel": "protocol", "epoch": 1,
     "wire": {"id": 91, "method": "initialize", "params": {"clientInfo": {"name": "consumer", "version": "1"}}}},
    {"ordinal": 2, "direction": "s2c", "channel": "protocol", "epoch": 1,
     "wire": {"id": 91, "result": {"userAgent": "synthetic", "codexHome": "/synthetic", "platformFamily": "unix", "platformOs": "linux"}}},
    {"ordinal": 3, "direction": "c2s", "channel": "protocol", "epoch": 1,
     "wire": {"method": "initialized"}}
  ]
}
```

Claude uses `direction: "client"` or `"server"`, `channel: "control"` or
`"protocol"`, sequential ordinals starting at one, and `data` for native JSON:

```json
{
  "version": 1,
  "provenance": "synthetic",
  "frames": [
    {"ordinal": 1, "direction": "client", "channel": "control",
     "data": {"type": "control_request", "request_id": "init", "request": {"subtype": "initialize", "hooks": null, "forwardSubagentText": true}}},
    {"ordinal": 2, "direction": "server", "channel": "control",
     "data": {"type": "control_response", "response": {"subtype": "success", "request_id": "init", "response": {}}}}
  ]
}
```

Client request IDs bind to the SDK's actual IDs and corresponding server replies
are rewritten with those IDs. Native server request IDs remain explicit and the
application must answer the exact request handle. All other client fields must
match structurally. Whitespace and JSON object key order are immaterial;
unknown, missing or mismatched requests and duplicate request IDs fail. There is no wildcard
that silently accepts arbitrary application behavior. There are no implicit
startup messages to omit from the script.

Extend the sequence with server requests/control requests, expected answers,
notifications, result/error messages and interleaved child events. This supports
application tests for Codex user questions/tool approvals, Claude tool permission
and `AskUserQuestion` input updates, explicit interrupt requests, server-side
permission cancellation, completion, failures and child event routing. An
assistant text/tool-use event alone does not grant permission-answer authority.
The mock sends only the scripted JSON; successful SDK observation still depends
on that SDK's native event and relationship rules.

Delays are explicit: Codex `delayMs`, Claude `delay_ms`, applied before the frame.
Captured elapsed timestamps do not introduce implicit sleeps. A final Codex
`{"ordinal": N, "direction": "s2c", "epoch": 1, "disconnect": true}` action
has no `wire`; Claude uses `{"ordinal": N, "direction": "disconnect",
"channel": "protocol"}` without `data`. Disconnect deliberately allows pending
work to fail. Ordinary completion requires every frame and request to finish,
then client EOF; extra client frames and early EOF fail. Close your owned pipe
ends when using the library. SDK `Close` initiates subprocess shutdown; check its
error and diagnostics. Use a direct subprocess or runner when asserting an exact
peer exit code, since Codex exposes bounded diagnostics rather than a process exit code.

## Direct library use

Import the native mock package and use the same runner as the CLI:

```go
import (
    "context"
    "io"
    codexmock "github.com/zoumo/agent-sdk-go/codex/tools/mock"
)

func serve(ctx context.Context, scenarioJSON io.Reader, input io.ReadCloser, output io.WriteCloser) error {
    scenario, err := codexmock.Load(scenarioJSON)
    if err != nil { return err }
    return codexmock.Run(ctx, input, output, scenario)
}
```

For Claude, replace the import with
`github.com/zoumo/agent-sdk-go/claude/tools/mock`; the entry points have the same
signatures and native scenario/frame types. Both runners own and close their
streams; `Close` must unblock I/O, as with files, connections and Go pipes.
Both reuse their native `tools/replay` matching and ID engine. Neither native
SDK has a mock mode or a runtime dependency on these tools.

| Limit / exit | Codex | Claude |
|---|---|---|
| Scenario size | 8 MiB | 16 MiB |
| Input / output traffic | 8 MiB each | 16 MiB each |
| Frames | 10,000 | 10,000 |
| Native frame size | 2 MiB | Below 2 MiB including wrapper |
| Scenario deadline | 30 s default; `timeoutMs` up to 300,000 | 10 s maximum |
| Explicit delay | 0–300,000 ms per frame | 0–30,000 ms per frame |
| Exit 0 | Complete EOF | Complete EOF |
| Exit 1 | Mismatch, incomplete or I/O | Mismatch, incomplete or I/O |
| Exit 2 | Invalid configuration/scenario | Invalid configuration/scenario |
| Exit 3 | Timeout/cancellation | Timeout/cancellation |
| Exit 4 | Intentional disconnect | Intentional disconnect |

A shorter caller deadline wins. Stdout contains protocol JSON lines only;
diagnostics go to stderr. The tools retain their native frame formats and share
these exit codes.

## Recording provenance and validation

Use `provenance: "synthetic"` for authored scenarios, including generated child
events, deliberate failures and modified recordings. Use `"recording"` only for
unaltered capture frames. Both require `source` identifying the capture and reject
synthetic delay or disconnect additions when provenance is recording. Retain the
original capture manifest and hashes separately. Wrapping a
recording does not establish model intelligence, current CLI compatibility, or
live subagent evidence. The retained [recording index](recordings.md) describes
actual Codex tool/question evidence and Claude authentication-failure evidence;
existing capture bytes and manifests are preserved.

For example, adapt an embedded Codex capture without duplicating its fixture:

```go
frames, err := recording.Load("s2") // codex/tools/replay, package recording
if err != nil { return err }
scenario := codexmock.Scenario{
    Version: codexmock.Version,
    Provenance: "recording",
    Source: "codex real-sanitized/s2; retain the original manifest",
    Frames: frames,
}
return codexmock.Run(ctx, input, output, scenario)
```

Use the capture's exact initialization, thread and turn parameters; the public
`recording.Params` helper extracts them. This strict capture is a scenario source,
not a wildcard for different application requests. Claude capture frame wrappers
can likewise be decoded into `mock.Frame`; its retained capture exercises only
authentication failure. Author successful Claude conversations as synthetic.


[External consumer tests](../tests/consumers/mock/mock_test.go) demonstrate
complete synthetic initialization, two-way question/permission handling,
interrupts, canceled caller contexts, disconnection and strict mismatch failures.
Paused-consumer stress sends 2,048 ordered native events through each built mock
CLI with the legacy event capacity set to one. Question/permission answers and
control replies finish before events are read; accepted events then drain in order
after the synthetic disconnect. These are synthetic stress cases, not recordings.
The consumer gate fetches the precise pushed module SHA into a fresh module and
cache, builds both commands by public import path and runs those tests against
the real SDKs. Downloads occur first; test execution then disables module network
access. No real agent CLI is installed or invoked:

```sh
mise exec -- go test ./codex/tools/mock ./claude/tools/mock
mise exec -- bash scripts/check-consumers.sh FULL_PUSHED_COMMIT_SHA
```

These peers expose SDK behavior; they do not repair it. Current native tests and
source audit identify these limits, which are not a permanent API promise: Codex
late responses after local call timeout count as unmatched responses, queued
approval handles may remain writable after resolution, and duplicate `item/completed` keys
follow native deduplication. See native compatibility and observation docs for
other SDK limits. Synthetic passing tests establish the chosen offline paths;
they do not certify unmodeled real CLI behavior.
