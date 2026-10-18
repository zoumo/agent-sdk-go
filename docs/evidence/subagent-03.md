# Ticket 03 — observation lifecycle and history gaps

Baseline: main `9aca0c39a788b42ef7caa4d877f22fe30fee88d5` (ticket 02).
Protocol source: Codex 0.160.1 at
`d27764b82f7118f674371e6d6e76271d9d606edb`. No protocol regeneration inputs,
wire casing, authentication or other repositories were changed.

## Behavior

`TreeOptions.History` accepts caller-supplied native Thread object JSON snapshots.
It reads parent/source links and successful completed collab items in turns,
without issuing RPCs, replaying native events or importing execution state.
Relations established from snapshots have history provenance, reason history and
an empty evidence Method (a snapshot is not a notification). They only emit
Associated. Historical entities without a known parent also cannot become Created
when their parent is discovered live; that association carries its live evidence.
A new live child can still emit Created. Native event deduplication starts empty.

Snapshots are bounded by MaxNodes count and PendingBytes total bytes, with normal
graph/depth/queue limits during seeding. Input is checked for object/ID validity;
this lineage extractor is not a full native schema validator. Unknown/unusable
history remains covered by the explicit incomplete history-recovery-gap record.
Only IDs and relation evidence are retained, not the caller's snapshot buffers.

Close/cancel stop only the local observation and keep accepted records readable.
The existing shared server attachment is not owned by any TreeSubscription and
is never unsubscribed on observation teardown. Unexpected connection failure
preserves the original Err, closes Done/Events, and appends a disconnected state
with reason permanent-gap. One additional bounded queue slot is reserved for that
terminal diagnostic; no accepted event is evicted even if EventQueue is full.
Pending unresolved records are not reconstructed; the disconnect gap covers them.
Overflow that already terminated an observer retains its original overflow cause.

Parent and child states are independent. Parent idle does not close child
observation or mark child work complete. Root completion, child activity endings,
child turn completion/interruption, and agent entity lifetime are separate.
Native notifications and IDs are forwarded; no success/completion is synthesized.
While the parent is waiting in wait_agent, its turn remains active. Completing
that parent turn does not automatically call close_agent on children.

## Source evidence (E1)

The locally inspected upstream checkout `/tmp/codex-native-capabilities-01601`
was verified at the exact protocol revision above. In app-server
`bespoke_event_handling.rs:185–202`, TurnComplete first awaits
note_turn_completed, then handle_turn_complete. `thread_status.rs:156–158` clears
that thread's active state; lines 270–277 update only the keyed thread, and
loaded_thread_status at 446–468 derives status from that thread's runtime facts,
without aggregating children. The status notification and turn/completed use the
same outbound sender (`outgoing_message.rs`). An idle change, when applicable,
is enqueued before that thread's turn/completed. This is not a cross-thread
ordering guarantee, and does not promise that a successful result arrives first.
Links to the pinned source are in [compatibility](../compatibility.md).

New-thread best-effort auto-attach is separate from cold reconnect to existing
threads. Lagged thread-created notifications skip resync. No cursor/replay or
old-thread attachment guarantee is established by these source facts. All
runtimes, including unknown versions, retain an incomplete/unverified initial
state. Silence never establishes completion or completeness.

## Offline evidence (E2)

The original synthetic fixtures use the public SDK API over fake stdio, WS and
WSS peers. They cover parent idle/child active, post-parent child events,
activity completed/interrupted followed by later work, concurrent close/cancel,
two roots sharing one source plus an unaffected native subscription, historical
collab and source lineage, history versus new live creation, unlinked historical
entities, two explicit connection epochs receiving identical native IDs, EOF with
queued events, the reserved terminal slot at capacity, and invalid/oversize seeds.
RPC transcripts permit only explicitly requested fixture operations; there is no
automatic thread read/list/resume/start/unsubscribe or input/answer replay.

Final checks passed (exit 0) with mise Go 1.26.9 on Darwin arm64:

```sh
export RUN_MAC_STDIO_LIVE=0 RUN_MAC_INTERACTIONS_LIVE=0 RUN_MAC_WEBSOCKET_LIVE=0
export GOPROXY=off GOSUMDB=off
mise exec -- go test -v -count=1 ./...
mise exec -- go test -race -v -count=1 ./...
mise exec -- go vet ./...
mise exec -- go mod verify
python3 -m unittest discover -s scripts -p 'test_ci_checks.py' -v
python3 docs/protocol/verify_inventory.py
mise exec -- bash scripts/check_generated.sh
git diff --check
```

Local logs: `/tmp/ticket03-test-final.log`, `/tmp/ticket03-race-final.log`.
All three opt-in live tests explicitly SKIP. Six CI gate self-tests passed;
inventory remains 170/11/85/1, regeneration remains 397 types and 744 cases from
154 pinned definitions. Source/test SHA-256 values are recorded under
`subagent03-*` in [evidence.json](../protocol/evidence.json). Delivery SHA,
remote-SHA readback and hosted Linux/macOS CI are reported separately.

E3/E4 subagent validation was not run. Cold recovery remains incomplete: callers
explicitly create a new Client/SubscribeTree with a new epoch and optional seed.
No exactly-once, successful child result, complete historical status or seamless
recovery is claimed. Native IDs and raw bytes remain available to the caller.
