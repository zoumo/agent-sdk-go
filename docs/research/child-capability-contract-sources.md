# Child contract fixed-source comparison

Design ticket [#15](https://github.com/zoumo/agent-sdk-go/issues/15), parent
[#7](https://github.com/zoumo/agent-sdk-go/issues/7), reviewed 2026-10-25 at actual
base `52b329a77333e55063a97eaf1304c82b30a99a59` (completed #8). Normative design:
[delivery](../../harness/contracts/child-detail-delivery.md),
[child history](../../harness/contracts/claude-subagent-history.md),
[acceptance/preflight](../../harness/contracts/stories/child-capabilities.md).
These are targets for #16–#20, not implemented capabilities. No recording or model call.

## Pins and algorithm comparison

Claude official source is Python SDK 0.2.174 commit
[`0e89b298e771259378a6cd3734e00d323dff5703`](https://github.com/anthropics/claude-agent-sdk-python/tree/0e89b298e771259378a6cd3734e00d323dff5703).
Use this immutable source rather than a moving main or main-history algorithm.
This query source pin does not upgrade the Go runtime/recording CLI 2.1.258 pin.

| Official source | Observed source behavior | Go target decision |
|---|---|---|
| [resolver and collector, sessions.py 1367–1476](https://github.com/anthropics/claude-agent-sdk-python/blob/0e89b298e771259378a6cd3734e00d323dff5703/src/claude_agent_sdk/_internal/sessions.py#L1367) | Parent transcript resolves child directory, recursively walks sorted nested paths. | Explicit absolute ConfigDir/project scope, same child-relative layout, no worktree git/process or environment fallback. Full uniqueness scan instead of first match. |
| [child chain, 1477–1523](https://github.com/anthropics/claude-agent-sdk-python/blob/0e89b298e771259378a6cd3734e00d323dff5703/src/claude_agent_sdk/_internal/sessions.py#L1477) | Last user/assistant leaf, parentUuid traversal and reversal. | Same independent child selection; reject broken/duplicate/cyclic chains instead of truncating/last-write-wins. Do not use queryConversation main chain. |
| [list/get, 1525–1645](https://github.com/anthropics/claude-agent-sdk-python/blob/0e89b298e771259378a6cd3734e00d323dff5703/src/claude_agent_sdk/_internal/sessions.py#L1525) | Returns child IDs; selected nested agent file, optional sidecar. Some missing/access/invalid cases become empty. | Rich independent values, bounded inventory/content and typed errors; parent missing distinct from valid empty; malformed/unreadable sidecar is error. Session-bound discovery is not this API. |
| [sidecar and messages, 1648–1749](https://github.com/anthropics/claude-agent-sdk-python/blob/0e89b298e771259378a6cd3734e00d323dff5703/src/claude_agent_sdk/_internal/sessions.py#L1648) | toolUseId/parentAgentId copied to all user/assistant child messages; absent metadata unknown; slice after chain. | Preserve these facts plus original raw values/unknown; no synthetic parent edge. Bounded zero-limit defaults differ from upstream unbounded behavior. |
| [main chain, 933–1261](https://github.com/anthropics/claude-agent-sdk-python/blob/0e89b298e771259378a6cd3734e00d323dff5703/src/claude_agent_sdk/_internal/sessions.py#L933) | Main visibility/compaction and related parallel tool recovery. | Deliberately excluded from child chain; child sidechain/meta/team flags remain in returned messages, no sibling restoration or timestamp sorting. |

Go is intentionally stricter than upstream best effort: explicit scope, safe path-component IDs,
no symlink/special files, bounded bytes/lines/index/files/directories/depth, duplicate-key/identity
validation, ambiguous collision errors, no silent truncation, no partial content, observed-change
validation and visible coverage. This does not promise full upstream worktree/store/cloud parity
or atomic snapshots. Known metadata types reuse the main parser's classification only, not its
chain/visibility/recovery algorithm. Source inspection is not retained real child-file evidence.

Delivery pins remain Codex CLI/schema 0.162.1 upstream
[`092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`](https://github.com/openai/codex/tree/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d)
and Claude CLI 2.1.258 / SDK declarations 0.3.258. Existing fixed native evidence:
[Claude declarations/hashes](claude-session-native-evidence.md),
[Codex inventory](../../codex/docs/protocol/capabilities.json),
[output/return layers](subagent-event-delivery.md). Preserve actual known identity rules;
newer query source is not evidence for newer live agent_id emission or task parentage.

## Implementation handoff and evidence classification

| Ticket | Deterministic input | Required evidence; current limitation |
|---|---|---|
| #16 | Native Codex policy field, exact method allowlist and protected/mixed rules | F1–F5 public Receive + raw peer trace; no Session resume addition; current filtering N, real child M. |
| #17 | Native Claude policy field incl. ResumeID, alias rules, explicit forwarding true | F1–F5, mixed assistant/stream boundaries and original permission handles; filtering N, real child M. |
| #18 | Package functions/types/options, layout, child-only chain, sidecar, errors and bounds | H1–H5 local synthetic ConfigDir; independent child messages N, existing Session discovery I, real child files M. |
| #19 | Direct/nested/parallel/background topology and all numbered preflight gates | Offline recursive child policy/ledger proof first; cannot renew #9/old budgets, live blocked pending explicit approval/enforcement. |
| #20 | Same gates plus separate newly saved child files and sidecars | Offline recursive policy/storage allowlist proof first; authentication/isolation/budget/publication remain separate blockers. |

Shared acceptance extends SDK-US-006/007/008/019 and US-003/004/005/013/014 without
renumbering or erasing #8's Phase 1 matrix. CODEX-US-005 / CLAUDE-US-006/007 are new
native targets. Offline tests, source claims and future real captures stay separate.
