# Ticket 03: resume and unsubscribe native threads

Verification date: 2026-10-17.

Tested SDK implementation commit: `2ba56049bf8c37c4501b6335ee419a23552c0f95`.

Uncached normal/race tests, five consecutive normal runs, standalone model tests,
vet, exact-source inventory, and whitespace checks passed after that commit. This
following evidence-only commit records its identifier; SDK source and tests are
unchanged.

This is mock-only evidence for the third approved slice. The official target is
`d27764b82f7118f674371e6d6e76271d9d606edb`, with no version-range or live-server
claim. [Compatibility](../compatibility.md) remains the canonical complete
capability inventory; historical ticket records retain their own revisions.

## Implemented boundary

- Typed `ResumeThread` and `UnsubscribeThread` expose the complete pinned
  experimental-schema native parameter/result definitions.
- Resume sends the supplied native ID unchanged and accepts only the same
  returned ID. Mismatch returns `ErrProtocol`, `Responded`, and the matching RPC
  correlation ID, without exposing a replacement result or closing the connection.
- Native history/path selectors can return another identity. This typed wrapper
  deliberately enforces the stricter supplied-ID contract even for those
  selectors. It does not reverse server work after a rejected result. Those
  alternate-identity workflows remain caller-managed through raw `Call`.
- `ServiceTier` preserves omitted/explicit-null/string states. `ExcludeTurns`
  preserves omitted/false/true and rejects null. Native defaults are not inserted.
- Unsubscribe returns the native status or error. It neither closes local
  observers nor sends `turn/interrupt`. Closing an observer does not unsubscribe.
- Every call inherits the existing dispatch evidence and finite limits. There is
  no implicit start, reconnect, retry, replay, history reconstruction, or journal.

The selected closure now contains 117 upstream definitions, generating 316 Go
types and 581 synthetic round-trip cases. Every prior selected definition and
existing generated type declaration remains unchanged. Source checksums and the
exact 20-root selection are in [provenance](../protocol/README.md).

## Verification

All Go commands use mise-managed Go 1.26.9 with `GOTOOLCHAIN=local`:

```sh
mise exec go@1.26.9 -- go test -count=1 ./...
mise exec go@1.26.9 -- go test -race -count=1 ./...
mise exec go@1.26.9 -- go vet ./...
python3 docs/protocol/verify_inventory.py
git diff --check
```

Normal and race suites, vet, inventory, and whitespace checks pass. Repeated
normal runs also pass. Generation reproduces the selected snapshot, Go types,
and generated model tests byte-for-byte from the exact verified archive. The
inventory was checked against all exact pinned source files. Both examples are
compiled, never executed. No real Codex executable or model session was launched.

The independently authored external-package tests establish:

1. Two independent owned-child connections each initialize explicitly. Only a
   peer-created native string crosses between clients; resume/read preserves its
   whitespace, Unicode, session ID, large integer metadata, and optional results.
2. Strict mismatch failure retains the matching RPC ID and allows a subsequent
   unrelated RPC. History/path fields remain unchanged on the wire, while an
   alternate returned identity is rejected. No fallback creation occurs.
3. Missing/expired and internal RPC errors preserve native error codes and data.
   Invalid typed results are responded failures; malformed envelopes and EOF
   after dispatch are outcome-unknown. Complete method transcripts show no replay.
4. Pre-canceled contexts and local validation/admission failures are not-sent.
   Confirmed receipt precedes cancellation, deadline, or close in unknown-outcome
   cases. Late replies cannot satisfy the following call. Dead/closed connections
   fail locally without an implicit replacement connection.
5. All three native unsubscribe statuses, future status strings, and unknown
   fields survive. Existing local observers continue receiving unrelated events;
   local Close does not send unsubscribe or interruption.
6. Null non-nullable parameters, oversized frames, and exhausted pending-call
   capacity fail without sending either native method. Concurrent resume,
   unsubscribe, and Close pass under the race detector.

[Official test-pattern notes](../protocol/testing-patterns.md) describe the Rust
resume/unsubscribe cases, TypeScript exact-argument tests, and Python lifecycle
identity checks that informed original Go coverage. Those upstream suites were
read, not run or copied.

## Remaining limits

The fake peers are scripted and independent: these results establish client wire
identity and lifecycle behavior, not actual server durable persistence, history
recovery, ephemeral-thread resumability, or active-turn continuation after detach.
Reverse-request answers and remote transports remain later tickets. Native
alternate-identity history/path workflows are outside `ResumeThread`'s strict
contract. Experimental fields can have server capability gates. No live validation
was performed.
