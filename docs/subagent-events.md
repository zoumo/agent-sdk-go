# Subagent forwarding (ticket 01)

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

| Observation | Result in ticket 01 | Why |
| --- | --- | --- |
| `task_started`, `task_type: local_agent` | Associated, first-observed | Task start may resume an existing entity. |
| Assistant/user `agent_id` | Associated or identity-enriched | Fixed message contract equates it to task_id within the same session; first observation does not prove creation. |
| Agent tool name/input, first task ID, spawn depth | No creation claim | These cannot exclude resume. |
| SubagentStart hook | No creation claim; no hook installed | Native hook also fires for resume and teammate messages. |
| Unambiguous native entity creation excluding resume | Created remains conditional / unsupported here | No general creation signal has been established for the pinned protocol. |

A root assistant tool observed in the same native session can prove ownership
of the parent tool reference for this single layer. Otherwise Parent remains
unknown and Scope is unresolved. Permission attribution reads existing native
permission metadata and keeps the original request ID and sole handle.
Non-local-agent tasks are not promoted to subagents.

## Evidence and compatibility boundary

Protocol reference: Python SDK commit
[f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab, initialize forwarding](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/query.py)
and TypeScript SDK 0.3.302 declarations (`SDKAssistantMessage`, `SDKUserMessage`,
`SDKTaskStartedMessage`, `SDKToolProgressMessage`). The fixed declarations and
source are E1 evidence, not proof of installed CLI behavior.

| Capability | SDK / offline evidence | Runtime evidence |
| --- | --- | --- |
| Initialize forwarding option | Implemented; fake process captures false omission, true and automatic enabling | Unverified |
| One owned root stream, child text/thinking/tools, Raw and permission handle | Implemented; original synthetic subprocess fixtures (E2), including race suite | No live CLI/model test |
| Associated before attributed native | Single layer with explicit session and agent/task evidence | Unverified |
| Created | Conditional; deliberately never emitted | No unambiguous creation evidence |
| Recursive graph, legacy tool-only node reconciliation, run boundaries, provider-ID deduplication | Deferred to ticket 02; native data retained | Not claimed |
| Run terminal/background accounting | Deferred to ticket 03; root result already leaves transport open | Not claimed |
| Timed unresolved buffering and full relation/request bounds | Deferred to ticket 04 | Not claimed |

Initialize rejection fails OpenTree. A successful acknowledgment does not prove
the CLI honors forwarding, so the first wrapper always reports
`ObservationState(unverified, forwarding-not-confirmed)`. No fallback execution
or subscription attempts occur. Missing session or parent ownership is emitted
as unresolved immediately in this first ticket; unknown unowned events are
unscoped. This is not a promise of complete descendants or recovered history.
There is no native-event deduplication: equal no-ID deltas are both delivered.

The association/tool-owner index is bounded by `TreeOptions.MaxNodes` (default
4096, maximum 65536 total entries). Overflow terminates the owned client using
its existing ResourceLimit path and emits an incomplete observation plus the
already accepted native frame. Pending wrapper storage is at most two records;
the underlying bounded event/control transport remains unchanged. A disconnect
reports incomplete, then the original terminal error; explicit Close reports
closed, never child success. No text, usage or fees are aggregated.

Offline validation uses Go 1.26.9 through mise, `CLAUDE_GO_LIVE=0`,
`GOPROXY=off`, `GOSUMDB=off`, `GOTOOLCHAIN=local`, `GOENV=off`, and a temporary
GOCACHE. Commands: `go test -count=1 -timeout 180s ./...`,
`go test -race -count=1 -timeout 300s ./...`, and `go vet ./...`.
All fixtures re-execute the test binary under an isolated HOME; no installed
CLI, real model, authentication change, or account probe is used.
