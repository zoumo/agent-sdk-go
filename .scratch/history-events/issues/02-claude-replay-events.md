# 02: Consume Claude Load through the real-time event type

**What to build:** Deliver validated retained main-chain entries through Load.Events as normal ReceivedEvent/native Event values, including full blocks and explicit original history-source evidence.

**Blocked by:** None (can start immediately).

**Status:** done

- [x] The same native Event type switch consumes replay and Client.Receive values.
- [x] User/assistant/tool/unknown/system data and identity evidence survive projection.
- [x] No tools execute and historical content creates no live permission or creation authority.
- [x] Branch, compaction, mutation, native initialize, cancellation and cleanup contracts remain tested.

Public Load/Events acceptance and preserved history/lifecycle tests passed with pinned Go 1.26.9. All evidence is offline and synthetic.
