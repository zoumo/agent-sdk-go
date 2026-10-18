# Migrate to native events

This breaking change removes the public relationship envelope. The current CLI
target remains 2.1.258 and the official TypeScript contract is 0.3.258.

| Previous API | Current API |
| --- | --- |
| `Receive(ctx) (TreeRecord, error)` | `Receive(ctx) (ReceivedEvent, error)` |
| `record.Kind == "Native"`, then `record.Native` | Type assertion or switch on direct `Event` value |
| String Kind for derived records | Concrete `SubagentAssociated`, `SubagentCreated`, `ObservationState` values |
| Wrapper origin string | Removed; native `Event.Origin` remains the official object |
| `Options.Tree`, `TreeOptions`, `TreeSeed`, `TreeIdentity` | `Options.Subagents`, `SubagentOptions`, `SubagentHistory`, `SubagentIdentity` |
| Native wrapper Source/Parent/run metadata | Private ledger; use native IDs and separate relationship/observation events |

```go
opts := claude.OptionsFromTypes(config)
opts.Subagents = claude.SubagentOptions{MaxNodes: 4096}
opts.PermissionHandler = handler
client, err := claude.Open(ctx, opts)
if err != nil { return err }
defer client.Close()
for {
    received, err := client.Receive(ctx)
    if err != nil { return err }
    switch event := received.(type) {
    case claude.Event:
        _ = event.Raw
        _ = event.Origin // official *MessageOrigin, never SDK classification
    case claude.SubagentAssociated:
        _ = event.Source
    case claude.SubagentCreated:
        _ = event.Source.TaskID
    case claude.ObservationState:
        _ = event.Reason
    }
}
```

Native Event aliases retain identity with types.Event. Derived event definitions
and JSON codecs live separately in derived_events.go; they do not wrap native
messages. Their JSON type names are subagent_associated, subagent_created and
observation_state. There is no SDK-origin flag. Raw bytes remain exact; marshaling
a native event uses its existing lossless semantic JSON codec.

The first receive remains an unverified ObservationState. Open always requests
child text forwarding. Bounds/history validation still precedes process startup.
Options.Types omits Subagents and callbacks; OptionsFromTypes initializes defaults.

PermissionRequest remains the sole answerable handle. Send, Interrupt, SessionID,
RequestedResumeID, Done, Err, Close and Receive cancellation keep their contracts.
A root result or native idle does not close observation or prove every child is
finished. Call Close under an explicit application policy.

[Independent consumer compile check](../scripts/check-managed-consumer.sh) and
[offline child example](../examples/subagents-offline/main.go) use the current API.
Historical evidence files retain their original APIs and validation dates.
