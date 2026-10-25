# Independent history queries (offline)

From the repository root, run `mise exec -- go run ./codex/examples/history-query-offline`.
The program launches its own strict synthetic JSONL peer. It needs no installed
Codex, account, session files or model and creates no Session.

```text
threads: saved; next page: 0; end: true
metadata preview: hello…
summary: turn-1; items: 0
saved item: hello from saved content
```

This demonstrates public ListThreads, metadata-only ReadThread, summary turns
and saved items with one explicit continuation. It does not fetch complete
history or original events. Real servers control version, filtering, paging,
errors and server paths; see [native history boundaries](../../docs/history-query.md).
