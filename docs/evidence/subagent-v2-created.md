# V2 Created correction — 0.162.1

Baseline main: `6fa450ed92b0b2f30eb3b42e356f4cdcd8de913a`, initially clean.
Official pin: `092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`.
Date: 2026-10-18 UTC. Scope: this one correction, E1/E2 only.

## Source conclusion

V2 spawn awaits agent_control.spawn, which creates a new thread (fresh or forked)
and admits its first input before success. Only then does the handler emit
SubAgentActivity Started. The canonical emitter publishes item/started then
item/completed. The official app-server test takes agent_thread_id directly from
completed Started and asserts that no collab execution item was received.
These inspected facts correct the earlier claim that V2 Started cannot support
Created. The official test itself was not run here.

Follow-up/send emits Interacted, interrupt emits Interrupted, and completion emits
Completed; those activities do not establish creation or a parent relation.
Auto-attach attempts existing initialized connections but emits no thread/started
of its own, and remains explicitly best-effort. There is no guaranteed replay,
cold-thread attach, recovery or live model certification.

All nine files below were independently matched by Git blob SHA to the official
recursive Git tree fetched at the exact pin. The existing exact-source inventory
also passed file bytes, hashes, registry and stable/experimental schema membership.
The local source bundle was read only.

| Official source | Verified Git blob SHA |
| --- | --- |
| `codex-rs/core/src/tools/handlers/multi_agents_v2/spawn.rs` | `da7325bccb7faf9c1b81c4701eab0911b84ac225` |
| `codex-rs/core/src/agent/control/spawn.rs` | `20e28a8bfed7d5862ae3013207040386a1ac9f78` |
| `codex-rs/core/src/tools/handlers/multi_agents_v2.rs` | `ea44c0a427837bad62969097cacc947d9b0ed069` |
| `codex-rs/app-server/tests/suite/v2/turn_start.rs` | `491207ad7772291b100cf32ba6f478aa8925bb22` |
| `codex-rs/core/src/tools/handlers/multi_agents_v2/message_tool.rs` | `8392960121ce91a4f24ec539898934bf8a3d58f4` |
| `codex-rs/core/src/tools/handlers/multi_agents_v2/interrupt_agent.rs` | `e78b3629bdebf7c9903db8cce8e6b0c968e8e004` |
| `codex-rs/core/src/agent/control/completion.rs` | `587f4cdaebffca42c2d19451e8ba0e8db18c6598` |
| `codex-rs/app-server/src/lib.rs` | `896aa6623b690bff7789a830699ae498f1a3b6dc` |
| `codex-rs/app-server/src/request_processors/thread_processor.rs` | `8bdf6bbed3ee667905a9b1428fb4b0ffcac08f02` |

Immutable line references and ordering details are in
[event sequences](../event-sequences.md#关系通知created-和乱序暂存).

## Implementation and public regressions

A live successfully decoded item/completed Started maps notification.threadId
to the parent and activity.agentThreadId to the child. The SDK wrapper retains
the full native activity, source turn/item and unknown fields. It never invents
collab tool/receivers. Native structures, schema, generated files and JSON remain
unchanged. No new public entry point, RPC, socket, reconnect or model call is added.
Existing relationLocked handles creation confirmation, history suppression,
once-only announcements, accepted-parent conflicts, cycles, depth/node bounds,
ancestor ordering and pending flush. Evidence is independently owned when delivered.
History Started imports an Associated edge only, suppressing later live Created.

The external-package tests use the established public SubscribeTree/Subscribe
and synthetic peer seams over stdio, WS and WSS:

- TestTreeV2CreatedWithoutThreadStarted: child delta before relation; no lineage;
  one Created; native activity forwarding and terminal dedup; duplicate deltas;
  parent completion followed by child output; exact Raw/Params; trace proves only
  initialize, initialized and the fixture injection RPC, with zero observer RPC.
- TestTreeV2HistorySuppressesCreation: historical Started imports Associated,
  retains history identity/evidence and never becomes Created after live repeat.
- TestTreeV2GraphOrderingAndConfirmation: early grandchild; ancestor first;
  Associated then one Created across distinct items; conflicts retain both actual
  evidences; cycles diagnostic; caller mutation cannot corrupt retained evidence.
- TestTreeV2NonCreationEvidence: item/started, Interacted/Interrupted/Completed,
  future kinds, empty/missing/null/duplicate identities, conflicting item IDs and
  malformed model data establish no relation; Native remains forwardable and
  child events remain explicitly unresolved when pending bounds release them.

TDD red evidence: initial no-lineage test failed all three transports with
created=0, children=0, spawns=1; history test failed all three with historical
activity misclassified as Created. Both passed after the respective small changes.
The complete focused V2 suite then passed.

## Review and verification

Local Matt spec/ticket were written before implementation under
.scratch/v2-created (the repository local tracker is intentionally gitignored).
The user supplied the single-ticket scope and public transport seams.
Independent Standards and Spec reviews against the baseline found no code issues;
both requested this final evidence file, now present.

Uncached full ordinary suite passed (SDK 27.024s); the explicit offline verbose
repeat also passed (SDK 27.314s), with all three live tests visibly skipped.
Six CI gate self-tests,
vet, module verification, checked-in inventory, exact-source inventory,
reproducible generation and both offline public examples passed.
The optional source verifier initially lacked zstandard in system Python; a
temporary uv environment supplied it without changing repository dependencies.
Full uncached race passed (SDK 187.828s); focused V2 race also passed (21.093s).
Final commit/hosted CI verification is recorded in the final handoff.

Commands use mise Go 1.26.9 / GOTOOLCHAIN=local. Race and follow-up gates set
GOPROXY=off, GOSUMDB=off and all three RUN_MAC_*_LIVE switches to 0. The ordinary
suite used default live opt-ins (disabled); no real CLI/model/auth workflow ran.
Hosted Linux/macOS CI, when confirmed at the delivery SHA, is offline evidence.
