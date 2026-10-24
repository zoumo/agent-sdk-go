# History event delivery validation

Date: 2026-10-22. Implementation baseline: `cde4df8`.
Feature commit: `a22e4d9`; subsequent validation fixes preserve the same contract.

## Approved contract and tickets

The user approved both provider Load handles moving directly from Records to
Events, public Load/Events/Done/Err/Close test seams, and the local-file tracker.
The [specification](../../harness/contracts/history-events.md) and three tickets cover Codex, Claude and
integration. The two provider tickets have no blockers; integration depends on
both. Tickets are individually stored under `.scratch/history-events/issues`.

Codex Load.Events yields the same `types.Event` as subscriptions. Claude
Load.Events yields `ReceivedEvent`, carrying the same native Event values used
by Receive. Public acceptance tests use one handler for historical and
subsequent live events from offline peers. No provider-neutral protocol was added.

## Evidence

- Red: each provider's new public acceptance test failed to compile because its
  Load handle had no Events method. Claude also lacked history-source metadata.
- Green: focused Load/History tests passed for both providers, including
  previously existing resume, mutation, branching, compaction, limits,
  cancellation, session isolation, late-error and cleanup cases.
- Codex unknown-status red: an empty unknown-status turn produced no event.
  The implementation now retains the native Turn without claiming completion;
  the public acceptance test passes.
- Codex raw-evidence red: the item event repeated the entire source turn.
  Item events now retain exact original item JSON; the terminal or unknown turn
  event retains full-turn JSON, avoiding whole-turn duplication for every item.
- Literal native item assertions cover tool failure/output, unknown item and
  turn fields, compaction and interrupted turn status. Missing live completion
  timestamps are explicitly marked in History.MissingFields and omitted from
  projected Params, never presented as observed timestamps.
- Claude assertions cover exact original transcript bytes, full thinking/tool
  use/result blocks, unknown content, validated native session identity and
  exclusion of SDK-only metadata from native serialization.
- Full offline validation passed: module and dependency boundaries, pinned
  generation and inventory, recording/transcript hashes and sanitization,
  full Go tests, public API consumer and runnable offline examples.
- Full race validation passed across all packages; the two corrected Standards
  regression cases also passed a focused race run.
- `go vet ./...` passed with the pinned Go 1.26.9 compiler.

The initial offline run stopped because the default macOS Python 3.9 lacks
tomllib. Re-running with the already-installed Python 3.13 via a temporary PATH
shim passed. No repository dependency, compiler pin or fixture was changed.

Commands (all executed through rtk proxy):

```sh
mise exec -- go test ./codex ./claude -run 'Test(Load|History)' -count=1 -timeout=60s
PATH="/tmp/sdk-history-events-tools:$PATH" mise exec -- bash scripts/check-offline.sh
CODEX_E2E_STDIO=0 CODEX_E2E_INTERACTIONS=0 CODEX_E2E_WEBSOCKET=0 CLAUDE_E2E=0 SDK_REAL_AGENT_E2E=0 GOMAXPROCS=2 GORACE=atexit_sleep_ms=0 mise exec -- go test -race -p=1 -count=1 -timeout 300s ./...
mise exec -- go vet ./...
```

## Independent review

### Standards

Initial findings: two P2 test regressions, both fixed and independently rechecked.
The resume-failure test now counts every event rather than only terminal turns,
so early item delivery cannot evade its zero-event assertion. The pagination
cancellation test's added receive now has its own timeout and checks the terminal
method. Focused regression tests passed after both fixes. No remaining findings.

### Spec

No actionable findings. Independent source/test review found the approved type
reuse, retained evidence, missing-field semantics, unknown fallback, no live
authority fabrication and existing lifecycle behavior represented in the change.

## Limits

All new execution evidence is offline/synthetic. Existing real sanitized capture
provenance is unchanged. No personal-session Load, real agent/model task or new
recording was run for this implementation; the earlier private audit concerns
the pre-change SDK and is not claimed as evidence for this revision.

Fetched exact-commit external consumer validation is conditional on a pushed
revision and was not run: this work does not push or release. Both native
types packages remain standard-library-only, and generated protocol schemas
were not modified. Historical projection does not restore missing execution
timing, original live-event sequences or answer authority.
