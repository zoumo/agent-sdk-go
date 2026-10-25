# Issue #20: Claude child tooling and blocked #22 handoff

Base: `bf0b982c3f6883ec4c6fbd12076005861d84147d` (#10 / PR #27 and
#15 / PR #26 included). Scope: [#20](https://github.com/zoumo/agent-sdk-go/issues/20),
parent [#7](https://github.com/zoumo/agent-sdk-go/issues/7). Evidence follows the
[child contract](../../harness/contracts/child-detail-delivery.md),
[saved child contract](../../harness/contracts/claude-subagent-history.md) and
[Stories](../../harness/contracts/stories/child-capabilities.md).
No live calls, publication or new budget are granted. No native CLI probe, model,
child Agent, private history or credential inspection was performed.

## Implemented offline boundaries

- `Preflight` reports `ErrChildEnforcement` independently from managed-policy and
  financial/token/generation/retry blockers. Manual launch and proxy remain closed
  before version/help execution. Environment claims, prompts, concurrency reports
  and post-creation events cannot unlock them.
- The existing durable ledger fixes explicit `ChildLimits` before the first root
  turn: total activity attempts, per-root attempts, depth and concurrency. There
  are no default/approved child allowances. Unique intent is charged before
  dispatch with active reserved parentage; failure, completion, reopen and a new
  root do not refund/reset the total. Unknown/finished parents and ID reuse refuse.
  This proves accounting, **not native pre-creation interception**. The live
  driver never enables this policy. Old spent ledgers cannot be reset; a new path
  confers no authority.
- Root `Drive` rejects task start/progress/update/terminal/background facts and
  nonempty agent/parent-tool attribution, including facts after root result.
  Permissions with child/uncertain attribution cannot spend root Read/Write/
  question allowances. Denials retain original handles; errors cause owned Abort
  and a failed durable root outcome without retry. Root success cannot complete
  pending/failed children. No child/background driver is enabled.
- `Workspace.CaptureStorage` selects only one parent UUID in the newly owned
  project's explicit ConfigDir, retaining its creation identity. It copies
  separate main JSONL, direct/workflow/nested child JSONL and meta sidecars with
  SHA256 and explicit total bytes/files/entries/depth limits. Pinned directories,
  regular-file identity and nonblocking/no-follow opens reject symlinks, special
  files and observed replacement. Unsupported platforms refuse. Unknown layout
  stays quarantined in private storage; errors return no partial snapshot.
  Unknown JSON fields remain raw **unreviewed** bytes. This is not a history
  parser or sanitizer; paths cannot establish parentage, and missing sidecars do
  not invent it. No HOME/default-history discovery occurs.
- Snapshots are detached private memory copies; original files remain in the
  fresh private workspace. Caller retention is separate; no public file or source
  rewrite occurs. `Atomic=false`, `Publishable=false`: stop owned writers first.
  No cross-file snapshot, wire-log reconstruction or termination of unowned
  service work is promised. Existing identity-bound cleanup stays unchanged.
- Strict replay preserves child aliases, parent/task facts, native unknown fields
  and late completion byte-for-byte. A synthetic test checks original permission
  IDs rather than invented child answer authority. The publisher explicitly
  quarantines child facts: its root key set is not a reviewed child/storage
  profile. Child per-version allowlists and cross-file typed ID maps still need
  approved actual source evidence; no fabricated recording is published.

## Technical blockers before any model call

The recorder exposes only root S1/S2/R1/R2/R3 and pins CLI 2.1.258. Session task/tool
aliases are observed after creation. A native permission answer handle is not a
callback guaranteed before every recursive creation or provider retry. No public
native API here establishes cumulative pre-creation interception, recursive
workdir/tool/network confinement, child mandatory-hook/auth isolation or provider
billing/token enforcement. Requested `--tools`, ask policy, safe-mode and observed
hook events cannot prove those properties. 2.1.310 is not silently substituted.

Thus #20's actual-runtime recursive-enforcement acceptance is **blocked**, and
#22/P2.8 remains blocked. Native interception of every failed/late/nested/retried
creation before execution plus independent recursive policy/auth/financial proof
are prerequisites for a child driver and live background/storage capture.
Approval cannot supply missing technical proof. Mandatory controls cannot be
disabled; credentials cannot be inspected, copied or repaired.

## Parent review proposal: neither launch-ready nor approved

These planning ceilings are separate from historical root allowances. Reconcile
all previously spent attempts before any live request. None is launch authority.

| Proposed scene | Roots / turns | Child attempts / depth / concurrent | Wall including init/cleanup | Background deadline | Wire / saved / stderr | SDK / scene retries |
|---|---:|---:|---:|---:|---|---:|
| Direct root to A, ALPHA | 1 / 1 | 1 / 1 / 1 | 120 s (30 s / 3 s) | 0 | 2 MiB / 2 MiB / 256 KiB | 0 / 0 |
| Root to A/B, A to C, ALPHA/BRAVO/CHARLIE | 1 / 1 | 3 / 2 / 3 | 120 s (30 s / 3 s) | 0 | 2 MiB / 2 MiB / 256 KiB | 0 / 0 |
| Parent ROOT, background A ALPHA | 1 / 1 | 1 / 1 / 1 | 120 s (30 s / 3 s) | 15 s inside scene | 2 MiB / 2 MiB / 256 KiB | 0 / 0 |
| Activity ceiling | 3 / 3 | **5 total**, depth 2 | 360 s | included | 6 MiB / 6 MiB / 768 KiB | 0 / 0 |

Provider/model, absolute CLI path/digest, tool build/revision, exact neutral prompt
bytes/hashes, input hashes, opaque existing operator auth transport and private
retention destination must be fixed before asking the parent for approval.
Provider/native retries/generations, strict input/output token ceilings and an
independently enforceable dollar stop are **unproven**; no token/dollar number is
approved or encoded here. Historical `--max-budget-usd 1` is not independent
billing proof. Root turns do not equal provider requests. Missing outcomes never
authorize another turn, resume or retry. Auth/policy/budget/technical failure
stops. Root result, parent tool result, actual child terminal and background work
must be reported separately.

## Verification scope

TDD used public recorder/ledger/workspace/replay seams and re-executed synthetic
peers. Tests first failed on missing guards/accounting/storage and on unreviewed
child publication being accepted, then passed after implementation. No synthetic
pass is a successful real child capture. Original real fixtures remain unchanged.

Official `go.dev/dl` Go 1.26.9 archive SHA256 verified:
`42d158b4d8f7b61ac0a830567c940a86098fb7aac52e467a5ebec03ef5cc2f8d`.
The supplied `/usr/bin/go` is not a Go compiler; old `/tmp/ticket04-tools` paths are
absent. Mise fetches returned HTTP 403 (release API/version) and HTTP 404 (latest
binary), so local tests use the exact compiler directly with caches in `/tmp`.
CI retains its unchanged mise-managed six-job matrix. Exact commits, review,
local commands and CI evidence belong to the ticket/PR.
