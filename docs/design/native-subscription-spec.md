# Native subscription events

## Problem Statement
Consumers must unwrap TreeEvent.Native and interpret SDK tree metadata to receive native app-server notifications. This obscures the native API and adds public concepts unrelated to the official client vocabulary.

## Solution
Return types.Event directly from Subscription.Events. Keep automatic descendant management private. Preserve separate typed convenience events, native JSON, creation evidence, requests and offline recordings.

## User Stories
1. As a caller, I configure a client and create/resume native threads without an additional session wrapper.
2. As a caller, I send turns and consume native Method, Params, Data, DecodeError and Raw directly.
3. As a caller, I receive child events automatically with original identities and payloads.
4. As a caller, I inspect real creation evidence and distinguish association from creation.
5. As a caller, I see observation gaps and bounded failure independently from provider completion.
6. As a caller, I answer approvals/questions through the original one-shot request handle.
7. As a caller, I can compare actual pinned TS exec and Rust app-server APIs without fabricated equivalence.
8. As a caller, I can compile usage examples and replay existing recordings without a model.

## Implementation Decisions
- Use public types.Event and types.SubscriptionOptions. No Tree aliases, public wrapper, origin flag, raw/managed mode or added RPC.
- Convenience Data types are SubagentCreated, SubagentAssociated and ObservationState, separate from generated native types and JSON.
- Preserve synchronous subscription registration, bounded ancestor ordering, pending expiry, deduplication, history association, original request authority and local close.
- Preserve current V2 live completed Started creation semantics, without depending on thread/started.
- Native per-record Scope/Sequence/Epoch/Origin metadata disappears; evidence and unresolved-source diagnostics remain available.

## Testing Decisions
Use the existing external public Subscribe seam with synthetic stdio/WS/WSS peers and offline real recording replay. Preserve transport traces, creation negative cases, nested ordering, limits, independent buffers, request authority, cancellation and disconnect coverage. Run offline tests, race, vet, generation/inventory and recording gates. Compile usage examples; never execute live model examples.

## Out of Scope
New protocol roots, authentication/security configuration, live model calls, extra child RPCs, reconnect or raw mode, broad SDK redesign, branches/worktrees, and other repositories.

## Further Notes
CLI 0.162.1; official source 092d3acd6bec3e3a14bdc7e7a2810ab628ab759d. Baseline main 885bf4de79c9bde75c342c807a9e3e45f098187c, clean. The user explicitly chose one focused ticket and the established offline seams, resolving the personal to-spec/to-tickets review steps. Existing local Markdown tracker convention applies. Work directly on main; commit/push and verify remote SHA/CI.
