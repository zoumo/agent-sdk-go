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

## Optional child detail delivery

`SessionOptions.ChildDetailPolicy` (also on runtime `Options`) is fixed at `Open`,
including an explicit `ResumeID`. Empty and `ChildDetailPreserve` (`"preserve"`)
keep existing delivery. `ChildDetailSuppress` (`"suppress"`) filters only confirmed
child pure detail before admission to the same FIFO. Other values wrap
`ErrInvalidInput` before starting a process. `OptionsFromTypes` leaves the policy
empty and `Options.Types()` excludes this runtime setting.

```go
s, err := claude.Open(ctx, claude.SessionOptions{
    ChildDetailPolicy: claude.ChildDetailSuppress,
})
```

Membership and aliases still use CLI 2.1.258 `local_agent` task/tool evidence;
`agent_id` or arbitrary `parent_tool_use_id` alone cannot identify a child.
Complete parent knowledge is unnecessary. The native initialize still sends
`forwardSubagentText:true`; filtering neither disables source output nor changes
internal results-to-parent, history queries, token costs or the model configuration.

The [delivery contract](../../harness/contracts/child-detail-delivery.md) defines
this closed allowlist: assistant arrays containing only text/thinking/redacted
thinking, and indexed `content_block_delta` with text/thinking/signature/input JSON
deltas. Known message metadata and the four native token counters are accepted;
unreviewed usage extensions, content string forms and empty block arrays are
conservatively retained. Boundary events (including message stop), tool blocks,
parent tool results, original approval/question handles, task/relationship/status
facts, completion/failure and unknown/mixed fields stay intact, with original Raw
bytes and order. No frames are split and no filtered placeholder is generated.
Unknown versions/sources retain the existing delivery/quarantine behavior.

This reduces reviewed pure detail delivery, and is **not a privacy or complete
content removal guarantee**. Mixed assistant snapshots, system notifications and
future fields may still carry child text/thinking/tool content. A root result
still does not imply child/background completion. Synthetic public API F1–F5 and
native authority/write-barrier regressions verify filtering; real child forwarding
and parent consumption remain unverified, with no new recording or model call.
