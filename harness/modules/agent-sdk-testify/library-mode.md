---
type: Design
title: SDK tests with the agent-sdk-testify Go library
status: proposed
---

# Go library mode

This page defines the proposed in-process replacement for the external
`agent-sdk-testify` process in Go tests. The accepted process contract in
[the tool design](design.md) remains in force until this proposal is adopted.

The goal is that a Go test can link
`github.com/zoumo/agent-sdk-testify` and use its prepare, replay, history and
evidence logic in the current test process. The test must not need an
`agent-sdk-testify` executable, an inherited result/progress file descriptor, a
launcher script, or a helper process that impersonates the tool.

## Scope and boundaries

Library mode has these non-negotiable properties:

- `agent-sdk-go` resolves testify from an exact Go module revision. No published
  release is required; an exact commit pseudo-version is sufficient. `latest`,
  branches, `go.work` and committed `replace` are not allowed.
- The library is a test and simulation dependency only. It must not appear in
  the production dependency graph of `codex`, `claude`, `agent`, `codex/types`,
  or `claude/types`.
- The SDK sends and receives protocol bytes through directional byte streams.
  Their public contracts are `io.Reader`/`io.Writer`, but the transport accepts
  closeable endpoints (`io.ReadCloser`/`io.WriteCloser`) so cancellation can
  unblock I/O. It does not turn those endpoints into a fake native CLI and does
  not relay them through an adapter process.
- The library owns protocol matching, replay completion, history checks,
  execution receipts and material identity. The SDK owns business assertions,
  Session behavior, policy comparison and its own driver process.
- The SDK-owned driver process is allowed: it is the code under test and may
  import the testify library. A process that mimics `agent-sdk-testify`, calls
  its CLI, or re-executes the test binary as a fake protocol peer is not
  allowed.
- Native `os/exec` remains valid for the SDK's real CLI paths and for synthetic
  SDK-owned protocol peers. Those processes are not substitutes for testify.

The library and CLI continue to share fixture layout, locking, strict replay
and material rules. Library mode changes the in-process adapter, not the
meaning of a recording or a successful execution.

The required upstream library surface is prepare/open/export plus `MockAgent`,
`StartMockAgent` and typed execution evidence (and the existing record path
where synthetic capture is needed). The CLI's official multi-case orchestration
remains a separate command surface; SDK simulation owns its own case selection
and business flow and does not need that orchestrator to become importable.

The existing SDK process constructors cannot provide this seam. `NewStdio` and
`claude.Open` start an OS process and own its file descriptors, so passing an
`io.Pipe` through either constructor would still require an executable that
impersonates testify. A process-free constructor must instead install the
already-open endpoints in the same read/write state machines. Plain
`io.Reader`/`io.Writer` is insufficient for failure and cancellation: a blocked
read or write cannot be interrupted without a close operation. The transport
therefore uses directional closeable interfaces and exposes a separate input
half-close so the fixture can observe client EOF without losing final server
frames.

## Dependency and identity

`agent-sdk-go` records one exact testify commit in `go.mod`, for example a
commit pseudo-version such as `v0.0.0-<timestamp>-<revision>`. The matching
`go.sum` entries, the test binary's Go build information and the Go toolchain
version are evidence. A version bump is an explicit reviewed change; consumers
never resolve a moving branch or a local checkout. A tagged version, if used
later, is also pinned exactly.

An embedded fixture must not invent an executable path or an executable
SHA-256. Its public tool identity distinguishes the two forms:

```go
type ToolIdentity struct {
    Source         ToolSource // executable or embedded
    Version        string

    // executable only
    Command        string
    Args           []string
    SHA256         string

    // embedded only
    Module         string
    ModuleSum      string
    ModuleRevision string
    GoVersion      string
}
```

For an embedded identity, `Command`, `Args` and `SHA256` are empty.
`Module` must be `github.com/zoumo/agent-sdk-testify`. `ModuleSum`,
`ModuleRevision` and Go build information establish the reproducibility
identity; `Version` is an exact release, pseudo-version or audited development
identity, never merely `0.1.0-dev`. Embedded prepare records this identity in
the fixture descriptor, and `Open` validates it against the linked module build
information and fails on mismatch or a modified worktree. This covers the
two-binary simulation host and driver without treating a module path as an
executable. An embedded connection must likewise expose only its logical
identity:

```go
type Connection struct {
    ID        string
    Source    ToolSource
    Command   string   // executable only
    Args      []string // executable only
    Launcher  string   // executable only
}
```

For an embedded connection, `Source` is `ToolSourceEmbedded` and the three
executable fields are empty. Embedded routes must not expose a module path as
`Command`, generated CLI arguments, or a launcher that cannot be executed.
Callers select a connection by `ID`; they never launch it.

The persisted fixture and execution descriptor must represent this embedded
identity explicitly under schema 5. The current schema-4 encoding of the module
path in `tool.command` is not a valid target contract, and schema-4 fixtures are
rejected rather than silently reinterpreted. The identity is a discriminated
union: executable fixtures keep command, arguments and executable hash
semantics; embedded fixtures contain only module identity. Existing fixtures
whose source cannot be proved fail closed.
An embedded fixture has no executable for the `agent mock` process command; that
entry rejects it and never falls back to the test binary or the current process.
Conversely, `StartMockAgent` accepts only an in-process connection and rejects a
process-only connection rather than launching it.

## Testify library contract

### Directional mock streams

Record and process-backed mock keep their existing `*os.File` streams. The
in-process library entry uses a separate closeable transport:

```go
type StartMockAgentStreams struct {
    Reader io.ReadCloser // SDK -> fixture
    Writer io.WriteCloser // fixture -> SDK
    Stderr io.Writer     // optional diagnostics
}
```

`Reader` and `Writer` still satisfy the ordinary `io.Reader` and `io.Writer`
contracts. Making them closeable is required for bounded cancellation: generic
blocking readers cannot be interrupted otherwise. `Close` must unblock a
concurrent `Read` or `Write`; `io.Pipe`, `net.Pipe` and `os.Pipe` satisfy this
contract. `Reader`, `Writer` and `Stderr` must be non-nil. `Reader` and `Writer`
must not be the same object. The caller creates and transfers the fixture-side
endpoints; it must not reuse, concurrently close or replace them after
`StartMockAgent` returns.

The existing `MockAgent(... Streams)` process entry remains a thin adapter for
the CLI and recording path. Library consumers use `StartMockAgent` with
`StartMockAgentStreams`; both operations enter the same validation, execution
claim, replay and finalization state machine. Library mode does not require
`RecordAgent` or the CLI to be rewritten around generic streams.
The process adapter rejects a non-empty `ExecutionID`; explicit joining is
reserved for the in-process handle entry.

The fixture consumes `Reader` until the SDK closes its opposite writer, and
writes native server frames to `Writer`. `MockRun` owns and closes only these
fixture-side endpoints, including after an error, timeout or abort. It never
closes the SDK writer that produces input or the SDK reader that consumes
output. Each direction has one producer. The SDK closes its outbound writer to
give the fixture EOF; the fixture then closes its outbound writer to give the
SDK EOF. On a peer error, the fixture closes `Writer` and returns the cause in
the mock result and report. When the writer also implements
`CloseWithError(error)`, the fixture uses that optional operation so a blocked
SDK reader observes the cause directly.

`Stderr` is never part of the protocol stream and may be `io.Discard`. The
library must not close a caller-owned diagnostic sink that it did not create.

### Start handle and completion event

`MockAgentOptions` gains an explicit execution join key:

```go
type MockAgentOptions struct {
    Connection  string
    ExecutionID string // empty allocates; non-empty joins this execution
    Timeout     time.Duration
    NativeArgs  []string
}
```

The in-process entry returns a handle, not a blocking mock call:

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

`StartMockAgent` returns only after the fixture validates the request, allocates
or joins the execution, creates the execution overlay, claims the connection,
writes the running/start evidence and constructs an immutable `ExecutionStart`.
It performs no protocol read or write before returning. A replay goroutine may
start as soon as the handle is published. If failure occurs after the execution
claim, `StartMockAgent` returns a non-nil handle plus the error so `Wait` can
still expose the finalized failure. Preflight failures return no handle.

`ExecutionStart` is derived from the actual execution and is never taken from
the pre-launch route template:

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
    Transport       MockTransport // "in-process" or "process"
}
```

```go
type MockTransport string

const (
    MockTransportProcess  MockTransport = "process"
    MockTransportInProcess MockTransport = "in-process"
)

type FailureClass string

const (
    FailureClassConfiguration FailureClass = "configuration"
    FailureClassStrictReplay  FailureClass = "strict-replay"
    FailureClassTransport     FailureClass = "transport"
    FailureClassTimeout       FailureClass = "timeout"
    FailureClassCanceled      FailureClass = "canceled"
    FailureClassAborted       FailureClass = "aborted"
    FailureClassCleanup       FailureClass = "cleanup"
    FailureClassInternal      FailureClass = "internal"
)
```

`FailureClass` distinguishes at least `configuration`, `strict-replay`,
`transport`, `timeout`, `canceled`, `aborted`, `cleanup` and `internal`;
`Error` is diagnostic text and is not used for control flow.

`Env` is the effective execution environment: fixture baseline, execution
overlay and private runtime values merged into a fresh map. It is passed only
to the in-process SDK host and is never copied into public reports, receipts or
logs. `NativeArgs` is the saved native declaration, not a process argv. The SDK
must map supported arguments into its typed options and reject unknown,
duplicate or unsupported values before opening a client. It must not silently
ignore them or pass them to `exec.Command`.

`FullConsumption` is a one-shot event channel with capacity one. On success it
sends one token and closes the channel after all recorded frames have
completed, the native adapter has validated all pending requests and any
configured handshake assertion has passed, but before the library peeks for an
extra client byte or requires client EOF. The producer never waits for the
consumer. A receiver must select on both `FullConsumption` and `Done`, because a
failure before full consumption closes `Done` without closing or sending on
`FullConsumption`.

The SDK test host watches `FullConsumption` and calls the SDK's
`CloseInput` to produce fixture EOF. It must not call `Client.Close`/`Session.Close`,
close a fixture endpoint or the SDK reader, wait for `writeGate`/cleanup, or
wait for `MockRun.Wait` from that event goroutine. The fixture-to-SDK writer
remains open while the SDK consumes all final server frames and is closed only
after replay has completed. The execution timeout remains active while
awaiting EOF; a peer that never closes input fails instead of blocking forever.
The test must finish all normal protocol calls before half-closing input; any
write attempted after half-close is an SDK caller error, not permission to stop
checking extra input.

`Wait(ctx)` is the only final result path for a run. It waits for replay,
receipts and finalization and returns a typed `MockAgentResult`, including an
`ExecutionReport`. Cancellation of `Wait`'s context requests the same bounded
abort as `MockRun.Abort`, closes fixture-side endpoints, waits for the replays
and finalization to finish, and returns a canceled failure plus the context
error. `Abort` is idempotent and may run concurrently with `Wait`; it never
creates a successful result. `Done` closes after all run goroutines and
finalization finish. No `OnStarted` or `OnFullConsumption` callback is part of
the library contract.

This is the in-process replacement for the CLI progress descriptor. No file
descriptor is allocated, inherited or interpreted.

### Typed execution evidence

The current per-call result is not sufficient to decide whether a
multi-connection execution completed and whether every receipt proves strict
consumption. The library exposes a typed, validated view:

```go
func (f *Fixture) ExecutionReport(id string) (*ExecutionReport, error)
```

The report is computed by the library from its execution descriptor and
receipts and fails closed on missing, mismatched or corrupt evidence. Callers
must not need to parse `fixture.json`, `executions/index.json`,
`execution.json`, result notices or receipt JSON to make a pass/fail decision.

```go
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

    ReceiptPath        string
    ProcessReceiptPath string
    ResultPath         string
}
```

The report contains every declared connection, including pending connections.
Status decisions use these fields; evidence paths are for audit. The report
never contains environment values, credentials or raw protocol frames.
`FullConsumption=true` does not by itself prove success: transport, strict
replay, timeout and cleanup failures remain terminal. An abort cannot turn
`ClientEOF=false` into a normal completion, and a failed connection never
becomes `ExecutionComplete=true` merely because a later report was readable.

`MockAgentResult.Execution` is the same report snapshot returned by `Wait`.
`Fixture.ExecutionReport` re-reads the descriptor, index, receipts and result
from the fixture and creates the authoritative final view after all connections
finish. A report for an open execution may contain pending connections; it must
not fabricate terminal receipt facts. Without a valid report, `Wait` returns an
error or a failed report; it must not return `ExecutionComplete=true` by guess.

For an embedded connection, `Transport` is `"in-process"`,
`NativeLaunched` is false and `ExitCode` is nil.
The report must not copy the test binary path, forge an exit status, or call the
host executable a launched tool. `ProcessReceiptPath` is empty for an
in-process connection; a process connection keeps the real process receipt.

Reports are immutable copies. The caller may retain them after the run and
fixture cleanup.

### Multi-connection and cancellation semantics

One execution can contain several connections. Each connection has its own
`StartMockAgent` result and own run. Calls join an open execution only by an
explicit `ExecutionID`; an empty ID allocates a new execution and does not scan
for the latest pending directory. The non-empty ID must reference the same
fixture, current material, timeout and native declaration, and must claim an
unstarted declared connection. Join failure occurs before claiming a connection
and creates no empty execution. One execution must not mix CLI and library
connections. The first connection may return with `ExecutionStatusOpen`;
callers must wait for every declared connection and then read the typed
execution report.

On context cancellation the library closes both fixture-side streams to unblock
reads and writes, finalizes a failed execution with receipts, and returns. If
the stream writer supports an error-close operation, the fixture uses it so a
blocked SDK reader observes the cause; otherwise the returned mock result and
report carry the cause. No reader or writer goroutine may outlive `Wait` or
`Done`. No global working directory, process environment or signal handler may
be changed.

## agent-sdk-go stream transport

Both native SDKs gain a transport-only construction path. It runs the same
initialize, read, write, cancellation and close state machines as the
executable path, but starts no process. A private transaction-neutral transport
is implemented by the existing process path and the new stream path; protocol,
Session and observation state machines are not duplicated.

The existing `NewStdio` and `Open` paths retain their process and security
behavior; stream construction is an explicit alternative, not a fallback.
The public shape is:

```go
type StreamTransport struct {
    Reader io.ReadCloser // peer -> SDK
    Writer io.WriteCloser // SDK -> peer
    Stderr io.ReadCloser // optional; nil discards diagnostics
    // Wait reports the peer's final result after it has stopped writing and
    // closed its side. It is required for testify-backed peers; nil is only
    // for a peer whose reader EOF is itself authoritative.
    Wait func(context.Context) error
}
```

The SDK owns normal closure of the endpoints passed in `StreamTransport`: it
closes `Reader`, `Writer` and a non-nil `Stderr` on every failure and on
`Close`. Each endpoint's `Close` must unblock a concurrent read or write.

Each native client also exposes an idempotent half-close:

```go
func (c *Client) CloseInput() error       // codex
func (s *Session) CloseInput() error      // claude
```

`CloseInput` closes only the SDK outbound writer. It rejects later writes but
keeps the reader active for final server frames. It is a raw half-close:
idempotent, non-blocking, and it must not wait for `writeGate`, cleanup or the
peer. Closing the writer unblocks an in-flight write and makes later writes fail
immediately. Both native clients expose one dedicated input-closed error, such
as `ErrInputClosed`, for that state; they must not report it as an unexpected
transport failure. The first successful call returns nil; later calls return
the same `ErrInputClosed` result without closing a second time.

The client records `inputClosed` independently of its terminal cause. A queued
or in-flight write interrupted by `CloseInput` completes with `ErrInputClosed`
and marks its dispatch outcome consistently, but it does not install a
transport failure. A write error unrelated to the half-close still fails the
client. The `FullConsumption` event handler uses `CloseInput` to produce fixture
EOF. If the underlying writer close itself fails, the error is sticky and later
calls return that failure. Normal cleanup tolerates an already half-closed
writer and treats `ErrInputClosed` as expected.

`Wait` is bounded by the SDK's existing shutdown timeout and replaces
`exec.Cmd.Wait`. Testify-backed construction must always supply it, usually as
a join of `MockRun.Wait`. Raw reader EOF is not sufficient to infer success; the
transport adapter must distinguish these terminal states:

- **Normal completion:** input was half-closed, the reader reached EOF after
  the final frames, and `Wait` returns nil. The reader may reach EOF just before
  the `Wait` result is observed; the client retains the EOF fact and waits for
  `Wait` instead of failing on that transient ordering. The client/Session ends
  without `ErrUnexpectedEOF`, and `Close` returns cleanup success.
- **Caller abort:** `Close` begins before normal completion. The SDK closes both
  directions to unblock I/O, bounds any `Wait`, and preserves the intentional
  close state without treating a peer failure as clean completion. A mock
  failure caused by those closed streams is expected abort fallout and must not
  replace the intentional terminal cause.
- **Transport failure:** a read/write error occurs or `Wait` returns non-nil.
  The SDK preserves that cause through `Client.Err`/`Session.Err` instead of
  replacing it with a synthetic EOF error. `Wait` returning nil before the peer
  reader reaches EOF is not normal completion; the SDK waits at most the
  shutdown timeout and then records a failure.

`Wait` is the authoritative result of a testify-backed peer. A non-nil result
becomes the terminal error returned by `Client.Err`/`Session.Err` and, unless a
more specific cleanup error occurs, by `Close`. Close is idempotent and repeated
calls return the stored result. A nil result never turns a prior read, write or
cleanup failure into success.

The existing Codex read loop and Claude lifecycle must consume this explicit
completion state. Adding `CloseInput` alone is insufficient because their
current EOF paths classify every peer EOF as unexpected.

### Codex

```go
type StreamOptions struct {
    Args       []string
    Env        []string
    Dir        string
    Initialize types.InitializeParams
    Limits     Limits
}

func NewStreams(ctx context.Context, options StreamOptions, transport StreamTransport) (*Client, error)
```

`NewStreams` performs the same initialize/initialized exchange and returns only
after initialization. The existing `NewStdio` path remains unchanged. `Args`
must be the validated mapping of `ExecutionStart.NativeArgs` and must be
identical to the vector passed to `MockAgentOptions.NativeArgs`; unknown,
duplicate or unsupported native declarations fail before startup. `Dir` is set
to `ExecutionStart.ProtocolCwd`, which is the logical cwd of the native
app-server that produced the recording; `Env` is the effective
`ExecutionStart.Env` snapshot. Stream construction does not call `os.Chdir`,
mutate `os.Environ`, perform executable lookup or call `exec.Command`.
`ThreadStartParams.Cwd` remains part of the recorded business operation and is
not replaced by `Dir`.

### Claude

```go
func OpenStreams(ctx context.Context, options SessionOptions, transport StreamTransport) (*Session, error)

func NativeArgs(options SessionOptions) ([]string, error)
```

`OpenStreams` constructs the same native argument vector as `Open` but uses the
provided streams instead of `CLIPath`. `NativeArgs` is the single source of
truth used both by `Open`/`OpenStreams` and by tests that must pass the exact
arguments to `MockAgentOptions.NativeArgs`. The helper must not duplicate a
second argv implementation. `OpenStreams` ignores `CLIPath` and performs no
executable lookup or `exec.Command` call.
It validates that the supplied native declaration is exactly the vector
returned by `NativeArgs(options)`.

`WorkDir` is set from `ExecutionStart.Workspace`, because the Claude process
path uses one `WorkDir` for both native cwd and host-side history/discovery
scope. `ExecutionStart.ProtocolCwd` is not substituted for it and the
pre-launch route template is never used. `Env` is set from
`ExecutionStart.Env`; stream construction does not mutate the host working
directory or process environment. Options that participate in the native argv
must be identical between the `NativeArgs` call supplied to the mock and the
`OpenStreams` call.

Claude config and history resolution use `Env`, `Config` and `Workspace` as
appropriate; `CLAUDE_CONFIG_DIR` must come from the effective environment.
Codex `CODEX_HOME` must likewise come from `Env` or `Config`, never from the
host process. A provider's `ProtocolCwd` and `Workspace` remain distinct even
when their current values happen to be equal.

## Connection flow

The test owns two pipe pairs and connects them in opposite directions:

```text
SDK Writer  ->  fixture Reader
SDK Reader  <-  fixture Writer
```

One connection proceeds as follows:

1. Prepare or open a fixture in the test process.
2. Call `Fixture.StartMockAgent` and retain the returned `*MockRun` and
   `run.Start`; the replay goroutine may start as soon as the handle exists.
3. Use `run.Start`'s actual workspace, protocol cwd, home, config, tmp and
   environment to construct `codex.NewStreams` or `claude.OpenStreams`.
4. Start a watcher that selects `run.FullConsumption()` and `run.Done()`. On
   full consumption, call the SDK's `CloseInput` without closing the SDK reader
   or any fixture endpoint.
5. The fixture observes client EOF, completes replay, and closes its own
   fixture-to-SDK writer. The SDK observes final frame EOF and its `Wait`
   closure observes `run.Wait`.
6. Close the SDK Session/Client, wait for every `MockRun`, and validate the
   typed execution report.
7. For simulation, export the material before removing the caller-owned
   fixture; for ordinary tests, remove it after evidence is read.

`StartMockAgent` must return before replay performs protocol I/O. The SDK is
created by the test goroutine after that return, never by a callback on the
replay goroutine. The full-consumption watcher may run concurrently, but it may
only call the already-created, idempotent SDK `CloseInput`; it must not wait for
`MockRun.Wait` or the fixture to return.

## Failure, cancellation and cleanup

The test must register cleanup immediately after a successful prepare. Cleanup
uses a separate bounded context and runs even when business assertions fail or
the test context is canceled.

The shutdown order is fixed:

1. Close the SDK Session/Client and wait for its owned goroutines. If protocol
   completion has not already half-closed input, close it first.
2. Cancel or close every fixture-side stream and wait for every `MockRun`.
3. Read the execution report for the exact execution ID.
4. Export when the case requires persistent material.
5. Remove the caller-owned fixture directory.

An intentional scripted disconnect is still a failed replay, not a successful
execution. Expected failures assert the report's failure and cleanup facts; they
must not downgrade strict matching, EOF or receipt checks.

Both directions may be closed by their producers during normal protocol
completion. Tests should use `io.Pipe` (or an equivalent with idempotent
producer close) and must not close an endpoint owned by the other participant.
SDK cleanup treats an already closed outbound writer as expected and continues
to close the reader and wait for peer completion.

## Test and simulation migration

`internal/mocktest` becomes a library-backed bridge. It may import the testify
module and the standard library, but it must not import either native SDK or
contain protocol matching. It should expose fixture preparation, execution
startup, the SDK-side stream pair, `ExecutionStart`, typed reports and cleanup.
`Command`, `Launcher`, result descriptors, progress descriptors, schema-3
`session.json` parsing and `--session-dir`/`--result-fd`/`--progress-fd`
construction are removed from its public surface. The bridge may start the
library mock goroutine, but it must not re-exec the test binary as a mock or
forward protocol bytes between two child processes.

Native package tests and agent provider tests use `NewStreams`/`OpenStreams`
directly. Their synthetic inputs still come from the SDK-owned capture builder
and are passed to `agenttestify.Prepare` as a recording source. Existing
SDK-owned fake native peers used for non-testify contract tests remain valid.

Simulation keeps the SDK driver process as the public SDK host. The driver
imports `internal/mocktest` and runs stdio mocks in process; it must not exec a
testify command or generate a peer launcher. The simulate process embeds the
same library for prepare/export, and for remote-close it owns the in-process
mock behind the authenticated WebSocket bridge. That bridge adapts WebSocket
frames to the same directional streams; it forwards bytes, not testify argv or
process output, and the mock handle events execute in the simulate process.

The simulate request carries a fixture directory and connection selection, not
a peer executable. The test driver receives the exported assertion material and
the fixture path; it uses `ExecutionStart` for all native runtime paths. After
the driver exits, the simulate process reads the typed `ExecutionReport` and
records the same execution, material and connection facts as before.

Default offline tests no longer read `AGENT_SDK_TESTIFY_BIN`, build or install
the CLI, search `PATH`, allocate inherited result/progress descriptors, or
create launcher scripts. CLI conformance remains an upstream consumer concern
and can stay an explicitly optional process test; it is not a prerequisite for
the SDK library gate.

The module graph contains testify because `go test` compiles the test-only
bridge. Boundary checks must verify that no production package imports it.
`go.mod` and `go.sum` contain the same exact revision used by the local and CI
gates; the old external-build workflow and its environment variable are
removed from the required path.

## Acceptance

The proposal is complete only when all of the following are demonstrated:

- A test process with no testify executable and no `AGENT_SDK_TESTIFY_BIN`
  completes a Codex and a Claude mock through the public SDK stream APIs.
- Both stream clients are given a bounded `Wait` that joins `MockRun.Wait` and
  propagates its failure.
- `MockRun.Start` exposes the actual execution workspace/config/env before
  protocol I/O, and the SDK uses those values.
- `MockRun.FullConsumption` occurs after replay and pending-request checks but before
  the extra-byte/EOF check; `CloseInput` closes only the SDK writer and lets
  the fixture finish, while final server frames still reach the SDK.
- `CloseInput` is non-blocking and idempotent, including while a write is
  blocked. Normal completion, caller abort and transport failure take the
  distinct paths above; a half-close-induced write failure does not become a
  transport failure, clean EOF is not an unexpected EOF, and a non-nil `Wait`
  error remains terminal evidence.
- The typed report records all declared connections, strict consumption,
  client EOF, `nativeLaunched == false`, cleanup, material hashes and a nil
  exit status for embedded execution, for both success and expected-disconnect
  cases. An executable connection still records its real exit status.
- Cancellation closes both directions within the configured timeout and leaves
  no blocked reader/writer goroutine.
- Embedded fixtures expose no executable command, launcher or fake tool hash.
- Fixture and execution descriptors use schema 5 and encode embedded module
  identity with a discriminated union; they cannot be confused with the
  rejected schema-4 command field. Executable fixtures retain their existing
  command/hash evidence.
- A module/module graph check proves testify remains test-only and the exact
  revision and checksum are pinned by `go.mod`/`go.sum`, with no `Replace`,
  `go.work` or production-package import. A fixture opened by a binary linked
  to a different module identity fails closed.
- Simulation completes without launching an `agent-sdk-testify` binary or
  re-executing a fake protocol peer, while preserving public SDK business
  assertions, export validation and cleanup evidence.

## Adoption order

1. Land the upstream stream, start-handle, typed-report and embedded-identity
   contract; add upstream tests for event ordering, cancellation and
   multi-connection reports.
2. Add the Codex and Claude stream constructors and their native contract tests.
3. Replace `internal/mocktest` process calls and migrate native and provider
   tests.
4. Move simulation prepare/export and stdio/remote mock ownership to the
   library-backed bridge.
5. Remove the required external binary build, environment override and CLI
   adapters from the default SDK gates; retain optional CLI conformance outside
   the library gate.
