# 03: Integrate and validate one consumption contract per provider

**What to build:** Migrate SDK consumers, examples and documentation to event-based Load, publish the revised contract and complete offline verification and independent review.

**Blocked by:** 01: Consume Codex Load through the real-time event type; 02: Consume Claude Load through the real-time event type.

**Status:** in-progress

- [ ] No public Load example or active contract requires separate snapshot consumption.
- [ ] Shared handlers accept both replay and real-time provider-native event types.
- [ ] Required pinned Go offline, race, vet, generation and dependency checks pass.
- [ ] Standards and Spec reviews are completed and actionable findings resolved.
- [ ] Only task files are committed on the current branch; no push or release.
