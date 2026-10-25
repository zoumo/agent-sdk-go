# Independent native history queries

Contract: [independent history](../../harness/contracts/history-query.md),
[US-014](../../harness/contracts/stories/US-014.md).

Use an initialized `Client` without `Open`:

```go
page, err := client.ListThreads(ctx, types.ThreadListParams{
    UseStateDbOnly: types.Some(true),
})
metadata, err := client.ReadThread(ctx, types.ThreadReadParams{
    ThreadID: id, IncludeTurns: types.Some(false),
})
turns, err := client.ListThreadTurns(ctx, types.ThreadTurnsListParams{ThreadID: id})
items, err := client.ListThreadItems(ctx, types.ThreadItemsListParams{ThreadID: id})
```

These are independent examples; check each error before consuming that result.
Each call returns one native response. `ListThreads` adds a typed convenience
entrypoint over `thread/list`; existing read/turn/item methods retain their
original dispatch. It does not reuse `Session.ListSubagents` or its fixed-main,
experimental capability and coverage policy. Native servers can still require
experimental support for particular filters; preserve their rejection.

The binding remains **Codex 0.162.1**, commit
[`092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`](https://github.com/openai/codex/commit/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d).
No schema/generated code was upgraded. Fixed primary sources checked for this
slice are the [protocol](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/src/protocol/v2/thread.rs)
and [handlers](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/thread_processor.rs).
Separately inspected upstream
[`970111f6cb62cc02fe36a82ea7d71b37a2889c97` protocol](https://github.com/openai/codex/blob/970111f6cb62cc02fe36a82ea7d71b37a2889c97/codex-rs/app-server-protocol/src/protocol/v2/thread.rs)
and [handlers](https://github.com/openai/codex/blob/970111f6cb62cc02fe36a82ea7d71b37a2889c97/codex-rs/app-server/src/request_processors/thread_processor.rs)
are additional source evidence, not a compatibility claim for the pinned server.
New `readState`/`readStates` response fields survive in `UnknownFields`.

| Operation | Native behavior and limits |
|---|---|
| list | Default interactive sources, non-archived; not all history. Provider/source/archive, cwd/project/section, parent/ancestor, title search, sort and DB-only options pass through unchanged. Nonempty originators is rejected locally and supported only by hosted backends. |
| metadata | Prefer includeTurns=false. Identity, preview, times/status are metadata; status may reflect live state. Missing status facts are not inferred. |
| turns | Default desc/summary; full requests saved ThreadItems, not raw events. Reverse backwardsCursor can include the anchor again to capture updates. Legacy fallback may rebuild the entire rollout on each page. |
| items | Default asc; opaque cursor or exclusive `{type:"item",itemId}` anchor. Anchor requires nonempty turnId. Subsequent continuation uses the returned string cursor. Local legacy or missing DB/metadata support can reject with native method-not-found. |

The checked handlers default list/turn/item page sizes to **25**, clamped **1–100**.
These are fixed native implementation details, not new SDK defaults: the wrapper
transmits explicit 0/101 without clamping and leaves omitted fields omitted.
Default list sort is created_at/desc, with section_position defaulting asc;
backwards list cursors preserve native timestamp/tie behavior. The SDK imposes no
sorting or deduplication. Keep opaque next/backwards cursors and the requested
scope/direction when continuing. `nextCursor:null` means no more results now;
omitted and null remain distinct. No operation promises a stable snapshot across
pages or against concurrent live updates.

Queries do not resume, start, subscribe, submit or answer, create Session
membership, or synthesize events in `Receive`. They may run alongside a Session
owning the queried identity without the removed Load idle/stability gate. Saved
messages/items cannot create answer handles. Metadata-only, summary and full
saved items have different coverage; none recovers the complete delta,
notification, approval or hook log.

Queries use app-server: constructing `NewStdio` starts a process and initializes
it; the native list path may scan rollouts and repair metadata. This is not a
zero-process/zero-disk-write API. `UseStateDbOnly:true` requests native DB-only
behavior; unavailable DB support remains an error. `NewRemote` queries the
server's storage with server paths, never scans client transcripts, and does not
assume the server uses LocalThreadStore. Native validation, unsupported features,
RPC errors (including data/raw), context cancellation and existing dispatch
states remain observable without retry, resume or replay fallback.

The [offline example](../examples/history-query-offline) executes only a synthetic
peer. [Slice validation](../../docs/reviews/2026-10-25-codex-history-query.md)
records exact checks. No live server, private history or model was accessed.
Claude independent local queries remain a separate ticket; this slice does not
certify completion of US-014 across both SDKs.
