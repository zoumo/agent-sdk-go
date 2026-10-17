# Ticket 04: answer server requests safely

Verification date: 2026-10-17.

Tested SDK implementation commit: `37679703719ae2c88713b37e3eeb2ec118a3d1ac`.

The final source and test tree passed the full uncached normal/race checks and
five normal repetitions, then was committed unchanged. Normal tests, vet, and
exact-source inventory passed again after that commit. This following evidence-only
change records its exact identifier without changing SDK source or tests.

This slice uses only independent mock children and unit/race tests. The official
source target remains `d27764b82f7118f674371e6d6e76271d9d606edb`. No real Codex
executable, account, credential, live model, or upstream test suite was used.
[Compatibility](../compatibility.md) is the canonical full surface inventory.

## Implemented boundary

- `Client.Requests()` is a single bounded pull queue. Immutable request accessors
  preserve method, absent/null/raw parameters, exact integer/string RPC identity,
  and the original connection-bound answer handle. Payload access returns copies.
- Modern command/file/permissions approvals, tool user input, and two legacy
  approvals expose the complete pinned native parameter/result definitions.
  Malformed supported payloads retain raw bytes and return `DecodeError`; unknown
  methods have a raw response path without endpoint workflow certification.
- `Answer` and `Reject` send only caller-selected results/errors. Copied handles
  share one one-shot state. No automatic approval, denial, policy, or replay exists.
- Admission is the race winner boundary. Competing answers, local cancel/expiry,
  native resolution, and Close are synchronized. Once admitted, an answer consumes
  the handle even if a later context cancellation is still `NotSent`. A successful
  write is not a server acknowledgment. Partial/ambiguous writes return
  `AnswerError` with `OutcomeUnknown`, prevent replay, and release blocked writers.
- Encoding, already-canceled context, frame-size, and outbound-admission failures
  leave an unanswered handle safely usable. Caller marshalers run off the reader
  and outside the lock; Close does not wait for them or for consumers.
- Queue and active-request limits are separately configurable and finite. Capacity
  exhaustion explicitly fails the connection with `ErrRequestOverflow`/`ErrLimit`
  rather than blocking the reader, silently dropping a request, or guessing an
  approval response. Duplicate active peer request IDs are protocol errors.
- Local cancel and expiry abandon a handle without transmitting a cancellation or
  claiming server work stopped. Closing the connection closes the request queue,
  invalidates all pending handles, and leaves buffered terminal requests drainable.

## Pinned native resolution

`serverRequest/resolved` carries required `threadId` and `requestId`. The SDK
matches both native thread identity and the exact tagged original request ID.
Wrong/malformed identities do not invalidate an unrelated handle. The notification
is also observable as a raw/typed event; malformed known payloads retain a separate
decode error. Resolution cannot retract an answer already admitted locally.

Official source emits this signal after a reply or during pending-request
resolution on turn interruption. Official tests expect it before `turn/completed`.
It is neither a generic cancellation RPC nor proof of successful execution.
Legacy `conversationId` is itself the native `ThreadId` and can be correlated if a
resolution arrives, but the pinned modern production handlers do not establish
legacy request emission. See [source provenance](../protocol/README.md) and
[official testing patterns](../protocol/testing-patterns.md).

The selected schema closure now has 33 roots, 154 exact upstream definitions,
397 generated types, and 744 synthetic round-trip cases. Earlier selected
schema definitions and generated declarations remain unchanged. Generator
extensions support signed integer unions, combined string decision alternatives,
and native `kind` discriminators without changing existing type shapes.

## Verification

All Go commands use mise-managed Go 1.26.9 with `GOTOOLCHAIN=local`:

```sh
mise exec go@1.26.9 -- go test -count=1 ./...
mise exec go@1.26.9 -- go test -race -count=1 ./...
mise exec go@1.26.9 -- go test -count=5 ./...
mise exec go@1.26.9 -- go vet ./...
python3 docs/protocol/verify_inventory.py
git diff --check
```

All commands pass, including five consecutive normal runs, the uncached full
race suite (104.562 seconds for the exported-client package), standalone normal
and race model suites, vet, exact-source inventory, and whitespace checks.
Generation reproduces all three selected/generated files byte-for-byte from the
verified upstream archive. Two read-only reviews found no correlation, one-shot,
cancellation, or cleanup defect. Examples compile but are not executed.

## Public-boundary scenarios

The 26 independent external-package request tests check complete wire transcripts:

1. Equal-text numeric/string IDs, integers above float64 precision, and matching
   IDs in opposite protocol directions stay distinct. Result/error envelopes have
   exactly their native members and never add a `jsonrpc` field.
2. All six typed request/response pairs round-trip. Raw unknown, absent/null,
   malformed known payloads, and separately mutable decoded/raw copies preserve
   native data without changing the answer target.
3. No consumer can block ordinary calls/events. Queue and active limits fail
   explicitly; duplicate active IDs and malformed envelopes fail without guessed
   replies. Startup requests remain available after initialization, and startup
   overflow cleans up the owned child.
4. Safe preflight failures are reusable and emit no bytes. A cooperatively blocked
   writer proves outbound-queue rejection does not consume the losing handle.
   Admitted-but-unwritten context cancellation is `NotSent` but terminal; release
   of the blocked writer never sends that canceled response later.
5. Duplicate answers, local cancel/expiry, close, old connections, and same-ID
   reuse cannot revive an old handle. Buffered expired requests remain observable
   after Close without becoming answerable.
6. Exact thread/request identity drives native resolution; unrelated/malformed
   notifications stay observable without affecting the handle. Legacy identity
   uses conversationId. Unknown or malformed payloads never trigger a policy guess.
7. Thirty-two concurrent answers/rejections produce one reply. Repeated concurrent
   Answer/Cancel and Answer/native-resolution races have one winner and at most
   one emitted response for each original ID.
8. Confirmed partial writes followed by context cancellation, write timeout, or
   Close are outcome-unknown and never replayed. A frozen caller marshaler cannot
   block reader progress or Close, and its late return cannot write a stale answer.
9. Invalid limits, zero handles, closed consumers, and pending handle cleanup are
   exercised through exported APIs. No test substitutes private dispatcher access
   for wire behavior.

## Limits

No remote transport is included in this ticket. No real-server compatibility,
legacy production emission, security-policy interpretation, or durable approval
storage is established. Unknown endpoint workflows remain caller-managed. A
server can keep working after local abandonment; use an explicit native operation
when execution control is intended. Terminal requests can remain in a bounded
queue until consumed, so consumers must observe handle status and stale UI actions
must handle local failure. Go cannot stop noncooperative application code.
