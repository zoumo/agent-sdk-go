# Ticket 04 — bounded admission and original request authority

Baseline: main `94b7ba6cceec55ca8a56493313d9fbae58988fcd` (ticket 03).
Protocol remains the checked-in Codex 0.160.1 schema. Native protocol types,
Raw envelopes, request handling, execution and authentication are unchanged.

## Behavior and bounds

Each live notification stages its derived associations and any resolved pending
records in a batch bounded by EventQueue. Admission succeeds for the complete
batch or terminates that local observation with SubscriptionOverflowError.
Already accepted records remain readable and Done/Err do not require queue space.
Pending-expiry diagnostics and their unresolved Native records use the same rule.
A temporary batch has at most EventQueue entries; it is released on commit or
termination. This makes fanout atomicity independent of consumer scheduling.
The reserved disconnect slot remains separate and carries permanent-gap.
An overflowing EventQueue plus reserved slot is rejected, including inherited
Client limits, before channel allocation.

Existing independently configurable defaults remain: EventQueue inherits
Limits.EventQueue (128 by default), MaxNodes 4096 including root/unresolved nodes,
MaxDepth 32 edges, PendingEvents 128, PendingBytes 4 MiB of stored envelope plus
params, PendingAge 5 seconds, DedupEntries 8192. Zero selects defaults; negative
values fail admission. Graph/depth exhaustion terminates only the observer with
ErrLimit. Pending count/byte/age limits release unresolved records with explicit
incomplete diagnostics; if those records cannot fit, queue overflow terminates
that observer. Identity-cache eviction permits old replays; identical deltas
without occurrence IDs are never deduplicated. Closing clears pending/batch,
relations and dedup storage and stops its one association timer.

The reader never waits on an event consumer. Control replies and Requests use
their existing independent paths. The original connection-bound Request handle
is the sole answering authority. Observation cancellation/Close leaves it usable;
request Cancel, timeout, native resolution and Client.Close retain existing
failure semantics. A stale handle cannot target the same request ID on a new
Client/epoch. No automatic approval, response, resume, replay or historical
Created is introduced. Parent text, child text/thinking and each native usage
payload retain source identities and exact Raw. UsageScope is unknown and no
parent/child aggregation or final-answer synthesis is performed.

## Offline validation (E2)

Original synthetic public-API fixtures run over fake stdio, loopback WS and WSS:

- One-slot-short association fanout, multi-receiver spawn and newly resolved
  pending delivery admit no partial batch; earlier diagnostics remain drainable.
- Pending byte and deadline exhaustion preserve diagnostic/Native atomicity.
- A completely full tree queue still permits child command approval and user
  question delivery, control RPC replies and one-shot answers from 12 concurrent
  callers. Another tree and the raw subscriber continue receiving events.
- Concurrent context cancellation, repeated Close and publication preserve the
  original outstanding request authority and produce exactly one response.
- Cancel/expiry/native resolution/Client.Close reject late answers; reused IDs on
  new Clients have fresh epochs and independent handles.
- Repeated identical child deltas, child thinking, parent/child usage and the
  typed parent final answer preserve raw bytes, native IDs and source scope.
- Explicit/inherited integer-overflow capacities fail cleanly. Existing tree
  contracts cover graph nodes/depth, pending count/bytes/age, cache eviction,
  history, disconnect, and other-observer isolation.

Validation commands use the existing mise Go 1.26.9 on Darwin arm64:

```sh
export RUN_MAC_STDIO_LIVE=0 RUN_MAC_INTERACTIONS_LIVE=0 RUN_MAC_WEBSOCKET_LIVE=0
export GOPROXY=off GOSUMDB=off
mise exec -- go test -v -count=1 ./...
mise exec -- go test -race -v -count=1 ./...
mise exec -- go vet ./...
mise exec -- go mod download
mise exec -- go mod verify
python3 -m unittest discover -s scripts -p 'test_ci_checks.py' -v
python3 docs/protocol/verify_inventory.py
mise exec -- bash scripts/check_generated.sh
git diff --check
```

All checks passed (exit 0). The SDK normal suite completed in 54.723 seconds and
the full race suite in 236.165 seconds. Six CI gate self-tests passed; inventory
remains 170/11/85/1 and generation remains 397 types/744 cases/154 definitions.
After completing the thinking/usage fixture with pinned native fields, the full
normal suite and a focused race rerun of source preservation/capacity tests passed.
The race records distinguish the earlier fixture hash from that final revision.

The final source/test hashes and E2 outcomes are recorded under `subagent04-*`
in [evidence.json](../protocol/evidence.json). Local validation logs are
`/Users/jim/Documents/Codex/2026-10-18/task-20/ticket04-*.log`. The initial targeted
run failed because its newly authored final-answer fixture omitted required
completedAtMs; the corrected native fixture passed. No production protocol
relaxation was made. Delivery SHA, remote readback and exact-commit hosted
Linux/macOS CI are reported separately.

All live/model tests remain explicitly skipped. No E3/E4 evidence is claimed.
Best-effort auto-attach remains runtime-unverified; old thread lifecycles are not
automatically resumed, disconnect remains a permanent gap, and history cannot
prove real-time creation or seamless recovery.
