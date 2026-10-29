# Migration

Replace imports of `github.com/zoumo/codex-sdk-go` with
`github.com/zoumo/agent-sdk-go/codex`, and imports of
`github.com/zoumo/claude-agent-sdk-go` with
`github.com/zoumo/agent-sdk-go/claude`. Apply the same prefix substitution to
`/types`. Update your dependency to the single module
`github.com/zoumo/agent-sdk-go`. Old import paths are not forwarded.

The native source roots moved under `codex/` and `claude/`. Package-local examples
and scripts retain package-relative paths: run their documented commands from the
corresponding package directory. The root `mise.toml` supplies the compiler to both.
Root checks and CI run from the repository root. There is one `go.mod` and `go.sum`.

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

The normal interface is `Open`, followed by `Session.Submit`
and the single `Session.Receive` FIFO. The native SDKs and `types` imports stay
independent. [Session interfaces](../harness/modules/native-session/design.md#interface-原生-session-公开-interface)
and [event delivery](../harness/modules/native-session/design.md#delivery-event-delivery) govern the behavior.

| Previous consumer coordination | Session path |
|---|---|
| Codex start/resume + Subscribe + Requests | Open fixes main ownership; Receive delivers events and actual requests |
| Claude NewClient + Send + callback worker pool | Open + Submit + Receive; process interaction private; application owns decision UI |
| Ancestor-first held events and occurrence/UUID dedup | Native arrival order and duplicates remain; optional parent knowledge follows evidence |

### Input and requests

Codex Input no longer requires ThreadID; Submit retains the native turn/start
response. Claude Submit proves only a complete local write. Neither SDK guesses
scheduling from local Busy state or changes the input target. Replace separate
Events/Requests/callback consumers with Receive values: Event, Request and
Observation. Answer through the original IncomingRequest and distribute UI/log
output in the application. Native parameters and result semantics are in the
[Codex](../codex/README.md) and [Claude](../claude/README.md) guides.

### Native resume and independent history

Load, LoadOptions, LoadState and ReceivedLoad have been removed. Codex retains
Client.ResumeThread and native thread/turn/item query RPCs. Claude retains Open's
ResumeID and exposes independent package-level saved-history queries. Resume does not promise
SDK history replay. Historical data cannot create answer authority.

### Resources and close

Close cleans up interaction and invalidates handles; accepted records remain
drainable. Abort explicitly discards unread records. Done reports termination,
Err retains its first cause, and Close reports cleanup errors separately.

Each Session owns one dynamically growing FIFO, limited by available memory.
History producers are bounded independently; this does not cap total unread
backlog or guarantee crash/OOM delivery. Configure frame, pending call/control,
request identity, membership, parent knowledge, quarantine and history read/index
bounds separately. Removed Claude resource options are PermissionWorkers,
MaxDedupEntries, MaxRuns, MaxCreationEntries and MaxPendingRecords; they no longer
back hidden runtimes. Codex Limits.Subscriptions now bounds concurrent main
Session owners; deprecated EventQueue/RequestQueue/RequestTimeout do not allocate
second consumer queues or impose approval expiration. Parent-depth limits do not suppress confirmed member facts.
Quarantine declares retained count/bytes/age and evicted/expired gaps; later
membership never backfills old records. A failed Open returning no consumer aborts
its unreachable queue.
