# Phase 2 merge-readiness: approval dispatch scheduling

Follow-up base: `6fc6f57d18fd8c3872506bdc90f772b530652b55` on
`phase2/history-query-integration`. Scope: remove actual merge blockers under the
existing [Session interfaces](../../harness/modules/native-session/interfaces.md)
and [testing rules](../../harness/testing.md), without new runtime features.
The local short spec and ticket are in `.scratch/phase2-merge-readiness/`.
The [history integration report](history-query-integration.md) retains all earlier
failures and exact input/publication evidence. They are not rewritten as passes.

## Short repair specification

Keep these three distinct native dispatch outcomes:

1. Resolution before answer admission: NotSent + ErrRequestResolved, zero response
   frames, terminal original handle and no re-answer.
2. A complete locally committed answer before resolution: success,
   ErrRequestAnswered retained, exactly one unchanged caller response, no re-answer.
3. Resolution/owned Close while a write is demonstrably in progress:
   OutcomeUnknown + the original cause, no retry or regrant. A later complete
   response can exist; never claim it was not sent or that the server accepted it.

Use native public operations and explicit protocol/caller barriers for each
schedule. Keep exact zero/one counts and one-shot assertions; do not blanket
accept errors, skip checks, add sleeps, retry until green, or change correct
production behavior to conceal an invalid test oracle. Preserve strict blocked-
write cancellation/cleanup diagnostics, unknown/raw data and ownership semantics.
Normal work-branch publication is authorized; PR/main merge/force/release are not.

## Reproduction and root cause

The original Codex check failed in 0.871 seconds with this public-path loop:

```sh
mise exec -- go test ./codex \
  -run '^TestServerRequestLifecycleSuite$/^TestConcurrentAnswerAndNativeResolutionHaveOneOutcome$' \
  -count=100
```

The test raced Answer and an RPC asking the independent peer to emit native
serverRequest/resolved. Its oracle assumed only outcomes 1 and 2, mapping **every**
answer error to NotSent/zero frames. But the reader and writer run independently.
The peer can emit resolution after the writer starts and before it settles:
request invalidation intentionally reports OutcomeUnknown. In particular, the
outbound RPC asking the peer to resolve can precede the answer in the writer
queue, so returning that RPC and reading native resolution races the following
answer write. A shared local mutex does not order the remote signal against I/O.
The two-outcome oracle incorrectly classified this third schedule as a defect.

A deterministic ws peer now consumes a payload byte, pauses the remaining frame
beyond socket capacity and emits native resolution. Applying the old NotSent/
zero-frame oracle to this controlled schedule failed in 0.258 seconds: actual
OutcomeUnknown and exactly one original response after release. That diagnostic
mutation was removed; the committed regression asserts the existing contract's
exact third outcome. It also proves the original handle stays resolved after the
write finishes, a retry is NotSent, the connection remains usable, and no second
response appears. Both ws and verified wss execute this regression.

The original stdio Session blocked-write/resolution regression is preserved.
The repaired original two-outcome check still runs 16 iterations through stdio,
ws and wss. A held caller marshaler fixes resolution-before-admission; the other
schedule withholds native resolution until answer completion. Both goroutines
exist concurrently, but the observed boundary is explicit. Assertions remain
exact NotSent/zero or success/one, with new exact payload and stale-handle checks.
No arbitrary scheduler result selects a weaker expected outcome.

The separate Claude close/answer stress failed at the clean-Close assertion:
`go test -race ./claude -run '^TestSessionPermissionConcurrentAnswersAndClose$'
-count=100`. Sixteen answers raced owned Close; Close could interrupt an already
authorized write. Its contract deliberately retains an unknown write/cleanup
outcome, as the existing deterministic FIFO tests require. Requiring Close to
always succeed contradicted those in-flight tests.

The repaired Claude check keeps 16 competing answer callers. Its close schedule
holds the winning caller's own context preparation after reservation and before
write authorization, proving Close does not wait for caller code. It now requires
zero successful answers, zero response frames and **no** OutcomeUnknown. The
answer schedule requires exactly one successful answer/response and all other
callers to fail as already used. A native control acknowledgment establishes
peer consumption before counting the trace; local write completion alone did
not establish that the subprocess had recorded it. Existing FIFO tests still
require OutcomeUnknown for interrupted writes and preserve it in Close and Err.

The evidenced defects were the schedule assumptions in the acceptance tests.
Production dispatch/permission state machines already retain the correct
uncertainty; none is changed. Suppressing those errors in production would break
the established cancellation/cleanup guarantees.

## TDD and diagnostic evidence

The original public regressions were red before the repair. The deterministic
in-flight counterexample demonstrates why their unconstrained oracle was wrong.
The repaired schedules retain exact original outcomes and add stronger wire and
permission checks. An initial new payload assertion used the wrong literal for
the existing held marshaler; it was corrected to its actual fixed artificial
payload. An initial stronger Claude exact-one trace assertion lacked a peer
consumption barrier and observed zero; the native acknowledgment barrier corrected
that synchronization issue without reducing the count. Failed development logs
remain separate from final validation.

All Go commands use mise-managed Go 1.26.9, real-agent gates zero, GOMAXPROCS=2 and
GORACE=atexit_sleep_ms=0. Logs are under the ignored local validation directory;
no private session, credential, new model execution or recording is involved.
The parent reported that 6fc6f57 re-read the unchanged synthetic real-session
files successfully through both providers' list/metadata/content and paging,
without another model call. Claude was Claude Code + Magpie + a Codex model.
That is parent-reported evidence at its exact SHA, not a new run in this context.

## Main and CI checks

Read-only GitHub connector results initially reported main protection disabled,
required status enforcement off/empty contexts, and no repository rulesets.
The detailed branch-protection endpoint was denied 403 because this integration
has no administration permission; no configuration was changed. Workflow source
supports main pushes, pull requests and workflow_dispatch. A work-branch push
alone cannot certify CI. A manual dispatch on the final branch can run both
Ubuntu/macOS offline and fetched consumers, so a PR is not technically required
for cross-platform CI. No PR or workflow dispatch was performed here.

A separate authorized task added ADR-only main commit
`6b00af9494e111ebe6f5f2b8b0297376a9a74b91`, tree
`db24bab6020db068032d65c110c721694b8691c2`. Its five files are documentation only.
This branch refreshes and preserves those documents, without implementing the
new child-flow design or changing live child routes.

Final validation, independent reviews and publication results follow below.
