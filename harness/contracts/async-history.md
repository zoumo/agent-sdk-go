---
type: Specification
title: 异步原生历史加载合同
status: stable
---

# Asynchronous native history loading

## Public interface and completion

Load explicitly resumes the SAME native main session and replays its full retained main conversation through the Session's single consumption entry point. Ordinary resume keeps its no-replay meaning. Native SDKs remain independent; no SDK-only flag is added to generated resume parameters.

Load admission returns a consumable Session before asynchronous work completes. History, live notifications, actual interactive requests and load boundaries share that entry point. A separately inspectable load outcome reports pending, success or failure, independently of Session termination and consumer drain. History keeps native event values and explicit History provenance. It cannot create answer authority.

Load succeeds only after native evidence confirms the requested resumed identity, all retained main-history records in the declared scope have been accepted, and integrity checks complete. Request options, disk identity and a successful initialize handshake alone are insufficient. A missing confirmation remains an explicit evidence gap, never successful Load. A mismatch fails. No extra prompt, model invocation, new session fallback or tool execution may be used to obtain confirmation.

Replay starts only after the resume confirmation. Live traffic can arrive earlier and remains subject to Session membership checks; it is neither silently discarded nor represented as replay. Live records preserve reception order; history preserves replay order. The two can interleave without claiming the original cross-source timeline. Load success follows all history admissions, not all live events or consumer drain.

Ordinary input before successful Load is rejected locally. Actual live request answers, interrupt and close remain available during loading; controls do not wait for history consumption. Configuration changes are rejected as load-pending until success to avoid changing the source being validated; no hidden scheduling is introduced. Failure never permits normal continuation, but accepted events remain drainable.

The load context owns startup, confirmation and replay only. Canceling it after success does not stop the Session. Closing during Load cancels reads and pagination, settles the load outcome, invalidates owned live handles and releases owned resumed resources without waiting for event drain. Failure after resume performs the same cleanup. Shared connections remain open for other Sessions. Unknown native dispatch or release outcomes and cleanup errors remain visible; uncertain ownership cannot be reopened as a fresh successful attachment.

Both adapters require a known native ID and absolute cwd plus the applicable data-home identity. The same connection and native main identity have one Session owner, reserving ownership before dispatch. All supported typed operations obey that ownership. Raw connection operations are explicit extensions and cannot create a second owner or answer handle; arbitrary raw protocol operations are outside coordinated Session guarantees.

Codex checks the connection's CodexHome and stored thread identity before resuming with excludeTurns=true. The actual resume response Thread.ID and cwd must match the requested identity and location before replay, independently of any later read of the requested thread. Attachment belongs to the resumed Session for its whole lifetime; no separate replay handle owns an unsubscribe that the application must coordinate.

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
an observed SessionID from request options or disk evidence. Initialization alone
cannot complete Load. If the fixed native version provides no non-executing
confirmation mechanism, report the capability gap and fail Load rather than
waiting for an extra application input or silently weakening success.

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

History producers retain a bounded page or record plus bounded index/cursor metadata, not all transcript payloads. They admit progressively to the Session FIFO with cancellation points and opportunities for live delivery. They do not own a second growing replay queue. The Session FIFO is dynamically growing and limited by available memory, so bounded reading does not promise bounded total backlog memory. Explicit record/page/count limits fail rather than truncate.

Cancellation releases blocked reads and RPC waits. Close never closes a caller-owned shared connection. Partial delivery followed by failure remains failure; failed history is not a complete load boundary or normal EOF. No automatic retry; callers reconcile partial results explicitly. Active-history rejection and non-atomic source reads remain declared limitations, not guarantees of lossless recovery of running work. If live changes invalidate the selected source, fail visibly rather than hide the mutation.

## Acceptance

Public-interface synthetic tests cover ordered multipage complete delivery,
first-record retention, tool content/parent evidence, missing/mismatched identity,
unknown/malformed data, branch and compaction semantics, repeated cursor/IDs,
late errors, bounded resources, slow consumers, cancel/close, and session isolation.
