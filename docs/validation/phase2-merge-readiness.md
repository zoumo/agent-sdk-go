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

## Independent review correction

Initial review of `6fc6f57...a1e6709`: Spec found 0 deviations and independently
confirmed that the faulty component was the test oracle, not the production
state machines. Exact counts, concurrent one-shot coverage and in-flight
uncertainty remain covered. Standards found one failure-cleanup issue: a held
caller test synchronously called Close, so a Close regression waiting for that
caller could trap the test before cleanup. Close now runs in a goroutine with a
bounded assertion, and failure cleanup releases the caller barrier and attempts
bounded collection of both Close and all answer workers. Strict zero/one counts,
no-unknown prewrite checks and all original causes are unchanged. The updated
Claude race check again passed 100 repetitions.

The parent supplied current main CI evidence: run
[40175585836](https://github.com/zoumo/agent-sdk-go/actions/runs/40175585836),
commit 6b00af9, passed Linux offline and both fetched-consumer jobs, but its
[macOS offline job](https://github.com/zoumo/agent-sdk-go/actions/runs/40175585836/job/121115416409)
failed exactly the Claude concurrent Close clean-result assertion diagnosed
above. This is a real current-main CI failure and remains distinct from the
previous original-baseline 4/4 successful run. The fix covers both providers.

The GitHub connector exposes workflow reads but no callable dispatch action.
The installed gh CLI's harmless workflow-list request was blocked with Forbidden
before any dispatch attempt. Thus this context cannot trigger the final branch's
cross-platform manual run through those available paths. No PR or CI settings
change is substituted. A parent/user with workflow-dispatch access can select the
final work branch in SDK checks; a PR is not required by the workflow source.

## Final local verification

Reviewed implementation revision: `e124883e94364efc0d8535db0acd10cae2a251a3`,
tree `cb51a1b1733c373332c182df7bf5af07a27e36e6`. Subsequent evidence commits do
not change Go source. Main merge commit is
`a1e6709557612f1fa22a57d3697bea99dff2fa8e`, parents
`a23ad1c9f1a8e90516b82e13228bb80e3a5287e8` and
`6b00af9494e111ebe6f5f2b8b0297376a9a74b91`. The only merge conflict was docs/index.md;
all Phase 2 links and the independent child-delivery research link were retained.
ADR/research/index files match main, and glossary additions are preserved.

### Standards

After the bounded-cleanup correction, 0 remaining standard violations and 0
smells needing correction. Exact assertions, cleanup, native public boundaries
and caller/peer barriers were independently reviewed; no sleep fix or runtime
semantic change was introduced.

### Spec

0 remaining deviations. The repair fixes demonstrated test-oracle/schedule
defects and retains every required dispatch category. Existing in-flight and
one-shot coverage remains, with stricter exact counts and additional ws/wss
resolution coverage. Cross-platform CI is not claimed from focused/local passes.

| Gate on final reviewed Go source | Result |
|---|---|
| Codex original failing public check before correction, 100 repetitions | Failed with old NotSent/zero oracle, retained |
| Deterministic ws in-flight schedule under the old oracle | Failed in 0.258s; diagnostic-only mutation removed |
| Codex ordered stdio/ws/wss checks with race, 100 repetitions | Passed, 18.494s |
| New ws/wss in-flight native resolution with race, 10 repetitions | Passed, 32.573s |
| Claude concurrent-answer/Close race, 100 repetitions after cleanup correction | Passed, 2.091s |
| Full offline script after main integration and final cleanup correction | **Passed**: all packages, checkout public consumer, seven examples, real E2E gates skipped |
| Generation/inventory/module/dependency/CI-gate/sanitization/hash/transcript gates | Passed within full offline script (417 types / 783 generated cases) |
| Full race `-race -p=1 -v -count=1 -timeout=300s ./...` | **Passed**, every package, no DATA RACE report |
| Full vet | Passed |
| Committed whitespace/gofmt, shell syntax, OKF/internal/touched links | Passed |
| Real-sanitized fixture diff against 6fc6f57 | Empty |
| Production Go, module and dependency diff against 6fc6f57 | Empty |
| Main refresh and ancestor check | Latest known main 6b00af9 included; no main write |
| Standards / Spec after correction | 0 / 0 remaining |
| Final exact published-SHA external consumers | Recorded in ticket/final publication handoff after immutable evidence commit creation |
| Final-revision macOS / GitHub Actions | Pending manual SDK checks dispatch; not a local pass |

The initial complete runs on the pre-cleanup-review implementation also passed.
They were repeated only because the review caused a real test-source correction,
not to seek a lucky scheduler result. No failed run was hidden and no gate was
skipped to get green. Earlier failures remain historical diagnostic evidence;
they no longer describe the final local gate result.

The connector recheck after main's ADR commit reports main
`6b00af9494e111ebe6f5f2b8b0297376a9a74b91`, protected=false,
required_status_checks enforcement=off with empty contexts/checks, rulesets=[].
A PR is not a configured prerequisite, and this task creates none. The remaining
cross-platform evidence can be obtained, with suitable workflow permission, by:

```sh
gh workflow run ci.yml --repo zoumo/agent-sdk-go --ref phase2/history-query-integration
# Verify the resulting run.head_sha equals the final published handoff SHA.
```

This workflow's Ubuntu/macOS Offline and External consumer jobs should all be
checked against that exact SHA. The read-only connector has no dispatch action;
the installed gh path's workflow-list API call was Forbidden, so no dispatch was
attempted and no CI operation has an uncertain outcome. This remains a pending
external verification action, not a remaining diagnosed code/test defect or an
excuse to label missing CI green.

Final full offline log SHA-256:
`5dac5be0a0a3f42a5a959b50e5ee99f2c9c22d261e97bd851c92d229544d0bb0`;
full race: `fdef2f76e1f103b3460352679a951fc64c695ca115ff951947383aa2b3f3b8d5`;
Codex ordered race100: `352e0cfc43f205916f1fb3e9edde3bb1219e41cc1d647e30e5a0dfe6ed4e3999`;
Claude cleanup race100: `97b4571bd2792511780dd8a42a3d101efb19f15cf8680ffd0d963d699e75dfc2`.
All logs, the separate short spec/ticket, before/after checks and publication
receipt remain in `.scratch/phase2-merge-readiness/`. They contain synthetic
protocol/test results, not real session material or credentials.

## macOS 26 matrix follow-up

Base: `e922c0b81f39b897e3c0a70e72157fdb09c88014`.
The user explicitly requested retaining macOS 15 and adding macOS 26 to both
Offline and External consumer coverage. Ticket/spec are recorded separately in
`.scratch/phase2-merge-readiness/issues/02-macos26-matrix.md` and spec.md.

Both existing ci.yml matrices now declare `ubuntu-24.04`, `macos-15`, `macos-26`.
This expands main push/manual dispatch to **six jobs**: three Offline and three
External consumer. PRs retain three Offline jobs; the consumer exclusion remains.
Prior four-job receipts in this report describe their older exact commits and
are not rewritten. Current testing/release instructions now require all six
jobs on the intended SHA. Action/compiler/interpreter pins, permissions,
concurrency, triggers, checks, opt-out gates and timeout budgets are unchanged.

GitHub's [official macOS 26 runner announcement](https://github.blog/changelog/2026-02-26-macos-26-is-now-generally-available-for-github-hosted-runners/)
confirms the explicit macos-26 standard label. It is used as an additional named
OS target, rather than replacing macOS 15 or depending on a moving latest label.
This source check establishes label availability, not successful execution of
our pinned toolchain or tests on that host.

TDD: a new WorkflowCoverage case in the existing native Python CI-gate tests
reads the actual workflow's two job matrices and checks the independently stated
three-label requirement plus matrix-based runs-on. It initially failed separately
for both missing macos-26 entries; changing only the two OS lists made it pass.
It also guards accidental removal of macOS 15 or a one-job-only update. No YAML
package or new validation framework was added. The first broad Python invocation
lacked mise's gofmt and also failed the existing generator test; rerunning the
focused red under mise isolated only the two intended matrix failures. All seven
Python CI-gate tests passed after the matrix change under the pinned environment.

This follow-up does not alter production code, approval scheduling fixes,
recordings or the incorporated main ADR documents. Final local checks, reviews
and exact publication receipts follow in the ticket/handoff. The previous gh
Forbidden is respected: no retry, alternate identity or route is used to dispatch.
No PR, main merge or remote CI execution is claimed. A user/parent with appropriate
access must run SDK checks on the final branch/SHA and inspect all six jobs;
configured macOS coverage is not a macOS pass.


## Additional blocker exposed during macOS matrix validation

The first complete checks after matrix commit e60caff failed: the offline script
reached the S2 recorded-replay example and returned OutcomeUnknown +
ErrRequestResolved; full race failed
TestRecordedManagedIntegration/s2/repetition-3 in codex/tools/replay. These failed
checks are retained, not marked green. The matrix commit changed no Go code.
Ticket `.scratch/phase2-merge-readiness/issues/03-recorded-replay-answer-boundary.md`
records the narrowly scoped follow-up.

The strict recorded peer validated the client response, then immediately emitted
the next recorded native resolution. That signal could precede the local Answer
call settling. The SDK's conservative OutcomeUnknown is valid, but this replay
is meant to verify the recorded successful-answer ordering, not randomly select
an in-flight cancellation schedule. RunSession correctly rejected the error;
no error handling or native dispatch semantics is relaxed.

The existing private offline replay protocol now has a per-answer settlement
barrier in addition to its final assertion. After an exactly matched reverse
answer, the verification peer waits for replay/answer. Only Replay's own driver
sends that control after the public Answer call has returned successfully. Every
answer error remains fatal. The peer then acknowledges the private control and
continues the original captured frames in their original relative order.
Neither the golden bytes/hashes nor expected observations are changed. Public
RunPeer/mock CLI scenarios and the live recorder have no settlement controls and
retain their original behavior; native in-flight cancellation is still tested
with strict OutcomeUnknown and no-retry assertions elsewhere.

A literal public Peer regression first failed with extra client request. It now
checks exact acknowledgment-before-resolution order, rejects missing/wrong/
malformed/parameterized settlement controls without releasing native frames,
and verifies public RunPeer needs no private handshake. New control output
counts toward the existing frame/traffic budgets. The original ID correlation
and replay mismatch tests remain. The synthetic private-peer driver was updated
to use the same explicit settlement mode; an initial development stress run found
that missing pairing and was stopped after diagnosis, rather than counted as a
pass. Focused race and all handshake/fault tests pass after the correction.

The original failing recorded S2 path then passed race count=10, covering its
three existing repetitions each time (30 replays) in 30.967 seconds. No sleep,
retry-until-green, discarded native error, weakened matcher, real CLI/model or
new recording is part of the repair. Complete gates and independent review are
repeated for the combined matrix/tool change before final publication.

## Configuration-cancel write boundary

The next full race run passed after the replay repair. Full offline then exposed
Claude TestSessionConfigurationCanceledAfterWriteIsUnknownWithoutRetry/cancel at
the strict healthy-connection assertion: peer reception of the control did not
prove the local Write's cancellation hook had been stopped. Cancel could still
interrupt that write and correctly terminate the connection with an unknown
outcome. The failed run remains retained. A separate pre-fix focused cancel race
count=100 happened to pass; that is not evidence that the observed failure was
fixed and was not substituted for the failing full run.

Ticket 04 adds a public native control acknowledgment before the deliberate
manual cancellation. It traverses the same serialized writer, establishing that
the earlier write and its callback cleanup have settled. The unanswered model
configuration must still return OutcomeUnknown, the Session must remain healthy,
and subsequent thinking configuration and Close must succeed. The 100ms timeout
case and its deadline are untouched. Exact trace assertions now include the
intentional interrupt barrier and require the precise subtype sequence, including
exactly one set_model; no retry or extra operation is permitted. Native SDK code
and write-cancellation semantics remain unchanged.

Both cancel and timeout subcases then passed race count=100 in 12.647 seconds.
No sleeps, enlarged deadline, discarded error or relaxed healthy-state assertion
was added. This fixes the test's claimed after-write schedule, not the behavior
of actual in-progress writes, whose strict unknown/termination tests remain.

## Final combined matrix and test-repair verification

Reviewed implementation: `4ac78f522210fb86e95dbd91f447188474ecb22c`, tree
`09d16c21366fa2becf1a696b74cbed73c21b1953`. Review range:
`git diff e922c0b81f39b897e3c0a70e72157fdb09c88014...4ac78f522210fb86e95dbd91f447188474ecb22c`.
Commits e60caff, 62b4cc0 and 4ac78f5 cover the matrix, recorded replay boundary
and configuration cancellation boundary respectively. Later evidence-only
commits do not alter the tested Go/Python/workflow sources.

### Standards

0 confirmed standard violations and 0 smells requiring correction after reviewing
all three changes. Existing native interfaces, strict assertions, tool/runtime
boundaries and capture provenance are retained. The recorded-verification Peer
uses an explicitly private protocol; public RunPeer/mock scenarios do not gain
that protocol. The exported Handler's private replay configuration is used only
by the package's verification driver; it is not a native SDK API or recorder mode.

### Spec

0 outstanding deviations. Both matrices retain macOS 15 and add macOS 26;
recorded replay still fails on every answer/matching error and preserves native
source frames; configuration cancellation now proves its claimed after-write
schedule with a public acknowledgment. No unknown outcome is swallowed, and no
sleep, enlarged deadline, implicit retry or CI permission bypass is introduced.

| Final combined local gate | Result |
|---|---|
| Existing Python CI-gate tests plus both-job runner regression | Passed, seven tests |
| Recorded S2 original failing path, race count=10 with three repetitions each | Passed, 30 replays / 30.967s |
| Configuration cancel + unchanged timeout cases, race count=100 | Passed, 12.647s |
| Full offline script after both observed blockers were repaired | **Passed**, all packages and seven examples |
| Generation, inventory, module/dependency boundaries, CI gates, sanitization/hash and transcript checks | Passed within full offline script |
| Full race (`-race -p=1 -v -count=1 -timeout=300s ./...`) | **Passed**, no DATA RACE report |
| Full vet, committed whitespace/gofmt, shell syntax and touched links | Passed |
| Original native approval fixes and main ADR documents | Retained unchanged |
| Real-sanitized capture/manifest/expected-observation diff | Empty |
| New exact-SHA fetched consumers and remote SHA/tree | Recorded in the final immutable publication receipt / ticket handoff |
| macOS 15 / macOS 26 / final GitHub Actions jobs | **Not run by this task**; configured coverage is not a pass |

The failed full checks before the two actual repairs remain in local evidence.
The successful full run followed source corrections; there was no repeat-until-
green substitution. No actual user's history, account material, real model or new
recording was accessed. The installed gh request previously returned Forbidden;
that denial was not retried, replaced by another identity, or bypassed through
another dispatch route. Git publication and fetched-module verification use the
same already authorized repository paths.

The authorized user/parent should use GitHub Actions → SDK checks → Run workflow,
select `phase2/history-query-integration`, and verify the run's head SHA matches
the final publication receipt. Inspect **six** results: Offline and External
consumer on each of Ubuntu 24.04, macOS 15 and macOS 26. No PR is required by the
workflow source, and none is created. Until that operation actually runs, the
cross-platform CI verification remains pending; this context does not claim it.

Final full offline log SHA-256:
`c077896bb71351fe6c3d1a37d679ae7d944fec3d142f83ed2a92bbe30d970d42`.
Full race log SHA-256: `764c08031aa9fef97261e96dc197e0c68a695e5a5e294b091c812eb984fefff3`.

## PR-triggered CI and authorized squash

The user subsequently authorized creating a PR to trigger CI and squash merging
only after all checks, review blockers and mergeability are verified. Draft
[PR #2](https://github.com/zoumo/agent-sdk-go/pull/2) was created from
phase2/history-query-integration into main at head
`3841c6f3417f38d4235533a8f7235fa44eab8522`. Auto-merge was not enabled.
This explicit authorization replaces the earlier no-PR/no-merge boundary for
this PR; it does not authorize bypassing failed or missing checks.

Initial [PR run 40179658444](https://github.com/zoumo/agent-sdk-go/actions/runs/40179658444)
actually ran three Offline jobs: Ubuntu succeeded, macOS 15 and macOS 26 failed
in the same explicit-project fixture, and External consumer was skipped. It was
not a six-job pass. The workflow's old consumer condition excluded all PRs, and
its SHA expression selected the synthetic merge commit. These are changed only
for same-repository PRs: consumer checkout and retrieval now both use the actual
published PR head SHA. Push/manual retain github.sha; fork PR consumer jobs stay
excluded because their commits are not assumed retrievable from this repository.
No token permission, trigger family or pull_request_target execution is added.
The new real-workflow regression first failed, then all eight CI-gate tests
passed with the same-repository condition and matching head-SHA checkout/fetch.
Current testing/release documentation reflects six jobs for same-repository PRs.

The two macOS failure logs showed the public query searching a canonical
-private-var project key, while the fixture had encoded the lexical /var temp
path. The contract intentionally resolves ProjectDir symlinks. An explicit
symlink project alias reproduced this failure on Linux with the original
fixture. Synthetic storage is now created from filepath.EvalSymlinks(project),
while the public Scope still receives the alias. Strict success, selected-message
count and identity are retained/strengthened; native query behavior is unchanged.
Focused scope race count=10 passed. This removes the fixture's Linux-only path
assumption without skips, relaxed scope semantics or access to real history.

The new PR synchronize event must actually run all six jobs on the final head
before squash. Internal Standards/Spec reviews and applicable local gates are
recorded in the ticket/publication handoff. Immediately before merging, verify
the final head, successful checks, no unresolved review blocker, mergeability and
no auto-merge; request squash with expected_head_sha. Then verify the resulting
remote main commit/tree, preserved ADR and six main-push CI jobs. Do not manually
delete the work branch. Any CI failure must be diagnosed rather than retried into
a lucky result. This follows the newly authorized PR event path and does not
retry or route around the earlier Forbidden manual-workflow operation.
