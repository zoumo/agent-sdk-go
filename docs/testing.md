---
type: Guide
title: Offline validation
description: "Explains how to run offline checks, fixed-recording simulation, and opt-in live CLI checks, and how to read the results."
tags: [testing, offline, simulation, release]
sources:
  - id: test-standards
    resource: "testing-standards.md"
    title: "SDK testing standards"
---

# Offline validation

Run commands in this guide from the repository root with Go 1.26 or newer.
Choose the check that matches the evidence you need. The repository defines
required coverage by test level in the [testing standards](testing-standards.md).

| If you need to check | Run | Read the result |
|---|---|---|
| The shared session API or an MCP declaration | [Run an offline example](#code-integration-tests) | Go checks the examples' expected output. |
| The native public interfaces against synthetic peers | `go test -count=1 ./tests/integration ./codex ./claude` | Go reports test results by package. [Integration tests](#code-integration-tests) |
| All required cases against fixed native recordings | [Run the simulation gate](#run-the-fixed-recording-gate) | Read `coverage.json` and the per-run results in the reported output directory. |
| The repository's offline gates | [Run the offline checks](#run-the-offline-checks) | The command exits successfully when every selected check passes. |
| A real installed CLI or model | [Select an opt-in real CLI check](#opt-in-real-cli-checks) | Read the result for that specific CLI, version, and scenario. |

## Run the offline checks

Prepare the linter and module cache before running the offline gate.

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
go mod download
```

Run the format, lint, and offline checks.

```sh
make fmt-check
make lint
bash scripts/check-offline.sh all
```

The script takes one optional task argument.

| Task | Checks |
|---|---|
| `all` | Run `quality`, `test`, `race`, and `build` in order. |
| `parallel` | Run `quality`, then `test` and `build` concurrently, then `race` alone. |
| `checks` | Run `quality`, `test`, and `build` for the CI checks job. |
| `quality` | Check module and import boundaries, Codex protocol inventory and generation, and `go vet`. |
| `test` | Run the package suites, including the recording gate, and execute offline examples. |
| `race` | Run the package suites, including the recording gate, under the race detector. |
| `e2e` | Run only the offline recording gate with verbose output. |
| `build` | Compile every package, including the SDK simulation driver. |

## Run the fixed recording gate

```sh
bash scripts/check-offline.sh e2e
```

The test runs the gate in process. `TestMain` builds the SDK driver, which runs
in a separate process with its own environment and workspace. Both link the
Replay package. The test reads the typed execution report before
removing its fixture. [Testify library contract](modules/agent-sdk-testify/library-mode.md)

Inspect `coverage.json` for each provider and case's requirement, material,
source verification, and SDK result. Per-run `summary.json` records business,
protocol, evidence, and cleanup outcomes. Use the
[simulation guide](../tests/simulation/drivers/README.md) to select a single case
or an external recording.

The test runs in a short private directory under `/tmp` and resolves its
physical path before starting the simulation. This keeps the execution workspace
within the SDK's 200-byte Claude project-key limit even when the checkout,
`TMPDIR`, or archive path is deep.

The test archives the output after success, failure, or handled INT/TERM
cancellation. Set `-simulation.output` to a new absolute archive directory
whose parent exists. The default archive is under
`.artifacts/simulation/gates/gate.*`, which CI collects. The test logs the
archive location and removes the temporary run after it copies the evidence.
Recorded paths retain the original execution identity. If archiving fails,
the test returns an error and reports the retained temporary output location.

## Opt-in real CLI checks

Real CLI/agent checks live in [`codex/tests/e2e`](../codex/tests/e2e) and
[`claude/tests/e2e`](../claude/tests/e2e). Codex covers stdio lifecycle/history,
interaction/interrupt scenarios and WebSocket authentication/lifecycle. Its
WebSocket harness requires Unix process groups for bounded server cleanup.
It checks authentication, reconnection and client/server lifecycle through the
SDK, Go networking and the server process handle. It does not inspect process-tree
listener ownership or assert the absence of additional listeners. Claude includes
an initialization/shutdown probe and opt-in session-identity checks. The latter
check unified claim-based Open before input, one model input, native running/idle
and no active work after idle, plus a native first turn
whose init, assistant and success result match the UUID passed in --session-id.
An opt-in 2.1.293 relationship check starts one local task and requires forwarded
child identity, confirmed membership, a main-session parent and successful completion.
They do not establish complete agent E2E coverage.
Offline version tests exercise the public native Open path and assert that old,
prerelease, ambiguous and oversized version output does not launch the Session
process. Resume also checks query execution failures before launch.

Each SDK has independent, opt-in E2E switches.
Real CLI/agent tests are disabled through `CODEX_E2E_STDIO=0`,
`CODEX_E2E_INTERACTIONS=0`, `CODEX_E2E_WEBSOCKET=0`, `CLAUDE_E2E=0`,
and `SDK_REAL_AGENT_E2E=0`. Codex requires `CODEX_E2E_CLI` and Claude requires
`CLAUDE_E2E_CLI` to select the executable. Each selected test requires its switch
to equal `1` and refuses a nonempty `CI`. Offline validation and recorded replay invoke no real agent/model.
See each package's compatibility and recording documentation for precise evidence.

Run the assigned-session model probes only with explicit opt-in:

```sh
SDK_REAL_AGENT_E2E=1 CLAUDE_E2E=1 \
CLAUDE_E2E_CLI=/absolute/path/to/claude \
CLAUDE_E2E_MODEL=your-model \
go test ./claude/tests/e2e -run 'TestUnifiedOpenBeforeInput|TestNativeAssignedSessionIdentityAcrossFirstTurn|TestNativeSubagentRelationship293' -v -count=1
```

These probes use temporary HOME/config directories. The relationship check
enables the native Task/Agent tool; the identity-only probes use simple mode. They copy
only the Anthropic endpoint and authentication variables from the caller's
environment or `~/.claude/settings.json`, and never print their values.

`TestUnifiedResumeIdentityWithoutInput` is a separate opt-in lifecycle check for
an explicitly supplied, offline, caller-owned saved Session. It calls only public
Core Resume/Close/Stop and scoped metadata queries; it sends no user input and
makes no model call. It reads no credential files. Supply an isolated CLI entrypoint
that retains legitimate CLI authentication and disables personal hooks/tools:

```sh
SDK_REAL_AGENT_E2E=1 CLAUDE_E2E=1 \
CLAUDE_RESUME_E2E_CLI=/absolute/path/to/isolated-claude \
CLAUDE_RESUME_E2E_CONFIG_DIR=/absolute/native/config \
CLAUDE_RESUME_E2E_CWD=/absolute/owned/project \
CLAUDE_RESUME_E2E_SESSION_ID=your-owned-session-uuid \
go test ./claude/tests/e2e -run '^TestUnifiedResumeIdentityWithoutInput$' -v -count=1
```

The check verifies the same native ID and transcript file identity before and
after promptless resume. It does not establish a resumed business workflow or
MCP connectivity; those require separate acceptance evidence.

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

The [workflow](../.github/workflows/ci.yml) defines three independent
validation jobs on `ubuntu-24.04`. `checks` installs Go and downloads the pinned
formatter/linter's official precompiled archive, verifying its SHA-256 before
extraction. Source scans use the Python standard library. The job checks source
ancestry, module cleanliness and whitespace, then runs `quality`, `test` and
`build`. The dedicated `race` job runs the timing-sensitive race suite on a
separate runner, without installing the linter or running generation checks.
Both jobs include the strict fixed-recording gate through `go test ./...`;
there is no separate simulation-gate job. Failures and logs remain isolated while
the jobs run concurrently.
The offline tasks build the tests and SDK-owned driver; the
in-process Replay library is built from the SDK checkout.
External consumer CI runs for pushed `main` commits, manual workflow runs and
same-repository PRs.
Push/manual consumers target `github.sha`; PR consumers check out and fetch the
published `github.event.pull_request.head.sha`, never the synthetic merge SHA.
These events configure two validation jobs and one External consumer job.
Fork PRs retain all validation jobs and skip
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
[event delivery contract](modules/native-session/design.md#delivery-event-delivery).

`TestNativeIntegration` in `tests/integration` exercises both native SDK public
interfaces through synthetic protocol peers. It retains original-handle authority,
unknown fields, cancellation, ordering, drain, backpressure and error classification.
Testify suites run sequentially; worker outcomes return to the owning test goroutine.

The optional unified layer has separate public-contract, Core lease, event, interaction
and dependency tests under `agent/`, `agent/internal/corelease/`,
`agent/internal/eventstream/`, `agent/codex/` and `agent/claude/`. Boundary gates
check direct imports and exact production dependency allowlists for corelease
and eventstream. Adapter
integration tests launch only synthetic protocol peers. `ExampleCore` runs the
adapter-neutral open/resume/start-or-steer/stream/answer/close flow twice without
a CLI:

```sh
go test -run '^ExampleCore$' ./agent
```

`ExampleOpenOptions_mcpServers` validates the final external MCP list for both unified
transports without starting a CLI or connecting to a server:

```sh
go test -run '^ExampleOpenOptions_mcpServers$' ./agent
go test -run '^TestProviderMapsFinalMCPServersToOpenAndResumeThreadConfig$' ./agent/codex
go test -run '^TestProviderOpenPassesFinalMCPListToClaudeOptions$' ./agent/claude
```

The adapter checks use synthetic app-server and in-process native peers and assert
only the generated native configuration. They do not establish real MCP
connectivity, authentication, tool discovery, or execution.

The additional-system-prompt contract uses the same offline synthetic boundary:

```sh
go test -run 'AdditionalSystemPrompt' ./agent ./agent/codex ./agent/claude
go test ./agent ./agent/codex ./agent/claude ./claude/...
```

These checks cover empty omission, exact Codex request and Claude argv mapping,
literal Unicode and line endings, Resume replacement of the newly supplied value,
and invalid UTF-8/NUL rejection before native side effects. They launch no real
Agent or model.

Context injection has dedicated offline protocol checks:

```sh
go test ./agent ./agent/codex ./agent/claude ./codex ./codex/types -run Inject -count=1
```

They cover exact context-only envelopes, idle/active input routing, unchanged
interrupt targets, version/native rejection, local admission, cancellation,
close and unknown Codex outcomes. These synthetic checks do not establish
real-provider consumption. Official recordings and their strict replay remain
owned by Replay; a new recording must be adopted and exercised
through the public Go SDK before it constitutes SDK simulation evidence.

`NativeIntegrationSuite.TestIndependentHistoryBrowser` uses a strict synthetic Codex
app-server trace and Claude temporary saved storage. Native focused tests retain
transport, live coexistence, cancellation, unknown, resource and integrity checks.
The offline examples show both native history workflows. None of these checks
establishes real recording or model execution.

## Simulation coverage

The [SDK acceptance matrix](../tests/simulation/simulate/coverage.go) defines
which provider and case combinations are required. Replay supplies
the fixed recording corpus. The gate reports missing required material as a
failure. For business evidence and pass criteria, see the
[SDK acceptance guide](sdk/validation-cases.md). Check the matrix independently
of any recording with the existing policy test.

```sh
go test -count=1 ./tests/simulation/simulate -run '^TestAcceptanceMatrixMatchesSharedPolicy$'
```

SDK simulation evidence combines reviewed native recordings, public SDK
assertions, strict replay, full consumption, and cleanup. Official verification
belongs to the [Replay library](../replay/README.md).
Go simulation flows live under `tests/simulation/drivers/`; synthetic native
replay fixtures support the [code integration tests](#code-integration-tests).

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
2. Require the exact SHA's `checks` and `race` CI jobs and the External
   consumer job, and inspect their logs; rerun on the resulting main commit after
   merge. Fork PR consumer skips do not satisfy this gate.
3. Review CHANGELOG, migration, licenses, native attribution and actual simulation
   coverage. Resolve the missing coverage described under [Simulation coverage](#simulation-coverage).
4. Record the exact commit, CI URLs and remaining limits in the release review.
   Resolve failed/skipped required gates before creating a tag/release.

GitHub requires `Release / verified main SHA` when you create a `v*` tag. The
check records success on the exact commit only when all three jobs succeed on
a push to `main`. Open the check's details to see the run that verified the
commit.

Real agent checks remain opt-in. These commands do not publish anything.
