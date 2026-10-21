# Asynchronous native history loading

Status: implementation spec, 2026-10-21. Resume-and-replay revision baseline: `7878e36`.
Event-consumption contract revised 2026-10-22; see [history events](history-events.md).

## Public API and agreed test seams

`Load` means resume the SAME native session plus asynchronous replay of its full
retained main conversation, followed by normal continued input. Ordinary resume
keeps its current no-replay contract. Keep Load as an explicit operation rather
than adding an SDK-only flag to generated native resume parameters. The two SDKs
reuse their existing native resume primitives and remain independent.

Public seams: `codex.Client.Load(ctx, HistoryOptions) (*LoadedThread, error)` and
`claude.Load(ctx, LoadOptions) (*LoadedSession, error)`. Claude LoadOptions contains
normal process Options plus HistoryOptions. Both load handles expose Events,
Done, Err and Close. Events uses each provider's real-time event type. Caller must drain the history stream then check Err. Done
means resume AND retained-history replay completed, not that the live client or
Agent work ended. Load acceptance is asynchronous and is not readiness. Input
before successful Done is rejected locally. Failure never permits continuation.

Codex's caller-owned connection remains the normal execution/notification seam;
use StartTurn after the load completes. LoadedThread.ResumeResponse exposes the
validated native resume result after successful Done. Claude's LoadedSession.Client
returns its owned normal Client only after successful Done; Send/Receive and live
permission handles stay on that Client. History uses the same event types and
consumer as live delivery, with explicit SDK-only History source metadata. Its
projected snapshots are not received wire notifications or revived approvals.

The load context controls startup and replay only. After successful completion,
canceling it does not stop the resumed session. Close cancels blocked delivery or
pagination, waits for replay cleanup and releases the owned resumed session:
Claude closes its child; Codex unsubscribes this thread without closing the shared
connection. Failure after a successful resume performs the same cleanup; unknown
native dispatch/release outcomes remain errors. A dispatched Codex resume whose
attachment cannot be confirmed retains the admission gate even after Close; close
the original Client to settle ownership. Close errors are not discarded.

Both require a known native ID and absolute cwd. Codex checks the connection's
CodexHome and stored thread identity BEFORE resuming with excludeTurns=true;
resume identity/cwd must still match. No history/path substitution, new thread,
implicit prompt, model invocation or tool reexecution. Load reserves the thread on
this Client, blocks competing typed StartTurn/ResumeThread operations, and has a
bounded number of retained handles. Subscribe to live notifications before Load
when they matter; Load does not consume the caller's notification subscription.
Raw Call is an advanced protocol escape hatch and does not implement these gates.

Codex pages native turns oldest-first with itemsView=full until explicit
pagination end. Events projects retained items as item/completed snapshots and
known terminal turns as turn/completed. Missing item timestamps remain explicitly
unknown; no start/delta events are manufactured. Unknown turn statuses survive
as Event with native Turn Data and no claimed notification method. It preserves
exact raw turn JSON in History metadata, decoded native items, native IDs and
recorded timestamps. Reject active history, changed identity, nonadvancing/cyclic
cursors, repeated IDs, summary-only turns and observed source changes. Paging is
not atomic; callers must quiesce external writers. Generated turns/items types
remain sourced from the existing exact verified 0.162.1 schema blob.

Claude validates and indexes exactly one transcript at explicit ConfigDir/cwd,
then starts the ordinary CLI resume process with that exact session ID and data
home before delivering the first record. It does not feed history back as prompts
or use Python. The native initialize handshake must succeed; nonexistent native
resume must fail. Known native system/result root identity must match throughout
continued use. Preserve RequestedResumeID versus actually observed SessionID:
CLI initialization does not itself expose the native session ID, so do not invent
an observed SessionID from request options or disk evidence.

Claude selects the latest terminal-derived main user/assistant leaf, walks
parentUuid root-to-leaf, excludes sidechain/team/meta leaves and does not follow
logicalParentUuid across compaction. Retain compact summaries, tool-use/result
blocks, UUID, parent, time and raw bytes. Project retained entries through the
normal native Event decoder, mapping validated transcript session identity to
the native envelope and retaining exact original bytes in History metadata.
Do not replay abandoned branches or
separate subagent files. Unknown blocks survive raw; unknown transcript kinds,
missing parents/messages, duplicate UUID/JSON keys, cycles or corrupt data fail
with offending raw evidence. The original transcript prefix is immutable (SHA256
verified). Native resume may append known configuration metadata with matching
identity; appended conversation, unknown/corrupt metadata, truncation or rewriting
fails. Unsupported long project keys fail explicitly. Completeness means all retained records in this declared native
main-chain scope, not deleted pre-compaction content or absent token deltas.

## Resource and completion contract

An unbuffered event channel provides backpressure. Codex retains one bounded
RPC page plus bounded ID/cursor tracking; Claude retains bounded offset metadata
and one record, never a full transcript payload slice. Explicit record/page/count
limits fail instead of truncating. Cancellation releases blocked sends and RPC
waits; closing a load handle never closes a caller-owned Codex connection.
Partial delivery followed by failure remains failure, never normal EOF. No
automatic retry; callers discard or reconcile partial results explicitly.

## Acceptance

Public-interface synthetic tests cover ordered multipage complete delivery,
first-record retention, tool content/parent evidence, missing/mismatched identity,
unknown/malformed data, branch and compaction semantics, repeated cursor/IDs,
late errors, bounded resources, slow consumers, cancel/close, and session isolation.
Each slice starts red, then gains its implementation. New acceptance verifies native resume before first replay,
no-prompt/no-new-session traces, input gating, same-ID continued Send/StartTurn,
ordinary resume without replay, startup/resume mismatch and cleanup failure. Run pinned mise Go tests
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
