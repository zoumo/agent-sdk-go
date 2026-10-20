# Asynchronous native history loading

Status: implementation spec, 2026-10-21. Baseline: `6dbf3e9c8264`.

## Public API and agreed test seams

The public seams are `codex.Client.LoadHistory(ctx, HistoryOptions)` and
`claude.LoadHistory(ctx, HistoryOptions)`. Each returns a native `History`
handle before reading all history. The handle exposes `Records()`, `Done()`,
`Err()` and idempotent `Close()`. Records are history snapshots, never live
notifications, approval handles or current running-state transitions. The load
context owns the entire replay lifetime; cancel it or Close the handle to abort.
Err is final after Done; nil means the selected retained history was exhausted.
Consumers drain Records, then check Err. Close waits for owned work to stop.
No root client, hidden prompt, model call, tool execution or implicit resume.

Both require a known native ID and absolute cwd. Codex also requires the expected
absolute CodexHome and checks the connection's initialize response. Claude
requires an explicit absolute ConfigDir and addresses exactly one project file;
no global search, Python bridge or access to unrelated sessions. Native identity
and cwd are checked against stored evidence, with failures explicit. Missing
sessions never become new ones. Load acceptance does not certify existence,
capability or completion; source errors can arrive asynchronously through Err.

Codex adds generated native `ThreadTurnsList*` and `ThreadItemsList*` types and
`ListThreadTurns` / `ListThreadItems` convenience RPCs. Regenerate from the exact
existing verified upstream blob, preserving reproducibility and inventory.
Replay pages turns oldest-first with `itemsView: full`; each record retains the
exact received turn JSON and decoded native turn/items. Continue until explicit
null/absent nextCursor; reject empty/nonadvancing/cyclic cursors and repeated
turn/item IDs. No fallback to resume or deprecated includeTurns. Unsupported
pagination remains an RPC error. Reject active threads; compare metadata before
and after paging and fail on observed mutation. Native pagination is not an
atomic snapshot; success covers an unchanged observed retained view, not proof
that an external writer never raced. Callers must quiesce writers for that proof.

Claude opens the exact project transcript and freezes the initial byte boundary.
Index bounded record offsets/chain metadata asynchronously, then deliver the
selected root-to-leaf chain by reading original bytes. Select the latest main
user/assistant leaf by file position, excluding sidechain/team/meta leaves;
follow parentUuid, including system/progress/attachment links. Do not follow
logicalParentUuid across compaction. Preserve compact summaries, raw message
blocks (including unknown blocks), UUID, timestamp, parent UUID and tool IDs
inside raw content. Raw blocks are not flattened through the live stream codec.
Missing parents, duplicate UUIDs, cycles, malformed JSON and unsupported record
types fail explicitly, retaining offending raw bytes in diagnostic errors.
Known transcript metadata is outside the selected conversation-chain scope.
No claim of historical token deltas, permission timing, overwritten branches or
content removed by native compaction. Standalone subagent files are not loaded.
Changed/truncated files fail rather than claiming a complete active snapshot.
Long/unrecognized project paths may be rejected explicitly rather than searched.

## Resource and completion contract

An unbuffered record channel provides backpressure. Codex retains one bounded
RPC page plus bounded ID/cursor tracking; Claude retains bounded offset metadata
and one record, never a full transcript payload slice. Explicit record/page/count
limits fail instead of truncating. Cancellation releases blocked sends and RPC
waits; closing a history handle never closes a caller-owned Codex connection.
Partial delivery followed by failure remains failure, never normal EOF. No
automatic retry; callers discard or reconcile partial results explicitly.

## Acceptance

Public-interface synthetic tests cover ordered multipage complete delivery,
first-record retention, tool content/parent evidence, missing/mismatched identity,
unknown/malformed data, branch and compaction semantics, repeated cursor/IDs,
late errors, bounded resources, slow consumers, cancel/close, and session isolation.
Each slice starts red, then gains its implementation. Run pinned mise Go tests
regularly, full offline/race/vet checks at completion, independent Standards and
Spec reviews against this baseline, fix findings, then commit only task changes.

Real validation is separate: use owned temporary cwd/data and externally supplied
authentication, never personal history or copied credentials. Report capability,
native read-only integration, and real model-created histories separately. A
missing prerequisite is unverified, not a pass. Do not push. After SDK validation,
update MASS harness contracts only, preserving its current branch and unrelated
changes; no MASS implementation or dependency upgrade.

## Sources

- [Codex app-server](https://developers.openai.com/codex/app-server/): pagination,
  explicit ordering and full items; current documentation is newer than the pin.
- [Pinned Codex schema](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/schema/precomputed/app-server-exports-experimental.json.zst).
- [Claude session API](https://code.claude.com/docs/en/agent-sdk/python#get_session_messages).
- [Pinned Python session reader](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/sessions.py):
  format and branch-selection reference only; Go implementation is original.
