# Compatibility

This is the canonical compatibility record for this SDK. The complete per-method
matrix is [capabilities.json](protocol/capabilities.json); its records are part of
this document, not a separate supported-version claim.

## Protocol target

- Official repository: [openai/codex](https://github.com/openai/codex).
- Exact revision: [`092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`](https://github.com/openai/codex/tree/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d).
- Workspace version label: `0.162.1`, verified in the pinned `codex-rs/Cargo.toml`.
- Pinned experimental export archive SHA-256:
  `41d34da4227eadccf971272b9a02dfc6679e23a9992cce1d265ac01a20ab38e3`.
- Extracted experimental aggregate schema SHA-256:
  `e4e7f0c7d3fd77c48cd8619cad2bf2b315d860ab006ad5a7ebe44f6e32e666c1`.
- Selected SDK schema snapshot SHA-256:
  `4f129fdc2bbaebf4b1752dd8f7bfb22180849736d0c064a7ac0052b111e0733a`.
- This target is not a version range. Other revisions, including other releases
  carrying similar version labels, have unknown compatibility.
- Original source hashes, extraction details, and notices are recorded in
  [protocol provenance](protocol/README.md) and [sources.json](protocol/sources.json).

## Current implementation

The implemented slices own one explicitly selected local child process or connect
to one caller-selected remote WS/WSS endpoint. They expose transport-correct
framing, the `initialize` / `initialized` handshake, typed `thread/start`,
`thread/read`, `thread/resume`, `thread/unsubscribe`, `turn/start`, and
`turn/interrupt`, raw `Call`, bounded local subscriptions, one-shot reverse-request
answers, and bounded cleanup. Protocol models cover the complete selected
experimental-schema definition closure, with omitted/null/value handling and
unknown-field and unknown-union preservation. The bindings are loss-preserving
models, not full JSON Schema validators: not every native minimum/maximum or
`additionalProperties` constraint is enforced, and unknown enum/union/extensions
can be retained. A representable value is not a promise that a server accepts it.

Thread/turn methods expose their complete selected parameter/result shapes.
Interactive approvals and user input can now be answered explicitly. Other
capability-dependent workflows (attestation, elicitation, dynamic tools, accounts)
remain caller-managed through raw extensions and are not certified by typed
request support. Do not infer their support from reachability or generated fields.
`turn/interrupt` exposes the native request and acknowledgement; it does not prove
execution completion.

`ResumeThread` strictly checks the supplied native thread ID against the returned
ID, retaining correlation and responded-error evidence on mismatch. It never
falls back to `thread/start`, replays a failed request, or reconnects. Native
history/path selectors can deliberately return another identity; those native
workflows are outside this typed wrapper's strict-ID contract even though their
full parameter/result schemas are exposed. The raw escape hatch does not confer
support. `UnsubscribeThread` sends one native request and returns its status; it
is independent of local subscription cancellation and never sends an interrupt.
Fake-peer connection tests establish explicit reinitialization and unchanged
native-ID handling. Separate Mac live evidence below establishes durable history
and explicit fresh-connection resume only for its exact scenarios and CLI version.

Subscriptions preserve all valid notifications as raw method and parameter JSON.
Seven methods also have typed payloads: `turn/started`, `turn/completed`,
`item/started`, `item/completed`, `item/agentMessage/delta`, `error`, and
`serverRequest/resolved`. Malformed known payloads retain their raw bytes with `DecodeError`. A subscriber's full queue
terminates only that stream with an inspectable loss cause outside its queue.
There is no filtering, replay, native-ID replacement, or event-driven turn state.
The remaining notification inventory rows are partial raw receive paths, with no
per-method typed/workflow evidence unless explicitly recorded. Envelope emission
timestamps are not exposed.

Reverse requests expose immutable method/parameters/exact tagged IDs and opaque
one-shot handles. Modern command/file/permissions approvals, user input, and two
legacy approvals have complete typed payload/results; every other method remains
raw. Legacy approval schemas are modeled, but the pinned modern production
handlers do not establish those legacy workflows as live-supported. Legacy
conversationId has the native ThreadId type and is used for resolution correlation
if a matching notification is received. Queue/active-capacity exhaustion explicitly
terminates the connection without
inventing an approval or denial. Local cancel/expiry does not cancel server work.
Native resolution matches both requestId and thread identity; an answer already
admitted wins over later local cancellation, expiry, or resolution.

Remote clients use an explicit bearer header, verified TLS, bounded complete JSON
text messages, and the same initialization, methods, subscriptions, and answer
handles. They reject URL credentials/query/fragment, redirects, and disabled TLS
verification. Non-loopback plaintext WS requires explicit opt-in. No reconnect,
replay, token lookup/refresh, or remote process ownership is added. The official
server drops unsupported inbound binary messages; this client instead fails its
connection on received binary data, making the loss observable. Remote errors
redact a peer-reflected bearer from diagnostic message/data/raw fields, including
supported encoded echoes. This deliberate exception to raw error preservation
replaces only affected diagnostic lexical values; unaffected bytes, duplicate
members, native error codes, and correlation stay unchanged. Successful results and notification/request
payloads remain caller-sensitive data, not a universal redaction facility.

There is no Unix-socket, remote-control, embedded-runtime, turn runner,
or login management in these slices. A generated type or a successful raw `Call`
does not change an endpoint's support status. Unknown extensions are caller-managed and uncertified.

The inventory enumerates **173 client requests, 11 server requests, 86 server
notifications, and 1 client notification**, including experimental, legacy, and
internal registry entries. It also records 24 framing, transport, and capability
controls. It covers the app-server surface, not every Codex CLI command, model API,
or MCP endpoint. The stable aggregate schema has 105/10/84/1 entries and is not a
complete inventory. Even the experimental aggregate omits three legacy requests
and two internal notifications; the exact Rust registry supplies those entries.

### Status and evidence are different

- **Supported**: the stated SDK behavior is implemented. This does not imply a
  real server was tested or every capability-dependent workflow is available.
- **Partial**: only the explicit subset in the record is implemented.
- **Unsupported**: no supported SDK workflow; raw transport reachability does not
  confer support.

Each inventory row keeps unit, fake-peer/mock, race, and live evidence separately.
Ticket 05 passed final working-tree normal unit/mock, five-repeat normal, full
race, vet, dependency, generation, and inventory checks; exact revision/scope is
recorded in [evidence.json](protocol/evidence.json). Implementation status alone is
not a test pass. That historical ticket did not run hosted CI. Current Mac
evidence and exact subsequent offline CI readback are recorded in the final
report below.
Empty evidence arrays mean no result has been recorded for that row. Evidence
records and reproducible commands belong in [evidence.json](protocol/evidence.json).
Official tests inspected as design references are described in
[testing patterns](protocol/testing-patterns.md); their existence is not test
execution evidence for this SDK.

## Wire and lifecycle boundaries

The pinned protocol deliberately omits `"jsonrpc":"2.0"`. Request identifiers
are either strings or signed 64-bit integers; integer `1` and string `"1"` are
different identities. The SDK does not coerce IDs through floating point.

A context cancels local waiting. It does not send a fictional generic cancellation
notification and does not establish that the server stopped. `turn/interrupt`
and `userVerification/cancel` are separate native methods, listed individually.
`serverRequest/resolved` is a server notification sent after answers or pending
request resolution during interruption; it is not a success acknowledgment.
Errors after possible dispatch
carry outcome-unknown evidence and are never automatically replayed.

Closing a stdio client closes its owned child connection, then bounds waiting
and forceful termination. Closing a remote client closes only its WebSocket
connection. Neither sends the managed daemon shutdown protocol or takes ownership
of an existing service. Arbitrary descendant process cleanup
is not guaranteed. See the exported `Limits` and README for finite defaults.

## Live validation

The [final Mac report](evidence/mac-live-summary.md) consolidates real CLI
**0.162.1** on Darwin/arm64, Go 1.26.9, against the SDK's then-current **0.160.1**
protocol target. That historical run does not validate the refreshed bindings or
managed subagent API. Exact tested bases and delivery commits are listed separately.
Stdio and authenticated loopback WS passed two-turn events, durable history,
fresh-connection same-ID resume and bounded lifecycle assertions. Stdio user
input passed BLUE answer → exact native resolution → BLUE result → completion.

Command/file/permissions approvals were **untriggered**. A synthetic command
effect passed independently. The interrupt command guard did not match, so no
InterruptTurn was sent and interruption is **untriggered**, not passed. Public
WSS, JWT issuer/signed bearer, proxies and cross-host operation are **not covered**.
Mock WSS and schema/typed representation do not replace live workflow evidence.
All 9/9 authorized model attempts were used; this summary adds zero model calls.

The [historical Linux attempt](evidence/final-live.md#historical-linux-attempt)
at SDK `a83ab64915e0bb7e7290d74ca3ac0204d11ec61c` / CLI 0.159.2 remains
blocked by SQLite startup before initialization completed, with zero model turns.
No new live pass, version range or complete-protocol certification is claimed.

## Managed direct-child observation (ticket 01)

The current [per-capability contract](subagent-compatibility.md) covers the full
five-ticket recursive API. The sections below preserve its implementation and
evidence history; no subagent live validation is claimed.

`Client.Subscribe` adds a local, bounded stream for a root and direct children
identified by explicit native evidence. Successful live spawnAgent collab and
live decoded V2 item/completed SubAgentActivity kind started confirm SDK-derived
Created; successful non-spawn collab evidence is Associated. V2 derives parent
from notification threadId and child from activity.agentThreadId, retaining the
actual activity in TreeEvidence.Activity without requiring thread/started.
SDK wrapper names are outside `types`; native Event and protocol generation
remain unchanged. Native envelopes and parameter bytes are retained independently
for each observer. Request handles remain exclusively on `Client.Requests()`.

Ticket 02 extends this API to recursive relations and bounded early-event
routing. Native Thread parent/source, completed collab and V2 Started evidence establish
edges; no path inference or child RPC is used. Conflicts retain the original
parent and emit both accepted and rejected evidence; cycles emit diagnostics. Related ancestors
precede buffered descendants. Associated can later receive one Created
confirmation with its original supporting native identity and the same relation
key. Entity identity remains distinct from each turn.

The initial derived state remains incomplete due to best-effort auto-attach and
unverified runtime coverage. Unknown sources are buffered within configurable
count/bytes/age bounds, then explicitly delivered as unresolved with an incomplete
diagnostic. They are never replayed after later association. Source-less events
remain unscoped. Deduplication only covers successfully decoded known-type terminal
item/completed identities scoped to thread/turn/method/item and this connection;
cache eviction permits old replays. Identical deltas, unknown occurrence IDs and
partial versus complete items are preserved. Graph, depth, queue, pending storage
and identity cache are bounded independently.

Ticket 03 adds explicit native Thread snapshots through `TreeOptions.History`.
Seeded parent/source, successful historical collab and V2 Started relations are history
Associated only; they never create entities or replay notifications. Their evidence
has history provenance and no notification method. Seeded entities cannot later
be reclassified as newly created. New live children can still be Created. Snapshot
count/bytes use MaxNodes/PendingBytes, with ordinary graph/depth/queue bounds.
No current work state is inferred from a snapshot. History seeds explicitly emit
an incomplete recovery-gap record; cold reconnect attachment to old threads and
missed/lagged auto-attach remain unverified. Unknown versions remain incomplete,
with no speculative resume or read/list fallback.

Parent idle and child active can coexist. Root turn completion, child activity
completion/interruption and entity lifetime are separate. Closing/canceling one
observation leaves native and other tree observations intact, including another
root sharing a child source. Unexpected disconnect appends a disconnected /
permanent-gap record using a reserved terminal queue slot, preserves accepted
records and the connection failure in Err, and never synthesizes work completion.
Explicit new Clients have fresh epochs and occurrence caches; there is no seamless
recovery or exactly-once claim. No live/model validation was run.

Pinned lifecycle source: [TurnComplete handling](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/bespoke_event_handling.rs#L186-L203)
awaits note_turn_completed before handle_turn_complete; [thread status](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/thread_status.rs#L148-L158)
clears only that thread, and [publication](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/thread_status.rs#L223-L248)
uses the shared outbound sender. Idle change (when applicable) precedes that
thread's turn/completed enqueue; child status is not aggregated. Waiting for
wait_agent does not complete the parent turn; completing the parent does not
invoke close_agent on children. Native IDs/notifications are retained. These
source facts do not promise cross-thread order or successful result arrival first.

| Capability | Native availability / source evidence (E1) | SDK implementation | Offline evidence (E2) | Live evidence |
| --- | --- | --- | --- | --- |
| New-thread notification routing | Pinned [server lib.rs lines 1291–1319](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/lib.rs#L1291-L1319) auto-attaches initialized connections; lagged notifications skip resync | Existing connection only; zero child RPCs/connections | Synthetic stdio/WS/WSS auto-attach fixture | Not run |
| Independent creation / association | V2 [successful spawn / Started](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/tools/handlers/multi_agents_v2/spawn.rs#L185-L239) and pinned [collab item schema](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/v2/item.rs#L1131) | Derived Created or Associated precedes child native events | V1 success/failure/resume/repeated receivers; TestTreeV2CreatedWithoutThreadStarted, TestTreeV2GraphOrderingAndConfirmation, TestTreeV2HistorySuppressesCreation, TestTreeV2NonCreationEvidence | Not run |
| Native compatibility and isolation | Existing raw/typed notification transport | Recursive membership and bounded unresolved routing, per-observer buffers, exact envelope bytes | Early delivery before root response, unknown fields/methods/null, malformed typed payloads, unchanged broadcast | Not run |
| History and disconnect boundaries | Native snapshots expose lineage; new-thread attach is distinct from cold recovery | History-only Associated; disconnected/permanent-gap; explicit new epochs, zero recovery RPCs | Seed/live distinction, repeated IDs across epochs, full-queue EOF, close/cancel shared-source race | Not run |
| Bounded local observation | Existing SDK subscription limits | Independent queue, shared admission cap, graph-node bound, idempotent local close | Slow observer isolation, node/admission bounds, concurrent close; Go race suite | Not run |

Offline commands and fixture scope are recorded under `subagent01-*`,
`subagent02-*` and `subagent03-*` in
[protocol/evidence.json](protocol/evidence.json). Real CLI/model tests and auth
changes were excluded. Existing live evidence elsewhere in this document does
not establish subagent runtime coverage.

Ticket 02 validation and API defaults: [recursive ordering evidence](evidence/subagent-02.md).

Ticket 03 lifecycle and recovery limits: [offline evidence](evidence/subagent-03.md).

Ticket 04 admits each live notification fanout atomically, including related
associations and resolved pending records. Expiry diagnostics and unresolved native
records are also admitted together. Overflow preserves previously accepted records
and terminates only that observer; Done/Err remain independent of queue capacity.
Control replies and original Requests handles continue on their existing paths.
Observation cancellation/Close does not invalidate a request; request cancellation,
expiry, native resolution and Client.Close retain the original handle semantics.
Repeated request IDs on new Clients cannot reuse old answer authority. Parent
final text, child deltas/thinking and each native usage payload keep their source
and exact Raw; UsageScope stays unknown and no tree total is produced.

Ticket 04 bounds and original request authority: [offline evidence](evidence/subagent-04.md).

Final per-capability contract, including raw-only representation, E1/E2 evidence
and all five tickets: [managed subagent matrix](subagent-compatibility.md).
Runnable model-free scenario: [offline tree example](../examples/observe-tree-offline/README.md).
Ticket 05 publication and final validation: [evidence](evidence/subagent-05.md).
ACP subagent proposals remain drafts; no ACP wire or stable-standard compliance
is claimed. No subagent E3/E4 test was run.

Current installed CLI **0.162.1** is the sole active target; the previous
0.160.1 pin is historical provenance, not a maintained compatibility mode.
See the [current-version follow-up](evidence/subagent-05-current.md) for the
actual schema changes, local binary identity and current E1/E2 validation.

[事件时序](event-sequences.md) explains the current V1/V2 spawn and wait modes.
V2 live decoded completed Started proves creation; other activity kinds and
item/started prove neither creation nor parentage. Historical Started yields
Associated only. [Correction evidence](evidence/subagent-v2-created.md) records
the fixed-source reasoning and public regression coverage.
