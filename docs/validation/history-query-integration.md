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
