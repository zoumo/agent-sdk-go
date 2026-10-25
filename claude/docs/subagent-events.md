# Session forwarding and relationship evidence

`Open(ctx, SessionOptions)` owns one native process and one `Received` FIFO.
It requests `forwardSubagentText: true` during initialize. The acknowledgment
confirms the request only; complete child forwarding remains unverified.
Use [Session usage](../README.md#session-usage) and the
[runnable offline example](../examples/subagents-offline/main.go).

`Session.Receive` distinguishes native `Event`, actual `IncomingRequest`, SDK
`Observation`. Native Raw, unknown fields, null/absence,
text/thinking/tool blocks, usage and Origin survive without rewriting. Native
facts retain arrival order and duplicates. Parent knowledge can arrive later;
missing ancestry does not delay confirmed same-session facts. No Created label,
current-run ledger, lexical run ordering or UUID suppression is inferred.

The fixed relationship evidence is SDK 0.3.258 / CLI 2.1.258, recorded in
[type sources](type-sources.md). An exact-version init and a local_agent
`task_started` with native task/tool IDs can establish an alias. A forwarded
message's parent_tool_use_id can use that alias. Observed native tool ownership
can establish a parent; the shared graph rejects cycles/conflicts/depth excess.
Unknown message agent_id, parent_task_id and run_id extensions stay available in
native data, without equating agent/task identities or inventing parent/run state.
Non-agent tasks and tools cannot establish local-agent aliases.

Unknown envelopes, pre-identity frames and foreign-session records stay in
`Session.Unattributed()`. Snapshots retain exact Raw, timestamps, reasons,
limits, dropped/expired counters and coverage gaps. Count/byte eviction and
active expiry are diagnostic retention policies. Later evidence never backfills
these records into Receive or grants their request authority.

| SubagentOptions | Default | Maximum / behavior |
|---|---|---|
| MaxNodes | 4096 | 65536; member overflow preserves the triggering fact then terminates |
| MaxDepth | 32 | 256; rejected parent knowledge does not revoke membership |
| MaxUnresolvedEvents | 128 | 65536 diagnostic records |
| MaxUnresolvedBytes | 4 MiB | 64 MiB; oversized record counts as dropped |
| MaxUnresolvedWait | 5 seconds | 1 minute, actively expired |
| History | empty | Caller-verified task/parent knowledge, bounded by MaxNodes; not replay or native resume confirmation |

Zero selects defaults; invalid active bounds fail before process launch. The
Session FIFO grows with available memory independently of diagnostic bounds.
Tool knowledge limits report a gap while keeping native facts. There is no
separate held-event FIFO, bounded derived fanout or message/creation/run ledger.

Actual permissions arrive on the same Receive path. Applications retain their
original handles and choose answers; there is no default decision, callback pool
or SDK approval timer. Native cancellation and termination invalidate handles
without waiting for a consumer. See [requests](session-requests.md).

A root result, idle snapshot or task terminal does not close Session or combine
parent/child usage. Receive cancellation ends one wait. Close stops admission,
invalidates owned requests and cleans up; accepted facts drain. Abort additionally
abandons unread FIFO entries. Ordinary Open with ResumeID passes the literal ID
to a fresh process without replay; requested and observed identities stay distinct.


The [capability matrix](subagent-compatibility.md) separates source, synthetic,
recorded and real evidence. No real child/model execution was performed for this
migration. Existing license/source pins and recording bytes remain unchanged.


## Pinned source references

- [official Hooks reference](https://code.claude.com/docs/en/hooks#subagentstart)
- [f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab, initialize forwarding](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/query.py)
- [official Python SystemMessage reference](https://code.claude.com/docs/en/agent-sdk/python#systemmessage)
