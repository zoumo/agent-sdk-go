> Historical evidence for the pre-removal baseline. Load and its contracts have been removed; this report is not a current API guide.

# Ticket 13 consumer migration result

Status: complete. Final post-fix offline gate passed.
Fixed base: `3f1776e4da94cc53d48e5029b20bd254dd727152`.
Initial consumer slice: `994e9289a0f544450484dcc193ffbedef100fbb6`.
The contract commit follows that slice on `codex-session-refactor`; see local Git
history and the ticket result SHA. No push or release was performed.

## Completed contract phase

- Recorder, replay, mock/public consumers, examples, Go Example contracts and
  gated e2e callers use Session/Receive/Submit and real native answer handles.
- Codex independent Subscription/Requests runtime, held/pending/dedup delivery
  gates and Client.Load/LoadedThread lifecycle are removed. Native Client/raw
  extensions retain connection ownership fences and native turns. StartResponse
  preserves the actual thread/start result, unknown data and copy isolation.
- Claude public Client/NewClient, managed tree, callback worker pool, parallel
  native FIFO, creation/run/UUID ledgers and independent history stream are removed.
  Private interaction retains process/control/writer and permission registry
  mechanics; Session owns the sole FIFO. No automatic permission deny is restored.
- Native Raw, unknown/null/absent values, original request authority, cancellation,
  single-use answers, NotSent reuse, OutcomeUnknown no-retry, shared ownership,
  drain/Abort, resource limits and historical integrity have public or independent
  mechanical replacements. The [per-function guarantee audit](ticket13-legacy-guarantees.md)
  distinguishes each retained guarantee from superseded convenience behavior.
- Current guides cover breaking input/receive/approval/Load/resource changes.
  Claude ordinary Open ResumeID remains a literal fresh-process no-replay request,
  with requested and observed identity distinct. Explicit Load still fails
  ErrResumeUnconfirmed without extra input, replay or fabricated confirmation.
- Historical protocol evidence, native source pins, generated roots and recording
  assets/hashes/provenance remain unchanged. Explicit test-migrations.json keeps
  retired paths and exact historical IDs traceable to current files without
  transferring old race/live outcomes. The verifier rejects absent/stale targets.

## Validation

All heavy checks ran serially with mise Go 1.26.9,
`GORACE=atexit_sleep_ms=0` and `GOMAXPROCS=2`.

- Root offline gate passed after contraction (Claude 10.488s, Codex 34.213s):
  boundaries, module verification, generation, protocol inventory, sanitizer and
  fixture hash checks, all Go packages, default live skips, local public consumer
  and four runnable offline examples. The final post-shutdown-fix run also passed
  (Claude 10.008s, Codex 34.402s, shared stories 2.176s), retaining all gates.
- Full Codex package race passed (53.494s).
- Claude Session/Load/history/native codec/client protocol/recorded observation
  selections passed under race. Its migrated ControlInteractions suite also passed.
- Lineage, queue, both replay/mock libraries and recorder provenance race passed.
- A subsequent shared-story race found SDK-US-017/Claude draining 31/32 final
  facts; a narrow 20-run race reproduced 13/32. Process exit could seal Session
  admission before buffered stdout was parsed. The lifecycle now waits for reader
  completion within the existing first shutdown phase, using the original overall
  deadline. The story preserves 32 facts and additionally checks exact sequence.
  The fixed narrow race passed 50 iterations (2.892s). Then Claude relevant race
  (12.336s) and the complete shared-story race (4.188s) passed. Both the 20 new and
  12 old stories remain separate. No count, payload size or deadline was reduced.
- ProcessLifecycleSuite race passed (2.567s), including descendant/pipe cleanup.
- Built both mock commands and ran explicit local mock-consumer race (4.034s),
  including original 2048-fact paused consumers and control/answer barriers.
- Both native package test binaries cross-compiled for Linux/amd64. Linux-specific
  permission FIFO tests compile; they were not executed on this macOS host.
- Both README first Go blocks and Claude comparison/managed-consumer fragments
  compiled independently as local source files; no real process was launched.
- The three current Claude Mermaid diagrams now show Session/native delivery and
  original handles; github-light PNG renders passed visual inspection.
- go vet ./... and git diff --check passed. Inventory mutation checks rejected
  missing current targets, unmapped retired paths, stale IDs, duplicate mappings
  and pinned snapshot drift, while accepting the real migration.

## Independent reviews

Two independent review agents reviewed the fixed base through the complete
working tree, explicitly including new native regressions, audit and migration
manifest. Spec found missing historical V2 Load coverage; Standards found Linux
caller-cancel expectations and audit wording. All were corrected. Linux now
asserts caller cancellation is NotSent, leaves the original handle live, allows
an unrelated started write to finish and verifies exactly one later answer;
its native reply echo is consumed before snapshot readback. Historical V2 Load
retains exact history bytes/typed unknowns without granting live membership.

Both axes reviewed the later shutdown fix separately and reported no unresolved
finding. The final committed revision is recorded in the ticket after commit.

## Evidence limits

No real CLI/Agent/model, new recording, upstream update, push or release occurred.
Exact fetched pushed-revision consumers remain unverified because no revision was
pushed; local compile/public/mock checks are separate evidence. Known full-Claude
race 100 ms writer and 1 second writer-prefix timing cases remain T14 concerns;
this report does not claim full-repository race success. T14 overall validation
is separate from completion of this T13 contract phase.
