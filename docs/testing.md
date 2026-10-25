# Offline validation

The [testing standards](../harness/testing.md) define test organization and safety;
this guide contains the commands and validation scope. Directory responsibilities
are defined in the [layout standards](../harness/code-layout.md).

See the [recorded test data index](recordings.md) for direct links to native wire
files, manifests, expected observations and ID maps. Real Codex tool/question
captures and the Claude authentication-failure capture remain distinct from
synthetic fixtures; real subagent recordings are still missing.

Run from the repository root with the pinned compiler:

```sh
mise install
mise exec -- go mod download
mise exec -- bash scripts/check-offline.sh
GOMAXPROCS=2 GORACE=atexit_sleep_ms=0 mise exec -- go test -race -p=1 -count=1 -timeout 300s ./...
mise exec -- go vet ./...
```

The offline script first validates the current 19/14 Story inventory, nine native
supplement IDs, local evidence links and existing public suite entrypoint inventory
with `scripts/check-story-evidence.py` plus negative regression cases. This static
check does not replace execution of `TestCommonStories`.

The offline script checks single-module and native dependency boundaries, both
standard-library-only types packages, native protocol inventory, reproducible
Codex generation, sanitization canaries and recordings, unit/subprocess tests, and
all runnable offline examples. Validation uses `GOMAXPROCS=2`, package scheduling
`-p=1`, and `GORACE=atexit_sleep_ms=0` to bound concurrent test load and avoid
per-subprocess race-detector exit sleeps. CI runs the same checks, race and vet on
`ubuntu-24.04`, `macos-15` and `macos-26`. macOS 15 remains an explicit target;
macOS 26 is additive. Every example compiles with `go test ./...`.

Real CLI/agent checks live in [`codex/tests/e2e`](../codex/tests/e2e) and
[`claude/tests/e2e`](../claude/tests/e2e). Codex covers stdio lifecycle/history,
interaction/interrupt scenarios and WebSocket authentication/lifecycle. Its
WebSocket harness requires Unix process groups for bounded server cleanup.
It checks authentication, reconnection and client/server lifecycle through the
SDK, Go networking and the server process handle. It does not inspect process-tree
listener ownership or assert the absence of additional listeners. Claude currently has
only an initialization/shutdown transport integration probe: it sends no model
turn and does not establish complete agent E2E coverage. Moving this probe does
not add missing real model, tool, question, resume/interrupt or subagent coverage.

Each SDK has independent E2E switches; the directory name does not enable
execution. The former Mac-specific variable names are no longer supported.
Real CLI/agent tests are disabled through `CODEX_E2E_STDIO=0`,
`CODEX_E2E_INTERACTIONS=0`, `CODEX_E2E_WEBSOCKET=0`, `CLAUDE_E2E=0`,
and `SDK_REAL_AGENT_E2E=0`. Codex requires `CODEX_E2E_CLI` and Claude requires
`CLAUDE_E2E_CLI` to select the executable. Each selected test requires its switch
to equal `1` and refuses a nonempty `CI`. Offline validation and recorded replay invoke no real agent/model. Native successful subagent interoperability remains unverified;
see each package's compatibility and recording documentation for precise evidence.

Fixture evidence and its small embedding packages live in each native
`tests/testdata`, alongside `tests/e2e`. Native replay tools embed those files,
so replay tests and examples do not depend on the caller's working directory.
External consumer checks also load both native recordings from the downloaded
module to verify fixture packaging after path changes.

## External consumers

After pushing a commit, validate the new module from independent temporary
consumer modules using its full SHA:

```sh
mise exec -- bash scripts/check-consumers.sh "$(git rev-parse HEAD)"
mise exec -- bash claude/scripts/check-managed-consumer.sh "$(git rev-parse HEAD)"
```

Both scripts create their modules and module caches under `/tmp`, disable
`go.work` and inherited Go overlays/modfiles, and fetch from the new repository
without `replace`. The root script resolves the exact SHA directly from GitHub,
checks its full VCS origin hash, compiles all four public imports, copied public
examples and documented configuration/approval snippets, and runs synthetic
value/configuration checks and the [pure Codex public consumer](../tests/consumers/codex-public/main.go).
This consumer checks import identity, API signatures and configuration only; SDK
usage demonstrations stay under each native package's `examples/` directory. The retained
Claude script separately compiles its native consumer and configuration snippet.
Dependency download requires network access; compilation/runtime checks then use
`GOPROXY=off` and `GOSUMDB=off`. The root script also builds both [mock CLIs](mock.md) from the fetched module
and opens real SDK clients against them to test bidirectional question/permission
answers, interrupts, local cancellation, disconnection and strict mismatches.
Neither script invokes a real agent CLI or model. The mock consumer also pauses
event consumption during 2,048-event bursts with the legacy capacity set to one,
answers questions/permissions and finishes control RPCs, then verifies complete
ordered drain after disconnection. Shared FIFO tests cover cancellation, atomic
batches, concurrent producers, close/abort races and reference release. See the
[event delivery contract](../harness/contracts/event-delivery.md).

External consumer CI uses the same `ubuntu-24.04`, `macos-15` and `macos-26`
matrix for pushed `main` commits, manual workflow runs and same-repository PRs.
Push/manual consumers target `github.sha`; PR consumers check out and fetch the
published `github.event.pull_request.head.sha`, never the synthetic merge SHA.
These events configure six jobs (three Offline and three External consumer).
Fork PRs retain three Offline jobs and skip consumers because fork-only SHAs are
not assumed retrievable from this repository. Token permissions remain contents:
read; no pull_request_target execution is added. Configured coverage does not
prove that any runner executed. See
[release readiness](releasing.md) for the required exact-commit CI evidence.

## Testify migration scope

The inventory covered all 61 original `*_test.go` files: 310 Test functions,
four TestMain dispatch hooks, six Go Examples and 750 generated schema cases.
All original test names remain represented. Fifty-five original test files were
updated (including moved replay tests); six assertion-free files retain native
Example, platform helper, view adapter or protocol-peer roles. Five new test
files bring the total to 66.

There are 15 sequential suites with 187 scenario methods across native client,
permission, session, observation, replay, live opt-in, shared graph and story
tests. Single-function cases use named tables. Native synctest, tight-deadline,
concurrent stress and subprocess dispatch keep their testing structure; no
existing deadline or expected result was relaxed. Workers return outcomes to
the owning test goroutine. Go Examples and four TestMain hooks retain their Go
toolchain contracts.

The schema generator preserves the original 750 case names, types and JSON.
The story suite preserves 28 native adapter leaf scenarios. Its private facts
and acceptance methods live directly in `tests/stories/acceptance_test.go`;
there is no separate testkit package. Testify v1.12.1 is a test dependency;
both native types packages still have standard-library-only production trees,
and SDK production imports cannot reach recording tools or fixture containers.

## Independent history-query acceptance

`CommonStoriesSuite.TestIndependentHistoryBrowser` runs both native adapters for
US-014/SDK-US-019. Codex exercises list → read metadata → turns → items with a
strict synthetic app-server trace. Claude exercises list → info → message slices
after compaction/parallel-tool recovery in temporary storage. Existing native
focused cases retain transport, live coexistence, cancellation, unknown, errors,
resource and integrity coverage. The seven offline examples include both native
history workflows; the fetched consumer compiles all three history examples and
runs them from the downloaded revision. See [integration evidence](validation/history-query-integration.md).
