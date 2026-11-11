# Migration

Replace imports of `github.com/zoumo/codex-sdk-go` with
`github.com/zoumo/agent-sdk-go/codex`, and imports of
`github.com/zoumo/claude-agent-sdk-go` with
`github.com/zoumo/agent-sdk-go/claude`. Apply the same prefix substitution to
`/types`. Update your dependency to the single module
`github.com/zoumo/agent-sdk-go`. Old import paths are not forwarded.

The native source roots moved under `codex/` and `claude/`. Package-local examples
and scripts retain package-relative paths: run their documented commands from the
corresponding package directory. The root `go.mod` declares the shared Go version;
`mise.toml` is an optional developer convenience. Root checks and CI run from the
repository root. There is one `go.mod` and `go.sum`.

The migration merges these complete source histories as parents, preserving commit
objects and original author information:

| Native SDK | Imported source main |
| --- | --- |
| [Codex](https://github.com/zoumo/codex-sdk-go) | `0719539580dce97726aba7ddb56c7c6ae0c24362` |
| [Claude](https://github.com/zoumo/claude-agent-sdk-go) | `72041470958a0d9f2f8e6ff5c0086a66ef193745` |

Historical evidence documents and recording manifests remain source-repository
records. Their original commit URLs, commands, timestamps and hashes are retained;
paths in those historical records are relative to the original native root.
Current validation uses the migrated paths. Native LICENSE and NOTICE files remain
with each package, including upstream protocol and third-party attribution.

## Session

The native SDK interface is `Open`, followed by the native
`codex.Session.Submit` or `claude.Session.Submit` and the single
`Session.Receive` FIFO. The native SDKs and `types` imports stay
independent. [Session interfaces](../harness/modules/native-session/design.md#interface-原生-session-公开-interface)
and [event delivery](../harness/modules/native-session/design.md#delivery-event-delivery) govern the behavior.

| Previous consumer coordination | Session path |
|---|---|
| Codex start/resume + Subscribe + Requests | Open fixes main ownership; Receive delivers events and actual requests |
| Claude NewClient + Send + callback worker pool | Open + native Submit + Receive; process interaction private; application owns decision UI |
| Ancestor-first held events and occurrence/UUID dedup | Native arrival order and duplicates remain; optional parent knowledge follows evidence |

### Unified Agent session configuration

For the optional `agent` layer, replace `OpenOptions.Execution` with
`OpenOptions.Config`, whose type is `SessionConfig`. Set its `Model` and
`Effort` fields when opening or resuming a session; empty fields omit
the option. Replace unified `ModelController` and execution-option setters with
the optional `ConfigController.SetConfig(ctx, ConfigChange{ID, Value})` method.
`ConfigID` supports only `ConfigModel` (`model`) and `ConfigEffort` (`effort`).
Runtime values must be nonempty and are passed literally. `ConfigReceipt`
reports the requested change and dispatch/native acceptance, without a current
or effective configuration report. These unpublished APIs were replaced without
compatibility aliases. Native provider configuration methods remain available.

### Input and requests

Codex Input no longer requires ThreadID; Submit retains the native turn/start
response. Claude Submit proves only a complete local write. Neither SDK guesses
scheduling from local Busy state or changes the input target. Replace separate
Events/Requests/callback consumers with Receive values: Event, Request and
Observation. Answer through the original IncomingRequest and distribute UI/log
output in the application. Native parameters and result semantics are in the
[Codex](../codex/README.md) and [Claude](../claude/README.md) guides.

### Permission modes and approval policies

Claude `SetPermissionMode` now takes `PermissionMode`; use the
`PermissionModeDefault`, `PermissionModeAcceptEdits`,
`PermissionModeBypassPermissions`, `PermissionModePlan`, `PermissionModeDontAsk`
or `PermissionModeAuto` constant. `PermissionUpdate.Mode` and
`SystemEvent.PermissionMode` use the same type.

Codex `AskForApproval.Value` now uses `*types.ApprovalPolicy`; use
`ApprovalPolicyUntrusted`, `ApprovalPolicyOnRequest` or `ApprovalPolicyNever`.
The former `AskForApprovalValue` names remain deprecated aliases. Native
`granular` and unrecognized string approval values still round-trip.

### Native resume and independent history

Load, LoadOptions, LoadState and ReceivedLoad have been removed. The undocumented
SDK history replay projection, its `HistoryOptions` / `HistoryError` types, and
`Event.History` / `HistorySource` are also gone. Use native query parameter types
and Claude `SessionQueryError` instead. Codex retains `Client.ResumeThread`,
`ThreadResumeParams.History`, and native thread/turn/item query RPCs. Claude
retains `Open`'s `ResumeID`, `Event.IsReplay`, and independent package-level
saved-history queries. Resume does not import or project saved content as live
events. Historical data cannot create answer authority.

### Resources and close

Claude unified `Open` now assigns a UUID through official `--session-id` and
returns after initialize succeeds. `Session.ID()` is nonempty and stable before
the first input. Initialize does not echo the ID; later native main messages
are checked against the assignment. A replayed or duplicate init, a different
main-session identity, or an init cwd conflict terminates the Session with
`native_rejected`. Available commands arrive with the later init notification.
No user turn, refresh API or local identity file is needed. Native callers can
opt into the same behavior with `claude.Options.SessionID`, which is mutually
exclusive with `ResumeID`. Resume retains its saved-session preflight and live
identity confirmation.

Close cleans up interaction and invalidates handles; accepted records remain
drainable. Abort explicitly discards unread records. Done reports termination,
Err retains its first cause, and Close reports cleanup errors separately.

Each Session owns one dynamically growing FIFO, limited by available memory.
Saved-history readers have their own read/index bounds and never feed the Session
FIFO. This does not cap total unread backlog or guarantee crash/OOM delivery.
Configure frame, pending call/control, request identity, membership, parent
knowledge, quarantine and saved-history query bounds separately. Removed Claude resource options are PermissionWorkers,
MaxDedupEntries, MaxRuns, MaxCreationEntries and MaxPendingRecords; they no longer
back hidden runtimes. Codex Limits.Subscriptions now bounds concurrent main
Session owners; deprecated EventQueue/RequestQueue/RequestTimeout do not allocate
second consumer queues or impose approval expiration. Parent-depth limits do not suppress confirmed member facts.
Quarantine declares retained count/bytes/age and evicted/expired gaps; later
membership never backfills old records. A failed Open returning no consumer aborts
its unreachable queue.
