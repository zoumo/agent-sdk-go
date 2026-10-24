# Subagent forwarding and lineage

See the [per-capability compatibility matrix](subagent-compatibility.md),
[runnable offline example](../examples/subagents-offline/main.go) and
[source provenance](type-sources.md). Created is
conditional for exact CLI 2.1.258, the current installed version. `--version`
inspection and static source review do not constitute live subagent validation.

`Open` always requests `forwardSubagentText: true` at initialize and returns
`*Client`. `Client.Receive` returns `ReceivedEvent`: direct native `Event` values or separate
`SubagentAssociated`, `SubagentCreated`, and `ObservationState` values.
`Options.Subagents` configures bounds/history only; zero values still manage the stream.
There is no extra CLI flag, child process, subscription, hook, or automatic resume.
See the [user-story sequences](event-sequences.md).

```go
client, err := claude.Open(ctx, claude.Options{
    CLIPath: "claude",
    PermissionHandler: handler, // original PermissionRequest; no second responder
    Subagents: claude.SubagentOptions{},
})
if err != nil {
    return err
}
defer client.Abort()
if err := client.Send(ctx, "Inspect the project"); err != nil {
    return err
}
for {
    record, err := client.Receive(ctx)
    if err != nil {
        return err
    }
    switch event := record.(type) {
    case claude.Event:
        _ = event.Raw // exact frame bytes, excluding newline
        _ = event.ParentToolUseID // native identity, never rewritten
    case claude.SubagentAssociated:
        _ = event.Source // observation, not proof of creation
    case claude.SubagentCreated:
        _ = event.Source.TaskID // conservative creation confirmation
    case claude.ObservationState:
        _ = event.Reason

    }
}
```

`Client` owns one process and private managed state, the sole native dequeue
consumer. It adds no background reader. Send, Interrupt, SessionID,
RequestedResumeID, Done, Err and Close retain their lifecycle/concurrency contract.
Receive requires one caller; its context bounds that call. Close owns process
shutdown; root result or idle does not close observation or stop permissions.

Native values retain UUID, session, parent tool, content, usage, Origin and
unknown fields without rewriting Raw. Event.AgentID and Event.SubagentType expose
native fields only where present. No inferred source, run state, delivery sequence
or SDK-origin flag is added to native messages.

Derived convenience values carry relationship and observation metadata separately.
Their definitions and JSON codecs live in derived_events.go. Their concrete Go
types identify the event; no native message is nested inside them. Sequence is
local delivery order on these notifications, not a native cursor. SubagentCreated
is independent of association and the observer never reports complete forwarding.

## Creation evidence

The creation contract targets **exactly CLI 2.1.258**, inspected at
the installed executable on 2026-10-18. Official SDK 0.3.258 declarations and
manifest, plus read-only static inspection of that local binary, support the
criteria below. No model invocation, upgrade or auth change was made. Other
version strings retain observation/Raw but cannot establish Created. This does
not assert a minimum version or support for every version after 2.1.77.

A single root `system/init` must identify that exact version, a nonempty stable
session and the builtin tools. Open must own a fresh process: explicit
ResumeID or any History seed disables creation for the entire observation.
Replay, repeated init (even UUID-deduplicated), a session change or a graph
identity/parent/tool conflict disables further creation claims for the epoch.
Those frames and ordinary associations still flow normally.

| Observation | SDK result | Evidence required |
| --- | --- | --- |
| `local_agent` task_started with explicitly false `is_backgrounded` | Foreground Created | Nonempty native task/tool IDs, complete observed builtin Agent/Task request, attributable ancestry, no contradictions. Fixed 0.3.258 declaration says resumed subagents always register in background. Missing/null/true does not satisfy this route. |
| Successful Agent structured result | Possibly late Created | Complete builtin request; exactly one successful matching tool_result in the user frame; structured tool_use_result status completed/async_launched and nonempty agentId; matching local_agent task/tool identity. Matcher tolerates synthetic reordered evidence; this is not a producer-order guarantee. |
| Ordinary task start, first task ID, name, depth, input alone, hook | Associated only | These alone do not establish creation. No hook is installed. |
| SendMessage, resume key (including null), remote_launched, error/malformed/missing result, ambiguous result blocks or contradictory IDs | No Created from that invocation | Hooks can replace input; absence of resume in a request is never the sole proof. |

The request's input must be an object with nonempty prompt and description.
Remote isolation and unadvertised/namespaced tools are excluded. Matching uses
native task/tool IDs, never descriptions or agent names. A result must belong to
the same caller as the request. Conflicting evidence poisons that invocation;
graph conflicts conservatively disable all later creation claims in this epoch.

Created confirms that creation occurred; it supplies no invented timestamp.
Associated precedes attributed native events. Child Native continues while
waiting for background confirmation, including after a parent result. A late
Created can therefore follow child text when later task/ancestry evidence completes
the proof. In current 0.3.258, a root turn result follows that turn's
assistant/user/stream_event messages: its own tool-result receipt must not be
illustrated after its result. Created is emitted once per merged
entity, across tool aliases and later work, without depending on UUID history.
The evidence ledger has no eviction: MaxCreationEntries defaults to 8192,
ceiling 131072. Exhaustion uses the existing visible terminal ResourceLimit
path; it cannot turn an evicted entity into a second creation. No-ID native
messages remain independently delivered.

Current 2.1.258 declarations do **not** expose run_id, parent_task_id or message
agent_id. Created has empty Source.RunID and `RunScope: legacy-unknown`; it
uses observed tool ancestry. Optional codecs/Raw preserve
additional native fields covered by offline fixtures, but those newer fields are
not a claim about current CLI emission. Missing identity/ancestry remains a
conservative creation gap. The observer makes no live validation claim.

## Identity, lineage and runs

`SystemEvent` includes optional `RunID`, `ParentTaskID`, `SpawnDepth` and
`IsBackgrounded` pointers. Existing `TaskType` and raw `Patch` remain unchanged.
Missing/null/value and unknown patch members round-trip through the existing
Raw/Fields codec, including false and zero. Native frames are never rewritten.

`SubagentIdentity.AssociationID` is an SDK key scoped by session identity and epoch;
it is separate from native agent, task, run and tool IDs. A tool-only node has
`AssociationID: "tool:<id>"`, `ToolUseID: "<id>"`, and no invented AgentID/TaskID.
When a same-session message or local-agent task bridges tool and task identity,
the node is enriched under its original association key. Independently observed
aliases are joined only with that bridge evidence: an `identity-merged`
association carries `PreviousAssociationID` for the retired key. Consumers
redirect that key to Source.AssociationID. These records are not entity creation.

Assistant/user agent_id equals task_id only under the fixed message contract.
Generic agent IDs (including permission metadata) acquire that mapping only
after the message evidence arrives. Explicit non-local-agent task types are
retained as task identity without subagent associations; known non-agent tool
references cannot create legacy agent nodes. An unclassified task remains
unresolved until a later frame proves it is an agent task. Already delivered
native frames are not replayed or reclassified.

Nested lineage uses `task_started.parent_task_id` or an observed assistant
tool's owning source, scoped to the native session. Unknown explicit task
parents remain unresolved references, even if spawn_depth is present. Depth
never selects a parent. Known ancestors are associated before descendant
association/enrichment records. An unresolved ancestor keeps its descendants'
scope unresolved. Later evidence enriches relationships without replaying Native.
Cycles, conflicting identities, conflicting parents and conflicting tool owners
produce `ObservationState(incomplete, <diagnostic>)`; accepted relationships are
not silently rebound. The pinned live message contract does not establish
parent_agent_id: that field belongs to the TS historical SessionMessage API.
It remains Raw data, not invented live ancestry.

`Source.RunID` carries only the event's native run_id. No ID means
`RunScope: "legacy-unknown"`, even when a newer native run is known.
`CurrentRunID` is the greatest observed run_id for that same session/task, using
only the fixed protocol's plain-string ordering. A newly observed run emits
`ObservationState(run-observed, new-run-observed)` before Native and keeps the
entity's association key. This is discovery of work, not proof of creation or
start time. A lower run retains its original native RunID; the private ledger marks it stale;
it cannot modify current-run metadata, task type, tool aliases or parent.
Terminal accounting is described below; a late terminal changes only its own
run ledger entry. Missing run IDs cannot disambiguate
late work, and the SDK does not fabricate globally comparable legacy IDs.

`SubagentStart` was rechecked against the
[official Hooks reference](https://code.claude.com/docs/en/hooks#subagentstart)
on 2026-10-18: it also fires on resume and teammate messages. The conditional
Created rules above use task/result evidence instead. No observation hook is installed.

Permission attribution retains the original request ID and sole answer handle.
Root answers, native status (including killed), content and usage remain native;
no text, usage or fees are aggregated.

## Evidence and compatibility boundary

Current creation baseline: official TS SDK **0.3.258**, whose manifest names
CLI **2.1.258**. See [source hashes](type-sources.md). The rest of
the existing observation API was originally implemented against the following
newer schema reference; this is not a current-CLI emission claim.

Original protocol reference: Python SDK commit
[f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab, initialize forwarding](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/query.py)
and TypeScript SDK 0.3.302 declarations (`SDKAssistantMessage`, `SDKUserMessage`,
`SDKTaskStartedMessage`, `SDKTaskProgressMessage`, `SDKTaskUpdatedMessage`,
`SDKTaskNotificationMessage`, `SDKPartialAssistantMessage`). The fixed declarations and
source are E1 evidence, not proof of installed CLI behavior.

| Capability | SDK / offline evidence | Runtime evidence |
| --- | --- | --- |
| Initialize forwarding option | Implemented; synthetic process captures automatic enabling | Unverified |
| One owned root stream, child text/thinking/tools, Raw and permission handle | Implemented; original synthetic subprocess fixtures (E2), including race suite | No live CLI/model test |
| Associated before attributed native | Recursive same-session task/tool graph; ancestor ordering and later enrichment (E2 fixtures) | Unverified |
| Created | Exact 2.1.258 foreground/corroborated-result rules; original subprocess fixtures (E2) | No live subagent test |
| Modern recursive graph and legacy tool reconciliation | Implemented; three-level fixtures, bridge evidence, conflicts/cycles (E2) | Unverified |
| Run identity and late native frames | Implemented; new-run, stale metadata, resumed tool alias and missing-run fixtures (E2) | Unverified |
| Scoped UUID deduplication | Implemented; session/source/run/type scope, bounded cache, no-ID/partial/complete fixtures (E2) | Single epoch only; unverified runtime |
| Task lineage codecs and non-agent tasks | Implemented; null/absent/value/typed edits and task-type fixtures (E2) | Unverified |
| Run terminal/background accounting | Implemented; patch-only and notification terminal, stale/legacy isolation, bounded ledger (E2) | Unverified |
| Native session state / background snapshot | Typed State / raw Tasks entries; idle and result remain distinct (E2) | Emission is CLI/version/environment dependent; unverified |
| History association seed / explicit resume | History provenance and permanent gap; no replay or creation (E2) | No live history recovery claim |
| Timed unresolved buffering, atomic bounded fanout and request isolation | Implemented; public API synthetic peers, full-queue control replies, pending-work invalidation, original question handles and close races (E2) | Unverified |

Initialize rejection fails Open. A successful acknowledgment does not prove
the CLI honors forwarding, so the first ObservationState always reports
`ObservationState(unverified, forwarding-not-confirmed)`. Missing source attribution
waits within the bounds below, then remains explicitly unresolved; unknown unowned
events are unscoped. This is not a promise of complete descendants or recovered
history.

Dedup uses only native UUIDs on known envelope contracts (assistant/user/system/
result/stream_event), scoped by epoch, native session/source, task/run when
present, type/subtype and parent tool. It does not hash text, message.id, Raw or
time windows. Identical no-ID deltas are both delivered; partial and complete
messages are distinct kinds. Unknown event kinds, unknown system subtypes and sessionless frames are not
deduplicated. No cross-epoch exactly-once claim is made. Frames lacking native
run_id remain legacy/unknown-run. A newly observed run creates a conservative
dedup boundary so a later no-run frame is never suppressed solely by an older
run's UUID history; this assigns no native run to that frame.

The native alias/tool-owner/non-agent index is bounded by `SubagentOptions.MaxNodes`
(default 4096, ceiling 65536 total entries). `MaxDepth` defaults to 32 (ceiling
256), and `MaxDedupEntries` to 8192 (ceiling 131072). Zero selects defaults;
negative/above-ceiling values fail before startup. Alias entries count against
the native index bound; no stale identity history is silently evicted. Overflow uses
the owned client's terminal ResourceLimit path and emits incomplete plus the
already accepted native frame with unresolved scope. Derived-event fanout and pending
source attribution have their own bounds below. Native consumption uses a dynamic
FIFO shared by root and child events within this client, independent of bounded
control/permission work queues. Derived records use bounded inline transaction
staging, not a second event stream. A disconnect reports incomplete, then the original
terminal error; explicit Close reports closed, never child success.

The retained official TS 0.3.302 declaration file SHA256 is
`26f9a0c91ad64e2020f0d3734932302e3cc09685bddf2b2f9ef60839ce278773`.
Its task declarations specify same-task run ordering and stable task identity
across resume (lines 6145–6271); assistant/user task mapping is at lines 3702
and 6475. These are E1 declaration evidence, not live interoperability tests.
The fixtures in `tree_lineage_test.go` are original synthetic peers through
Open/Send/Receive (E2); no upstream implementation was copied.

Offline validation uses Go 1.26.9 through mise, `CLAUDE_E2E=0`,
`GOPROXY=off`, `GOSUMDB=off`, `GOTOOLCHAIN=local`, `GOENV=off`, and a temporary
GOCACHE. Commands: `go test -count=1 -timeout 180s ./...`,
`go test -race -count=1 -timeout 300s ./...`, and `go vet ./...`.
Synthetic subprocess fixtures re-execute the test binary under an isolated HOME; no installed
CLI, real model, authentication change, or account probe is used.


## Result, waiting, idle, stopping and identity

Keep these independent dimensions in the UI and application state:

| Evidence / action | Meaning | Does not prove |
| --- | --- | --- |
| Root native `result` | The foreground answer/result boundary; preserve its source, answer and usage | Native session idle, no pending request, all background runs finished, or agent deletion |
| Original permission / AskUserQuestion handle | A request needs a response under existing deny/timeout/cancel policy | Task success or task deletion; silence while waiting is not completion |
| Native `session_state_changed.state` | `running`, `requires_action`, or `idle` from the provider | Every task has a terminal edge, or agent identities were deleted |
| `background_tasks_changed.tasks` | Replace the process-local set of background tasks; ambient tasks may be excluded from activity indicators | A missing task succeeded, failed, stopped, or was deleted |
| Task patch / notification terminal | One identified run ended with the explicit native status | Agent entity deletion, transcript deletion, or closure of the observation |
| `Interrupt` acknowledgment | Provider acknowledged an interrupt request | Per-task terminal status, rollback, or deletion |
| `Receive(ctx)` cancellation | That receive wait ended | Process shutdown or cancellation of provider work |
| `Client.Close` | Shut down the owned process and invalidate its request handles, using Client's bounded cleanup | A provider-side delete, successful background completion, or retention policy |

The fixed official TS 0.3.302 `sdk.d.ts` (same SHA256 above), lines 5999–6007,
defines `system/session_state_changed` and describes idle as the turn-over signal
after result flushing and the background-agent loop exits. `SystemEvent.State`
exposes that native value; unknown values remain unknown and Raw/Fields survive.
It is never synthesized from result, timeout, quiet, task count, EOF or disconnect.
Older CLIs without this event provide only the foreground result boundary; global
idle remains unobserved. Continue calling Receive after result to see later native
state and background/control events. Native idle is not a terminal edge for every
task and never deletes an agent identity.

The [official Python SystemMessage reference](https://code.claude.com/docs/en/agent-sdk/python#systemmessage)
(accessed 2026-10-18) documents the emission opt-in
`CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1` and receiving beyond result. Callers can
request it with `Options.Env` using the existing environment contract; Open
does not change this environment setting or assert installed CLI support. The
Python `data.state` accessor is its wrapper shape; the fixed TS stream-json wire
field is top-level `state`. No new Go control protocol is introduced.

Lines 3763–3795 of the same fixed declaration define background snapshots as a
REPLACE level signal with unspecified ordering relative to lifecycle edges and
process-local scope. `SystemEvent.Tasks` exposes a slice of raw JSON entries so
future fields, optional run_id, parent_task_id and ambient remain lossless.
This SDK does not correlate snapshot disappearance with terminal edges or use
snapshots to create agent entities. Reset any application snapshot at a new Epoch;
no startup snapshot is promised for a newly spawned CLI.

### Run ledger

The private ledger for each task lifecycle event with session/task identity tracks
`RunStatus`, `RunTerminal` and optional `RunBackgrounded`, plus
`CurrentRunStatus` / `CurrentRunTerminal` for the greatest observed native run.
This includes unclassified and non-agent tasks without promoting them to agents.
`task_started` records running; patch `is_backgrounded` updates background mode.
A task_updated patch status of completed, failed or killed terminates that run;
a task_notification status of completed, failed or stopped does the same.
The first terminal emits an SDK `ObservationState(run-terminal,
native-task-terminal)` before the unchanged Native. Duplicate terminals do not
emit a second transition. The first terminal status stays sticky (killed is never
rewritten to stopped); each later native status is still present in Raw/System.
Pending/running/paused and unknown statuses never imply success. Progress without
status keeps the previous ledger status; no status evidence means unknown.

Older run terminals update only that older entry. Missing run IDs use a separate
legacy-unknown bucket and never terminate the known current run. Within that
bucket multiple legacy executions cannot be distinguished; terminal stickiness
cannot establish a new run without a native run ID. A new native run reuses the
same agent association and remains observed after prior runs finish. The ledger
has no delete operation and does not evict state. `SubagentOptions.MaxRuns` bounds
all session/task/run buckets (default 8192, ceiling 131072); exhaustion reports
incomplete/run-limit, preserves the accepted Native and terminates through the
existing ResourceLimit path. The current-run index has at most one entry per
ledger task and is bounded by the same limit. No global all-done count is inferred.

### Explicit recovery and gaps

`SubagentOptions.History` accepts `SubagentHistory{SessionID, TaskID, ParentTaskID}` only for
caller-verified historical local-agent associations. It uses existing graph bounds
and lineage diagnostics. Seeds emit SubagentAssociated with reason/provenance
history before live events; they never emit Created, native transcript frames,
terminal states or deletion. An absent parent stays unresolved. Seeds do not run
historical file queries, poll files, send user input, answer requests or resume
anything. Live task evidence can enrich the same association later.

Explicit ResumeID and/or history seeds emit incomplete/history-gap: earlier
transcript and request outcomes have not been recovered. RequestedResumeID is
still a request, not proof that the CLI resumed it. A new Open has a new epoch,
a fresh run/dedup ledger and no reusable old request handles. Disconnect/EOF emits
incomplete/stream-ended followed by the original client error; explicit Close
emits closed/client-closed followed by EOF. Neither path invents run success.

Original synthetic subprocess tests in tree_terminal_test.go cover the
result→original permission handle→patch killed sequence. Coverage includes
both terminal families, duplicate terminals, late/unknown/paused/legacy runs,
background snapshot ordering, native idle without inferred task completion,
receive timeout, disconnect, history seeds, fresh epochs and run exhaustion.
Existing close-race tests remain part of the full race suite. These are E2
observations only. No real CLI/model, installed runtime interoperability,
authentication change or transcript completeness is claimed.

## Bounded attribution and original requests

| SubagentOptions limit | Default | Ceiling |
| --- | --- | --- |
| MaxPendingRecords | 8192 records per atomic fanout | 131072 |
| MaxUnresolvedEvents | 128 held Native frames | 65536 |
| MaxUnresolvedBytes | 4 MiB of held native wire bytes | 64 MiB |
| MaxUnresolvedWait | 5 seconds | 1 minute |

Zero selects defaults; negative values and values above a ceiling fail before
startup. Native event buffering grows dynamically; the deprecated
`Options.Limits.EventBuffer` is ignored (0..65,536 is still validated). Incoming
frame size remains bounded by `FrameBytes`. Decoded views share each frame's
lifetime; MaxUnresolvedBytes measures wire bytes, not total Go heap allocation.
Graph, alias, depth, run and UUID bounds continue to apply independently.

A native frame with unresolved attribution waits for later graph evidence.
Associations/enrichments precede attributed delivery. Once a held frame resolves,
its private attribution gains the proven source/parent while Event.Raw, original native IDs,
run metadata, text and usage remain intact. Unrelated root/control frames can pass
held frames. Derived Sequence means local notification order, and buffering can change delivery
order relative to native arrival. There is no server cursor or replay guarantee.
Unknown events without source identity remain unscoped and are never buffered.
Control requests/cancellations bypass association waiting; the sole answer authority
is still the original PermissionRequest delivered by the existing worker pool.
Relationship metadata copies have no answer method or authority.

The association wait starts when Client.Receive accepts a frame. It advances only
local attribution, never a task lifecycle. Expiration produces
`ObservationState(incomplete, unresolved-source)` followed by the unchanged Native
without attaching SDK scope/reason fields to it. Later evidence enriches the
association without replaying already delivered Native. Receive cancellation
preserves held frames. No extra reader/timer goroutine, process, RPC or auto-answer
is introduced. Expiration is observed on Receive, and Close/EOF releases held
frames immediately as unresolved without waiting for their deadline or inventing
success. Consumers must drain Receive to obtain accepted frames and terminal errors.

Count/byte exhaustion is terminal ResourceLimit (reason unresolved-source), in
keeping with the owned Client contract. The raw overflow frame and already held
frames remain observable; nothing is silently discarded. Each native input's
association/diagnostic/Native fanout reserves MaxPendingRecords atomically. If it
cannot fit, none of that input's staged derived records is published. Instead a
fixed diagnostic plus a single reserved raw frame reports incomplete/fanout-limit
and unresolved attribution. The graph is frozen after a failed fanout; remaining
accepted transport frames drain as unchanged native events. Historical seed fanout
exhaustion fails Open before spawning its process. Emergency storage is constant
(one reserved input plus one released held frame), independent of the full fanout
queue, so diagnostics cannot be hidden by a full queue. Done and Err expose
terminal failure without draining Receive.

The ordinary stdout reader continues dispatching control replies before event
capacity checks. Full capacity does not block Interrupt acknowledgments; exceeding
capacity still fails the whole owned client and wakes pending controls, writes and
permission contexts. Relationship buffering never queues a permission response, replaces
request/tool/agent IDs, automatically approves, or changes default deny and unknown
control errors. Native cancellation, caller answer cancellation, Close and a fresh
epoch retain the existing single-use handle rules. Parent result is still only
its native answer/usage boundary; child text/thinking, partial/complete snapshots
and task usage stay separate. Derived notifications retain relationship
source/scope and unknown UsageScope; native events carry only native fields.
No totals are accumulated.

Original synthetic `tree_bounds_test.go` peers cover attribution before/after the
wait deadline, receive cancellation, count/byte/single-frame exhaustion, atomic
fanout/history bounds, invalid limits before spawn, full native queues and control
replies, terminal wake-up of pending Interrupt/permissions, question input and sole
handle, native cancel/fresh epochs/concurrent Close, default deny/unknown controls,
and separate root/child usage. Existing lineage/terminal cases explicitly choose
a short wait when testing later enrichment after unresolved delivery. Evidence is
E2 only; no real CLI/model, authentication change or runtime support claim.

中文事件时序说明：[前台/后台任务、子流、审批和结束边界](event-sequences.md)。


## Shared relationship storage

Each managed observer uses the shared graph for accepted parent relationships,
root membership, cycles, depth and identity contraction. See the
[shared relationship contract](../../harness/contracts/lineage.md) for its independent bounds:
at most `2 * MaxNodes` vertices (stable associations plus session roots) and
`MaxNodes` relationships, in addition to the native index limit above.
Native task/agent/tool alias recognition chooses a stable association before
asking the graph to merge identities atomically. Unresolved parent-task evidence,
runs, creation checks, deduplication, raw frames, permission handles and transport
shutdown remain native responsibilities.
