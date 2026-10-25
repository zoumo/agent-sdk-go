# Phase 1 root capture and replay gate proposal

For [#8](https://github.com/zoumo/agent-sdk-go/issues/8), parent
[#6](https://github.com/zoumo/agent-sdk-go/issues/6). **Proposal only: zero new model
calls are authorized or executed by this document.** It is input to #9/#10 tooling
and #11/#12 capture approvals, not executable permission. The
[per-Story matrix](validation/story-coverage.md) defines current gaps; the
[recording inventory](recordings.md) preserves originals. Root bundles reuse one
capture for multiple goals rather than one paid run per Story.

## Fixed neutral root bundles

Use a new empty isolated project/config/storage, never this checkout or old
conversations. Each provider gets its own roots and ledger entries. Exact prompt,
input bytes, native settings and optional extensions must be frozen and hashed in
the later reviewed run table before launch. The texts below are the proposed
baseline; native tool schemas and permission policy must be reviewed separately.
No arbitrary prompt, shell, network integration, child or fallback is permitted.

| Bundle | Controlled task and native operations | Expected replay outcome and Story coverage |
|---|---|---|
| R1 continuation/config | Root with two ordinary submitted inputs: `Reply with the word Ready.` then `Reply with the word Done.` Creation specifies selected model/native thinking; between turns issue one SetModel and one SetThinking, preserving native receipts/rejections and any later observation. Store this synthetic root for R4. Text is the baseline; no media by default. | SDK-US-001/002/004/005/012/013 and CODEX-US-002, CLAUDE-US-001/003/004. Verify Submit versus result/idle, continued Receive, no rebuild. Native rejection or absent observed config remains explicit; exact generated wording/delta count is not required. Full image/document acceptance remains missing unless a separately bounded extension is approved. |
| R2 original interaction | Two inputs in one new root. First: `Read numbers.txt, then write summary.txt with total=18.` Files fixed to `7\n11\n` and expected `total=18\n`; exact Read approval is allowed once and exact Write request denied once (no output success claimed). Second: `Ask two questions once: choose Blue or Green; enter a short label.` Answer Blue and `sample` once through the original native request. Hold one request while continuing Receive and one non-generating control. | SDK-US-004/009/010/011, CODEX-US-001, CLAUDE-US-002. Native command/file approval must actually be emitted; Codex dynamic-tool reply never substitutes. Question identity/full text and original answer preserved. Unknown/out-of-policy request denied and run stopped. Missing approval/question is **untriggered**, not success; no extra prompt to force it. Allow/deny policy does not authorize native shell. Exact safe native tool path remains #9 feasibility gate. |
| R3 execution-time input/interrupt | First: `Produce a short numbered list from one to ten.` On the first observed native execution-progress barrier, submit `Also include the word sample.` once; after native execution-start evidence issue one Interrupt and retain subsequent terminal/idle or error. Close owned resources. | SDK-US-003/016, CODEX-US-002/004, CLAUDE-US-001. Capture native acceptance/rejection/write-only state; start is not steer. If execution finishes before the barrier/interrupt, label untriggered and stop; no retry or invented interrupted terminal. Deterministic ordering remains F. |
| R4 saved root query (zero model) | Reuse persisted R1 only. Codex independent initialized reader: list exact synthetic scope → read metadata → one turns page (summary) → one full/items page → one continuation/explicit end when present, at most six query RPCs. Claude: explicit isolated ConfigDir/project → list → info → bounded message slices + end, at most six calls. At most one extra Codex app-server reader process, no new root, resume or Subscribe. | SDK-US-019 / scenario US-014 and CODEX-US-003, CLAUDE-US-005. Save query request/response fixtures and sanitized storage JSONL/metadata manifest separately from wire event captures. Retain unknown/scope/error/page semantics; no answer authority, running claim, invented cursor or complete raw log. If CLI cannot persist/read within gates, record missing/unsupported and stop. |
| F synthetic companions (zero model) | Use retained real frames as inputs where useful, plus clearly separated synthetic protocol peers. Inject unknown/duplicate fields, invalid frames, stale/double/revoked handles, blocked writes, cancel/timeout, transport loss, slow consumers and cleanup schedules. | SDK-US-005/010/014/015/017/018; precise 003/016 races; synthetic parent-first/nested/parallel evidence for 006/007/008. Test exact dispatch classification, full drain, no live fallback/retry and bounded cleanup. These remain synthetic even when derived from real bytes. |

Recorder S1 = original read/write fixture story, S2 = original question fixture
story; they are **not** US-001/002 or SDK-US-001/002. R2 combines related interaction
coverage but does not rename originals. S3 denotes deferred child/background
recording: **0 roots/turns/children granted in Phase 1**. Phase 2 needs a separate
finite cumulative child/depth/tool plan; root completion cannot certify it.

## Requested finite ceilings, not a renewed ledger

Every failed start/reservation counts as one attempt. No automatic scene retry,
resume, extra root or replacement turn. Provider-internal retries count against
both retry and generation ceilings. An input can cause multiple model generations
(e.g. a tool loop), while a rejected input may cause zero: these columns are
independent. Existing recorders cannot yet enforce every proposal below.

| Per provider run | New roots max | Submitted turns max | Actual provider API generations max (including retries) | SDK/scene retries | Native/provider retries max | Child attempts / depth | Activity wall max | Wire+control max | Stderr max |
|---|---:|---:|---:|---:|---:|---|---|---|---|
| R1 | 1 | 2 | 6 | 0 | 0 | 0 / 0 | 120 s | 2 MiB | 256 KiB |
| R2 | 1 | 2 | 6 | 0 | 0 | 0 / 0 | 120 s | 2 MiB | 256 KiB |
| R3 | 1 | 2 | 4 | 0 | 0 | 0 / 0 | 120 s | 2 MiB | 256 KiB |
| R4 | 0 (reuse R1) | 0 | 0 | 0 | 0 | 0 / 0 | 30 s | 2 MiB query/storage | 256 KiB reader |
| F / S3 | 0 | 0 | 0 | 0 | 0 | 0 / 0 | Offline bounded tests / S3 disabled | Synthetic test bounds | Synthetic test bounds |
| Provider total | 3 | 6 | 16 | 0 | 0 | 0 / 0 | 390 s including query | 8 MiB | 1 MiB |
| Both providers total | 6 | 12 | 32 | 0 | 0 | 0 / 0 | 780 s; coordinator hard ceiling 900 s | 16 MiB | 2 MiB |

Scene wall budgets include initialization (max 30 s), activity and owned cleanup
(max 3 s); these are not extra time grants. Reader may only initialize/query/close,
never start or resume a root. Absolute failure to reap owned processes stops all
remaining runs. Historical 120 s / 480 s / 2 MiB / 256 KiB recorder values are
constraints, not renewed authority; Codex historical 9/9 lifecycle turns stay
exhausted. A single durable cross-ticket ledger retains spent, reserved and failed
attempts; older ledger bounds are not reset to obtain this proposed budget.

| Provider/model request | Exact CLI/build gate | Requested token ceiling | Requested dollar ceiling | Current enforceability / decision |
|---|---|---|---|---|
| Codex / gpt-6.1-sol (subject to explicit model approval) | 0.162.1 absolute binary path + SHA256; historical macOS digest `74c6a6d263f40d65f99c27a815275f5b4ed0bcc2baecc396b4cf3255498bdfc0` is provenance, not a Linux binary approval | Each generating bundle 20,000 input (including cached input once) + 1,500 output; provider 60,000 + 4,500 | Each bundle USD 1, provider USD 3 | No verified monetary hard stop or strict token/API-generation/retry control. **BLOCKED**, no launch until an approved independent enforcement mechanism exists. |
| Claude / claude-opus-5[1m] (subject to explicit model approval) | 2.1.258 absolute binary path + SHA256; historical macOS digest `0d036f0b635d239844e6df6996bc05263fe305df505b073c71f79f83668ea35a` is provenance only | Same per-bundle and provider caps as Codex | Each bundle USD 1, provider USD 3 | --max-budget-usd 1 is advisory native control, not independent billing proof; thinking budget is not output cap. API-generation/retry/token stop unproven. Auth/policy isolation also **BLOCKED**. |
| Aggregate | Freeze exact tool/SDK commit + clean build digest and both selected binaries before authorization | 120,000 input + 9,000 output across both providers, including failed attempts | USD 6 total, including failures/retries | All ceilings cumulative, no credit from missing telemetry. Unknown enforcement or accounting means no run. |

These are requested maximums, not predictions of required tokens/cost or proof of
feasibility. If a chosen account/provider cannot independently prevent the next
request exceeding the cap (including caching and in-flight charges), the required
financial gate fails. A post-run usage report, CLI max-turns flag, capture byte
limit or wall-time kill does not prove a dollar hard stop. Missing provider-request
observability or retry suppression also blocks this proposed table; do not guess
API generations from submitted-turn counts. Revalidate exact CLI flags and native
model identifiers without silently switching versions or consuming a model turn.

## Numbered launch and publication gates

1. Parent reconciles this table with spent ledgers and explicitly approves exact
   provider/model, absolute CLI/version/digest, clean SDK/tool revisions, neutral
   prompt/input hashes, environment and every cumulative ceiling. #8 grants none.
2. #9/#10 offline guards demonstrate attempt reservation before launch, all finite
   ceilings, strict allowlisted operations, fail-closed retry/generation control,
   cleanup and poisoned CLI/account lookup for replay. New R1–R4 are not supported
   by the old S1/S2-only recorders yet. Do not use CLI help/version discovery as a
   model call or substitute for source validation.
3. Independently establish isolated policy/project/config/storage and no children,
   hooks, Apps, MCP, shell or arbitrary network tools. The Claude SessionStart
   event guard runs after a hook may already have executed; it cannot prove hook
   isolation. Operator supplies existing auth through approved opaque transport;
   no credential inspection/copy/repair or platform private auth access.
4. Prove monetary/token/generation/attempt/retry enforcement before model launch.
   Failure, unknown telemetry, policy/unknown request, byte/time cap, auth failure,
   unowned process risk or refused permission stops the current action and all
   dependent captures. Never automatically retry, broaden tools or change policy.
5. Capture raw wire/control/stderr/query/storage outside Git in owned private
   0700 directories / 0600 files. Per-version reviewed allowlist, unknown-field
   quarantine, typed ID equality mapping, timestamp type/order, source/build/CLI
   identity, prompt/input hashes, exclusions and coverage limits are mandatory.
   Only fresh neutral synthetic-task data is eligible. Old private-publication
   denial remains in force; private history cannot fill a missing row.
6. Publication requires separate reviewed approval after token/header/path/email
   canary scans, exact native request/answer replay, full consumption/EOF, cleanup,
   offline/race/vet/inventory/hash/dependency/examples and independent two-axis
   review. Never overwrite originals or label injected faults as recordings.
   Permission denial stops publication, not grounds for another route.
7. Parent integrates approved commits and only then publishes the code revision
   and checks all six Offline/External consumer jobs at that precise published
   SHA, outside-checkout consumers with no replace/go.work. No release/tag/settings
   change or automatic ticket closure follows from these results.

## Shared handoff for following tickets

#9/#10 consume the matrix and bundle plan, implement guards first, and report
which ceilings remain unenforceable. #13 can independently add zero-model F
coverage using the existing public seams; it does not wait for live permission.
#11/#12 remain blocked by tooling plus numbered gates, not merely by #8 completion.
#14 integrates evidence without converting unsupported/untriggered/missing into
passes. Phase 2 child work remains separate and can plan offline independently.

For an unpublished local result, hand off a full base/result SHA pair and a
`git format-patch` or Git bundle in repository-external temporary storage with a
SHA256 digest. The parent applies it to a clean unified checkout and verifies
ancestry/diff and fixture hashes before integration. A bundle/patch is reviewable
local code delivery, not a pushed branch or PR; do not run fetched-consumer scripts
against an unpublished SHA and claim success from a different revision. Share
only source/diff/check summaries, never private captures or credentials. Preserve
one fixture copy, provider module/FIFO/answer ownership boundaries and all live
switches at zero.
