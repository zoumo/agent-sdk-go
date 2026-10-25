# Phase one: remove Session Load

Baseline: `zoumo/agent-sdk-go` main `fc395595d9b5eb95038fdbe1188aeb9578997052`.
Worktree: `/workspace/agent-sdk-go`, local branch `phase1/remove-load`.
The stage-one change is isolated in a local checkpoint; publication and merge
status are tracked by its independent PR. The validation below records the local
pre-publication snapshot. The original
`/workspace/codex-sdk-go` checkout remains unchanged.

## Delivered scope

Removed both native packages' Load orchestration, LoadOptions/LoadState/status,
ReceivedLoad boundaries, input/configuration gates, startup/replay cancellation
workers, Load-only identity diagnostics and tests/peers. Removed the two Load
contracts, their validation plans and native implementation guides; updated
active harness/SDK docs, ADR names, indexes, migration, capability references and
historical-test migration inventory. Previous evidence reports remain explicitly
marked as pre-removal history; they do not certify the current APIs.

Preserved native Codex ResumeThread, UnsubscribeThread and thread/turn/item query
APIs, Claude Open+ResumeID, original permission behavior and Session delivery.
Existing bounded transcript/history parser/projection foundations remain internal.
Their independent regression tests survive without a public Load lifecycle.
Mock/replay scenario Load functions are unrelated and remain.
`go mod tidy` produced no go.mod/go.sum change: no module dependency was exclusive
to Session Load.

The opted-in native Codex E2E tests still contain the second continuation turn
and stored-marker recall check through ResumeThread/StartTurn/ReadThread. They
were compiled and skipped, never enabled or executed against a model.

Official interface verification, pinned sources, examples, limitations, actual
meaning of harness/agent layers and phase-two boundary proposals are in
[history-query-boundaries](../research/history-query-boundaries.md).
Phase two has no new public interface, adapter or generic protocol in this diff.

## Validation

Standards: [testing](../../harness/testing.md),
[architecture](../../harness/architecture.md),
[layout](../../harness/code-layout.md).

All Go commands used mise-managed Go 1.26.9. The host initially had neither mise
nor the required Go compiler. Tool downloads/installations and caches were kept
under /tmp. `/tmp/mrun` sets isolated mise and Go paths and executes the pinned
compiler. It is a local convenience wrapper, not part of the SDK.

| Check | Result |
|---|---|
| go mod download / tidy / verify | Passed; dependency files unchanged |
| scripts/check-offline.sh boundary, protocol inventory and generation gates | Passed: 417 types, 783 generated synthetic cases; 173 client requests, 11 server requests, 86 server notifications, 1 client notification, 24 controls |
| Sanitization, recording hashes/correlations, Claude transcript fixture checks | Passed; real capture bytes/provenance unchanged |
| Full offline go test within check-offline.sh | Failed on two pre-existing concurrent answer/resolution cases listed below; all other package results passed in this run |
| GOMAXPROCS=2 GORACE=atexit_sleep_ms=0 go test -race -p=1 -count=1 -timeout=300s ./... | Failed on pre-existing Codex answer/resolution test; other packages passed, no race-detector DATA RACE report |
| go vet ./... | Passed, including final recheck |
| Migrated history query/reader/projection/payload tests with race | Passed |
| Public Codex consumer and all four documented offline examples | Passed separately after the full offline script stopped at failed tests |
| git diff --check | Passed |
| Real agent/model E2E | Disabled/skipped; no live compatibility claim |
| External exact-pushed-SHA consumer | Not run during local validation; local consumer is not a substitute |

Focused history race command:

```sh
GOMAXPROCS=2 GORACE=atexit_sleep_ms=0 mise exec -- go test -race -p=1 -count=1 -timeout=60s \
  -run 'TestHistoryReaderIntegrity|TestHistoryProjectionRetainsNativeFacts|TestHistoryNativePagesWithoutResume|TestObservationSuite/TestSessionOwnsPayloadAndNoReplay' ./codex
```

## Baseline failures and limited fixture corrections

The unchanged base was independently extracted with `git archive HEAD` before
any commit into `/tmp/agent-sdk-go-baseline` and tested with the same Go version.
This distinguishes baseline defects from the Load removal.

Two deterministic baseline test issues were corrected, with no production change:

1. `claude/TestSessionDiscoveryLimitsAndPartialCoverage/entries` expected one
   record under MaxEntries=1, but ReadDir may encounter the nested directory
   first and legitimately produce zero records. It failed on original main and
   repeatedly in the changed tree. The entry-limit fixture now has only files;
   the separate depth case still retains its nested directory. The expected
   resource error, partial coverage and record count are unchanged.
2. `claude/TestControlInteractions/TestQueuedSendAndInterruptDoNotInheritStartedWriteAmbiguity`
   expected a user frame beginning `{"message":`, whereas the existing encoder
   sends `{"type":"user","session_id":...}`. Original main reproduced the same
   failure. Only the stale prefix assertion was aligned; the blocked actual
   write barrier and unknown-outcome/queued-operation assertions are unchanged.

Remaining baseline failures were not expanded into production fixes:

- `codex/TestRemotePublicContractParity/ws/TestConcurrentAnswerAndNativeResolutionHaveOneOutcome`
  expects NotSent and zero answer frames whenever resolution wins. It observed
  OutcomeUnknown and one frame. A five-run baseline check reproduced the same
  failure for ws and wss. Full offline and race runs hit this test. Source:
  `codex/request_contract_test.go:967` and `:977`.
- `codex/tools/replay/TestManagedRecording/TestRecordedManagedIntegration/s2/repetition-1`
  observed `answer item/tool/requestUserInput (outcome-unknown): server request
  resolved by server`. A five-run baseline check reproduced it. Full offline
  failed here; the full race invocation passed this package, confirming timing
  dependence. Source: `codex/tools/replay/recording_test.go:296`.

These are unresolved baseline concurrency/test-contract issues, so full-suite
acceptance is blocked and this change is not declared release-ready. No assertion
was weakened or test skipped to conceal them.

## Review and evidence

Independent code-review Standards and Spec passes initially identified retained
history test loss, lost native continuation coverage and one obsolete capability
link. All were addressed; rechecks reported zero outstanding documented-standard
or spec violations. A bounded context was also added to the internal history
protocol test seam.

Local supporting logs:

- `/tmp/agent-sdk-go-offline-final.log`
- `/tmp/agent-sdk-go-race.log`
- `/tmp/agent-sdk-go-phase1-race.log`
- `/tmp/agent-sdk-go-vet-final.log`
- `/tmp/agent-sdk-go-examples.log`
- `/tmp/agent-sdk-go-answer-baseline.log`
- `/tmp/agent-sdk-go-replay-baseline.log`

The local validation accessed no private sessions or host Codex session directory,
and performed no paid model calls, new real recordings or release. There was no
automatic approval denial; the remaining blocker is the failed baseline tests.
