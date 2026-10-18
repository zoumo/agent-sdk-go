# Subagent forwarding and lineage (tickets 01–02)

`Options.ForwardSubagentText` and `types.Options.ForwardSubagentText` default to
false. `OptionsFromTypes` and `Options.Types` preserve the value. True adds
`forwardSubagentText: true` to the native initialize request; false omits it.
There is no new CLI flag, child process, subscription, hook, or automatic resume.
Ordinary `Open` / `Client.Receive` retain their native event interface.

```go
client, err := claude.OpenTree(ctx, claude.Options{
    CLIPath: "claude",
    PermissionHandler: handler, // original PermissionRequest; no second responder
}, claude.TreeOptions{})
if err != nil {
    return err
}
defer client.Close()
if err := client.Send(ctx, "Inspect the project"); err != nil {
    return err
}
for {
    record, err := client.Receive(ctx)
    if err != nil {
        return err
    }
    switch record.Kind {
    case "Native":
        // record.Native.Raw is the exact native frame, excluding its newline.
        // Render by Source/Scope; do not append child text to the root answer.
    case "SubagentAssociated":
        // Establish or enrich an observed node before its native event.
    case "ObservationState":
        // Surface record.State / record.Reason to the observer.
    }
}
```

`TreeClient` owns its private ordinary Client and is its only Receive consumer.
It adds no background reader. Send, Interrupt, SessionID, RequestedResumeID,
Done, Err and Close delegate to the owned client. Receive requires one caller;
other methods retain the client's concurrency contract. A receive context only
bounds that call. Close owns process shutdown; root result does not close the
stream or stop permission handling.

Every wrapper contains provider, process epoch, local delivery sequence,
live-arrival provenance, root/source identity, scope and unknown usage scope.
Native UUID, session, parent tool, content, usage and unknown fields remain in
`Native`, without rewriting Raw. `Event.AgentID` and `Event.SubagentType` expose
native `agent_id` / `subagent_type`; the existing Raw/Fields codec preserves
absent, null and typed edits. These fields remain absent where the CLI omits
them; the SDK does not synthesize them into native messages.

SDK records carry `origin: sdk-derived`; Native carries `origin: native`.
Sequence is a local arrival/delivery order, not a server cursor. This version
never emits `SubagentCreated` and never reports complete forwarding.

## Creation evidence

| Observation | SDK result | Why |
| --- | --- | --- |
| `task_started`, `task_type: local_agent` | Associated, first-observed | Task start may resume an existing entity. |
| Assistant/user `agent_id` | Associated or identity-enriched | Fixed message contract equates it to task_id within the same session; first observation does not prove creation. |
| Agent tool name/input, first task ID, spawn depth | No creation claim | These cannot exclude resume. |
| SubagentStart hook | No creation claim; no hook installed | Native hook also fires for resume and teammate messages. |
| Unambiguous native entity creation excluding resume | Created remains conditional / unsupported here | No general creation signal has been established for the pinned protocol. |

## Identity, lineage and runs

`SystemEvent` adds optional `RunID`, `ParentTaskID`, `SpawnDepth` and
`IsBackgrounded` pointers. Existing `TaskType` and raw `Patch` remain unchanged.
Missing/null/value and unknown patch members round-trip through the existing
Raw/Fields codec, including false and zero. Native frames are never rewritten.

`TreeIdentity.AssociationID` is an SDK key scoped by wrapper SessionID and Epoch;
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
start time. A lower run stays Native with `Stale: true` and its original RunID;
it cannot modify current-run metadata, task type, tool aliases or parent.
No inferred terminal state is maintained here; terminal/background accounting
and historical seeds remain ticket 03. Missing run IDs cannot disambiguate
late work, and the SDK does not fabricate globally comparable legacy IDs.

`SubagentStart` was rechecked against the
[official Hooks reference](https://code.claude.com/docs/en/hooks#subagentstart)
on 2026-10-18: it also fires on resume and teammate messages. No extra creation
criterion excluding resume was found. **Created remains conditional and is
never emitted.** No observation hook is installed.

Permission attribution retains the original request ID and sole answer handle.
Root answers, native status (including killed), content and usage remain native;
no text, usage or fees are aggregated.

## Evidence and compatibility boundary

Protocol reference: Python SDK commit
[f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab, initialize forwarding](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/query.py)
and TypeScript SDK 0.3.302 declarations (`SDKAssistantMessage`, `SDKUserMessage`,
`SDKTaskStartedMessage`, `SDKTaskProgressMessage`, `SDKTaskUpdatedMessage`,
`SDKTaskNotificationMessage`, `SDKPartialAssistantMessage`). The fixed declarations and
source are E1 evidence, not proof of installed CLI behavior.

| Capability | SDK / offline evidence | Runtime evidence |
| --- | --- | --- |
| Initialize forwarding option | Implemented; fake process captures false omission, true and automatic enabling | Unverified |
| One owned root stream, child text/thinking/tools, Raw and permission handle | Implemented; original synthetic subprocess fixtures (E2), including race suite | No live CLI/model test |
| Associated before attributed native | Recursive same-session task/tool graph; ancestor ordering and later enrichment (E2 fixtures) | Unverified |
| Created | Conditional; deliberately never emitted | No unambiguous creation evidence |
| Modern recursive graph and legacy tool reconciliation | Implemented; three-level fixtures, bridge evidence, conflicts/cycles (E2) | Unverified |
| Run identity and late native frames | Implemented; new-run, stale metadata, resumed tool alias and missing-run fixtures (E2) | Unverified |
| Scoped UUID deduplication | Implemented; session/source/run/type scope, bounded cache, no-ID/partial/complete fixtures (E2) | Single epoch only; unverified runtime |
| Task lineage codecs and non-agent tasks | Implemented; null/absent/value/typed edits and task-type fixtures (E2) | Unverified |
| Run terminal/background accounting | Deferred to ticket 03; root result already leaves transport open | Not claimed |
| Timed unresolved buffering and full relation/request bounds | Deferred to ticket 04 | Not claimed |

Initialize rejection fails OpenTree. A successful acknowledgment does not prove
the CLI honors forwarding, so the first wrapper always reports
`ObservationState(unverified, forwarding-not-confirmed)`. Missing session or
parent ownership is emitted as unresolved immediately; unknown unowned events
are unscoped. Timed child-before-relation buffering is still ticket 04. This is
not a promise of complete descendants or recovered history.

Dedup uses only native UUIDs on known envelope contracts (assistant/user/system/
result/stream_event), scoped by epoch, native session/source, task/run when
present, type/subtype and parent tool. It does not hash text, message.id, Raw or
time windows. Identical no-ID deltas are both delivered; partial and complete
messages are distinct kinds. Unknown event kinds and sessionless frames are not
deduplicated. No cross-epoch exactly-once claim is made. Frames lacking native
run_id remain legacy/unknown-run. A newly observed run creates a conservative
dedup boundary so a later no-run frame is never suppressed solely by an older
run's UUID history; this assigns no native run to that frame.

The graph/alias/tool-owner/non-agent index is bounded by `TreeOptions.MaxNodes`
(default 4096, ceiling 65536 total entries). `MaxDepth` defaults to 32 (ceiling
256), and `MaxDedupEntries` to 8192 (ceiling 131072). Zero selects defaults;
negative/above-ceiling values fail before startup. Alias entries count against
the graph bound; no stale identity history is silently evicted. Overflow uses
the owned client's terminal ResourceLimit path and emits incomplete plus the
already accepted native frame. Pending wrapper fanout is bounded by the graph;
the underlying bounded event/control transport remains unchanged. A disconnect
reports incomplete, then the original terminal error; explicit Close reports
closed, never child success.

The retained official TS 0.3.302 declaration file SHA256 is
`26f9a0c91ad64e2020f0d3734932302e3cc09685bddf2b2f9ef60839ce278773`.
Its task declarations specify same-task run ordering and stable task identity
across resume (lines 6145–6271); assistant/user task mapping is at lines 3702
and 6475. These are E1 declaration evidence, not live interoperability tests.
All new fixtures in `tree_lineage_test.go` are original synthetic peers through
OpenTree/Send/Receive (E2); no upstream implementation was copied.

Offline validation uses Go 1.26.9 through mise, `CLAUDE_GO_LIVE=0`,
`GOPROXY=off`, `GOSUMDB=off`, `GOTOOLCHAIN=local`, `GOENV=off`, and a temporary
GOCACHE. Commands: `go test -count=1 -timeout 180s ./...`,
`go test -race -count=1 -timeout 300s ./...`, and `go vet ./...`.
All fixtures re-execute the test binary under an isolated HOME; no installed
CLI, real model, authentication change, or account probe is used.
