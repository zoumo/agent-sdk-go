# Ticket 02: observe and interrupt native turns

Verification date: 2026-10-17.

Tested SDK implementation commit: `9e4d0840e56a52d686ded8d95ea464a5efc961ed`.

Uncached normal and race tests, vet, inventory, and whitespace checks passed after
that commit. This following evidence-only change records the exact revision
without changing SDK source or tests.

This adds the second approved slice only. [Compatibility](../compatibility.md) is
the canonical implementation/evidence inventory. The exact official target remains
`d27764b82f7118f674371e6d6e76271d9d606edb`; no version range or live compatibility
is established.

## Implemented boundary

- Typed `StartTurn` and `InterruptTurn` use generated native parameters/results.
- Local subscriptions are active on return and have finite configurable admission
  and queue limits. No callbacks run on the reader and delivery never waits for a
  consumer. SDK-generated payload decoding does run on the reader.
- Every event preserves method and parameter JSON, including unknown methods,
  absent/null values, unknown union variants, and malformed supported payloads.
- Six native notification payloads are decoded: turn started/completed, item
  started/completed, agent message delta, and error. Malformed supported payloads
  have separate `*DecodeError` values without losing raw data.
- Subscribers own separate raw buffers and typed object graphs.
- `Done` and `Err` signal terminal status outside the event queue. Overflow reports
  `*SubscriptionOverflowError`; accepted events remain drainable. Other streams
  and RPC response routing continue. Normal close differs from connection loss
  and context cancellation.
- Cancellation is local observation/waiting only. Interruption is one explicit
  native request; its acknowledgement does not fabricate a completion event.
- No turn runner, native-ID synthesis, durable state, replay, or journal exists.

The pinned experimental-schema closure is now 99 official definitions, producing
236 Go types and 431 synthetic round-trip cases. The existing generator now also
handles annotation-only arbitrary-JSON fields such as `outputSchema`. Required
item lifecycle timestamps are preserved from this exact pin. Schema selection and
source checksums are recorded in [provenance](../protocol/README.md).

## Verification

All Go commands use mise-managed Go 1.26.9 with `GOTOOLCHAIN=local`:

```sh
mise exec go@1.26.9 -- go test -count=1 ./...
mise exec go@1.26.9 -- go test -race -count=1 ./...
mise exec go@1.26.9 -- go vet ./...
python3 docs/protocol/verify_inventory.py
git diff --check
```

Normal, race, vet, inventory, and whitespace checks pass. Five consecutive normal
runs also pass, and generation from the exact verified archive reproduces the
snapshot, Go types, and generated tests byte-for-byte. The compilable observe-turn
example is built by the suite but is not executed. Tests launch only the explicitly selected test helper,
never a real Codex executable or an authenticated/model session.

The external-package scenarios establish:

1. Eight early notifications, including a same-thread stale completion, another
   thread, another turn, and immediate current-turn completion, are read while the
   peer deliberately withholds the start response. Only a second RPC releases the
   response, whose native ID and status remain unchanged by those events.
2. Known/unknown/malformed payloads retain raw JSON and order. Decode failures do
   not close the stream; malformed or oversized envelopes do close it explicitly.
3. A full queue's error is observable before draining it. A paced healthy observer
   continues, and a separate 4,096-notification flood cannot block an ordinary RPC
   response. Subscriber capacity becomes available after termination.
4. Mutating raw bytes, typed fields, unknown fields, or an unknown item union for
   one subscriber does not mutate another subscriber's data.
5. No pre-registration replay, no automatic turn/start replay after RPC error,
   invalid response, or connection loss, and no implicit interrupt after call or
   subscription cancellation. Explicit interrupt parameters and count are exact.
6. Close, context deadlines/cancellation, full-queue cancellation, peer EOF, and
   malformed input produce distinguishable terminal status. Concurrent subscribe,
   cancel, consume, and close pass under the race detector.

[Testing-pattern references](../protocol/testing-patterns.md) explain how official
Rust, TypeScript, and Python tests informed independently authored fake-peer
coverage. Upstream suites were not run or copied.

## Remaining limits

Reverse-request answers, resume/unsubscribe wrappers, and remote transports belong
to later tickets. Turn workflows that require approvals, dynamic tools, or other
server requests still fail explicitly. Other notifications are raw-only. Envelope
emission timestamps are not exposed. Storage and decoding cost scale with event
size and subscription limits; worst-case maximum-frame fanout latency is not
benchmarked. No real-server or live-model validation was performed.
