# Migrate native consumers to Session

The normal interface is `Open`, followed by `Session.Submit`
and the single `Session.Receive` FIFO. The native SDKs and `types` imports stay
independent. [Session interfaces](../harness/modules/native-session/interfaces.md)
and [delivery contracts](../harness/contracts/index.md) govern the behavior.

| Previous consumer coordination | Session path |
|---|---|
| Codex start/resume + Subscribe + Requests | Open fixes main ownership; Receive delivers events and actual requests |
| Claude NewClient + Send + callback worker pool | Open + Submit + Receive; process interaction private; application owns decision UI |
| Ancestor-first held events and occurrence/UUID dedup | Native arrival order and duplicates remain; optional parent knowledge follows evidence |

## Input, requests and native execution

Codex Input carries native turn/start execution options without a caller-chosen
ThreadID. Submission retains TurnStartResponse and native turn ID. Submit does
not infer start/steer/queue from a local Busy flag; native turns remain native.
Claude Input carries text or supported native structured user content. Its fixed
main envelope is not an arbitrary target selector. Written proves local complete
write only, with no invented acceptance receipt or Turn.

Receive discriminates native Event, actual IncomingRequest and SDK observation
evidence. It preserves Raw/Params, unknown native data, source identity and
live origin. A pure decoder or historical projection cannot create an
answer handle. Concurrent receivers compete; application fan-out is explicit.

Use the actual request's Approve/Deny/Answer or retained native Handle. Codex
question answers retain question IDs and arrays; Claude answers use full question
text and retain original questions/input. Native scope, permission updates and
deny interrupt remain native options. RPC Reject is separate from a user denial.
At most one effective answer can dispatch. Cancel/resolution/termination invalidates
the original authority. Unknown write outcome never restores retry permission.
Session does not impose a default policy or approval timeout.

The recorder/replay runner converts known native turn options explicitly. Unknown
turn execution extensions fail with ErrInvalidInput before submission; they are
not silently dropped. Real capture bytes, manifests, IDs and evidence scopes
remain unchanged. Synthetic example output changes to show isolation and gaps.

## Native resume and independent history

Load, LoadOptions, LoadState and ReceivedLoad have been removed. Codex retains
Client.ResumeThread and native thread/turn/item query RPCs. Claude retains Open's
ResumeID and the internal bounded transcript parser. Resume does not promise
SDK history replay. Historical data cannot create answer authority.

## Configuration, resources and close

Creation options remain provider-native. Dynamic SetModel/SetThinking preserve
requested payload and native acceptance separately from observed settings; they
do not wait for idle, restart the Session or infer child inheritance. ListSubagents
returns native records plus coverage/error, with no resume, execution or membership
side effects. A partial/unknown scan is not a complete empty result.

Receive cancellation affects one wait. Explicit Interrupt retains native selectors
(Codex TurnID, no invented Claude execution ID). Its ack does not prove rollback
or recursive termination. Close invalidates owned authority and cleans resources
without waiting for consumers. Drain accepted records before EOF/terminal cause,
or explicitly Abort to abandon unread backlog. Close's cleanup error must not
replace the first interaction cause.

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

## Evidence and validation limits

Default validation is offline. The sanitized Codex S1/S2 captures retain their
original root/tool/question scope. Claude's checked-in real capture is an
authentication failure. None establishes real Session child/configuration
certification. Gated live tests remain opt-in/skipped. No upstream version,
fixture bytes, license or provenance is upgraded by this migration. Historical
architecture reports describe their original baseline.

An independently fetched exact pushed-revision consumer requires a pushed
revision. Local checks do not certify that gate, and this work does not authorize
push, release, live CLI/model execution or new recordings. Repository-wide final
acceptance remains ticket 14's separate baseline review.
