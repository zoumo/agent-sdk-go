---
type: Specification
title: 已确认关系与资源合同
status: stable
---

# Shared relationship rules

`internal/lineage` validates confirmed optional parent relationships. Each Session owns its parent graph separately from its membership evidence. The package imports only the standard library and has no event, protocol, creation, permission, transport or SDK type dependencies. Graph reachability proves only a known parent path, not the full extent of Session observation membership.

Native adapters establish membership, identities, aliases and parent edges using version-supported evidence. An identity can be a confirmed member while its parent is unknown. Communication targets alone do not establish parentage; only evidence for the particular native operation may do so. No missing root edge or creation fact is invented. A conflict preserves the accepted parent; self-links and cycles are rejected even in components that have not reached a root. Ancestor queries include confirmed parents, nearest first.

`Confirm` validates candidate vertices, edges, cycles and every affected depth
before committing. Rejected candidates retain no vertex or relationship.
`ConfirmRoot` also rolls back a newly observed root or root designation when its
child edge is rejected. A newly
arriving ancestor can exceed an existing descendant's depth, so admission checks
all observed chains. `Merge` atomically contracts two caller-confirmed equivalent
identities, rewrites every descendant edge and removes the discarded vertex. It
rejects conflicting parents, ancestor contraction and excessive resulting depth.
Identity equivalence is established by the native adapter, never by the graph.

The graph's integer limits are literal: zero relations permits no edge; zero depth permits only depth-zero vertices. Public native options document their own default mapping. Admission validates all affected chains atomically; a rejected orphan edge cannot reserve vertices.

Graph, member, alias and request limits are distinct resources. Node accounting includes parent-only identities. Native adapters document count/byte limits and report rejected relationship evidence as a coverage/resource gap. A bounded parent index is not a preset maximum Session observation depth and cannot suppress already confirmed member events. It must not fabricate a root edge to fit a bound. If safe continued observation is impossible, termination is explicit and accepted events remain drainable.

The native adapter owns identity equivalence, source provenance, creation interpretation, aliases and observation gaps. Unknown run identifiers remain opaque without fixed native comparison semantics. SDK relationship announcements may avoid duplicates; this does not authorize filtering native events by UUID or entity identity, or keeping an unbounded occurrence ledger on every root message.

Answers require the original live native request handle. Relationship records carry context only; they do not grant answer, input or configuration authority. No TreeRecord or TreeEvent Interface is introduced. See [event delivery](event-delivery.md) for acceptance, isolation and drain semantics.
