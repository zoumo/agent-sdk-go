# Direct native subscription events: breaking migration

Only CLI **0.162.1**, official source `092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`, is supported. Offline validation establishes SDK behavior, not live descendant routing coverage.

`Client.Subscribe(ctx, rootID, types.SubscriptionOptions{})` returns `*codex.Subscription`. `Events()` returns `<-chan types.Event` directly. The public Tree wrapper, kinds, identity/evidence names and origin flags are removed without aliases or a mode switch. This is a delivery-shape change, not a rename of the wrapper.

1. Obtain a native thread ID through StartThread or ResumeThread. Subscribe synchronously before StartTurn; earlier notifications are not replayed.
2. Read `event.Method`, `Params`, `Data`, `DecodeError`, and `Raw` directly. Native fields, unknown variants, absent/null distinctions and received envelope bytes are unchanged. `codex.Event` moves to `types.Event`.
3. Switch on concrete `event.Data`: native notification pointers remain native. Convenience events are `*types.SubagentCreated`, `*types.SubagentAssociated`, or `*types.ObservationState`, defined separately in [types/derived_events.go](../types/derived_events.go). These have empty Method and nil Params/Raw, and never modify native structs or JSON.
4. Inspect SubagentCreated/Associated's ThreadID, ParentThreadID and Evidence. `types.SubagentEvidence.Activity` retains the actual V2 creation item. Associated and history evidence never prove creation. Current V2 completed Started activity still confirms creation without thread/started.
5. Relationship management and bounded child forwarding remain automatic and private. There is no per-native-event Scope, Sequence, Epoch, Origin or Native wrapper. An unresolved-source state names UnresolvedThreadID; the following native event does not prove membership. Applications may maintain their own relationships from convenience evidence.
6. Answer approvals/questions only through Client.Requests and their original Handle. Keep draining events independently of UI decisions.
7. Continue after parent completion when child/background work matters. No combined completion, answer or usage is synthesized.
8. Inspect Done/Err and drain accepted events. Local Close/cancellation does not interrupt work or send unsubscribe. Overflow terminates only this subscription. Unexpected disconnect retains the original cause and a reserved permanent-gap state.

```go
sub, err := client.Subscribe(ctx, rootID, types.SubscriptionOptions{})
if err != nil { return err }
defer sub.Close()
for event := range sub.Events() {
    switch data := event.Data.(type) {
    case *types.ItemCompletedNotification:
        _ = data.Item
    case *types.SubagentCreated:
        _ = data.Evidence.Activity
    case *types.SubagentAssociated:
        _ = data.ParentThreadID
    case *types.ObservationState:
        _ = data.Reason
    }
}
return sub.Err()
```

SubscriptionOptions retains History and the same queue, depth, pending and deduplication bounds. History imports associations only; it causes no read/resume RPC and never replays native events. Reconnecting requires an explicit new Client and leaves a permanent gap. Native auto-attach remains best-effort at the pinned version. No extra child start/resume/turn/read/list/subscribe RPC or connection is introduced.

See [compiling usage](../examples/observe-turn/main.go), [synthetic offline observation](../examples/observe-subagents-offline/main.go), [offline real transcript replay](../examples/replay-recorded/main.go), and [official API comparison](design/native-sdk-comparison.md).
