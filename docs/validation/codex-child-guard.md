# Issue #19: Codex child guard evidence and operator handoff

Parent [#7](https://github.com/zoumo/agent-sdk-go/issues/7), actual base
`f0e5ddb2ea6bb54d204ef62d21386aafa7d5f2dd`; dependencies #15/#9 are in its ancestry.
Thread: `01a1721d-2a48-73d9-a675-16b23d1cd0d8` (delegating thread).
Contracts: [child scenarios](../../harness/contracts/stories/child-capabilities.md),
[delivery](../../harness/contracts/child-detail-delivery.md),
[history](../../harness/contracts/claude-subagent-history.md).
Standards: [testing](../../harness/testing.md), [layout](../../harness/code-layout.md).
No model, CLI probe, actual child creation, auth/history read or recording occurred.

## Fixed source findings

All links below use Codex CLI/schema 0.162.1 source
`092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`. Public source was fetched separately
under `/tmp`, without running upstream code. This is source inspection, not a
claim that a selected installed executable matches it.

| Requirement | Source fact | Conclusion |
|---|---|---|
| Cumulative attempts, including failed creation | [registry.rs 90–130, 390–400](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/agent/registry.rs#L90): `reserve_spawn_slot` increments a count, `release_spawned_thread` and reservation Drop decrement it. [spawn.rs 697–727](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/agent/control/spawn.rs#L697) uses residency slots instead of `max_threads` for V2 resident spawns. | Count/residency is not a durable cumulative attempt allowance. A failed spawn can release capacity; completion/removal does not establish a spent-attempt limit. No pre-creation native interceptor connected to this recorder's ledger was established. BLOCKED. |
| Recursive depth | [V1 handler 73–79](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/tools/handlers/multi_agents/spawn.rs#L73) checks `agent_max_depth`; [V2 132 onward](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/tools/handlers/multi_agents_v2/spawn.rs#L132) computes depth without the same local guard. | V1 has a native guard. Do not generalize it to every creation path/version or confuse Session MaxParentDepth with spawn depth. V2/full-path proof remains unestablished. |
| Tools/workdir/network recursively | [child_config.rs 101–194](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/agent/child_config.rs#L101) starts from parent effective config and copies runtime approval, cwd and permission profile. Role overrides are layered separately. | Inheritance is real, but is not independent evidence of an exact recursive tool allowlist, managed hook isolation or containment for every role/path. Post-event notifications cannot stop an already executed tool/spawn. BLOCKED pending verified native isolation. |
| Background cleanup | [PendingSpawn Drop](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/agent/control/spawn_guard.rs#L62) schedules async shutdown; [stdio close and app shutdown](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/lib.rs#L1163) initiate cleanup. | Existing Proxy bounds and reaps its owned process group; it does not establish that a remote/provider background task or an external daemon stopped. Root completion and process exit are separate facts. Full recursive cleanup proof BLOCKED. |

A concurrency limit of one still permits sequential attempts after release and
failed starts; neither a prompt nor a terminal notification fixes this gap.
`ChildPolicy` is an explicit configurable proposal only. Every such profile is
refused before profile filesystem inspection, native opener, version probe or
model launch, including asserted matching source/version. Unknown versions and
sources cannot unlock it. Existing root financial policy was not changed.
Risk acceptance and technical isolation remain distinct; this ticket imposes
no additional permanent monetary or token cap.

## Delivered offline boundaries

- Version 2 of the existing aggregate ledger accepts explicit `maxChildAttempts`
  and `maxChildDepth` alongside existing source/time/root/turn guards. Version 1
  cannot grant children. No automatic migration/reset/refund exists. The journal
  remains private, bounded, locked and synced, and retains the 480-second window.
- `ReserveChildAttempt` records one pre-dispatch intent with an attempt ID and an
  optional earlier same-scene parent attempt; empty parent means the reserved
  root. Depth is computed from this journal, not supplied by a model. Failed or
  uncertain creation and completion never remove a charge. Parallel contention
  may conservatively refuse; accepted intents across all scenes consume one
  aggregate allowance. These are **intent identities**, not fabricated native
  child IDs or proof that creation took place. Caller-controlled accounting alone
  cannot intercept native model-owned spawns.
- Strict RunPeer retains real-shaped native parent/child IDs, root-before-child
  terminal ordering, unknown controller frames and original approval IDs/answers;
  wrong identity/answer fails. Synthetic frames execute no command. Existing
  proxy cleanup and cancellation tests continue to run; no background success is
  inferred from root terminal or a successful mock.
- Publisher transformation now maps `senderThreadId`, `receiverThreadIds`,
  `agentsStates` keys and `agentPath` aliases in distinct namespaces, consistent
  with existing parent/session/thread/turn/item mappings. It preserves state,
  ordering and original timestamp handling. Unknown legacy `codex/event/*` methods are quarantined instead of prefix-dropped.
  Unknown events/fields still fail
  schema review; no child controller class was added to raw exclusions or
  auto-approved. Root-only publication refuses multiple thread identities or
  child aliases rather than publishing a hardcoded child-free manifest. A future
  child publication format and approval remain required. Old real bytes and
  manifests are unchanged; no new synthetic fixture is labeled real.

## Operator approval table — entirely pending

Values are configurable proposals, with **no approved numeric child budget**.
The example root→A/B, A→C would need at least three accepted child intents and
depth two; it is not an allowance. Reconcile historical spent/uncertain attempts
across ledgers before proposing a new run; do not renew old root allowances.

| Requirement | Concrete value/evidence operator must resolve | Status |
|---|---|---|
| Provider/model/source/build | Exact model, absolute CLI, 0.162.1 binary digest, SDK/build revision, fixed source and neutral input/prompt hashes | Pending approval; source inspected only |
| Root sessions / submitted turns | Per-scene and aggregate finite counts in one durable ledger, including failed/uncertain starts | Pending, not inherited from root risk acceptance |
| Cumulative children / topology / depth | Finite attempts including failures/retries, direct/nested/parallel/background topology, pre-creation recursive interception and depth enforcement | Technically blocked; no approved values |
| Native/provider retries | Distinct finite retry/generation policy and enforceable accounting; no extra root turns to force a result | Pending; not measured by root turn count |
| Financial / usage | Metering, explicitly requested stops and risk acceptance; distinguish feasible enforcement from accepted exposure | Pending separate decision; this ticket adds no dollar/token cap |
| Isolation / authentication | Every role/layer's tools, cwd, network, hook policy; operator-provided approved opaque auth transport | Technically blocked/unverified; no credential inspection or repair |
| Time / bytes | Per-scene and activity windows, wire/stderr/storage limits, separate positive background deadline | Existing 120 s/480 s and 2 MiB/256 KiB guards retained; new background value pending |
| Cleanup | Bounded child stop/wait, transport/group reap, external/background scope, durable failure stop | Owned transport behavior tested synthetically; recursive runtime proof blocked |
| Publication | Separately reviewed child manifest, per-version schema, mappings, canaries, raw exclusions, source/digests and coverage | Pending; root publisher refuses child claims |

Downstream child live capture remains BLOCKED even if root financial risk is
accepted. Removing this gate requires new technical evidence and explicit child
topology/budget/isolation approval. No permission string or boolean bypass exists.

## Validation scope

TDD red/green cycles were observed at the existing public recorder/ledger,
publication transform and strict replay seams. Local Go 1.26.9 was obtained from
the official `https://go.dev/dl/go1.26.9.linux-amd64.tar.gz` into `/tmp` because
`/usr/bin/go` is not a usable compiler. Cache/module paths are also under `/tmp`.
CI still uses the unchanged mise pin and synthetic merge-tree Offline jobs;
External consumers fetch the exact pushed head. CI run/head/base/merge evidence
and independent Standards/Spec review are recorded in the PR and issue comments.
No claim of live child success, real background cleanup or complete child
publication follows from these offline tests.
