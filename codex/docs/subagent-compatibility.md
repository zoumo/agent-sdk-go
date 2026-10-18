# Managed subagent compatibility contract

Protocol target: Codex **0.162.1**, official revision
`092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`. `Client.Subscribe` observes one
existing connection. Native server auto-attach to newly created threads is
**best-effort**. Every observation begins incomplete; source inspection and a
synthetic peer cannot prove runtime coverage. No subagent live pass or supported
server-version range is claimed. The earlier unrelated live reports do not
validate this API. [README](../README.md#observe-a-root-and-its-live-descendants)
and this contract describe the same boundaries.

E1 means inspected pinned source/types; E2 means executed synthetic offline
contracts, with race results recorded separately. E3 (real provider with mock
backend) and E4 (real model) were **not run** for any row. These are evidence
categories, not cumulative certifications. Test names below refer to the public
API fixtures in [tree contract](../tree_contract_test.go),
[nested](../tree_nested_test.go), [lifecycle](../tree_lifecycle_test.go), and
[bounds/requests](../tree_bounds_requests_test.go) tests. Historical revisions and results remain in [the evidence manifest](protocol/evidence.json);
current commands are in [testing and CI](testing.md). Earlier records retain their historical schema pin.

Read [事件时序](event-sequences.md) for the current native/model/RPC ordering.

## Capability matrix

Native availability does not imply that every server emits the event, enables
the capability, or grants access to it. Typed availability does not certify the
workflow. E1 source keys link to immutable official code below.

| Capability | Native availability / E1 | SDK implementation | Executed E2 contract | Limits / unverified runtime |
| --- | --- | --- | --- | --- |
| Current creation | C/V: V1 completed spawnAgent collab; V2 live decoded item/completed SubAgentActivity kind started | SDK-derived SubagentCreated once per live entity; actual activity or collab evidence and thread/turn/item retained | TestTreeLiveChildStream; TestTreeV2CreatedWithoutThreadStarted; TestTreeV2NonCreationEvidence | V2 needs no thread/started; other activity kinds and item/started prove no creation/relation. Auto-attach best-effort; E3/E4 not run |
| Association | T/C/V: native parent, thread_spawn source, successful completed collab and V2 completed Started evidence | Associated precedes child Native; later live spawn confirms same child thread ID once | TestTreeNestedOrderingAndIdentity; TestTreeV2GraphOrderingAndConfirmation | First accepted parent retained; conflicts/cycles diagnostic; agentPath is display only |
| Text / thinking | N/I: native message and reasoning notifications/items | Raw/Params retained at native source; message delta and item payloads typed where implemented | TestTreeTextThinkingAndUsageStayAtNativeSource; TestTreeLiveChildStream | Reasoning deltas can remain raw-only; no guarantee of provider thinking emission |
| Tools / extensions | C/I/N: native tool items, unknown methods and fields | Original events, native IDs, UnknownFields/unknown unions, null/absence and DecodeError retained | TestTreeLiveChildStream; TestTreeNestedOrderingAndIdentity | Unknown methods have no invented typed payload; malformed lineage cannot establish membership |
| Nested stream / attach | A/T: new-thread attach and parent/source lineage | Recursive existing-connection routing; zero child read/list/resume/start/subscribe RPC or added connection | TestTreeNestedOrderingAndIdentity; transport traces in tree contracts | New-thread best-effort attach does not imply cold-thread coverage; no generic cross-version fallback |
| Deduplication | I: terminal item identity, no general occurrence cursor | Relationships once; known decoded item/completed keyed by connection/thread/turn/method/item | TestTreeNestedOrderingAndIdentity; TestTreeDepthAndDedupBounds | No-ID deltas and unknown items preserved; FIFO cache eviction allows old replay; no cross-connection exactly-once |
| Parent / child status | L: per-thread status and native turn/activity boundaries | Independent raw status and typed turn/item events; parent completion leaves observation open | TestTreeParentIdleChildActive | Idle differs from turn/completed; neither means all children finished; activity end does not delete agent |
| Terminal / future work | L/I: native completed/interrupted states | Forwarded unchanged with native turn IDs; future run of same entity remains observable | TestTreeParentIdleChildActive; TestTreeNestedOrderingAndIdentity | Silence, waiting, timeout, cancel, EOF or parent final answer never imply child success |
| History / explicit recovery | T/C/V: native Thread snapshots expose lineage | Caller-supplied History seeds Associated only, no notification replay, no inferred current state | TestTreeDisconnectHistoryEpoch; TestTreeDisconnectFullQueueAndSeedBounds; TestTreeV2HistorySuppressesCreation | Historical spawn never Created, even if repeated live; seed emits history-recovery-gap; no automatic resume |
| Disconnect / reconnect | A: lag can skip resync, no verified replay cursor | Disconnected/permanent-gap reserved record, original Err; explicit new Client uses fresh local observation state | TestTreeDisconnectHistoryEpoch; TestTreeDisconnectFullQueueAndSeedBounds | No reconnect loop, replay, guaranteed cold attach, gap filling or seamless recovery |
| Unresolved / absent source | N/T: source can be missing or relation unknown | Bounded waiting then an unresolved-source state and the original native event; absent source proves no membership | TestTreePendingLimitsAndNoReplay; TestTreePendingDeadlineFanoutIsAtomic | Unresolved may belong to another root; later relation never replays delivered Native |
| Backpressure / bounds | Existing Go bounded transport; no server completeness guarantee | Atomic per-notification fanout, finite queue/graph/depth/pending/cache, observer-local failure | TestTreeFanoutAdmissionIsAtomic; TestTreeBoundsAndLocalClose; TestTreeAdmissionAndNodeBound | Overflow terminates observer; previously admitted records remain readable; control routing does not wait on consumer |
| Permissions / user questions | R: native reverse requests and resolution | Client.Requests original one-shot handle is sole answer authority; observation never auto-approves | TestTreeFullQueuePreservesOriginalRequestAuthority; TestTreeRequestTerminalAndEpochIsolation | Observation Close/cancel leaves handle valid; request cancel/expiry/resolution/connection loss retain original semantics; stale connection cannot answer a new request with the same ID |
| Parent final answer | I: source-scoped agent message item | Exact parent answer retained; child text/thinking not merged, no synthetic parent result | TestTreeTextThinkingAndUsageStayAtNativeSource | Application selects native source/turn/item; parent answer is not global child completion |
| Usage | N: native token-usage payload | Exact Raw and source retained; No aggregated usage total | TestTreeTextThinkingAndUsageStayAtNativeSource | No adding parent aggregate to child detail or cumulative snapshots to deltas; no cost inference |
| Observation close / ownership | Existing local Subscription vs explicit native unsubscribe | Idempotent bounded local Close/cancel, independent observers and original handles | TestTreeCloseCancelSharedSource; TestTreeObservationCancellationDoesNotCancelRequest | Does not interrupt/unsubscribe provider work; Client.Close retains transport ownership semantics |
| Protocol / version contract | N: fixed Codex native registry | Separate SDK convenience Data; Subscribe delivers native Event directly | TestTreeLiveChildStream; inventory/generation gates | ACP subagent RFD is draft design context only, not stable standard, runtime dependency or implemented ACP wire; older/unknown servers unverified |

## Pinned E1 sources and representation

- **A**: [new-thread attach / lag without resync](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/lib.rs#L1291-L1319)
  and [best-effort per-connection listener](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/thread_processor.rs#L3607-L3639).
- **C/I**: [collab tool and native item definitions](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/v2/item.rs).
- **V**: [V2 successful spawn / Started](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/tools/handlers/multi_agents_v2/spawn.rs#L185-L239),
  [canonical activity pair](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/tools/handlers/multi_agents_v2.rs#L45-L54),
  and [official completed Started child-ID test, no collab](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/tests/suite/v2/turn_start.rs#L4493-L4593).
- **T**: [native Thread definitions](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/v2/thread_data.rs#L204-L271).
- **N/R**: [notification / reverse-request registry](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/common.rs).
- **L**: [TurnComplete handling](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/bespoke_event_handling.rs#L186-L203)
  and [per-thread idle publication](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/thread_status.rs#L148-L158).

Native thread/started, thread/status/changed, reasoning deltas and token usage
are currently **raw-only notifications**. The native Thread and item objects
already occur in the generated closure; relation extraction reads only their
native fields and never inserts SDK fields into protocol. The 33 roots, 154 definitions, 399 types and 750 synthetic
round-trip cases remain as documented in [generation provenance](protocol/README.md).
The machine inventory records method-level raw/typed scope separately from the
internal descendant management and its E2 evidence. Exact Raw is the envelope without the
transport delimiter; typed re-encoding can normalize whitespace/key order.

Applications may record local delivery order and connection identity; these are not durable provider cursors. Native Events have no SDK sequence or epoch fields. Thread, turn and item identity
are kept distinct; no task/run identity is invented. Usage scope remains unknown.
Each subscription retains independently owned buffers and its own bounds, as detailed in
[README bounds](../README.md#observe-a-root-and-its-live-descendants). Limits constrain observation, not work time.

## Run without a model

The [offline example](../examples/observe-subagents-offline/README.md) starts only a
synthetic copy of itself, exercises root → child → grandchild, history Associated,
unresolved-source diagnostics and local close, and checks a deterministic transcript. Use:

```sh
GOPROXY=off GOSUMDB=off mise exec -- go run ./examples/observe-subagents-offline
```

## Current 0.162.1 fields

The active target is one pinned official revision, not a compatibility range.
SubAgentActivity model/reasoningEffort, Turn.rootTurnId, and
TurnStartParams.parentTurnId/rootTurnId are typed and retain omission/null/value.
Turn-chain IDs stay in their native payloads; they are not renamed to agent IDs
or used to infer thread membership. The new partial_answer phase is retained
without becoming final completion. Current CodexErrorInfo admits raw string/object
fallbacks; arbitrary objects remain Unknown when they cannot decode as one known
variant. The current schema also includes reviewTarget and additional_tools,
which are represented without certifying a new workflow.

TestTreeCurrentVersionMetadata exercises these fields over synthetic stdio/WS/WSS;
TestCurrentOpenErrorInfo verifies the current raw fallback. Existing tree tests
validate the managed subscription behavior against the refreshed generated closure.
No old-version negotiation or compatibility branch is maintained.

Current V1/V2 are feature modes in 0.162.1, not older-version compatibility.
V2 spawn tracks its collab item in analytics and emits SubAgentActivity Started
to the execution stream after successful child creation and initial-input admission.
A live decoded item/completed Started proves creation and a direct edge from the
notification threadId (parent) to activity.agentThreadId (child), without
thread/started. types.SubagentEvidence.Activity retains the native item, including unknown
fields; Tool and ReceiverThreadIDs stay collab-only. Interacted, Interrupted,
Completed and item/started establish no edge. Historical Started seeds Associated
only. See the [event-sequence document](event-sequences.md) for model-tool results, wait semantics and remaining gaps.


## Shared relationship storage

Each subscription now uses the bounded internal graph described in the
[shared relationship contract](../../docs/lineage.md). `MaxNodes` includes the
subscribed root and all confirmed-edge endpoints, including a parent that has not
itself been associated with the root. The independent graph relation bound is
`MaxNodes - 1`. Conflicts, cycles and limits reject the candidate atomically.
Native evidence recognition, creation claims, pending routing, deduplication,
raw data and request answer authority stay in this package.
