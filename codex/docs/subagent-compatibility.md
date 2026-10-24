# Codex Session descendant compatibility

Protocol baseline: Codex 0.162.1, official revision
`092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`. Session owns one main identity and
one Receive FIFO on an explicit shared or owned connection. Native auto-attach is
best-effort; inspected source and synthetic peers do not prove complete runtime
child forwarding. No subagent live pass or supported server-version range is claimed.

| Capability | Session behavior | Offline evidence | Limit |
|---|---|---|---|
| Membership | Completed valid spawnAgent or V2 completed started activity; native parent/source metadata | session_native_regression_test.go positive and full 14-case negative matrix | Empty/conflicting/malformed identity cannot admit a child |
| Parent knowledge | First accepted parent; conflicts/cycles/depth rejection reported separately | Native nested and graph tests | Parent graph reachability is not the sole membership proof |
| Native content | Exact Raw/Params, typed unknown fields/unions, native turn/item IDs | Native metadata, text/thinking/usage and overlapping-copy tests | No synthetic parent answer, aggregate usage or global completion |
| Duplicates / order | Every accepted native arrival survives in receive order | Repeated delta/spawn/unknown-item and 4096/8192 backlog cases | No occurrence cache, pending ancestry reorder or cross-connection exactly-once |
| Unknown scope | Bounded diagnostic quarantine, active expiry, copies and gaps | Session quarantine tests | No retroactive insertion or answer authority |
| Requests | Same Receive FIFO and original registry handle | request_contract_test.go; Session child/backlog/epoch/write cases | Ownerless connection controls remain diagnostic; no default policy |
| History / ordinary resume | Explicit package Load owns confirmed same-ID replay and attachment; raw ResumeThread keeps no-replay semantics | Session Load/history tests | Active/ephemeral/mutated/partial history cannot become success |
| Ownership / close | Unique main owner, shared physical attachment references, final observer releases | session_load_test.go; 64-way distinct-owner lifecycle | Close revokes owned requests; Receive cancellation only ends its wait |
| Failure | Accepted drain then stable cause; Abort abandons backlog | Native fault, remote coalescing and backpressure tests | No reconnect/resend/gap reconstruction |

E1 is immutable source/type inspection. E2 is original synthetic offline testing;
race results are reported separately. E3/E4 real process/model descendant validation
was not performed. Historical captures retain their original pins and scope in
[the evidence manifest](protocol/evidence.json). See [testing](testing.md).

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


Each Session owns independent native buffers. Source thread/turn/item identity
remains native; SDK relationship/coverage observations use a separate Received
kind. No SubagentCreated/Associated convenience lifecycle remains. The dynamic
FIFO is bounded by memory; membership, parent knowledge, request registries and
diagnostic retention have separate limits.

The [offline example](../examples/observe-subagents-offline/README.md) uses only
an isolated synthetic peer. It demonstrates native child facts, optional parent
knowledge, quarantine gaps and explicit close/drain. ACP RFDs remain design
context; no ACP wire protocol or runtime dependency is implemented.
