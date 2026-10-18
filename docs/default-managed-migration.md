# Migrate to default managed events

This is a breaking API change. The supported creation contract remains exact CLI
2.1.258 and official SDK 0.3.258; synthetic tests do not establish live coverage.

| Before | Now |
| --- | --- |
| `Open(ctx, opts)` returns `*Client`; Receive returns Event | Open still returns `*Client`; Receive returns `TreeRecord` |
| Read `event.Type`, `.Raw`, `.Assistant` | Check `record.Kind == "Native"`, then read `record.Native.Type`, `.Raw`, `.Assistant` |
| First receive assumed system/init | First record is ObservationState(unverified); handle all Kind values |
| `OpenTree(ctx, opts, bounds)` / `*TreeClient` | Set `opts.Tree = bounds`; call `Open(ctx, opts)` / `*Client` |
| Runtime/types `ForwardSubagentText` boolean | Remove the field; Open always requests `forwardSubagentText:true` |
| Optional forwarding path | One managed path; initialize rejection fails Open without fallback |

There is no compatibility factory, alias, raw-only Receive, or mode selector.
`ParseEvent` remains an offline native parser, not another runtime stream.

```go
opts := claude.OptionsFromTypes(config)
opts.Tree = claude.TreeOptions{MaxNodes: 4096}
opts.PermissionHandler = handler
client, err := claude.Open(ctx, opts)
if err != nil { return err }
defer client.Close()
for {
    record, err := client.Receive(ctx)
    if err != nil { return err }
    switch record.Kind {
    case "Native":
        var native *types.Event = record.Native
        var officialOrigin *types.MessageOrigin = native.Origin
        _ = officialOrigin
        _ = native.Raw // exact input bytes, excluding newline
    case "SubagentAssociated", "SubagentCreated":
        _ = record.Source
    case "ObservationState":
        _ = record.Reason // retain unverified/incomplete/history-gap diagnostics
    }
}
```

`TreeRecord`, `TreeIdentity` and `TreeSeed` are independently defined in
[derived_events.go](../derived_events.go). Kind is the authoritative discriminator.
The wrapper Origin is a native/sdk-derived string; Event.Origin and
ResultEvent.Origin remain official `*MessageOrigin` objects, with their existing
JSON codecs and absent/null/unknown-member behavior. Derived records have
Native=nil and do not fabricate native type/subtype. Marshal Native or use its
Raw for native JSON; marshaling TreeRecord produces an SDK envelope.

`Options.Tree` controls bounded resources and caller-verified history only.
`Options.Types()` omits runtime-only Tree and callbacks; `OptionsFromTypes()`
starts with zero/default Tree. The entire runtime Options is not a lossless
round trip through callback-free types.Options. Invalid bounds/history fail
before spawning the CLI. Default Tree still manages the stream.

Permissions retain the original sole answerable PermissionRequest. No automatic
approval, second responder, child resume/turn/process, hook, output-file reader,
or extra stdout consumer is introduced. Canceling Receive only cancels its wait.
Root result and native idle do not close observation; background tasks and
requests can require continued Receive. Native idle is not synthesized from
result, silence or an available input box. Scope/source output and usage stay
separate. Close releases the owned process; disconnect retains a visible gap.

See [user-story sequences](event-sequences.md), [bounds and creation rules](subagent-events.md)
and the [capability matrix](subagent-compatibility.md). Historical evidence files
retain their original API and validation dates; they are not new acceptance runs.
