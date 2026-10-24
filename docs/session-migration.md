# Migrate native consumers to Session

The normal interface is `Open` or explicit `Load`, followed by `Session.Submit`
and the single `Session.Receive` FIFO. The native SDKs and `types` imports stay
independent. [Session interfaces](../harness/modules/native-session/interfaces.md)
and [load/delivery contracts](../harness/contracts/index.md) govern the behavior.

| Previous consumer coordination | Session path |
|---|---|
| Codex start/resume + Subscribe + Requests | Open/Load fixes main ownership; Receive delivers events and actual requests |
| Claude NewClient + Send + callback worker pool | Open + Submit + Receive; application owns decision UI |
| Independent Client.Load/LoadedThread history lifecycle | Package Load returns Session; independent LoadState plus same Receive FIFO |
| Ancestor-first held events and occurrence/UUID dedup | Native arrival order and duplicates remain; optional parent knowledge follows evidence |
| Separate history completion and live observer ownership | One Session owns the attachment and one FIFO; successful load is not termination |

## Input, requests and native execution

Codex Input carries native turn/start execution options without a caller-chosen
ThreadID. Submission retains TurnStartResponse and native turn ID. Submit does
not infer start/steer/queue from a local Busy flag; native turns remain native.
Claude Input carries text or supported native structured user content. Its fixed
main envelope is not an arbitrary target selector. Written proves local complete
write only, with no invented acceptance receipt or Turn.

Receive discriminates native Event, actual IncomingRequest, SDK observation and
Load boundary. It preserves Raw/Params, unknown native data, source identity and
live/history origin. A pure decoder or historical projection cannot create an
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

## Load and ownership

Codex Load requires a native ThreadID, absolute CWD and matching CodexHome. The
actual resume response ID and both cwd values must match before history enters
the FIFO. Stable persisted main history is supported; active/ephemeral sources,
source mutations, repeated IDs/cursors and partial failure cannot become success.
History keeps original source bytes and missing timestamps, without fabricated
starts/deltas or historical answer authority. Live and history can interleave;
this does not reconstruct their original cross-source timeline.

Claude's fixed CLI 2.1.258 / SDK 0.3.258 baseline has no confirmed no-input native
recovery acknowledgment. Load explicitly fails ErrResumeUnconfirmed, with no
replay, extra prompt, silent Open fallback or ordinary input. Pending actual
requests, Interrupt and Close remain available. Independent offline transcript
projection/integrity checks do not certify successful live Load.

Inspect LoadState independently of Done and consumption. Pending or failed Load
rejects Submit and configuration. Startup cancellation after successful Codex
Load does not stop ongoing Session interaction. Same-connection main identity has
one Session owner. Overlapping ancestor/child ranges share attachment references;
closing one does not detach another, and the final observer releases the native
attachment. Uncertain resume or detach stays fenced. Raw Client.Call remains an
explicit uncoordinated extension and creates no second consumer/answer registry.

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
bounds separately. Parent-depth limits do not suppress confirmed member facts.
Quarantine declares retained count/bytes/age and evicted/expired gaps; later
membership never backfills old records. A Session returned by Load must retain
accepted startup facts for drain; a failed Open returning no consumer aborts
its unreachable queue.

## Evidence and validation limits

Default validation is offline. The sanitized Codex S1/S2 captures retain their
original root/tool/question scope. Claude's checked-in real capture is an
authentication failure. None establishes real Session child/configuration/Load
certification. Gated live tests remain opt-in/skipped. No upstream version,
fixture bytes, license or provenance is upgraded by this migration. Historical
architecture reports describe their original baseline.

An independently fetched exact pushed-revision consumer requires a pushed
revision. Local checks do not certify that gate, and this work does not authorize
push, release, live CLI/model execution or new recordings. Repository-wide final
acceptance remains ticket 14's separate baseline review.
