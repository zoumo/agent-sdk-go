# Claude Session migration (T03)

This implementation follows [Session design](../../harness/modules/native-session/design.md)
and [delivery contract](../../harness/contracts/event-delivery.md). This is an
implementation/evidence note, not a claim that every Session operation is done.

`Open(ctx, SessionOptions)` owns one process interaction and one `Received` FIFO.
The reader admits known same-session native envelopes directly. `Receive` does
not run relationship attribution, wait for ancestry, deduplicate UUIDs or sort
unknown run IDs. `Submission.Written` proves only a complete local write.
The native user envelope remains a main-session input; it exposes no arbitrary
target selection. A result does not stop reception or future submission.

The fixed declaration evidence is in [type sources](type-sources.md), especially
SDK 0.3.258 / CLI 2.1.258. `task_started` with explicit `local_agent`, `task_id`
and `tool_use_id` establishes a native local task/tool association only after
an exact `claude_code_version: 2.1.258` init. Missing/other versions report a
relationship coverage gap; same-session facts still deliver without these claims. A forwarded
message/stream's `parent_tool_use_id` may use that already established alias.
Message `agent_id` is retained as an **unverified extension**, never equated to
`task_id`. `parent_task_id` and run_id extensions remain raw; they do not establish
parentage or current/stale order. Parent edges come from observed tool ownership
or caller-verified history, and use the shared graph's cycle/conflict/depth
validation. Parent rejection never revokes session membership or discards the
native fact. Unknown identity/parent extensions report observation gaps.

Unknown envelopes, pre-identity material and different-session frames stay in
the owned connection's quarantine, accessible through `Session.Unattributed()`.
Defaults are 128 records, 4 MiB and 5 seconds. Existing `Subagents` unresolved
limits configure these bounds during migration. Oldest material is evicted to
respect count/byte bounds; an oversized record is counted as dropped. A single
active timer expires retained material even without further reads or traffic.
Snapshots are read-only and report retained bytes, limits, received timestamps, reasons, dropped
and expired counts, and a sticky coverage gap. Snapshots copy native values;
they cannot mint answer handles. Later membership never backfills old frames.

`Close` stops admission and cleans up without waiting for consumption. Accepted
facts drain to EOF or the stable first termination cause. `Abort` additionally
releases the Session's FIFO.
The FIFO is limited by available memory, not these quarantine limits. Task,
tool and alias indexes are bounded separately; no UUID/message ledger can end
a main-only long conversation. A task-index limit preserves the triggering fact
before terminating; tool/parent-knowledge limits report gaps without terminating.

The old public Client/NewClient, callback option, tree consumer and independent
history stream are removed. Native process interaction is private and always
bound to its Session. Permission workers and old dedup/run/creation/fanout resource
options are removed. Use active membership, parent-knowledge and diagnostic
retention limits above. Ordinary Open with ResumeID retains native no-replay
resume in a fresh process; requested identity does not become observed merely
because initialize succeeds.

Validation is offline only. `session_test.go` uses the existing synthetic native
process peer and public Open/Receive/Submit/Interrupt/Close/Abort methods. It
covers raw arrival order, duplicate preservation, unknown scope, task/tool aliases,
late parent knowledge, graph conflicts/cycles/depth, 8,300 main messages, local
wait cancellation, expiry, shutdown and fault drain. No real CLI/model was run,
no new recording was made, and synthetic behavior is not live compatibility proof.
