# Offline validation

The [testing standards](../harness/testing.md) define test levels and required
coverage.
This guide describes current commands and configured gates. Simulation catalog
availability and synthetic checks do not prove real recordings; see
[data provenance](../tools/official-sdk-drivers/README.md#fixtures-仿真录制与测试输入).

Run from the repository root with the pinned compiler:

```sh
mise install
mise exec -- go mod download
mise exec -- bash scripts/check-offline.sh
GOMAXPROCS=2 GORACE=atexit_sleep_ms=0 bash scripts/with-agent-sdk-testify.sh mise exec -- go test -race -p=1 -count=1 -timeout 300s ./...
mise exec -- go vet ./...
```

The offline script builds `tools/agent-sdk-testify` from this checkout unless an absolute
`AGENT_SDK_TESTIFY_BIN` is supplied. Direct SDK tests need the same binary; use
`scripts/with-agent-sdk-testify.sh` to build it temporarily. Select Python 3.11 or newer
as `python3` on PATH before running the script.

The offline script validates the current simulation catalog, case ownership,
provider availability and local documentation links with `scripts/check-story-evidence.py`.
It runs recording-tool tests and code integration tests independently. Synthetic
protocol faults, resource boundaries, cancellation and races have no story registry.

Go tool implementation and its own tests live in `tools/agent-sdk-testify/`.
Go SDK targeted integration tests remain in `tests/integration/`; the separate
simulation driver and shared case materials live in `tests/simulation/sdk/` and
`tests/simulation/scenarios/`. Official SDK drivers, shared Python helpers and
their offline tests live independently in `tools/official-sdk-drivers/`.
The offline script discovers both its `tests/` and `tests/scenarios/` suites.

The remaining gates check module and native dependency boundaries, standard-library-only
types packages, native inventory, reproducible Codex generation, unit/subprocess tests
and offline examples. `GOMAXPROCS=2`, package scheduling `-p=1` and
`GORACE=atexit_sleep_ms=0` bound test load. CI runs offline, race and vet checks on
`ubuntu-24.04`, `macos-15` and `macos-26`. Every example compiles with `go test ./...`.
The installed-wheel smoke check runs outside the checkout and verifies driver packaging
and the request/result contract without installing an official SDK or launching an Agent.

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
mise exec -- bash scripts/check-consumers.sh "$(git rev-parse HEAD)"
mise exec -- bash claude/scripts/check-managed-consumer.sh "$(git rev-parse HEAD)"
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

The [workflow](../.github/workflows/ci.yml) runs offline, race, vet and installed-wheel checks.
External consumer CI uses the same `ubuntu-24.04`, `macos-15` and `macos-26`
matrix for pushed `main` commits, manual workflow runs and same-repository PRs.
Push/manual consumers target `github.sha`; PR consumers check out and fetch the
published `github.event.pull_request.head.sha`, never the synthetic merge SHA.
These events configure six jobs (three Offline and three External consumer).
Fork PRs retain three Offline jobs and skip consumers because fork-only SHAs are
not assumed retrievable from this repository. Token permissions remain contents:
read; no pull_request_target execution is added. Configured coverage does not
prove that any runner executed. See
[release readiness](#release-preparation) for the required exact-commit CI evidence.

## Code integration tests

[Codex public API tests](../codex/public_api_test.go) check API signatures, import
identity and configuration during ordinary package tests. Go mock CLI integration
lives in [Codex](../codex/mock_cli_test.go) and [Claude](../claude/mock_cli_test.go)
package tests: each SDK starts its Go mock executable as the CLI process and checks
question/permission answers, interrupts, cancellation, disconnects and mismatches.
Both tests also pause consumption during 2,048-event bursts, complete control and
answer operations, then verify ordered drain after disconnection. The Go tool and Python-to-Go integration tests check
mock command exit status and handshake completion. Shared FIFO tests cover
cancellation, atomic batches, concurrent producers, close/abort races and reference
release. See the [event delivery contract](../harness/modules/native-session/design.md#delivery-event-delivery).

`TestNativeIntegration` in `tests/integration` exercises both native SDK public
interfaces through synthetic protocol peers. It retains original-handle authority,
unknown fields, cancellation, ordering, drain, backpressure and error classification.
Testify suites run sequentially; worker outcomes return to the owning test goroutine.

`NativeIntegrationSuite.TestIndependentHistoryBrowser` uses a strict synthetic Codex
app-server trace and Claude temporary saved storage. Native focused tests retain
transport, live coexistence, cancellation, unknown, resource and integrity checks.
The offline examples show both native history workflows. None of these checks
establishes real recording or model execution.

## Simulation coverage

The catalog defines eight stories and 36 flows. No recorded simulation dataset
is bundled. Required Go SDK simulation still needs reviewed real recordings,
public-interface assertions, full consumption and cleanup checks; tooling or
synthetic checks cannot fill that gap. Missing required coverage must be reported.
Recording and optional official-SDK replay use the [agent-sdk-testify guide](../tools/agent-sdk-testify/README.md).

## Focused checks

Run these from the repository root; default live gates remain disabled:

```sh
python3 scripts/check-story-evidence.py
python3 codex/docs/protocol/verify_inventory.py
mise exec -- bash -c 'cd codex && bash scripts/check_generated.sh'
bash scripts/with-agent-sdk-testify.sh mise exec -- go test -count=1 ./tests/integration ./codex ./claude
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
2. Require the exact SHA's three Offline and three External consumer CI jobs;
   rerun on the resulting main commit after merge. Fork PR skips do not satisfy this gate.
3. Review CHANGELOG, migration, licenses, native attribution and actual simulation
   coverage. Resolve the missing coverage described under [Simulation coverage](#simulation-coverage).
4. Record the exact commit, CI URLs and remaining limits in the release review.
   Resolve failed/skipped required gates before creating a tag/release.

Real agent checks remain opt-in. These commands do not publish anything.
