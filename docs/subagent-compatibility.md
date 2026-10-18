# Managed subagent compatibility contract

Protocol target: Codex **0.160.1**, official revision
`d27764b82f7118f674371e6d6e76271d9d606edb`. `Client.SubscribeTree` observes one
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
[bounds/requests](../tree_bounds_requests_test.go) tests. Commands, revision
hashes and results are in [ticket 05 evidence](evidence/subagent-05.md) and
[the evidence manifest](protocol/evidence.json), records `subagent05-*`.

## Capability matrix

Native availability does not imply that every server emits the event, enables
the capability, or grants access to it. Typed availability does not certify the
workflow. E1 source keys link to immutable official code below.

| Capability | Native availability / E1 | SDK implementation | Executed E2 contract | Limits / unverified runtime |
| --- | --- | --- | --- | --- |
| Current creation | C: completed collab item, spawnAgent and receiver IDs | SDK-derived SubagentCreated once per live entity, supporting thread/turn/item evidence retained | TestTreeLiveChildStream; TestTreeFailedSpawnAndResumeAreNotCreated | Started activity, failed spawn and non-spawn calls cannot prove creation; E3/E4 not run |
| Association | T/C: native parent, thread_spawn source and successful completed collab evidence | Associated precedes child Native; later live spawn confirms same relation key once | TestTreeNestedOrderingAndIdentity | First accepted parent retained; conflicts/cycles diagnostic; agentPath is display only |
| Text / thinking | N/I: native message and reasoning notifications/items | Raw/Params retained at native source; message delta and item payloads typed where implemented | TestTreeTextThinkingAndUsageStayAtNativeSource; TestTreeLiveChildStream | Reasoning deltas can remain raw-only; no guarantee of provider thinking emission |
| Tools / extensions | C/I/N: native tool items, unknown methods and fields | Original events, native IDs, UnknownFields/unknown unions, null/absence and DecodeError retained | TestTreeLiveChildStream; TestTreeNestedOrderingAndIdentity | Unknown methods have no invented typed payload; malformed lineage cannot establish membership |
| Nested stream / attach | A/T: new-thread attach and parent/source lineage | Recursive existing-connection routing; zero child read/list/resume/start/subscribe RPC or added connection | TestTreeNestedOrderingAndIdentity; transport traces in tree contracts | New-thread best-effort attach does not imply cold-thread coverage; no generic cross-version fallback |
| Deduplication | I: terminal item identity, no general occurrence cursor | Relationships once; known decoded item/completed keyed by epoch/thread/turn/method/item | TestTreeNestedOrderingAndIdentity; TestTreeDepthAndDedupBounds | No-ID deltas and unknown items preserved; FIFO cache eviction allows old replay; no cross-epoch exactly-once |
| Parent / child status | L: per-thread status and native turn/activity boundaries | Independent raw status and typed turn/item events; parent completion leaves observation open | TestTreeParentIdleChildActive | Idle differs from turn/completed; neither means all children finished; activity end does not delete agent |
| Terminal / future work | L/I: native completed/interrupted states | Forwarded unchanged with native turn IDs; future run of same entity remains observable | TestTreeParentIdleChildActive; TestTreeNestedOrderingAndIdentity | Silence, waiting, timeout, cancel, EOF or parent final answer never imply child success |
| History / explicit recovery | T/C: native Thread snapshots expose lineage | Caller-supplied History seeds Associated only, no notification replay, no inferred current state | TestTreeDisconnectHistoryEpoch; TestTreeDisconnectFullQueueAndSeedBounds | Historical spawn never Created, even if repeated live; seed emits history-recovery-gap; no automatic resume |
| Disconnect / reconnect | A: lag can skip resync, no verified replay cursor | Disconnected/permanent-gap reserved record, original Err; explicit new Client gives fresh process-local epoch | TestTreeDisconnectHistoryEpoch; TestTreeDisconnectFullQueueAndSeedBounds | No reconnect loop, replay, guaranteed cold attach, gap filling or seamless recovery |
| Unresolved / unscoped | N/T: source can be missing or relation unknown | Bounded waiting then explicit incomplete/unresolved Native; no-source remains unscoped | TestTreePendingLimitsAndNoReplay; TestTreePendingDeadlineFanoutIsAtomic | Unresolved may belong to another root; later relation never replays delivered Native |
| Backpressure / bounds | Existing Go bounded transport; no server completeness guarantee | Atomic per-notification fanout, finite queue/graph/depth/pending/cache, observer-local failure | TestTreeFanoutAdmissionIsAtomic; TestTreeBoundsAndLocalClose; TestTreeAdmissionAndNodeBound | Overflow terminates observer; previously admitted records remain readable; control routing does not wait on consumer |
| Permissions / user questions | R: native reverse requests and resolution | Client.Requests original one-shot handle is sole answer authority; tree never auto-approves | TestTreeFullQueuePreservesOriginalRequestAuthority; TestTreeRequestTerminalAndEpochIsolation | Observation Close/cancel leaves handle valid; request cancel/expiry/resolution/connection loss retain original semantics; stale epoch cannot answer new ID |
| Parent final answer | I: source-scoped agent message item | Exact parent answer retained; child text/thinking not merged, no synthetic parent result | TestTreeTextThinkingAndUsageStayAtNativeSource | Application selects native source/turn/item; parent answer is not global child completion |
| Usage | N: native token-usage payload | Exact Raw and source retained; UsageScope=unknown, no tree total | TestTreeTextThinkingAndUsageStayAtNativeSource | No adding parent aggregate to child detail or cumulative snapshots to deltas; no cost inference |
| Observation close / ownership | Existing local Subscription vs explicit native unsubscribe | Idempotent bounded local Close/cancel, independent observers and original handles | TestTreeCloseCancelSharedSource; TestTreeObservationCancellationDoesNotCancelRequest | Does not interrupt/unsubscribe provider work; Client.Close retains transport ownership semantics |
| Protocol / version contract | N: fixed Codex native registry | SDK wrappers outside protocol; native Subscribe remains connection broadcast | TestTreeLiveChildStream; inventory/generation gates | ACP subagent RFD is draft design context only, not stable standard, runtime dependency or implemented ACP wire; older/unknown servers unverified |

## Pinned E1 sources and representation

- **A**: [new-thread attach / lag without resync](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/codex-rs/app-server/src/lib.rs#L1282-L1310)
  and [best-effort per-connection listener](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/codex-rs/app-server/src/request_processors/thread_processor.rs#L3607-L3639).
- **C/I**: [collab tool and native item definitions](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/codex-rs/app-server-protocol/src/protocol/v2/item.rs).
- **T**: [native Thread definitions](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/codex-rs/app-server-protocol/src/protocol/v2/thread_data.rs#L204-L271).
- **N/R**: [notification / reverse-request registry](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/codex-rs/app-server-protocol/src/protocol/common.rs).
- **L**: [TurnComplete handling](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/codex-rs/app-server/src/bespoke_event_handling.rs#L185-L202)
  and [per-thread idle publication](https://github.com/openai/codex/blob/d27764b82f7118f674371e6d6e76271d9d606edb/codex-rs/app-server/src/thread_status.rs#L148-L158).

Native thread/started, thread/status/changed, reasoning deltas and token usage
are currently **raw-only notifications**. The native Thread and item objects
already occur in the generated closure; relation extraction reads only their
native fields and never inserts SDK fields into protocol. No generated root was
added in this round. The 33 roots, 154 definitions, 397 types and 744 synthetic
round-trip cases remain as documented in [generation provenance](protocol/README.md).
The machine inventory records method-level raw/typed scope separately from the
managed-tree control and its E2 evidence. Exact Raw is the envelope without the
transport delimiter; typed re-encoding can normalize whitespace/key order.

Sequence is local delivery order, not a durable cursor. Epoch is Client identity
within one Go process, not a global provider ID. Thread, turn and item identity
are kept distinct; no task/run identity is invented. Usage scope remains unknown.
Each tree retains independently owned buffers and its own bounds, as detailed in
[ticket 04](evidence/subagent-04.md). Limits constrain observation, not work time.

## Run without a model

The [offline example](../examples/observe-tree-offline/README.md) starts only a
synthetic copy of itself, exercises root → child → grandchild, history Associated,
unresolved scope and local close, and checks a deterministic transcript. Use:

```sh
GOPROXY=off GOSUMDB=off mise exec -- go run ./examples/observe-tree-offline
```

All five tickets are accepted at E1/E2 for the explicitly scoped contract:
01 direct creation/association; 02 recursive ordering/deduplication; 03 independent
lifecycle/history/gaps; 04 bounded admission/original authority/source preservation;
05 compatibility publication and runnable offline evidence. This is an offline
acceptance conclusion, not certification of live subagent support or ACP.
