# Ticket 14 integration verification

Implementation base: `fcf122fba4eefdca624101517171fc059ad41538`.
Whole-refactor review base: `77abc43d29eac8786f317e3e8ac6b354160a346b`.
The implementation revision is the commit containing this report. The ignored
local T14 ticket records its full SHA after commit. This report is evidence,
not a replacement for the [Session contract](../../harness/modules/native-session/interfaces.md).

## Integration repairs and red/green evidence

- Claude native object decoding now uses exact JSON tags. Unknown `TYPE`,
  `SESSION_ID`, `REQUEST_ID`, role/content and permission-field casing remains
  unknown Raw and cannot overwrite exact typed values. Control response routing
  uses the already-decoded exact native response; permission registration uses
  native PermissionMetadata. Raw, absent/null presence and typed edits survive.
  The initial `TestExactNativeDataKeys` failed with wrong session/type values;
  the public structured-input mixed-case test separately failed on a fabricated
  role. Both now pass, alongside wrong-case-only input rejection and subsequent
  normal Submit. Public live tests exercise actual request/cancel handles and
  exact control response correlation, not a separate writer or registry model.
- Claude duplicate exact/escaped keys, invalid UTF-8 and unpaired UTF-16 are
  rejected before identity/authority creation. Decode/route Error.Raw retains
  offending bytes without including them in Error text. Existing accepted facts
  drain; malformed requests never provide new handles. Claude Load clones the
  added Raw in Error snapshots. A public malformed-startup Load regression first
  demonstrated mutation leaking across snapshots, then passed with deep copying.
- Codex pre-codec source and parent extraction validates native identity strings
  before decoding. A genuine U+FFFD-containing member receives valid facts;
  malformed surrogates cannot route to it or create members/parents. Valid main
  identity still admits unknown facts even if another identity field is invalid.
  The public regression initially observed a normalized U+FFFD turn ID and now
  verifies exact accepted order plus seven quarantined original frames.
- Codex history splits `data`, `items` and `id` using exact keys consistently with
  generated types. Public Load initially panicked at history.go's raw/typed
  index mismatch with `data:[turn],DATA:[]`; it now retains exact content/IDs and
  unknown original bytes. Unknown-only required keys fail without projection.
  Missing/wrong-case-only nextCursor originally announced success; the final
  implementation requires an explicit exact cursor/end marker.
- Codex HistoryError preserves actual failing native page/read bytes, thread,
  stage, input cursor and number of accepted history events, using copy-returning
  Raw accessors. The public second-page failure initially had no recoverable
  page; it now reports page2, exact malformed page and two already-admitted facts.
  A small-MaxRecords regression also proves an unrequested next page reports
  its uncovered cursor, no fabricated response Raw, and the correct accepted
  count; the loop freezes that position before enforcing its page budget.
  Load snapshots clone mutable native error trees: the native RPC regression
  initially leaked mutated Code/Raw, then passed for repeated state and boundary.
- Claude large text previously passed through several nested Marshal/Unmarshal/
  Raw compact operations before writer admission. Three focused race repetitions
  returned NotSent before dispatch for both original 1 MiB/100 ms fault cases
  (roughly 0.56–0.66 s per call). A CPU profile confirmed JSON marshaling,
  validation and compact costs. Text now encodes once inside the same writer
  admission/limit/authorization path; structured-input preparation is also inside
  admission. No deadline or payload reduction, retry or dispatch relabeling was
  introduced. Closed stdin is acknowledged by the actual peer; blocked stdin
  consumes and reports a real 64-byte prefix before blocking. The test submits
  via public Session.Submit. One intermediate 20-run failure came from polling
  quarantine for a synthetic marker actually delivered through Session FIFO;
  the corrected public Receive check passed 20 rounds with the original 100 ms
  deadline, 1 MiB input and existing 2.5 MB/one-second pipe barriers.

## Whole-refactor contract audit

Read final source, public acceptance bodies and native request/Load tests; the
T13 per-function inventory was navigation, not proof. The following rows are
implementation/evidence conclusions for the fixed native baselines. The common
public tests are `CommonStoriesSuite/TestSessionTargets/SDK-US-NNN/{codex,claude}`
in tests/stories, with native package regressions for faults and mechanical
codec/history/queue/graph invariants. Synthetic acceptance is not live compatibility.

| Goal | Final implementation and checked public observation |
|---|---|
| 001 | Open/Submit/Receive remain live across two results; Codex duplicate owner fails. |
| 002 | Main target fixed; exact structured native content/options retained; unsupported/redirection rejected. |
| 003 | Additional input uses native submission while prior execution remains unresolved; native rejection is preserved. |
| 004 | Submission, tool result, execution result and interaction termination remain distinct. |
| 005 | Duplicate native Raw/large numeric unknowns survive; T14 adds exact-key and malformed-identity fidelity. |
| 006 | Member evidence independent of optional parent; nested/sibling/parent-unknown output survives without tree ordering. |
| 007 | Root result does not close children; unavailable forwarding/unknown communication targets expose coverage gaps. |
| 008 | Codex native ancestor pagination/archive/source filters and Claude fresh bounded storage discovery; no resume/subscribe/input side effects. |
| 009 | Actual same-stream handles preserve native approval/denial extensions and exact request correlation. |
| 010 | No SDK unattended decision; native cancellation invalidates immediately; pre-dispatch cancellation preserves still-live authority. |
| 011 | Actual question handles retain original question/answer structure; ordinary input and relationship facts grant no authority. |
| 012 | Creation and native dynamic model/thinking configuration remain distinct; unsupported input is explicit. |
| 013 | Receipts expose requested/accepted evidence; separately received native values can differ; no synthetic observed setting. |
| 014 | One per-Session ChannX FIFO; paused 256-fact stories and explicit 2048-fact mock consumer retain ordered facts while controls progress. |
| 015 | Receive cancellation local; submitted/answer write ambiguity remains OutcomeUnknown/no retry, queued NotSent remains distinct. |
| 016 | Native Interrupt distinct from Close; Codex shared-connection sibling remains usable, owned processes are reclaimed independently of consumption. |
| 017 | Native task failure leaves interaction live; transport failure invalidates handles, drains 32 accepted facts (Claude exact sequence), and preserves first cause. |
| 018 | No automatic reconnect/retry or invented missing interval; subsequent input on failed Session fails. |
| 019 | Codex confirmed same-ID resume plus stable complete history, same FIFO interleaving with actual approvals/interrupt, overlap/cleanup fencing. Claude successful Load remains a native gap. |
| 020 | Codex original retained history, native correlation/unknowns and absent timestamp evidence; no live approval from history. Claude success-dependent replay remains a native gap. |

Source audit confirms one Session registration per connection/main identity,
Session-owned FIFO only, sole native request registry/handle authority, real
waiting without local approval timeout/default denial, bounded quarantine and no
backfill. Load pending blocks ordinary input/configuration while actual answers,
Interrupt and Close remain usable. History is progressive into the same FIFO;
state/termination/drain are distinct. Codex attachment reference/fence logic
preserves overlapping observation and shared-client cleanup. Claude ordinary
Open ResumeID remains a literal native resume with no historical replay; explicit
Load does not forbid that ordinary path. Old independent subscriptions, callbacks,
managed tree, creation/run/UUID suppression and LoadedThread lifecycle are absent.

Tickets 01–13: read local completed-ticket records and coordinator identity audits,
and verified their result commits are on current history; source/consumers/gates
were reviewed independently of those statuses. Their user-chat terminal state is
coordinator-owned; this chat does not update the coordinator manifests. T14 is
this independent implementation/review/commit. No extra user chats were created.

## Independent review

Both independent agents reviewed the original baseline through the complete
working tree, including intent-to-add exact_json.go. Standards sampled the broad
197-file migration rather than claiming every hunk was exhaustively inspected.
Spec covered normative requirements, final core runtime paths and selected public
acceptance/regression bodies across that full diff; it likewise was not an
exhaustive reading of every hunk.

- Standards: one finding, three polling predicates invoked trace/require from
  testify worker goroutines. All now poll on the owning test goroutine using
  waitTraceWithin and preserve their one-second limits. Re-review: zero outstanding.
- Spec: three findings: missing explicit history cursor end, discarded failed
  history source, and shared mutable Codex Load errors. All fixed and public
  red/green regressions added. Re-review: zero outstanding.

## Final validation

All commands below exited 0 on the final runtime/test source, with mise Go
1.26.9 and the Python 3.13 shim inside the mise shell. Heavy gates ran serially.
Saved logs normalize indentation/trailing whitespace only; diagnostic values and
results are unchanged. Live opt-ins were disabled; the first broad race also had all opt-in variables
unset. Only evidence/report Markdown and saved logs were added after the final
source freeze. No production or test code changed afterward.

| Check | Actual result / saved output |
|---|---|
| `rtk mise exec -- sh -c 'export PATH=/tmp/session-ticket01-bin:$PATH; bash scripts/check-offline.sh'` | PASS: [complete output](../validation/ticket14/offline.log). Boundaries, module verification, six CI tests, inventory 173/11/86/1 plus 24 controls, 417 generated types/783 cases, seven sanitizer tests, recording/transcript hashes, all Go packages, public consumer and four executed offline examples. Claude 10.087s, Codex 34.771s, stories 2.428s. |
| `rtk mise exec -- sh -c 'export GORACE=atexit_sleep_ms=0 GOMAXPROCS=2 CODEX_E2E_STDIO=0 CODEX_E2E_INTERACTIONS=0 CODEX_E2E_WEBSOCKET=0 CLAUDE_E2E=0 SDK_REAL_AGENT_E2E=0; go test -race -p=1 -count=1 -timeout 240s ./...'` | PASS: [complete output](../validation/ticket14/race.log). Claude 14.172s, Codex 55.849s, stories 3.801s; all packages/default live skips. The env-gated mock consumer is checked explicitly below. |
| `rtk mise exec -- sh -c 'export PATH=/tmp/session-ticket01-bin:$PATH; bash /tmp/session-t14-local-checks.sh'` | PASS: [exact command script](../validation/ticket14/local-checks-command.txt), [output](../validation/ticket14/local-checks.log), [snippet extraction script](../validation/ticket14/doc-checks-command.txt). `go vet ./...`, both mock builds, explicit mock-consumer race (3.952s, including 2048-fact slow consumers), two native Linux/amd64 test-binary cross-compiles, both README snippets and Claude configuration snippet compile. No Linux test binary or snippet main was executed. |
| Focused original writer matrix and real pipe barriers, `GORACE=atexit_sleep_ms=0 GOMAXPROCS=2 go test -race ./claude -run '^(TestWriteFailuresAreReturnedDirectly\|TestSessionPermissionQueuedCallerCancelPreservesLiveAuthority\|TestSessionPermissionStartedWriteOutcomeUnknownCannotRetry\|TestSessionConfigurationQueuedCancelAndStartedWrite)$' -count=20` through `rtk mise exec -- sh -c` | PASS: [output](../validation/ticket14/writer-race-20.log), 21.993s. Earlier red profile run: [output](../validation/ticket14/writer-before.log). No later Claude runtime/writer-test changes; final full race also includes the matrix. |
| `rtk git diff --check`; fixed-base diff of fixture/source pins/generated schema/go.mod/go.sum | PASS: no whitespace errors; recording assets, schema/source pins, generated output, module dependencies unchanged by T14. |

Runtime/test/fixture source fingerprint: 217 tracked files, SHA256
`4dcb29b9bc840abd5e696e0ff3d8df559bd827cf39bdc4f3352d84cbdb9b0304`.
Computed from sorted tracked `.go/.py/.sh/.json/.jsonl/.ndjson` files plus
`go.mod`, `go.sum`, `mise.toml`: SHA256 of each UTF-8 path + NUL + binary SHA256
of its contents, concatenated. Saved `.log/.txt` evidence and Markdown do not
enter that fingerprint. Final commit records the same source bytes.

## Remaining native and evidence limits

Claude fixed TypeScript SDK 0.3.258 / CLI 2.1.258 has no proven no-input resume
confirmation route. Explicit Load fails ErrResumeUnconfirmed (or its actual
startup/identity fault), sends no extra prompt, replays nothing and never opens
ordinary input. SDK-US-019/020 successful interleaved replay/post-success portions
are not implemented on that native baseline. This accepted strict-failure
implementation must not be described as satisfying the complete common capability set.
Codex Load requires stable retained main history and quiescent external writers;
non-atomic pagination does not prove active-execution or crash/offline recovery.
The original real recordings remain only their captured scopes: no real child
recording in either SDK, Claude recording is an authentication failure.

No real CLI/Agent/model, new recording, source-pin upgrade, push or release ran.
Final exact pushed-SHA fetched consumers remain unverified because the revision
has not been pushed. Local source public/mock checks are separate. Linux tests
are cross-compiled only on this macOS host; no Linux runtime pass is claimed.
