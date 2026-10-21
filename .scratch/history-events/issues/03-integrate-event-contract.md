# 03: Integrate and validate one consumption contract per provider

**What to build:** Migrate SDK consumers, examples and documentation to event-based Load, publish the revised contract and complete offline verification and independent review.

**Blocked by:** 01: Consume Codex Load through the real-time event type; 02: Consume Claude Load through the real-time event type.

**Status:** done

- [x] No public Load example or active contract requires separate snapshot consumption.
- [x] Shared handlers accept both replay and real-time provider-native event types.
- [x] Required pinned Go offline, race, vet, generation and dependency checks pass.
- [x] Standards and Spec reviews are completed and actionable findings resolved.
- [x] Only task files are committed on the current branch; no push or release.

Offline validation, full race and vet passed. Standards review found two test regressions; both were fixed, passed focused regression tests and were independently rechecked. Spec review found no actionable issues. No push or release.
