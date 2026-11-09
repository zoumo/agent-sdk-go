# Offline validation

The [testing standards](../harness/testing.md) define test levels and required
coverage.
This guide describes current commands and configured gates. Simulation catalog
availability and synthetic checks do not prove real recordings; see
[data provenance](../tests/simulation/drivers/README.md#fixtures-仿真录制与测试输入).

Run from the repository root with Go 1.26 or newer:

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
go mod download
make fmt-check
make lint
bash scripts/check-offline.sh all
```

The offline entry also accepts `parallel`, `quality`, `test`, `race`, `e2e` or
`build`. `all` runs every task in order; `parallel` runs `quality` first, then
runs `test` and `build` concurrently, then runs `race` and `e2e` one at a time
in the same checkout.

Test and simulation hosts link the exact test-only in-process testify module; no
separate helper process is required. The dependency boundary and pin are fixed
by [Go library mode](../harness/modules/agent-sdk-testify/library-mode.md).
`scripts/simulation.sh build` builds only the SDK runner and driver. The library
owns official driver/package and material checks. Scenario, recording,
official-driver and Python snapshots do not remain in this repository.
Simulation obtains assertion inputs from the typed execution report and prepared
material. Callers read the
execution report, then remove the caller-owned fixture after every SDK client
and mock exits. The migrated synthetic, single-case and 72-slot gate checks
remain in `tests/simulation/simulate`.

The `quality` task checks module and native dependency boundaries, the
standard-library-only native types and `agent` core, provider-specific adapter
imports, the absence of reverse native/adapter/MASS imports, native inventory,
reproducible Codex generation and committed whitespace, then runs `go vet`.
`test` runs unit/subprocess tests and offline examples. `race` repeats the
package suite under the race detector, `e2e` runs the fixed real-recording
simulation gate, and `build` compiles every package and the SDK simulation
runner/driver. `GOMAXPROCS=2`, package scheduling `-p=1` and
`GORACE=atexit_sleep_ms=0` bound test load. CI runs all five tasks on
`ubuntu-24.04` and `macos-26`. Every example compiles with `go test ./...`.
The pinned testify module owns official driver/package checks. Hosted CI builds
only the SDK-owned simulation runner and driver and links that module; it does
not build or download a separate testify artifact.

Real CLI/agent checks live in [`codex/tests/e2e`](../codex/tests/e2e) and
[`claude/tests/e2e`](../claude/tests/e2e). Codex covers stdio lifecycle/history,
interaction/interrupt scenarios and WebSocket authentication/lifecycle. Its
WebSocket harness requires Unix process groups for bounded server cleanup.
It checks authentication, reconnection and client/server lifecycle through the
SDK, Go networking and the server process handle. It does not inspect process-tree
listener ownership or assert the absence of additional listeners. Claude currently has
only an initialization/shutdown transport integration probe: it sends no model
turn and does not establish complete agent E2E coverage.

Each SDK has independent, opt-in E2E switches.
Real CLI/agent tests are disabled through `CODEX_E2E_STDIO=0`,
`CODEX_E2E_INTERACTIONS=0`, `CODEX_E2E_WEBSOCKET=0`, `CLAUDE_E2E=0`,
and `SDK_REAL_AGENT_E2E=0`. Codex requires `CODEX_E2E_CLI` and Claude requires
`CLAUDE_E2E_CLI` to select the executable. Each selected test requires its switch
to equal `1` and refuses a nonempty `CI`. Offline validation and recorded replay invoke no real agent/model. Native successful subagent interoperability remains unverified;
see each package's compatibility and recording documentation for precise evidence.

Small fixture embedding packages under each native `tests/testdata`
contain code-owned synthetic inputs from `integration/`. Replay adapters share those bytes without
depending on the caller's working directory. External consumers verify their packaging.

## External consumers

After pushing a commit, validate the new module from independent temporary
consumer modules using its full SHA:

```sh
bash scripts/check-consumers.sh "$(git rev-parse HEAD)"
bash claude/scripts/check-managed-consumer.sh "$(git rev-parse HEAD)"
```

Both scripts create their modules and module caches in private temporary directories, disable
`go.work` and inherited Go overlays/modfiles, and fetch from the new repository
without `replace`. The root script resolves the exact SHA directly from GitHub,
checks its full VCS origin hash, compiles all four public imports, copied public
examples and documented configuration/approval snippets, and runs synthetic
value/configuration and embedded fixture packaging smoke checks. It does not copy
or rerun the SDK interaction suites. The retained Claude script separately compiles
its native consumer and configuration snippet. Dependency download requires network
access; compilation/runtime checks then use `GOPROXY=off` and `GOSUMDB=off`.
Neither script invokes a real agent CLI or model. These checks validate a published
revision, not uncommitted changes in the working checkout.

## Hosted gates

The [workflow](../.github/workflows/ci.yml) defines one offline job for each
supported runner: `ubuntu-24.04 / required` and `macos-26 / required`. Each job
installs Go, `ripgrep` and the pinned formatter/linter once, runs `quality`, then
runs `test` and `build` concurrently, then runs `race` and `e2e` separately.
Race and the required recording gate both exercise timing-sensitive subprocess
protocols, so they do not overlap each other or package tests. The first task
mutates generated files for its drift check, so it precedes the concurrent
phase. The job status is the required aggregate for that platform; task output
remains grouped in its logs. The offline tasks build only the SDK-owned
simulation runner and driver; the in-process testify module is fetched by the
normal Go module graph.
External consumer CI uses the same `ubuntu-24.04` and `macos-26` matrix for
pushed `main` commits, manual workflow runs and same-repository PRs.
Push/manual consumers target `github.sha`; PR consumers check out and fetch the
published `github.event.pull_request.head.sha`, never the synthetic merge SHA.
These events configure two offline platform jobs and two External consumer jobs.
Fork PRs retain the offline jobs and skip
consumers because fork-only SHAs are not assumed retrievable from this
repository. Token permissions remain contents: read; no pull_request_target
execution is added. Configured coverage does not prove that any runner executed. See
[release readiness](#release-preparation) for the required exact-commit CI evidence.

## Code integration tests

[Codex public API tests](../codex/public_api_test.go) check API signatures, import
identity and configuration during ordinary package tests. Mock integration
lives in [Codex](../codex/mock_cli_test.go) and [Claude](../claude/mock_cli_test.go)
package tests: each SDK opens the in-process mock through closeable SDK streams and
checks question/permission answers, interrupts, cancellation, disconnects and
mismatches. Both tests also pause consumption during 2,048-event bursts, complete
control and answer operations, then verify ordered drain after disconnection.
Typed execution reports check strict replay, full consumption, client EOF and the
absence of a native launch. Shared FIFO tests cover cancellation, atomic batches,
concurrent producers, close/abort races and reference release. See the
[event delivery contract](../harness/modules/native-session/design.md#delivery-event-delivery).

`TestNativeIntegration` in `tests/integration` exercises both native SDK public
interfaces through synthetic protocol peers. It retains original-handle authority,
unknown fields, cancellation, ordering, drain, backpressure and error classification.
Testify suites run sequentially; worker outcomes return to the owning test goroutine.

The optional unified layer has separate public-contract, lifecycle, event, interaction
and dependency tests under `agent/`, `agent/codex/` and `agent/claude/`. Adapter
integration tests launch only synthetic protocol peers. `ExampleProvider` runs the
provider-neutral open/resume/start-or-steer/stream/answer/close flow twice without
a CLI:

```sh
go test -run '^ExampleProvider$' ./agent
```

`ExampleMCPServer` validates the final external MCP list for both unified
transports without starting a CLI or connecting to a server:

```sh
go test -run '^ExampleMCPServer$' ./agent
go test -run '^TestProviderMapsFinalMCPServersToOpenAndResumeThreadConfig$' ./agent/codex
go test -run '^TestProviderOpenPassesFinalMCPListToClaudeOptions$' ./agent/claude
```

The adapter checks use synthetic app-server and in-process native peers and assert
only the generated native configuration. They do not establish real MCP
connectivity, authentication, tool discovery, or execution.

`NativeIntegrationSuite.TestIndependentHistoryBrowser` uses a strict synthetic Codex
app-server trace and Claude temporary saved storage. Native focused tests retain
transport, live coexistence, cancellation, unknown, resource and integrity checks.
The offline examples show both native history workflows. None of these checks
establishes real recording or model execution.

## Simulation coverage

The catalog defines eight stories and 36 flows. Available material comes from the pinned library's builtin catalog. Required Go SDK simulation still needs reviewed real recordings,
public-interface assertions, full consumption and cleanup checks; tooling or
synthetic checks cannot fill that gap. Missing required coverage must be reported.
Go SDK simulation flow implementations and their regression tests live in
`tests/simulation/drivers/codex/go` and `tests/simulation/drivers/claude/go`; the drivers root
owns the driver entry and delivery checks. Native `tests/replay` packages retain
SDK integration tests and synthetic fixture adapters over in-process streams. Pure
protocol matching and official verification belong to the
[pinned testify library](https://github.com/zoumo/agent-sdk-testify).
See the [SDK adapter guide](../tests/simulation/drivers/README.md) for the
library-backed fixture boundary.

## Focused checks

Run these from the repository root; default live gates remain disabled:

```sh
python3 codex/docs/protocol/verify_inventory.py
(cd codex && bash scripts/check_generated.sh)
go test -count=1 ./tests/integration ./codex ./claude
```

Codex generation uses the checked-in schema without downloading upstream sources;
consistency does not prove live compatibility. CI whitespace checks compare Git
objects: push before→HEAD, PR merge-base→head, manual first-parent→HEAD, with an
empty-tree baseline for an initial/root commit. Missing commits fail closed.
[check_whitespace.py](../codex/scripts/check_whitespace.py) uses
`GITHUB_EVENT_NAME` and `GITHUB_EVENT_PATH`; negative fixtures verify actual failures.
CI uses pinned action commits, read-only permissions and no persisted credentials
or service secrets. Concurrency cancellation stays within workflow/event/ref lanes.
No Windows runtime support is claimed.

## Release preparation

One version covers all four native imports. Before an authorized publication:

1. Confirm the intended main SHA and both imported source histories.
2. Require the exact SHA's two platform required checks and two External
   consumer CI jobs, and inspect all five task logs on each platform; rerun on the
   resulting main commit after merge. Fork PR consumer skips do not satisfy this gate.
3. Review CHANGELOG, migration, licenses, native attribution and actual simulation
   coverage. Resolve the missing coverage described under [Simulation coverage](#simulation-coverage).
4. Record the exact commit, CI URLs and remaining limits in the release review.
   Resolve failed/skipped required gates before creating a tag/release.

Real agent checks remain opt-in. These commands do not publish anything.
