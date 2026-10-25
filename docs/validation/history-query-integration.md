# Phase two: independent main-session history integration

Contract: [independent history](../../harness/contracts/history-query.md),
[US-014](../../harness/contracts/stories/US-014.md),
[interfaces](../../harness/modules/native-session/interfaces.md).
This report records implementation evidence, not an expanded native contract.

## Exact inputs and integration

Repository: `https://github.com/zoumo/agent-sdk-go` (origin verified).
Original main and both branches' merge-base:
`bd557d91b98ab4db5e2b968b125fba6d583f791b`.
The old `/workspace/codex-sdk-go` checkout was inspected read-only and not reused.
No applicable repository/environment `.agents/skills` was present.

| Ticket | Published input |
|---|---|
| 01 Codex | `a98b6d0d90c1592512ba9fd5e88b7a6907055056` |
| 02 Claude messages | `bfce634d75044b07c0cedce030d2862ab25f292d` |
| 03 Claude metadata (contains 02) | `4fd9b03352d56f960cee3e2e1cad33d602bed9b1`, tree `8f4cdb1bff9250ff550dbbc284dde182f7175748` |

Integrated base: `7c3c9758ac6bef7f8e1f2ce8fceef92f9ced1d4d`, tree
`7b0027449c1816c979afad188a20ceec6d69ef0b`.
Merge parents, in order: 03 then 01 from the table above.
Conflicts in document indexes, SDK-US-019 wording, the private acceptance file
and offline-example list retained both native contracts/tests and seven examples.
No production API conflict required alteration. Prior evidence was read from the
01 tracker and [02](claude-session-messages.md)/[03](claude-session-metadata.md)
reports, together with the parent's exact remote consumer verification. The 02/03
local tracker files were not included in their published trees.

## Public acceptance and native differences

`CommonStoriesSuite.TestIndependentHistoryBrowser` enters both native public
adapters for the same US-014/SDK-US-019 target. No generic production Client,
shared Turn, second history stream or test framework was added.

| Goal | Codex | Claude |
|---|---|---|
| No Session | initialized Client: ListThreads → ReadThread(includeTurns=false) → ListThreadTurns → ListThreadItems | package ListSessions → GetSessionInfo → GetSessionMessages; temporary explicit ConfigDir, unavailable runtime PATH |
| Metadata/content/log | preview, summary without full items, separate saved items | sampled preview may describe pre-compaction prompt; selected chain keeps summary and parallel tools; raw source is not wire replay |
| Pages/order | opaque next/backwards cursors, native defaults/order, null end, anchor distinctions | visible-message offset/limit after selection/recovery, explicit empty slice, no cursor/total or I/O pagination |
| Unknown/ownership | native unknown variants/readState, null/omitted, detached results | exact JSON including unknown blocks/null/large integers, detached values, unchanged source hashes/modes |
| Errors/coverage | native RPC/transport/cancellation and unsupported backend preserved | typed missing/corrupt/unsupported/resource/changed errors; metadata partial+error differs from failed content with no messages |
| Coexistence | stdio/ws/wss query tests retain typed live notifications and strict request trace; no Receive projection or membership/answer creation | original live approvals remain authoritative, list/info/messages add no peer writes; live delivery continues independently |

The new combined Claude fixture pages six selected messages across compaction and
parallel sibling recovery, excluding a side branch. It compares literal expected
IDs `summary,a1,a2,r2,r1,end`, not raw JSONL line positions. Metadata still reports
the earlier prompt, demonstrating that preview is not the selected content.
Existing focused native cases retain filters, cursor edges, failures, cancellation,
limits and source mutation checks; nothing was deferred from preceding tickets.

TDD: the common entry first failed compilation for its missing Claude adapter.
The first combined fixture then failed because a hidden side terminal displaced
the intended main terminal; the fixed official algorithm confirmed that behavior.
The fixture now attaches the side branch to the summary, preserving the original
six-message/order assertions. The public combined case passed. The Claude example
output test failed until the example used the discovered identity and read
one-message slices through an empty end. Its final output contract passed.
No assertion or live opt-in gate was weakened.

Fixed-source details remain in the [Codex guide](../../codex/docs/history-query.md)
and Claude [message](../../claude/docs/session-messages.md)/[metadata](../../claude/docs/session-metadata.md)
guides. Codex bindings remain 0.162.1 / `092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`;
the separate `970111f6…` inspection is not the same compatibility proof.
Claude Python 0.2.174 / `0e89b298e771259378a6cd3734e00d323dff5703`
sessions.py was fetched again for selection verification; SHA-256 matched
`5faa6011b0559bb2ccbe0e54ec9dea029418001da04013a67c6d107ebd192419`.
The preverified npm 0.3.310 integrity remains recorded in those input reports;
this integration does not claim another npm execution.

## Validation environment and commands

Linux/amd64; mise 2026.10.9 managing Go **1.26.9**, GOTOOLCHAIN=local,
GOMAXPROCS=2, GORACE=atexit_sleep_ms=0. Tools/cache are isolated under
`/tmp/ticket04-tools`. Python here is 3.12.14; CI pins 3.13.13.
Every real-agent gate is explicitly zero: CODEX_E2E_STDIO,
CODEX_E2E_INTERACTIONS, CODEX_E2E_WEBSOCKET, CLAUDE_E2E, SDK_REAL_AGENT_E2E.

```sh
mise exec -- go test -v ./tests/stories -run TestCommonStories/TestIndependentHistoryBrowser -count=1
mise exec -- go test -v ./claude/examples/session-metadata-offline -count=1
mise exec -- bash scripts/check-offline.sh
mise exec -- go test -race -p=1 -v -count=1 -timeout=300s ./...
mise exec -- go vet ./...
git diff --check
bash -n scripts/check-offline.sh scripts/check-consumers.sh
# After publishing the exact implementation/evidence revision:
mise exec -- bash scripts/check-consumers.sh FULL_PUSHED_SHA
mise exec -- bash claude/scripts/check-managed-consumer.sh FULL_PUSHED_SHA
```

Final full-check, independent-review and publication outcomes are recorded below
once those operations complete. Local logs are retained under the ignored
`.scratch/history-query-phase2/validation/` directory.

## Subsequent real-agent test entry points

The parent must fetch the final published SHA, verify its tree, use the pinned Go
compiler, and keep offline results separate from any newly authorized real run.
Codex uses `NewStdio` with an explicit app-server executable, or `NewRemote` with
an existing server, then the four Client methods shown in its guide. No Open is
needed for history. Carry filters/direction and opaque next/backwards cursors;
use ReadThread IncludeTurns=false for metadata, then turns/items as needed.
Local connection creation can launch app-server, native list may repair metadata,
and remote paths/storage belong to the server. Preserve native unsupported
errors (including legacy/DB limits); do not resume as a query fallback.

Claude uses only the three package functions with an explicit absolute ConfigDir
and optional absolute ProjectDir. Inspect error and Coverage before displaying
partial metadata; check Info for nil. Query messages with a bounded Limit and
caller-chosen Offset; each call may scan the entire bounded main source.
The [offline metadata example](../../claude/examples/session-metadata-offline/main.go)
shows list/info/content slices using the discovered identity. Its fixture is
synthetic and immutable; real pages need not share a snapshot.

A separately authorized live coexistence run may Open a Session explicitly and
observe normal Receive/approval handles while calling these independent methods.
The query itself must add no execution, consumer, membership, answer rights or
history events. Saved messages cannot answer approvals. This ticket ran no real
Agent/model, accessed no private transcripts or Mac, and made no new recording.

Main-session history does **not** certify child Agent history or independent
subscriptions. Parent/child flow isolation is separate research/design; no live
child routing was changed. Other remaining limits: Claude immediate local
main-source layout only, no worktree expansion/SessionStore/cloud/system inclusion;
metadata sampling cannot validate an unread middle; neither backend promises an
atomic snapshot or complete original event log. Synthetic/offline success is not
successful real model/child interoperability evidence.

## Review correction: exact tail line boundary

Initial independent review of `17fefd0e6ec2ad87ce80034ccc6d7dd8b8ce7e63` found
Standards 0 and Spec 1 P2. A separated metadata tail window unconditionally dropped
its first line, even when starting exactly at a complete JSONL record. The public
regression reproduced Summary="hello" instead of the latest complete custom title.
This inherited issue was fixed during integration: a preceding one-byte probe
lets the existing fragment trimming distinguish a complete first line. The probe
is counted before reads in MaxBytes/Bytes and is included in final range
verification. Raw Tail remains the original window; no tail record becomes head
provenance. The public regression verifies info/list title selection, unchanged
raw Tail, head FirstPrompt, exact bytes and rejection at a one-byte-short budget.
The existing bounded-middle bytes assertion changes from 512 to 513 to reflect
that newly documented, checked probe; its corrupt-middle/unknown/coverage
assertions remain unchanged. Other partial-line and nested-object provenance
regressions also pass. Focused race/source-integrity checks pass. Full validation
is repeated after this production correction; the earlier all-pass run alone is
not counted as the final corrected-source check.

## Independent review

Review fixed point resolves to `bd557d91b98ab4db5e2b968b125fba6d583f791b`;
`git diff bd557d91b98ab4db5e2b968b125fba6d583f791b...17fefd0` was nonempty.
Commits reviewed: a98b6d0 (01), bfce634 (02), 7f654d0/f222ab3/4fd9b03 (03),
7c3c975 (merge), 17fefd0 (04 acceptance), followed by corrective f4056d2.
Independent Standards and Spec agents re-reviewed `17fefd0...f4056d2`.

### Standards

0 substantive documented-standard violations and 0 smells needing correction.
Native package/dependency boundaries, CommonStoriesSuite placement, public
example output, harness ownership and fixed real capture bytes are preserved.
The correction retains bounded range reads, checks the probe budget before I/O,
and separates raw Tail/head provenance. No remaining Standards finding.

### Spec

The original P2 is resolved. Independent public reproduction now returns the tail
title and two Records with unchanged raw Tail. The probe participates in budget
and verification without head provenance. Exact-line, budget, partial-line,
nested-provenance and priority tests pass. No additional missing requirement,
scope expansion or Spec deviation found. No remaining Spec finding.

Review totals: Standards **0**, Spec **0** outstanding after the correction.

## Retained full-check failure and baseline reproduction

The first full offline and full race runs on 17fefd0 passed. After the tail fix,
a full offline run on f4056d2 failed unchanged Codex
`TestConcurrentAnswerAndNativeResolutionHaveOneOutcome`, both direct and ws:
expected not-sent/zero responses, observed outcome-unknown/one response. This is
not labeled a passing run. All other packages and the preceding generation,
inventory, dependency, sanitization/hash/transcript gates passed in that run;
the script stopped before its subsequent consumer/example loop.

An isolated `git archive bd557d91b98ab4db5e2b968b125fba6d583f791b` extracted to
`/tmp/ticket04-baseline` reproduced the same failure with:

```sh
cd /tmp/ticket04-baseline
mise exec -- go test ./codex \
  -run '^TestServerRequestLifecycleSuite$/^TestConcurrentAnswerAndNativeResolutionHaveOneOutcome$' \
  -count=100
```

The same focused count=100 also failed on the corrected implementation. A first
attempt accidentally ran that focused check in the integration checkout rather
than the extracted base; only the subsequent command in the explicit baseline
cwd is counted as independent baseline evidence. Its log is retained separately.
No approval implementation, synchronization, deadline or expected result was
changed. The intermittent preexisting approval race remains a known limitation.
Full race on f4056d2 passed all packages with no DATA RACE report; vet passed.
The complete offline script is repeated once on the same source after baseline
diagnosis, preserving the failed run rather than hiding it.

The baseline CI run [40162360860](https://github.com/zoumo/agent-sdk-go/actions/runs/40162360860)
was verified by the parent as successful at 2026-10-25 04:39:32 UTC: Ubuntu/macOS
Offline and External consumer **4/4**, real E2E explicitly skipped. This is a
successful baseline CI fact, not validation of these new commits. The workflow
listens only to main, PRs and manual dispatch; publication of this work branch
alone does not produce a CI pass. No new CI execution or macOS test is claimed.

## Final local results on corrected source

Implementation revision: `f4056d20bbf4c87c34e63244588426757b01b7cb`, tree
`e24bcc9cc1eb73f0f1787f12f4ffceb2c256df7b`. The next commit 09ec013 only
recorded evidence; the compatibility follow-up below changes source again.

| Gate | Result |
|---|---|
| Public combined browser, exact-tail regression, metadata/parallel/compaction/coexistence | Passed, including focused race/integrity checks |
| Full `scripts/check-offline.sh` | **Failed** twice after correction solely at the independently reproduced baseline Codex approval competition; no skip or assertion relaxation |
| Offline gates before that test | Passed: module/dependency boundaries, protocol inventory, 417 types/783 generated cases, six CI-gate tests, seven sanitization tests, capture hashes and Claude transcript checks |
| Full `go test -race -p=1 -v -count=1 -timeout=300s ./...` | Passed all packages on corrected source, no DATA RACE report |
| `go vet ./...` | Passed on corrected source |
| Checkout public consumer and all seven runnable offline examples | Passed separately after the failing full script stopped before that loop |
| Committed whitespace/gofmt, shell syntax, harness OKF/internal links, touched report/guide links | Passed |
| Real-sanitized capture diff against original base | Empty; capture bytes and provenance unchanged |
| Standards / Spec review after correction | 0 / 0 outstanding |
| Real Agent/model, macOS, current-revision CI | Not executed; explicitly not inferred from local results |

The single same-source offline retry again failed only the ws form of the same
baseline approval assertion. Both failed full runs and independent baseline
failure are retained. The final state is **not an all-green offline gate** despite
full race/vet and new history-query acceptance passing. Fixing that independent
approval competition remains separate work; no failure was suppressed.
The checkout mock-consumer package's ordinary skip asks for built mock CLIs;
the fetched-module gate builds and runs them, and is a distinct required check.

Log SHA-256 values (local ignored validation directory):

- corrected full race: `5cc47766a38949ab9f9bb05c4f59e7dfe8e1e052e12c9fbbbdc46dfca741d759`
- corrected vet (empty successful output): `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`
- first corrected offline failure: `cdc08ff531c4c90dcf0ffa38780f8fad0c0a8dddb03563ddb35f4154dc108d55`
- independent exact-base focused failure: `b193fc586ac790db09d358e18c6804e9c34a91c5a14fb4bf073f91c4df634d3b`

Initial direct invocation of the CI-only whitespace script lacked
GITHUB_EVENT_PATH and failed before checking. It was rerun with a local push-event
fixture identifying the original base, and passed the full committed range;
this was a local check, not a GitHub Actions run.

## Publication receipt location

Publication is authorized only to
[`phase2/history-query-integration`](https://github.com/zoumo/agent-sdk-go/tree/phase2/history-query-integration),
without force, PR or main merge. The exact immutable final commit/tree and remote
verification, followed by both precise-SHA fetched-module consumer outcomes, are
recorded in ticket04 Comments and the final handoff after this report commit is
created. They must be read as separate evidence, not inferred from a local
`replace`, checkout build or missing branch CI. Consumer scripts fetch the exact
VCS hash into fresh modules/caches without replace/go.work, build both synthetic
mock CLIs, exercise their public APIs, and run the copied native history examples.

## Compatibility follow-up: non-message request records

Follow-up base: `09ec0138c2ef2a14182ee601b2d975a4577d3491`, after the first
published integration. The parent reported a separately authorized live run:
Codex 0.164.0 completed a model task and a new Client's independent list/read/
turn/item path, including two item pages at limit=1; a single turn does not prove
multi-page turn traversal. Claude Code 2.1.310 completed via Magpie with exit 0;
list/info coverage was complete, but message reading rejected api-request-shape.
Parent diagnosis identified four non-message record types lacking uuid/parentUuid:
api-request-shape, api-request-blob, api-request and cost-state. These live results
are parent-reported evidence, not execution by this ticket context, and do not
certify the corrected revision until the parent rechecks unchanged source files.

No real transcript, request body, credential, cloud test file or captured bytes
were copied into this checkout. Regression fixtures are handwritten minimal JSON
objects carrying only type and clearly synthetic unknown data, between artificial
user/assistant records in a temporary ConfigDir. They first reproduced the exact
unsupported error for all four kinds through public GetSessionMessages.

Fixed official evidence was inspected again:

- [Python sessions.py](https://github.com/anthropics/claude-agent-sdk-python/blob/0e89b298e771259378a6cd3734e00d323dff5703/src/claude_agent_sdk/_internal/sessions.py),
  SDK 0.2.174 (bundled CLI 2.1.310): `_parse_transcript_entries` at lines 897–928
  accepts only the five chain-entry kinds and requires a string uuid. Other
  records do not enter `_build_conversation_chain` or content pagination.
- Official npm [sdk.mjs 0.3.310](https://unpkg.com/@anthropic-ai/claude-agent-sdk@0.3.310/sdk.mjs):
  public exported getSessionMessages dispatches through EKt → sF → uxe → oF.
  The uxe parser includes only user/assistant/progress/system/attachment records
  with a string uuid. Its ancillary delivery bookkeeping does not promote these
  four request/cost record kinds into messages. This is a static code finding;
  the request-kind strings themselves are not present in the SDK file.
- The fixed npm tarball was downloaded only for static inspection; its SHA-512
  again matched `fo+sr+9Ks73P001f6XKRQlyzfIBAJMgXArLUkH5Tu98NikRDZeGtFQVj23d5/hj2ijpFgzffrr4fHywY9qUkoQ==`.
  sdk.mjs SHA-256: `4ee428f7e39da03262a8a3fc547d3ec60801d3af17b2d40e553c0086ffdac1f8`;
  Python source SHA-256 remains `5faa6011b0559bb2ccbe0e54ec9dea029418001da04013a67c6d107ebd192419`.

The minimal correction classifies only those four confirmed kinds as query-only
bookkeeping after the existing JSON and optional session identity validation.
It does not expand transcriptMetadata or the older replay frozen-tail allowlist.
Unknown entry kinds still return unsupported; no fallback ignores arbitrary
unknown records. Source byte/line/line-size limits and complete-source integrity
verification are unchanged, and chain-entry limits still count the graph.

Public TDD acceptance now reads both saved messages and correct slices/end with
all four kinds present. Metadata Raw Records still preserve their exact synthetic
unknown/null/large-number content. Guard cases cover foreign camel/snake session
identity, invalid typed/duplicate identity, invalid Unicode, an exact full-source
byte budget and one-byte-short failure, line/index/oversized-metadata-line limits,
and an unrecognized api-request-future kind. Errors return no partial messages.
Existing source mutation and legacy replay strictness checks are retained.

Focused command:

```sh
mise exec -- go test -v ./tests/stories \
  -run 'TestCommonStories/(TestClaudeNonMessageRequestMetadata|TestClaudeRequestMetadataGuards)' -count=1
mise exec -- go test -race ./claude ./tests/stories \
  -run 'TestQuerySourceIntegrity|TestHistory|TestCommonStories/(TestClaude|TestIndependentHistoryBrowser)' -count=1
```

Both passed after the correction. Full-check, review and final publication receipts
for this follow-up are recorded below and in ticket04 Comments/final handoff.
The parent will re-read its unchanged real source at the final new SHA without
another model call. No successful corrected-SHA real query is claimed yet here.


### Follow-up checks and review

Corrected implementation: `86bd84dd482b988bf787100cd8dade8eea98a2f1`, tree
`0f6b5c8943f34a03e52913758b9447ecaabaefef`, based on 09ec0138.
The code-review fixed point resolves and the diff is nonempty:
`git diff 09ec0138c2ef2a14182ee601b2d975a4577d3491...86bd84dd482b988bf787100cd8dade8eea98a2f1`.
The sole implementation commit is 86bd84d. Both independent review axes read the
follow-up specification and fixed official evidence.

#### Standards

0 documented-standard violations and 0 smells needing correction. The exact
query-only classification preserves the shared/legacy/live boundaries. Public
CommonStoriesSuite tests, private acceptance assertions, artificial fixtures and
harness/external evidence locations conform to the repository standards.

#### Spec

0 outstanding findings. The four types are excluded only after existing strict
JSON and optional identity validation; byte/line/line-size/index and final source
integrity bounds remain. Unknown types are still unsupported, with no changes to
legacy replay/frozen-tail or live child routing. Independent public regressions
passed; neither final publication nor real-source revalidation was preclaimed.

| Follow-up gate | Result |
|---|---|
| Public four-type regression, paging/end, metadata Raw, identity/JSON/resource/unknown guards | Passed |
| Focused race, source-integrity, legacy history and Claude/common query acceptance | Passed |
| Full offline script on 86bd84d | Failed solely at the known Codex concurrent approval assertion (direct/ws/wss), independently reproduced on exact original base earlier in this report |
| Full race on 86bd84d (`-race -p=1 -v -count=1 -timeout=300s ./...`) | Passed every package; no DATA RACE report |
| Full vet | Passed |
| Generated types/cases, protocol inventory, module/dependency boundaries, CI-gate tests, sanitizer/hash/transcript checks | Passed before full offline test failure |
| Checkout public consumer and seven runnable offline examples | Passed separately after the script stopped before that loop |
| Committed whitespace/gofmt, shell syntax, OKF/internal and touched documentation links | Passed |
| Real-sanitized data and old history.go/history_snapshot.go diff against 09ec0138 | Empty |
| Standards / Spec | 0 / 0 remaining |
| Corrected-SHA original real source query | Pending parent revalidation; no new model needed |

The failed offline run is retained and is not all-green. No Codex approval code
or test was changed, skipped or weakened. There was no repeat-until-green loop.
No current-branch CI or macOS evidence is inferred. The precise final published
follow-up SHA/tree, remote agreement and both new fetched consumers are recorded
in ticket04 Comments/final handoff after this evidence commit is created.

Follow-up log SHA-256 values: offline failure
`6f04b45c1b1b675fda8c65f3b42c8f4c4b60488ba286e491ecc29bbd30dc1013`,
public guards `2a02cf55ac2d533b8b793511044f70a564ee4c8cd01e67741d22609a3b776730`,
focused race `01728fa2d427a138283f17f1ac556ed5ca9e0ec88292d3a41aba8081ab886bda`.
Full race and fetched consumer hashes join the local ignored receipt. No real
capture or secret-bearing artifact is part of these logs or commits.
