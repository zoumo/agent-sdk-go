---
type: Design
title: SDK tests with the agent-sdk-testify Go library
status: stable
---

# Go library mode

This page is the accepted in-process contract between `agent-sdk-go` tests,
simulation and the `agent-sdk-testify` Go library. Tests link the library into
their own process. They do not execute, install or locate a separate testify
program.

The library owns fixture preparation, recording/replay matching, history
materialization, execution evidence and typed reports. The SDK owns the process
under test, public SDK operations, business assertions, policy comparison,
cancellation and its simulation orchestration.

## Exact dependency

The repository uses this exact test dependency:

```text
require github.com/zoumo/agent-sdk-testify v0.1.2
sum h1:7pO42y7Dg8NXbQNg51d02+TKLzjmQ8WyW4EvCsCZ51k=
```

The tag resolves to revision
`ec80d165277190756b6506594141436a9bdcab97`. The exact version and checksum
appear in `go.mod`, `go.sum` and `internal/mocktest/testify_identity.go`.
`TestTestifyIdentityMatchesModulePin` guards agreement between those sources.
A version or checksum change is an explicit reviewed dependency update, not a
runtime override.

The module is test-only. A module-graph gate must prove that `codex`, `claude`,
`agent`, `codex/types`, `claude/types` and other production trees do not import
it. There is no committed `replace`, nested module, `go.work` or local checkout.

## Boundary and ownership

Library mode has these properties:

- The SDK sends and receives native protocol bytes through directional
  `io.ReadCloser` and `io.WriteCloser` endpoints. The transport runs the same
  protocol, Session, cancellation and close state machines as an owned native
  process without starting a fake native program.
- The SDK-owned simulation driver is a normal process boundary for exercising
  the public SDK. It may import this repository's test bridge and the testify
  module. It does not proxy protocol bytes to another testify process.
- `internal/mocktest` owns fixture lifecycle, SDK-side stream construction and
  typed evidence views. It contains no protocol matcher and imports neither
  native SDK.
- The caller owns prepared and exported directories. The library owns its
  in-process fixture resources and the connection endpoints transferred at
  startup. Every successful prepared fixture is removed after its execution
  evidence has been read, including after failure or cancellation.

## Embedded identity

The linked library exposes an embedded tool identity:

```go
type ToolIdentity struct {
    Source         ToolSource
    Version        string
    Module         string
    ModuleSum      string
    ModuleRevision string
    ModuleTime     string
    GoVersion      string

    Command string
    Args    []string
    SHA256  string
}
```

For this repository, `Source` is `ToolSourceEmbedded`; `Module` is
`github.com/zoumo/agent-sdk-testify`; `Version`, `ModuleSum`, module revision
and Go version must match the linked binary. `Command`, `Args` and `SHA256`
must be empty. Fixture and execution descriptors carry this identity under
schema 5. Opening a fixture whose embedded identity differs from the linked
module fails closed.

Fixture material identity is separate from module identity. It binds the case
or recording source, plan, manifest and connection set. Neither identity grants
permission to traverse library storage outside the public API.

## Directional mock streams

The in-process entry accepts two directional streams:

```go
type StartMockAgentStreams struct {
    Reader io.ReadCloser // SDK -> fixture
    Writer io.WriteCloser // fixture -> SDK
    Stderr io.Writer     // diagnostics; must be non-nil
}
```

`Reader`, `Writer` and `Stderr` must be non-nil. `Reader` and `Writer` must be
different objects. Their `Close` methods must unblock concurrent I/O.
`io.Pipe`, `net.Pipe` and `os.Pipe` satisfy the contract.

The caller creates both pipe pairs and transfers the fixture-side endpoints to
`StartMockAgent`. After that call returns, the library owns and closes those
fixture-side endpoints, including after timeout, abort or failure. It never
closes the SDK-side reader that consumes server frames or the SDK-side writer
that produces client frames. `Stderr` is not part of the protocol and is never
closed by the library.

Each direction has one producer. The SDK closes its outbound writer to signal
end of input. The fixture then consumes all remaining input, validates strict
replay, closes its outbound writer to signal end of output, and finalizes
evidence. When the writer implements `CloseWithError`, the fixture may use it
to make a blocked SDK reader observe the replay failure.

## Start and completion

An in-process mock starts through a handle:

```go
func (f *Fixture) StartMockAgent(
    ctx context.Context,
    options MockAgentOptions,
    streams StartMockAgentStreams,
) (*MockRun, error)

type MockRun struct {
    Start ExecutionStart
}

func (r *MockRun) FullConsumption() <-chan struct{}
func (r *MockRun) Done() <-chan struct{}
func (r *MockRun) Wait(ctx context.Context) (*MockAgentResult, error)
func (r *MockRun) Abort() error
```

`StartMockAgent` returns after validating the connection, creating or joining
the execution, claiming the connection and publishing its immutable start
state. It performs no protocol read or write before returning. A failure after
the claim returns both a non-nil handle and an error so `Wait` can expose the
final result. Preflight failure returns no handle.

`MockAgentOptions` selects a declared connection and may join a multi-connection
execution:

```go
type MockAgentOptions struct {
    Connection  string
    ExecutionID string // empty allocates; non-empty joins this execution
    Timeout     time.Duration
    NativeArgs  []string
}
```

An empty execution ID creates a new execution. A non-empty ID may join only an
open execution from the same fixture, material, timeout and native declaration,
and only to claim an unstarted declared connection. Callers do not infer the
latest execution from directory state.

`ExecutionStart` is derived from the actual execution:

```go
type ExecutionStart struct {
    FixtureID       string
    ExecutionID     string
    ConnectionID    string
    Material        Material
    ExecutionSHA256 string
    ExecutionPath   string

    Workspace       string
    ProtocolCwd     string
    Home            string
    Config          string
    Tmp             string
    HistoryFiles    map[string]string
    Env             map[string]string
    NativeArgs      []string
    Transport       MockTransport
}
```

The SDK host uses these actual paths and this environment. It does not substitute
values from a pre-launch template. `Env` may contain private runtime values and
must not be copied into public reports, logs or receipts.

### Full-consumption half-close

`FullConsumption` is a one-shot channel with capacity one. It fires after all
recorded frames have completed, pending native requests have been validated and
the configured handshake assertion has passed. It fires before the fixture
checks for an extra client byte or client EOF.

A watcher selects on both `FullConsumption` and `Done`. On full consumption it
calls the SDK's idempotent `CloseInput`, which closes only the SDK outbound
writer. It does not close the SDK reader, close a fixture endpoint, call
`Client.Close`/`Session.Close` or wait for `MockRun.Wait` from the replay
goroutine. A failure before full consumption closes `Done` without sending on
`FullConsumption`.

The execution timeout remains active while the fixture waits for input EOF.
The SDK must finish all writes before calling `CloseInput`; a later protocol
write is an SDK caller error. Final server frames remain readable until the
fixture closes its outbound writer.

### Result and typed evidence

`Wait` is the only final result path. It waits for replay, evidence
finalization and endpoint cleanup, then returns `MockAgentResult`. Cancellation
of `Wait` performs the same bounded abort as `MockRun.Abort`, closes fixture-side
endpoints and returns the cancellation cause. `Abort` is idempotent and cannot
produce a successful result. `Done` closes after all run goroutines and
finalization have ended.

Typed evidence is read from the library rather than inferred from storage:

```go
func (f *Fixture) ExecutionReport(id string) (*ExecutionReport, error)

type ExecutionReport struct {
    FixtureID       string
    ExecutionID     string
    Status          ExecutionStatus
    Complete        bool
    Material        Material
    ExecutionSHA256 string
    ExecutionPath   string
    Connections     []ConnectionReport
}

type ConnectionReport struct {
    ID     string
    Status ConnectionStatus

    Transport       MockTransport
    FullConsumption bool
    ClientEOF       bool
    NativeLaunched  bool
    CleanupComplete bool
    ExitCode        *int
    Failure         FailureClass
    Error           string

    ReceiptPath string
    ResultPath  string
}
```

The report contains every declared connection, including pending connections,
and fails closed on missing, mismatched or corrupt evidence. A passed connection
requires full consumption, client EOF, completed cleanup and no failure class.
An embedded connection has transport `in-process`, `NativeLaunched=false` and a
nil exit status. It never records a host-program identity as the mock.

`MockAgentResult.Execution` is the same report snapshot returned by `Wait`.
`Fixture.ExecutionReport` re-reads authoritative final evidence after all
connections finish. Reports are immutable copies and contain no environment
values, credentials or raw protocol frames.

## SDK stream transport

Both native SDKs expose a transport-only construction path:

```go
type StreamTransport struct {
    Reader io.ReadCloser // peer -> SDK
    Writer io.WriteCloser // SDK -> peer
    Stderr io.ReadCloser // optional; nil discards diagnostics
    Wait   func(context.Context) error
}
```

The SDK owns and closes all supplied endpoints on failure and `Close`. `Wait`
joins `MockRun.Wait` for a testify-backed peer and is bounded by the SDK's
shutdown timeout. Reader EOF alone is not success; it must agree with full
consumption, input half-close and a nil `Wait` result.

Each client exposes an idempotent input half-close:

```go
func (*codex.Client) CloseInput() error
func (*claude.Session) CloseInput() error
```

The first successful call closes only the outbound writer and returns nil.
Later calls return `ErrInputClosed` without closing another endpoint. A queued
or in-flight write interrupted by the half-close also reports
`ErrInputClosed`, but does not turn a completed replay into a transport
failure. Normal Session cleanup tolerates a writer that its full-consumption
watcher already half-closed.

Terminal completion has three distinct paths:

- **Normal completion:** input was half-closed, all final frames were consumed,
  the fixture closed its writer and `Wait` returned nil.
- **Caller abort:** `Close` begins before normal completion. Both directions are
  closed to unblock I/O, and replay fallout from those closes does not replace
  the intentional terminal cause.
- **Transport failure:** a read/write error occurs or `Wait` returns non-nil.
  That cause remains authoritative in `Client.Err`/`Session.Err` and `Close`.

### Codex

```go
type StreamOptions struct {
    Args       []string
    Env        []string
    Dir        string
    Initialize types.InitializeParams
    Limits     Limits
}

func NewStreams(
    ctx context.Context,
    options StreamOptions,
    transport StreamTransport,
) (*Client, error)
```

`NewStreams` performs the same initialize/initialized exchange as `NewStdio`.
`Args` must be the validated mapping of `ExecutionStart.NativeArgs` and must
match `MockAgentOptions.NativeArgs`. `Dir` is `ExecutionStart.ProtocolCwd` and
`Env` is the effective execution environment. Stream construction does not call
`os.Chdir`, mutate the process environment, search `PATH` or start a process.
The recorded `ThreadStartParams.Cwd` remains part of the business operation.

### Claude

```go
func OpenStreams(
    ctx context.Context,
    options SessionOptions,
    transport StreamTransport,
) (*Session, error)

func NativeArgs(options SessionOptions) ([]string, error)
```

`NativeArgs` is the single native argument-vector implementation used by both
`Open` and `OpenStreams`. `OpenStreams` ignores `CLIPath`, performs no executable
lookup and starts no process. `WorkDir` comes from `ExecutionStart.Workspace`;
the effective environment comes from `ExecutionStart.Env`. Config and history
queries therefore use the actual execution rather than host process state.

## Connection flow

The SDK host and fixture use opposite ends of two directional pipes:

```text
SDK Writer  ->  fixture Reader
SDK Reader  <-  fixture Writer
```

One connection proceeds as follows:

1. Prepare or open a caller-owned fixture in the current process.
2. Start `StartMockAgent`, retain `MockRun` and read `run.Start`.
3. Construct `codex.NewStreams` or `claude.OpenStreams` from the actual start
   paths, environment and native arguments.
4. Watch `FullConsumption` and `Done`. On full consumption, call `CloseInput`.
5. Let the fixture validate input EOF, finish replay and close its writer.
6. Close the SDK Session/Client, wait for every `MockRun`, and read the typed
   execution report for each exact execution ID.
7. Export when persistent material is required, then remove every prepared
   fixture directory.

For remote-close simulation, the SDK-owned loopback bridge adapts authenticated
WebSocket frames to the same directional streams. The mock remains in the
simulation process.

## Failure, cancellation and cleanup

Cleanup is registered immediately after prepare and uses a separate bounded
context. The fixed shutdown order is:

1. Close the SDK Session/Client and wait for SDK-owned goroutines. Half-close
   input first when protocol completion did not do so.
2. Abort or close every fixture-side stream and wait for every `MockRun`.
3. Read typed reports for the exact execution IDs.
4. Export required material.
5. Remove caller-owned prepared fixture directories.

A scripted disconnect remains a failed replay. Expected failures assert the
report's failure class, full-consumption, client-EOF and cleanup facts. They do
not weaken strict matching. Cancellation closes both directions within the
execution timeout, and no reader or writer goroutine may outlive `Wait` or
`Done`.

## Repository integration

`internal/mocktest` is the shared test bridge. It:

- imports the exact testify library;
- prepares and opens fixtures;
- creates SDK-side directional pipes;
- returns `ExecutionStart` and typed reports;
- removes fixtures it owns;
- contains no protocol matcher and imports neither native SDK.

Native tests use `NewStreams`/`OpenStreams` directly. Synthetic inputs are
SDK-owned captures passed to `agenttestify.Prepare`. Simulation keeps the SDK
driver process as the public SDK host; the driver receives exported material and
uses the bridge for every mock connection.

The SDK simulation process owns prepare/export, coverage and typed evidence
validation. It validates schema 5, embedded identity, source identity, the mock
route and the complete connection set before any protocol I/O. Export goes to a
new directory outside the fixture. The driver runs the public SDK business flow
and the simulation process binds the result to exactly one execution and
material.

## Adoption contract

Adoption is accepted when the offline gate proves all of the following:

- `go.mod`, `go.sum` and the embedded identity agree on the exact version,
  checksum and revision.
- No production package imports the testify module, and the test bridge does not
  import `os/exec`.
- Codex and Claude complete in-process replay through public SDK stream APIs,
  including a bounded `Wait`.
- Full consumption precedes `CloseInput`; final server frames remain readable;
  normal completion, abort and transport failure remain distinct.
- Cleanup after full-consumption half-close does not emit `ErrInputClosed` or
  falsely clear ownership.
- Typed reports prove strict replay, full consumption, client EOF, incomplete
  or complete cleanup and `NativeLaunched=false` for every embedded connection.
- Fixture and execution descriptors use schema 5 and fail closed on identity,
  material or schema mismatch.
- HTTP, socket, generic UI and unrelated process checks cannot substitute for
  the offline library gate.

Real Agent/model execution and new recordings remain explicit opt-in work and
are not part of this adoption gate.
