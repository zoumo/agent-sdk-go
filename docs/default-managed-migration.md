# Default managed subscriptions: breaking migration

Only Codex CLI **0.162.1**, pinned source `092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`, is supported. Offline tests establish SDK behavior, not live routing coverage or compatibility with other server versions.

`Client.Subscribe(ctx, rootThreadID, TreeOptions{}) (*Subscription, error)` is now the only observation entry. Zero options enable managed root/descendant observation. The old `Subscribe(ctx)` connection broadcast, `SubscribeTree`, and `TreeSubscription` have been removed, without aliases or a raw/managed switch.

1. Obtain a native root thread ID from an existing thread or StartThread response. An empty root is invalid; a turn ID or V2 task path is not a thread ID.
2. Register Subscribe before StartTurn. Registration is synchronous; notifications before registration (including thread/started during StartThread) are not replayed.
3. Read `TreeEvent` records from Subscription.Events. Switch on Kind before inspecting Native. The initial record is ObservationState/incomplete, not a native notification.
4. For TreeNative, use `record.Native.Method`, Params, Data and DecodeError. Assert concrete `types` notification pointers against Native.Data. `record.Raw` preserves the exact received envelope, excluding transport delimiters. Unknown methods, fields and union variants remain inspectable; absent Params differs from explicit null.
5. Handle TreeSubagentCreated, TreeSubagentAssociated and TreeObservationState separately. Their names, Origin, identity and evidence are SDK wrapper metadata in [derived_events.go](../derived_events.go); they never modify native types, native JSON, or Event. Derived records have nil Native/Raw and no fabricated method.
6. Continue after root idle or turn/completed if descendants or commands still matter. Each native thread/turn/item retains its own state and output. There is no tree-wide completion event, usage sum, or merged final answer.
7. Answer permissions/questions only through Client.Requests and the original Request.Handle. Records cannot answer, and observation never approves automatically.
8. Check Done/Err and drain accepted records. Close/context cancellation ends local observation only. Overflow or limits terminate this subscription independently; unexpected connection termination preserves the original cause and a reserved permanent-gap record while the observation is still active.

```go
sub, err := client.Subscribe(ctx, rootID, codex.TreeOptions{})
if err != nil { return err }
defer sub.Close()
// StartTurn only after registration; handle Requests independently.
for record := range sub.Events() {
    switch record.Kind {
    case codex.TreeNative:
        switch data := record.Native.Data.(type) {
        case *types.ItemCompletedNotification:
            _ = data // native item completion, not whole-tree completion
        }
        _ = record.Raw
    case codex.TreeSubagentCreated, codex.TreeSubagentAssociated:
        _ = record.Evidence
    case codex.TreeObservationState:
        _ = record.Reason
    }
}
return sub.Err()
```

Existing managed consumers only rename SubscribeTree to Subscribe and TreeSubscription to Subscription. Existing broadcast consumers must also select an explicit root and unwrap Native. Unknown source events use bounded unresolved/unscoped delivery; they must not be displayed as proven children. Sequence is subscription-local and Epoch process-local, neither a provider cursor. History imports relations only, never creation or native replay. Reconnect requires an explicit new Client and leaves a permanent gap. The SDK sends no additional child start/resume/turn/read/list/subscribe RPCs or connections; native auto-attach remains best-effort.

The V2 Started prerequisite is main `0db0a97271cb881745dd33e735266253ab798ed9`, whose [offline CI 39105250983](https://github.com/zoumo/codex-sdk-go/actions/runs/39105250983) passed Linux and macOS. Its actual Activity evidence, no-thread/started routing and negative cases remain in [tree_v2_contract_test.go](../tree_v2_contract_test.go).
