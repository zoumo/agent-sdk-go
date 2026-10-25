# Bounded Codex root tooling (#9)

Base: `52b329a77333e55063a97eaf1304c82b30a99a59` (#8 / PR #24).
Parent [#6](https://github.com/zoumo/agent-sdk-go/issues/6),
[crosswalk](../../docs/validation/story-coverage.md),
[shared capture proposal](../../docs/capture-plan.md).
This is offline tool support, not recording evidence or live authorization.
Original S1/S2 bytes, manifests, hashes and public replay remain unchanged.

## Implemented public tooling seams

- `recorder.Reserve`: one existing durable aggregate JSONL ledger, shared across
  directories/tickets. Exclusive advisory lock, strict fields/duplicate-key
  checks, 0600 regular file, no symlink components, 64 KiB cap, fsync before
  return. Partial writes, duplicate attempts, unknown scenes/operations, expiry
  and exhausted bounds fail closed. There is no refund/retry/reset API. Startup
  failure still spends its root reservation. The operator must preserve this
  ledger; a local file cannot prevent an operator creating a different ledger,
  rolling back the filesystem or changing the clock. It is not a global billing
  authority or an authorization source.
- `recorder.ValidateProfile`: bounded reviewed nonsecret input, exact CLI pin
  0.162.1 and executable digest, clean build revision, absolute private disjoint
  paths and isolation-review reference. It reads only the supplied CLI/profile
  and path metadata. It never discovers private hooks, config, accounts or old
  history. An isolation-review string is an audit reference, not technical proof
  that policy isolation exists. No opaque authentication transport is implemented.
- `replay.RunRoot`: existing public Session, finite neutral R1/R2/R3 inputs,
  reservation callback before every Submit, separate model/thinking receipts,
  original submitted-turn terminal correlation (including status/error),
  structured output schema on R1's first input, execution-start barrier before
  R3's additional input and explicit turn-ID Interrupt. Receipt acceptance does
  not prove observed settings; Submit during execution remains turn/start,
  never an invented steer. Interrupt acceptance does not prove an interrupted
  terminal. Callers own Close/drain and must capture the native terminal facts.
- `replay.Handler.Exact`: finite ordered complete native Params/Result policy,
  original request handles, exact question IDs/text/options and Blue/sample
  responses. R2 holds the first request, issues one no-op native settings control,
  continues Receive and releases only after a same-root nonterminal native fact;
  absent progress stays untriggered. Command/file permission support is **deny only**; permission grant
  response is empty/turn scoped. Dynamic tools never count as native approval.
  Any unknown/out-of-policy request is rejected and stops the scene. Request
  identities must be supplied by a reviewed fixture policy; this is immediately
  usable for strict offline replay, not a wildcard live-policy generator.
- `replay.QuerySavedRoot`: independent initialized reader; exact supplied fresh
  synthetic root/cwd, state-DB-only list, metadata, summary, full turns, items and
  optional opaque continuation: at most six query RPCs. Unknown data and native
  optional cursors survive. ExplicitEnd refers only to the bounded samples;
  missing cursors, more pages or native errors remain incomplete. Returned
  values are saved content, not a full event log or answer authority. No local
  history scan, root creation, resume or subscription occurs.

Root APIs use an already opened Session/client; they do not launch processes.
A synthetic native Input test retains text-element byte ranges and an image
variant exactly; its deliberately invalid image bytes prove serialization only.
This is separate from R1 structured *output* schema and grants no media capture.
The recording command validates an opt-in profile then calls `Preflight`
**before any model or proxy spawn**. Without a reviewed cost-risk policy it
returns `ErrFinancialGate`; the narrowly accepted R1/R4 policy instead reaches
`ErrIsolationGate`. Both paths remain fail-closed. Direct `--proxy` is
retired. The Python entry now accepts `--runner` and `--profile` and forwards only
an allowlisted minimal environment; the old hook/config discovery and
per-directory authorization markers are removed. Successful preflight cannot
open a live gate. The lower-level `replay.Proxy` remains an explicit subprocess
capture primitive used by synthetic tests, not an independently authorized
recorder entry point.

## Durable aggregate format and invocation order

An operator-reviewed ledger starts with exactly one header (example values are
not permission; startedAt must be frozen for the approved activity window):

```json
{"version":1,"authorization":"review-reference-not-an-opt-in","revision":"52b329a77333e55063a97eaf1304c82b30a99a59","startedAt":"2026-10-25T00:00:00Z","maxRoots":3,"maxTurns":6}
```

Subsequent rows are durable reservations, for example:

```json
{"scene":"r1","operation":"root"}
{"scene":"r1","operation":"turn1"}
{"scene":"r1","operation":"turn2"}
{"scene":"r4","operation":"reader"}
```

Root/reader reservation precedes native startup; a failed/uncertain startup is
never retried. Turn reservation precedes Submit, including an additional input.
R4 cannot create roots/turns; at most one reader. S1/S2 each have one turn maximum;
R1/R2/R3 each two, with turn1 before turn2. At most three generating roots and six
submissions across all scenes, not per directory. Child attempts/depth and SDK
scene retries remain zero. No historical budget is reset or reused. The window
expires after 480 seconds from its header, including prior invocations; it must
not be recreated to obtain a replacement budget. Clock tampering is outside this
local accounting mechanism's guarantees.

The ledger records reserved root, reader and submitted-input attempts separately.
Capture reservations bind the private output target; a proxy checks that binding
and a once-only owned startup marker before native startup. Failures persist a
global stop marker before its journal row, preventing later scenes even when
  another writer holds the journal lock. An exclusive persisted active/closed
  capture lease serializes capture lifetimes; a crash/uncertain failure leaves
  the lease unsettled and refuses restart. Finalization creates a durable barrier
  before exposing closed state, removed only after a successful close. These
  files cannot be deleted to retry. Durability assumes OS locking/fsync and
  stable storage; arbitrary kernel/disk failure, clock rollback or operator
  modification cannot be made a global financial guarantee by local files.
Provider API request/retry counts, usage and USD are **unknown**, never inferred
as zero or one per turn. It does not implement a live provider-meter observer.
`CaptureWithPeer` now verifies the ledger/build revision, reserves before the
supplied opener and each Submit, applies the lesser aggregate/scene deadline,
preserves S1/S2 handlers, dispatches R1–R4, closes/drains the owned client and
writes private metadata including actual root/start/terminal facts and unknown
provider accounting. Initialization failure still consumes its root; cleanup or
metadata failure persists stop. Tests inject only strict synthetic subprocesses.
The native opener/proxy is connected behind `Preflight`, which always fails
closed: accepting cost uncertainty does not bypass the independent isolation
gate. Proxy retains its 2 MiB wire/control and 256 KiB stderr limits and bounded owned cleanup. The
private proxy entry repeats profile/financial checks and requires the bound
reservation. An opaque-auth/policy-isolation adapter and exact approved-workload
ledger binding remain prerequisites, not missing basic scene orchestration.
Activity deadline and cleanup are distinct;
owned cleanup runs after activity stops. The native opener gives its proxy a
12-second SDK shutdown timeout (six-second grace), exceeding Proxy’s bounded
five-second worst-case native teardown so SDK shutdown does not prematurely kill
the proxy before native reaping. Missing transport cleanup still stops the ledger. A strict 120-second *total including teardown* would need a
separately validated earlier activity cutoff; no stronger timing claim is made.

## Smallest #11 decision table

No execution has occurred. The smallest useful initial selection is R1 plus R4:

| Selection | Root / inputs / reader | Activity wall | Wire / stderr | Evidence |
|---|---|---|---|---|
| R1 | 1 / 2 / 0 | 120 s | 2 MiB / 256 KiB | Continuation and native config receipts |
| R4, only new R1 | 0 / 0 / 1; max 6 query RPCs | 30 s | 2 MiB / 256 KiB | Fresh saved metadata/content |
| Minimum total | **1 / 2 / 1**, children/depth 0/0, SDK retries 0 | **150 s**, bounded teardown separately, aggregate activity window 480 s | **4 MiB / 512 KiB** | No approval/child/media claim |
| Optional R3 increment | +1 / +2 / 0 | +120 s | +2 MiB / 256 KiB | Execution-time input / original-turn interrupt or explicit gap |

The user accepted this finite workload and unknown dollar/token/provider-
generation/internal-retry exposure on 2026-10-25. This is not a hard financial
cap or approval to bypass technical gates. These tools
cannot make a truthful strict bill cap promise. The proposed USD 1/scene below
is a proposed decision, not automatic authority or a permanent inherited rule.
Separately, the operator must prove policy/config/hook/integration isolation and
an approved existing-auth transport without reading private/platform credentials.
Approving fee uncertainty does not prove or relax that isolation. R2 safe native
Read approval remains technically unavailable within the current no-shell policy.

## Concrete #11 run proposal and stop conditions

This table is ready for review after the blocking enforcement work, **not ready
for execution**. Model proposed: `gpt-6.1-sol`; CLI 0.162.1. The operator must supply
an approved absolute binary/digest for the actual OS, clean merged SDK/tool SHA
and build digest, new isolated profile/auth transport, exact input hashes and
one preserved aggregate ledger reconciled with all historical spent attempts.
Historical macOS CLI digests do not approve another binary.

| Scene | Root / submitted-input attempts | Scene wall | Wire / stderr | Task and stop |
|---|---|---|---|---|
| R1 | 1 / 2 | 120 s activity including startup; bounded owned teardown | 2 MiB / 256 KiB | Exact Ready/Done prompts in RunRoot; first has the committed JSON word schema. One SetModel and SetThinking high between turns. Persist only this new synthetic root for R4. Rejection stops; no replacement input. |
| R2 | 1 / 2, currently policy gap | 120 s | 2 MiB / 256 KiB | Exact read/write task then two questions. No native read approval can currently be safely allowed without shell; file approval lacks patch/path. Denial-only replay support is not successful read/write evidence. Do not launch until a separately reviewed safe native policy exists. Never substitute dynamic tools. |
| R3 | 1 / 2 | 120 s | 2 MiB / 256 KiB | List 1–10; first root turn/started triggers exact additional sample input, then one original-turn Interrupt. Early completion means untriggered, no retry. |
| R4 | 0 / 0, one reader process | 30 s | 2 MiB query / 256 KiB | Only persisted R1 root, at most six query RPCs, no resume/Subscribe. Unsupported persistence/query remains missing. |
| Total | 3 roots / 6 inputs / 1 reader; children 0/depth 0 | 390 s planned, 480 s aggregate hard window | 8 MiB / 1 MiB | SDK/scene retries 0. Stop every later scene on unexpected policy, unknown accounting, failure, cap or incomplete owned cleanup. |

Requested provider ceilings remain those of the shared proposal: API generations
including native retries at most 6/6/4 (16 total), native/provider retries 0;
per generating scene 20,000 input and 1,500 output tokens, USD 1; Codex total
60,000 input / 4,500 output, USD 3 including failed attempts and in-flight charges.
These are historical proposed limits, superseded for the approved minimal
R1/R4 workload by explicit risk acceptance; **none of the monetary/token/
generation/retry limits is independently enforceable by these tools**. Codex has
no verified dollar hard stop. A wall kill, wire cap, post-run token report, effort level or
submitted-turn counter cannot prevent the next provider request exceeding a
financial cap. Unknown accounting must remain unknown, not be reported as zero.
Do not reset a ledger or obtain extra attempts through a replacement profile. The operator
may record the approved decision with `costPolicy` =
`accept-unknown-provider-cost-v1` and a nonsecret `costReview` reference.
`Preflight` accepts that distinction only for R1/R4 with model `gpt-6.1-sol`
and R1 high effort; it still always returns `ErrIsolationGate`. Neither field
is proof of authority or authentication. Actual launch remains blocked until
an independently reviewed opaque-auth/policy-isolation adapter is available
and a reconciled durable ledger enforces the exact 1-root/2-input/1-reader,
150-second activity workload. No new account, login, credential inspection,
copying or platform-auth access is enabled by this change.

Media/image acceptance is not added. Native Read approval, execution-time
acceptance, settings observation, terminal interrupt, persisted query support and
successful native approval/question emission must be actually observed under the
selected protocol or remain unsupported/untriggered/missing. An exact recorded
identity policy cannot be known before a new live request; a separately reviewed
identity-binding policy would be required without relaxing full request review.

Raw captures/query/storage stay outside Git, 0700/0600. Publication still needs
separate approval, per-version sanitizer/schema additions for any new methods,
unknown quarantine, secret/path/header/email canaries, typed ID mapping,
source/build/CLI/input identities, exact replay/EOF/cleanup and review. The old
sanitizer's S1/S2 allowlist is not silently widened by these tool APIs. No new
real capture, private session read, credential access or publication took place.
