# 01: Consume Codex Load through the real-time event type

**What to build:** Deliver retained full items and terminal turns through Load.Events using the same Event type and handler as real-time subscriptions, with explicit historical source evidence.

**Blocked by:** None (can start immediately).

**Status:** done

- [x] The same handler consumes loaded and live events without a snapshot parser.
- [x] Ordered items, tool states, unknown fields, terminal statuses and exact source raw survive.
- [x] No fabricated starts, deltas, answer handles or live creation/state transitions.
- [x] Existing resume, resource, cancellation, late-error and cleanup contracts remain tested.

Public Load/Events acceptance and preserved history/lifecycle tests passed with pinned Go 1.26.9. All evidence is offline and synthetic.
