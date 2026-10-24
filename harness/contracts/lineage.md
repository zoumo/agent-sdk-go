---
type: Specification
title: 已确认关系与资源合同
status: stable
---

# Shared relationship rules

`internal/lineage` is the parent relationship authority used by both native
observers. Each Codex subscription and each Claude managed observer owns a
separate graph. The package imports only the standard library and has no event,
protocol, creation, permission, answer, transport or SDK type dependencies.

Adapters confirm identities and parent edges using their own native evidence.
An observed vertex without a confirmed parent remains unknown unless the adapter
explicitly marks it as a root. No path is inferred beyond confirmed edges. A
conflict preserves the accepted parent; self-links and cycles are rejected even
in components that have not reached a root. Ancestor queries include only
confirmed parents, nearest first. Root membership follows those same edges.

`Confirm` validates candidate vertices, edges, cycles and every affected depth
before committing. Rejected candidates retain no vertex or relationship.
`ConfirmRoot` also rolls back a newly observed root or root designation when its
child edge is rejected. A newly
arriving ancestor can exceed an existing descendant's depth, so admission checks
all observed chains. `Merge` atomically contracts two caller-confirmed equivalent
identities, rewrites every descendant edge and removes the discarded vertex. It
rejects conflicting parents, ancestor contraction and excessive resulting depth.
Identity equivalence is established by the native adapter, never by the graph.

The graph's integer limits are literal: zero relations allows no edge; zero depth
allows only depth-zero vertices. Public SDK options still interpret zero as their
documented defaults. The public bounds are mapped as follows:

| Observer | Shared graph vertices | Shared graph relations | Depth |
|---|---|---|---|
| Codex subscription | `MaxNodes`, including subscribed root and identities seen only as a confirmed parent | `MaxNodes - 1` | Edges to root, or edges to an unknown top vertex |
| Claude managed observer | `2 * MaxNodes`, including stable native associations and session roots | `MaxNodes` | Edges to a confirmed session root; an unknown top association counts as depth one |

Codex defaults remain 4096 nodes and depth 32. Parent-only identities now count
against the graph limit; a rejected orphan relation cannot reserve identities.
`MaxNodes: 1` admits the subscribed root and no relationship. Codex native
evidence, history provenance, creation evidence, announcement order, pending
events/bytes/age and bounded occurrence cache remain in `codex`.

Claude's existing `MaxNodes` limit remains 4096 (maximum 65536) native
alias/tool-owner/non-agent entries in total. The shared graph independently
counts one vertex per stable association plus at most one confirmed root per
session containing an association, hence the explicit `2 * MaxNodes` ceiling.
Confirmed edges are independently bounded by `MaxNodes`. Aliases remain native
index entries and do not create graph vertices. The graph has no separate alias
registry. The earliest displayed association remains the keeper when native
message/task/tool evidence reconciles aliases. Native parent-task references
remain unresolved evidence until the native lookup confirms the parent entity.
Runs, creation evidence, UUID deduplication, held events, fanout, permissions and
transport shutdown retain their independent native bounds and lifecycle.

Graph resource errors use each SDK's existing visible limit path. Relationship
events remain context only: answers still require the original request or
permission handle. Neither native creation checks nor unknown raw data and gap
reporting are implemented by the graph. No `TreeRecord` or `TreeEvent` API exists.
